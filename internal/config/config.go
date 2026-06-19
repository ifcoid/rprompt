// Package config memuat konfigurasi service dari environment variable
// (dan file .env bila ada).
package config

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config menyimpan seluruh konfigurasi runtime.
type Config struct {
	BotToken      string         // token bot dari @BotFather
	TelegramMode  string         // "webhook" | "polling"
	AllowedChats  map[int64]bool // daftar chat id yang diizinkan
	ListenAddr    string         // alamat listen HTTP, mis. ":8080"
	WebhookPath   string         // path endpoint webhook, mis. "/telegram/webhook"
	WebhookURL    string         // base URL publik (tanpa path), mis. "https://xxx.trycloudflare.com"
	WebhookSecret string         // secret_token yang divalidasi pada setiap update
	ClaudeBin     string            // path binary claude
	WorkDir       string            // direktori kerja default tempat claude dijalankan
	Projects      map[string]string // nama -> path folder (whitelist untuk /project)
	ExtraArgs     []string          // argumen tambahan untuk claude, mis. --dangerously-skip-permissions
	SessionFile   string            // file penyimpanan session id per chat
	Timeout       time.Duration     // batas waktu eksekusi satu prompt

	// AGY CLI (opsional) untuk routing via field "model" di API.
	AgyEnabled   bool     // aktifkan engine AGY
	AgyBin       string   // path binary agy
	AgyExtraArgs []string // argumen tambahan untuk agy
	AgyUseOAuth  bool     // paksa login CLI (strip AGY_API_KEY/GOOGLE_API_KEY)

	// Kiro CLI (opsional) untuk routing via field "model" di API.
	KiroEnabled   bool     // aktifkan engine Kiro
	KiroBin       string   // path binary kiro-cli
	KiroExtraArgs []string // argumen tambahan untuk kiro-cli

	// Bob Shell CLI (opsional) untuk routing via field "model" di API.
	BobEnabled   bool     // aktifkan engine Bob
	BobBin       string   // path binary bob
	BobExtraArgs []string // argumen tambahan untuk bob

	// Izin interaktif via tombol Telegram.
	Interactive     bool          // aktifkan persetujuan izin tool via Telegram
	MCPAddr         string        // alamat listen lokal server MCP, mis. "127.0.0.1:8765"
	MCPPath         string        // path endpoint MCP, mis. "/mcp"
	ApprovalTimeout time.Duration // batas tunggu tap tombol sebelum auto-tolak
	ResultFormat    string        // format hasil izin: "behavior" | "hook"

	// Auto-tunnel: jalankan cloudflared & daftarkan webhook otomatis.
	AutoTunnel     bool   // jalankan cloudflared sendiri lalu set-webhook otomatis
	CloudflaredBin string // path binary cloudflared

	// Named tunnel: bila diisi, rprompt menjalankan `cloudflared tunnel run <nama>`
	// sebagai subprocess (URL tetap) — satu proses untuk rprompt + tunnel.
	CloudflaredTunnel string

	// Daftarkan webhook ke WEBHOOK_URL saat start (untuk URL tetap, mis. di Docker).
	SetWebhookOnStart bool

	// HTTP API untuk aplikasi lain (POST /api/prompt).
	APIEnabled       bool   // aktifkan endpoint API
	APIToken         string // token Bearer yang wajib pada setiap request API
	APIMaxConcurrent int    // maks eksekusi API paralel; 0 = tak terbatas
}

// LocalPort mengembalikan port dari ListenAddr (default "8080").
func (c *Config) LocalPort() string {
	_, port, err := net.SplitHostPort(c.ListenAddr)
	if err != nil || port == "" {
		return "8080"
	}
	return port
}

// MCPURL menyusun URL penuh server MCP untuk --mcp-config.
func (c *Config) MCPURL() string {
	host := c.MCPAddr
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	return "http://" + host + c.MCPPath
}

