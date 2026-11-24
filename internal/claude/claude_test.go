package claude

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/awangga/rprompt/internal/toolcall"
)

func TestParseStreamLineSystemInit(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"init","session_id":"abc-123"}`)
	evs, ok := parseStreamLine(line)
	if !ok || len(evs) != 1 || evs[0].SessionID != "abc-123" {
		t.Fatalf("event init gagal diparse: ok=%v evs=%#v", ok, evs)
	}
}

func TestParseStreamLineAssistantText(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Halo"}]}}`)
	evs, ok := parseStreamLine(line)
	if !ok || len(evs) != 1 || evs[0].AppendText != "Halo" {
		t.Fatalf("teks asisten gagal diparse: %#v", evs)
	}
}

func TestParseStreamLineToolUse(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"sebentar"},{"type":"tool_use","name":"Bash","input":{}}]}}`)
	evs, ok := parseStreamLine(line)
	if !ok || len(evs) != 2 {
		t.Fatalf("harus 2 event (teks + tool), dapat %#v", evs)
	}
	if evs[0].AppendText != "sebentar" || evs[1].ToolName != "Bash" {
		t.Fatalf("event tidak sesuai: %#v", evs)
	}
}

func TestParseStreamLineResult(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"selesai","session_id":"xyz"}`)
	evs, ok := parseStreamLine(line)
	if !ok || len(evs) != 2 {
		// 1 event session_id + 1 event final
		t.Fatalf("harus 2 event, dapat %#v", evs)
	}
	var final *Result
	for _, e := range evs {
		if e.Final != nil {
			final = e.Final
		}
	}
	if final == nil || final.Text != "selesai" || final.SessionID != "xyz" || final.IsError {
		t.Fatalf("result final tidak sesuai: %#v", final)
	}
}

func TestParseStreamLineIgnoresJunk(t *testing.T) {
	for _, line := range [][]byte{[]byte(""), []byte("  "), []byte("bukan json"), []byte(`{"type":"user"}`)} {
		if _, ok := parseStreamLine(line); ok {
			t.Errorf("baris %q seharusnya diabaikan", line)
		}
	}
}

func TestBuildArgs(t *testing.T) {
	r := &Runner{Bin: "claude", ExtraArgs: []string{"--dangerously-skip-permissions"}}

	got := r.buildArgs("", "")
	want := []string{"-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"}
	if !equal(got, want) {
		t.Fatalf("tanpa sesi: dapat %#v", got)
	}

	got = r.buildArgs("sess-1", "")
	want = []string{"-p", "--output-format", "stream-json", "--verbose", "--resume", "sess-1", "--dangerously-skip-permissions"}
	if !equal(got, want) {
		t.Fatalf("dengan sesi: dapat %#v", got)
	}

	got = r.buildArgs("", "opus")
	want = []string{"-p", "--output-format", "stream-json", "--verbose", "--model", "opus", "--dangerously-skip-permissions"}
	if !equal(got, want) {
		t.Fatalf("dengan model: dapat %#v", got)
	}

	got = r.buildArgs(ResumeLatest, "")
	want = []string{"-p", "--output-format", "stream-json", "--verbose", "--continue", "--dangerously-skip-permissions"}
	if !equal(got, want) {
		t.Fatalf("ResumeLatest harus --continue: dapat %#v", got)
	}
}

func TestBuildArgsPermission(t *testing.T) {
	r := &Runner{
		Bin:            "claude",
		PermissionArgs: []string{"--permission-prompt-tool", "mcp__rprompt__approval_prompt", "--mcp-config", "/tmp/x.json"},
	}
	got := r.buildArgs("", "")
	want := []string{
		"-p", "--output-format", "stream-json", "--verbose",
		"--permission-prompt-tool", "mcp__rprompt__approval_prompt", "--mcp-config", "/tmp/x.json",
	}
	if !equal(got, want) {
		t.Fatalf("dapat %#v", got)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Mode tool tidak boleh membawa tool bawaan, MCP, atau ExtraArgs.
func TestRunToolsMengunci(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("memakai skrip sh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nfor a in \"$@\"; do echo \"[$a]\"; done > " + dir + "/args\nls -A > " + dir + "/ls\n" +
		"while [ $# -gt 0 ]; do [ \"$1\" = --system-prompt-file ] && cat \"$2\" > " + dir + "/sys; shift; done\ncat > /dev/null\n" +
		`echo '{"type":"result","result":"ok","usage":{"input_tokens":1,"cache_read_input_tokens":2,"cache_creation_input_tokens":4,"output_tokens":3}}'`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Bin: bin, ExtraArgs: []string{"--dangerously-skip-permissions"}, PermissionArgs: []string{"--mcp-config", "x"}}
	rep, err := r.RunTools(context.Background(), toolcall.Request{System: "SIS", Prompt: "PRO", Model: "haiku"})
	if err != nil || rep.Text != "ok" || rep.InTokens != 7 || rep.OutTokens != 3 {
		t.Fatalf("RunTools = %+v, %v", rep, err)
	}
	read := func(n string) string { b, _ := os.ReadFile(filepath.Join(dir, n)); return string(b) }
	args := read("args")
	for _, want := range []string{"[--tools]\n[]", "[--strict-mcp-config]", "[--setting-sources]\n[]", "[--model]\n[haiku]"} {
		if !strings.Contains(args, want) {
			t.Errorf("args tanpa %q: %s", want, args)
		}
	}
	if strings.Contains(args, "dangerously") || strings.Contains(args, "--mcp-config]") && !strings.Contains(args, "strict-mcp-config]") {
		t.Errorf("ExtraArgs/PermissionArgs tidak boleh ikut: %s", args)
	}
	if read("ls") != "" || read("sys") != "SIS" {
		t.Errorf("folder kerja %q, system %q", read("ls"), read("sys"))
	}
}
