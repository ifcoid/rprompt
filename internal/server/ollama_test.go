package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/awangga/rprompt/internal/ollama"
)

// fakeOllamaServer meniru /api/chat & /api/tags Ollama; mencatat request.
type fakeOllamaServer struct {
	got   []ollama.ChatRequest
	lines []string // NDJSON balasan /api/chat
	code  int      // status non-200 (opsional)
	body  string   // body untuk status non-200
}

func newOllamaTestServer(t *testing.T, f *fakeOllamaServer) *Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			fmt.Fprint(w, `{"models":[{"name":"qwen3.5:9b","capabilities":["completion"]},{"name":"bge-m3:latest","capabilities":["embedding"]}]}`)
			return
		}
		var req ollama.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("body tidak valid: %v", err)
		}
		f.got = append(f.got, req)
		if f.code != 0 {
			w.WriteHeader(f.code)
			fmt.Fprint(w, f.body)
			return
		}
		for _, l := range f.lines {
			fmt.Fprintln(w, l)
		}
	}))
	t.Cleanup(ts.Close)
	s := newAPITestServer(&fakeRunner{})
	s.ollama = ollama.New(ts.URL)
	return s
}

var ollamaReply = []string{
	`{"message":{"content":"","thinking":"mikir"},"done":false}`,
	`{"message":{"content":"Nama saya"},"done":false}`,
	`{"message":{"content":" Budi."},"done":false}`,
	`{"message":{"content":""},"done":true,"done_reason":"stop","prompt_eval_count":20,"eval_count":9}`,
}

func doResponses(s *Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer rahasia")
	rr := httptest.NewRecorder()
	s.handleResponses(rr, req)
	return rr
}

func TestChatOllamaNormalizesAndMapsParams(t *testing.T) {
	f := &fakeOllamaServer{lines: ollamaReply}
	s := newOllamaTestServer(t, f)
	rr := doChat(s, http.MethodPost, "Bearer rahasia", `{
		"model":"ollama/qwen3.5:9b","reasoning_effort":"none","temperature":0.2,"max_tokens":64,
		"messages":[{"role":"user","content":"Siapa namamu?"},{"role":"system","content":"Namamu Budi."}]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body)
	}

	got := f.got[0]
	wantMsgs := []ollama.Message{{Role: "system", Content: "Namamu Budi."}, {Role: "user", Content: "Siapa namamu?"}}
	if got.Model != "qwen3.5:9b" || !reflect.DeepEqual(got.Messages, wantMsgs) {
		t.Errorf("request ke Ollama = %+v", got)
	}
	if got.Think != false {
		t.Errorf("effort none harus think=false, dapat %v", got.Think)
	}
	if got.Options["temperature"] != 0.2 || got.Options["num_predict"] != float64(64) {
		t.Errorf("options = %v", got.Options)
	}

	var resp chatCompletionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	m := resp.Choices[0].Message
	if m.Content != "Nama saya Budi." || m.ReasoningContent != "mikir" || resp.Model != "ollama/qwen3.5:9b" {
		t.Errorf("response = %+v", resp)
	}
	if resp.Usage != (chatUsage{PromptTokens: 20, CompletionTokens: 9, TotalTokens: 29}) {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestChatOllamaStreaming(t *testing.T) {
	f := &fakeOllamaServer{lines: []string{
		`{"message":{"content":"","thinking":"hm"},"done":false}`,
		`{"message":{"content":"Hai"},"done":false}`,
		`{"message":{"content":""},"done":true,"done_reason":"length"}`,
	}}
	s := newOllamaTestServer(t, f)
	rr := doChat(s, http.MethodPost, "Bearer rahasia",
		`{"model":"ollama/qwen3.5:9b","stream":true,"reasoning":{"effort":"high"},"messages":[{"role":"user","content":"x"}]}`)
	body := rr.Body.String()
	for _, want := range []string{`"reasoning_content":"hm"`, `"content":"Hai"`, `"finish_reason":"length"`, "data: [DONE]"} {
		if !strings.Contains(body, want) {
			t.Errorf("stream tidak memuat %s:\n%s", want, body)
		}
	}
	if f.got[0].Think != "high" {
		t.Errorf("think = %v", f.got[0].Think)
	}
}

func TestChatOllamaErrors(t *testing.T) {
	s := newAPITestServer(&fakeRunner{})
	rr := doChat(s, http.MethodPost, "Bearer rahasia", `{"model":"ollama/x","messages":[{"role":"user","content":"hi"}]}`)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "OLLAMA_ENABLED") {
		t.Errorf("nonaktif: %d %s", rr.Code, rr.Body)
	}

	f := &fakeOllamaServer{code: http.StatusNotFound, body: `{"error":"model 'x' not found"}`}
	s = newOllamaTestServer(t, f)
	rr = doChat(s, http.MethodPost, "Bearer rahasia", `{"model":"ollama/x","messages":[{"role":"user","content":"hi"}]}`)
	if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), "model 'x' not found") {
		t.Errorf("404: %d %s", rr.Code, rr.Body)
	}

	for _, body := range []string{
		`{"model":"ollama/x","reasoning_effort":"turbo","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"ollama/","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"ollama/x","messages":[{"role":"user","content":"  "}]}`,
		`{"model":"ollama/x","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://a/b.png"}}]}]}`,
	} {
		if rr := doChat(s, http.MethodPost, "Bearer rahasia", body); rr.Code != http.StatusBadRequest {
			t.Errorf("harus 400 untuk %s, dapat %d %s", body, rr.Code, rr.Body)
		}
	}
}

func TestResponsesOllama(t *testing.T) {
	f := &fakeOllamaServer{lines: ollamaReply}
	s := newOllamaTestServer(t, f)
	rr := doResponses(s, `{
		"model":"ollama/qwen3.5:9b","instructions":"Jawab singkat.","reasoning":{"effort":"low"},
		"max_output_tokens":100,
		"input":[
			{"role":"user","content":"Siapa namamu?"},
			{"role":"system","content":"Namamu Budi."},
			{"type":"reasoning","summary":[]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Lihat"},{"type":"input_image","image_url":"data:image/png;base64,QUJD"}]}
		]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body)
	}

	got := f.got[0]
	wantMsgs := []ollama.Message{
		{Role: "system", Content: "Jawab singkat.\n\nNamamu Budi."},
		{Role: "user", Content: "Siapa namamu?\n\nLihat", Images: []string{"QUJD"}},
	}
	if !reflect.DeepEqual(got.Messages, wantMsgs) {
		t.Errorf("messages = %#v", got.Messages)
	}
	if got.Think != "low" || got.Options["num_predict"] != float64(100) {
		t.Errorf("think=%v options=%v", got.Think, got.Options)
	}

	var resp responseObject
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Object != "response" || resp.Status != "completed" || len(resp.Output) != 2 {
		t.Fatalf("response = %+v", resp)
	}
	if o := resp.Output[0]; o.Type != "reasoning" || o.Content[0].Text != "mikir" {
		t.Errorf("output[0] = %+v", o)
	}
	if o := resp.Output[1]; o.Type != "message" || o.Role != "assistant" || o.Content[0].Type != "output_text" || o.Content[0].Text != "Nama saya Budi." {
		t.Errorf("output[1] = %+v", o)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 29 || resp.Reasoning.Effort != "low" {
		t.Errorf("usage=%+v reasoning=%+v", resp.Usage, resp.Reasoning)
	}
	// Field wajib SDK: annotations & summary harus array, bukan hilang.
	if !strings.Contains(rr.Body.String(), `"annotations":[]`) || !strings.Contains(rr.Body.String(), `"summary":[]`) {
		t.Errorf("annotations/summary hilang: %s", rr.Body)
	}
}

