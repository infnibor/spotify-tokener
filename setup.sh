#!/usr/bin/env bash
set -euo pipefail

# ——————————————————————————————————————————————————————————————————
# CONFIG
# ——————————————————————————————————————————————————————————————————

NAME="spotify-tokener"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NODE_ENV="production"

# Detect the real user who invoked sudo (or fallback)
INSTALLER_USER="${SUDO_USER:-${USER}}"
USER_HOME="$(eval echo "~${INSTALLER_USER}")"
BUN_INSTALL_DIR="$USER_HOME/.bun/bin"

SERVICE_FILE="/etc/systemd/system/$NAME.service"

# ——————————————————————————————————————————————————————————————————
# HELPERS
# ——————————————————————————————————————————————————————————————————

info()  { echo -e "🔧  $*"; }
error() { echo -e "❌  $*" >&2; exit 1; }

# ——————————————————————————————————————————————————————————————————
# 0) REQUIRE sudo
# ——————————————————————————————————————————————————————————————————

if [ "$EUID" -ne 0 ]; then
  error "Please run with sudo: sudo bash ./setup.sh"
fi

info "Setting up $NAME…"
info "Base directory: $DIR/src/app.ts"

# ——————————————————————————————————————————————————————————————————
# 1) SYSTEM DEPENDENCIES
# ——————————————————————————————————————————————————————————————————

info "Installing system dependencies for Playwright..."

# Update package list
apt-get update -qq

# ——————————————————————————————————————————————————————————————————
# 2) INSTALL BUN (if needed)
# ——————————————————————————————————————————————————————————————————

if ! command -v bun &>/dev/null; then
  info "Bun not found. Installing Bun for user '$INSTALLER_USER'…"
  su -l "$INSTALLER_USER" -c 'curl -fsSL https://bun.sh/install | bash'
fi

# Export the user's bun into root's PATH
if [ -d "$BUN_INSTALL_DIR" ]; then
  export PATH="$BUN_INSTALL_DIR:$PATH"
fi

# Double‑check
if ! command -v bun &>/dev/null; then
  error "Bun still not found after installation. Aborting."
fi

info "Found Bun: $(bun -v)"

# Symlink into /usr/local/bin for global access
if [ ! -L /usr/local/bin/bun ]; then
  info "Creating symlink /usr/local/bin/bun → $BUN_INSTALL_DIR/bun"
  ln -sf "$BUN_INSTALL_DIR/bun" /usr/local/bin/bun
fi

# ——————————————————————————————————————————————————————————————————
# 3) INSTALL PROJECT DEPENDENCIES
# ——————————————————————————————————————————————————————————————————

info "Installing dependencies with Bun…"
su -l "$INSTALLER_USER" -c "cd $DIR && bun install"

# ——————————————————————————————————————————————————————————————————
# 4) CREATE ENV FILE (if not exists)
# ——————————————————————————————————————————————————————————————————

ENV_FILE="$DIR/.env"
if [ ! -f "$ENV_FILE" ]; then
  info "Creating default .env file..."
  cat > "$ENV_FILE" <<EOF
# Server Configuration
PORT=8080
NODE_ENV=production

# Browser Configuration
HEADLESS=true
# BROWSER_PATH=/usr/bin/chromium-browser

# Set to false for debugging
# HEADLESS=false
EOF
  chown "$INSTALLER_USER:$INSTALLER_USER" "$ENV_FILE"
fi

# ——————————————————————————————————————————————————————————————————
# 5) CREATE systemd SERVICE
# ——————————————————————————————————————————————————————————————————

info "🧾 Creating systemd service..."

cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=$NAME Service
After=network.target
StartLimitIntervalSec=60
StartLimitBurst=3

[Service]
Type=simple
ExecStart=$BUN_INSTALL_DIR/bun --env-file=$DIR/.env $DIR/src/app.ts
WorkingDirectory=$DIR
Restart=always
RestartSec=10
User=$INSTALLER_USER
Environment=NODE_ENV=$NODE_ENV
Environment=DISPLAY=:99

# Security settings
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=$DIR $USER_HOME/.cache
AmbientCapabilities=CAP_SYS_ADMIN
NoNewPrivileges=false

# Resource limits
MemoryMax=1G
CPUQuota=200%

[Install]
WantedBy=multi-user.target
EOF

# ——————————————————————————————————————————————————————————————————
# 6) RELOAD & START SERVICE
# ——————————————————————————————————————————————————————————————————

info "🔄 Reloading systemd daemon..."
systemctl daemon-reload

info "✅ Enabling and starting $NAME service..."
systemctl enable "$NAME"
systemctl start  "$NAME"

# Wait a moment and check status
sleep 3
if systemctl is-active --quiet "$NAME"; then
  info "✅ Service is running successfully!"
else
  info "⚠️  Service may have issues. Check logs with: journalctl -u $NAME -f"
fi

info "✅ Setup complete! Management commands:"
echo "  • systemctl status $NAME"
echo "  • journalctl -u $NAME -f"
echo "  • systemctl stop $NAME"
echo "  • systemctl restart $NAME"
echo ""
echo "🌐 Service will be available at: http://localhost:8080/api/token"
