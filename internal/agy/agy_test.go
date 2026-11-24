package agy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/awangga/rprompt/internal/toolcall"
)

func TestErrorDuaBentuk(t *testing.T) {
	for _, in := range []string{`{"error":"rusak"}`, `{"error":{"message":"rusak"}}`} {
		var c cliJSON
		if err := json.Unmarshal([]byte(in), &c); err != nil || c.Error != "rusak" {
			t.Errorf("%s -> %+v, %v", in, c, err)
		}
	}
}

// RunTools harus mengunci tool bawaan, tanpa ExtraArgs, di folder kosong.
func TestRunToolsMengunci(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("memakai skrip sh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "agy")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + dir + "/args\nls -A > " + dir + "/ls\ncat > " + dir + "/stdin\n" +
		`echo '{"response":"ok","usage":{"input_tokens":5,"cache_read_tokens":2,"output_tokens":3}}'`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := New(bin, []string{"--dangerously-skip-permissions"}, false)
	rep, err := r.RunTools(context.Background(), toolcall.Request{System: "SIS", Prompt: "PRO", Model: "agy/m1", Effort: "low"})
	if err != nil || rep.Text != "ok" || rep.InTokens != 7 || rep.OutTokens != 3 {
		t.Fatalf("RunTools = %+v, %v", rep, err)
	}
	read := func(n string) string { b, _ := os.ReadFile(filepath.Join(dir, n)); return string(b) }
	args := read("args")
	for _, want := range []string{"--settings", "command(*)", "write_file(*)", "mcp(*)", "--model\nm1", "--effort\nlow"} {
		if !strings.Contains(args, want) {
			t.Errorf("args tanpa %q: %s", want, args)
		}
	}
	if strings.Contains(args, "dangerously") {
		t.Errorf("ExtraArgs tidak boleh ikut: %s", args)
	}
	if read("ls") != "" || !strings.HasPrefix(read("stdin"), "SIS") || !strings.Contains(read("stdin"), "PRO") {
		t.Errorf("folder kerja %q, stdin %q", read("ls"), read("stdin"))
	}
}
