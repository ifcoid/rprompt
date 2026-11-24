package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/awangga/rprompt/internal/ollama"
	"github.com/awangga/rprompt/internal/toolcall"
)

// --- Tipe request/response ala OpenAI Responses API ---

type responsesRequest struct {
	Model              string          `json:"model"`
	Input              json.RawMessage `json:"input"` // string ATAU array item
	Instructions       string          `json:"instructions"`
	Stream             bool            `json:"stream"`
	Reasoning          *reasoningParam `json:"reasoning"`
	Temperature        *float64        `json:"temperature"`
	TopP               *float64        `json:"top_p"`
	MaxOutputTokens    *int            `json:"max_output_tokens"`
	PreviousResponseID string          `json:"previous_response_id"`
	Tools              []responsesTool `json:"tools"`
	ToolChoice         json.RawMessage `json:"tool_choice"`
}

// responsesTool adalah tool versi Responses API: tanpa pembungkus "function".
type responsesTool struct {
	Type string `json:"type"`
	toolcall.Tool
}

func (r *responsesRequest) chatTools() []openAITool {
	var out []openAITool
	for _, t := range r.Tools {
		out = append(out, openAITool{Type: t.Type, Function: t.Tool})
	}
	return out
}

type responseObject struct {
	ID                string             `json:"id"`
	Object            string             `json:"object"`
	CreatedAt         int64              `json:"created_at"`
	Status            string             `json:"status"` // in_progress | completed | incomplete | failed
	Model             string             `json:"model"`
	Output            []responseItem     `json:"output"`
	Error             *responseError     `json:"error"`
	IncompleteDetails *incompleteDetails `json:"incomplete_details"`
	Reasoning         *reasoningParam    `json:"reasoning,omitempty"`
	Usage             *responseUsage     `json:"usage"`
}

// responseItem adalah item output: "reasoning" (thinking model), "message", atau
// "function_call".
type responseItem struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Status  string         `json:"status"`
	Role    string         `json:"role,omitempty"`
	Summary []responsePart `json:"summary,omitzero"` // wajib [] pada reasoning
	Content []responsePart `json:"content,omitzero"`

	CallID    string  `json:"call_id,omitempty"` // function_call
	Name      string  `json:"name,omitempty"`
	Arguments *string `json:"arguments,omitempty"`
}

type responsePart struct {
	Type        string `json:"type"` // output_text | reasoning_text
	Text        string `json:"text"`
	Annotations []any  `json:"annotations,omitzero"` // wajib [] pada output_text
}

type responseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type incompleteDetails struct {
	Reason string `json:"reason"`
}

type responseUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// --- Handler ---

// handleResponses mengimplementasikan POST /v1/responses ala OpenAI untuk
// engine Ollama. Stateless: seluruh percakapan dikirim di "input".
func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "gunakan POST", "invalid_request_error")
		return
	}
	if !s.apiAuthorized(r) {
		writeOpenAIError(w, http.StatusUnauthorized, "API key tidak valid", "invalid_request_error")
		return
	}

	var req responsesRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, apiMaxBody)).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "JSON tidak valid", "invalid_request_error")
		return
	}
	if !isOllamaModel(req.Model) {
		writeOpenAIError(w, http.StatusBadRequest,
			"/v1/responses saat ini hanya untuk model Ollama (\"ollama/<nama>\"); engine lain lewat /v1/chat/completions",
			"invalid_request_error")
		return
	}
	if s.ollama == nil {
		writeOpenAIError(w, http.StatusBadRequest,
			"model Ollama tidak diaktifkan (set OLLAMA_ENABLED=true)", "invalid_request_error")
		return
	}
	if req.PreviousResponseID != "" {
		writeOpenAIError(w, http.StatusBadRequest,
			"previous_response_id tidak didukung (stateless): kirim seluruh percakapan di 'input'",
			"invalid_request_error")
		return
	}
	oreq, err := responsesToOllama(&req)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}

	release, ok := s.acquireAPI(w)
	if !ok {
		return
	}
	defer release()

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Timeout)
	defer cancel()

	b := newResponseBuilder(req.Model, req.Reasoning)
	if req.Stream {
		s.streamResponses(ctx, w, oreq, b)
		return
	}
	res, err := s.ollama.Chat(ctx, oreq, b.add)
	if err != nil {
		log.Printf("responses api ollama error: %v", err)
		writeOllamaError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b.finish(res))
}

