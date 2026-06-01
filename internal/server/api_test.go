package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/awangga/rprompt/internal/claude"
	"github.com/awangga/rprompt/internal/config"
)

// fakeRunner mengimplementasikan promptRunner tanpa menjalankan claude.
type fakeRunner struct {
	result       claude.Result
	err          error
	gotPrompt    string
	gotSessionIn string
}

func (f *fakeRunner) RunStream(_ context.Context, prompt, sessionID string, _ func(claude.StreamEvent)) (claude.Result, error) {
	f.gotPrompt = prompt
	f.gotSessionIn = sessionID
	return f.result, f.err
}

func newAPITestServer(runner promptRunner) *Server {
	return &Server{
		cfg:       &config.Config{APIEnabled: true, APIToken: "rahasia", Timeout: 5 * time.Second},
		apiRunner: runner,
		// apiSem nil = tak terbatas (paralel penuh)
	}
}

// blockingRunner menahan eksekusi sampai release ditutup; dipakai menguji
// paralelisme.
type blockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingRunner) RunStream(_ context.Context, _, _ string, _ func(claude.StreamEvent)) (claude.Result, error) {
	b.started <- struct{}{}
	<-b.release
	return claude.Result{Text: "done", SessionID: "s"}, nil
}

func doAPI(s *Server, method, auth, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/prompt", strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rr := httptest.NewRecorder()
	s.handleAPIPrompt(rr, req)
	return rr
}

func TestAPIMethodNotAllowed(t *testing.T) {
	s := newAPITestServer(&fakeRunner{})
	if rr := doAPI(s, http.MethodGet, "Bearer rahasia", ""); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET harus 405, dapat %d", rr.Code)
	}
}

func TestAPIUnauthorized(t *testing.T) {
	s := newAPITestServer(&fakeRunner{})
	cases := []string{"", "Bearer salah", "rahasia", "Basic rahasia"}
	for _, auth := range cases {
		rr := doAPI(s, http.MethodPost, auth, `{"prompt":"hai"}`)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("auth %q harus 401, dapat %d", auth, rr.Code)
		}
	}
}

func TestAPIBadJSON(t *testing.T) {
	s := newAPITestServer(&fakeRunner{})
	if rr := doAPI(s, http.MethodPost, "Bearer rahasia", "bukan json"); rr.Code != http.StatusBadRequest {
		t.Fatalf("JSON rusak harus 400, dapat %d", rr.Code)
	}
}

func TestAPIEmptyPrompt(t *testing.T) {
	s := newAPITestServer(&fakeRunner{})
	if rr := doAPI(s, http.MethodPost, "Bearer rahasia", `{"prompt":"   "}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("prompt kosong harus 400, dapat %d", rr.Code)
	}
}

func TestAPISuccess(t *testing.T) {
	runner := &fakeRunner{result: claude.Result{Text: "halo balik", SessionID: "sess-9"}}
	s := newAPITestServer(runner)

	rr := doAPI(s, http.MethodPost, "Bearer rahasia", `{"prompt":"halo","session_id":"sess-1"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("harus 200, dapat %d (%s)", rr.Code, rr.Body.String())
	}
	var resp apiPromptResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("respons bukan JSON valid: %v", err)
	}
	if resp.Result != "halo balik" || resp.SessionID != "sess-9" || resp.IsError {
		t.Errorf("respons tak sesuai: %#v", resp)
	}
	// Pastikan prompt & session diteruskan ke runner.
	if runner.gotPrompt != "halo" || runner.gotSessionIn != "sess-1" {
		t.Errorf("runner menerima prompt=%q session=%q", runner.gotPrompt, runner.gotSessionIn)
	}
}

func TestAPIRunsInParallel(t *testing.T) {
	br := &blockingRunner{started: make(chan struct{}, 2), release: make(chan struct{})}
	s := newAPITestServer(br) // apiSem nil = tak terbatas

	go doAPI(s, http.MethodPost, "Bearer rahasia", `{"prompt":"a"}`)
	go doAPI(s, http.MethodPost, "Bearer rahasia", `{"prompt":"b"}`)

	// Kedua request harus mulai SEBELUM ada yang dilepas → bukti paralel.
	for i := 0; i < 2; i++ {
		select {
		case <-br.started:
		case <-time.After(2 * time.Second):
			t.Fatal("request kedua tidak jalan paralel (terblok/diantrikan)")
		}
	}
	close(br.release)
}

func TestAPIConcurrencyLimit(t *testing.T) {
	br := &blockingRunner{started: make(chan struct{}, 2), release: make(chan struct{})}
	s := newAPITestServer(br)
	s.apiSem = make(chan struct{}, 1) // batas 1

	go doAPI(s, http.MethodPost, "Bearer rahasia", `{"prompt":"a"}`)
	<-br.started // request pertama memegang satu-satunya slot

	// Request kedua harus langsung 429 (penuh), bukan menunggu.
	rr := doAPI(s, http.MethodPost, "Bearer rahasia", `{"prompt":"b"}`)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("saat penuh harus 429, dapat %d", rr.Code)
	}
	close(br.release)
}

func TestAPIClaudeError(t *testing.T) {
	runner := &fakeRunner{result: claude.Result{Text: "boom", SessionID: "s", IsError: true}}
	s := newAPITestServer(runner)

	rr := doAPI(s, http.MethodPost, "Bearer rahasia", `{"prompt":"x"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("harus 200 (error level claude), dapat %d", rr.Code)
	}
	var resp apiPromptResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !resp.IsError {
		t.Errorf("is_error harus true, dapat %#v", resp)
	}
}
