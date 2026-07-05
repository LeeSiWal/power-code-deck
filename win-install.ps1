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
    # UTF-8 WITHOUT BOM for piping to wsl/bash - a BOM would break the first
    # line of the piped shell script ("set: command not found").
    $OutputEncoding = New-Object System.Text.UTF8Encoding $false
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

# Set up same-Wi-Fi mobile handoff: detect the Windows LAN IP + WSL IP, write
# BIND_HOST/LAN_URL into the WSL .env, and forward the port + open the firewall.
function Setup-LanHandoff {
    $port = 33033
    $lanIp = $null
    try {
        $lanIp = (Get-NetIPConfiguration | Where-Object {
                $_.IPv4DefaultGateway -and $_.NetAdapter.Status -eq 'Up' -and
                $_.InterfaceAlias -notmatch 'WSL|Loopback|vEthernet'
            } | Select-Object -First 1).IPv4Address.IPAddress
    } catch {}
    if (-not $lanIp) { Say "LAN handoff: no LAN IP detected; skipping." Yellow; return }
    $wslIp = ((wsl hostname -I) -split '\s+' | Where-Object { $_ }) | Select-Object -First 1
    if (-not $wslIp) { return }
    $lanUrl = "http://${lanIp}:$port"

    $bash = @"
mkdir -p ~/.powercodedeck; cd ~/.powercodedeck; touch .env
grep -v '^POWERCODEDECK_BIND_HOST=' .env 2>/dev/null | grep -v '^POWERCODEDECK_LAN_URL=' > .env.tmp || true
mv .env.tmp .env
printf 'POWERCODEDECK_BIND_HOST=0.0.0.0\n' >> .env
printf 'POWERCODEDECK_LAN_URL=$lanUrl\n' >> .env
"@
    $b = [Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes(($bash -replace "`r`n", "`n")))
    wsl -d Ubuntu -u root -- bash -lc "echo $b | base64 -d | bash"

    netsh interface portproxy delete v4tov4 listenport=$port listenaddress=0.0.0.0 2>$null | Out-Null
    netsh interface portproxy add v4tov4 listenport=$port listenaddress=0.0.0.0 connectport=$port connectaddress=$wslIp | Out-Null
    netsh advfirewall firewall delete rule name="PowerCodeDeck $port" 2>$null | Out-Null
    netsh advfirewall firewall add rule name="PowerCodeDeck $port" dir=in action=allow protocol=TCP localport=$port | Out-Null
    Say "LAN handoff ready - phones on this Wi-Fi can open:  $lanUrl" Green
    Say "(WSL IP changes on reboot - re-run to refresh mobile access.)" Gray
}

# Ensure we run as Administrator. WSL feature install, port forwarding and the
# firewall rule all need it - relaunch elevated (UAC) if we aren't.
if (-not (Test-Admin)) {
    Write-Host "  Requesting administrator rights (UAC)..." -ForegroundColor Yellow
    try {
        Start-Process powershell -Verb RunAs -ArgumentList '-NoProfile', '-ExecutionPolicy', 'Bypass', '-NoExit', '-Command', "iwr -useb $RepoRaw | iex"
    } catch {
        Write-Host "  Elevation cancelled. Right-click PowerShell > 'Run as administrator', then retry." -ForegroundColor Red
    }
    return
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
# Pass the script as base64 (pure ASCII) and decode inside WSL. This is immune to
# PowerShell's native-arg quoting/encoding mangling (which otherwise turned "\r"
# into "r" and left a BOM), and to CRLF/BOM issues. Normalize to LF first.
$scriptLF = $linux -replace "`r`n", "`n"
$b64 = [Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes($scriptLF))
wsl -d Ubuntu -u root -- bash -lc "echo $b64 | base64 -d > /tmp/pcd-setup.sh && bash /tmp/pcd-setup.sh"

if ($LASTEXITCODE -ne 0) {
    Write-Host ""
    Say "Something failed during install. Check the errors above." Red
    return
}

# -- 4. LAN handoff (same Wi-Fi mobile access) --
Write-Host ""
Say "Setting up LAN handoff (mobile on same Wi-Fi)..." Yellow
Setup-LanHandoff

# -- 5. Done --
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
