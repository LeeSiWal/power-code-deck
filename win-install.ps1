# ================================================================
#  PowerCodeDeck - Windows one-line installer
#
#  Usage (run in an Administrator PowerShell):
#    iwr -useb https://raw.githubusercontent.com/LeeSiWal/power-code-deck/main/win-install.ps1 | iex
#
#  Sets up WSL + Ubuntu, creates a normal Linux user (not root), and builds
#  PowerCodeDeck under that user's home. Creates 3 desktop shortcuts (run /
#  open workspace / open in VS Code). If WSL was just enabled it offers to
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
mkdir -p ~/PowerCodeDeck; cd ~/PowerCodeDeck; touch .env
grep -v '^POWERCODEDECK_BIND_HOST=' .env 2>/dev/null | grep -v '^POWERCODEDECK_LAN_URL=' > .env.tmp || true
mv .env.tmp .env
printf 'POWERCODEDECK_BIND_HOST=0.0.0.0\n' >> .env
printf 'POWERCODEDECK_LAN_URL=$lanUrl\n' >> .env
"@
    $b = [Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes(($bash -replace "`r`n", "`n")))
    wsl -d Ubuntu -u $script:LinuxUser -- bash -lc "echo $b | base64 -d | bash"

    # Bind the forward to the LAN IP ONLY, never 0.0.0.0 — a 0.0.0.0 rule also
    # captures 127.0.0.1, shadowing WSL2's native localhost forwarding and
    # sending localhost to a (soon-stale) WSL IP. Remove any old 0.0.0.0 rule.
    netsh interface portproxy delete v4tov4 listenport=$port listenaddress=0.0.0.0 2>$null | Out-Null
    netsh interface portproxy delete v4tov4 listenport=$port listenaddress=$lanIp 2>$null | Out-Null
    netsh interface portproxy add v4tov4 listenport=$port listenaddress=$lanIp connectport=$port connectaddress=$wslIp | Out-Null
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

# -- 3. Provision a NORMAL Linux user (not root) --
# Non-devs never see /root: PowerCodeDeck runs as a regular user whose home is
# reachable from Windows (\\wsl.localhost\Ubuntu\home\<user>) and from VS Code
# Remote-WSL. Derive a safe Linux username from the Windows username.
$rawUser = "$env:USERNAME"
$LinuxUser = ($rawUser -replace '[^A-Za-z0-9_-]', '').ToLower()
if ([string]::IsNullOrEmpty($LinuxUser) -or ($LinuxUser -notmatch '^[a-z]')) { $LinuxUser = 'pcduser' }
if ($LinuxUser.Length -gt 32) { $LinuxUser = $LinuxUser.Substring(0, 32) }
$script:LinuxUser = $LinuxUser

Write-Host ""
Say "Setting up a normal Linux user '$LinuxUser' (not root)..." Yellow

$provision = @'
set -e
U=__USER__
if ! id -u "$U" >/dev/null 2>&1; then
  adduser --disabled-password --gecos "" "$U"
fi
usermod -aG sudo "$U" || true
printf '%s ALL=(ALL) NOPASSWD:ALL\n' "$U" > /etc/sudoers.d/$U
chmod 440 /etc/sudoers.d/$U
# Make this the default WSL user so `wsl` and VS Code Remote connect as them.
if [ -f /etc/wsl.conf ] && grep -q '^\[user\]' /etc/wsl.conf; then
  if grep -q '^default=' /etc/wsl.conf; then
    sed -i "s/^default=.*/default=$U/" /etc/wsl.conf
  else
    printf 'default=%s\n' "$U" >> /etc/wsl.conf
  fi
else
  printf '\n[user]\ndefault=%s\n' "$U" >> /etc/wsl.conf
fi
# Migrate any earlier root-owned install/projects into the user's home.
mkdir -p /home/$U
for d in PowerCodeDeck power-code-deck code; do
  if [ -e /root/$d ] && [ ! -e /home/$U/$d ]; then mv /root/$d /home/$U/$d; fi
