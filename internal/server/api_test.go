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
	result     claude.Result
	err        error
	gotPrompt  string
	gotWorkDir string
	gotModel   string
}

func (f *fakeRunner) RunStream(_ context.Context, prompt, _, workDir, model string, _ func(claude.StreamEvent)) (claude.Result, error) {
	f.gotPrompt = prompt
	f.gotWorkDir = workDir
	f.gotModel = model
	return f.result, f.err
}

// blockingRunner menahan eksekusi sampai release ditutup; untuk uji paralelisme.
type blockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingRunner) RunStream(_ context.Context, _, _, _, _ string, _ func(claude.StreamEvent)) (claude.Result, error) {
	b.started <- struct{}{}
	<-b.release
	return claude.Result{Text: "done"}, nil
}

// fakeGemini mengimplementasikan GeminiRunner.
type fakeGemini struct {
	out       string
	err       error
	gotPrompt string
	gotModel  string
}

func (g *fakeGemini) Run(_ context.Context, prompt, _, model string) (string, error) {
	g.gotPrompt = prompt
	g.gotModel = model
	return g.out, g.err
}

func newAPITestServer(runner promptRunner) *Server {
	return &Server{
		cfg:       &config.Config{APIEnabled: true, APIToken: "rahasia", WorkDir: "/wd", Timeout: 5 * time.Second},
		apiRunner: runner,
		// apiSem nil = paralel tak terbatas
	}
}

func doChat(s *Server, method, auth, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/v1/chat/completions", strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rr := httptest.NewRecorder()
	s.handleChatCompletions(rr, req)
	return rr
}

func TestChatMethodNotAllowed(t *testing.T) {
	s := newAPITestServer(&fakeRunner{})
	if rr := doChat(s, http.MethodGet, "Bearer rahasia", ""); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET harus 405, dapat %d", rr.Code)
	}
}

func TestChatUnauthorized(t *testing.T) {
	s := newAPITestServer(&fakeRunner{})
	for _, auth := range []string{"", "Bearer salah", "rahasia"} {
		body := `{"messages":[{"role":"user","content":"hai"}]}`
		if rr := doChat(s, http.MethodPost, auth, body); rr.Code != http.StatusUnauthorized {
			t.Errorf("auth %q harus 401, dapat %d", auth, rr.Code)
		}
	}
}

func TestChatBadJSON(t *testing.T) {
	s := newAPITestServer(&fakeRunner{})
	if rr := doChat(s, http.MethodPost, "Bearer rahasia", "bukan json"); rr.Code != http.StatusBadRequest {
		t.Fatalf("JSON rusak harus 400, dapat %d", rr.Code)
	}
}

func TestChatEmptyMessages(t *testing.T) {
	s := newAPITestServer(&fakeRunner{})
	if rr := doChat(s, http.MethodPost, "Bearer rahasia", `{"messages":[]}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("messages kosong harus 400, dapat %d", rr.Code)
	}
}

func TestChatStreamRejected(t *testing.T) {
	s := newAPITestServer(&fakeRunner{})
	body := `{"stream":true,"messages":[{"role":"user","content":"hai"}]}`
	rr := doChat(s, http.MethodPost, "Bearer rahasia", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("stream=true harus 400, dapat %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "error") {
		t.Errorf("balasan harus format error OpenAI: %s", rr.Body.String())
	}
}

func TestChatSuccess(t *testing.T) {
	runner := &fakeRunner{result: claude.Result{Text: "hi there"}}
	s := newAPITestServer(runner)

	body := `{"model":"gpt-4o","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hello"}]}`
	rr := doChat(s, http.MethodPost, "Bearer rahasia", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("harus 200, dapat %d (%s)", rr.Code, rr.Body.String())
	}
	var resp chatCompletionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("respons bukan JSON valid: %v", err)
	}
	if resp.Object != "chat.completion" {
		t.Errorf("object=%q", resp.Object)
	}
	if resp.Model != "gpt-4o" {
		t.Errorf("model harus di-echo, dapat %q", resp.Model)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "hi there" ||
		resp.Choices[0].Message.Role != "assistant" || resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("choices tak sesuai: %#v", resp.Choices)
	}
	// system + user diratakan jadi satu prompt.
	if runner.gotPrompt != "be brief\n\nhello" {
		t.Errorf("prompt diratakan salah: %q", runner.gotPrompt)
	}
	if runner.gotWorkDir != "/wd" {
		t.Errorf("workDir harus diteruskan ke runner, dapat %q", runner.gotWorkDir)
	}
}

func TestChatGeminiRouting(t *testing.T) {
	claudeR := &fakeRunner{result: claude.Result{Text: "dari claude"}}
	gem := &fakeGemini{out: "dari gemini"}
	s := newAPITestServer(claudeR)
	s.gemini = gem

	body := `{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hai"}]}`
	rr := doChat(s, http.MethodPost, "Bearer rahasia", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("harus 200, dapat %d (%s)", rr.Code, rr.Body.String())
	}
	var resp chatCompletionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Choices[0].Message.Content != "dari gemini" {
		t.Errorf("harus dijawab Gemini, dapat %q", resp.Choices[0].Message.Content)
	}
	if gem.gotPrompt != "hai" {
		t.Errorf("Gemini menerima prompt %q", gem.gotPrompt)
	}
	if gem.gotModel != "gemini-2.5-flash" {
		t.Errorf("model harus diteruskan ke Gemini, dapat %q", gem.gotModel)
	}
	if claudeR.gotPrompt != "" {
		t.Errorf("Claude tidak boleh dipanggil untuk model gemini")
	}
}

