package server

import (
	"testing"

	"github.com/awangga/rprompt/internal/config"
)

func TestChatWorkDir(t *testing.T) {
	s := &Server{
		cfg: &config.Config{WorkDir: "/default"},
		cwd: map[int64]string{},
	}

	// Default sebelum diubah.
	if got := s.chatWorkDir(7); got != "/default" {
		t.Fatalf("default workdir salah: %q", got)
	}

	// Override per-chat.
	s.setChatWorkDir(7, "/proyek/a")
	if got := s.chatWorkDir(7); got != "/proyek/a" {
		t.Fatalf("override workdir salah: %q", got)
	}

	// Chat lain tetap default.
	if got := s.chatWorkDir(99); got != "/default" {
		t.Fatalf("chat lain harus default, dapat %q", got)
	}
}

func TestIsGeminiModel(t *testing.T) {
	cases := map[string]bool{
		"gemini-2.5-flash": true,
		"Gemini-2.5-pro":   true,
		"  gemini  ":       true,
		"claude-code":      false,
		"gpt-4o":           false,
		"":                 false,
	}
	for model, want := range cases {
		if got := isGeminiModel(model); got != want {
			t.Errorf("isGeminiModel(%q)=%v, mau %v", model, got, want)
		}
	}
}
