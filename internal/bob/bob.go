package bob

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
	// ansiRegex digunakan untuk membersihkan kode warna dari output.
	ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
)

// Runner mengeksekusi binary bob CLI.
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
	// bob <prompt> -y
	args := []string{prompt, "-y"}
	args = append(args, r.ExtraArgs...)

	// Opsional: Jika menerima flag --model, kita bisa meneruskannya
	if m := strings.TrimSpace(model); m != "" && !strings.EqualFold(m, "bob") && !strings.EqualFold(m, "bob-shell") {
		args = append(args, "--model", m)
	}

	cmd := r.command(ctx, args)
	if workDir != "" {
		cmd.Dir = workDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	outStr := stdout.String()
	// Bersihkan output ANSI codes
	cleanOut := ansiRegex.ReplaceAllString(outStr, "")
	cleanOut = strings.TrimSpace(cleanOut)

	if runErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = cleanOut
		}
		if msg == "" {
			msg = runErr.Error()
		}
		return "", fmt.Errorf("bob gagal: %s", msg)
	}

	return cleanOut, nil
}
