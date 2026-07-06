# PowerCodeDeck on Windows

PowerCodeDeck is a terminal-oriented web console with a **Go backend**. On
Windows, the recommended local installation method is **WSL2**. Other paths
(Docker, native `.exe`) exist but are advanced or experimental.

| Option | Recommended for | Notes |
|--------|-----------------|-------|
| **WSL2** | Most Windows users | Recommended local install path |
| Remote / home server | Users with a server / NAS / VPS | Best for multi-device access (mobile/iPad handoff) |
| Docker | Advanced users | Requires Docker Desktop, and usually virtualization |
| Native `.exe` | Experimental | Requires a signed build or Smart App Control disabled |
| Full Node backend port | Not planned | The Go backend is maintained |

---

## Recommended: WSL2

WSL2 gives PowerCodeDeck a Linux environment where shell sessions, Git, Node,
pnpm, the Claude CLI, and project files all work naturally. The Windows
installer sets this up for you:

```powershell
# Administrator PowerShell
iwr -useb https://raw.githubusercontent.com/LeeSiWal/power-code-deck/main/win-install.ps1 | iex
```

This installer uses **WSL2**. It does **not** run the unsigned native `pcd.exe`
directly. When WSL was just enabled it will ask to reboot and then resume
automatically after you log back in. When it prints `Done!`, run:

```powershell
wsl -d Ubuntu -u root /root/PowerCodeDeck/pcd    # or just: pcd (shortcut created by the installer)
```

Then open **http://localhost:33033**.

### Virtualization requirement

WSL2 requires **virtualization to be enabled in BIOS/UEFI**. This is the most
common reason WSL2 installation fails for beginners. If installation fails with
a virtualization-related error, enable virtualization first:

- **Intel CPU:** enable **Intel VT-x** / **Virtualization Technology**
- **AMD CPU:** enable **SVM Mode** / **AMD-V**

After changing BIOS/UEFI settings, **reboot Windows** and run the installer
again. You can check the current state in **Task Manager → Performance → CPU →
"Virtualization"**.

> The installer will automatically fall back to **WSL1** (which needs no
> virtualization) when it detects that WSL2 can't start — but enabling
> virtualization and using WSL2 is the smoother, recommended path.

---

## Advanced: Docker

Docker is supported as an advanced installation option. It's useful for server
deployment or for users who already run Docker Desktop.

On Windows, Docker Desktop itself usually requires virtualization (WSL2 backend
or Hyper-V), so it doesn't avoid the virtualization requirement — **WSL2 remains
the recommended beginner path**. Use Docker only if you already rely on it.

---

## Experimental: native Windows `.exe`

PowerCodeDeck can build a **native Windows binary** (`pcd.exe`) with no cgo and
no WSL — the internal PTY engine uses `go-pty` (ConPTY on Windows) and a pure-Go
SQLite driver (`modernc.org/sqlite`). Two ways to get it:

**Build from source (locally):**
```powershell
iwr -useb https://raw.githubusercontent.com/LeeSiWal/power-code-deck/main/win-native-build.ps1 | iex
```
Installs Git/Go/Node via winget if missing, then builds `pcd.exe` on your machine.

**Prebuilt download:**
```powershell
iwr -useb https://raw.githubusercontent.com/LeeSiWal/power-code-deck/main/win-native-install.ps1 | iex
```
Downloads a prebuilt `pcd.exe`.

### Smart App Control blocks unsigned executables

Native `.exe` builds are **experimental**. On systems with **Smart App Control**
(SAC) enabled, Windows blocks unsigned executables — *even ones built locally* —
with `An Application Control policy has blocked this file`. SAC has no
"run anyway" option, and self-signing does not bypass it.

Check SAC state:

```powershell
Get-MpComputerStatus | Select-Object SmartAppControlState   # "On" means blocked
```

If SAC blocks the app you have two choices:

1. **Turn Smart App Control off** — Settings → Privacy & security → Windows
   Security → App & browser control → Smart App Control → **Off**. Then run
   `%USERPROFILE%\PowerCodeDeck\pcd.exe`.
   ⚠️ Once Off, re-enabling SAC requires a Windows reset.
2. **Use WSL2 instead** (not affected by Smart App Control). This is why WSL2 is
   the recommended path.

PowerCodeDeck does **not** recommend disabling Smart App Control for beginners.
Native Windows will become practical once **signed builds** are available.

---

## Why not a full Node backend?

PowerCodeDeck keeps the **Go backend**. A full Node backend port is **not
planned** because:

- The Go backend already provides the REST API, WebSocket hub, `SessionEngine`,
  scrollback ring buffer, auth, and deployment logic.
- Go keeps the server lightweight and easy to deploy on Linux / macOS / home
  servers as a single binary.
- Rewriting the whole backend in Node would duplicate existing, working code.
- Terminal/PTY support in Node still needs native modules (e.g. `node-pty`),
  which reintroduces the native-dependency problem it was meant to avoid.
- Windows support is better solved today by **WSL2**, and in the future by
  **signed native builds** — not by a backend rewrite.

---

## Summary

> PowerCodeDeck keeps the Go backend.
>
> For Windows users, **WSL2 is the recommended local installation path**. If
> WSL2 fails, **enable virtualization in BIOS/UEFI** and try again.
>
> **Docker** is available for advanced users. Native Windows `.exe` builds are
> **experimental** until signed releases are available. A **full Node backend
> port is not planned**.
