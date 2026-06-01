package server

import (
	"context"
	"strings"
	"testing"
)

// fakeSink merekam pesan terkirim & editan, mensimulasikan Telegram.
type fakeSink struct {
	nextID   int64
	messages map[int64]*string // message_id -> teks terkini
	order    []int64           // urutan pembuatan
}

func newFakeSink() *fakeSink {
	return &fakeSink{messages: map[int64]*string{}}
}

func (f *fakeSink) Send(_ context.Context, _ int64, text string) (int64, error) {
	f.nextID++
	id := f.nextID
	t := text
	f.messages[id] = &t
	f.order = append(f.order, id)
	return id, nil
}

func (f *fakeSink) EditMessageText(_ context.Context, _ int64, messageID int64, text string) error {
	t := text
	f.messages[messageID] = &t
	return nil
}

// combined mengembalikan gabungan isi semua pesan sesuai urutan pembuatan.
func (f *fakeSink) combined() string {
	var parts []string
	for _, id := range f.order {
		parts = append(parts, *f.messages[id])
	}
	return strings.Join(parts, "")
}

func TestLiveMessageSingle(t *testing.T) {
	f := newFakeSink()
	lm := newLiveMessage(f, 1)
	lm.minInterval = 0 // nonaktifkan throttle untuk determinisme

	ctx := context.Background()
	_ = lm.set(ctx, "Ha")
	_ = lm.set(ctx, "Halo du")
	_ = lm.flush(ctx, "Halo dunia")

	if len(f.order) != 1 {
		t.Fatalf("harus tetap 1 pesan, dapat %d", len(f.order))
	}
	if got := f.combined(); got != "Halo dunia" {
		t.Fatalf("isi akhir salah: %q", got)
	}
}

func TestLiveMessageThrottleThenFlush(t *testing.T) {
	f := newFakeSink()
	lm := newLiveMessage(f, 1) // minInterval default besar → set kedua di-throttle

	ctx := context.Background()
	_ = lm.set(ctx, "a")     // membuat pesan
	_ = lm.set(ctx, "ab")    // ter-throttle, dilewati
	_ = lm.flush(ctx, "abc") // dipaksa

	if got := f.combined(); got != "abc" {
		t.Fatalf("flush harus mengejar state terkini: %q", got)
	}
}

func TestLiveMessageRollsOver(t *testing.T) {
	f := newFakeSink()
	lm := newLiveMessage(f, 1)
	lm.minInterval = 0
	lm.limit = 10 // batas kecil untuk menguji rolling

	ctx := context.Background()
	full := strings.Repeat("a", 25) // 25 > 10 → harus pecah jadi beberapa pesan
	_ = lm.flush(ctx, full)

	if len(f.order) < 2 {
		t.Fatalf("teks panjang harus memakai >1 pesan, dapat %d", len(f.order))
	}
	if got := f.combined(); got != full {
		t.Fatalf("rekonstruksi rolling gagal: %q", got)
	}
	// Tiap pesan tidak boleh melebihi limit.
	for _, id := range f.order {
		if len([]rune(*f.messages[id])) > lm.limit {
			t.Fatalf("pesan melebihi limit: %q", *f.messages[id])
		}
	}
}

func TestLiveMessageRollsOverOnNewline(t *testing.T) {
	f := newFakeSink()
	lm := newLiveMessage(f, 1)
	lm.minInterval = 0
	lm.limit = 12

	ctx := context.Background()
	// Ada newline sebelum batas: pesan pertama harus berhenti di sana.
	full := "satu enam\nlanjut teks panjang berikutnya"
	_ = lm.flush(ctx, full)

	if len(f.order) < 2 {
		t.Fatalf("harus >1 pesan, dapat %d", len(f.order))
	}
	if *f.messages[f.order[0]] != "satu enam" {
		t.Fatalf("pesan pertama harus berhenti di newline, dapat %q", *f.messages[f.order[0]])
	}
}
