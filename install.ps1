# ================================================
#  PowerCodeDeck - Windows Installer (via WSL)
# ================================================

Write-Host ""
Write-Host "  ================================================" -ForegroundColor Cyan
Write-Host "     PowerCodeDeck Windows Installer" -ForegroundColor Cyan
Write-Host "  ================================================" -ForegroundColor Cyan
Write-Host ""

# ── 1. Check if WSL is available ──
$wslInstalled = $false
try {
    $wslVersion = wsl --version 2>&1
    if ($LASTEXITCODE -eq 0) {
        $wslInstalled = $true
    }
} catch {}

if (-not $wslInstalled) {
    # Check if WSL feature is enabled but no distro installed
    $wslList = wsl --list --quiet 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Host "  WSL is not installed." -ForegroundColor Yellow
        Write-Host ""
        Write-Host "  PowerCodeDeck requires WSL (Windows Subsystem for Linux)." -ForegroundColor White
        Write-Host "  This is a one-time setup that takes a few minutes." -ForegroundColor Gray
        Write-Host ""

        $response = Read-Host "  Install WSL now? [Y/n]"
        if ($response -eq '' -or $response -match '^[Yy]') {
            Write-Host ""
            Write-Host "  Installing WSL (this may require a restart)..." -ForegroundColor Yellow
            Start-Process -FilePath "wsl" -ArgumentList "--install" -Verb RunAs -Wait
            Write-Host ""
            Write-Host "  WSL installed! Please restart your computer," -ForegroundColor Green
            Write-Host "  then run this installer again." -ForegroundColor Green
            Write-Host ""
            Read-Host "  Press Enter to exit"
            exit 0
        } else {
            Write-Host "  Installation cancelled." -ForegroundColor Red
            exit 1
        }
    }
}

# ── 2. Check if a distro is installed ──
$distros = wsl --list --quiet 2>&1 | Where-Object { $_ -and $_ -notmatch 'Windows Subsystem' }
if (-not $distros -or $distros.Count -eq 0) {
    Write-Host "  No Linux distribution found in WSL." -ForegroundColor Yellow
    Write-Host "  Installing Ubuntu..." -ForegroundColor Yellow
    Start-Process -FilePath "wsl" -ArgumentList "--install -d Ubuntu" -Verb RunAs -Wait
    Write-Host "  Ubuntu installed! Set up your username/password in the WSL window," -ForegroundColor Green
    Write-Host "  then run this installer again." -ForegroundColor Green
    Read-Host "  Press Enter to exit"
    exit 0
}

Write-Host "  ✓ WSL found" -ForegroundColor Green

# ── 3. Copy project to WSL and run install ──
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$wslPath = wsl wslpath -u "$scriptDir" 2>&1

if ($LASTEXITCODE -ne 0) {
    # Fallback: convert Windows path manually
    $drive = $scriptDir.Substring(0, 1).ToLower()
    $rest = $scriptDir.Substring(2).Replace('\', '/')
    $wslPath = "/mnt/$drive$rest"
}

Write-Host "  Project path (WSL): $wslPath" -ForegroundColor Gray
Write-Host ""
Write-Host "  Running installer inside WSL..." -ForegroundColor Yellow
Write-Host ""

# Run the Linux installer inside WSL
wsl bash -c "cd '$wslPath' && chmod +x install.sh && ./install.sh"

if ($LASTEXITCODE -eq 0) {
    Write-Host ""
    Write-Host "  ================================================" -ForegroundColor Cyan
    Write-Host "     Installation complete!" -ForegroundColor Green
    Write-Host "  ================================================" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "  To start PowerCodeDeck:" -ForegroundColor White
    Write-Host ""
    Write-Host "    wsl ~/PowerCodeDeck/pcd" -ForegroundColor Yellow
    Write-Host ""

    # Create desktop shortcut
    $desktopPath = [Environment]::GetFolderPath("Desktop")
    $shortcutPath = Join-Path $desktopPath "PowerCodeDeck.bat"

    @"
@echo off
title PowerCodeDeck
wsl bash -c "cd ~/PowerCodeDeck && ./pcd"
"@ | Out-File -FilePath $shortcutPath -Encoding ASCII

    Write-Host "  ✓ Desktop shortcut created: PowerCodeDeck.bat" -ForegroundColor Green
    Write-Host "    Double-click it to start PowerCodeDeck!" -ForegroundColor Gray
    Write-Host ""
} else {
    Write-Host ""
    Write-Host "  Installation failed. Check the errors above." -ForegroundColor Red
    Write-Host ""
}

Read-Host "  Press Enter to exit"
