package kiro

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

var (
	// ansiRegex digunakan untuk membersihkan kode warna dari output Kiro.
	ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	// footerRegex digunakan untuk membuang baris footer "▸ Credits: ... Time: ...".
	footerRegex = regexp.MustCompile(`▸\s*Credits:.*`)
)

// Runner mengeksekusi binary kiro-cli.
type Runner struct {
	Bin       string
	ExtraArgs []string
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

// Run menjalankan prompt dan mengembalikan teks jawaban bersih tanpa ANSI.
func (r *Runner) Run(ctx context.Context, prompt, workDir, model string) (string, error) {
	// kiro-cli chat --no-interactive
	// prompt dilempar via stdin untuk menghindari error "filename too long" di Windows
	args := []string{"chat", "--no-interactive"}
	args = append(args, r.ExtraArgs...)

	// Opsional: Jika kiro menerima flag --model, kita bisa meneruskannya
	if m := strings.TrimSpace(model); m != "" && !strings.EqualFold(m, "kiro") {
		args = append(args, "--model", m)
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
