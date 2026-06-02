// Package gemini menjalankan Gemini CLI (`gemini`) dalam mode headless
// (`gemini -p "..." --output-format json`) dan mengambil teks jawabannya.
// Dipakai sebagai engine alternatif Claude pada API OpenAI-compatible.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Runner mengeksekusi binary gemini dengan konfigurasi tetap.
type Runner struct {
	Bin       string
	ExtraArgs []string
	// OAuthOnly: bila true, hapus GEMINI_API_KEY/GOOGLE_API_KEY dari environment
	// subprocess agar gemini memakai login CLI (OAuth) alih-alih API key.
	OAuthOnly bool
}

// New membuat Runner.
func New(bin string, extraArgs []string, oauthOnly bool) *Runner {
	return &Runner{Bin: bin, ExtraArgs: extraArgs, OAuthOnly: oauthOnly}
}

// command menyusun *exec.Cmd. Di Windows, binary .cmd/.bat (mis. shim npm
// gemini.cmd) tidak bisa dieksekusi langsung oleh Go, jadi dibungkus `cmd /c`.
func (r *Runner) command(ctx context.Context, args []string) *exec.Cmd {
	lower := strings.ToLower(r.Bin)
	if runtime.GOOS == "windows" && (strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat")) {
		return exec.CommandContext(ctx, "cmd", append([]string{"/c", r.Bin}, args...)...)
	}
	return exec.CommandContext(ctx, r.Bin, args...)
}

// env mengembalikan environment untuk subprocess. Bila OAuthOnly, GEMINI_API_KEY
// & GOOGLE_API_KEY dihapus agar gemini jatuh ke login CLI. Mengembalikan nil
// (warisi environment apa adanya) bila tidak perlu disaring.
func (r *Runner) env() []string {
	if !r.OAuthOnly {
		return nil
	}
	base := os.Environ()
	out := make([]string, 0, len(base))
	for _, e := range base {
		u := strings.ToUpper(e)
		if strings.HasPrefix(u, "GEMINI_API_KEY=") || strings.HasPrefix(u, "GOOGLE_API_KEY=") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// cliJSON memetakan output `--output-format json` dari gemini.
type cliJSON struct {
	Response string `json:"response"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Run menjalankan satu prompt dan mengembalikan teks jawaban. Prompt dikirim
// lewat stdin (gemini non-interaktif karena stdin/stdout di-pipe) agar aman dari
// masalah panjang/escaping argumen. workDir menjadi direktori kerja gemini
// (kosong = direktori kerja proses).
func (r *Runner) Run(ctx context.Context, prompt, workDir string) (string, error) {
	args := []string{"--output-format", "json"}
	args = append(args, r.ExtraArgs...)

	cmd := r.command(ctx, args)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if e := r.env(); e != nil {
		cmd.Env = e
	}
	cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	// Parse JSON terstruktur lebih dulu.
	var parsed cliJSON
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &parsed); err == nil {
		if parsed.Error != nil && parsed.Response == "" {
			return "", fmt.Errorf("gemini: %s", parsed.Error.Message)
		}
		return parsed.Response, nil
	}

	// Gagal parse: kembalikan error eksekusi dengan pesan dari stderr.
	if runErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = runErr.Error()
		}
		return "", fmt.Errorf("gemini gagal: %s", msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}
