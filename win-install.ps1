# ================================================================
#  PowerCodeDeck — Windows one-line installer
#
#  Usage (run in an **Administrator** PowerShell):
#    iwr -useb https://raw.githubusercontent.com/LeeSiWal/power-code-deck/main/win-install.ps1 | iex
#
#  Sets up WSL + Ubuntu (no interactive account — runs as root) and
#  builds PowerCodeDeck inside it. If WSL was just enabled it offers to
#  reboot and then resumes automatically after you log back in.
# ================================================================

$ErrorActionPreference = 'Stop'
$RepoRaw = 'https://raw.githubusercontent.com/LeeSiWal/power-code-deck/main/win-install.ps1'

function Say($msg, $color = 'White') { Write-Host "  $msg" -ForegroundColor $color }

function Test-Admin {
    return ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
        ).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)
}

# Schedule this installer to run once more after the next logon, so the
# user only has to reboot — the setup picks up where it left off.
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
        Say "재부팅하면 설치가 '자동으로 이어집니다'. (다시 붙여넣지 않아도 됩니다)" Green
    } else {
        Say "재부팅 후 이 명령을 한 번 더 붙여넣어 주세요." Yellow
    }
    $ans = Read-Host "  지금 다시 시작(재부팅)할까요? [Y/n]"
    if ($ans -match '^(n|no|N|No)$') {
        Write-Host ""
        Say "알겠습니다. 준비되면 직접 재부팅해 주세요." Yellow
        if ($resumed) { Say "로그인하면 설치가 자동으로 이어집니다." Gray }
        else { Say "재부팅 후 같은 명령을 다시 실행하면 됩니다." Gray }
    } else {
        Write-Host ""
        Say "10초 후 재부팅합니다... (취소: Ctrl+C)" Yellow
        Start-Sleep -Seconds 10
        Restart-Computer -Force
    }
}

Write-Host ""
Write-Host "  ================================================" -ForegroundColor Cyan
Write-Host "     PowerCodeDeck  Windows Installer" -ForegroundColor Cyan
Write-Host "  ================================================" -ForegroundColor Cyan
Write-Host ""

# ── 1. Is WSL itself available? ──
$wslReady = $false
try {
    wsl --status *> $null
    if ($LASTEXITCODE -eq 0) { $wslReady = $true }
} catch { $wslReady = $false }

if (-not $wslReady) {
    # Enabling the WSL feature needs admin (only this step does).
    if (-not (Test-Admin)) {
        Say "WSL 설치에는 관리자 권한이 필요합니다." Yellow
        Say "시작 메뉴 > PowerShell > 마우스 오른쪽 > '관리자 권한으로 실행' 후" Gray
        Say "이 명령을 다시 붙여넣어 주세요." Gray
        return
    }
    Say "WSL(Windows Subsystem for Linux)을 설치합니다..." Yellow
    try { wsl --install --no-launch } catch { wsl --install }
    Say "✓ WSL 설치를 시작했습니다. 재부팅이 필요합니다." Green
    Offer-Reboot
    return
}

# ── 2. Ensure the Ubuntu distro exists (no interactive account) ──
function Get-Distros {
    # `wsl -l -q` prints UTF-16 with null bytes; strip them for matching.
    return (((wsl -l -q) 2>$null) -join "`n") -replace "`0", ""
}

if ((Get-Distros) -notmatch 'Ubuntu') {
    Say "Ubuntu 를 설치합니다... (몇 분 걸릴 수 있습니다)" Yellow
    try { wsl --install --no-launch -d Ubuntu } catch { wsl --install -d Ubuntu }
    Start-Sleep -Seconds 3
}

if ((Get-Distros) -notmatch 'Ubuntu') {
    Say "Ubuntu 설치를 마치려면 재부팅이 필요할 수 있습니다." Yellow
    Offer-Reboot
    return
}

Say "✓ WSL / Ubuntu 준비됨" Green

# ── 3. Build & install PowerCodeDeck inside Ubuntu (as root) ──
Write-Host ""
Say "PowerCodeDeck 를 Ubuntu 안에서 빌드/설치합니다..." Yellow
Say "(tmux · Go · Node.js · pnpm 설치 + 빌드 — 몇 분 걸립니다)" Gray
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
    Say "설치 중 문제가 발생했습니다. 위의 오류 메시지를 확인해 주세요." Red
    return
}

# ── 4. Done ──
Write-Host ""
Write-Host "  ================================================" -ForegroundColor Green
Say "설치 완료! 🎉" Green
Write-Host "  ================================================" -ForegroundColor Green
Write-Host ""
Say "지금 실행하려면 아래 한 줄을 붙여넣으세요:" White
Write-Host '    wsl -d Ubuntu -u root -- bash -lc "cd ~/.powercodedeck && ./pcd"' -ForegroundColor Cyan
Write-Host ""
Say "그다음 브라우저에서:  http://localhost:33033" White
Write-Host ""