done
# One-time reset of a migrated .env: drop the absolute DB_PATH pinned to /root
# (pcd re-resolves) AND any leftover auth (PIN/password) so the documented
# no-auth default is restored. This runs only during the root->user migration,
# so a PIN the user intentionally sets later is never wiped.
if [ -f /home/$U/PowerCodeDeck/.env ]; then
  grep -vE '^(POWERCODEDECK|AGENTDECK)_(DB_PATH|AUTH_ENABLED|AUTH_METHOD|PIN|PASSWORD_HASH)=' /home/$U/PowerCodeDeck/.env > /home/$U/PowerCodeDeck/.env.tmp 2>/dev/null && mv /home/$U/PowerCodeDeck/.env.tmp /home/$U/PowerCodeDeck/.env || rm -f /home/$U/PowerCodeDeck/.env.tmp
fi
mkdir -p /home/$U/PowerCodeDeck/projects
chown -R "$U":"$U" /home/$U
'@
$provision = $provision -replace '__USER__', $LinuxUser
$pv = [Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes(($provision -replace "`r`n", "`n")))
wsl -d Ubuntu -u root -- bash -lc "echo $pv | base64 -d > /tmp/pcd-provision.sh && bash /tmp/pcd-provision.sh"
# Trust the OUTCOME (does the user exist?), not just the exit code.
$userExists = (wsl -d Ubuntu -u root -- bash -lc "id -u '$LinuxUser' >/dev/null 2>&1 && echo yes") 2>$null
if ("$userExists".Trim() -ne 'yes') {
    Write-Host ""; Say "Failed to create the Linux user '$LinuxUser'. See errors above." Red; return
}
Say "Linux user '$LinuxUser' ready." Green
wsl --terminate Ubuntu 2>$null | Out-Null   # apply the default-user change

# -- 4. Build & install PowerCodeDeck AS that user --
Write-Host ""
Say "Building PowerCodeDeck (as '$LinuxUser')..." Yellow
Say "(installs Go, Node.js, pnpm + builds - a few minutes; passwordless sudo)" Gray
Write-Host ""

$linux = @'
set -e
export DEBIAN_FRONTEND=noninteractive
sudo apt-get update
sudo apt-get install -y git curl ca-certificates
cd ~
# A prior clone can be "up to date" yet have a deleted working-tree file (git
# pull won't restore it). Re-clone if incomplete, then hard-reset to origin/main
# so every tracked file (install.sh included) is guaranteed present.
if [ ! -f power-code-deck/install.sh ] || [ ! -d power-code-deck/.git ]; then
  rm -rf power-code-deck
  git clone https://github.com/LeeSiWal/power-code-deck.git
fi
cd power-code-deck
git fetch origin 2>/dev/null && git reset --hard origin/main 2>/dev/null || true
if [ ! -f install.sh ]; then
  cd ~ && rm -rf power-code-deck && git clone https://github.com/LeeSiWal/power-code-deck.git && cd power-code-deck
fi
bash install.sh </dev/null
# Write an EXPLICIT no-auth default + WSL-home project location into .env. The
# explicit AUTH_ENABLED=false is essential: without it pcd treats every launch
# as a first run and shows the interactive auth wizard (which can end up
# enabling a PIN). Strip any prior auth/workspace lines first so this is
# idempotent and clears leftovers from an earlier PIN setup.
cd ~/PowerCodeDeck
touch .env
grep -vE '^(POWERCODEDECK_|AGENTDECK_)(AUTH_ENABLED|AUTH_METHOD|PIN|PASSWORD_HASH|WORKSPACE_ROOT)=' .env > .env.tmp 2>/dev/null || true
mv .env.tmp .env 2>/dev/null || true
printf 'POWERCODEDECK_AUTH_ENABLED=false\nPOWERCODEDECK_AUTH_METHOD=none\nPOWERCODEDECK_WORKSPACE_ROOT=%s/PowerCodeDeck/projects\n' "$HOME" >> .env
mkdir -p ~/PowerCodeDeck/projects
'@
$bl = [Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes(($linux -replace "`r`n", "`n")))
wsl -d Ubuntu -u $LinuxUser -- bash -lc "echo $bl | base64 -d > /tmp/pcd-setup.sh && bash /tmp/pcd-setup.sh"
if ($LASTEXITCODE -ne 0) {
    # A build hiccup should NOT stop us from creating the desktop shortcuts if a
    # binary is already in place from a previous run.
    $havePcd = (wsl -d Ubuntu -u $LinuxUser -- bash -lc "test -x ~/PowerCodeDeck/pcd && echo yes") 2>$null
    if ("$havePcd".Trim() -ne 'yes') {
        Write-Host ""
        Say "Something failed during install and no existing binary was found." Red
        Say "Fix the errors above and re-run. Shortcuts are still created below." Yellow
    } else {
        Write-Host ""
        Say "Build step reported an error, but an existing pcd binary was found - continuing." Yellow
    }
}

