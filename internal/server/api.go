package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// apiPromptRequest adalah body JSON untuk POST /api/prompt.
type apiPromptRequest struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id"` // opsional: lanjutkan percakapan
}

// apiPromptResponse adalah respons JSON.
type apiPromptResponse struct {
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
	IsError   bool   `json:"is_error"`
}

// handleAPIPrompt menjalankan satu prompt melalui Claude Code dan mengembalikan
// hasil akhirnya sebagai JSON. Caller mengelola session_id sendiri (stateless di
// sisi server). Memerlukan header Authorization: Bearer <API_TOKEN>.
func (s *Server) handleAPIPrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "gunakan POST")
		return
	}
	if !s.apiAuthorized(r) {
		writeJSONError(w, http.StatusUnauthorized, "token tidak valid")
		return
	}

	var req apiPromptRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON tidak valid")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeJSONError(w, http.StatusBadRequest, "field 'prompt' wajib diisi")
		return
	}

	// API berjalan paralel (tidak diantrikan). Bila batas konkurensi diset dan
	// penuh, tolak segera dengan 429 alih-alih membuat caller menunggu.
	if s.apiSem != nil {
		select {
		case s.apiSem <- struct{}{}:
			defer func() { <-s.apiSem }()
		default:
			writeJSONError(w, http.StatusTooManyRequests, "kapasitas API penuh, coba lagi")
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Timeout)
	defer cancel()

	res, runErr := s.apiRunner.RunStream(ctx, prompt, req.SessionID, nil)
	if runErr != nil {
		log.Printf("api claude error: %v", runErr)
	}

	text := res.Text
	if strings.TrimSpace(text) == "" {
		if res.IsError || runErr != nil {
			text = "Claude melaporkan error."
		} else {
			text = "(tidak ada output)"
		}
	}
	writeJSON(w, http.StatusOK, apiPromptResponse{
		Result:    text,
		SessionID: res.SessionID,
		IsError:   res.IsError || runErr != nil,
	})
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

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