// streamResponses membalas dalam format SSE Responses API (event bertipe),
// disisipi keepalive agar tidak kena timeout 524 Cloudflare.
func (s *Server) streamResponses(ctx context.Context, w http.ResponseWriter, oreq ollama.ChatRequest, b *responseBuilder) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "server tidak mendukung streaming", "api_error")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var mu sync.Mutex // serialisasi penulisan (event vs keepalive)
	seq := 0
	b.emit = func(typ string, data map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		data["type"] = typ
		data["sequence_number"] = seq
		seq++
		j, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, j)
		flusher.Flush()
	}
	stopPing := keepalive(func(str string) {
		mu.Lock()
		_, _ = io.WriteString(w, str)
		flusher.Flush()
		mu.Unlock()
	})

	b.emit("response.created", map[string]any{"response": b.object("in_progress")})
	b.emit("response.in_progress", map[string]any{"response": b.object("in_progress")})
	res, err := s.ollama.Chat(ctx, oreq, b.add)
	stopPing()

	if err != nil {
		log.Printf("responses stream ollama error: %v", err)
		b.emit("response.failed", map[string]any{"response": b.fail(err.Error())})
		return
	}
	obj := b.finish(res)
	typ := "response.completed"
	if obj.Status == "incomplete" {
		typ = "response.incomplete"
	}
	b.emit(typ, map[string]any{"response": obj})
}

// keepalive mengirim komentar SSE berkala (20s < batas 100s Cloudflare) sampai
// fungsi stop yang dikembalikan dipanggil.
func keepalive(raw func(string)) (stop func()) {
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				raw(": keepalive\n\n")
			case <-done:
				return
			}
		}
	}()
	return func() {
		close(done)
		<-exited
	}
}

// responseBuilder menyusun output Responses API dari potongan thinking/content
// Ollama. Thinking menjadi item "reasoning", content menjadi item "message".
// Bila emit tidak nil (streaming), tiap perubahan juga dipancarkan sebagai event.
type responseBuilder struct {
	id        string
	model     string
	created   int64
	reasoning *reasoningParam
	items     []responseItem
	open      bool            // item terakhir masih menerima delta
	buf       strings.Builder // teks item yang sedang terbuka
	emit      func(typ string, data map[string]any)
}

func newResponseBuilder(model string, reasoning *reasoningParam) *responseBuilder {
	now := time.Now()
	return &responseBuilder{
		id:        "resp_" + strconv.FormatInt(now.UnixNano(), 36),
		model:     model,
		created:   now.Unix(),
		reasoning: reasoning,
	}
}

// add menerima satu potongan dari ollama.Client.Chat.
func (b *responseBuilder) add(content, thinking string) {
	if thinking != "" {
		b.write("reasoning", thinking)
	}
	if content != "" {
		b.write("message", content)
	}
}

func (b *responseBuilder) write(kind, delta string) {
	if !b.open || b.items[len(b.items)-1].Type != kind {
		b.closeItem()
		b.openItem(kind)
	}
	b.buf.WriteString(delta)
	idx := len(b.items) - 1
	b.send(textEvent(kind)+".delta", map[string]any{
		"item_id": b.items[idx].ID, "output_index": idx, "content_index": 0, "delta": delta,
	})
}

func (b *responseBuilder) openItem(kind string) {
	idx := len(b.items)
	it := responseItem{
		ID:      fmt.Sprintf("%s_%s_%d", itemPrefix(kind), b.id[len("resp_"):], idx),
		Type:    kind,
		Status:  "in_progress",
		Content: []responsePart{},
	}
	if kind == "message" {
		it.Role = "assistant"
	} else {
		it.Summary = []responsePart{}
	}
	b.items = append(b.items, it)
	b.open = true
	b.buf.Reset()
	b.send("response.output_item.added", map[string]any{"output_index": idx, "item": it})
	b.send("response.content_part.added", map[string]any{
		"item_id": it.ID, "output_index": idx, "content_index": 0, "part": newPart(kind, ""),
	})
}