# -- 5. LAN handoff (same Wi-Fi mobile access) --
Write-Host ""
Say "Setting up LAN handoff (mobile on same Wi-Fi)..." Yellow
Setup-LanHandoff

# -- 6. Windows launcher files + 3 desktop shortcuts (no WSL path to memorize) --
$appDir  = Join-Path $env:LOCALAPPDATA 'PowerCodeDeck'
$projUnc = "\\wsl.localhost\Ubuntu\home\$LinuxUser\PowerCodeDeck\projects"
try {
    New-Item -ItemType Directory -Force -Path $appDir | Out-Null

    # Launcher 1: start pcd if it isn't already up, then open the browser.
    # Decide by the actual WSL pcd process, NOT the Windows port: a leftover
    # netsh portproxy keeps 33033 "listening" even when pcd is down, which would
    # otherwise make us skip starting it and open a dead/old page.
    $launchPs1 = @"
`$ErrorActionPreference = 'SilentlyContinue'
`$user = '$LinuxUser'
`$running = (wsl -d Ubuntu -u `$user -- bash -lc 'pgrep -f PowerCodeDeck/pcd >/dev/null 2>&1 && echo yes') 2>`$null
if ("`$running".Trim() -ne 'yes') {
  Start-Process -WindowStyle Hidden wsl -ArgumentList '-d','Ubuntu','-u',`$user,'--','bash','-lc','~/PowerCodeDeck/pcd'
  Start-Sleep -Seconds 3
}
Start-Process 'http://localhost:33033'
"@
    Set-Content -Path (Join-Path $appDir 'launch-powercodedeck.ps1') -Value $launchPs1 -Encoding UTF8

    # Launcher 3: open the projects folder in VS Code via Remote WSL.
    $vscodePs1 = @"
`$user = '$LinuxUser'
`$has = (wsl -d Ubuntu -u `$user -- bash -lc 'command -v code >/dev/null 2>&1 && echo yes') 2>`$null
if ("`$has".Trim() -eq 'yes') {
  wsl -d Ubuntu -u `$user -- bash -lc 'cd ~/PowerCodeDeck/projects && code .'
} else {
  Write-Host ''
  Write-Host '  VS Code WSL integration was not found.' -ForegroundColor Yellow
  Write-Host '  Install Visual Studio Code + the WSL extension, then open VS Code once'
  Write-Host "  and run 'WSL: Connect to WSL'. After that, run this shortcut again."
  Write-Host ''
  Read-Host '  Press Enter to close'
}
"@
    Set-Content -Path (Join-Path $appDir 'open-vscode-wsl.ps1') -Value $vscodePs1 -Encoding UTF8

    $ws = New-Object -ComObject WScript.Shell

    # Resolve EVERY plausible Desktop folder. OneDrive "Known Folder" redirection
    # and running elevated can make GetFolderPath('Desktop') differ from the
    # desktop the user actually sees, so we write to all of them.
    $desktops = New-Object System.Collections.Generic.List[string]
    foreach ($cand in @(
        [Environment]::GetFolderPath('Desktop'),
        (Join-Path $env:USERPROFILE 'Desktop'),
        (Join-Path $env:USERPROFILE 'OneDrive\Desktop'),
        (Join-Path $env:OneDrive 'Desktop')
    )) {
        if ($cand -and (Test-Path $cand) -and -not $desktops.Contains($cand)) { $desktops.Add($cand) }
    }
    $programs = [Environment]::GetFolderPath('Programs')

    # Build the Korean labels from Unicode code points. Raw Korean string
    # literals can arrive as "?" through `iwr | iex` (source-encoding loss), and
    # "?" is an illegal filename char that makes .Save() fail. Code points are
    # pure ASCII in the source, so they survive any download encoding. Short
    # names — the containing "PowerCodeDeck" folder already provides the brand.
    $lblRun  = -join ([char[]](0xC2E4,0xD589))                             # 실행
    $lblWork = -join ([char[]](0xC791,0xC5C5,0xD3F4,0xB354))               # 작업폴더
    $lblCode = 'VSCode' + [char]0xB85C + ' ' + (-join ([char[]](0xC5F4,0xAE30)))  # VSCode로 열기
    $lblOld  = 'PowerCodeDeck ' + (-join ([char[]](0xB370,0xC774,0xD130,0x20,0xD3F4,0xB354)))   # old loose "PowerCodeDeck 데이터 폴더"

    $made = @()
    foreach ($dir in $desktops) {
        # Remove older LOOSE shortcuts from the desktop root (pre-folder layout).
        Remove-Item -LiteralPath (Join-Path $dir 'PowerCodeDeck.lnk')                        -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath (Join-Path $dir "$lblOld.lnk")                              -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath (Join-Path $dir "PowerCodeDeck $lblRun.lnk")                -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath (Join-Path $dir ('PowerCodeDeck ' + $lblWork + '.lnk'))     -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath (Join-Path $dir ('PowerCodeDeck ' + $lblCode + '.lnk'))     -ErrorAction SilentlyContinue

        # Group all 3 shortcuts inside a single "PowerCodeDeck" folder.
        $grp = Join-Path $dir 'PowerCodeDeck'
        New-Item -ItemType Directory -Force -Path $grp | Out-Null

        $s1 = $ws.CreateShortcut((Join-Path $grp "$lblRun.lnk"))
        $s1.TargetPath   = 'powershell.exe'
        $s1.Arguments    = "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$appDir\launch-powercodedeck.ps1`""
        $s1.IconLocation = "$env:SystemRoot\System32\wsl.exe,0"
        $s1.Save()

        $s2 = $ws.CreateShortcut((Join-Path $grp "$lblWork.lnk"))
        $s2.TargetPath = 'explorer.exe'
        $s2.Arguments  = $projUnc
        $s2.Save()

        $s3 = $ws.CreateShortcut((Join-Path $grp "$lblCode.lnk"))
        $s3.TargetPath = 'powershell.exe'
        $s3.Arguments  = "-NoProfile -ExecutionPolicy Bypass -File `"$appDir\open-vscode-wsl.ps1`""
        $s3.Save()

        $made += $grp
    }
    if ($programs -and (Test-Path $programs)) {
        Remove-Item -LiteralPath (Join-Path $programs 'PowerCodeDeck.lnk') -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath (Join-Path $programs "$lblOld.lnk")       -ErrorAction SilentlyContinue
    }

    # Keep a one-word `pcd` command on PATH too (WindowsApps is on PATH).
    $winApps = Join-Path $env:LOCALAPPDATA 'Microsoft\WindowsApps'
    New-Item -ItemType Directory -Force -Path $winApps | Out-Null
    Set-Content -Path (Join-Path $winApps 'pcd.cmd') -Value "@echo off`r`nwsl -d Ubuntu -u $LinuxUser -- bash -lc `"cd ~/PowerCodeDeck && ./pcd`"" -Encoding ASCII

    if ($made.Count -gt 0) {
        Say "Created a 'PowerCodeDeck' folder on your Desktop (Run / Workspace / VS Code):" Green
        foreach ($m in $made) { Write-Host "    $m" -ForegroundColor Gray }
    } else {
        Say "No Desktop folder found. Start with the 'pcd' command instead." Yellow
    }
} catch {
    Write-Host ""
    Say "Could not create desktop shortcuts:" Red
    Say "  $($_.Exception.Message)" Yellow
    Say "You can still start PowerCodeDeck by typing:  pcd" Gray
}

# -- 7. Done --
Write-Host ""
Write-Host "  ================================================" -ForegroundColor Green
Say "Done! PowerCodeDeck is installed (Linux user: $LinuxUser)." Green
Write-Host "  ================================================" -ForegroundColor Green
Write-Host ""
Say "Open the 'PowerCodeDeck' folder on your Desktop - it holds 3 shortcuts:" White
Write-Host "    [Run]        start + open the web UI" -ForegroundColor Cyan
Write-Host "    [Workspace]  open the projects folder in Explorer" -ForegroundColor Cyan
Write-Host "    [VS Code]    open projects in VS Code (Remote WSL)" -ForegroundColor Cyan
Write-Host ""
Say "Projects live inside WSL (fast + reliable file watching):" White
Write-Host "    $projUnc" -ForegroundColor Cyan
Write-Host ""
