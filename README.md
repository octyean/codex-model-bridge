# codex-bridge

## 简介

`codex-bridge` 是给 Codex CLI / Codex App 使用的第三方模型协议转换层。

它在 Codex 侧提供 OpenAI Responses 风格的 `/v1/responses`、`/v1/models` 等接口，并向上游调用 OpenAI-compatible Chat Completions 服务。你可以用它把 DeepSeek、Kimi、Qwen、Mimo 或其他兼容 Chat Completions 的模型接入 Codex，同时保留 Codex 的工具协议语义，比如 `apply_patch`、`tool_search`、`local_shell`、`web_search` 和多模态降级处理。

项目把 Codex Responses 协议、第三方模型差异、外部搜索和视觉扩展分开维护。新增模型时，通过 adapter 处理模型差异；接入新的搜索或视觉服务时，通过 extension provider 扩展。

### 工作路径

```text
Codex CLI / App
  -> codex-bridge /v1/responses
  -> OpenAI-compatible /chat/completions
  -> upstream model
```

### 功能

- 提供 `GET /health`、`GET /v1`、`GET /v1/models`、`POST /v1/responses`。
- 自动生成 Codex App / CLI 可识别的模型目录，例如 `~/.codex/models.codex-bridge.json`。
- 自动写入 Codex provider 配置，并保留已有 `model_provider` / `model`。
- 转换 Responses input、Responses tools 和 Chat Completions messages。
- 处理 `function`、`custom`、`apply_patch`、`tool_search`、`additional_tools`、`namespace`、`local_shell`、`shell`、`web_search` / `web_search_preview`。
- 通过 Jina、SearXNG、Brave Search、Tavily、Serper、DuckDuckGo Instant Answer、Firecrawl、Wikipedia、Semantic Scholar 等 provider 补充搜索能力。
- 通过 OpenAI-compatible vision provider 给 text-only 模型补充图片转文本能力。
- 为外部搜索、视觉、MCP provider 提供统一代理配置。

## 目录

