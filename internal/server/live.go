package server

import (
	"context"
	"strings"
	"time"
)

// liveLimit adalah panjang maksimum satu pesan Telegram (karakter).
const liveLimit = 4096

// msgSink adalah subset Telegram client yang dibutuhkan liveMessage.
// Diabstraksikan agar bisa diuji tanpa jaringan.
type msgSink interface {
	Send(ctx context.Context, chatID int64, text string) (int64, error)
	EditMessageText(ctx context.Context, chatID, messageID int64, text string) error
}

// liveMessage menampilkan teks yang tumbuh secara bertahap dengan mengedit satu
// pesan Telegram. Bila teks melampaui batas, pesan saat ini "dibekukan" dan
// kelanjutannya ditulis ke pesan baru (rolling). Edit pada ekor yang aktif
// di-throttle agar tidak melanggar rate limit Telegram.
type liveMessage struct {
	sink        msgSink
	chatID      int64
	limit       int
	minInterval time.Duration

	consumed int       // jumlah rune dari teks penuh yang sudah dibekukan
	curID    int64     // message_id pesan aktif (0 = belum ada)
	curText  string    // teks pada pesan aktif saat ini
	lastEdit time.Time // waktu edit terakhir (untuk throttle)
}

func newLiveMessage(sink msgSink, chatID int64) *liveMessage {
	return &liveMessage{
		sink:        sink,
		chatID:      chatID,
		limit:       liveLimit,
		minInterval: 1200 * time.Millisecond,
	}
}

// set memperbarui tampilan ke teks penuh "full" (throttled).
func (l *liveMessage) set(ctx context.Context, full string) error {
	return l.write(ctx, full, false)
}

// flush memaksa tampilan ke teks penuh "full" tanpa throttle.
func (l *liveMessage) flush(ctx context.Context, full string) error {
	return l.write(ctx, full, true)
}

func (l *liveMessage) write(ctx context.Context, full string, force bool) error {
	runes := []rune(full)

	// Bekukan potongan penuh selama sisa masih melebihi batas.
	for len(runes)-l.consumed > l.limit {
		window := runes[l.consumed : l.consumed+l.limit]
		cut := lastNewline(window)
		skip := 0
		if cut <= 0 {
			cut = l.limit // tak ada baris baru: potong keras
		} else {
			skip = 1 // buang '\n' di batas
		}
		chunk := string(runes[l.consumed : l.consumed+cut])
		if err := l.put(ctx, chunk); err != nil {
			return err
		}
		l.consumed += cut + skip
		l.curID = 0 // pesan berikutnya mulai baru
		l.curText = ""
	}

	tail := string(runes[l.consumed:])
	// Throttle edit pada ekor aktif; akan dikejar saat flush.
	if !force && l.curID != 0 && time.Since(l.lastEdit) < l.minInterval {
		return nil
	}
	return l.put(ctx, tail)
}

// put mengirim (bila belum ada) atau mengedit pesan aktif menjadi text.
func (l *liveMessage) put(ctx context.Context, text string) error {
	if l.curID == 0 {
		if strings.TrimSpace(text) == "" {
			return nil // belum ada apa pun untuk dikirim
		}
		id, err := l.sink.Send(ctx, l.chatID, text)
		if err != nil {
			return err
		}
		l.curID = id
		l.curText = text
		l.lastEdit = time.Now()
		return nil
	}
	if text == l.curText {
		return nil
	}
	if err := l.sink.EditMessageText(ctx, l.chatID, l.curID, text); err != nil {
		return err
	}
	l.curText = text
	l.lastEdit = time.Now()
	return nil
}

// lastNewline mengembalikan indeks rune '\n' terakhir, atau -1.
func lastNewline(runes []rune) int {
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] == '\n' {
			return i
		}
	}
	return -1
}
