#!/bin/bash
set -e

# ================================================
#  PowerCodeDeck - One-Click Installer
# ================================================

INSTALL_DIR="$HOME/.powercodedeck"
BIN_NAME="pcd"

echo ""
echo "  ================================================"
echo "     PowerCodeDeck Installer"
echo "  ================================================"
echo ""

# ── 1. Check OS ──
OS=$(uname -s)
ARCH=$(uname -m)
echo "  System: $OS $ARCH"

if [ "$OS" != "Darwin" ] && [ "$OS" != "Linux" ]; then
    echo "  ❌ Unsupported OS: $OS"
    echo "  Windows users: run install.ps1 instead"
    exit 1
fi

# Detect WSL
IS_WSL=false
if grep -qi microsoft /proc/version 2>/dev/null; then
    IS_WSL=true
    echo "  Running inside WSL"
fi

# ── 2. Install Xcode Command Line Tools (macOS, required for CGO/SQLite) ──
if [ "$OS" = "Darwin" ]; then
    if ! xcode-select -p &>/dev/null; then
        echo "  Installing Xcode Command Line Tools (required for SQLite build)..."
        xcode-select --install
        echo "  ⚠ After CLT installation completes, re-run ./install.sh"
        exit 0
    else
        echo "  ✓ Xcode CLT found"
    fi
fi

# ── 2b. Install Homebrew (macOS only, if missing) ──
if [ "$OS" = "Darwin" ]; then
    if ! command -v brew &>/dev/null; then
        echo "  Installing Homebrew..."
        /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

        # Add to PATH for Apple Silicon
        if [ -f /opt/homebrew/bin/brew ]; then
            eval "$(/opt/homebrew/bin/brew shellenv)"
        fi
        echo "  ✓ Homebrew installed"
    else
        echo "  ✓ Homebrew found"
    fi
fi

# ── 3. Install base dependencies (Linux) ──
# git + curl + ca-certificates are all this installer needs. PowerCodeDeck now
# builds with pure-Go SQLite (modernc) and go-pty, so there is NO cgo / C
# compiler requirement, and tmux is not used.
if [ "$OS" = "Linux" ] && command -v apt-get &>/dev/null; then
    echo "  Installing base dependencies (git, curl, ca-certificates)..."
    sudo apt-get update -qq
    sudo apt-get install -y -qq git curl ca-certificates
    echo "  ✓ Base dependencies installed"
fi

# ── 4. Install Go (for building) ──
if ! command -v go &>/dev/null; then
    echo "  Installing Go..."
    if [ "$OS" = "Darwin" ]; then
        brew install go
    else
        sudo apt-get install -y -qq golang
    fi
    echo "  ✓ Go installed"
else
    echo "  ✓ Go found ($(go version | awk '{print $3}'))"
fi