// Load membaca .env (jika ada) lalu environment, dan memvalidasi nilai wajib.
func Load() (*Config, error) {
	loadDotEnv(".env")

	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	c := &Config{
		BotToken:      os.Getenv("TELEGRAM_BOT_TOKEN"),
		ListenAddr:    normalizeAddr(getenv("LISTEN_ADDR", ":8080")),
		WebhookPath:   getenv("WEBHOOK_PATH", "/telegram/webhook"),
		WebhookURL:    strings.TrimRight(os.Getenv("WEBHOOK_URL"), "/"),
		WebhookSecret: os.Getenv("WEBHOOK_SECRET"),
		ClaudeBin:     getenv("CLAUDE_BIN", "claude"),
		WorkDir:       getenv("WORK_DIR", wd),
		SessionFile:   getenv("SESSION_FILE", "sessions.json"),
		AllowedChats:  map[int64]bool{},
	}

	if extra := strings.TrimSpace(os.Getenv("CLAUDE_EXTRA_ARGS")); extra != "" {
		c.ExtraArgs = strings.Fields(extra)
	}

	// PROJECTS: daftar whitelist "nama=path;nama2=path2" untuk perintah /project.
	c.Projects = map[string]string{}
	for _, pair := range strings.Split(os.Getenv("PROJECTS"), ";") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, path, ok := strings.Cut(pair, "=")
		name, path = strings.TrimSpace(name), strings.TrimSpace(path)
		if !ok || name == "" || path == "" {
			return nil, fmt.Errorf("PROJECTS tidak valid pada %q (format: nama=path;nama2=path2)", pair)
		}
		c.Projects[name] = path
	}

	c.AgyEnabled = getenvBool("AGY_ENABLED", true)
	c.AgyBin = getenv("AGY_BIN", "agy")
	c.AgyUseOAuth = isTrue(os.Getenv("AGY_USE_OAUTH"))
	if g := strings.TrimSpace(os.Getenv("AGY_EXTRA_ARGS")); g != "" {
		c.AgyExtraArgs = strings.Fields(g)
	}

	c.KiroEnabled = getenvBool("KIRO_ENABLED", true)
	c.KiroBin = getenv("KIRO_BIN", "kiro-cli")
	if k := strings.TrimSpace(os.Getenv("KIRO_EXTRA_ARGS")); k != "" {
		c.KiroExtraArgs = strings.Fields(k)
	}

	c.BobEnabled = getenvBool("BOB_ENABLED", true)
	c.BobBin = getenv("BOB_BIN", "bob")
	if b := strings.TrimSpace(os.Getenv("BOB_EXTRA_ARGS")); b != "" {
		c.BobExtraArgs = strings.Fields(b)
	}

	secs := getenv("CLAUDE_TIMEOUT_SECONDS", "600")
	n, err := strconv.Atoi(secs)
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("CLAUDE_TIMEOUT_SECONDS tidak valid: %q", secs)
	}
	c.Timeout = time.Duration(n) * time.Second

	c.Interactive = isTrue(os.Getenv("INTERACTIVE_PERMISSIONS"))
	c.MCPAddr = normalizeAddr(getenv("MCP_ADDR", "127.0.0.1:8765"))
	c.MCPPath = getenv("MCP_PATH", "/mcp")
	c.ResultFormat = getenv("PERMISSION_RESULT_FORMAT", "behavior")
	if c.ResultFormat != "behavior" && c.ResultFormat != "hook" {
		return nil, fmt.Errorf("PERMISSION_RESULT_FORMAT harus \"behavior\" atau \"hook\", dapat %q", c.ResultFormat)
	}
	asecs := getenv("APPROVAL_TIMEOUT_SECONDS", "120")
	an, err := strconv.Atoi(asecs)
	if err != nil || an <= 0 {
		return nil, fmt.Errorf("APPROVAL_TIMEOUT_SECONDS tidak valid: %q", asecs)
	}
	c.ApprovalTimeout = time.Duration(an) * time.Second

	c.AutoTunnel = isTrue(getenv("AUTO_TUNNEL", "true"))
	c.CloudflaredBin = getenv("CLOUDFLARED_BIN", "cloudflared")
	c.CloudflaredTunnel = strings.TrimSpace(os.Getenv("CLOUDFLARED_TUNNEL"))
	if c.AutoTunnel && c.CloudflaredTunnel != "" {
		return nil, fmt.Errorf("pilih salah satu: AUTO_TUNNEL (quick tunnel) atau CLOUDFLARED_TUNNEL (named tunnel)")
	}
	c.SetWebhookOnStart = isTrue(os.Getenv("SET_WEBHOOK_ON_START"))
	if c.SetWebhookOnStart && !c.AutoTunnel && c.WebhookURL == "" {
		return nil, fmt.Errorf("WEBHOOK_URL wajib diisi bila SET_WEBHOOK_ON_START=true")
	}

	c.APIEnabled = isTrue(getenv("API_ENABLED", "true"))
	c.APIToken = os.Getenv("API_TOKEN")
	if c.APIEnabled && c.APIToken == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("gagal membuat API_TOKEN acak: %w", err)
		}
		c.APIToken = fmt.Sprintf("rprompt-sec-%x", b)
	}
	amc := getenv("API_MAX_CONCURRENT", "0")
	mc, err := strconv.Atoi(amc)
	if err != nil || mc < 0 {
		return nil, fmt.Errorf("API_MAX_CONCURRENT tidak valid (harus >= 0): %q", amc)
	}
	c.APIMaxConcurrent = mc

	for _, part := range strings.Split(os.Getenv("ALLOWED_CHAT_IDS"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("ALLOWED_CHAT_IDS berisi nilai bukan angka: %q", part)
		}
		c.AllowedChats[id] = true
	}

	c.TelegramMode = strings.ToLower(getenv("TELEGRAM_MODE", "webhook"))
	if c.TelegramMode != "webhook" && c.TelegramMode != "polling" {
		return nil, fmt.Errorf("TELEGRAM_MODE harus \"webhook\" atau \"polling\", dapat %q", c.TelegramMode)
	}

	if c.BotToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN wajib diisi")
	}
	if len(c.AllowedChats) == 0 {
		return nil, fmt.Errorf("ALLOWED_CHAT_IDS wajib diisi (minimal 1 chat id)")
	}
	return c, nil
}

// normalizeAddr memastikan alamat listen memiliki format yang benar untuk
// net.Listen. Jika user hanya memasukkan angka port (mis. "56789"), otomatis
// diawali titik dua menjadi ":56789". Jika sudah mengandung ":" (mis.
// ":8080" atau "0.0.0.0:8080"), dikembalikan apa adanya.
func normalizeAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ":8080"
	}
	// Sudah memiliki format host:port atau :port
	if strings.Contains(addr, ":") {
		return addr
	}
	// Hanya angka port tanpa ":"
	if _, err := strconv.Atoi(addr); err == nil {
		return ":" + addr
	}
	return addr
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	}
	return false
}

func getenvBool(key string, def bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		return isTrue(val)
	}
	return def
}

// loadDotEnv memuat pasangan KEY=VALUE dari file .env ke environment.
// Baris yang sudah ada di environment tidak ditimpa. Baris kosong / diawali #
// diabaikan. Bukan parser dotenv penuh, cukup untuk kebutuhan dasar.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}
