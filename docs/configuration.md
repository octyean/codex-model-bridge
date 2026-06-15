# 配置指南

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

## 基础配置

下面是可直接改的 DeepSeek 系列示例。把占位值替换成自己的服务地址、模型名和 key。

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
type = "openai_compatible"
base_url = "https://api.deepseek.com"
api_key = "sk-xxx"
profile = "deepseek"
protocol = "chat_completions"

[models.deepseek-v4-flash]
display_name = "DeepSeek V4 Flash"
provider = "deepseek"
upstream_model = "deepseek-v4-flash"
context_window = 64000
supports_parallel_tool_calls = true
apply_patch_tool_type = "freeform"
```

## OpenAI-compatible provider

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

`type` 支持两种写法：

- `openai_chat_compatible`：老兼容模式，始终向上游请求 `/chat/completions`。
- `openai_compatible`：同时支持 Chat Completions 和 Responses。`protocol` 可写 `chat_completions`、`responses` 或 `auto`；不写时，`gpt-*`、`o3*`、`o4*` upstream 模型会走 `/responses`，其他模型走 `/chat/completions`。

如果上游是 OpenAI 原生 GPT / o 系列模型，建议使用：

```toml
[providers.openai]
type = "openai_compatible"
base_url = "https://api.openai.com/v1"
api_key = "sk-xxx"
profile = "default"
protocol = "auto"

[models.gpt-5-bridge]
display_name = "GPT 5 Bridge"
provider = "openai"
upstream_model = "gpt-5.4"
context_window = 400000
supports_parallel_tool_calls = true
apply_patch_tool_type = "freeform"
```

这条路径会把 Codex 的 `/v1/responses` 请求原样转给上游 `/responses`，只替换 `model` 为 `upstream_model`，因此 `reasoning`、`verbosity` 和 Responses 原生工具语义不会被降级成 Chat Completions 字段。

`profile` 当前支持：

- `default`
- `deepseek`

`default` 适合普通 OpenAI-compatible 模型。`deepseek` 适合 DeepSeek 这类对工具调用和补丁格式更挑剔的模型。

## 模型名与显示名

Codex App 里看到的是模型目录，真正发给上游的是 `upstream_model`。

三层名字各管一件事：

- `models.<slug>`：Codex 侧选择模型时使用的模型 ID。为了兼容 Codex App，可以用 `gpt-*` 形式。
- `display_name`：Codex App / CLI 里显示给人的名字，可以写成 `DeepSeek V4 Flash`、`Qwen3 Coder` 这类人能看懂的名称。
- `upstream_model`：实际发给上游 API 的模型名，比如 `deepseek-v4-flash`。

如果你想让 Codex App 里显示得顺眼，`slug` 可以保留 `gpt-*`，`display_name` 改成真实模型名。

示例：

```toml
[models."gpt-5.2"]
display_name = "DeepSeek V4 Flash"
provider = "deepseek"
profile = "deepseek"
upstream_model = "deepseek-v4-flash"
context_window = 64000
supports_parallel_tool_calls = true
apply_patch_tool_type = "freeform"
input_modalities = ["text", "image"]
```

## 搜索配置

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

## 视觉配置

Codex 是否允许上传图片，先看模型目录里的 `input_modalities`。如果模型只声明 `["text"]`，图片会在 Codex CLI / App 侧被拦住，bridge 收不到请求，第三方视觉 provider 也不会触发。

上游模型支持 image input 时，bridge 会按 Chat Completions `image_url` 传递。

上游是 text-only 模型时，也可以显式声明：

```toml
input_modalities = ["text", "image"]
```

这表示“这个 bridge 模型入口能接图片”，不表示上游模型原生能看图。请求进入 bridge 后，会使用 `[capabilities.vision]` 配置的视觉 provider 先把图片转成文本，再把 `[image analysis]` 内容交给 text-only 模型。

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

## 代理配置

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

## 字段说明

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

## 写入 Codex 配置

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
