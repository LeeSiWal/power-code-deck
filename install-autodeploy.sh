#!/usr/bin/env bash
# ONE-TIME setup (run once, with sudo) to enable chat-triggered auto-redeploy.
#
#   sudo /home/siwal/code/power-code-deck/install-autodeploy.sh
#
# It installs a systemd `path` unit that lives in system.slice — OUTSIDE pcd's
# cgroup — and restarts pcd whenever the trigger file is written. After this,
# deploy.sh (run by you OR by Claude inside the deck) only builds + swaps the
# binary and writes the trigger; systemd performs the privileged restart. No
# per-deploy terminal command and no sudo are needed ever again.
set -euo pipefail

[ "$(id -u)" -eq 0 ] || { echo "run with sudo:  sudo $0"; exit 1; }

DECK_USER=siwal
INSTALL_DIR=/home/$DECK_USER/PowerCodeDeck
TRIGGER="$INSTALL_DIR/.redeploy"

cat >/etc/systemd/system/pcd-redeploy.service <<'UNIT'
[Unit]
Description=PowerCodeDeck redeploy — restart pcd on trigger

[Service]
Type=oneshot
# Runs as root in system.slice (not pcd.service's cgroup), so restarting pcd
# does not kill the restarter itself.
ExecStart=/usr/bin/systemctl restart pcd
UNIT

cat >/etc/systemd/system/pcd-redeploy.path <<UNIT
[Unit]
Description=Watch PowerCodeDeck redeploy trigger

[Path]
# PathModified fires on write+close; deploy.sh writes a timestamp here.
PathModified=$TRIGGER
Unit=pcd-redeploy.service

[Install]
WantedBy=multi-user.target
UNIT

# Trigger file must be owned by the deck user so a non-sudo write can arm it.
install -o "$DECK_USER" -g "$DECK_USER" -m 644 /dev/null "$TRIGGER"

systemctl daemon-reload
systemctl enable --now pcd-redeploy.path

echo "✅ auto-redeploy armed."
echo "   From now on a deploy = build + swap binary + write $TRIGGER (deploy.sh does this)."
echo "   Test:  date +%s%N > $TRIGGER   # → pcd restarts within ~1s"
