// Package codex menjalankan Codex CLI (`codex exec --json`) sebagai engine
// alternatif pada API OpenAI-compatible. Sandbox selalu read-only.
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/awangga/rprompt/internal/toolcall"
)

// Runner mengeksekusi binary codex.
type Runner struct {
	Bin       string
	ExtraArgs []string
}

// New membuat Runner.
func New(bin string, extraArgs []string) *Runner {
	return &Runner{Bin: bin, ExtraArgs: extraArgs}
}

// toolFeatures adalah fitur Codex yang memberi model tool sendiri. Ditulis lewat
// -c (bukan --disable) agar nama yang tak dikenal versi lain tidak jadi error.
var toolFeatures = []string{
	"shell_tool", "unified_exec", "apps", "plugins", "browser_use", "computer_use",
	"image_generation", "view_image", "goals", "hooks", "skill_search", "sleep_tool",
	"tool_suggest", "multi_agent", "multi_agent_v2", "code_mode_host", "workspace_dependencies",
}

func (r *Runner) args(model, effort, instructions string) []string {
	args := []string{"exec", "--json", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only"}
	for _, a := range r.ExtraArgs {
		// Mode tool: jangan sampai sandbox dilepas.
		if instructions != "" && (strings.HasPrefix(a, "--dangerously-") || a == "--yolo") {
			continue
		}
		args = append(args, a)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+tomlString(effort))
	}
	if instructions != "" {
		// Config pengguna dilewati: bisa memuat server MCP, yaitu tool lain.
		args = append(args, "--ignore-user-config", "--ignore-rules",
			"-c", "model_instructions_file="+tomlString(instructions),
			"-c", `web_search="disabled"`, "-c", "project_doc_max_bytes=0",
			"-c", "skills.include_instructions=false")
		for _, f := range toolFeatures {
			args = append(args, "-c", "features."+f+"=false")
		}
	}
	return append(args, "-") // prompt dari stdin
}

func tomlString(s string) string {
	b, _ := json.Marshal(s) // string JSON juga string dasar TOML yang sah
	return string(b)
}

// Run menjalankan prompt di workDir (read-only) dan mengembalikan jawabannya.
func (r *Runner) Run(ctx context.Context, prompt, workDir, model string) (string, error) {
	rep, err := r.exec(ctx, r.args(MapModel(model), "", ""), prompt, workDir)
	return rep.Text, err
}

// RunTools menjawab satu giliran dengan semua tool bawaan Codex dimatikan.
func (r *Runner) RunTools(ctx context.Context, req toolcall.Request) (toolcall.Reply, error) {
	dir, cleanup, err := toolcall.TempDir()
	if err != nil {
		return toolcall.Reply{}, err
	}
	defer cleanup()
	work := filepath.Join(dir, "kerja")
	if err := os.Mkdir(work, 0o700); err != nil {
		return toolcall.Reply{}, err
	}
	instr := filepath.Join(dir, "instructions.md")
	if err := os.WriteFile(instr, []byte(req.System), 0o600); err != nil {
		return toolcall.Reply{}, err
	}
	// Aturan tool diulang di prompt: Codex tetap menyertakan satu tool bawaan
	// (request_user_input), dan sebagian model lalu mengira itu satu-satunya tool.
	prompt := req.System + "\n\n# Conversation\n\n" + req.Prompt
	return r.exec(ctx, r.args(MapModel(req.Model), req.Effort, instr), prompt, work)
}

// MapModel mengambil nama model Codex: "codex" -> default, "codex/x" -> x.
func MapModel(model string) string {
	m := strings.TrimSpace(model)
	if strings.EqualFold(m, "codex") {
		return ""
	}
	return strings.TrimPrefix(m, "codex/")
}

func (r *Runner) exec(ctx context.Context, args []string, prompt, workDir string) (toolcall.Reply, error) {
	cmd := r.command(ctx, args)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(prompt)
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	rep, err := parse(stdout.Bytes())
	if err == nil && runErr != nil && rep.Text == "" {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = runErr.Error()
		}
		err = errors.New(msg)
	}
	if err != nil {
		return rep, fmt.Errorf("codex gagal: %w", err)
	}
	return rep, nil
}

// parse membaca event JSONL: jawaban adalah item agent_message terakhir.
func parse(out []byte) (toolcall.Reply, error) {
	var rep toolcall.Reply
	var failed string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var ev struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Usage struct {
				In  int `json:"input_tokens"`
				Out int `json:"output_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "item.completed":
			if ev.Item.Type == "agent_message" {
				rep.Text = ev.Item.Text
			}
		case "turn.completed":
			rep.InTokens, rep.OutTokens = ev.Usage.In, ev.Usage.Out
		case "turn.failed":
			failed = "giliran gagal"
			if ev.Error != nil && ev.Error.Message != "" {
				failed = ev.Error.Message
			}
		}
	}
	if failed != "" {
		return rep, errors.New(failed)
	}
	return rep, sc.Err()
}

// command menyusun *exec.Cmd; di Windows shim .cmd dibungkus `cmd /c`.
func (r *Runner) command(ctx context.Context, args []string) *exec.Cmd {
	bin := r.Bin
	if runtime.GOOS == "windows" {
		if !strings.ContainsAny(bin, `\/`) {
			if p, err := exec.LookPath(bin); err == nil {
				bin = p
			}
		}
		if l := strings.ToLower(bin); strings.HasSuffix(l, ".cmd") || strings.HasSuffix(l, ".bat") {
			return exec.CommandContext(ctx, "cmd", append([]string{"/c", bin}, args...)...)
		}
	}
	return exec.CommandContext(ctx, bin, args...)
}
