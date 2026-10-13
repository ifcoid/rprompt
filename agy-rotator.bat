@echo off
setlocal EnableDelayedExpansion

:: Lokasi folder dasar untuk 5 profil akun
set "ACCOUNTS_DIR=%SystemDrive%%HOMEPATH%\.gemini-accounts"
set "STATE_FILE=%TEMP%\agy_rotator_index.txt"

:: Baca index akun terakhir
set "INDEX=1"
if exist "%STATE_FILE%" (
    set /p INDEX=<"%STATE_FILE%"
)

:: Validasi agar index berada di rentang 1 - 5
if "!INDEX!"=="" set "INDEX=1"
if !INDEX! LSS 1 set "INDEX=1"
if !INDEX! GTR 5 set "INDEX=1"

:: Hitung indeks untuk request berikutnya (Round-Robin 1 -> 2 -> 3 -> 4 -> 5 -> 1)
set /a "NEXT=(INDEX %% 5) + 1"
> "%STATE_FILE%" echo !NEXT!

:: Alihkan USERPROFILE dan HOME ke folder akun terpilih
set "TARGET_PROFILE=%ACCOUNTS_DIR%\account!INDEX!"
if not exist "%TARGET_PROFILE%" (
    mkdir "%TARGET_PROFILE%" 2>nul
)

set "USERPROFILE=%TARGET_PROFILE%"
set "HOME=%TARGET_PROFILE%"

:: Jalankan binary agy dengan seluruh argumen yang diteruskan
agy.exe %*
