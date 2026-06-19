# rprompt

Remote prompt **Telegram ↔ Claude Code**. Kirim prompt dari HP via Telegram,
dijalankan `claude -p` di laptop, hasilnya balik ke chat — laptop bisa ditinggal.

Bonus: **HTTP API OpenAI-compatible** (Claude + Gemini) untuk dipakai aplikasi lain.

## Quick Start (portabel)

Mode `polling` (default): bot menarik update sendiri — **tanpa webhook, tunnel,
atau service**. Jalan di balik NAT.

Prasyarat: [Go](https://go.dev/dl/) 1.26+, [Claude Code CLI](https://code.claude.com) (sudah `claude login`).

```sh
git clone https://github.com/ifcoid/rprompt.git && cd rprompt
go build -o rprompt.exe ./cmd/rprompt          # Linux/Mac: -o rprompt

cp .env.example .env        # lalu isi 2 baris wajib:
#   TELEGRAM_BOT_TOKEN  -> dari @BotFather
#   ALLOWED_CHAT_IDS    -> chat id Anda (dari @userinfobot)

./rprompt.exe               # lalu kirim pesan ke bot dari HP
```

Pindah komputer? `git clone` → salin `.env` → `claude login` → jalankan.

## Fitur

- **Streaming** balasan bertahap di Telegram (+ indikator tool `🔧`).
- **Kontinuitas sesi** (`--resume`); `/new` untuk reset.
- **Pindah folder dari Telegram**: `/cd`, `/project`, `/pwd` (per-chat).
- **File**: kirim foto/dokumen untuk dianalisis, `/get <path>` ambil berkas,
  gambar yang disebut di jawaban dikirim otomatis.
- **Izin tool via tombol** Telegram (opsional).
- **HTTP API OpenAI-compatible** dengan 4 engine: **Claude**, **AGY CLI**, **Kiro CLI**, & **Bob Shell**.
- Whitelist chat id + secret token; Telegram serial, API paralel.

## Perintah bot

Panduan pemakaian lengkap dari HP ada di [TELEGRAM.md](TELEGRAM.md).

| Perintah | Fungsi |
|---|---|
| `/new` | sesi baru (lupakan konteks) |
| `/continue` | lanjutkan sesi Claude terakhir di folder aktif (mis. dari PC) |
| `/status` `/pwd` | lihat folder kerja & status sesi |
| `/cd <path>` | pindah folder kerja (path bebas) |
| `/project [nama]` | pindah ke proyek whitelist (`PROJECTS` di `.env`); tanpa nama = daftar |
| `/get <path>` | kirim berkas dari folder kerja |
| `/help` | bantuan |
| teks / foto / dokumen | jadi prompt ke Claude |

Tiap chat punya folder kerja sendiri (default `WORK_DIR`); reset saat restart.

## HTTP API (OpenAI-compatible)

Aktifkan: `API_ENABLED=true` + `API_TOKEN` di `.env`. Endpoint di `LISTEN_ADDR`
(default `:8080`): `POST /v1/chat/completions`, `GET /v1/models`.

```python
from openai import OpenAI
c = OpenAI(base_url="http://localhost:8080/v1", api_key="<API_TOKEN>")
c.chat.completions.create(model="claude-code",      messages=[...])               # Claude
c.chat.completions.create(model="gemini-2.5-flash", messages=[...], stream=True)  # Gemini, streaming
c.chat.completions.create(model="kiro",             messages=[...])               # Kiro
c.chat.completions.create(model="bob",              messages=[...])               # Bob Shell
```

- **Engine via `model`**: "kiro" → Kiro CLI; "bob" atau "bob-shell" → Bob Shell; diawali `gemini` → AGY CLI; selain itu → Claude.
- **Streaming** (`stream:true`): SSE + keepalive — **wajib untuk output panjang di
  balik Cloudflare** (hindari timeout 524 ~100s). `stream:false` balas JSON utuh.
- **Stateless** (kirim seluruh `messages` tiap call), **paralel** (`API_MAX_CONCURRENT`).
- **AGY**: `irm https://antigravity.google/cli/install.ps1 | iex` lalu `agy login` → `AGY_ENABLED=true`.
- **Kiro**: `npm i -g kiro-cli` → `KIRO_ENABLED=true`.
- **Bob Shell**: `npm i -g @ibm/bob` → `BOB_ENABLED=true`.

## Ekspos ke internet

Butuh URL publik HTTPS (tunnel) untuk: HTTP API dari luar, atau bot via webhook.
rprompt bisa menjalankan cloudflared sendiri — pilih satu di `.env`:

- **URL tetap (named tunnel)** — `CLOUDFLARED_TUNNEL=<nama>`. Pasangkan dengan
  `TELEGRAM_MODE=webhook` + `SET_WEBHOOK_ON_START=true` + `WEBHOOK_URL` agar bot
  **dan** API lewat tunnel yang sama. Cukup `rprompt.exe`.
- **URL acak (quick tunnel)** — `AUTO_TUNNEL=true` (mode webhook); webhook
  didaftarkan otomatis tiap jalan.

(`CLOUDFLARED_TUNNEL` & `AUTO_TUNNEL` tidak boleh bersamaan.)

Setup named tunnel (sekali): `cloudflared login` → `cloudflared tunnel create <nama>`
→ `cloudflared tunnel route dns <nama> sub.domain-anda.com`.

> Mode `polling` tidak butuh tunnel untuk bot (lebih tahan bila tunnel mati).
> Pakai `CLOUDFLARED_TUNNEL` + polling hanya bila ingin mengekspos **API saja**.

## Izin tool

Di headless, tool yang butuh izin gagal kecuali diatur:

- **Tombol Telegram** (disarankan): `INTERACTIVE_PERMISSIONS=true` → bot kirim
  **[✅ Izinkan] [⛔ Tolak]** tiap Claude mau pakai tool.
- **Statis**: `CLAUDE_EXTRA_ARGS`, mis. `--permission-mode acceptEdits` atau
  `--dangerously-skip-permissions` (pahami risikonya).

Jalur API tak pakai tombol (tak ada chat) → atur lewat `CLAUDE_EXTRA_ARGS`.

## Konfigurasi & pengembangan

Semua opsi + penjelasan ada di [`.env.example`](.env.example).

```sh
go test ./... && go vet ./...
```

## Automated Multi-OS Build (GitHub Actions)

Repositori ini dilengkapi dengan *workflow* GitHub Actions yang otomatis melakukan kompilasi (*cross-compile*) ke berbagai OS (Windows, Linux, macOS) setiap kali ada *push* ke *branch* `main`. Hasil kompilasi juga menggunakan `garble` sebagai standar industri untuk menghindari *reverse engineering* (obfuscation), lalu diunggah otomatis ke repositori `llm-y/download`.

**Setup Environment / Secret yang Wajib Disiapkan:**
Agar *workflow* bisa berjalan dan mengunggah ke repositori target, Anda harus mengatur *environment/secret* di pengaturan repositori GitHub Anda:
1. Buat **Personal Access Token (PAT)** di akun GitHub Anda dengan izin `repo` (atau `contents: write`).
2. Buka halaman repo `rprompt` di GitHub -> **Settings** -> **Secrets and variables** -> **Actions**.
3. Klik **New repository secret**.
4. Isi **Name** dengan `GH_PAT`.
5. Isi **Secret** dengan token PAT yang baru Anda buat.

## Keamanan

Bot menjalankan Claude Code di laptop Anda. Jaga `.env`, `TELEGRAM_BOT_TOKEN`, dan
`API_TOKEN` tetap rahasia; pertahankan whitelist chat id; pakai
`--dangerously-skip-permissions` hanya bila paham risikonya.
