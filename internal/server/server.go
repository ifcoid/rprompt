// Package server menyediakan handler webhook Telegram: memvalidasi keamanan,
// memfilter chat yang diizinkan, menangani perintah & lampiran, dan menjalankan
// prompt melalui Claude Code dengan output streaming.
package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/awangga/rprompt/internal/approval"
	"github.com/awangga/rprompt/internal/claude"
	"github.com/awangga/rprompt/internal/config"
	"github.com/awangga/rprompt/internal/store"
	"github.com/awangga/rprompt/internal/telegram"
)

// promptRunner adalah subset claude.Runner yang dipakai Server (diabstraksikan
// agar handler bisa diuji tanpa menjalankan binary claude).
type promptRunner interface {
	RunStream(ctx context.Context, prompt, sessionID, workDir, model string, onEvent func(claude.StreamEvent)) (claude.Result, error)
}

// GeminiRunner menjalankan prompt lewat Gemini CLI (nil bila tidak diaktifkan).
type GeminiRunner interface {
	Run(ctx context.Context, prompt, workDir, model string) (string, error)
}

// Server menahan dependensi runtime handler.
type Server struct {
	cfg       *config.Config
	tg        *telegram.Client
	runner    promptRunner // jalur Telegram (boleh pakai izin interaktif)
	apiRunner promptRunner // jalur API (tanpa izin interaktif)
	gemini    GeminiRunner // engine Gemini untuk API (nil bila nonaktif)
	store     *store.Store
	reg       *approval.Registry // nil bila izin interaktif nonaktif

	tgSem  chan struct{} // serialisasi Telegram: ukuran 1
	apiSem chan struct{} // batas paralel API; nil = tak terbatas

	cwdMu sync.Mutex        // melindungi cwd & cont
	cwd   map[int64]string  // direktori kerja per-chat (override cfg.WorkDir)
	cont  map[int64]bool    // /continue: prompt berikutnya pakai --continue
}

// New membangun Server. reg boleh nil bila izin interaktif tidak dipakai.
// apiRunner dipakai jalur API (sebaiknya tanpa argumen --permission-prompt-tool).
// gem boleh nil bila Gemini tidak diaktifkan.
func New(cfg *config.Config, tg *telegram.Client, runner, apiRunner promptRunner, gem GeminiRunner, st *store.Store, reg *approval.Registry) *Server {
	var apiSem chan struct{}
	if cfg.APIMaxConcurrent > 0 {
		apiSem = make(chan struct{}, cfg.APIMaxConcurrent)
	}
	return &Server{
		cfg:       cfg,
		tg:        tg,
		runner:    runner,
		apiRunner: apiRunner,
		gemini:    gem,
		store:     st,
		reg:       reg,
		tgSem:     make(chan struct{}, 1),
		apiSem:    apiSem,
		cwd:       map[int64]string{},
		cont:      map[int64]bool{},
	}
}

// setContinue menandai bahwa prompt berikutnya pada chat memakai --continue.
func (s *Server) setContinue(chatID int64) {
	s.cwdMu.Lock()
	s.cont[chatID] = true
	s.cwdMu.Unlock()
}

// takeContinue mengembalikan true (sekali) bila /continue sedang aktif, lalu
// menghapusnya.
func (s *Server) takeContinue(chatID int64) bool {
	s.cwdMu.Lock()
	defer s.cwdMu.Unlock()
	if s.cont[chatID] {
		delete(s.cont, chatID)
		return true
	}
	return false
}

// chatWorkDir mengembalikan direktori kerja untuk chat (override per-chat bila
// ada, jika tidak default cfg.WorkDir).
func (s *Server) chatWorkDir(chatID int64) string {
	s.cwdMu.Lock()
	defer s.cwdMu.Unlock()
	if d, ok := s.cwd[chatID]; ok {
		return d
	}
	return s.cfg.WorkDir
}

// setChatWorkDir menetapkan direktori kerja untuk chat.
func (s *Server) setChatWorkDir(chatID int64, dir string) {
	s.cwdMu.Lock()
	s.cwd[chatID] = dir
	s.cwdMu.Unlock()
}

// Handler mengembalikan http.Handler yang siap dipasang.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.cfg.WebhookPath, s.handleWebhook)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if s.cfg.APIEnabled {
		// API OpenAI-compatible (non-streaming).
		mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
		mux.HandleFunc("/v1/models", s.handleModels)
	}
	return mux
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.WebhookSecret != "" {
		if r.Header.Get("X-Telegram-Bot-Api-Secret-Token") != s.cfg.WebhookSecret {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	var update telegram.Update
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&update); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Balas 200 segera; pemrosesan berjalan asinkron agar Telegram tidak retry.
	w.WriteHeader(http.StatusOK)
	s.dispatch(&update)
}