func TestChatClaudeModelOverride(t *testing.T) {
	// model "opus" -> diteruskan ke claude --model; "gpt-4o" -> default ("").
	cases := map[string]string{"opus": "opus", "claude-code": "", "gpt-4o": "", "claude-opus-4-8": "claude-opus-4-8"}
	for model, wantArg := range cases {
		runner := &fakeRunner{result: claude.Result{Text: "ok"}}
		s := newAPITestServer(runner)
		body := `{"model":"` + model + `","messages":[{"role":"user","content":"x"}]}`
		rr := doChat(s, http.MethodPost, "Bearer rahasia", body)
		if rr.Code != http.StatusOK {
			t.Fatalf("model %q: harus 200, dapat %d", model, rr.Code)
		}
		if runner.gotModel != wantArg {
			t.Errorf("model %q -> --model %q, mau %q", model, runner.gotModel, wantArg)
		}
	}
}

func TestClaudeModelOverride(t *testing.T) {
	cases := map[string]string{
		"": "", "claude-code": "", "claude": "",
		"opus": "opus", "sonnet": "sonnet", "haiku": "haiku",
		"claude-opus-4-8": "claude-opus-4-8", "gpt-4o": "", "gemini-2.5-pro": "",
	}
	for in, want := range cases {
		if got := claudeModelOverride(in); got != want {
			t.Errorf("claudeModelOverride(%q)=%q, mau %q", in, got, want)
		}
	}
}

func TestChatGeminiDisabled(t *testing.T) {
	s := newAPITestServer(&fakeRunner{}) // s.gemini nil
	body := `{"model":"gemini-2.5-pro","messages":[{"role":"user","content":"hai"}]}`
	rr := doChat(s, http.MethodPost, "Bearer rahasia", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("model gemini tanpa GEMINI_ENABLED harus 400, dapat %d", rr.Code)
	}
}

func TestChatRunsInParallel(t *testing.T) {
	br := &blockingRunner{started: make(chan struct{}, 2), release: make(chan struct{})}
	s := newAPITestServer(br)

	body := `{"messages":[{"role":"user","content":"x"}]}`
	go doChat(s, http.MethodPost, "Bearer rahasia", body)
	go doChat(s, http.MethodPost, "Bearer rahasia", body)

	for i := 0; i < 2; i++ {
		select {
		case <-br.started:
		case <-time.After(2 * time.Second):
			t.Fatal("request kedua tidak jalan paralel (terblok/diantrikan)")
		}
	}
	close(br.release)
}

func TestChatConcurrencyLimit(t *testing.T) {
	br := &blockingRunner{started: make(chan struct{}, 2), release: make(chan struct{})}
	s := newAPITestServer(br)
	s.apiSem = make(chan struct{}, 1)

	body := `{"messages":[{"role":"user","content":"x"}]}`
	go doChat(s, http.MethodPost, "Bearer rahasia", body)
	<-br.started // slot satu-satunya terpakai

	rr := doChat(s, http.MethodPost, "Bearer rahasia", body)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("saat penuh harus 429, dapat %d", rr.Code)
	}
	close(br.release)
}

func TestModels(t *testing.T) {
	s := newAPITestServer(&fakeRunner{})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer rahasia")
	rr := httptest.NewRecorder()
	s.handleModels(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("harus 200, dapat %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), apiModelName) {
		t.Errorf("daftar model harus memuat %q: %s", apiModelName, rr.Body.String())
	}
}

func TestBuildPrompt(t *testing.T) {
	cases := []struct {
		name string
		msgs []chatMessage
		want string
	}{
		{
			"user tunggal",
			[]chatMessage{{Role: "user", Content: json.RawMessage(`"halo"`)}},
			"halo",
		},
		{
			"system + user",
			[]chatMessage{
				{Role: "system", Content: json.RawMessage(`"S"`)},
				{Role: "user", Content: json.RawMessage(`"U"`)},
			},
			"S\n\nU",
		},
		{
			"banyak giliran",
			[]chatMessage{
				{Role: "user", Content: json.RawMessage(`"a"`)},
				{Role: "assistant", Content: json.RawMessage(`"b"`)},
				{Role: "user", Content: json.RawMessage(`"c"`)},
			},
			"User: a\n\nAssistant: b\n\nUser: c",
		},
		{
			"content array",
			[]chatMessage{{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"x"},{"type":"text","text":"y"}]`)}},
			"xy",
		},
	}
	for _, c := range cases {
		if got := buildPrompt(c.msgs); got != c.want {
			t.Errorf("%s: dapat %q, mau %q", c.name, got, c.want)
		}
	}
}