- [简介](#简介)
- [一键安装/更新](#一键安装更新)
- [配置](#配置)
- [管理命令](#管理命令)
- [排障](#排障)

## 一键安装/更新

### Linux / macOS

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
- 写入 Codex provider 和模型目录配置。
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

### Windows

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

### 重启服务

Linux：

```bash
systemctl --user restart codex-bridge.service
```

macOS：

```bash
launchctl kickstart -k "gui/$(id -u)/com.codex-bridge"
```

Windows 直接关闭当前窗口，再双击 exe 启动。

### 从源码运行

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

## 配置

配置文件位置取决于安装方式：

```text
Linux / macOS 一键安装：~/.codex-bridge/config.toml
Windows 双击运行：exe 同目录下的 config.toml
源码运行：config/config.toml
```

源码运行时，Unix 上执行：

```bash
chmod 600 config/config.toml
```

### 基础配置

下面是可直接改的 DeepSeek 系列 OpenAI-compatible 示例。把占位值替换成自己的服务地址、模型名和 key。

```toml
[server]
listen = "127.0.0.1:8787"

[codex]
model_catalog_path = "/home/you/.codex/models.codex-bridge.json"
default_model = "deepseek-v4-flash"
local_token = "codex-bridge-local-token"

[model_discovery]
enabled = true
mode = "config"
cache_ttl = "10m"

[extensions.network]
proxy_url = ""

[providers.deepseek]
type = "openai_chat_compatible"
base_url = "https://api.deepseek.com"
api_key = "sk-xxx"
profile = "deepseek"

[models.deepseek-v4-flash]
display_name = "DeepSeek V4 Flash"
provider = "deepseek"
upstream_model = "deepseek-v4-flash"
context_window = 64000
supports_parallel_tool_calls = true
apply_patch_tool_type = "freeform"
```

### OpenAI-compatible provider

其他兼容 Chat Completions 的服务可以按同样方式配置：

```toml
[providers.openai_compatible]
type = "openai_chat_compatible"
base_url = "https://api.example.com/v1"
api_key = "sk-xxx"
profile = "default"

[models.example-model]
display_name = "Example Model"
provider = "openai_compatible"
upstream_model = "example-model"
context_window = 128000
supports_parallel_tool_calls = true
apply_patch_tool_type = "freeform"
```

`profile` 当前支持：

- `default`
- `deepseek`

DeepSeek 系列建议使用 `deepseek`，它会处理工具调用、强制 `tool_choice`、stream usage、prompt cache usage、tool pairing 等兼容细节。

### 搜索配置

搜索默认关闭。开启后，Codex 传来的 `web_search` / `web_search_preview` 会被转换成 bridge 内部可执行的搜索调用。

```toml
[capabilities.search]
enabled = true
providers = ["jina"]
max_results = 5
read_top_k = 3

[search_providers.jina]
type = "jina"
search_base_url = "https://s.jina.ai"
reader_base_url = "https://r.jina.ai"
api_key = "jina_xxx"
```

可用搜索 provider：

```toml
[search_providers.searxng_local]
type = "searxng"
base_url = "http://127.0.0.1:8080"

[search_providers.brave]
type = "brave"
api_key = "brave_xxx"

[search_providers.tavily]
type = "tavily"
api_key = "tvly_xxx"

[search_providers.serper]
type = "serper"
api_key = "serper_xxx"

[search_providers.duckduckgo_ia]
type = "duckduckgo_instant_answer"

[search_providers.firecrawl]
type = "firecrawl"
base_url = "https://api.firecrawl.dev"
api_key = "fc_xxx"

[search_providers.wikipedia]
type = "wikipedia"
base_url = "https://en.wikipedia.org"

[search_providers.semantic_scholar]
type = "semantic_scholar"
api_key = ""
```

Jina MCP 也可以作为搜索 provider：

```toml
[search_providers.jina_mcp]
type = "mcp"
server_url = "https://mcp.jina.ai/v1?include_tags=search,read"
authorization = "Bearer jina_xxx"
search_tool = "search_web"
read_tool = "read_url"
```

### 视觉配置

上游模型支持 image input 时，bridge 会按 Chat Completions `image_url` 传递。text-only 模型可以接入外部视觉 provider，把图片内容转成文本再交给模型。

```toml
[capabilities.vision]
enabled = true
provider = "jina_vlm"
mode = "describe"

[vision_providers.jina_vlm]
type = "openai_chat_compatible_vision"
base_url = "https://api-beta-vlm.jina.ai/v1"
api_key = "jina_xxx"
model = "jina-vlm"
```

### 代理配置

外部搜索、视觉、MCP provider 支持统一代理配置：

```toml
[extensions.network]
proxy_url = "socks5h://127.0.0.1:7890"
```

支持：

- `http://`
- `https://`
- `socks5://`
- `socks5h://`

### 字段说明

| 字段 | 说明 |
| --- | --- |
| `server.listen` | bridge 监听地址。Codex 的 provider `base_url` 要指向这个地址的 `/v1`。 |
| `codex.model_catalog_path` | 自动生成的 Codex 模型目录文件。服务启动时会刷新。 |
| `codex.default_model` | 新 Codex 配置没有默认模型时，`codex configure` 会写入这个模型。 |
| `codex.local_token` | Codex 调用 bridge 时使用的本地 bearer token。 |
| `providers.*.base_url` | 上游 OpenAI-compatible 服务地址，可以是 host、`/v1` 或直接 `/chat/completions`。 |
| `providers.*.api_key` | 上游模型服务密钥。 |
| `providers.*.profile` | 模型 adapter。 |
| `models.*.upstream_model` | 发给上游 Chat Completions 的真实模型名。 |
| `models.*.context_window` | Codex 侧可见上下文窗口。 |
| `models.*.apply_patch_tool_type` | Codex patch tool 类型，建议使用 `freeform`。 |
| `capabilities.search.enabled` | 是否启用 bridge web search 兼容层。 |
| `capabilities.vision.enabled` | 是否启用 text-only 模型的图片转文本。 |
| `extensions.network.proxy_url` | 外部搜索、视觉、MCP provider 使用的代理地址。 |

### 写入 Codex 配置

配置好 `config/config.toml` 后，执行：

```bash
codex-bridge codex configure --config config/config.toml
```

源码运行：

```bash
go run ./cmd/codex-bridge codex configure --config config/config.toml
```

命令会写入或更新：

- `model_catalog_json`
- `[model_providers.codex_bridge]`

已存在的 `~/.codex/config.toml` 会先写备份，例如：

```text
config.toml.bak-20260614153000
```

如果原配置已有 `model_provider` 或 `model`，命令会保留原值。空配置或新配置会写入 `codex_bridge` 和 `codex.default_model`。

自动写入后的典型配置：

```toml
model_catalog_json = "/home/you/.codex/models.codex-bridge.json"

[model_providers.codex_bridge]
name = "Codex Bridge"
base_url = "http://127.0.0.1:8787/v1"
wire_api = "responses"
experimental_bearer_token = "codex-bridge-local-token"
```

需要让 Codex 默认使用 bridge 模型时，可以手动加：

```toml
model_provider = "codex_bridge"
model = "deepseek-v4-flash"
```

## 管理命令

下面的 `codex-bridge` 可以替换成实际二进制路径，例如 `~/.codex-bridge/bin/codex-bridge`。

### 配置检查

```bash
codex-bridge config check --config config/config.toml
```

### 启动服务

```bash
codex-bridge --config config/config.toml
```

看到类似日志表示服务已启动：

```json
{"level":"INFO","msg":"catalog_written","path":"/home/you/.codex/models.codex-bridge.json"}
{"level":"INFO","msg":"server_started","listen":"127.0.0.1:8787"}
```

### 健康检查

```bash
curl -sS http://127.0.0.1:8787/health
```

### 检查模型列表

```bash
curl -sS \
  -H 'Authorization: Bearer codex-bridge-local-token' \
  http://127.0.0.1:8787/v1/models
```

### 最小 Responses 请求

```bash
curl -sS \
  -H 'Authorization: Bearer codex-bridge-local-token' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "deepseek-v4-flash",
    "input": "用一句话回答：codex-bridge 是什么？",
    "stream": false
  }' \
  http://127.0.0.1:8787/v1/responses
```

### 刷新模型目录

服务启动时会自动刷新模型目录，也可以手动执行：

```bash
codex-bridge catalog generate --config config/config.toml
```

模型目录会在这些场景变化：

- 启动或重启 bridge 服务。
- 手动执行 `catalog generate`。
- 修改 `[models.*]`、`[providers.*]`、模型 profile、上下文窗口、工具能力后重新生成。
- 修改 adapter 的能力声明后重新生成。

普通对话请求不会修改模型目录。

### Linux 服务命令

```bash
systemctl --user status codex-bridge.service
systemctl --user restart codex-bridge.service
systemctl --user stop codex-bridge.service
journalctl --user -u codex-bridge.service -f
```

部分 Linux 发行版默认不保留用户服务，可按需启用 linger：

```bash
loginctl enable-linger "$USER"
```

### macOS 服务命令

```bash
launchctl print "gui/$(id -u)/com.codex-bridge"
launchctl kickstart -k "gui/$(id -u)/com.codex-bridge"
launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.codex-bridge.plist"
```

### Codex CLI 验证

确保 bridge 已启动，Codex config 已写入：

```bash
codex --search exec --json --skip-git-repo-check -C /tmp \
  "请使用网页搜索查询 OpenAI Codex GitHub 仓库页面标题，并用一句中文回答。"
```

## 排障

### Codex 里看不到 bridge 模型

启动 bridge：

```bash
codex-bridge --config config/config.toml
```

服务启动时应写出 `catalog_written`。然后检查 Codex 配置：

```toml
model_catalog_json = "/home/you/.codex/models.codex-bridge.json"

[model_providers.codex_bridge]
base_url = "http://127.0.0.1:8787/v1"
wire_api = "responses"
experimental_bearer_token = "codex-bridge-local-token"
```

缺少配置时执行：

```bash
codex-bridge codex configure --config config/config.toml
```

### 请求返回 401

`codex.local_token` 和 Codex provider 的 `experimental_bearer_token` 必须一致。

### 服务启动时报配置权限错误

Unix 上配置文件需要限制权限：

```bash
chmod 600 config/config.toml
```

Windows 不按 Unix 权限位检查。

### 端口被占用

修改：

```toml
[server]
listen = "127.0.0.1:8788"
```

然后重新执行：

```bash
codex-bridge codex configure --config config/config.toml
```

这样 Codex provider 的 `base_url` 会跟着改。

### `apply_patch` 生成了错误格式

确认模型目录里该模型的：

```json
"apply_patch_tool_type": "freeform"
```

并确认 Codex 使用的是 bridge 生成的模型目录。DeepSeek adapter 已经对 `apply_patch` 提示和输入归一做了处理。模型偶发输出不合规 patch 时，可以让 Codex 重试当前工具调用。

### `--search` 没有效果

检查三处：

1. `capabilities.search.enabled = true`
2. `capabilities.search.providers` 至少有一个可用 provider
3. Codex 请求里带了 `web_search` 或 `web_search_preview`

bridge 会把 Codex 的 `web_search` 转成同名 Chat function tool。工具名保持为 `web_search`，模型更容易按预期调用搜索。

### Windows 双击后没有看到 bridge 模型

打开 PowerShell，进入 exe 所在目录后手动运行：

```powershell
.\codex-bridge-windows-amd64.exe config check --config .\config.toml
.\codex-bridge-windows-amd64.exe --config .\config.toml
```

这样可以看到完整错误信息。常见原因是端口被占用、`api_key` 仍是占位值，或 Codex 配置没有写入成功。
