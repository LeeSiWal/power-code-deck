# ================================================================
#  PowerCodeDeck - Native Windows installer (BUILD FROM SOURCE)
#
#  Usage (PowerShell):
#    iwr -useb https://raw.githubusercontent.com/LeeSiWal/power-code-deck/main/win-native-build.ps1 | iex
#
#  Installs Git + Go + Node (via winget) if missing, clones the repo, and
#  builds a native pcd.exe ON YOUR MACHINE. Because the exe is locally built
#  (not downloaded), Windows SmartScreen "unrecognized app" blocks generally
#  don't apply. No WSL, no cgo.
#
#  A winget install may pop a UAC prompt - click Yes. If Go/Node were just
#  installed and the build can't find them, close PowerShell, reopen it, and
#  run this one-liner again (it will skip what's already installed).
# ================================================================

$ErrorActionPreference = 'Stop'

try {
    cmd /c "chcp 65001 >nul" 2>$null | Out-Null
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
} catch {}
try { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 } catch {}

function Say($msg, $color = 'White') { Write-Host "  $msg" -ForegroundColor $color }

# Reload PATH from the registry so tools installed this session are visible.
function Update-SessionPath {
    $m = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    $u = [Environment]::GetEnvironmentVariable('Path', 'User')
    $env:Path = ($m, $u -join ';')
}

function Have($cmd) { return [bool](Get-Command $cmd -ErrorAction SilentlyContinue) }

function Ensure-Tool($cmd, $wingetId, $name) {
    if (Have $cmd) { Say "OK - $name found" Green; return }
    Say "Installing $name ..." Yellow
    winget install -e --id $wingetId --accept-source-agreements --accept-package-agreements --silent
    Update-SessionPath
}

Write-Host ""
Write-Host "  ================================================" -ForegroundColor Cyan
Write-Host "     PowerCodeDeck  Native Build Installer" -ForegroundColor Cyan
Write-Host "  ================================================" -ForegroundColor Cyan
Write-Host ""

# 0. winget available?
if (-not (Have winget)) {
    Say "This installer needs 'winget' (App Installer)." Red
    Say "Install 'App Installer' from the Microsoft Store, then run this again." Gray
    return
}

# 1. Toolchain
Ensure-Tool git  Git.Git           "Git"
Ensure-Tool go   GoLang.Go         "Go"
Ensure-Tool node OpenJS.NodeJS.LTS "Node.js"
Update-SessionPath

$missing = @()
foreach ($t in 'git', 'go', 'node', 'npm') { if (-not (Have $t)) { $missing += $t } }
if ($missing.Count -gt 0) {
    Write-Host ""
    Say "Installed the tools, but this session can't see: $($missing -join ', ')" Yellow
    Say "Close PowerShell, open it again, and re-run the same one-liner." Yellow
    Say "(It will skip what's already installed and continue to the build.)" Gray
    return
}

# 2. Get the source
$src = Join-Path $env:USERPROFILE 'power-code-deck'
if (Test-Path (Join-Path $src '.git')) {
    Say "Updating existing source at $src ..." Yellow
    git -C $src pull --ff-only
} else {
    Say "Cloning source to $src ..." Yellow
    git clone https://github.com/LeeSiWal/power-code-deck.git $src
}

# 3. Build the frontend
Say "Building the web UI (npm install + build - a few minutes)..." Yellow
Push-Location (Join-Path $src 'client')
try {
    npm install --no-audit --no-fund
    npm run build
} finally { Pop-Location }

# 4. Embed the frontend into the server
$static = Join-Path $src 'server\static'
if (Test-Path $static) { Remove-Item -Recurse -Force $static }
Copy-Item -Recurse (Join-Path $src 'client\dist') $static

# 5. Build the native binary (no cgo)
Say "Building pcd.exe (native, no cgo)..." Yellow
$exeSrc = Join-Path $src 'pcd.exe'
Push-Location (Join-Path $src 'server')
try {
    $env:CGO_ENABLED = '0'
    go build -o $exeSrc .
} finally { Pop-Location }
if (-not (Test-Path $exeSrc)) { Say "Build failed - pcd.exe was not produced." Red; return }

# 6. Install to %USERPROFILE%\PowerCodeDeck
$dir = Join-Path $env:USERPROFILE 'PowerCodeDeck'
$legacy = Join-Path $env:USERPROFILE '.powercodedeck'
if ((Test-Path $legacy) -and -not (Test-Path $dir)) {
    try { Move-Item $legacy $dir; Say "Moved data: .powercodedeck -> PowerCodeDeck" Gray } catch {}
}
New-Item -ItemType Directory -Force -Path $dir | Out-Null
Get-Process pcd -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 500
$exe = Join-Path $dir 'pcd.exe'
Copy-Item -Force $exeSrc $exe

# Desktop shortcut (best-effort)
try {
    $ws = New-Object -ComObject WScript.Shell
    $lnk = $ws.CreateShortcut((Join-Path ([Environment]::GetFolderPath('Desktop')) 'PowerCodeDeck.lnk'))
    $lnk.TargetPath = $exe
    $lnk.WorkingDirectory = $dir
    $lnk.Save()
} catch {}

# 7. Run
Write-Host ""
Say "OK - built and installed to $exe" Green
Say "Starting PowerCodeDeck..." Yellow
try {
    Start-Process -FilePath $exe -WorkingDirectory $dir
    Write-Host ""
    Write-Host "  ================================================" -ForegroundColor Green
    Say "Done! Open in your browser:  http://localhost:33033" Green
    Write-Host "  ================================================" -ForegroundColor Green
    Write-Host ""
    Say "Next time, double-click the 'PowerCodeDeck' desktop shortcut." Gray
    Say "To update later, run this one-liner again (rebuilds from latest source)." Gray
    Write-Host ""
} catch {
    Write-Host ""
    Say "Windows blocked the app from starting:" Red
    Say "  $($_.Exception.Message)" Gray
    Write-Host ""
    Say "This is 'Smart App Control', which blocks ALL unsigned apps -" Yellow
    Say "even ones built locally. It has no 'run anyway' option. Two choices:" Yellow
    Write-Host ""
    Say "  1) Turn Smart App Control OFF (then pcd.exe runs):" White
    Say "     Settings > Privacy & security > Windows Security >" Gray
    Say "     App & browser control > Smart App Control > Off." Gray
    Say "     (Note: once Off, re-enabling needs a Windows reset.)" Gray
    Say "     Then run:  $exe" Cyan
    Write-Host ""
    Say "  2) Use the WSL install instead (not affected by Smart App Control):" White
    Say "     iwr -useb https://raw.githubusercontent.com/LeeSiWal/power-code-deck/main/win-install.ps1 | iex" Cyan
    Write-Host ""
    Say "Your built binary is here if you want it:  $exe" Gray
    Write-Host ""
}
