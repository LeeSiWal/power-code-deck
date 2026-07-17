#!/usr/bin/env bash
# One-command build + redeploy for PowerCodeDeck.
#
# RUN THIS FROM YOUR OWN TERMINAL — never from inside a Claude session on the deck.
# pcd runs as a systemd service and the in-deck Claude processes live INSIDE
# pcd.service's cgroup, so `systemctl restart pcd` kills them (and itself) mid-run.
# Your login shell is outside that cgroup, so the restart is safe there.
#
# systemd (Restart=unless-stopped) already keeps pcd alive across crashes/reboots;
# this script just builds the new binary, swaps it in, and asks systemd to restart.
#
# Usage:  ./deploy.sh
# Env:    PCD_REPO         repo checkout   (default: this script's dir)
#         PCD_INSTALL_DIR  where pcd lives (default: ~/PowerCodeDeck)
#         PCD_SKIP_PULL=1  skip `git pull`
#         PCD_SKIP_NPM=1   skip `npm install` (use when deps are unchanged — faster)
set -euo pipefail

REPO="${PCD_REPO:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}"
INSTALL_DIR="${PCD_INSTALL_DIR:-$HOME/PowerCodeDeck}"

echo "[1/6] latest main"
if [ "${PCD_SKIP_PULL:-0}" != "1" ]; then
  git -C "$REPO" pull --ff-only 2>/dev/null || echo "  (pull skipped)"
fi
git -C "$REPO" log --oneline -1

echo "[2/6] client deps"
cd "$REPO/client"
if [ "${PCD_SKIP_NPM:-0}" != "1" ]; then
  npm install --no-audit --no-fund
else
  echo "  (npm install skipped)"
fi

echo "[3/6] client build (tsc typecheck + vite)"
./node_modules/.bin/tsc --noEmit
./node_modules/.bin/vite build

echo "[4/6] embed static + go build"
cd "$REPO"
rm -rf server/static
cp -r client/dist server/static
cd "$REPO/server"
CGO_ENABLED=0 go build -o "$REPO/pcd" .

echo "[5/6] backup + swap binary"
cp -f "$INSTALL_DIR/pcd" "$INSTALL_DIR/pcd.bak.$(date +%Y%m%d-%H%M%S)"
cp -f "$REPO/pcd" "$INSTALL_DIR/pcd"
# keep only the 5 most recent backups
ls -1t "$INSTALL_DIR"/pcd.bak.* 2>/dev/null | tail -n +6 | xargs -r rm -f

echo "[6/6] trigger redeploy"
TRIGGER="$INSTALL_DIR/.redeploy"
if [ "${PCD_NO_TRIGGER:-0}" = "1" ]; then
  echo "  (trigger skipped — PCD_NO_TRIGGER; binary staged, run: date +%s%N > $TRIGGER)"
  exit 0
fi
if systemctl is-enabled pcd-redeploy.path >/dev/null 2>&1; then
  # Write (not just touch) so systemd's PathModified fires reliably. The out-of-cgroup
  # pcd-redeploy.service does the privileged `systemctl restart pcd` — no sudo here.
  # NOTE: if this script is run from INSIDE the deck, the restart kills this very
  # process, so the verify below may not print. The deploy still completes.
  date +%s%N > "$TRIGGER"
  echo "  triggered systemd auto-restart (out-of-cgroup)."
else
  echo "  ⚠ pcd-redeploy.path not installed yet."
  echo "    one-time setup:  sudo $REPO/install-autodeploy.sh"
  echo "    or restart now:  sudo systemctl restart pcd"
  exit 0
fi

# Best-effort verify (skipped if the restart already killed us).
sleep 2
echo -n "  live asset: "
curl -s http://127.0.0.1:33033/ | grep -oE 'assets/index-[A-Za-z0-9_-]+\.js' | head -1 || true
echo "완료 ✅  https://pcd.19921005.xyz"
