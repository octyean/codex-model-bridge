#!/usr/bin/env bash
set -euo pipefail

REPO="${CODEX_BRIDGE_REPO:-octyean/codex-model-bridge}"
INSTALL_DIR="${CODEX_BRIDGE_HOME:-$HOME/.codex-bridge}"
CONFIG_PATH="${CODEX_BRIDGE_CONFIG:-$INSTALL_DIR/config.toml}"
BIN_DIR="$INSTALL_DIR/bin"
BIN_PATH="$BIN_DIR/codex-bridge"
LOG_DIR="$INSTALL_DIR/logs"
CODEX_DIR="${CODEX_HOME:-$HOME/.codex}"

BASE_URL="${CODEX_BRIDGE_BASE_URL:-https://api.deepseek.com}"
API_KEY="${CODEX_BRIDGE_API_KEY:-sk-xxx}"
MODEL="${CODEX_BRIDGE_MODEL:-deepseek-v4-flash}"
DISPLAY_NAME="${CODEX_BRIDGE_DISPLAY_NAME:-DeepSeek V4 Flash}"
CONTEXT_WINDOW="${CODEX_BRIDGE_CONTEXT_WINDOW:-64000}"
LOCAL_TOKEN="${CODEX_BRIDGE_LOCAL_TOKEN:-}"

detect_asset() {
  local os
  local arch
  os="$(uname -s)"
  arch="$(uname -m)"
  case "$os:$arch" in
    Linux:x86_64|Linux:amd64) echo "codex-bridge-linux-amd64" ;;
    Linux:aarch64|Linux:arm64) echo "codex-bridge-linux-arm64" ;;
    Darwin:x86_64|Darwin:amd64) echo "codex-bridge-darwin-amd64" ;;
    Darwin:arm64) echo "codex-bridge-darwin-arm64" ;;
    *) echo "unsupported platform: $os $arch" >&2; exit 1 ;;
  esac
}

download() {
  local url="$1"
  local output="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fL --retry 3 --connect-timeout 20 -o "$output" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -O "$output" "$url"
  else
    echo "curl or wget is required" >&2
    exit 1
  fi
}

random_token() {
  if [ -n "$LOCAL_TOKEN" ]; then
    printf '%s\n' "$LOCAL_TOKEN"
    return
  fi
  od -An -N16 -tx1 /dev/urandom | tr -d ' \n'
}

write_config() {
  if [ -f "$CONFIG_PATH" ]; then
    chmod 600 "$CONFIG_PATH"
    return
  fi
  mkdir -p "$(dirname "$CONFIG_PATH")" "$CODEX_DIR"
  local token
  token="$(random_token)"
  cat > "$CONFIG_PATH" <<EOF
[server]
listen = "127.0.0.1:8787"

[codex]
model_catalog_path = "$CODEX_DIR/models.codex-bridge.json"
default_model = "$MODEL"
local_token = "$token"

[model_discovery]
enabled = true
mode = "config"
cache_ttl = "10m"

[extensions.network]
proxy_url = ""

[capabilities.search]
enabled = false
providers = ["jina"]
max_results = 5
read_top_k = 3

[capabilities.vision]
enabled = false
provider = "jina_vlm"
mode = "describe"

[search_providers.jina]
type = "jina"
search_base_url = "https://s.jina.ai"
reader_base_url = "https://r.jina.ai"
api_key = "jina_xxx"

[vision_providers.jina_vlm]
type = "openai_chat_compatible_vision"
base_url = "https://api-beta-vlm.jina.ai/v1"
api_key = "jina_xxx"
model = "jina-vlm"

[providers.deepseek]
type = "openai_chat_compatible"
base_url = "$BASE_URL"
api_key = "$API_KEY"
profile = "deepseek"

[models.$MODEL]
display_name = "$DISPLAY_NAME"
provider = "deepseek"
upstream_model = "$MODEL"
context_window = $CONTEXT_WINDOW
supports_parallel_tool_calls = true
apply_patch_tool_type = "freeform"
EOF
  chmod 600 "$CONFIG_PATH"
}

install_service() {
  case "$(uname -s)" in
    Linux)
      if ! command -v systemctl >/dev/null 2>&1; then
        echo "systemctl not found. Start manually: $BIN_PATH --config $CONFIG_PATH"
        return
      fi
      local service_dir="$HOME/.config/systemd/user"
      local service_file="$service_dir/codex-bridge.service"
      mkdir -p "$service_dir"
      cat > "$service_file" <<EOF
[Unit]
Description=Codex Bridge
After=network-online.target

[Service]
Type=simple
ExecStart=$BIN_PATH --config $CONFIG_PATH
Restart=always
RestartSec=2
WorkingDirectory=$(dirname "$CONFIG_PATH")

[Install]
WantedBy=default.target
EOF
      if systemctl --user daemon-reload && systemctl --user enable --now codex-bridge.service && systemctl --user restart codex-bridge.service; then
        echo "service: $service_file"
      else
        echo "service registration failed. Start manually: $BIN_PATH --config $CONFIG_PATH"
      fi
      ;;
    Darwin)
      local label="com.codex-bridge"
      local plist_dir="$HOME/Library/LaunchAgents"
      local plist_file="$plist_dir/$label.plist"
      mkdir -p "$plist_dir" "$LOG_DIR"
      cat > "$plist_file" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$label</string>
  <key>ProgramArguments</key>
  <array>
    <string>$BIN_PATH</string>
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
      launchctl bootout "gui/$(id -u)" "$plist_file" >/dev/null 2>&1 || true
      if launchctl bootstrap "gui/$(id -u)" "$plist_file" && launchctl enable "gui/$(id -u)/$label" && launchctl kickstart -k "gui/$(id -u)/$label"; then
        echo "service: $plist_file"
      else
        echo "service registration failed. Start manually: $BIN_PATH --config $CONFIG_PATH"
      fi
      ;;
  esac
}

asset="$(detect_asset)"
download_url="https://github.com/$REPO/releases/latest/download/$asset"
tmp_file="$(mktemp)"
trap 'rm -f "$tmp_file"' EXIT

mkdir -p "$BIN_DIR" "$LOG_DIR"
echo "Downloading $asset..."
download "$download_url" "$tmp_file"
install -m 0755 "$tmp_file" "$BIN_PATH"
write_config

"$BIN_PATH" config check --config "$CONFIG_PATH"
"$BIN_PATH" codex configure --config "$CONFIG_PATH"
install_service

echo
echo "codex-bridge installed"
echo "binary: $BIN_PATH"
echo "config: $CONFIG_PATH"
if [ "$API_KEY" = "sk-xxx" ]; then
  echo "edit config api_key before using upstream model"
fi