# ── 5a. Install Node.js (if missing, needed for pnpm/frontend) ──
if ! command -v node &>/dev/null; then
    echo "  Installing Node.js..."
    if [ "$OS" = "Darwin" ]; then
        brew install node
    else
        curl -fsSL https://deb.nodesource.com/setup_lts.x | sudo -E bash - 2>/dev/null
        sudo apt-get install -y -qq nodejs 2>/dev/null || {
            # Fallback: use nvm
            curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.0/install.sh | bash
            export NVM_DIR="$HOME/.nvm"
            [ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"
            nvm install --lts
        }
    fi
    echo "  ✓ Node.js installed ($(node -v))"
else
    echo "  ✓ Node.js found ($(node -v))"
fi

# ── 5b. Install pnpm (for building frontend) ──
if ! command -v pnpm &>/dev/null; then
    echo "  Installing pnpm..."
    npm install -g pnpm 2>/dev/null || brew install pnpm 2>/dev/null || {
        curl -fsSL https://get.pnpm.io/install.sh | sh -
    }
    echo "  ✓ pnpm installed"
else
    echo "  ✓ pnpm found"
fi

# ── 6. Build PowerCodeDeck ──
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo ""
echo "  Building PowerCodeDeck..."

# Build the frontend with npm from a clean node_modules. We deliberately avoid
# pnpm here: pnpm 10 blocks dependency build scripts by default
# (ERR_PNPM_IGNORED_BUILDS), so esbuild's native binary never gets set up and
# vite build fails. npm runs those build scripts normally. Starting clean also
# avoids npm choking on a pnpm-created node_modules.
cd "$SCRIPT_DIR/client"
echo "  Installing client dependencies + building (npm)..."
rm -rf node_modules
npm install --no-audit --no-fund
npm run build

cd "$SCRIPT_DIR"
if [ ! -d client/dist ]; then
    echo "  ❌ Client build failed — client/dist was not produced. See errors above."
    exit 1
fi

rm -rf server/static
cp -r client/dist server/static
cd "$SCRIPT_DIR/server" && CGO_ENABLED=0 go build -o "../$BIN_NAME" .
cd "$SCRIPT_DIR"

echo "  ✓ Build complete"

# ── 7. Install to ~/.powercodedeck ──
echo ""
echo "  Installing to $INSTALL_DIR ..."

mkdir -p "$INSTALL_DIR"
cp "$BIN_NAME" "$INSTALL_DIR/$BIN_NAME"
chmod +x "$INSTALL_DIR/$BIN_NAME"

# NOTE: We deliberately do NOT copy the repo's local .env into the install.
# A developer .env may carry stale/legacy settings (e.g. a leftover
# AGENTDECK_PIN) that would silently enable authentication and override the
# no-auth default. On first run the app generates a clean config and lets the
# user choose none/pin/password. To preserve an existing install's config,
# leave the .env already in "$INSTALL_DIR" untouched.
if [ -f "$INSTALL_DIR/.env" ]; then
    echo "  ✓ Existing config kept ($INSTALL_DIR/.env)"
else
    echo "  ○ No auth by default — config will be created on first run"
fi

echo "  ✓ Installed"

# ── 8. Create launcher ──
if [ "$OS" = "Darwin" ]; then
    # Create macOS .command file (double-clickable)
    LAUNCHER="$HOME/Desktop/PowerCodeDeck.command"
    cat > "$LAUNCHER" << 'LAUNCHER_EOF'
#!/bin/bash
cd "$HOME/.powercodedeck"
./pcd
LAUNCHER_EOF
    chmod +x "$LAUNCHER"
    echo "  ✓ Desktop shortcut created: PowerCodeDeck.command"

    # Also create a .app bundle for cleaner experience
    APP_DIR="$HOME/Applications/PowerCodeDeck.app/Contents/MacOS"
    mkdir -p "$APP_DIR"
    cat > "$APP_DIR/PowerCodeDeck" << 'APP_EOF'
#!/bin/bash
cd "$HOME/.powercodedeck"
exec ./pcd
APP_EOF
    chmod +x "$APP_DIR/PowerCodeDeck"

    # Info.plist
    cat > "$HOME/Applications/PowerCodeDeck.app/Contents/Info.plist" << 'PLIST_EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>PowerCodeDeck</string>
    <key>CFBundleIdentifier</key>
    <string>com.powercodedeck.app</string>
    <key>CFBundleName</key>
    <string>PowerCodeDeck</string>
    <key>CFBundleVersion</key>
    <string>0.2.0</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
</dict>
</plist>
PLIST_EOF
    echo "  ✓ App created: ~/Applications/PowerCodeDeck.app"
fi

if [ "$OS" = "Linux" ]; then
    # Create .desktop file
    DESKTOP_FILE="$HOME/.local/share/applications/powercodedeck.desktop"
    mkdir -p "$(dirname "$DESKTOP_FILE")"
    cat > "$DESKTOP_FILE" << DESKTOP_EOF
[Desktop Entry]
Name=PowerCodeDeck
Exec=$INSTALL_DIR/$BIN_NAME
Terminal=true
Type=Application
Categories=Development;
DESKTOP_EOF
    echo "  ✓ Desktop entry created"
fi

# ── 9. Check for AI CLI tools ──
echo ""
echo "  ── AI Tools ──"
if command -v claude &>/dev/null; then
    echo "  ✓ Claude Code found"
else
    echo "  ○ Claude Code not found (install: npm install -g @anthropic-ai/claude-code)"
fi
if command -v gemini &>/dev/null; then
    echo "  ✓ Gemini CLI found"
else
    echo "  ○ Gemini CLI not found"
fi
if command -v codex &>/dev/null; then
    echo "  ✓ Codex CLI found"
else
    echo "  ○ Codex CLI not found"
fi

# ── Done ──
echo ""
echo "  ================================================"
echo "     Installation complete!"
echo "  ================================================"
echo ""
echo "  How to start:"
echo ""
if [ "$OS" = "Darwin" ]; then
    echo "    Option 1: Double-click 'PowerCodeDeck.command' on Desktop"
    echo "    Option 2: Open 'PowerCodeDeck' from ~/Applications"
    echo "    Option 3: Run '$INSTALL_DIR/$BIN_NAME'"
else
    echo "    Run: $INSTALL_DIR/$BIN_NAME"
fi
echo ""
echo "  On first run you can choose:"
echo "    - no authentication (default)"
echo "    - PIN authentication"
echo "    - password authentication"
echo ""
echo "  If exposing through the internet, protect it with"
echo "  Caddy + Authelia, Tailscale, VPN, or SSH tunnel."
echo "  The browser will open automatically."
echo ""
echo "  ================================================"
echo ""

# Ask to launch now — only when interactive. In a non-interactive/piped install
# (e.g. the WSL installer runs this with stdin </dev/null) `read` gets EOF and
# returns non-zero, which under `set -e` would abort with a false "failed"; skip
# the prompt entirely in that case so the install reports success.
if [ -t 0 ]; then
    read -p "  Launch PowerCodeDeck now? [Y/n] " -n 1 -r REPLY || true
    echo ""
    if [[ ! $REPLY =~ ^[Nn]$ ]]; then
        cd "$INSTALL_DIR"
        exec ./"$BIN_NAME"
    fi
fi
