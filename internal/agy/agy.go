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
	Response string `json:"response"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Run menjalankan prompt dan mengembalikan teks jawaban, dengan retry untuk
// glitch sesaat AGY (mis. "Invalid stream / empty response / malformed tool
// call" atau model overloaded) yang biasanya pulih saat diulang.
func (r *Runner) Run(ctx context.Context, prompt, workDir, model string) (string, error) {
	const attempts = 3
	var lastErr error
	for i := 0; i < attempts; i++ {
		out, err := r.runOnce(ctx, prompt, workDir, model)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if ctx.Err() != nil || !isTransient(err) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(i+1) * 600 * time.Millisecond):
		}
	}
	return "", lastErr
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
// panjang/escaping. workDir = direktori kerja; model spesifik diteruskan ke -m.
func (r *Runner) runOnce(ctx context.Context, prompt, workDir, model string) (string, error) {
	args := []string{"--output-format", "json"}
	args = append(args, r.ExtraArgs...)
	if m := strings.TrimSpace(model); m != "" && !strings.EqualFold(m, "gemini") {
		args = append(args, "-m", m)
	}

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
			return "", fmt.Errorf("agy: %s", parsed.Error.Message)
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
		return "", fmt.Errorf("agy gagal: %s", msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}
