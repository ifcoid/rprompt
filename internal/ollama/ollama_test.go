package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   []Message
		want []Message
	}{
		{
			name: "system setelah user dipindah ke awal",
			in:   []Message{{Role: "user", Content: "Siapa namamu?"}, {Role: "system", Content: "Namamu Budi."}},
			want: []Message{{Role: "system", Content: "Namamu Budi."}, {Role: "user", Content: "Siapa namamu?"}},
		},
		{
			name: "system di tengah digabung, user yang jadi bersebelahan digabung",
			in: []Message{
				{Role: "system", Content: "S1"}, {Role: "user", Content: "A"},
				{Role: "system", Content: "S2"}, {Role: "user", Content: "B"},
			},
			want: []Message{{Role: "system", Content: "S1\n\nS2"}, {Role: "user", Content: "A\n\nB"}},
		},
		{
			name: "assistant beruntun digabung",
			in: []Message{
				{Role: "user", Content: "A"}, {Role: "assistant", Content: "X"},
				{Role: "assistant", Content: "Y"}, {Role: "user", Content: "B"},
			},
			want: []Message{{Role: "user", Content: "A"}, {Role: "assistant", Content: "X\n\nY"}, {Role: "user", Content: "B"}},
		},
		{
			name: "developer = system, tool = user, kosong dibuang",
			in: []Message{
				{Role: "user", Content: "A"}, {Role: "developer", Content: "D"},
				{Role: "assistant", Content: "  "}, {Role: "tool", Content: "hasil"},
			},
			want: []Message{{Role: "system", Content: "D"}, {Role: "user", Content: "A\n\nhasil"}},
		},
		{
			name: "gambar ikut digabung",
			in: []Message{
				{Role: "user", Content: "lihat", Images: []string{"img1"}},
				{Role: "user", Images: []string{"img2"}},
			},
			want: []Message{{Role: "user", Content: "lihat", Images: []string{"img1", "img2"}}},
		},
		{
			name: "hanya system -> dikirim sebagai user",
			in:   []Message{{Role: "system", Content: "Halo"}},
			want: []Message{{Role: "user", Content: "Halo"}},
		},
		{
			name: "tanpa system tidak ditambah",
			in:   []Message{{Role: "User", Content: "A"}},
			want: []Message{{Role: "user", Content: "A"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Normalize() =\n  %#v\nwant\n  %#v", got, tt.want)
			}
		})
	}
}

func TestNormalizeDoesNotMutateInput(t *testing.T) {
	in := []Message{{Role: "user", Content: "a", Images: []string{"1"}}, {Role: "user", Content: "b", Images: []string{"2"}}}
	Normalize(in)
	if !reflect.DeepEqual(in[0].Images, []string{"1"}) {
		t.Errorf("input termodifikasi: %v", in[0].Images)
	}
}

func TestThinkForEffort(t *testing.T) {
	tests := map[string]any{"": nil, "none": false, "minimal": "low", "LOW": "low", "medium": "medium", "high": "high", "xhigh": "high"}
	for in, want := range tests {
		got, err := ThinkForEffort(in)
		if err != nil || got != want {
			t.Errorf("ThinkForEffort(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ThinkForEffort("turbo"); err == nil {
		t.Error("effort tak dikenal harus error")
	}
}

// fakeOllama meniru /api/chat: mencatat body request dan membalas NDJSON.
func fakeOllama(t *testing.T, handle func(w http.ResponseWriter, req ChatRequest)) (*Client, *[]ChatRequest) {
	t.Helper()
	var got []ChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("body tidak valid: %v", err)
		}
		got = append(got, req)
		handle(w, req)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL + "/v1/"), &got
}

func TestChatStreamsAndCollects(t *testing.T) {
	c, got := fakeOllama(t, func(w http.ResponseWriter, _ ChatRequest) {
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"","thinking":"hmm"},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"Hai"},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":" Budi"},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":12,"eval_count":7}`)
	})
	var deltas []string
	res, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}},
		func(content, thinking string) { deltas = append(deltas, content+"|"+thinking) })
	if err != nil {
		t.Fatal(err)
	}
	want := Result{Content: "Hai Budi", Thinking: "hmm", DoneReason: "stop", PromptTokens: 12, CompletionTokens: 7}
	if res != want {
		t.Errorf("result = %+v, want %+v", res, want)
	}
	if strings.Join(deltas, ",") != "|hmm,Hai|, Budi|" {
		t.Errorf("deltas = %q", deltas)
	}
	if !(*got)[0].Stream {
		t.Error("request harus stream=true")
	}
}

func TestChatRetriesWithoutThink(t *testing.T) {
	c, got := fakeOllama(t, func(w http.ResponseWriter, req ChatRequest) {
		if req.Think != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"\"m\" does not support thinking"}`)
			return
		}
		fmt.Fprintln(w, `{"message":{"content":"ok"},"done":true,"done_reason":"stop"}`)
	})
	res, err := c.Chat(context.Background(), ChatRequest{Model: "m", Think: "low"}, nil)
	if err != nil || res.Content != "ok" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(*got) != 2 || (*got)[1].Think != nil {
		t.Errorf("harus retry sekali tanpa think, dapat %+v", *got)
	}
}

func TestChatErrors(t *testing.T) {
	c, _ := fakeOllama(t, func(w http.ResponseWriter, req ChatRequest) {
		switch req.Model {
		case "hilang":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"model 'hilang' not found"}`)
		case "putus":
			fmt.Fprintln(w, `{"message":{"content":"setengah"},"done":false}`)
		default:
			fmt.Fprintln(w, `{"error":"kehabisan memori"}`)
		}
	})
	var ae *APIError
	_, err := c.Chat(context.Background(), ChatRequest{Model: "hilang"}, nil)
	if !errors.As(err, &ae) || ae.StatusCode != 404 || ae.Message != "model 'hilang' not found" {
		t.Errorf("404: err = %#v", err)
	}
	if _, err := c.Chat(context.Background(), ChatRequest{Model: "putus"}, nil); err == nil {
		t.Error("stream tanpa done harus error")
	}
	if _, err := c.Chat(context.Background(), ChatRequest{Model: "x"}, nil); err == nil || err.Error() != "kehabisan memori" {
		t.Errorf("error di tengah stream: %v", err)
	}
}

func TestModelsSkipsEmbedding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"models":[{"name":"qwen3.5:9b","capabilities":["completion","thinking"]},{"name":"bge-m3:latest","capabilities":["embedding"]},{"name":"lama:1b"}]}`)
	}))
	defer srv.Close()
	names, err := New(srv.URL).Models(context.Background())
	if err != nil || !reflect.DeepEqual(names, []string{"qwen3.5:9b", "lama:1b"}) {
		t.Errorf("Models() = %v, %v", names, err)
	}
}