func (b *responseBuilder) closeItem() {
	if !b.open {
		return
	}
	b.open = false
	idx := len(b.items) - 1
	it := &b.items[idx]
	part := newPart(it.Type, b.buf.String())
	it.Content = []responsePart{part}
	it.Status = "completed"
	b.send(textEvent(it.Type)+".done", map[string]any{
		"item_id": it.ID, "output_index": idx, "content_index": 0, "text": part.Text,
	})
	b.send("response.content_part.done", map[string]any{
		"item_id": it.ID, "output_index": idx, "content_index": 0, "part": part,
	})
	b.send("response.output_item.done", map[string]any{"output_index": idx, "item": *it})
}

// addCalls menambahkan tiap tool call sebagai item "function_call".
func (b *responseBuilder) addCalls(calls []openAIToolCall) {
	for _, c := range calls {
		idx := len(b.items)
		empty, args := "", c.Function.Arguments
		it := responseItem{
			ID:     fmt.Sprintf("fc_%s_%d", b.id[len("resp_"):], idx),
			Type:   "function_call",
			Status: "in_progress",
			CallID: c.ID, Name: c.Function.Name, Arguments: &empty,
		}
		b.send("response.output_item.added", map[string]any{"output_index": idx, "item": it})
		ref := func(k, v string) map[string]any {
			return map[string]any{"item_id": it.ID, "output_index": idx, k: v}
		}
		b.send("response.function_call_arguments.delta", ref("delta", args))
		b.send("response.function_call_arguments.done", ref("arguments", args))
		it.Status, it.Arguments = "completed", &args
		b.items = append(b.items, it)
		b.send("response.output_item.done", map[string]any{"output_index": idx, "item": it})
	}
}

// finish menutup item terakhir dan mengembalikan response final. Bila tidak ada
// tool call, selalu ada satu item message (boleh kosong) agar klien menemukan
// output_text.
func (b *responseBuilder) finish(res ollama.Result) responseObject {
	b.closeItem()
	b.addCalls(fromOllamaCalls(res.ToolCalls))
	hasMsg := false
	for _, it := range b.items {
		hasMsg = hasMsg || it.Type == "message" || it.Type == "function_call"
	}
	if !hasMsg {
		b.openItem("message")
		b.closeItem()
	}
	obj := b.object("completed")
	obj.Usage = &responseUsage{
		InputTokens:  res.PromptTokens,
		OutputTokens: res.CompletionTokens,
		TotalTokens:  res.PromptTokens + res.CompletionTokens,
	}
	if res.DoneReason == "length" {
		obj.Status = "incomplete"
		obj.IncompleteDetails = &incompleteDetails{Reason: "max_output_tokens"}
	}
	return obj
}

// fail menutup item yang terbuka (output parsial dipertahankan) dan
// mengembalikan response berstatus failed.
func (b *responseBuilder) fail(msg string) responseObject {
	b.closeItem()
	obj := b.object("failed")
	obj.Error = &responseError{Code: "server_error", Message: msg}
	return obj
}

func (b *responseBuilder) object(status string) responseObject {
	return responseObject{
		ID:        b.id,
		Object:    "response",
		CreatedAt: b.created,
		Status:    status,
		Model:     b.model,
		Output:    append([]responseItem{}, b.items...),
		Reasoning: b.reasoning,
	}
}

func (b *responseBuilder) send(typ string, data map[string]any) {
	if b.emit != nil {
		b.emit(typ, data)
	}
}

func textEvent(kind string) string {
	if kind == "reasoning" {
		return "response.reasoning_text"
	}
	return "response.output_text"
}

func itemPrefix(kind string) string {
	if kind == "reasoning" {
		return "rs"
	}
	return "msg"
}

func newPart(kind, text string) responsePart {
	if kind == "reasoning" {
		return responsePart{Type: "reasoning_text", Text: text}
	}
	return responsePart{Type: "output_text", Text: text, Annotations: []any{}}
}
