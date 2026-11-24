package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/awangga/rprompt/internal/claude"
	"github.com/awangga/rprompt/internal/ollama"
)

// --- Tipe chunk streaming ala OpenAI (chat.completion.chunk) ---

type chatChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Choices []chatChunkChoice `json:"choices"`
	Usage   *chatUsage        `json:"usage,omitempty"`
}

type chatChunkChoice struct {
	Index        int            `json:"index"`
	Delta        chatChunkDelta `json:"delta"`
	FinishReason *string        `json:"finish_reason"`
}

type chatChunkDelta struct {
	Role             string `json:"role,omitempty"`
	Content          string `json:"content,omitempty"`
	ReasoningContent string `json:"reasoning_content,omitempty"` // thinking (Ollama)

	ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
}

// streamChat membalas /v1/chat/completions dalam format SSE OpenAI (stream=true).
// Output dikirim bertahap dan disisipi keepalive comment agar koneksi tetap
// aktif — mencegah timeout 524 Cloudflare (~100s) pada output yang panjang.
// oreq diisi (prompt kosong) bila model diarahkan ke Ollama, job bila giliran tool.
func (s *Server) streamChat(ctx context.Context, w http.ResponseWriter, req *chatCompletionRequest, prompt string, oreq *ollama.ChatRequest, job *toolJob) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "server tidak mendukung streaming", "api_error")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // jangan buffer di proxy
	w.WriteHeader(http.StatusOK)

	model := req.Model
	if model == "" {
		model = apiModelName
	}
	id := "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	created := time.Now().Unix()

	var mu sync.Mutex // serialisasi penulisan (konten vs keepalive)
	chunk := func(choices []chatChunkChoice, usage *chatUsage) {
		b, _ := json.Marshal(chatChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: choices, Usage: usage,
		})
		mu.Lock()
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(b)
		_, _ = w.Write([]byte("\n\n"))
		flusher.Flush()
		mu.Unlock()
	}
	send := func(delta chatChunkDelta, finish *string) {
		chunk([]chatChunkChoice{{Index: 0, Delta: delta, FinishReason: finish}}, nil)
	}
	sendCalls := func(calls []openAIToolCall) {
		for i := range calls {
			calls[i].Index = &i
			send(chatChunkDelta{ToolCalls: calls[i : i+1]}, nil)
		}
	}
	raw := func(str string) {
		mu.Lock()
		_, _ = io.WriteString(w, str)
		flusher.Flush()
		mu.Unlock()
	}

	// Chunk awal: role.
	send(chatChunkDelta{Role: "assistant"}, nil)

	// Keepalive berkala (20s < batas 100s Cloudflare) selama menunggu/jeda.
	stop := make(chan struct{})
	pingDone := make(chan struct{})
	go func() {
		defer close(pingDone)
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				raw(": keepalive\n\n")
			case <-stop:
				return
			}
		}
	}()

	var errDetail string // pesan error asli engine (mis. rate limit) bila ada
	finish := "stop"
	var usage *chatUsage
	if job != nil {
		res, err := job.run(ctx)
		if err != nil {
			errDetail = err.Error()
		} else {
			if res.text != "" {
				send(chatChunkDelta{Content: res.text}, nil)
			}
			sendCalls(res.calls)
			finish = toolFinish(finish, res.calls)
			if res.usage.TotalTokens > 0 {
				usage = &res.usage
			}
		}
	} else if oreq != nil {
		res, err := s.ollama.Chat(ctx, *oreq, func(content, thinking string) {
			send(chatChunkDelta{Content: content, ReasoningContent: thinking}, nil)
		})
		if err != nil {
			errDetail = "Ollama error: " + err.Error()
		} else {
			calls := fromOllamaCalls(res.ToolCalls)
			sendCalls(calls)
			finish = toolFinish(finishReason(res.DoneReason), calls)
			usage = &chatUsage{
				PromptTokens:     res.PromptTokens,
				CompletionTokens: res.CompletionTokens,
				TotalTokens:      res.PromptTokens + res.CompletionTokens,
			}
		}
	} else if isKiroModel(req.Model) {
		if s.kiro == nil {
			send(chatChunkDelta{Content: "[error] model Kiro tidak diaktifkan (KIRO_ENABLED=true)"}, nil)
		} else {
			out, err := s.kiro.Run(ctx, prompt, s.cfg.WorkDir, req.Model)
			if err != nil {
				errDetail = err.Error()
			} else if out != "" {
				send(chatChunkDelta{Content: out}, nil)
			}
		}
	} else if isBobModel(req.Model) {
		if s.bob == nil {
			send(chatChunkDelta{Content: "[error] model Bob tidak diaktifkan (BOB_ENABLED=true)"}, nil)
		} else {
			out, err := s.bob.Run(ctx, prompt, s.cfg.WorkDir, req.Model)
			if err != nil {
				errDetail = err.Error()
			} else if out != "" {
				send(chatChunkDelta{Content: out}, nil)
			}
		}
	} else if isCodexModel(req.Model) {
		if s.codex == nil {
			send(chatChunkDelta{Content: "[error] model Codex tidak diaktifkan (CODEX_ENABLED=true)"}, nil)
		} else {
			out, err := s.codex.Run(ctx, prompt, s.cfg.WorkDir, req.Model)
			if err != nil {
				errDetail = err.Error()
			} else if out != "" {
				send(chatChunkDelta{Content: out}, nil)
			}
		}
	} else if isGeminiModel(req.Model) {
		if s.agy == nil {
			send(chatChunkDelta{Content: "[error] model AGY tidak diaktifkan (AGY_ENABLED=true)"}, nil)
		} else {
			out, err := s.agy.Run(ctx, prompt, s.cfg.WorkDir, req.Model)
			if err != nil {
				errDetail = err.Error()
			} else if out != "" {
				send(chatChunkDelta{Content: out}, nil)
			}
		}
	} else {
		res, err := s.apiRunner.RunStream(ctx, prompt, "", s.cfg.WorkDir, claudeModelOverride(req.Model),
			func(ev claude.StreamEvent) {
				if ev.AppendText != "" {
					send(chatChunkDelta{Content: ev.AppendText}, nil)
				}
			})
		// Surface pesan asli claude (res.Text memuat stderr/pesan rate limit).
		if err != nil || res.IsError {
			errDetail = strings.TrimSpace(res.Text)
			if errDetail == "" && err != nil {
				errDetail = err.Error()
			}
		}
	}

	// Hentikan keepalive sebelum chunk penutup (hindari ping setelah [DONE]).
	close(stop)
	<-pingDone

	if errDetail != "" {
		log.Printf("stream api error: %s", errDetail)
		errObj := map[string]interface{}{
			"error": map[string]string{
				"message": errDetail,
				"type":    "server_error",
			},
		}
		b, _ := json.Marshal(errObj)
		mu.Lock()
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(b)
		_, _ = w.Write([]byte("\n\n"))
		flusher.Flush()
		mu.Unlock()
	} else {
		send(chatChunkDelta{}, &finish)
		if usage != nil && req.StreamOptions != nil && req.StreamOptions.IncludeUsage {
			chunk([]chatChunkChoice{}, usage)
		}
	}
	raw("data: [DONE]\n\n")
}
