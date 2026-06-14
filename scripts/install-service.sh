#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/install-service.sh [--binary PATH] [--config PATH] [--install-dir PATH]

Installs or updates codex-bridge as a user service on Linux(systemd) or macOS(launchd).
The script also registers the bridge provider and model catalog in Codex config.
USAGE
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd -P)"
CONFIG_PATH="$ROOT_DIR/config/config.toml"
INSTALL_DIR="${CODEX_BRIDGE_HOME:-$HOME/.codex-bridge}"
BINARY_PATH="${CODEX_BRIDGE_BINARY:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    --binary)
      BINARY_PATH="$2"
      shift 2
      ;;
    --config)
      CONFIG_PATH="$2"
      shift 2
      ;;
    --install-dir)
      INSTALL_DIR="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

abs_path() {
  local path="$1"
  local dir
  local base
  dir="$(dirname "$path")"
  base="$(basename "$path")"
  (cd "$dir" && printf '%s/%s\n' "$(pwd -P)" "$base")
}

select_binary() {
  local os
  local arch
  os="$(uname -s)"
  arch="$(uname -m)"
  case "$os:$arch" in
    Linux:x86_64) echo "$ROOT_DIR/dist/codex-bridge-linux-amd64" ;;
    Linux:aarch64|Linux:arm64) echo "$ROOT_DIR/dist/codex-bridge-linux-arm64" ;;
    Darwin:x86_64) echo "$ROOT_DIR/dist/codex-bridge-darwin-amd64" ;;
    Darwin:arm64) echo "$ROOT_DIR/dist/codex-bridge-darwin-arm64" ;;
    *) echo "" ;;
  esac
}

if [ -z "$BINARY_PATH" ]; then
  BINARY_PATH="$(select_binary)"
fi
if [ -z "$BINARY_PATH" ] || [ ! -f "$BINARY_PATH" ]; then
  echo "codex-bridge binary not found. Pass --binary PATH or build dist for this platform." >&2
  exit 1
fi

CONFIG_PATH="$(abs_path "$CONFIG_PATH")"
BINARY_PATH="$(abs_path "$BINARY_PATH")"
INSTALL_BIN="$INSTALL_DIR/bin/codex-bridge"
LOG_DIR="$INSTALL_DIR/logs"

mkdir -p "$(dirname "$INSTALL_BIN")" "$LOG_DIR"
install -m 0755 "$BINARY_PATH" "$INSTALL_BIN"

"$INSTALL_BIN" config check --config "$CONFIG_PATH"
if [ -n "${CODEX_HOME:-}" ]; then
  "$INSTALL_BIN" codex configure --config "$CONFIG_PATH" --codex-home "$CODEX_HOME"
else
  "$INSTALL_BIN" codex configure --config "$CONFIG_PATH"
fi

case "$(uname -s)" in
  Linux)
    SERVICE_DIR="$HOME/.config/systemd/user"
    SERVICE_FILE="$SERVICE_DIR/codex-bridge.service"
    mkdir -p "$SERVICE_DIR"
    cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Codex Bridge
After=network-online.target

[Service]
Type=simple
ExecStart=$INSTALL_BIN --config $CONFIG_PATH
Restart=always
RestartSec=2
WorkingDirectory=$(dirname "$CONFIG_PATH")

[Install]
WantedBy=default.target
EOF
    systemctl --user daemon-reload
    systemctl --user enable --now codex-bridge.service
    systemctl --user restart codex-bridge.service
    echo "installed systemd user service: $SERVICE_FILE"
    ;;
  Darwin)
    LABEL="com.codex-bridge"
    PLIST_DIR="$HOME/Library/LaunchAgents"
    PLIST_FILE="$PLIST_DIR/$LABEL.plist"
    mkdir -p "$PLIST_DIR"
    cat > "$PLIST_FILE" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>$INSTALL_BIN</string>
    <string>--config</string>
    <string>$CONFIG_PATH</string>
  </array>
  <key>WorkingDirectory</key>
  <string>$(dirname "$CONFIG_PATH")</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>$LOG_DIR/codex-bridge.out.log</string>
  <key>StandardErrorPath</key>
  <string>$LOG_DIR/codex-bridge.err.log</string>
</dict>
</plist>
EOF
    launchctl bootout "gui/$(id -u)" "$PLIST_FILE" >/dev/null 2>&1 || true
    launchctl bootstrap "gui/$(id -u)" "$PLIST_FILE"
    launchctl enable "gui/$(id -u)/$LABEL"
    launchctl kickstart -k "gui/$(id -u)/$LABEL"
    echo "installed launchd agent: $PLIST_FILE"
    ;;
  *)
    echo "unsupported OS: $(uname -s)" >&2
    exit 1
    ;;
esac
