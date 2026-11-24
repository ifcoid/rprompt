@echo off
setlocal
echo ========================================================
echo   PANDUAN SETUP 5 AKUN GOOGLE UNTUK AGY CLI
echo ========================================================
echo.
echo Skrip ini akan membuka AGY CLI secara berurutan untuk 5 akun.
echo Untuk setiap akun yang terbuka:
echo   1. Selesaikan login Google di browser yang terbuka.
echo   2. Setelah masuk ke prompt AGY, ketik /exit (atau tekan Ctrl+C).
echo   3. Kembali ke terminal ini dan tekan ENTER untuk lanjut ke akun berikutnya.
echo.
echo Folder penyimpanan profil:
echo %SystemDrive%%HOMEPATH%\.gemini-accounts\account1 s/d account5
echo ========================================================
echo.
pause

set "ACCOUNTS_DIR=%SystemDrive%%HOMEPATH%\.gemini-accounts"

for /L %%i in (1,1,5) do (
    echo.
    echo ----------------------------------------------------
    echo [AKUN %%i] Menyiapkan Akun ke-%%i...
    echo Folder Profil: %ACCOUNTS_DIR%\account%%i
    echo ----------------------------------------------------
    if not exist "%ACCOUNTS_DIR%\account%%i" mkdir "%ACCOUNTS_DIR%\account%%i"
    set "USERPROFILE=%ACCOUNTS_DIR%\account%%i"
    set "HOME=%ACCOUNTS_DIR%\account%%i"
    echo Membuka AGY untuk Akun %%i...
    agy.exe
    echo.
    echo Akun %%i selesai!
    if %%i LSS 5 (
        echo Tekan tombol apa saja untuk melanjutkan ke Akun %%i+1...
        pause >nul
    )
)

echo.
echo ========================================================
echo   SELURUH 5 AKUN SUDAH SELESAI DIKONFIGURASI!
echo ========================================================
echo Sekarang Anda bisa mengaktifkan rotator di .env:
echo   AGY_BIN=agy-rotator.bat
echo Lalu jalankan rprompt seperti biasa.
echo.
pause
