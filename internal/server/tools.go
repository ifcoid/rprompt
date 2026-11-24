package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/awangga/rprompt/internal/ollama"
	"github.com/awangga/rprompt/internal/toolcall"
)

// --- Tipe tool calling ala OpenAI Chat Completions ---

type openAITool struct {
	Type     string        `json:"type"`
	Function toolcall.Tool `json:"function"`
}

type openAIToolCall struct {
	Index    *int           `json:"index,omitempty"` // hanya pada chunk streaming
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // teks JSON
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// toolRequest mengembalikan true bila request menyangkut tool: ada definisi, atau
// riwayatnya memuat tool call (mis. klien meringkas percakapan tanpa tool).
func toolRequest(req *chatCompletionRequest) bool {
	if len(req.Tools) > 0 {
		return true
	}
	for _, m := range req.Messages {
		if m.Role == "tool" || len(m.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

var toolNameRe = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,128}$`)

// parseTools memvalidasi definisi tool. Tipe selain "function" (tool bawaan
// server OpenAI) dilewati karena tidak bisa dijalankan klien.
func parseTools(defs []openAITool) ([]toolcall.Tool, error) {
	var out []toolcall.Tool
	for i, t := range defs {
		if t.Type != "" && t.Type != "function" {
			continue
		}
		if !toolNameRe.MatchString(t.Function.Name) {
			return nil, fmt.Errorf("tools[%d]: nama fungsi %q tidak valid", i, t.Function.Name)
		}
		out = append(out, t.Function)
	}
	return out, nil
}

// parseToolChoice membaca "auto" | "none" | "required" | {"function":{"name"}}
// (Chat Completions) | {"type":"function","name"} (Responses).
func parseToolChoice(raw json.RawMessage) (toolcall.Choice, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch s {
		case "", "auto", "none", "required":
			return toolcall.Choice(s), nil
		}
		return "", fmt.Errorf("tool_choice %q tidak dikenal", s)
	}
	var o struct {
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &o) == nil {
		if o.Function.Name != "" {
			return toolcall.Choice(o.Function.Name), nil
		}
		if o.Name != "" {
			return toolcall.Choice(o.Name), nil
		}
	}
	return "", errors.New("tool_choice tidak valid")
}

// selectTools menerapkan tool_choice: "none" membuang semua tool, nama tertentu
// menyisakan satu tool itu.
func selectTools(tools []toolcall.Tool, choice toolcall.Choice) ([]toolcall.Tool, error) {
	switch choice {
	case "", "auto", "required":
		if choice == "required" && len(tools) == 0 {
			return nil, errors.New("tool_choice required tanpa tools")
		}
		return tools, nil
	case "none":
		return nil, nil
	}
	for _, t := range tools {
		if t.Name == string(choice) {
			return []toolcall.Tool{t}, nil
		}
	}
	return nil, fmt.Errorf("tool_choice: tool %q tidak ada di 'tools'", string(choice))
}

// requestTools membaca tools dan tool_choice sebuah request.
func requestTools(defs []openAITool, rawChoice json.RawMessage) ([]toolcall.Tool, toolcall.Choice, error) {
	tools, err := parseTools(defs)
	if err != nil {
		return nil, "", err
	}
	choice, err := parseToolChoice(rawChoice)
	if err != nil {
		return nil, "", err
	}
	tools, err = selectTools(tools, choice)
	return tools, choice, err
}

// --- Engine CLI ---

// toolJob adalah satu giliran tool calling pada engine CLI.
type toolJob struct {
	runner toolcall.Runner
	req    toolcall.Request
	tools  []toolcall.Tool
}

// effortRe: nilai effort diteruskan sebagai argumen CLI.
var effortRe = regexp.MustCompile(`^(minimal|low|medium|high|xhigh|max)$`)

// optionRe mengenali nama model yang akan terbaca sebagai opsi CLI ("-x", "codex/-x").
var optionRe = regexp.MustCompile(`(^|/)\s*-`)

// newToolJob menyiapkan giliran tool untuk engine CLI yang dipilih req.Model.
func (s *Server) newToolJob(req *chatCompletionRequest) (*toolJob, error) {
	tools, choice, err := requestTools(req.Tools, req.ToolChoice)
	if err != nil {
		return nil, err
	}
	var msgs []toolcall.Message
	for _, m := range req.Messages {
		t := toolcall.Message{
			Role: strings.ToLower(strings.TrimSpace(m.Role)), Text: messageText(m.Content),
			CallID: m.ToolCallID, Name: m.Name,
		}
		for _, c := range m.ToolCalls {
			t.Calls = append(t.Calls, toolcall.Call{ID: c.ID, Name: c.Function.Name, Arguments: c.Function.Arguments})
		}
		if t.Text == "" && len(t.Calls) == 0 && t.Role != "tool" {
			continue
		}
		msgs = append(msgs, t)
	}
	if len(msgs) == 0 {
		return nil, errors.New("'messages' kosong atau tanpa teks")
	}

	model := strings.TrimSpace(req.Model)
	var engine any
	name := "Claude"
	switch {
	case isKiroModel(model):
		return nil, errors.New("engine Kiro belum mendukung tools")
	case isBobModel(model):
		return nil, errors.New("engine Bob belum mendukung tools")
	case isCodexModel(model):
		engine, name = s.codex, "Codex"
	case isGeminiModel(model):
		engine, name = s.agy, "AGY"
	default:
		engine, model = s.apiRunner, claudeModelOverride(model)
	}
	runner, ok := engine.(toolcall.Runner)
	if !ok {
		return nil, fmt.Errorf("engine %s tidak aktif atau tidak mendukung tools", name)
	}
	job := &toolJob{runner: runner, tools: tools}
	parallel := req.ParallelToolCalls == nil || *req.ParallelToolCalls
	job.req = toolcall.Build(msgs, tools, choice, parallel)
	job.req.Model = model
	effort := req.ReasoningEffort
	if effort == "" && req.Reasoning != nil {
		effort = req.Reasoning.Effort
	}
	if effortRe.MatchString(effort) {
		job.req.Effort = effort
	}
	return job, nil
}

// toolResult adalah hasil satu giliran, siap dikirim ke klien.
type toolResult struct {
	text  string
	calls []openAIToolCall
	usage chatUsage
}

func (j *toolJob) run(ctx context.Context) (toolResult, error) {
	rep, err := j.runner.RunTools(ctx, j.req)
	if err != nil {
		return toolResult{}, err
	}
	text, calls := toolcall.Parse(rep.Text, j.tools)
	res := toolResult{text: text, usage: chatUsage{
		PromptTokens: rep.InTokens, CompletionTokens: rep.OutTokens, TotalTokens: rep.InTokens + rep.OutTokens,
	}}
	for _, c := range calls {
		res.calls = append(res.calls, openAIToolCall{
			ID: c.ID, Type: "function", Function: openAIFunction{Name: c.Name, Arguments: c.Arguments},
		})
	}
	return res, nil
}

// --- Ollama ---

// ollamaTools mengubah tool ke bentuk yang diterima /api/chat.
func ollamaTools(tools []toolcall.Tool) []json.RawMessage {
	var out []json.RawMessage
	for _, t := range tools {
		if len(t.Parameters) == 0 || string(t.Parameters) == "null" {
			t.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		b, err := json.Marshal(openAITool{Type: "function", Function: t})
		if err == nil {
			out = append(out, b)
		}
	}
	return out
}

// ollamaCall mengubah tool call OpenAI ke bentuk Ollama. Argumen harus berupa
// objek JSON; teks lain (dari model yang salah tulis) dibungkus agar tak hilang.
func ollamaCall(name, args string) ollama.ToolCall {
	raw := json.RawMessage(strings.TrimSpace(args))
	var obj map[string]any
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	} else if json.Unmarshal(raw, &obj) != nil || obj == nil {
		raw, _ = json.Marshal(map[string]string{"arguments": args})
	}
	return ollama.ToolCall{Function: ollama.ToolFunction{Name: name, Arguments: raw}}
}

// fromOllamaCalls mengubah tool call Ollama ke bentuk OpenAI.
func fromOllamaCalls(calls []ollama.ToolCall) []openAIToolCall {
	var out []openAIToolCall
	for _, c := range calls {
		args := string(bytes.TrimSpace(c.Function.Arguments))
		if args == "" || args == "null" {
			args = "{}"
		}
		out = append(out, openAIToolCall{
			ID: toolcall.NewID(), Type: "function", Function: openAIFunction{Name: c.Function.Name, Arguments: args},
		})
	}
	return out
}

// handleToolJob membalas giliran tool engine CLI tanpa streaming.
func (s *Server) handleToolJob(ctx context.Context, w http.ResponseWriter, model string, j *toolJob) {
	res, err := j.run(ctx)
	if err != nil {
		log.Printf("openai api tool error: %v", err)
		writeOpenAIError(w, http.StatusBadGateway, err.Error(), "api_error")
		return
	}
	writeChat(w, model, chatRespMessage{Role: "assistant", Content: res.text, ToolCalls: res.calls}, "stop", res.usage)
}