// dispatch merutekan satu update ke handler yang sesuai (dipakai webhook & polling).
func (s *Server) dispatch(u *telegram.Update) {
	switch {
	case u.CallbackQuery != nil:
		go s.handleCallback(u.CallbackQuery)
	case u.Message != nil:
		go s.process(u.Message)
	}
}

// Poll menjalankan long polling getUpdates sampai ctx dibatalkan. Dipakai pada
// mode polling (tanpa webhook/URL publik). Webhook harus sudah dihapus dulu.
func (s *Server) Poll(ctx context.Context) {
	const pollTimeout = 30 // detik Telegram menahan koneksi
	var offset int64
	for {
		if ctx.Err() != nil {
			return
		}
		updates, err := s.tg.GetUpdates(ctx, offset, pollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("getUpdates gagal: %v (coba lagi)", err)
			time.Sleep(3 * time.Second)
			continue
		}
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			up := u
			s.dispatch(&up)
		}
	}
}

// handleCallback menangani penekanan tombol Izinkan/Tolak pada permintaan izin.
func (s *Server) handleCallback(cb *telegram.CallbackQuery) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var chatID int64
	if cb.Message != nil {
		chatID = cb.Message.Chat.ID
	}
	if !s.cfg.AllowedChats[chatID] {
		_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "Tidak diizinkan.")
		return
	}
	if s.reg == nil {
		_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "Fitur izin tidak aktif.")
		return
	}

	// callback_data: "a|<reqID>" (izinkan) atau "d|<reqID>" (tolak).
	verb, reqID, ok := strings.Cut(cb.Data, "|")
	if !ok {
		_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "Data tombol tidak valid.")
		return
	}
	allow := verb == "a"

	_, msgID, resolved := s.reg.Resolve(reqID, allow)
	if !resolved {
		_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, "Permintaan sudah kedaluwarsa.")
		return
	}

	label := "⛔ DITOLAK"
	if allow {
		label = "✅ DIIZINKAN"
	}
	_ = s.tg.AnswerCallbackQuery(ctx, cb.ID, strings.TrimSpace(label))
	if msgID != 0 {
		_ = s.tg.EditTextClearButtons(ctx, chatID, msgID, "Keputusan izin: "+label)
	}
}

func (s *Server) process(msg *telegram.Message) {
	chatID := msg.Chat.ID

	if !s.cfg.AllowedChats[chatID] {
		log.Printf("tolak chat tidak diizinkan: chat_id=%d user=%q", chatID, username(msg))
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = s.tg.SendMessage(ctx, chatID,
			"Maaf, Anda tidak diizinkan memakai bot ini.\nChat ID Anda: "+strconv.FormatInt(chatID, 10))
		return
	}

	// Lampiran (foto/dokumen) diproses sebagai prompt dengan berkas terlampir.
	if len(msg.Photo) > 0 || msg.Document != nil {
		s.handleAttachment(msg)
		return
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}
	if strings.HasPrefix(text, "/") {
		s.handleCommand(chatID, text)
		return
	}
	s.handlePrompt(chatID, text)
}

func (s *Server) handleCommand(chatID int64, text string) {
	fields := strings.Fields(text)
	cmd := strings.SplitN(fields[0], "@", 2)[0] // buang @namabot pada grup

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch cmd {
	case "/start", "/help":
		_ = s.tg.SendMessage(ctx, chatID, helpText)
	case "/new", "/reset":
		if err := s.store.Reset(chatID); err != nil {
			log.Printf("gagal reset sesi chat_id=%d: %v", chatID, err)
		}
		s.takeContinue(chatID) // batalkan /continue yang menggantung
		_ = s.tg.SendMessage(ctx, chatID, "Sesi baru dimulai. Konteks percakapan sebelumnya dilupakan.")
	case "/continue", "/c":
		// Lanjutkan sesi Claude TERAKHIR di folder aktif (mis. sesi yang dibuat
		// di PC), via `claude --continue`.
		s.setContinue(chatID)
		if len(fields) > 1 {
			s.handlePrompt(chatID, strings.Join(fields[1:], " "))
			return
		}
		_ = s.tg.SendMessage(ctx, chatID,
			"Akan melanjutkan sesi Claude terakhir di:\n"+s.chatWorkDir(chatID)+"\nKirim prompt Anda sekarang.")
	case "/status":
		state := "tidak ada (akan memulai sesi baru)"
		if s.store.Get(chatID) != "" {
			state = "ada (percakapan dilanjutkan)"
		}
		_ = s.tg.SendMessage(ctx, chatID,
			"Direktori kerja: "+s.chatWorkDir(chatID)+"\nSesi tersimpan: "+state)
	case "/pwd":
		_ = s.tg.SendMessage(ctx, chatID, "Direktori kerja saat ini:\n"+s.chatWorkDir(chatID))
	case "/cd":
		if len(fields) < 2 {
			_ = s.tg.SendMessage(ctx, chatID, "Penggunaan: /cd <path-folder>")
			return
		}
		s.handleCd(ctx, chatID, strings.Join(fields[1:], " "))
	case "/project":
		s.handleProject(ctx, chatID, fields[1:])
	case "/get":
		if len(fields) < 2 {
			_ = s.tg.SendMessage(ctx, chatID, "Penggunaan: /get <path-relatif-ke-workdir>")
			return
		}
		s.handleGet(ctx, chatID, strings.Join(fields[1:], " "))
	default:
		_ = s.tg.SendMessage(ctx, chatID, "Perintah tidak dikenal. Ketik /help.")
	}
}

