# ================================================================
#  PowerCodeDeck - Windows one-line installer
#
#  Usage (run in an Administrator PowerShell):
#    iwr -useb https://raw.githubusercontent.com/LeeSiWal/power-code-deck/main/win-install.ps1 | iex
#
#  Sets up WSL + Ubuntu (no interactive account - runs as root) and
#  builds PowerCodeDeck inside it. If WSL was just enabled it offers to
#  reboot and resumes automatically after you log back in.
#
#  If CPU virtualization (VT-x/AMD-V) is off, WSL2 cannot start; this
#  script then falls back to WSL1, which needs no virtualization.
#
#  Messages are intentionally ASCII/English so they never turn into "???"
#  on a non-UTF-8 Windows console.
# ================================================================

$ErrorActionPreference = 'Stop'
$RepoRaw = 'https://raw.githubusercontent.com/LeeSiWal/power-code-deck/main/win-install.ps1'

# Make the console speak UTF-8 so localized wsl.exe output is not garbled.
try {
    cmd /c "chcp 65001 >nul" 2>$null | Out-Null
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
    $OutputEncoding = [System.Text.Encoding]::UTF8
} catch {}

function Say($msg, $color = 'White') { Write-Host "  $msg" -ForegroundColor $color }

function Test-Admin {
    return ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
        ).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)
}

# Schedule this installer to run once more after the next logon, so the
# user only has to reboot - setup picks up where it left off.
function Register-Resume {
    try {
        $cmd = "powershell -NoProfile -ExecutionPolicy Bypass -Command `"iwr -useb $RepoRaw | iex`""
        New-ItemProperty -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\RunOnce' `
            -Name 'PowerCodeDeckSetup' -Value $cmd -PropertyType String -Force | Out-Null
        return $true
    } catch { return $false }
}

function Offer-Reboot {
    $resumed = Register-Resume
    Write-Host ""
    if ($resumed) {
        Say "After you reboot, setup will CONTINUE AUTOMATICALLY (no need to paste again)." Green
    } else {
        Say "After reboot, paste the same one-line command again." Yellow
    }
    $ans = Read-Host "  Reboot now? [Y/n]"
    if ($ans -match '^(n|no)$') {
        Write-Host ""
        Say "OK - reboot yourself when ready." Yellow
        if ($resumed) { Say "Setup resumes automatically after you log back in." Gray }
    } else {
        Write-Host ""
        Say "Rebooting in 10 seconds... (Ctrl+C to cancel)" Yellow
        Start-Sleep -Seconds 10
        Restart-Computer -Force
    }
}

function Get-Distros {
    # `wsl -l -q` prints UTF-16 with null bytes; strip them for matching.
    return (((wsl -l -q) 2>$null) -join "`n") -replace "`0", ""
}

# Can we actually launch the Ubuntu distro? (WSL2 fails when virtualization is off.)
function Test-DistroRuns {
    try {
        $out = (wsl -d Ubuntu -u root -- echo pcd-ok) 2>$null
        return ("$out" -match 'pcd-ok')
    } catch { return $false }
}

Write-Host ""
Write-Host "  ================================================" -ForegroundColor Cyan
Write-Host "     PowerCodeDeck  Windows Installer" -ForegroundColor Cyan
Write-Host "  ================================================" -ForegroundColor Cyan
Write-Host ""

# -- 1. Is WSL itself available? --
$wslReady = $false
try {
    wsl --status *> $null
    if ($LASTEXITCODE -eq 0) { $wslReady = $true }
} catch { $wslReady = $false }

if (-not $wslReady) {
    # Enabling the WSL feature needs admin (only this step does).
    if (-not (Test-Admin)) {
        Say "Installing WSL needs Administrator rights." Yellow
        Say "Start menu > PowerShell > right-click > 'Run as administrator', then paste again." Gray
        return
    }
    Say "Installing WSL (Windows Subsystem for Linux)..." Yellow
    try { wsl --install --no-launch } catch { wsl --install }
    Say "WSL install started. A reboot is required." Green
    Offer-Reboot
    return
}

# -- 2. Ensure the Ubuntu distro exists (no interactive account) --
if ((Get-Distros) -notmatch 'Ubuntu') {
    Say "Installing Ubuntu... (a few minutes)" Yellow
    try { wsl --install --no-launch -d Ubuntu } catch { wsl --install -d Ubuntu }
    Start-Sleep -Seconds 3
}

if ((Get-Distros) -notmatch 'Ubuntu') {
    Say "Ubuntu needs a reboot to finish installing." Yellow
    Offer-Reboot
    return
}

# -- 2b. Make sure the distro can actually run; fall back to WSL1 if not --
if (-not (Test-DistroRuns)) {
    Write-Host ""
    Say "Ubuntu could not start under WSL2." Yellow
    Say "This usually means CPU virtualization (VT-x / AMD-V) is disabled." Gray
    Say "Falling back to WSL1 (no virtualization needed)..." Yellow
    try { wsl --set-version Ubuntu 1 } catch {}
    Start-Sleep -Seconds 2

    if (-not (Test-DistroRuns)) {
        Write-Host ""
        Say "WSL2 installation failed." Red
        Say "This is usually because virtualization is disabled in BIOS/UEFI." Yellow
        Write-Host ""
        Say "Please enable virtualization:" White
        Say "  - Intel: Intel VT-x / Virtualization Technology" Gray
        Say "  - AMD:   SVM Mode / AMD-V" Gray
        Say "Then reboot Windows and run this installer again." White
        Write-Host ""
        Say "(Check current state: Task Manager > Performance > CPU > Virtualization.)" Gray
        Say "More help: https://github.com/LeeSiWal/power-code-deck/blob/main/docs/windows.md" Gray
        return
    }
    Say "OK - running on WSL1." Green
} else {
    Say "OK - WSL / Ubuntu ready." Green
}

# -- 3. Build & install PowerCodeDeck inside Ubuntu (as root) --
Write-Host ""
Say "Building PowerCodeDeck inside Ubuntu..." Yellow
Say "(installs Go, Node.js, pnpm + builds - a few minutes; no tmux needed)" Gray
Write-Host ""

$linux = @'
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y git curl ca-certificates
cd /root
if [ -d power-code-deck ]; then
  cd power-code-deck && (git pull --ff-only || true)
else
  git clone https://github.com/LeeSiWal/power-code-deck.git
  cd power-code-deck
fi
bash install.sh </dev/null
'@

# Pipe the script over stdin so Windows/Linux quoting stays simple.
$linux | wsl -d Ubuntu -u root -- bash -lc 'cat > /tmp/pcd-setup.sh && bash /tmp/pcd-setup.sh'

if ($LASTEXITCODE -ne 0) {
    Write-Host ""
    Say "Something failed during install. Check the errors above." Red
    return
}

# -- 4. Done --
Write-Host ""
Write-Host "  ================================================" -ForegroundColor Green
Say "Done! PowerCodeDeck is installed." Green
Write-Host "  ================================================" -ForegroundColor Green
Write-Host ""
Say "Start it with:" White
Write-Host '    wsl -d Ubuntu -u root -- bash -lc "cd ~/.powercodedeck && ./pcd"' -ForegroundColor Cyan
Write-Host ""
Say "Then open in your browser:  http://localhost:33033" White
Write-Host ""
