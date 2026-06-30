# 安装与运行

## Linux / macOS

直接安装最新 release，并注册用户级服务：

```bash
curl -fsSL https://raw.githubusercontent.com/octyean/codex-model-bridge/main/scripts/install.sh | bash
```

带上 DeepSeek key：

```bash
curl -fsSL https://raw.githubusercontent.com/octyean/codex-model-bridge/main/scripts/install.sh | env CODEX_BRIDGE_API_KEY="sk-xxx" bash
```

安装脚本会自动完成这些事：

- 按系统和 CPU 架构下载 GitHub Releases latest 里的二进制。
- 安装到 `~/.codex-bridge/bin/codex-bridge`。
- 首次运行时创建 `~/.codex-bridge/config.toml`。
- 写入 Codex `codex_bridge` provider、auth helper 和模型目录配置。
- 保留已有 Codex 默认模型；只有全新 Codex 配置才默认选 bridge 模型。
- Linux 注册 systemd user service，macOS 注册 launchd agent。
- 启动或重启 bridge 服务。

可选环境变量：

```bash
CODEX_BRIDGE_API_KEY="sk-xxx"
CODEX_BRIDGE_BASE_URL="https://api.deepseek.com"
CODEX_BRIDGE_MODEL="deepseek-v4-flash"
CODEX_BRIDGE_HOME="$HOME/.codex-bridge"
CODEX_BRIDGE_CONFIG="$HOME/.codex-bridge/config.toml"
```

重复执行同一条安装命令即可更新。配置文件不会被覆盖。

## Windows

打开 [GitHub Releases latest](https://github.com/octyean/codex-model-bridge/releases/latest)，下载对应的 Windows 二进制：

```text
codex-bridge-windows-amd64.exe
codex-bridge-windows-arm64.exe
```

把 exe 放到一个固定目录，双击运行。

第一次双击时会在 exe 所在目录创建：

```text
config.toml
```

同时会写入 Codex provider 和模型目录配置，并启动 bridge。首次运行后，把 `config.toml` 里的 `api_key = "sk-xxx"` 改成自己的 key，再双击 exe 启动即可。

更新时下载新版 exe 覆盖旧文件，然后重新双击。

## 重启服务

Linux：

```bash
systemctl --user restart codex-bridge.service
```

macOS：

```bash
launchctl kickstart -k "gui/$(id -u)/com.codex-bridge"
```

Windows 直接关闭当前窗口，再双击 exe 启动。

## 从源码运行

要求：

- Go 1.24+

运行：

```bash
go run ./cmd/codex-bridge config check --config config/config.toml
go run ./cmd/codex-bridge --config config/config.toml
```

构建所有平台：

```bash
scripts/build-release.sh
```

单独构建当前平台：

```bash
go build -o dist/codex-bridge ./cmd/codex-bridge
```
