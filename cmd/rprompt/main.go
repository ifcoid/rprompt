// Command rprompt menjalankan jembatan Telegram <-> Claude Code.
//
// Mode:
//
//	rprompt                  jalankan server webhook
//	rprompt -set-webhook     daftarkan webhook ke Telegram lalu keluar
//	rprompt -delete-webhook  hapus webhook lalu keluar
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/awangga/rprompt/internal/approval"
	"github.com/awangga/rprompt/internal/claude"
	"github.com/awangga/rprompt/internal/config"
	"github.com/awangga/rprompt/internal/mcpserver"
	"github.com/awangga/rprompt/internal/server"
	"github.com/awangga/rprompt/internal/store"
	"github.com/awangga/rprompt/internal/telegram"
	"github.com/awangga/rprompt/internal/tunnel"
)

func main() {
	setWebhook := flag.Bool("set-webhook", false, "daftarkan webhook ke Telegram lalu keluar")
	deleteWebhook := flag.Bool("delete-webhook", false, "hapus webhook lalu keluar")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("konfigurasi: %v", err)
	}

	tg := telegram.New(cfg.BotToken)

	switch {
	case *deleteWebhook:
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := tg.DeleteWebhook(ctx); err != nil {
			log.Fatalf("hapus webhook: %v", err)
		}
		log.Println("webhook dihapus.")
		return

	case *setWebhook:
		if cfg.WebhookURL == "" {
			log.Fatal("WEBHOOK_URL wajib diisi untuk -set-webhook")
		}
		full := cfg.WebhookURL + cfg.WebhookPath
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := tg.SetWebhook(ctx, full, cfg.WebhookSecret); err != nil {
			log.Fatalf("set webhook: %v", err)
		}
		log.Printf("webhook didaftarkan: %s", full)
		return
	}

	st, err := store.New(cfg.SessionFile)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	runner := &claude.Runner{Bin: cfg.ClaudeBin, WorkDir: cfg.WorkDir, ExtraArgs: cfg.ExtraArgs}

	// Mode izin interaktif: jalankan server MCP lokal & arahkan claude ke sana.
	var reg *approval.Registry
	if cfg.Interactive {
		reg = approval.New(tgNotifier{tg}, cfg.ApprovalTimeout, cfg.ResultFormat)

		cfgPath, err := writeMCPConfig(cfg.MCPURL())
		if err != nil {
			log.Fatalf("tulis mcp-config: %v", err)
		}
		toolName := fmt.Sprintf("mcp__%s__%s", mcpserver.ServerName, mcpserver.ToolName)
		runner.PermissionArgs = []string{
			"--permission-prompt-tool", toolName,
			"--mcp-config", cfgPath,
		}

		mcpMux := http.NewServeMux()
		mcpMux.Handle(cfg.MCPPath, mcpserver.Handler(reg, cfg.ResultFormat))
		mcpSrv := &http.Server{Addr: cfg.MCPAddr, Handler: mcpMux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			log.Printf("server MCP izin listen di %s (tool %s)", cfg.MCPURL(), toolName)
			if err := mcpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server MCP: %v", err)
			}
		}()
		log.Println("izin interaktif AKTIF: tiap tool akan minta persetujuan via tombol Telegram")
	}

	// Runner khusus jalur API: tanpa argumen izin interaktif (--permission-prompt-tool),
	// karena API tidak punya chat Telegram untuk meminta persetujuan. Tool pada
	// jalur API mengikuti CLAUDE_EXTRA_ARGS saja.
	apiRunner := *runner
	apiRunner.PermissionArgs = nil

	srv := server.New(cfg, tg, runner, &apiRunner, st, reg)

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("rprompt listen di %s (path %s), work_dir=%s", cfg.ListenAddr, cfg.WebhookPath, cfg.WorkDir)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	// Auto-tunnel: jalankan cloudflared & daftarkan webhook otomatis.
	var tun *tunnel.Tunnel
	if cfg.AutoTunnel {
		t, err := tunnel.Start(context.Background(), cfg.CloudflaredBin, cfg.LocalPort(), 60*time.Second)
		if err != nil {
			log.Fatalf("auto-tunnel: %v", err)
		}
		tun = t
		log.Printf("cloudflared tunnel: %s", tun.URL)

		// PENTING: tunggu DNS quick-tunnel ter-publish SEBELUM memanggil
		// setWebhook. Bila setWebhook dipanggil saat host belum bisa di-resolve,
		// resolver Telegram nge-cache hasil NXDOMAIN (negative cache) dan menahan
		// kegagalan walau DNS sudah hidup. Cek pakai resolver publik (1.1.1.1)
		// karena resolver lokal bisa lambat/tak meng-resolve subdomain baru.
		host := tunnelHost(tun.URL)
		log.Println("menunggu DNS tunnel ter-publish (bisa ~1-2 menit)...")
		if waitDNSPublished(host, 180*time.Second) {
			log.Println("DNS ter-publish; memberi jeda singkat lalu mendaftarkan webhook.")
			time.Sleep(5 * time.Second)
		} else {
			log.Println("peringatan: DNS belum terkonfirmasi setelah 3 menit; tetap mencoba.")
		}
		if err := registerWebhook(tg, tun.URL+cfg.WebhookPath, cfg.WebhookSecret); err != nil {
			_ = tun.Stop()
			log.Fatalf("set webhook otomatis gagal: %v", err)
		}
		log.Printf("webhook otomatis terdaftar: %s%s", tun.URL, cfg.WebhookPath)
	} else if cfg.SetWebhookOnStart {
		// URL tetap (mis. domain/named-tunnel di depan container): daftarkan
		// sekali saat start. Tidak dihapus saat shutdown karena URL permanen.
		if err := registerWebhook(tg, cfg.WebhookURL+cfg.WebhookPath, cfg.WebhookSecret); err != nil {
			log.Fatalf("set webhook saat start gagal: %v", err)
		}
		log.Printf("webhook terdaftar: %s%s", cfg.WebhookURL, cfg.WebhookPath)
	}

	// Tunggu sinyal untuk shutdown rapi.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("mematikan server...")

	// Bersihkan tunnel & webhook bila auto-tunnel dipakai.
	if tun != nil {
		dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := tg.DeleteWebhook(dctx); err != nil {
			log.Printf("hapus webhook: %v", err)
		}
		dcancel()
		if err := tun.Stop(); err != nil {
			log.Printf("stop tunnel: %v", err)
		}
		log.Println("tunnel & webhook dibersihkan")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// tunnelHost mengambil hostname dari URL tunnel.
func tunnelHost(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://"), "http://")
}

// waitDNSPublished melakukan polling resolusi host lewat resolver publik
// (1.1.1.1) sampai berhasil atau timeout. Memakai resolver publik karena
// resolver sistem/lokal bisa lambat atau gagal meng-resolve subdomain baru.
func waitDNSPublished(host string, timeout time.Duration) bool {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "udp", "1.1.1.1:53")
		},
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		addrs, err := resolver.LookupHost(ctx, host)
		cancel()
		if err == nil && len(addrs) > 0 {
			return true
		}
		time.Sleep(3 * time.Second)
	}
	return false
}

