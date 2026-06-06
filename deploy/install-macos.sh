#!/usr/bin/env bash
# One-shot macOS deploy for the gomoku server.
#
# Builds the frontend + Go binary, installs them, and loads the launchd
# services (server + daily backup). Re-runnable: running it again rebuilds and
# reloads in place.
#
# Run as your normal user from the repo root (NOT with sudo — the script calls
# sudo itself only for the steps that need root):
#   ./deploy/install-macos.sh
#
# Runtime config (port, paths, env) is read straight from
# deploy/com.gomoku.server.plist, so that file is the single source of truth.
# After it finishes, expose the service with a Cloudflare tunnel (web-managed)
# pointing at localhost:<printed port>.
set -euo pipefail

if [ "$(id -u)" -eq 0 ]; then
  echo "请用普通用户运行（不要加 sudo）；需要 root 时脚本会自动提权。" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

SERVER_PLIST_SRC="$SCRIPT_DIR/com.gomoku.server.plist"
BACKUP_PLIST_SRC="$SCRIPT_DIR/com.gomoku.backup.plist"
SERVER_LABEL="com.gomoku.server"
BACKUP_LABEL="com.gomoku.backup"
LAUNCH_DAEMONS="/Library/LaunchDaemons"
RUN_USER="$(id -un)"
SUDO="sudo"

plist_get() { /usr/bin/plutil -extract "$2" raw -o - "$1"; } # file, keypath

# --- read config from the plists (single source of truth) ---
BIN_DEST="$(plist_get "$SERVER_PLIST_SRC" 'ProgramArguments.0')"
ADDR="$(plist_get "$SERVER_PLIST_SRC" 'EnvironmentVariables.ADDR')"
DB_PATH="$(plist_get "$SERVER_PLIST_SRC" 'EnvironmentVariables.DB_PATH')"
STATIC_DIR="$(plist_get "$SERVER_PLIST_SRC" 'EnvironmentVariables.STATIC_DIR')"
LOG_PATH="$(plist_get "$SERVER_PLIST_SRC" 'StandardOutPath')"
BACKUP_SH_DEST="$(plist_get "$BACKUP_PLIST_SRC" 'ProgramArguments.0')"
BACKUP_DIR="$(plist_get "$BACKUP_PLIST_SRC" 'EnvironmentVariables.BACKUP_DIR')"
PORT="${ADDR##*:}"
HEALTH_URL="http://127.0.0.1:${PORT}/api/health"

echo "==> repo:   $REPO_ROOT"
echo "==> user:   $RUN_USER"
echo "==> addr:   $ADDR  (health: $HEALTH_URL)"
echo "==> binary: $BIN_DEST"
echo "==> static: $STATIC_DIR"
echo "==> db:     $DB_PATH"

# --- prerequisites ---
for tool in go npm; do
  command -v "$tool" >/dev/null 2>&1 || { echo "error: '$tool' not found in PATH (try: brew install go node)" >&2; exit 1; }
done

# --- build (as the current user, normal PATH) ---
echo "==> building frontend"
( cd frontend && npm ci && npm run build )
echo "==> building server binary"
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$REPO_ROOT/gomoku" .

# --- install files (root) ---
echo "==> installing files (sudo)"
$SUDO mkdir -p "$(dirname "$BIN_DEST")" "$(dirname "$DB_PATH")" "$(dirname "$LOG_PATH")" "$BACKUP_DIR" "$(dirname "$STATIC_DIR")"
$SUDO install -m 0755 "$REPO_ROOT/gomoku" "$BIN_DEST"
$SUDO install -m 0755 "$SCRIPT_DIR/backup.sh" "$BACKUP_SH_DEST"
$SUDO rm -rf "$STATIC_DIR"
$SUDO cp -R "$REPO_ROOT/frontend/dist" "$STATIC_DIR"
# The daemon runs as $RUN_USER, so it must own the data and log directories.
$SUDO chown -R "$RUN_USER" "$(dirname "$DB_PATH")" "$(dirname "$LOG_PATH")" "$BACKUP_DIR"

# --- install + (re)load launchd services ---
install_plist() { # src, label
  local src="$1" label="$2" dest="$LAUNCH_DAEMONS/$2.plist"
  sed "s/__MACOS_USER__/$RUN_USER/g" "$src" | $SUDO tee "$dest" >/dev/null
  $SUDO chown root:wheel "$dest"
  $SUDO chmod 0644 "$dest"
  $SUDO launchctl bootout "system/$label" 2>/dev/null || true
  $SUDO launchctl bootstrap system "$dest"
  echo "==> loaded $label"
}
echo "==> installing launchd services (sudo)"
install_plist "$SERVER_PLIST_SRC" "$SERVER_LABEL"
install_plist "$BACKUP_PLIST_SRC" "$BACKUP_LABEL"

# --- health check ---
echo "==> waiting for $HEALTH_URL"
if curl -fsS --retry 20 --retry-delay 1 --retry-connrefused -m 2 "$HEALTH_URL" >/dev/null; then
  echo "==> OK: server is healthy on $ADDR"
else
  echo "error: health check failed; check logs at $LOG_PATH" >&2
  exit 1
fi

cat <<EOF

Deploy complete. gomoku is running on $ADDR (launchd: $SERVER_LABEL).

Next — expose it with a Cloudflare tunnel (web-managed):
  1) Zero Trust dashboard -> Networks -> Tunnels -> Create a tunnel -> Cloudflared
  2) Run the install command it shows, e.g.:  sudo cloudflared service install <TOKEN>
  3) Add a Public Hostname:  your.domain  ->  HTTP  ->  localhost:$PORT
  4) Optional: enable Always Use HTTPS + HSTS; keep the Mac awake: sudo pmset -a sleep 0 disablesleep 1

Manage the service:
  sudo launchctl kickstart -k system/$SERVER_LABEL   # restart
  sudo launchctl bootout    system/$SERVER_LABEL     # stop (graceful SIGTERM)
  tail -f $LOG_PATH                                   # logs
EOF