func TestResponsesOllamaStreaming(t *testing.T) {
	f := &fakeOllamaServer{lines: ollamaReply}
	s := newOllamaTestServer(t, f)
	rr := doResponses(s, `{"model":"ollama/qwen3.5:9b","stream":true,"input":"Hai"}`)

	var types []string
	var deltas strings.Builder
	var last map[string]any
	for _, block := range strings.Split(strings.TrimSpace(rr.Body.String()), "\n\n") {
		ev, data, ok := strings.Cut(block, "\ndata: ")
		if !ok {
			t.Fatalf("blok SSE tidak valid: %q", block)
		}
		var d map[string]any
		if err := json.Unmarshal([]byte(data), &d); err != nil {
			t.Fatal(err)
		}
		typ := strings.TrimPrefix(ev, "event: ")
		if d["type"] != typ || int(d["sequence_number"].(float64)) != len(types) {
			t.Errorf("event %s: type/sequence tidak cocok: %v", typ, d)
		}
		types = append(types, typ)
		if typ == "response.output_text.delta" {
			deltas.WriteString(d["delta"].(string))
		}
		last = d
	}

	want := []string{
		"response.created", "response.in_progress",
		"response.output_item.added", "response.content_part.added", "response.reasoning_text.delta",
		"response.reasoning_text.done", "response.content_part.done", "response.output_item.done",
		"response.output_item.added", "response.content_part.added",
		"response.output_text.delta", "response.output_text.delta",
		"response.output_text.done", "response.content_part.done", "response.output_item.done",
		"response.completed",
	}
	if !reflect.DeepEqual(types, want) {
		t.Errorf("urutan event =\n  %v\nwant\n  %v", types, want)
	}
	if deltas.String() != "Nama saya Budi." {
		t.Errorf("delta = %q", deltas.String())
	}
	if out := last["response"].(map[string]any)["output"].([]any); len(out) != 2 {
		t.Errorf("output final = %v", out)
	}
}

func TestResponsesStreamingFailure(t *testing.T) {
	f := &fakeOllamaServer{lines: []string{`{"message":{"content":"sete"},"done":false}`, `{"error":"model crash"}`}}
	s := newOllamaTestServer(t, f)
	body := doResponses(s, `{"model":"ollama/m","stream":true,"input":"Hai"}`).Body.String()
	if !strings.Contains(body, "event: response.failed") || !strings.Contains(body, "model crash") {
		t.Errorf("harus response.failed:\n%s", body)
	}
}

func TestResponsesRejects(t *testing.T) {
	s := newOllamaTestServer(t, &fakeOllamaServer{lines: ollamaReply})
	for _, body := range []string{
		`{"model":"claude-code","input":"Hai"}`,
		`{"model":"ollama/m","input":"Hai","previous_response_id":"resp_1"}`,
		`{"model":"ollama/m","input":[{"type":"computer_call_output","output":"x"}]}`,
		`{"model":"ollama/m","input":42}`,
		`{"model":"ollama/m"}`,
	} {
		if rr := doResponses(s, body); rr.Code != http.StatusBadRequest {
			t.Errorf("harus 400 untuk %s, dapat %d %s", body, rr.Code, rr.Body)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	s.handleResponses(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("tanpa token harus 401, dapat %d", rr.Code)
	}
}

func TestModelsIncludesOllama(t *testing.T) {
	s := newOllamaTestServer(t, &fakeOllamaServer{})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer rahasia")
	rr := httptest.NewRecorder()
	s.handleModels(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, `"id":"ollama/qwen3.5:9b"`) || strings.Contains(body, "bge-m3") {
		t.Errorf("models = %s", body)
	}
}