// registerWebhook mendaftarkan webhook dengan sedikit retry. Dipanggil setelah
// DNS dipastikan ter-publish, jadi umumnya berhasil pada percobaan pertama.
func registerWebhook(tg *telegram.Client, url, secret string) error {
	const (
		attempts = 12
		interval = 5 * time.Second
	)
	var err error
	for i := 1; i <= attempts; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err = tg.SetWebhook(ctx, url, secret)
		cancel()
		if err == nil {
			return nil
		}
		if i%3 == 0 || i == 1 { // jangan terlalu berisik
			log.Printf("  ...belum siap (percobaan %d/%d): %v", i, attempts, err)
		}
		if i < attempts {
			time.Sleep(interval)
		}
	}
	return err
}

// tgNotifier mengimplementasikan approval.Notifier memakai Telegram.
type tgNotifier struct {
	tg *telegram.Client
}

func (n tgNotifier) AskApproval(ctx context.Context, chatID int64, reqID, toolName, summary string) (int64, error) {
	text := fmt.Sprintf("🔐 Claude minta izin memakai tool:\n\nTool: %s\nDetail:\n%s\n\nIzinkan?", toolName, summary)
	rows := [][]telegram.InlineButton{{
		{Text: "✅ Izinkan", Data: "a|" + reqID},
		{Text: "⛔ Tolak", Data: "d|" + reqID},
	}}
	return n.tg.SendButtons(ctx, chatID, text, rows)
}

func (n tgNotifier) Finalize(ctx context.Context, chatID, messageID int64, text string) {
	_ = n.tg.EditTextClearButtons(ctx, chatID, messageID, text)
}

// writeMCPConfig menulis berkas --mcp-config yang mengarah ke server MCP lokal,
// mengembalikan path-nya.
func writeMCPConfig(mcpURL string) (string, error) {
	cfg := map[string]any{
		"mcpServers": map[string]any{
			mcpserver.ServerName: map[string]any{
				"type": "http",
				"url":  mcpURL,
			},
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(os.TempDir(), "rprompt-mcp-config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
