// Package approval menjembatani permintaan izin tool dari Claude Code ke
// persetujuan pengguna lewat Telegram. MCP server memanggil Request (yang
// memblokir sampai pengguna menekan tombol atau waktu habis); handler webhook
// memanggil Resolve saat tombol ditekan.
package approval

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Notifier mengirim permintaan izin & memfinalisasi pesannya (implementasi
// nyata memakai Telegram). Diabstraksikan agar Registry bisa diuji.
type Notifier interface {
	// AskApproval mengirim pesan dengan tombol Izinkan/Tolak untuk reqID,
	// mengembalikan message_id pesan tersebut.
	AskApproval(ctx context.Context, chatID int64, reqID, toolName, summary string) (int64, error)
	// Finalize mengubah pesan izin menjadi teks akhir & menghapus tombol.
	Finalize(ctx context.Context, chatID, messageID int64, text string)
}

// Registry melacak permintaan izin yang sedang menunggu. Aman dari banyak
// goroutine.
type Registry struct {
	mu         sync.Mutex
	activeChat int64
	seq        int64
	pending    map[string]*pendingReq

	notifier Notifier
	timeout  time.Duration
	format   string // "behavior" | "hook"
}

type pendingReq struct {
	ch       chan bool
	chatID   int64
	msgID    int64
	toolName string
}

// New membuat Registry.
func New(notifier Notifier, timeout time.Duration, format string) *Registry {
	return &Registry{
		pending:  map[string]*pendingReq{},
		notifier: notifier,
		timeout:  timeout,
		format:   format,
	}
}

// Activate menandai chat yang sedang menjalankan prompt. Karena prompt
// diserialisasi (satu per satu), permintaan izin yang masuk merujuk ke chat ini.
func (r *Registry) Activate(chatID int64) {
	r.mu.Lock()
	r.activeChat = chatID
	r.mu.Unlock()
}

// Deactivate menghapus chat aktif setelah prompt selesai.
func (r *Registry) Deactivate() {
	r.mu.Lock()
	r.activeChat = 0
	r.mu.Unlock()
}

// Request memblokir sampai pengguna memutuskan, waktu habis, atau ctx batal,
// lalu mengembalikan true bila diizinkan.
func (r *Registry) Request(ctx context.Context, toolName string, input json.RawMessage) bool {
	r.mu.Lock()
	chatID := r.activeChat
	if chatID == 0 {
		r.mu.Unlock()
		return false // tak ada chat aktif: tolak demi aman
	}
	r.seq++
	reqID := "r" + strconv.FormatInt(r.seq, 10)
	p := &pendingReq{ch: make(chan bool, 1), chatID: chatID, toolName: toolName}
	r.pending[reqID] = p
	r.mu.Unlock()

	defer r.remove(reqID)

	msgID, err := r.notifier.AskApproval(ctx, chatID, reqID, toolName, Summarize(input))
	if err != nil {
		return false
	}
	r.mu.Lock()
	p.msgID = msgID
	r.mu.Unlock()

	timer := time.NewTimer(r.timeout)
	defer timer.Stop()

	select {
	case allow := <-p.ch:
		return allow
	case <-timer.C:
		r.notifier.Finalize(context.Background(), chatID, msgID,
			"⏱️ Permintaan izin kedaluwarsa — otomatis DITOLAK.\nTool: "+toolName)
		return false
	case <-ctx.Done():
		return false
	}
}

// Resolve dipanggil saat tombol ditekan. Mengembalikan chatID & msgID pesan izin
// agar pemanggil bisa memfinalisasinya; ok=false bila reqID tak dikenal/usang.
func (r *Registry) Resolve(reqID string, allow bool) (chatID, msgID int64, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, found := r.pending[reqID]
	if !found {
		return 0, 0, false
	}
	select {
	case p.ch <- allow:
	default: // sudah terisi
	}
	return p.chatID, p.msgID, true
}

func (r *Registry) remove(reqID string) {
	r.mu.Lock()
	delete(r.pending, reqID)
	r.mu.Unlock()
}

// Decision menyusun string JSON hasil izin sesuai format yang dipilih, untuk
// dikembalikan oleh tool MCP permission-prompt.
//
// format "behavior" (default, kontrak permission-prompt-tool):
//
//	allow -> {"behavior":"allow","updatedInput":<input>}
//	deny  -> {"behavior":"deny","message":"..."}
//
// format "hook" (alternatif gaya PermissionRequest hook):
//
//	{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow|deny"}}}
func Decision(format string, allow bool, input json.RawMessage, denyMsg string) string {
	if format == "hook" {
		behavior := "deny"
		if allow {
			behavior = "allow"
		}
		out := map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName": "PermissionRequest",
				"decision":      map[string]any{"behavior": behavior},
			},
		}
		b, _ := json.Marshal(out)
		return string(b)
	}

	// format "behavior"
	if allow {
		ui := input
		if len(strings.TrimSpace(string(ui))) == 0 {
			ui = json.RawMessage("{}")
		}
		out := map[string]json.RawMessage{
			"behavior":     json.RawMessage(`"allow"`),
			"updatedInput": ui,
		}
		b, _ := json.Marshal(out)
		return string(b)
	}
	if denyMsg == "" {
		denyMsg = "Ditolak oleh pengguna."
	}
	b, _ := json.Marshal(map[string]string{"behavior": "deny", "message": denyMsg})
	return string(b)
}

// Summarize membuat ringkasan singkat & manusiawi dari input tool untuk
// ditampilkan di pesan izin.
func Summarize(input json.RawMessage) string {
	if len(input) == 0 {
		return "(tanpa argumen)"
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return truncate(string(input), 500)
	}
	// Tonjolkan field yang paling informatif bila ada.
	for _, key := range []string{"command", "file_path", "path", "url", "pattern", "query"} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return truncate(key+": "+s, 500)
			}
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return truncate(string(input), 500)
	}
	return truncate(string(b), 500)
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
