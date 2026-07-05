# ================================================================
#  PowerCodeDeck - LAN handoff setup for WSL (same Wi-Fi)
#
#  Usage (Administrator PowerShell):
#    iwr -useb https://raw.githubusercontent.com/LeeSiWal/power-code-deck/main/win-lan-handoff.ps1 | iex
#
#  Makes the QR "Continue on Mobile" reachable from a phone on the same Wi-Fi:
#   - detects the Windows LAN IP and the WSL IP,
#   - sets POWERCODEDECK_BIND_HOST=0.0.0.0 + POWERCODEDECK_LAN_URL in the WSL .env,
#   - forwards Windows :PORT -> WSL (netsh portproxy) and opens the firewall.
#
#  Admin is required (portproxy + firewall). Re-run after a Windows reboot, since
#  the WSL IP changes.
# ================================================================

$ErrorActionPreference = 'Stop'
$Port = 33033
$RuleName = "PowerCodeDeck $Port"

try {
    cmd /c "chcp 65001 >nul" 2>$null | Out-Null
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
    $OutputEncoding = New-Object System.Text.UTF8Encoding $false
} catch {}

function Say($msg, $color = 'White') { Write-Host "  $msg" -ForegroundColor $color }

Write-Host ""
Write-Host "  ================================================" -ForegroundColor Cyan
Write-Host "     PowerCodeDeck  LAN Handoff Setup (WSL)" -ForegroundColor Cyan
Write-Host "  ================================================" -ForegroundColor Cyan
Write-Host ""

# 0. Admin required for netsh portproxy + firewall.
$admin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
        ).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)
if (-not $admin) {
    Say "This needs Administrator (port forwarding + firewall)." Yellow
    Say "Open PowerShell as Administrator and run this again." Gray
    return
}

# 1. Windows LAN IP = the up adapter that has a default gateway (not WSL/loopback).
$lanIp = $null
try {
    $lanIp = (Get-NetIPConfiguration | Where-Object {
            $_.IPv4DefaultGateway -and $_.NetAdapter.Status -eq 'Up' -and
            $_.InterfaceAlias -notmatch 'WSL|Loopback|vEthernet'
        } | Select-Object -First 1).IPv4Address.IPAddress
} catch {}
if (-not $lanIp) {
    Say "Could not detect the Windows LAN IP automatically." Red
    Say "Run 'ipconfig', find your 'IPv4 Address' (e.g. 192.168.x.x)," Gray
    Say "then re-run with:  \$env:PCD_LAN_IP='192.168.x.x'; iwr ... | iex" Gray
    if ($env:PCD_LAN_IP) { $lanIp = $env:PCD_LAN_IP } else { return }
}
Say "Windows LAN IP : $lanIp" Green

# 2. WSL IP.
$wslIp = ((wsl hostname -I) -split '\s+' | Where-Object { $_ }) | Select-Object -First 1
if (-not $wslIp) { Say "Could not get the WSL IP (is Ubuntu installed?)." Red; return }
Say "WSL IP         : $wslIp" Green

$lanUrl = "http://${lanIp}:$Port"

# 3. Write BIND_HOST + LAN_URL into the WSL .env (base64 to avoid quoting issues).
Say "Configuring $lanUrl in WSL .env ..." Yellow
$bash = @"
mkdir -p ~/.powercodedeck
cd ~/.powercodedeck
touch .env
grep -v '^POWERCODEDECK_BIND_HOST=' .env 2>/dev/null | grep -v '^POWERCODEDECK_LAN_URL=' > .env.tmp || true
mv .env.tmp .env
printf 'POWERCODEDECK_BIND_HOST=0.0.0.0\n' >> .env
printf 'POWERCODEDECK_LAN_URL=$lanUrl\n' >> .env
"@
$b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes(($bash -replace "`r`n", "`n")))
wsl -d Ubuntu -u root -- bash -lc "echo $b64 | base64 -d | bash"

# 4. Port forward Windows :Port -> WSL, and open the firewall (idempotent).
Say "Forwarding Windows :$Port -> WSL and opening the firewall ..." Yellow
netsh interface portproxy delete v4tov4 listenport=$Port listenaddress=0.0.0.0 2>$null | Out-Null
netsh interface portproxy add v4tov4 listenport=$Port listenaddress=0.0.0.0 connectport=$Port connectaddress=$wslIp | Out-Null
netsh advfirewall firewall delete rule name="$RuleName" 2>$null | Out-Null
netsh advfirewall firewall add rule name="$RuleName" dir=in action=allow protocol=TCP localport=$Port | Out-Null

Write-Host ""
Write-Host "  ================================================" -ForegroundColor Green
Say "LAN handoff is set up." Green
Write-Host "  ================================================" -ForegroundColor Green
Write-Host ""
Say "Phones on the same Wi-Fi can now reach:  $lanUrl" White
Write-Host ""
Say "Next:" White
Say "  1) Restart PowerCodeDeck so the new .env applies:" Gray
Say "       wsl -d Ubuntu -u root -- bash -lc `"cd ~/.powercodedeck && ./pcd`"" Cyan
Say "  2) Open a session, click 'Continue on Mobile', pick the Local Wi-Fi URL," Gray
Say "     and scan the QR with your phone." Gray
Write-Host ""
Say "Note: the WSL IP changes on every Windows reboot - re-run this script after" Yellow
Say "a reboot if mobile access stops working." Gray
Write-Host ""
