# ================================================================
#  PowerCodeDeck - Native Windows installer
#
#  Usage (normal PowerShell - admin not required):
#    iwr -useb https://raw.githubusercontent.com/LeeSiWal/power-code-deck/main/win-native-install.ps1 | iex
#
#  Downloads a prebuilt native pcd.exe (built with go-pty/ConPTY + pure-Go
#  SQLite - no cgo, no WSL) and runs it. No Go/Node/build required.
#
#  EXPERIMENTAL: the native Windows build compiles and launches, but the
#  ConPTY session runtime hasn't been validated on Windows hardware yet.
#  If a terminal session doesn't open, please report it (the WSL installer
#  win-install.ps1 remains the fully-tested fallback).
# ================================================================

$ErrorActionPreference = 'Stop'

# UTF-8 console so any non-ASCII output isn't garbled.
try {
    cmd /c "chcp 65001 >nul" 2>$null | Out-Null
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
} catch {}
# Ensure modern TLS for the download on older PowerShell.
try { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 } catch {}

function Say($msg, $color = 'White') { Write-Host "  $msg" -ForegroundColor $color }

$RepoRaw = 'https://raw.githubusercontent.com/LeeSiWal/power-code-deck/main/dist/pcd.exe'
$InstallDir = Join-Path $env:USERPROFILE '.powercodedeck'
$Exe = Join-Path $InstallDir 'pcd.exe'

Write-Host ""
Write-Host "  ================================================" -ForegroundColor Cyan
Write-Host "     PowerCodeDeck  Native Windows Installer" -ForegroundColor Cyan
Write-Host "  ================================================" -ForegroundColor Cyan
Write-Host ""

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

# Stop any running instance - Windows can't overwrite a running .exe.
$running = Get-Process pcd -ErrorAction SilentlyContinue
if ($running) {
    Say "Closing the running PowerCodeDeck to update it..." Yellow
    $running | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 700
}

Say "Downloading pcd.exe ..." Yellow
# Cache-buster query so a CDN doesn't serve a stale build.
$url = "$RepoRaw`?nc=$(Get-Random)"
try {
    Invoke-WebRequest -UseBasicParsing -Uri $url -Headers @{ 'Cache-Control' = 'no-cache' } -OutFile $Exe
} catch {
    Say "Download failed: $($_.Exception.Message)" Red
    Say "Close any running pcd.exe (Task Manager) and try again." Gray
    return
}
$sizeMB = [math]::Round((Get-Item $Exe).Length / 1MB, 1)
Say "Saved to $Exe ($sizeMB MB)" Green

# Strip the "Mark of the Web" so SmartScreen doesn't block the downloaded exe.
try { Unblock-File -Path $Exe -ErrorAction SilentlyContinue } catch {}

# Desktop shortcut (best-effort).
try {
    $ws = New-Object -ComObject WScript.Shell
    $lnk = $ws.CreateShortcut((Join-Path ([Environment]::GetFolderPath('Desktop')) 'PowerCodeDeck.lnk'))
    $lnk.TargetPath = $Exe
    $lnk.WorkingDirectory = $InstallDir
    $lnk.Save()
    Say "Created a Desktop shortcut: PowerCodeDeck" Gray
} catch {}

Write-Host ""
Say "Starting PowerCodeDeck..." Yellow
# Run detached in its own window; pcd opens the browser itself.
try {
    Start-Process -FilePath $Exe -WorkingDirectory $InstallDir
    Write-Host ""
    Write-Host "  ================================================" -ForegroundColor Green
    Say "Done! Open in your browser:  http://localhost:33033" Green
    Write-Host "  ================================================" -ForegroundColor Green
    Write-Host ""
    Say "Next time, double-click the 'PowerCodeDeck' desktop shortcut." Gray
} catch {
    Write-Host ""
    Say "Windows blocked the app from starting:" Red
    Say "  $($_.Exception.Message)" Gray
    Write-Host ""
    Say "This is a Windows security policy blocking an unsigned downloaded app." Yellow
    Say "Try one of these:" White
    Say "  1) Right-click $Exe > Properties > check 'Unblock' > OK, then run it." Gray
    Say "  2) If it says 'Smart App Control': Settings > Privacy & security >" Gray
    Say "     Windows Security > App & browser control > Smart App Control >" Gray
    Say "     turn it Off, then run pcd.exe again." Gray
    Say "  3) Or use the WSL install (not affected by this policy):" Gray
    Say "     iwr -useb https://raw.githubusercontent.com/LeeSiWal/power-code-deck/main/win-install.ps1 | iex" Cyan
    Write-Host ""
}