// handleCd menetapkan direktori kerja chat ke path bebas (harus folder yang ada).
func (s *Server) handleCd(ctx context.Context, chatID int64, path string) {
	path = strings.Trim(path, `"'`)
	abs, err := filepath.Abs(path)
	if err != nil {
		_ = s.tg.SendMessage(ctx, chatID, "Path tidak valid: "+err.Error())
		return
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		_ = s.tg.SendMessage(ctx, chatID, "Folder tidak ditemukan: "+abs)
		return
	}
	s.setChatWorkDir(chatID, abs)
	_ = s.tg.SendMessage(ctx, chatID, "Direktori kerja diubah ke:\n"+abs)
}

// handleProject menetapkan direktori kerja chat dari daftar whitelist (PROJECTS).
// Tanpa argumen: menampilkan daftar yang tersedia.
func (s *Server) handleProject(ctx context.Context, chatID int64, args []string) {
	if len(s.cfg.Projects) == 0 {
		_ = s.tg.SendMessage(ctx, chatID, "Belum ada proyek terdaftar. Set PROJECTS di .env (nama=path;...) atau pakai /cd.")
		return
	}
	if len(args) == 0 {
		var b strings.Builder
		b.WriteString("Proyek tersedia (pilih: /project <nama>):")
		for name, path := range s.cfg.Projects {
			b.WriteString("\n• ")
			b.WriteString(name)
			b.WriteString(" → ")
			b.WriteString(path)
		}
		_ = s.tg.SendMessage(ctx, chatID, b.String())
		return
	}
	name := args[0]
	path, ok := s.cfg.Projects[name]
	if !ok {
		_ = s.tg.SendMessage(ctx, chatID, "Proyek tidak dikenal: "+name+"\nKetik /project untuk daftar.")
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	s.setChatWorkDir(chatID, abs)
	_ = s.tg.SendMessage(ctx, chatID, "Direktori kerja diubah ke proyek '"+name+"':\n"+abs)
}

// handleGet mengirim sebuah berkas dari dalam workdir ke chat.
func (s *Server) handleGet(ctx context.Context, chatID int64, rel string) {
	workDir := s.chatWorkDir(chatID)
	rel = strings.Trim(rel, `"'`)
	target := rel
	if !filepath.IsAbs(target) {
		target = filepath.Join(workDir, rel)
	}
	// Cegah keluar dari workdir.
	absWork, _ := filepath.Abs(workDir)
	absTarget, err := filepath.Abs(target)
	if err != nil || !strings.HasPrefix(absTarget+string(filepath.Separator), absWork+string(filepath.Separator)) {
		_ = s.tg.SendMessage(ctx, chatID, "Path harus berada di dalam direktori kerja.")
		return
	}
	if err := s.tg.SendFile(ctx, chatID, absTarget, ""); err != nil {
		_ = s.tg.SendMessage(ctx, chatID, "Gagal mengirim berkas: "+err.Error())
	}
}

// handleAttachment mengunduh foto/dokumen dari Telegram, menyimpannya ke
// workdir/uploads, lalu menjalankannya sebagai prompt dengan path berkas.
func (s *Server) handleAttachment(msg *telegram.Message) {
	chatID := msg.Chat.ID
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	uploads := filepath.Join(s.chatWorkDir(chatID), "uploads")
	var paths []string

	if len(msg.Photo) > 0 {
		largest := msg.Photo[len(msg.Photo)-1] // varian resolusi tertinggi
		p, err := s.tg.DownloadFile(ctx, largest.FileID, uploads, "")
		if err != nil {
			_ = s.tg.SendMessage(ctx, chatID, "Gagal mengunduh foto: "+err.Error())
			return
		}
		paths = append(paths, p)
	}
	if msg.Document != nil {
		p, err := s.tg.DownloadFile(ctx, msg.Document.FileID, uploads, msg.Document.FileName)
		if err != nil {
			_ = s.tg.SendMessage(ctx, chatID, "Gagal mengunduh dokumen: "+err.Error())
			return
		}
		paths = append(paths, p)
	}
	if len(paths) == 0 {
		return
	}

	caption := strings.TrimSpace(msg.Caption)
	if caption == "" {
		caption = "Tolong analisis berkas terlampir."
	}
	var b strings.Builder
	b.WriteString(caption)
	b.WriteString("\n\nBerkas terlampir:")
	for _, p := range paths {
		b.WriteString("\n")
		b.WriteString(p)
	}
	s.handlePrompt(chatID, b.String())
}

func (s *Server) handlePrompt(chatID int64, prompt string) {
	// Telegram diserialisasi: hanya satu prompt diproses pada satu waktu.
	select {
	case s.tgSem <- struct{}{}:
	default:
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = s.tg.SendMessage(ctx, chatID, "Sedang memproses prompt lain, mohon tunggu sebentar lalu kirim ulang.")
		cancel()
		return
	}
	defer func() { <-s.tgSem }()

	// Tandai chat aktif agar permintaan izin (bila ada) dikirim ke sini.
	if s.reg != nil {
		s.reg.Activate(chatID)
		defer s.reg.Deactivate()
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Timeout)
	defer cancel()

	_ = s.tg.SendChatAction(ctx, chatID, "typing")
	live := newLiveMessage(s.tg, chatID)
	_ = live.flush(ctx, "⏳ Memproses...")

	var disp strings.Builder
	sessionID := s.store.Get(chatID)
	if s.takeContinue(chatID) {
		sessionID = claude.ResumeLatest // /continue -> lanjutkan sesi terakhir di folder
	}
	workDir := s.chatWorkDir(chatID)

	res, runErr := s.runner.RunStream(ctx, prompt, sessionID, workDir, "", func(ev claude.StreamEvent) {
		switch {
		case ev.AppendText != "":
			disp.WriteString(ev.AppendText)
		case ev.ToolName != "":
			disp.WriteString("\n🔧 ")
			disp.WriteString(ev.ToolName)
			disp.WriteString("\n")
		default:
			return
		}
		if err := live.set(ctx, disp.String()); err != nil {
			log.Printf("gagal update streaming chat_id=%d: %v", chatID, err)
		}
	})
	if runErr != nil {
		log.Printf("claude error chat_id=%d: %v", chatID, runErr)
	}

	if res.SessionID != "" {
		if err := s.store.Set(chatID, res.SessionID); err != nil {
			log.Printf("gagal simpan sesi chat_id=%d: %v", chatID, err)
		}
	}

	// Gunakan context baru untuk finalisasi; context utama mungkin sudah mepet.
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer sendCancel()

	final := disp.String()
	if strings.TrimSpace(final) == "" {
		final = res.Text
	}
	if strings.TrimSpace(final) == "" {
		final = "(tidak ada output)"
	}
	if res.IsError {
		final = "⚠️ " + final
	}
	if err := live.flush(sendCtx, final); err != nil {
		log.Printf("gagal kirim balasan chat_id=%d: %v", chatID, err)
	}

	// Kirim otomatis gambar yang disebut di output bila berkasnya ada.
	for _, img := range detectImageFiles(disp.String(), workDir) {
		if err := s.tg.SendFile(sendCtx, chatID, img, ""); err != nil {
			log.Printf("gagal kirim gambar %q chat_id=%d: %v", img, chatID, err)
		}
	}
}

const helpText = `Halo! Saya jembatan antara Telegram dan Claude Code.

Kirim teks apa pun sebagai prompt, dan saya akan menjalankannya di Claude Code lalu mengirim hasilnya ke sini secara bertahap (streaming). Percakapan dilanjutkan otomatis antar pesan. Anda juga bisa mengirim foto/dokumen untuk dianalisis.

Perintah:
/new            - mulai sesi baru (lupakan konteks)
/continue       - lanjutkan sesi Claude terakhir di folder aktif (mis. dari PC)
/status         - lihat direktori kerja & status sesi
/pwd            - lihat direktori kerja saat ini
/cd <path>      - pindah direktori kerja (path bebas)
/project [nama] - pindah ke proyek terdaftar (tanpa nama = daftar)
/get <path>     - kirim berkas dari direktori kerja ke chat
/help           - tampilkan bantuan ini`

func username(m *telegram.Message) string {
	if m.From == nil {
		return ""
	}
	if m.From.Username != "" {
		return "@" + m.From.Username
	}
	return m.From.FirstName
}
