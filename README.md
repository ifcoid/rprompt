# rprompt

Remote Prompt dari telegram ke cloude code dan dari cloude code ke telegram

Memudahkan pekerjaan jadi laptop bisa di tinggal dan kita bisa prompt dari telegram hp kita kapanpu dan dimanapun. Permudah hidup dan tidak harus di depan laptop terus-terusan untuk menunggu prompt selesai, bisa sambil jalan jalan dan wisata bersama keluarga.

## Quick Start

Portabel — **satu binary, tanpa cloudflared, tanpa service**. Mode `polling`
membuat bot menarik update sendiri dari Telegram, jadi tidak perlu URL publik
(jalan di balik NAT). Cocok dibawa pindah komputer.

**Prasyarat:** [Go](https://go.dev/dl/) 1.26+, [Claude Code CLI](https://code.claude.com)
(sudah `claude login`). *(cloudflared hanya perlu bila pakai mode webhook.)*

```sh
# 1. Ambil kode & build (hasilnya satu file binary)
git clone https://github.com/ifcoid/rprompt.git
cd rprompt
go build -o rprompt.exe ./cmd/rprompt        # Linux/Mac: go build -o rprompt ./cmd/rprompt

# 2. Konfigurasi
copy .env.example .env                        # Linux/Mac: cp .env.example .env
#   Isi minimal di .env:
#     TELEGRAM_BOT_TOKEN   -> token dari @BotFather
#     ALLOWED_CHAT_IDS     -> chat id Anda (dari @userinfobot)
#     TELEGRAM_MODE=polling   (default; tanpa webhook/cloudflared)

# 3. Jalankan
./rprompt.exe
#   Tunggu log "mode Telegram: long polling", lalu kirim pesan ke bot dari HP.
#   Ctrl-C untuk berhenti.
```

Itu saja — tidak perlu cloudflared, URL publik, Windows service, atau Task
Scheduler. Jalankan `./rprompt.exe` saat ingin memakai bot, tutup saat selesai.
Pindah komputer? `git clone` lagi, salin `.env`, `claude login`, jalankan.

> **Mode webhook (opsional):** bila ingin URL publik (mis. untuk memanggil HTTP
> API dari internet), set `TELEGRAM_MODE=webhook` + `AUTO_TUNNEL=true` (quick
> tunnel) atau named tunnel — lihat [Menjalankan](#menjalankan).
>
> **Engine Gemini & API (opsional):** lihat [HTTP API](#http-api-openai-compatible).

## Cara kerja

```
HP (Telegram)  ──►  Telegram Bot API  ──►  webhook  ──►  service rprompt  ──►  claude -p
      ▲                                                          │
      └──────────────────  hasil dikirim balik  ◄───────────────┘
```

Service `rprompt` berjalan di laptop yang sudah terpasang Claude Code. Ia
menerima pesan Telegram lewat **webhook**, menjalankan prompt dengan
`claude -p` (mode headless), lalu mengirim hasilnya kembali ke chat. Konteks
percakapan dilanjutkan otomatis antar pesan (via `--resume`).

Keamanan: hanya chat id yang ada di **whitelist** yang dilayani, dan setiap
update Telegram diverifikasi dengan `secret_token`.

## Fitur

- **Streaming output** — hasil tampil bertahap dengan mengedit satu pesan
  Telegram (rolling ke pesan baru bila melebihi 4096 karakter), termasuk
  indikator tool yang sedang dipakai (mis. `🔧 Bash`).
- **Kontinuitas sesi** — percakapan dilanjutkan otomatis; `/new` untuk reset.
- **Pindah direktori dari Telegram** — `/cd`, `/project` (whitelist), `/pwd`;
  tiap chat punya direktori kerja sendiri (lihat di bawah).
- **Kirim gambar otomatis** — bila jawaban menyebut path berkas gambar yang ada
  di direktori kerja, gambarnya ikut dikirim.
- **Unggah dari HP** — kirim foto/dokumen ke bot; berkas diunduh ke
  `WORK_DIR/uploads` dan path-nya disertakan ke prompt agar Claude bisa
  menganalisisnya.
- **Ambil berkas** — `/get <path>` mengirim berkas dari direktori kerja ke chat.
- **Izin tool via tombol** — opsional: setujui/tolak tiap pemakaian tool lewat
  tombol Telegram (lihat di bawah).
- **HTTP API OpenAI-compatible** — opsional: `POST /v1/chat/completions` agar
  aplikasi/SDK OpenAI bisa prompting ke Claude (lihat di bawah).
- **Engine ganda di API** — opsional: pilih Claude atau **Gemini CLI** lewat
  field `model` pada endpoint OpenAI yang sama.
- **Whitelist + secret token**, antrian 1 prompt, shutdown rapi.

### Izin tool

Di mode headless tidak ada prompt "izinkan/tolak" interaktif, jadi tool yang
butuh izin akan **gagal** kecuali diatur. Ada dua pendekatan:

1. **Statis** lewat `CLAUDE_EXTRA_ARGS` (lihat `.env.example`) — dari paling
   aman `--permission-mode acceptEdits` / `--allowedTools ...` hingga
   `--dangerously-skip-permissions` (jalankan semua tanpa bertanya).
2. **Interaktif via tombol Telegram** (disarankan) — set
   `INTERACTIVE_PERMISSIONS=true`. Saat Claude hendak memakai tool yang butuh
   izin, bot mengirim **[✅ Izinkan] [⛔ Tolak]** ke Telegram dan Claude
   menunggu keputusan Anda (auto-tolak setelah `APPROVAL_TIMEOUT_SECONDS`).

#### Cara kerja izin interaktif

```
Telegram  ◄── tombol Izinkan/Tolak ──┐
                                      │
claude -p ──(--permission-prompt-tool)──►  server MCP lokal (in-process)
                                      │         │
                                      └─ approval.Registry ─► kirim ke Telegram
```

Service menjalankan **server MCP kecil di localhost** (`MCP_ADDR`, default
`127.0.0.1:8765`) dan menyuruh Claude memakainya sebagai
`--permission-prompt-tool`. MCP ini meneruskan tiap permintaan izin ke Telegram
dan mengembalikan keputusan Anda ke Claude. Server MCP cukup di localhost — tidak
perlu diekspos ke internet.

> Format hasil izin (`PERMISSION_RESULT_FORMAT`) defaultnya `behavior` dan sudah
> **terverifikasi bekerja** pada Claude Code CLI v2.1.159 (tap "Izinkan" benar-
> benar membuat Claude menjalankan tool). Kontrak `--permission-prompt-tool`
> kurang terdokumentasi; bila pada versi lain tombol tidak berpengaruh, ganti ke
> `hook` tanpa rebuild.

## Prasyarat

- Go 1.26+
- Claude Code CLI (`claude`) terpasang & sudah login
- Bot Telegram (buat lewat [@BotFather](https://t.me/BotFather), catat token-nya)
- Sebuah URL publik HTTPS yang menunjuk ke service ini. Karena laptop umumnya
  di balik NAT, paling mudah pakai tunnel, mis. `cloudflared`:
  `cloudflared tunnel --url http://localhost:8080`

## Konfigurasi

Salin `.env.example` menjadi `.env`, lalu isi minimal:

- `TELEGRAM_BOT_TOKEN` — token dari @BotFather
- `ALLOWED_CHAT_IDS` — chat id Anda (pisahkan koma untuk lebih dari satu)
- `WEBHOOK_URL` — base URL publik dari tunnel/VPS
- `WEBHOOK_SECRET` — string acak panjang

Lihat `.env.example` untuk daftar lengkap dan penjelasannya.

> Tidak tahu chat id Anda? Jalankan service, kirim pesan ke bot dari HP; bot
> akan membalas dengan chat id Anda (atau pakai [@userinfobot](https://t.me/userinfobot)).

## Menjalankan

### Cara mudah — auto-tunnel (disarankan)

Set `AUTO_TUNNEL=true` di `.env`, lalu cukup:

```sh
go build -o rprompt ./cmd/rprompt        # Windows: go build -o rprompt.exe ./cmd/rprompt
./rprompt
```

Service akan menjalankan `cloudflared` sendiri, mengambil URL-nya, lalu
mendaftarkan webhook otomatis — tiap kali dijalankan. Saat dihentikan (Ctrl-C),
webhook dihapus & cloudflared dimatikan. Tidak perlu salin-tempel URL.

> Quick-tunnel cloudflared butuh ~1-2 menit agar DNS-nya ter-publish sebelum
> webhook bisa didaftarkan (service menunggu otomatis lalu mendaftar). Ini
> wajar. Untuk URL tetap & tanpa tunggu, gunakan **named tunnel** (lihat bawah).

### Cara manual (URL/tunnel sendiri)

```sh
go build -o rprompt ./cmd/rprompt
cloudflared tunnel --url http://localhost:8080   # salin URL → WEBHOOK_URL di .env (AUTO_TUNNEL=false)
./rprompt -set-webhook                            # ulangi tiap WEBHOOK_URL berubah
./rprompt
```

Setelah itu, kirim pesan apa pun ke bot dari HP — itu menjadi prompt untuk
Claude Code, dan hasilnya dikirim balik ke Telegram.

> **URL permanen:** quick-tunnel memberi URL acak tiap jalan. Untuk hostname
> tetap (tanpa tunggu DNS), pakai cloudflared *named tunnel* dengan domain Anda
> di Cloudflare, set `WEBHOOK_URL` sekali, dan `AUTO_TUNNEL=false`.

## Perintah bot

| Perintah          | Fungsi                                              |
| ----------------- | --------------------------------------------------- |
| `/help`           | Tampilkan bantuan                                   |
| `/new`            | Mulai sesi baru (lupakan konteks percakapan)        |
| `/status`         | Lihat direktori kerja & status sesi                 |
| `/pwd`            | Lihat direktori kerja saat ini                      |
| `/cd <path>`      | Pindah direktori kerja (path bebas)                 |
| `/project [nama]` | Pindah ke proyek terdaftar (tanpa nama = daftar)    |
| `/get <path>`     | Kirim berkas dari direktori kerja ke chat           |
| foto/dokumen      | Diunduh & diteruskan ke Claude untuk dianalisis     |
| teks lain         | Dikirim sebagai prompt ke Claude Code               |

### Pindah direktori kerja dari Telegram

Tiap chat punya direktori kerja sendiri (default `WORK_DIR`). Ubah saat itu juga
tanpa restart:

- `/cd C:/proyek/x` — pindah ke path bebas (folder harus ada).
- `/project web` — pindah ke proyek dari **whitelist** `PROJECTS` di `.env`
  (mis. `PROJECTS=web=C:/proyek/web;api=C:/proyek/api`). `/project` tanpa nama
  menampilkan daftar. Lebih aman karena Claude hanya bisa diarahkan ke folder
  yang Anda daftarkan.
- `/pwd` — lihat folder aktif. Perubahan berlaku untuk prompt, `/get`, dan
  unggahan berikutnya pada chat itu. (Reset ke default saat service di-restart.)

## HTTP API (OpenAI-compatible)

Selain Telegram, rprompt mengekspos **API yang kompatibel dengan OpenAI**,
sehingga bisa dipakai langsung oleh OpenAI SDK, LangChain, LiteLLM, Open WebUI,
dll. — cukup arahkan `base_url` ke rprompt dan pakai `API_TOKEN` sebagai "API
key". Aktifkan dengan `API_ENABLED=true` + `API_TOKEN`.

Endpoint (di server yang sama, `LISTEN_ADDR`):
- `POST /v1/chat/completions` (non-streaming)
- `GET  /v1/models`

Header wajib: `Authorization: Bearer <API_TOKEN>`

### Contoh curl

```sh
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-code",
    "messages": [
      {"role": "system", "content": "Jawab singkat."},
      {"role": "user", "content": "Sebut 1 warna."}
    ]
  }'
# -> {"object":"chat.completion","choices":[{"message":{"role":"assistant","content":"Merah."},"finish_reason":"stop"}], ...}
```

### Contoh OpenAI SDK (Python)

```python
from openai import OpenAI
client = OpenAI(base_url="http://localhost:8080/v1", api_key="<API_TOKEN>")
resp = client.chat.completions.create(
    model="claude-code",
    messages=[{"role": "user", "content": "Halo"}],
)
print(resp.choices[0].message.content)
```

### Mengakses API dari luar (ekspos manual)

Pada mode **polling** (default portabel), API hanya didengar di `localhost:8080`
— bot Telegram tetap jalan tanpa tunnel, tapi API tidak otomatis terekspos.
Untuk memanggil API dari internet, jalankan **cloudflared secara manual** di
terminal terpisah (rprompt tetap berjalan apa adanya):

```sh
# Opsi A — quick tunnel (URL acak tiap jalan)
cloudflared tunnel --url http://localhost:8080
#   -> API di https://xxx.trycloudflare.com/v1/chat/completions

# Opsi B — named tunnel (URL tetap; lihat "Menjalankan" untuk setup)
cloudflared tunnel run <nama-tunnel>
#   -> API di https://nama.domain-anda.com/v1/chat/completions
```

`AUTO_TUNNEL` sengaja TIDAK dipakai untuk ini karena ia terikat ke alur webhook
(menyalakan cloudflared **dan** mendaftarkan webhook Telegram); di mode polling
alur webhook dilewati. Jadi untuk API, cloudflared dijalankan terpisah.

**Praktis (Windows):** `start.ps1` menjalankan rprompt + named tunnel sekaligus,
dan mematikan cloudflared saat rprompt berhenti:

```powershell
.\start.ps1                 # named tunnel "rprompt"
.\start.ps1 -Tunnel namaku  # nama tunnel lain
```

> Endpoint `/v1/*` jadi publik tetapi dilindungi `API_TOKEN` (Bearer) — jaga
> token tetap rahasia & panjang. Hanya `/healthz` & `/v1/*` yang relevan; jalur
> webhook tidak dipakai di mode polling.

### Catatan penting

- **Stateless** (sesuai OpenAI): kirim seluruh `messages` tiap request. rprompt
  meratakannya jadi satu prompt dan menjalankan `claude -p` sebagai sesi baru —
  tidak ada kontinuitas sesi otomatis seperti di Telegram.
- **Field `model` memilih engine** (lihat Gemini di bawah): `model` diawali
  `gemini` → Gemini CLI; selain itu → Claude (default `claude-code`). Untuk
  Claude, nama model spesifik diabaikan (model ikut langganan CLI). `/v1/models`
  mengiklankan `claude-code` (dan `gemini-2.5-flash` bila Gemini aktif).
- **Streaming (SSE)** — kirim `stream:true` untuk balasan bertahap format SSE
  OpenAI (`chat.completion.chunk` … `data: [DONE]`). **Disarankan untuk output
  panjang di balik Cloudflare**: koneksi disisipi keepalive sehingga tidak kena
  timeout **524** (~100s). `stream:false` (default) membalas JSON utuh — bisa
  kena 524 bila jawaban makan >100s.
- **Token usage** dilaporkan `0` (tidak dilacak).
- **Konkurensi:** API berjalan **paralel** (tidak diantrikan). Batasi dengan
  `API_MAX_CONCURRENT` (0 = tak terbatas); penuh -> `429`. **Telegram tetap
  serial** dan terpisah dari API.
- **Izin tool:** jalur API memakai runner **tanpa** izin interaktif. Meski
  `INTERACTIVE_PERMISSIONS=true`, tool via API mengikuti `CLAUDE_EXTRA_ARGS`
  (mis. `--permission-mode acceptEdits`). Konkurensi tinggi berbagi
  kuota/rate-limit langganan Claude Anda.

### Engine Gemini (opsional)

API yang sama bisa **diteruskan ke Gemini CLI** dengan memilih `model`. Aktifkan
dengan `GEMINI_ENABLED=true`, dan pastikan Gemini CLI terpasang & terotentikasi:

```sh
npm install -g @google/gemini-cli      # butuh Node 20+
gemini                                  # login Google sekali (OAuth), atau set GEMINI_API_KEY
```

Lalu cukup ganti `model` pada request yang sama:

```python
client.chat.completions.create(model="gemini-2.5-flash",
    messages=[{"role": "user", "content": "Halo"}])   # -> dijalankan oleh Gemini
```

- `model` diawali `gemini` → Gemini; selain itu → Claude. Satu base_url, dua engine.
- Gemini dipanggil headless (prompt via stdin) `gemini --output-format json` +
  `GEMINI_EXTRA_ARGS`; rprompt mem-parse field `response`. Disarankan
  `GEMINI_EXTRA_ARGS=-m gemini-2.5-flash --approval-mode yolo`.
- **Auth:** memakai login `gemini` yang sudah ada (`~/.gemini`) **atau**
  `GEMINI_API_KEY` — API key tidak wajib bila sudah login. Pastikan rprompt jalan
  sebagai user yang login (agar bisa baca `~/.gemini`). Bila `GEMINI_API_KEY`
  terlanjur ter-set di environment tapi Anda ingin memakai **login CLI**, set
  `GEMINI_USE_OAUTH=true` — rprompt akan menghapus key itu dari subprocess gemini.
- **Windows:** `GEMINI_BIN=gemini` (nama pendek) cukup bila ada di PATH — rprompt
  meresolve-nya & membungkus shim `.cmd` via `cmd /c` otomatis (full path juga
  boleh). Folder Node (mis. `C:\Program Files\nodejs`) **harus ada di PATH** karena
  `gemini` memanggil `node`. Hal yang sama: `CLAUDE_BIN=claude` cukup bila di PATH.
- Jalur Gemini juga **stateless** & berbagi batas konkurensi API yang sama.

## Pengembangan

```sh
go test ./...     # jalankan unit test
go vet ./...      # analisis statis
gofmt -l .        # cek format (output kosong = rapi)
```

## Catatan keamanan

Bot ini menjalankan Claude Code di laptop Anda. Jaga `TELEGRAM_BOT_TOKEN` dan
`.env` tetap rahasia, pertahankan whitelist chat id, dan gunakan
`CLAUDE_EXTRA_ARGS=--dangerously-skip-permissions` **hanya** bila Anda paham
risikonya (Claude akan menjalankan tool tanpa meminta izin).