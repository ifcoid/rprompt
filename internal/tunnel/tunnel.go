// Package tunnel menjalankan cloudflared quick tunnel sebagai subprocess dan
// menangkap URL publik yang dihasilkannya (https://...trycloudflare.com), agar
// service bisa mendaftarkan webhook secara otomatis tiap kali dijalankan.
package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"time"
)

// urlRe mencocokkan URL quick tunnel cloudflared.
var urlRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// Tunnel memegang proses cloudflared dan URL publiknya.
type Tunnel struct {
	cmd *exec.Cmd
	URL string
}

// Start menjalankan `cloudflared tunnel --url http://localhost:<localPort>` dan
// menunggu hingga URL publik muncul di output (atau timeout).
func Start(ctx context.Context, bin, localPort string, timeout time.Duration) (*Tunnel, error) {
	cmd := exec.Command(bin, "tunnel", "--no-autoupdate", "--url", "http://localhost:"+localPort)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("menjalankan %q: %w", bin, err)
	}

	// Drain kedua pipe terus-menerus (mencegah cloudflared memblok saat pipe
	// penuh) dan kirim URL pertama yang ditemukan.
	urlCh := make(chan string, 1)
	go drain(stdout, urlCh)
	go drain(stderr, urlCh)

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case u := <-urlCh:
		return &Tunnel{cmd: cmd, URL: u}, nil
	case <-timer.C:
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("timeout %s menunggu URL cloudflared", timeout)
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return nil, ctx.Err()
	}
}

// Run menjalankan `cloudflared tunnel run <name>` (named tunnel) sebagai
// subprocess. URL-nya tetap (ditentukan config.yml/DNS), jadi tidak ada URL yang
// perlu di-parse. Output cloudflared dibuang agar log rprompt tetap bersih.
func Run(bin, name string) (*Tunnel, error) {
	cmd := exec.Command(bin, "tunnel", "run", name)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("menjalankan %q tunnel run %q: %w", bin, name, err)
	}
	return &Tunnel{cmd: cmd}, nil
}

// Stop menghentikan proses cloudflared.
func (t *Tunnel) Stop() error {
	if t == nil || t.cmd == nil || t.cmd.Process == nil {
		return nil
	}
	_ = t.cmd.Process.Kill()
	_, err := t.cmd.Process.Wait()
	return err
}

// drain membaca baris demi baris; saat menemukan URL, mengirimnya sekali ke ch,
// lalu terus membaca (membuang) hingga EOF.
func drain(r io.Reader, ch chan<- string) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if u := findURL(sc.Text()); u != "" {
			select {
			case ch <- u:
			default:
			}
		}
	}
}

func findURL(s string) string {
	return urlRe.FindString(s)
}
