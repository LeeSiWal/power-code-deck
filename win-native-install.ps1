# ================================================================
#  PowerCodeDeck - Native Windows installer
#
#  Usage (normal PowerShell — admin not required):
#    iwr -useb https://raw.githubusercontent.com/LeeSiWal/power-code-deck/main/win-native-install.ps1 | iex
#
#  Downloads a prebuilt native pcd.exe (built with go-pty/ConPTY + pure-Go
#  SQLite — no cgo, no WSL) and runs it. No Go/Node/build required.
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

Say "Downloading pcd.exe ..." Yellow
try {
    Invoke-WebRequest -UseBasicParsing -Uri $RepoRaw -OutFile $Exe
} catch {
    Say "Download failed: $($_.Exception.Message)" Red
    Say "Check your internet connection and try again." Gray
    return
}
$sizeMB = [math]::Round((Get-Item $Exe).Length / 1MB, 1)
Say "Saved to $Exe ($sizeMB MB)" Green

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
Start-Process -FilePath $Exe -WorkingDirectory $InstallDir

Write-Host ""
Write-Host "  ================================================" -ForegroundColor Green
Say "Done! Open in your browser:  http://localhost:33033" Green
Write-Host "  ================================================" -ForegroundColor Green
Write-Host ""
Say "Next time, just double-click the 'PowerCodeDeck' desktop shortcut" Gray
Say "or run:  $Exe" Gray
Write-Host ""
Say "Note: Windows SmartScreen may warn about an unrecognized app." Yellow
Say "  Click 'More info' > 'Run anyway' (the file is your own build)." Gray
Write-Host ""
