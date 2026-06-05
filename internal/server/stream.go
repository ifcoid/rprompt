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
)

// --- Tipe chunk streaming ala OpenAI (chat.completion.chunk) ---

type chatChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Choices []chatChunkChoice `json:"choices"`
}

type chatChunkChoice struct {
	Index        int            `json:"index"`
	Delta        chatChunkDelta `json:"delta"`
	FinishReason *string        `json:"finish_reason"`
}

type chatChunkDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// streamChat membalas /v1/chat/completions dalam format SSE OpenAI (stream=true).
// Output dikirim bertahap dan disisipi keepalive comment agar koneksi tetap
// aktif — mencegah timeout 524 Cloudflare (~100s) pada output yang panjang.
func (s *Server) streamChat(ctx context.Context, w http.ResponseWriter, req *chatCompletionRequest, prompt string) {
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
	send := func(delta chatChunkDelta, finish *string) {
		b, _ := json.Marshal(chatChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []chatChunkChoice{{Index: 0, Delta: delta, FinishReason: finish}},
		})
		mu.Lock()
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(b)
		_, _ = w.Write([]byte("\n\n"))
		flusher.Flush()
		mu.Unlock()
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
	if isGeminiModel(req.Model) {
		if s.gemini == nil {
			send(chatChunkDelta{Content: "[error] model Gemini tidak diaktifkan (GEMINI_ENABLED=true)"}, nil)
		} else {
			out, err := s.gemini.Run(ctx, prompt, s.cfg.WorkDir, req.Model)
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
		send(chatChunkDelta{Content: "\n[error] " + errDetail}, nil)
	}
	finish := "stop"
	send(chatChunkDelta{}, &finish)
	raw("data: [DONE]\n\n")
}
