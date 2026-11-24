package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/awangga/rprompt/internal/claude"
	"github.com/awangga/rprompt/internal/toolcall"
)

// fakeTools adalah engine CLI yang mendukung tool.
type fakeTools struct {
	fakeRunner
	reply string
	err   error
	got   toolcall.Request
	calls int
}

func (f *fakeTools) RunTools(_ context.Context, req toolcall.Request) (toolcall.Reply, error) {
	f.got = req
	f.calls++
	return toolcall.Reply{Text: f.reply, InTokens: 30, OutTokens: 5}, f.err
}

// fakeToolAPI sama, untuk engine yang memenuhi APIRunner.
type fakeToolAPI struct {
	fakeAgy
	reply string
	got   toolcall.Request
}

func (f *fakeToolAPI) RunTools(_ context.Context, req toolcall.Request) (toolcall.Reply, error) {
	f.got = req
	return toolcall.Reply{Text: f.reply}, nil
}

const toolsJSON = `"tools":[{"type":"function","function":{"name":"read","description":"Baca","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}},{"type":"web_search"}]`

const callReply = "Saya baca.\n<use_tool name=\"read\">\n{\"path\": \"a.txt\"}\n</use_tool>"

func TestToolsClaude(t *testing.T) {
	f := &fakeTools{reply: callReply}
	rr := doChat(newAPITestServer(f), http.MethodPost, "Bearer rahasia", `{"model":"opus","reasoning_effort":"low",`+toolsJSON+`,
		"messages":[{"role":"system","content":"SIS"},{"role":"user","content":"baca a"},
		{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":\"b\"}"}}]},
		{"role":"tool","tool_call_id":"c1","content":"isi b"}]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
	var resp chatCompletionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	c := resp.Choices[0]
	if c.FinishReason != "tool_calls" || c.Message.Content != "Saya baca." || len(c.Message.ToolCalls) != 1 {
		t.Fatalf("balasan = %s", rr.Body)
	}
	tc := c.Message.ToolCalls[0]
	if tc.ID == "" || tc.Type != "function" || tc.Function.Name != "read" || tc.Function.Arguments != `{"path":"a.txt"}` || tc.Index != nil {
		t.Errorf("tool call = %+v", tc)
	}
	if resp.Usage.TotalTokens != 35 || f.got.Model != "opus" || f.got.Effort != "low" || f.gotPrompt != "" {
		t.Errorf("usage=%+v req=%+v", resp.Usage, f.got)
	}
	for _, want := range []string{`<tool_result name="read">`, "isi b", `{"path":"b"}`} {
		if !strings.Contains(f.got.Prompt, want) {
			t.Errorf("prompt tanpa %q: %s", want, f.got.Prompt)
		}
	}
}

func TestToolsStreaming(t *testing.T) {
	f := &fakeTools{reply: callReply + "\n" + strings.Replace(callReply[11:], "a.txt", "b.txt", 1)}
	rr := doChat(newAPITestServer(f), http.MethodPost, "Bearer rahasia", `{"stream":true,"stream_options":{"include_usage":true},`+toolsJSON+`,"messages":[{"role":"user","content":"x"}]}`)
	var got []string
	var content, finish string
	var total int
	for _, l := range strings.Split(rr.Body.String(), "\n") {
		d, ok := strings.CutPrefix(l, "data: ")
		if !ok || d == "[DONE]" {
			continue
		}
		var ch chatChunk
		if err := json.Unmarshal([]byte(d), &ch); err != nil {
			t.Fatal(err)
		}
		if ch.Usage != nil {
			total = ch.Usage.TotalTokens
		}
		for _, c := range ch.Choices {
			content += c.Delta.Content
			for _, tc := range c.Delta.ToolCalls {
				if tc.Index == nil || *tc.Index != len(got) {
					t.Errorf("index = %v", tc.Index)
				}
				got = append(got, tc.Function.Arguments)
			}
			if c.FinishReason != nil {
				finish = *c.FinishReason
			}
		}
	}
	if content != "Saya baca." || finish != "tool_calls" || total != 35 || strings.Join(got, "") != `{"path":"a.txt"}{"path":"b.txt"}` {
		t.Errorf("content=%q finish=%q total=%d calls=%q", content, finish, total, got)
	}
}

func TestToolsRouting(t *testing.T) {
	agy, cdx := &fakeToolAPI{reply: callReply}, &fakeToolAPI{reply: "halo"}
	f := &fakeTools{}
	s := newAPITestServer(f)
	s.agy, s.codex, s.kiro = agy, cdx, &fakeKiro{}
	body := func(model string) string {
		return `{"model":"` + model + `",` + toolsJSON + `,"messages":[{"role":"user","content":"x"}]}`
	}
	for model, want := range map[string]int{
		"gemini-2.5-flash": 200, "agy/claude-x": 200, "codex": 200, "codex/gpt-x": 200, "gpt-4o": 200,
		"kiro": 400, "bob": 400, "codex/--yolo": 400, "agy/ -x": 400, "--yolo": 400,
	} {
		rr := doChat(s, http.MethodPost, "Bearer rahasia", body(model))
		if rr.Code != want {
			t.Errorf("%s: %d %s", model, rr.Code, rr.Body)
		}
	}
	if agy.got.Model == "" || f.calls != 1 || f.got.Model != "" {
		t.Errorf("agy=%q claude=%d/%q", agy.got.Model, f.calls, f.got.Model)
	}
}

func TestToolsChoice(t *testing.T) {
	f := &fakeTools{reply: callReply}
	s := newAPITestServer(f)
	do := func(choice string) (int, string) {
		rr := doChat(s, http.MethodPost, "Bearer rahasia", `{`+toolsJSON+`,"tool_choice":`+choice+`,"messages":[{"role":"user","content":"x"}]}`)
		return rr.Code, rr.Body.String()
	}
	if code, body := do(`"none"`); code != 200 || strings.Contains(body, "tool_calls") || strings.Contains(f.got.System, "Available tools") {
		t.Errorf("none: %d %s", code, body)
	}
	if code, _ := do(`{"type":"function","function":{"name":"read"}}`); code != 200 || !strings.Contains(f.got.System, "must call") {
		t.Errorf("nama: %d", code)
	}
	for _, bad := range []string{`{"type":"function","function":{"name":"tidakada"}}`, `"ngawur"`, `7`} {
		if code, _ := do(bad); code != 400 {
			t.Errorf("%s: %d", bad, code)
		}
	}
}

func TestToolsError(t *testing.T) {
	f := &fakeTools{err: errors.New("batas pemakaian")}
	rr := doChat(newAPITestServer(f), http.MethodPost, "Bearer rahasia", `{`+toolsJSON+`,"messages":[{"role":"user","content":"x"}]}`)
	if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "batas pemakaian") {
		t.Errorf("%d %s", rr.Code, rr.Body)
	}
}

// Riwayat tool tanpa definisi (klien sedang meringkas) tidak boleh jatuh ke jalur
// biasa, yang menjalankan CLI dengan tool bawaannya.
func TestToolHistoryTanpaTools(t *testing.T) {
	f := &fakeTools{reply: "ringkasan"}
	f.result = claude.Result{Text: "SALAH"}
	rr := doChat(newAPITestServer(f), http.MethodPost, "Bearer rahasia", `{"messages":[{"role":"user","content":"x"},
		{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":"isi"}]}`)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "ringkasan") {
		t.Errorf("%d %s", rr.Code, rr.Body)
	}
}

var ollamaToolReply = []string{
	`{"message":{"content":"Saya baca."},"done":false}`,
	`{"message":{"content":"","tool_calls":[{"id":"x","function":{"index":0,"name":"read","arguments":{"path":"a"}}}]},"done":false}`,
	`{"message":{"content":""},"done":true,"done_reason":"stop","prompt_eval_count":20,"eval_count":9}`,
}

func TestToolsOllama(t *testing.T) {
	f := &fakeOllamaServer{lines: ollamaToolReply}
	s := newOllamaTestServer(t, f)
	msgs := `"messages":[{"role":"user","content":"baca"},
		{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":\"b\"}"}},{"id":"c2","type":"function","function":{"name":"read","arguments":"rusak"}}]},
		{"role":"tool","tool_call_id":"c1","content":"isi b"},{"role":"tool","tool_call_id":"c2","content":"galat"}]`
	rr := doChat(s, http.MethodPost, "Bearer rahasia", `{"model":"ollama/q",`+toolsJSON+`,`+msgs+`}`)
	var resp chatCompletionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	c := resp.Choices[0]
	if c.FinishReason != "tool_calls" || len(c.Message.ToolCalls) != 1 || c.Message.ToolCalls[0].Function.Arguments != `{"path":"a"}` {
		t.Errorf("balasan = %s", rr.Body)
	}
	got := f.got[0]
	if len(got.Tools) != 1 || !strings.Contains(string(got.Tools[0]), `"name":"read"`) {
		t.Errorf("tools = %s", got.Tools)
	}
	m := got.Messages
	if len(m) != 4 || len(m[1].ToolCalls) != 2 || string(m[1].ToolCalls[0].Function.Arguments) != `{"path":"b"}` ||
		string(m[1].ToolCalls[1].Function.Arguments) != `{"arguments":"rusak"}` ||
		m[2].Role != "tool" || m[2].ToolName != "read" || m[2].Content != "isi b" || m[3].Content != "galat" {
		t.Errorf("messages = %+v", m)
	}

	rr = doChat(s, http.MethodPost, "Bearer rahasia", `{"model":"ollama/q","stream":true,`+toolsJSON+`,`+msgs+`}`)
	for _, want := range []string{`"tool_calls":[{"index":0,`, `"finish_reason":"tool_calls"`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("stream tanpa %q: %s", want, rr.Body)
		}
	}
}

func TestResponsesTools(t *testing.T) {
	f := &fakeOllamaServer{lines: ollamaToolReply}
	s := newOllamaTestServer(t, f)
	body := `{"model":"ollama/q","stream":%s,
		"tools":[{"type":"function","name":"read","strict":false,"parameters":{"type":"object"}},{"type":"namespace","name":"x"},{"type":"custom","name":"apply_patch"}],
		"input":[{"role":"user","content":"baca"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]},
		{"type":"function_call","call_id":"c1","name":"read","arguments":"{}"},{"type":"function_call","call_id":"c2","name":"read","arguments":"{}"},
		{"type":"function_call_output","call_id":"c1","output":"isi"},{"type":"function_call_output","call_id":"c2","output":[{"type":"input_text","text":"isi2"}]}]}`
	rr := doResponses(s, strings.Replace(body, "%s", "false", 1))
	var resp responseObject
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || len(resp.Output) != 2 {
		t.Fatalf("%v %s", err, rr.Body)
	}
	o := resp.Output[1]
	if o.Type != "function_call" || o.Name != "read" || o.CallID == "" || o.Arguments == nil || *o.Arguments != `{"path":"a"}` {
		t.Errorf("output = %s", rr.Body)
	}
	m := f.got[0].Messages
	if len(m) != 4 || m[1].Content != "ok" || len(m[1].ToolCalls) != 2 || m[2].Content != "isi" || m[3].Content != "isi2" || m[3].ToolName != "read" {
		t.Errorf("messages = %+v", m)
	}
	if len(f.got[0].Tools) != 1 {
		t.Errorf("tools = %s", f.got[0].Tools)
	}
	out := doResponses(s, strings.Replace(body, "%s", "true", 1)).Body.String()
	for _, want := range []string{"response.function_call_arguments.delta", "response.function_call_arguments.done", "event: response.output_item.done", `"call_id":"call_`} {
		if !strings.Contains(out, want) {
			t.Errorf("stream tanpa %q: %s", want, out)
		}
	}
}
