# CLAUDE.md

Panduan singkat untuk mengerjakan repo ini. Cara pakai umum ada di `README.md`,
`TELEGRAM.md`, dan `USER.md` — file ini fokus ke hal yang tidak tercatat di sana.

## Apa ini
`rprompt` — bridge **Telegram ↔ Claude Code** + **HTTP API OpenAI-compatible**.
Prompt dari Telegram dijalankan sebagai `claude -p` di mesin ini, hasilnya balik
ke chat. Go, ~4.4k baris. Butuh **Go 1.26+**.

## Build, test, deploy
```sh
go test ./... && go vet ./...        # sebelum commit
go build -o rprompt ./cmd/rprompt    # binary lokal
```

Deploy ke PC ini (mesin dev = mesin produksi):
```sh
go build -o rprompt ./cmd/rprompt
cp /home/adb/.local/bin/rprompt /home/adb/.local/bin/rprompt.bak   # backup
cp ./rprompt /home/adb/.local/bin/rprompt
sudo systemctl restart rprompt                                    # + cloudflared-rprompt bila perlu
systemctl status rprompt --no-pager
```
- Service berjalan dari `/home/adb/.local/bin/rprompt` (unit: `rprompt.service`,
  tunnel: `cloudflared-rprompt.service`). File unit & `.env` runtime ada di
  `/home/adb/awangga/rprompt/` (folder deployment, terpisah dari repo ini).
- CI (GitHub Actions) meng-cross-compile + obfuscate dengan **garble** lalu upload
  ke repo `llm-y/download`. Build lokal **tanpa** garble — fungsional sama.

## Peta package (`internal/`)
- `config` — parse `.env` (godotenv; env OS menimpa `.env`).
- `telegram` — bot: polling/webhook, streaming, perintah (`/new`, `/cd`, `/get`, …).
- `claude` — jalankan `claude` CLI; kontinuitas sesi via `--resume`.
- `server` — HTTP API OpenAI-compatible (`/v1/chat/completions`, `/v1/models`),
  SSE streaming, penanganan gambar.
- `store` — state sesi per-chat (disimpan ke `SESSION_FILE`, mis. `sessions.json`).
- `tunnel` — integrasi cloudflared (named/quick tunnel).
- `approval` — izin tool via tombol Telegram (✅/⛔).
- `mcpserver` — server MCP.
- `agy` / `kiro` / `bob` — engine alternatif (Gemini/AGY, Kiro, Bob Shell), dipilih
  lewat field `model` di API. Di deployment saat ini hanya Claude yang aktif.

## Catatan
- Module path `github.com/awangga/rprompt`, tapi remote git `github.com/ifcoid/rprompt`
  (fork/mirror). Perhatikan saat menambah import internal.
- Mode `polling` (default) tidak butuh tunnel untuk bot — tahan di balik NAT.
- Akses dibatasi whitelist `ALLOWED_CHAT_IDS` + `API_TOKEN`. Jaga `.env` rahasia.
