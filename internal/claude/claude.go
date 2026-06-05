// Package claude menjalankan Claude Code CLI dalam mode headless streaming
// (`claude -p --output-format stream-json`) dan mem-parse alirannya menjadi
// event yang bisa ditampilkan secara bertahap. Prompt dikirim lewat stdin agar
// aman dari masalah panjang/escaping argumen.
package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Runner mengeksekusi binary claude dengan konfigurasi tetap.
type Runner struct {
	Bin            string   // path binary claude
	WorkDir        string   // direktori kerja
	ExtraArgs      []string // argumen tambahan (mis. --dangerously-skip-permissions)
	PermissionArgs []string // argumen izin interaktif (--permission-prompt-tool, --mcp-config)
}

// Result adalah hasil akhir satu eksekusi prompt.
type Result struct {
	Text      string // teks jawaban final
	SessionID string // session id untuk melanjutkan percakapan berikutnya
	IsError   bool   // true bila claude melaporkan error
}

// StreamEvent adalah satu kejadian yang diparse dari aliran output claude.
// Hanya satu field "isi" yang terisi per event.
type StreamEvent struct {
	SessionID  string  // terisi saat session id ditemukan
	AppendText string  // teks asisten yang harus ditambahkan ke tampilan
	ToolName   string  // nama tool, terisi saat asisten memakai tool
	Final      *Result // terisi pada event "result" (akhir)
}

// streamMsg memetakan satu baris JSON dari --output-format stream-json.
type streamMsg struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Message   *struct {
		Content []struct {
			Type string `json:"type"` // "text" | "tool_use" | "thinking" | ...
			Text string `json:"text"`
			Name string `json:"name"` // nama tool untuk tool_use
		} `json:"content"`
	} `json:"message"`
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

// ResumeLatest dipakai sebagai nilai sessionID untuk melanjutkan sesi TERAKHIR
// di direktori kerja (claude --continue) alih-alih --resume <id> tertentu.
// Berguna untuk menyambung sesi Claude Code interaktif yang dibuat di PC.
const ResumeLatest = "\x00continue"

// buildArgs menyusun argumen CLI untuk satu eksekusi streaming. model kosong =
// pakai model default langganan.
func (r *Runner) buildArgs(sessionID, model string) []string {
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
	switch {
	case sessionID == ResumeLatest:
		args = append(args, "--continue")
	case sessionID != "":
		args = append(args, "--resume", sessionID)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, r.PermissionArgs...)
	args = append(args, r.ExtraArgs...)
	return args
}

// RunStream menjalankan satu prompt dan memanggil onEvent untuk tiap kejadian
// (teks bertahap, pemakaian tool, lalu hasil akhir). Bila sessionID tidak
// kosong, percakapan sebelumnya dilanjutkan via --resume. Mengembalikan Result
// final termasuk session id baru yang harus disimpan.
func (r *Runner) RunStream(ctx context.Context, prompt, sessionID, workDir, model string, onEvent func(StreamEvent)) (Result, error) {
	if onEvent == nil {
		onEvent = func(StreamEvent) {}
	}
	if workDir == "" {
		workDir = r.WorkDir
	}

	cmd := exec.CommandContext(ctx, r.Bin, r.buildArgs(sessionID, model)...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(prompt)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}

	result := Result{SessionID: sessionID}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024) // baris bisa besar (hasil tool)
	for sc.Scan() {
		events, ok := parseStreamLine(sc.Bytes())
		if !ok {
			continue
		}
		for _, ev := range events {
			if ev.SessionID != "" {
				result.SessionID = ev.SessionID
			}
			if ev.Final != nil {
				result.Text = ev.Final.Text
				result.IsError = ev.Final.IsError
				if ev.Final.SessionID != "" {
					result.SessionID = ev.Final.SessionID
				}
			}
			onEvent(ev)
		}
	}
	scanErr := sc.Err()
	waitErr := cmd.Wait()

	if waitErr != nil {
		// Bila proses gagal dan belum ada teks, kembalikan pesan error.
		if strings.TrimSpace(result.Text) == "" {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = waitErr.Error()
			}
			result.Text = msg
			result.IsError = true
		}
		return result, fmt.Errorf("claude gagal: %w", waitErr)
	}
	if scanErr != nil {
		return result, fmt.Errorf("baca output claude: %w", scanErr)
	}
	return result, nil
}

// parseStreamLine mem-parse satu baris JSON menjadi nol atau lebih StreamEvent.
// Mengembalikan ok=false untuk baris kosong atau yang gagal diparse.
func parseStreamLine(line []byte) ([]StreamEvent, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, false
	}
	var m streamMsg
	if err := json.Unmarshal(line, &m); err != nil {
		return nil, false
	}

	var evs []StreamEvent
	if m.SessionID != "" {
		evs = append(evs, StreamEvent{SessionID: m.SessionID})
	}
	switch m.Type {
	case "assistant":
		if m.Message != nil {
			for _, b := range m.Message.Content {
				switch b.Type {
				case "text":
					if b.Text != "" {
						evs = append(evs, StreamEvent{AppendText: b.Text})
					}
				case "tool_use":
					if b.Name != "" {
						evs = append(evs, StreamEvent{ToolName: b.Name})
					}
				}
			}
		}
	case "result":
		evs = append(evs, StreamEvent{Final: &Result{
			Text:      m.Result,
			SessionID: m.SessionID,
			IsError:   m.IsError,
		}})
	}
	if len(evs) == 0 {
		return nil, false
	}
	return evs, true
}
