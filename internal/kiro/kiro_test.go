package kiro

import (
	"context"
	"testing"
	"time"
)

func TestMapModel(t *testing.T) {
	cases := map[string]string{
		"":                        "",
		"kiro":                    "",
		"kiro-auto":               "",
		"kiro/auto":               "",
		"auto":                    "",
		"kiro-claude-sonnet-4.5":  "claude-sonnet-4.5",
		"kiro/claude-sonnet-4.5":  "claude-sonnet-4.5",
		"kiro-sonnet":             "claude-sonnet-4.5",
		"claude-sonnet":           "claude-sonnet-4.5",
		"kiro-sonnet-4":           "claude-sonnet-4",
		"kiro-haiku":              "claude-haiku-4.5",
		"kiro/claude-haiku-4.5":   "claude-haiku-4.5",
		"kiro-deepseek-3.2":       "deepseek-3.2",
		"deepseek":                "deepseek-3.2",
		"deepseek-3.2":            "deepseek-3.2",
		"kiro-minimax-m2.5":       "minimax-m2.5",
		"minimax":                 "minimax-m2.5",
		"kiro-glm-5":              "glm-5",
		"glm":                     "glm-5",
		"kiro-qwen3-coder-next":   "qwen3-coder-next",
		"qwen":                    "qwen3-coder-next",
		"custom-unknown-model":    "custom-unknown-model",
	}

	for in, want := range cases {
		got := MapModel(in)
		if got != want {
			t.Errorf("MapModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestListModelsFallbackAndCache(t *testing.T) {
	r := New("binary-yang-pasti-tidak-ada-12345", nil)
	models := r.ListModels(context.Background())
	if len(models) == 0 {
		t.Fatal("ListModels harus mengembalikan model default saat binary tidak ditemukan")
	}

	// Tes caching: cache manual dan pastikan diambil kembali
	r.modelsCache = []string{"model-a", "model-b"}
	r.cacheTime = time.Now()
	cached := r.ListModels(context.Background())
	if len(cached) != 2 || cached[0] != "model-a" {
		t.Fatalf("ListModels harus mengembalikan hasil cache, dapat %v", cached)
	}
}
