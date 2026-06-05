# Pakai rprompt dari Telegram

Panduan memakai bot dari HP. (Pemasangan & konfigurasi ada di [README](README.md).)

## Dasar

Kirim **teks apa pun** ke bot = prompt untuk Claude Code di laptop Anda. Jawaban
muncul **bertahap** (streaming) di pesan yang sama, termasuk indikator tool yang
dipakai (mis. `🔧 Bash`). Percakapan **dilanjutkan otomatis** antar pesan —
lanjut bertanya tanpa mengulang konteks.

> Hanya chat id yang di-whitelist yang dilayani. Bila bot membalas "tidak
> diizinkan", minta admin menambah chat id Anda (bot juga menyebut id Anda).

## Perintah

| Perintah | Fungsi |
|---|---|
| (teks biasa) | jadi prompt ke Claude |
| `/new` | mulai sesi baru (lupakan konteks) |
| `/continue` | lanjutkan sesi Claude terakhir di folder aktif (mis. dari PC) |
| `/cd <path>` | pindah folder kerja (path bebas) |
| `/project [nama]` | pindah ke proyek terdaftar; tanpa nama = daftar |
| `/pwd` | lihat folder kerja saat ini |
| `/status` | folder kerja + status sesi |
| `/get <path>` | kirim berkas dari folder kerja ke chat |
| `/help` | bantuan |
| foto / dokumen | diunduh & diteruskan ke Claude untuk dianalisis |

## Alur kerja umum

### Tanya & lanjut

```
Anda : Ringkas isi README.md
Bot  : ⏳ Memproses...  ->  (jawaban)
Anda : Sekarang buatkan versi Inggrisnya
Bot  : (lanjut, ingat konteks sebelumnya)
```

`/new` untuk mulai dari nol (konteks dilupakan).

### Pindah proyek

```
/project          -> lihat daftar proyek
/project nsa      -> kerja di proyek nsa
/pwd              -> cek folder aktif
```

Atau path bebas: `/cd C:/kerjaan/x`. Tiap chat punya folder sendiri; kembali ke
default saat service di-restart.

### Lanjutkan sesi dari PC

Sedang ngoding di Claude Code di laptop, ingin lanjut dari HP:

```
/cd ../nsa        -> ke folder proyek yang sama (atau /project nsa)
/continue         -> lalu kirim prompt Anda
```

`/continue lanjutkan yang tadi` juga bisa (langsung jalan). Syarat: folder aktif
**sama** dengan tempat sesi PC dibuat. Bila tak ada sesi di folder itu, bot
memberi tahu "tidak ada sesi untuk dilanjutkan".

### Kirim foto / dokumen

Kirim foto (mis. screenshot error) atau dokumen ke bot, beri **caption** sebagai
instruksi ("kenapa error ini?"). Berkas diunduh ke folder kerja lalu dianalisis
Claude. Tanpa caption, default-nya "tolong analisis berkas terlampir".

### Ambil berkas hasil

Claude membuat/mengubah file? Ambil ke HP:

```
/get laporan.txt
/get out/grafik.png   -> berkas gambar dikirim sebagai foto
```

Gambar yang disebut Claude di jawaban juga dikirim otomatis bila berkasnya ada.

## Izin tool (bila diaktifkan)

Bila admin menyalakan izin interaktif, tiap kali Claude hendak memakai tool
berisiko (jalankan perintah, edit file), bot mengirim tombol:

> 🔐 Claude minta izin memakai tool: **Bash**
> Detail: `command: ...`
> **[✅ Izinkan]  [⛔ Tolak]**

Tekan **Izinkan** untuk lanjut, **Tolak** untuk batal. Bila didiamkan, otomatis
ditolak setelah beberapa saat.

## Catatan

- **Satu prompt pada satu waktu** — bila mengirim saat masih memproses, bot minta
  Anda tunggu lalu kirim ulang.
- **Output panjang** dipecah ke beberapa pesan (batas 4096 karakter/pesan).
- Bot menjalankan Claude dengan **akun langganan Anda** — pemakaian banyak bisa
  kena rate limit langganan.

## Bot tidak membalas?

- Pastikan service `rprompt` berjalan di laptop.
- Mode polling: cukup laptop online. Mode webhook: tunnel/URL publik harus hidup.
- Pastikan chat id Anda ada di whitelist (`ALLOWED_CHAT_IDS`).
