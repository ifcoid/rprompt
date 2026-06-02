// Package config memuat konfigurasi service dari environment variable
// (dan file .env bila ada).
package config

import (
	"bufio"
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

	// Gemini CLI (opsional) untuk routing via field "model" di API.
	GeminiEnabled   bool     // aktifkan engine Gemini
	GeminiBin       string   // path binary gemini
	GeminiExtraArgs []string // argumen tambahan untuk gemini
	GeminiUseOAuth  bool     // paksa login CLI (strip GEMINI_API_KEY/GOOGLE_API_KEY)

	// Izin interaktif via tombol Telegram.
	Interactive     bool          // aktifkan persetujuan izin tool via Telegram
	MCPAddr         string        // alamat listen lokal server MCP, mis. "127.0.0.1:8765"
	MCPPath         string        // path endpoint MCP, mis. "/mcp"
	ApprovalTimeout time.Duration // batas tunggu tap tombol sebelum auto-tolak
	ResultFormat    string        // format hasil izin: "behavior" | "hook"

	// Auto-tunnel: jalankan cloudflared & daftarkan webhook otomatis.
	AutoTunnel     bool   // jalankan cloudflared sendiri lalu set-webhook otomatis
	CloudflaredBin string // path binary cloudflared

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

	c := &Config{
		BotToken:      os.Getenv("TELEGRAM_BOT_TOKEN"),
		ListenAddr:    getenv("LISTEN_ADDR", ":8080"),
		WebhookPath:   getenv("WEBHOOK_PATH", "/telegram/webhook"),
		WebhookURL:    strings.TrimRight(os.Getenv("WEBHOOK_URL"), "/"),
		WebhookSecret: os.Getenv("WEBHOOK_SECRET"),
		ClaudeBin:     getenv("CLAUDE_BIN", "claude"),
		WorkDir:       getenv("WORK_DIR", "."),
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

	c.GeminiEnabled = isTrue(os.Getenv("GEMINI_ENABLED"))
	c.GeminiBin = getenv("GEMINI_BIN", "gemini")
	c.GeminiUseOAuth = isTrue(os.Getenv("GEMINI_USE_OAUTH"))
	if g := strings.TrimSpace(os.Getenv("GEMINI_EXTRA_ARGS")); g != "" {
		c.GeminiExtraArgs = strings.Fields(g)
	}

	secs := getenv("CLAUDE_TIMEOUT_SECONDS", "600")
	n, err := strconv.Atoi(secs)
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("CLAUDE_TIMEOUT_SECONDS tidak valid: %q", secs)
	}
	c.Timeout = time.Duration(n) * time.Second

	c.Interactive = isTrue(os.Getenv("INTERACTIVE_PERMISSIONS"))
	c.MCPAddr = getenv("MCP_ADDR", "127.0.0.1:8765")
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

	c.AutoTunnel = isTrue(os.Getenv("AUTO_TUNNEL"))
	c.CloudflaredBin = getenv("CLOUDFLARED_BIN", "cloudflared")
	c.SetWebhookOnStart = isTrue(os.Getenv("SET_WEBHOOK_ON_START"))
	if c.SetWebhookOnStart && !c.AutoTunnel && c.WebhookURL == "" {
		return nil, fmt.Errorf("WEBHOOK_URL wajib diisi bila SET_WEBHOOK_ON_START=true")
	}

	c.APIEnabled = isTrue(os.Getenv("API_ENABLED"))
	c.APIToken = os.Getenv("API_TOKEN")
	if c.APIEnabled && c.APIToken == "" {
		return nil, fmt.Errorf("API_TOKEN wajib diisi bila API_ENABLED=true")
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

	if c.BotToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN wajib diisi")
	}
	if len(c.AllowedChats) == 0 {
		return nil, fmt.Errorf("ALLOWED_CHAT_IDS wajib diisi (minimal 1 chat id)")
	}
	return c, nil
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
