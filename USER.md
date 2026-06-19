# Panduan Pengguna Rprompt

Dokumen ini adalah panduan praktis bagi Anda (pengguna akhir) yang ingin langsung menggunakan `rprompt` tanpa perlu repot melakukan *coding*. 

Aplikasi `rprompt` memungkinkan Anda:
1. Mengendalikan AI (Claude, AGY, Kiro, Bob) di komputer Anda langsung dari chat Telegram di HP Anda.
2. Membuka **OpenAI-compatible HTTP API** yang bisa diakses secara publik lewat internet dengan domain dinamis otomatis.

---

## 1. Persiapan Awal (Prasyarat Wajib)
Sebelum memulai, pastikan Anda telah menginstal program AI bawaan berikut di komputer Anda. Semuanya membutuhkan NodeJS (NPM) untuk diinstal:

1. **Claude Code**:
   - Install: Buka terminal dan jalankan `npm install -g @anthropic-ai/claude-code`
   - Login: Ketik `claude login` di terminal dan ikuti instruksi.
2. **AGY CLI**:
   - Install: `irm https://antigravity.google/cli/install.ps1 | iex`
   - Login: Ketik `agy login` di terminal.
3. **Kiro CLI**:
   - Install: Buka terminal dan jalankan `npm install -g kiro-cli` (atau sesuai petunjuk resminya).
4. **Bob Shell CLI**:
   - Install: Buka terminal dan jalankan `npm install -g @ibm/bob`

## 2. Unduh Aplikasi Rprompt & Cloudflared
1. **Rprompt**: Anda tidak perlu menginstal bahasa pemrograman (Go). Cukup unduh *file binary* yang sudah dikompilasi sesuai OS komputer Anda (Windows, Linux, atau macOS) dari repositori `https://github.com/llm-y/download` (di dalam folder `bin/`).
2. **Cloudflared**: Aplikasi ini digunakan untuk memberi Anda domain URL publik secara gratis dan instan. Unduh dari [situs resmi Cloudflare](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/) dan pastikan *file* aplikasinya ada di satu folder yang sama dengan rprompt (atau masukkan ke `PATH` OS Anda).

## 3. Persiapan Bot Telegram
1. Buka aplikasi Telegram, cari akun **@BotFather**.
2. Ketik `/newbot`, ikuti petunjuknya, dan simpan **HTTP API Token** yang diberikan.
3. Cari akun **@userinfobot** di Telegram untuk melihat **Chat ID** Anda (berupa kumpulan angka).

---

## 4. Pengaturan Variabel Environment (OS)
Agar aplikasi bisa menyambung ke Telegram Anda, setel *Environment Variables* di komputer Anda sebelum menjalankannya. Anda hanya wajib mengisi dua hal ini:

*   `TELEGRAM_BOT_TOKEN` = *(Token dari BotFather)*
*   `ALLOWED_CHAT_IDS` = *(Chat ID Anda dari userinfobot)*

> **Cara 1: Windows (Permanen via Terminal CMD)**
> Buka **Command Prompt**, lalu *copy-paste* perintah ini satu per satu (tekan Enter). Ini akan menyimpannya ke sistem secara permanen.
> ```cmd
> setx TELEGRAM_BOT_TOKEN "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
> setx ALLOWED_CHAT_IDS "987654321"
> ```
> *Catatan PENTING: Setelah menekan Enter untuk semua perintah di atas, Anda **wajib menutup** jendela CMD tersebut dan membuka CMD yang baru agar pengaturannya mulai aktif.*
>
> **Cara Alternatif Windows (Permanen via Pengaturan/GUI)**
> 1. Buka Start Menu Windows, ketik **"Environment Variables"**, lalu pilih **Edit the system environment variables**.
> 2. Klik tombol **Environment Variables...** di pojok kanan bawah.
> 3. Di bagian *User variables*, klik **New...**
> 4. Masukkan nama (contoh: `TELEGRAM_BOT_TOKEN`) dan *value*-nya. Ulangi untuk variabel kedua, lalu klik OK.

> **Tips Linux / Mac (Permanen):**
> Tambahkan baris berikut ke bagian paling bawah *file* konfigurasi shell Anda (misalnya `~/.bashrc` atau `~/.zshrc`), lalu simpan dan *restart* terminal Anda:
> ```bash
> export TELEGRAM_BOT_TOKEN="123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
> export ALLOWED_CHAT_IDS="987654321"
> ```

---

## 5. Cara Menjalankan & Menemukan URL Dinamis API
Setelah mengatur variabel *environment* di atas, Anda cukup mengeksekusi *file binary* yang Anda unduh di terminal/CMD.

Contoh di Windows: `rprompt-windows-amd64.exe`
Contoh di Linux/Mac: `./rprompt-linux-amd64`

**Menemukan Domain URL Anda:**
Karena Anda mengaktifkan `AUTO_TUNNEL=true`, aplikasi akan menjalankan Cloudflared dan mendapatkan URL acak gratis (misalnya `https://buah-buku-meja.trycloudflare.com`). 

*   Perhatikan layar terminal/CMD saat Anda menjalankan `rprompt`. 
*   Cari teks yang berbunyi kurang lebih: **`Registered webhook to https://xxxxx.trycloudflare.com/...`** atau log Cloudflared yang menunjukkan URL publik Anda.

Domain `https://xxxxx.trycloudflare.com` tersebut adalah alamat publik sistem Anda.

## 6. Cara Menggunakan HTTP API
Karena Anda sudah menyalakan `API_ENABLED=true`, Anda dapat memakai URL Cloudflared tadi layaknya API OpenAI asli dari mana saja di internet.

URL dasar API (*Base URL*) Anda adalah:
`https://xxxxx.trycloudflare.com/v1`

**Contoh Akses API Menggunakan Python:**
```python
from openai import OpenAI

# Masukkan domain cloudflared dinamis Anda
c = OpenAI(
    base_url="https://xxxxx.trycloudflare.com/v1", 
    api_key="kosong" # Jika Anda set API_TOKEN di env, masukkan di sini
)

# Chat dengan Claude Code
response = c.chat.completions.create(
    model="claude-code",      
    messages=[{"role": "user", "content": "Halo Claude!"}]
)

# Chat dengan AGY (tetap pakai prefix model "gemini")
response = c.chat.completions.create(
    model="gemini-2.5-flash", 
    messages=[{"role": "user", "content": "Halo AGY!"}]
)

# Chat dengan Bob Shell
response = c.chat.completions.create(
    model="bob", 
    messages=[{"role": "user", "content": "Halo Bob!"}]
)
```

**Selesai!** HP dan aplikasi pihak ketiga (lewat API) Anda kini bisa terhubung secara langsung ke *AI engine* yang ada di laptop/komputer Anda.
