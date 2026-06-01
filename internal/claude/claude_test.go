package claude

import "testing"

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

	got := r.buildArgs("")
	want := []string{"-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"}
	if !equal(got, want) {
		t.Fatalf("tanpa sesi: dapat %#v", got)
	}

	got = r.buildArgs("sess-1")
	want = []string{"-p", "--output-format", "stream-json", "--verbose", "--resume", "sess-1", "--dangerously-skip-permissions"}
	if !equal(got, want) {
		t.Fatalf("dengan sesi: dapat %#v", got)
	}
}

func TestBuildArgsPermission(t *testing.T) {
	r := &Runner{
		Bin:            "claude",
		PermissionArgs: []string{"--permission-prompt-tool", "mcp__rprompt__approval_prompt", "--mcp-config", "/tmp/x.json"},
	}
	got := r.buildArgs("")
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
