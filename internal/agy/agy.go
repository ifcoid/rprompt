// Package agy menjalankan AGY CLI (`agy`) dalam mode headless
// (`agy -p "..." --output-format json`) dan mengambil teks jawabannya.
// Dipakai sebagai engine alternatif Claude pada API OpenAI-compatible.
package agy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/awangga/rprompt/internal/toolcall"
)

// Runner mengeksekusi binary agy dengan konfigurasi tetap.
type Runner struct {
	Bin       string
	ExtraArgs []string
	// OAuthOnly: bila true, hapus AGY_API_KEY/GOOGLE_API_KEY dari environment
	// subprocess agar agy memakai login CLI (OAuth) alih-alih API key.
	OAuthOnly bool
}

// New membuat Runner.
func New(bin string, extraArgs []string, oauthOnly bool) *Runner {
	return &Runner{Bin: bin, ExtraArgs: extraArgs, OAuthOnly: oauthOnly}
}

// command menyusun *exec.Cmd. Di Windows, binary .cmd/.bat (mis. shim npm
// agy.cmd) tidak bisa dieksekusi langsung oleh Go, jadi dibungkus `cmd /c`.
// Nama pendek (mis. "agy") di-resolve via PATH dulu agar ekstensinya
// terdeteksi, sehingga full path tidak wajib.
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

// env mengembalikan environment untuk subprocess. Bila OAuthOnly, AGY_API_KEY
// & GOOGLE_API_KEY dihapus agar agy jatuh ke login CLI. Mengembalikan nil
// (warisi environment apa adanya) bila tidak perlu disaring.
func (r *Runner) env() []string {
	if !r.OAuthOnly {
		return nil
	}
	base := os.Environ()
	out := make([]string, 0, len(base))
	for _, e := range base {
		u := strings.ToUpper(e)
		if strings.HasPrefix(u, "AGY_API_KEY=") || strings.HasPrefix(u, "GOOGLE_API_KEY=") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// cliJSON memetakan output `--output-format json` dari agy.
type cliJSON struct {
	Response string   `json:"response"`
	Error    cliError `json:"error"`
	Usage    struct {
		In     int `json:"input_tokens"`
		Cached int `json:"cache_read_tokens"`
		Out    int `json:"output_tokens"`
		Think  int `json:"thinking_tokens"`
	} `json:"usage"`
}

// cliError berupa string (agy 1.5) atau {"message": ...} (versi lama).
type cliError string

func (e *cliError) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*e = cliError(s)
		return nil
	}
	var o struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(b, &o)
	*e = cliError(o.Message)
	return nil
}

// Run menjalankan prompt dan mengembalikan teks jawaban, dengan retry untuk
// glitch sesaat AGY (mis. "Invalid stream / empty response / malformed tool
// call" atau model overloaded) yang biasanya pulih saat diulang.
func (r *Runner) Run(ctx context.Context, prompt, workDir, model string) (string, error) {
	args := append([]string{"--output-format", "json"}, r.ExtraArgs...)
	if m := mapModel(model); m != "" {
		args = append(args, "--model", m)
	}
	rep, err := r.retry(ctx, args, prompt, workDir)
	return rep.Text, err
}

// lockdown menolak semua tool bawaan agy yang menyentuh file, perintah, web, MCP.
const lockdown = `{"permissions":{"deny":["command(*)","read_file(*)","write_file(*)",` +
	`"read_url(*)","execute_url(*)","download_url(*)","mcp(*)"]}}`

// RunTools menjawab satu giliran dengan tool bawaan agy ditolak. ExtraArgs sengaja
// tidak dipakai: --dangerously-skip-permissions akan melawan penguncian ini.
func (r *Runner) RunTools(ctx context.Context, req toolcall.Request) (toolcall.Reply, error) {
	dir, cleanup, err := toolcall.TempDir()
	if err != nil {
		return toolcall.Reply{}, err
	}
	defer cleanup()
	args := []string{"--output-format", "json", "--disable-slash-commands", "--settings", lockdown}
	if m := mapModel(req.Model); m != "" {
		args = append(args, "--model", m)
	}
	if req.Effort != "" {
		args = append(args, "--effort", req.Effort)
	}
	return r.retry(ctx, args, req.System+"\n\n# Conversation\n\n"+req.Prompt, dir)
}

func (r *Runner) retry(ctx context.Context, args []string, prompt, workDir string) (toolcall.Reply, error) {
	const attempts = 3
	var lastErr error
	for i := 0; i < attempts; i++ {
		out, err := r.runOnce(ctx, args, prompt, workDir)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if ctx.Err() != nil || !isTransient(err) {
			return toolcall.Reply{}, err
		}
		select {
		case <-ctx.Done():
			return toolcall.Reply{}, ctx.Err()
		case <-time.After(time.Duration(i+1) * 600 * time.Millisecond):
		}
	}
	return toolcall.Reply{}, lastErr
}

// isTransient menandai error AGY yang layak di-retry.
func isTransient(err error) bool {
	s := strings.ToLower(err.Error())
	for _, sub := range []string{
		"invalid stream", "empty response", "malformed",
		"overloaded", "try again", "unavailable", "500", "503", "deadline",
	} {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// runOnce menjalankan satu eksekusi agy. Prompt dikirim lewat stdin (agy
// non-interaktif karena stdin/stdout di-pipe) agar aman dari masalah
// panjang/escaping. workDir = direktori kerja.
func (r *Runner) runOnce(ctx context.Context, args []string, prompt, workDir string) (toolcall.Reply, error) {
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
		if parsed.Error != "" && parsed.Response == "" {
			return toolcall.Reply{}, fmt.Errorf("agy: %s", parsed.Error)
		}
		u := parsed.Usage
		return toolcall.Reply{Text: parsed.Response, InTokens: u.In + u.Cached, OutTokens: u.Out + u.Think}, nil
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
		return toolcall.Reply{}, fmt.Errorf("agy gagal: %s", msg)
	}
	return toolcall.Reply{Text: strings.TrimSpace(stdout.String())}, nil
}

// mapModel memetakan request model OpenAI ke model ID resmi yang dikenali AGY CLI.
func mapModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case m == "" || m == "gemini" || m == "agy":
		return "" // pakai model default bawaan agy
	case strings.HasPrefix(m, "agy/"):
		return strings.TrimSpace(model)[len("agy/"):] // nama dari `agy models`
	case m == "gemini-2.5-flash" || m == "gemini-flash" || m == "gemini-3.8-flash":
		return "gemini-3.8-flash-medium"
	case m == "gemini-2.5-flash-lite" || m == "gemini-flash-low":
		return "gemini-3.8-flash-low"
	case m == "gemini-2.5-pro" || m == "gemini-pro" || m == "gemini-flash-high" || m == "gemini-3-pro-preview":
		return "gemini-3.8-flash-high"
	default:
		return strings.TrimSpace(model)
	}
}
