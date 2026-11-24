package codex

import (
	"slices"
	"strings"
	"testing"
)

func TestArgsTools(t *testing.T) {
	r := New("codex", []string{"--oss", "--dangerously-bypass-approvals-and-sandbox"})
	a := strings.Join(r.args("gpt-x", "low", `/tmp/a "b".md`), " ")
	for _, want := range []string{
		"--sandbox read-only", "--oss", "--model gpt-x", `model_reasoning_effort="low"`,
		`model_instructions_file="/tmp/a \"b\".md"`, "features.shell_tool=false", "--ignore-user-config",
	} {
		if !strings.Contains(a, want) {
			t.Errorf("args tanpa %q: %s", want, a)
		}
	}
	if strings.Contains(a, "dangerously") {
		t.Errorf("sandbox bisa dilepas: %s", a)
	}
	if p := r.args("", "", ""); !slices.Contains(p, "--dangerously-bypass-approvals-and-sandbox") || slices.Contains(p, "--ignore-user-config") {
		t.Errorf("mode biasa harus memakai ExtraArgs apa adanya: %v", p)
	}
}

func TestMapModel(t *testing.T) {
	for in, want := range map[string]string{"codex": "", "Codex": "", "codex/gpt-x": "gpt-x"} {
		if got := MapModel(in); got != want {
			t.Errorf("MapModel(%q) = %q", in, got)
		}
	}
}

func TestParse(t *testing.T) {
	rep, err := parse([]byte(`{"type":"thread.started"}
{"type":"item.completed","item":{"type":"error","message":"peringatan"}}
{"type":"item.completed","item":{"type":"agent_message","text":"satu"}}
{"type":"item.completed","item":{"type":"agent_message","text":"dua"}}
{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":3}}
`))
	if err != nil || rep.Text != "dua" || rep.InTokens != 10 || rep.OutTokens != 3 {
		t.Errorf("parse = %+v, %v", rep, err)
	}
	if _, err := parse([]byte(`{"type":"turn.failed","error":{"message":"habis"}}`)); err == nil || err.Error() != "habis" {
		t.Errorf("error = %v", err)
	}
}
