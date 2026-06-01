package approval

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

func TestDecisionBehaviorAllow(t *testing.T) {
	out := Decision("behavior", true, json.RawMessage(`{"command":"ls"}`), "")
	var got struct {
		Behavior     string          `json:"behavior"`
		UpdatedInput json.RawMessage `json:"updatedInput"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output bukan JSON valid: %v (%s)", err, out)
	}
	if got.Behavior != "allow" {
		t.Errorf("behavior=%q, mau allow", got.Behavior)
	}
	if string(got.UpdatedInput) != `{"command":"ls"}` {
		t.Errorf("updatedInput=%s", got.UpdatedInput)
	}
}

func TestDecisionBehaviorAllowEmptyInput(t *testing.T) {
	out := Decision("behavior", true, nil, "")
	if want := `{"behavior":"allow","updatedInput":{}}`; out != want {
		t.Errorf("dapat %s, mau %s", out, want)
	}
}

func TestDecisionBehaviorDeny(t *testing.T) {
	out := Decision("behavior", false, nil, "nope")
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got["behavior"] != "deny" || got["message"] != "nope" {
		t.Errorf("dapat %v", got)
	}
}

func TestDecisionHook(t *testing.T) {
	out := Decision("hook", true, nil, "")
	var got struct {
		HookSpecificOutput struct {
			HookEventName string `json:"hookEventName"`
			Decision      struct {
				Behavior string `json:"behavior"`
			} `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output bukan JSON valid: %v (%s)", err, out)
	}
	if got.HookSpecificOutput.HookEventName != "PermissionRequest" {
		t.Errorf("hookEventName salah: %s", out)
	}
	if got.HookSpecificOutput.Decision.Behavior != "allow" {
		t.Errorf("behavior salah: %s", out)
	}
}

func TestSummarize(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`{"command":"npm test"}`, "command: npm test"},
		{`{"file_path":"/a/b.go","content":"x"}`, "file_path: /a/b.go"},
		{``, "(tanpa argumen)"},
		{`{"foo":"bar"}`, `{"foo":"bar"}`},
	}
	for _, c := range cases {
		if got := Summarize(json.RawMessage(c.in)); got != c.want {
			t.Errorf("Summarize(%q)=%q, mau %q", c.in, got, c.want)
		}
	}
}

// fakeNotifier merekam permintaan & finalisasi tanpa jaringan.
type fakeNotifier struct {
	asked     chan string // reqID yang diminta
	finalized atomic.Bool
}

func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{asked: make(chan string, 8)}
}

func (f *fakeNotifier) AskApproval(_ context.Context, _ int64, reqID, _, _ string) (int64, error) {
	f.asked <- reqID
	return 100, nil
}

func (f *fakeNotifier) Finalize(_ context.Context, _, _ int64, _ string) {
	f.finalized.Store(true)
}

func TestRegistryAllow(t *testing.T) {
	f := newFakeNotifier()
	reg := New(f, time.Second, "behavior")
	reg.Activate(7)

	go func() {
		reqID := <-f.asked
		reg.Resolve(reqID, true)
	}()

	if !reg.Request(context.Background(), "Bash", json.RawMessage(`{"command":"ls"}`)) {
		t.Fatal("harus diizinkan")
	}
}

func TestRegistryDeny(t *testing.T) {
	f := newFakeNotifier()
	reg := New(f, time.Second, "behavior")
	reg.Activate(7)

	go func() {
		reqID := <-f.asked
		reg.Resolve(reqID, false)
	}()

	if reg.Request(context.Background(), "Bash", nil) {
		t.Fatal("harus ditolak")
	}
}

func TestRegistryTimeout(t *testing.T) {
	f := newFakeNotifier()
	reg := New(f, 30*time.Millisecond, "behavior")
	reg.Activate(7)

	if reg.Request(context.Background(), "Bash", nil) {
		t.Fatal("timeout harus menolak")
	}
	if !f.finalized.Load() {
		t.Error("Finalize harus dipanggil saat timeout")
	}
}

func TestRegistryNoActiveChat(t *testing.T) {
	f := newFakeNotifier()
	reg := New(f, time.Second, "behavior")
	// Tidak Activate → tidak ada chat aktif.

	if reg.Request(context.Background(), "Bash", nil) {
		t.Fatal("tanpa chat aktif harus menolak")
	}
	if len(f.asked) != 0 {
		t.Error("tidak boleh mengirim permintaan saat tak ada chat aktif")
	}
}

func TestRegistryResolveUnknown(t *testing.T) {
	reg := New(newFakeNotifier(), time.Second, "behavior")
	if _, _, ok := reg.Resolve("tidak-ada", true); ok {
		t.Error("reqID tak dikenal harus ok=false")
	}
}
