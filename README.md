# rprompt

Remote Prompt dari telegram ke cloude code dan dari cloude code ke telegram

Memudahkan pekerjaan jadi laptop bisa di tinggal dan kita bisa prompt dari telegram hp kita kapanpu dan dimanapun. Permudah hidup dan tidak harus di depan laptop terus-terusan untuk menunggu prompt selesai, bisa sambil jalan jalan dan wisata bersama keluarga.

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
- **Kirim gambar otomatis** — bila jawaban menyebut path berkas gambar yang ada
  di direktori kerja, gambarnya ikut dikirim.
- **Unggah dari HP** — kirim foto/dokumen ke bot; berkas diunduh ke
  `WORK_DIR/uploads` dan path-nya disertakan ke prompt agar Claude bisa
  menganalisisnya.
- **Ambil berkas** — `/get <path>` mengirim berkas dari direktori kerja ke chat.
- **Izin tool via tombol** — opsional: setujui/tolak tiap pemakaian tool lewat
  tombol Telegram (lihat di bawah).
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

> Format hasil izin (`PERMISSION_RESULT_FORMAT`) defaultnya `behavior`. Kontrak
> `--permission-prompt-tool` kurang terdokumentasi di Claude Code; bila pada
> versi Anda tombol tidak berpengaruh, coba ganti ke `hook` tanpa rebuild.

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

```sh
# 1. Build
go build -o rprompt ./cmd/rprompt        # Windows: go build -o rprompt.exe ./cmd/rprompt

# 2. Jalankan tunnel pada terminal terpisah, lalu set WEBHOOK_URL di .env
#    sesuai URL publik yang dihasilkan tunnel.

# 3. Daftarkan webhook ke Telegram (sekali setiap WEBHOOK_URL berubah)
./rprompt -set-webhook

# 4. Jalankan service
./rprompt
```

Setelah itu, kirim pesan apa pun ke bot dari HP — itu menjadi prompt untuk
Claude Code, dan hasilnya dikirim balik ke Telegram.

## Perintah bot

| Perintah      | Fungsi                                             |
| ------------- | -------------------------------------------------- |
| `/help`       | Tampilkan bantuan                                  |
| `/new`        | Mulai sesi baru (lupakan konteks percakapan)       |
| `/status`     | Lihat direktori kerja & status sesi                |
| `/get <path>` | Kirim berkas dari direktori kerja ke chat          |
| foto/dokumen  | Diunduh & diteruskan ke Claude untuk dianalisis    |
| teks lain     | Dikirim sebagai prompt ke Claude Code              |

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