package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// apiModelName adalah nama model yang diiklankan endpoint OpenAI-compatible.
// Model sebenarnya mengikuti langganan Claude pada CLI.
const apiModelName = "claude-code"

// --- Tipe request/response ala OpenAI Chat Completions ---

type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string ATAU array bagian konten
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatChoice struct {
	Index        int             `json:"index"`
	Message      chatRespMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type chatRespMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// --- Handler ---

// handleChatCompletions mengimplementasikan POST /v1/chat/completions ala
// OpenAI (non-streaming). Stateless: messages diratakan jadi satu prompt dan
// dijalankan sebagai sesi baru. Auth: Authorization: Bearer <API_TOKEN>.
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "gunakan POST", "invalid_request_error")
		return
	}
	if !s.apiAuthorized(r) {
		writeOpenAIError(w, http.StatusUnauthorized, "API key tidak valid", "invalid_request_error")
		return
	}

	var req chatCompletionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "JSON tidak valid", "invalid_request_error")
		return
	}
	if req.Stream {
		writeOpenAIError(w, http.StatusBadRequest,
			"streaming tidak didukung; gunakan stream=false", "invalid_request_error")
		return
	}
	prompt := buildPrompt(req.Messages)
	if prompt == "" {
		writeOpenAIError(w, http.StatusBadRequest, "'messages' kosong atau tanpa teks", "invalid_request_error")
		return
	}

	// Paralel (tidak diantrikan): bila batas konkurensi penuh, tolak segera.
	if s.apiSem != nil {
		select {
		case s.apiSem <- struct{}{}:
			defer func() { <-s.apiSem }()
		default:
			writeOpenAIError(w, http.StatusTooManyRequests, "kapasitas penuh, coba lagi", "rate_limit_error")
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Timeout)
	defer cancel()

	// Pilih engine berdasarkan field "model": "gemini..." -> Gemini, selain itu
	// -> Claude. Keduanya stateless & memakai direktori kerja default.
	var text string
	if isGeminiModel(req.Model) {
		if s.gemini == nil {
			writeOpenAIError(w, http.StatusBadRequest,
				"model Gemini tidak diaktifkan (set GEMINI_ENABLED=true)", "invalid_request_error")
			return
		}
		out, err := s.gemini.Run(ctx, prompt, s.cfg.WorkDir, req.Model)
		if err != nil {
			log.Printf("openai api gemini error: %v", err)
			writeOpenAIError(w, http.StatusBadGateway, "Gemini error: "+err.Error(), "api_error")
			return
		}
		text = out
	} else {
		res, runErr := s.apiRunner.RunStream(ctx, prompt, "", s.cfg.WorkDir, claudeModelOverride(req.Model), nil)
		if runErr != nil {
			log.Printf("openai api claude error: %v", runErr)
		}
		if strings.TrimSpace(res.Text) == "" && (res.IsError || runErr != nil) {
			writeOpenAIError(w, http.StatusBadGateway, "Claude melaporkan error", "api_error")
			return
		}
		text = res.Text
	}

	model := req.Model
	if model == "" {
		model = apiModelName
	}
	writeJSON(w, http.StatusOK, chatCompletionResponse{
		ID:      "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 36),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []chatChoice{{
			Index:        0,
			Message:      chatRespMessage{Role: "assistant", Content: text},
			FinishReason: "stop",
		}},
		Usage: chatUsage{}, // token tidak dilacak (mengikuti claude CLI)
	})
}

// handleModels mengimplementasikan GET /v1/models (sebagian klien memerlukannya).
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if !s.apiAuthorized(r) {
		writeOpenAIError(w, http.StatusUnauthorized, "API key tidak valid", "invalid_request_error")
		return
	}
	add := func(data []map[string]any, owner string, ids ...string) []map[string]any {
		for _, id := range ids {
			data = append(data, map[string]any{
				"id": id, "object": "model", "created": 0, "owned_by": owner,
			})
		}
		return data
	}
	var data []map[string]any
	// Claude: default + alias yang diteruskan ke `claude --model`.
	data = add(data, "anthropic", apiModelName, "opus", "sonnet", "haiku")
	if s.gemini != nil {
		// Gemini: nama yang diteruskan ke `gemini -m` (apa pun gemini-* valid).
		data = add(data, "google",
			"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini-3-pro-preview")
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// --- Helper ---

// isGeminiModel mengembalikan true bila nama model menunjuk ke engine Gemini.
func isGeminiModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gemini")
}

// claudeModelOverride memetakan field "model" OpenAI ke argumen --model claude.
// Mengembalikan "" (pakai default langganan) untuk nama generik/tak dikenal
// (mis. "claude-code", "gpt-4o") agar klien yang mengirim model sembarang tidak
// gagal. Hanya alias/id Claude yang diteruskan.
func claudeModelOverride(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case m == "" || m == "claude-code", m == "claude":
		return ""
	case m == "opus" || m == "sonnet" || m == "haiku":
		return m
	case strings.HasPrefix(m, "claude-"):
		return strings.TrimSpace(model)
	default:
		return ""
	}
}

// buildPrompt meratakan messages OpenAI menjadi satu prompt untuk claude -p.
// Pesan system diprepend; satu pesan user tunggal dikirim apa adanya; percakapan
// banyak-giliran dirender sebagai transkrip berlabel.
func buildPrompt(messages []chatMessage) string {
	var systems []string
	var convo []chatMessage
	for _, m := range messages {
		if m.Role == "system" {
			if t := messageText(m.Content); t != "" {
				systems = append(systems, t)
			}
			continue
		}
		convo = append(convo, m)
	}

	var b strings.Builder
	if len(systems) > 0 {
		b.WriteString(strings.Join(systems, "\n"))
		b.WriteString("\n\n")
	}
	if len(convo) == 1 && convo[0].Role == "user" {
		b.WriteString(messageText(convo[0].Content))
	} else {
		for _, m := range convo {
			label := "User"
			if m.Role == "assistant" {
				label = "Assistant"
			}
			b.WriteString(label)
			b.WriteString(": ")
			b.WriteString(messageText(m.Content))
			b.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// messageText mengambil teks dari content yang berupa string atau array bagian
// ({"type":"text","text":...}).
func messageText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

// apiAuthorized memvalidasi header Authorization: Bearer <token> secara
// constant-time.
func (s *Server) apiAuthorized(r *http.Request) bool {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return false
	}
	got := strings.TrimSpace(h[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.APIToken)) == 1
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeOpenAIError membalas dalam format error OpenAI: {"error":{message,type}}.
func writeOpenAIError(w http.ResponseWriter, code int, msg, typ string) {
	writeJSON(w, code, map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    typ,
		},
	})
}
