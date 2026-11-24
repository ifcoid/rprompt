package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	// ansiRegex digunakan untuk membersihkan kode warna dari output Kiro.
	ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	// footerRegex digunakan untuk membuang baris footer "▸ Credits: ... Time: ...".
	footerRegex = regexp.MustCompile(`▸\s*Credits:.*`)

	// DefaultModels adalah daftar model resmi Kiro CLI bila deteksi realtime gagal.
	DefaultModels = []string{
		"auto",
		"claude-sonnet-4.5",
		"claude-sonnet-4",
		"claude-haiku-4.5",
		"deepseek-3.2",
		"minimax-m2.5",
		"minimax-m2.1",
		"glm-5",
		"qwen3-coder-next",
	}
)

// Runner mengeksekusi binary kiro-cli.
type Runner struct {
	Bin         string
	ExtraArgs   []string
	modelsCache []string
	cacheTime   time.Time
	cacheMu     sync.Mutex
}

// New membuat Runner.
func New(bin string, extraArgs []string) *Runner {
	return &Runner{Bin: bin, ExtraArgs: extraArgs}
}

func (r *Runner) command(ctx context.Context, args []string) *exec.Cmd {
	bin := r.Bin
	if runtime.GOOS == "windows" && !strings.ContainsAny(bin, `\/`) {
		if p, err := exec.LookPath(bin); err == nil {
			bin = p
		}
	}
	lower := strings.ToLower(bin)
	if runtime.GOOS == "windows" && (strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat")) {
		return exec.CommandContext(ctx, "cmd", append([]string{"/c", bin}, args...)...)
	}
	return exec.CommandContext(ctx, bin, args...)
}

// ListModels mengambil daftar model yang didukung Kiro CLI secara realtime dengan cache TTL 5 menit.
func (r *Runner) ListModels(ctx context.Context) []string {
	r.cacheMu.Lock()
	if len(r.modelsCache) > 0 && time.Since(r.cacheTime) < 5*time.Minute {
		cached := append([]string(nil), r.modelsCache...)
		r.cacheMu.Unlock()
		return cached
	}
	r.cacheMu.Unlock()

	// Cek realtime via: kiro-cli chat --list-models --format json
	cmdCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	cmd := r.command(cmdCtx, []string{"chat", "--list-models", "--format", "json"})
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err == nil {
		var resp struct {
			Models []struct {
				ModelID   string `json:"model_id"`
				ModelName string `json:"model_name"`
			} `json:"models"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &resp); err == nil && len(resp.Models) > 0 {
			var ids []string
			for _, m := range resp.Models {
				id := strings.TrimSpace(m.ModelID)
				if id == "" {
					id = strings.TrimSpace(m.ModelName)
				}
				if id != "" {
					ids = append(ids, id)
				}
			}
			if len(ids) > 0 {
				r.cacheMu.Lock()
				r.modelsCache = ids
				r.cacheTime = time.Now()
				r.cacheMu.Unlock()
				return ids
			}
		}
	}

	return DefaultModels
}

// MapModel memetakan request model OpenAI ke model ID resmi yang dikenali Kiro CLI.
func MapModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case m == "" || m == "kiro" || m == "kiro-auto" || m == "kiro/auto" || m == "auto":
		return "" // default bawaan kiro
	}

	m = strings.TrimPrefix(m, "kiro-")
	m = strings.TrimPrefix(m, "kiro/")

	switch m {
	case "sonnet", "claude-sonnet", "sonnet-4.5", "claude-3-5-sonnet":
		return "claude-sonnet-4.5"
	case "sonnet-4":
		return "claude-sonnet-4"
	case "haiku", "claude-haiku", "haiku-4.5":
		return "claude-haiku-4.5"
	case "deepseek", "deepseek-v3", "deepseek-chat":
		return "deepseek-3.2"
	case "minimax":
		return "minimax-m2.5"
	case "glm":
		return "glm-5"
	case "qwen", "qwen-coder":
		return "qwen3-coder-next"
	default:
		return m
	}
}

// Run menjalankan prompt dan mengembalikan teks jawaban bersih tanpa ANSI.
func (r *Runner) Run(ctx context.Context, prompt, workDir, model string) (string, error) {
	// kiro-cli chat --no-interactive
	// prompt dilempar via stdin untuk menghindari error "filename too long" di Windows
	args := []string{"chat", "--no-interactive"}
	args = append(args, r.ExtraArgs...)

	// Pemetaan model realtime untuk Kiro CLI
	if targetModel := MapModel(model); targetModel != "" {
		args = append(args, "--model", targetModel)
	}

	cmd := r.command(ctx, args)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	outStr := stdout.String()
	// Bersihkan output ANSI codes
	cleanOut := ansiRegex.ReplaceAllString(outStr, "")
	// Bersihkan prefix "> " yang biasa dikeluarkan Kiro di awal pesan
	cleanOut = strings.TrimPrefix(strings.TrimSpace(cleanOut), "> ")
	// Bersihkan footer
	cleanOut = footerRegex.ReplaceAllString(cleanOut, "")
	cleanOut = strings.TrimSpace(cleanOut)

	if runErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = cleanOut
		}
		if msg == "" {
			msg = runErr.Error()
		}
		return "", fmt.Errorf("kiro gagal: %s", msg)
	}

	return cleanOut, nil
}
