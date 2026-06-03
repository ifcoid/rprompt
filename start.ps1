# start.ps1 — jalankan rprompt + cloudflared named tunnel (URL tetap) sekaligus.
#
# Pemakaian:
#   .\start.ps1                 # tunnel bernama "rprompt"
#   .\start.ps1 -Tunnel namaku  # tunnel lain
#
# rprompt berjalan di foreground (Ctrl-C untuk berhenti); cloudflared dimatikan
# otomatis saat rprompt berhenti. Mode Telegram mengikuti .env (mis. polling),
# named tunnel dipakai untuk mengekspos HTTP API (/v1/*) ke internet.
#
# Prasyarat: .env terisi, `claude login`, cloudflared & (untuk Gemini) node di PATH.

param(
    [string]$Tunnel = "rprompt"
)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

if (-not (Get-Command cloudflared -ErrorAction SilentlyContinue)) {
    Write-Error "cloudflared tidak ada di PATH. Install dulu: https://github.com/cloudflare/cloudflared/releases"
    exit 1
}
if (-not (Test-Path ".\.env")) {
    Write-Error ".env tidak ditemukan. Salin .env.example -> .env lalu isi."
    exit 1
}

# Build bila binary belum ada.
if (-not (Test-Path ".\rprompt.exe")) {
    Write-Host "Build rprompt.exe..." -ForegroundColor Cyan
    go build -o rprompt.exe ./cmd/rprompt
    if ($LASTEXITCODE -ne 0) { Write-Error "go build gagal"; exit 1 }
}

# Nyalakan cloudflared named tunnel di jendela terpisah (minimized).
Write-Host "Menyalakan cloudflared tunnel '$Tunnel'..." -ForegroundColor Cyan
$cf = Start-Process cloudflared -ArgumentList @("tunnel", "run", $Tunnel) -PassThru -WindowStyle Minimized

try {
    Write-Host "Menjalankan rprompt (Ctrl-C untuk berhenti semua)..." -ForegroundColor Green
    & ".\rprompt.exe"
}
finally {
    Write-Host "Mematikan cloudflared..." -ForegroundColor Cyan
    if ($cf -and -not $cf.HasExited) {
        Stop-Process -Id $cf.Id -Force -ErrorAction SilentlyContinue
    }
}
