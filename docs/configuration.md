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

修改配置后建议先检查：

```bash
codex-bridge config check --config ~/.codex-bridge/config.toml
```

`config check` 会严格检查字段名，拼错或已经移除的字段会直接报错并返回非零状态。服务启动仍保持兼容读取，不会因为未知字段中断，但会记录 `config_unknown_fields` 告警，便于清理未生效配置。

## 自动配置

推荐先让内置向导探测上游能力并生成配置：

```bash
codex-bridge setup \
  --config ~/.codex-bridge/config.toml \
  --upstream-base-url https://api.example.com/v1 \
  --upstream-api-key sk-xxx \
  --model kimi-for-coding \
  --profile kimi \
  --yes
```

`setup` 会探测 `/models`、`/responses` 流式和 `/chat/completions` 流式能力。上游支持 `/responses` 流式时，会写入：

```toml
protocol = "responses"
```

模型名无法可靠判断 adapter 时，例如上游只暴露内部别名 `k3`，应显式传 `--profile kimi`。可用值与配置文件中的 `profile` 相同：`default`、`deepseek`、`kimi`、`mimo`、`openai`。

已有配置默认会保留；需要更换上游时加：

```bash
--replace-upstream
```

只想查看探测结果、不写配置时，用：

```bash
codex-bridge probe --upstream-base-url https://api.example.com/v1 --upstream-api-key sk-xxx --model kimi-for-coding
```

## 手写配置

下面是可直接改的 DeepSeek 系列示例。把占位值替换成自己的服务地址、模型名和 key。

```toml
[server]
listen = "127.0.0.1:8787"

[codex]
model_catalog_path = "/home/you/.codex/models.codex-bridge.json"
default_model = "deepseek-v4-flash"
local_token = "replace-with-a-unique-random-token"

[model_discovery]
enabled = true
mode = "merge"

[verification]
max_age_hours = 720

[diagnostics]
retention_days = 14
max_total_mb = 1024

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

Bridge 有三种执行路径：

- `native_responses`：OpenAI 原生 profile 直接转发 `/responses`，只替换模型名。
- `projected_responses`：Kimi、Mimo 等第三方 profile 继续请求上游 `/responses`；把上游不接受的 `custom apply_patch` 投影为 `write_file`、`replace_text` 等 function tools，返回时再还原成 Codex 原生工具事件。
- `chat_completions`：只有配置明确使用 `chat_completions` 时，才把 Responses 会话投影到 Chat Completions。

第三方模型请求 Bridge 内置 `web_search` 时，Bridge 会在 Projected Responses 内执行搜索，把结果作为 `function_call_output` 继续提交给上游，不会切换到 Chat Completions。

对于非 GPT 的第三方模型，Projected Responses 和 Chat Completions 会额外启用显式任务终止协议。模型继续工作时必须调用普通 Codex 工具；完成或真实阻塞时必须调用 Bridge 内部的 `codex_bridge_task_end`。Bridge 会隐藏这个内部工具，并把它的 `result` 转成最终 assistant 消息。纯文本不会再被当成任务完成：Bridge 会纠正一次，第二次仍违反协议时返回 `model_behavior_error`。结构化输出请求不启用该协议。

GPT 系列始终保持原协议透传。判断同时依据 profile 和真实 `upstream_model`；即使 GPT 模型配置为 `profile = "default"`，也不会注入终止工具、拦截普通文本或增加纠正请求。

执行模式和 Responses 可选能力可以在模型级明确覆盖：

```toml
[models.gpt-5.3-codex]
display_name = "Kimi for Coding"
provider = "upstream"
profile = "kimi"
upstream_model = "kimi-for-coding"
execution_mode = "projected_responses"
supports_responses_options = true
supports_responses_structured_output = true
default_reasoning_level = "high"
supported_reasoning_levels = ["high", "max"]
context_window = 256000
supports_parallel_tool_calls = true
apply_patch_tool_type = "freeform"
```

- `execution_mode` 可写 `native_responses`、`projected_responses` 或 `chat_completions`。不写时继续按 provider 协议、真实 `upstream_model` 和 profile 推导。
- `supports_responses_options` 控制 reasoning 和 verbosity 是否向 Codex 宣告并发送给上游。
- `supports_responses_structured_output` 控制是否原样发送 `text.format=json_schema`。设为 `false` 时仍走 Responses，Bridge 会改用 JSON Schema 指令并校正最终输出。
- `default_reasoning_level` 控制 Codex 默认思考强度；不写时默认使用 `high`。
- `supported_reasoning_levels` 按顺序声明模型真实支持的思考档位。不写时使用 `low`、`medium`、`high`；`xhigh`、`max` 等额外能力应写在模型配置中，不再通过模型名硬编码。
- setup 只把实际 probe 过的模型能力写入生成配置，不会拿一个模型的可选能力替其他模型做结论。

已有配置建议用 `verify` 逐模型实测：

```bash
codex-bridge verify \
  --config ~/.codex-bridge/config.toml \
  --provider-name upstream \
  --models kimi-for-coding,mimo-v2.5-pro
```

`--models` 同时接受 Codex slug 和真实 `upstream_model`。必须显式传 `--models`，或者明确使用 `--all` 验证全部已配置模型；两者不能同时使用。结果默认写入配置文件同目录的 `model-capabilities.json`，不会修改 `config.toml`；provider 使用 `protocol = "auto"` 时，Bridge 会优先采用仍在有效期内的真实验证结果。

能力缓存当前为 version 3。每条模型记录包含 `probe_version`、`verified_at`、`expires_at`，以及凭据和有效 profile 的不可逆指纹；缓存文件不会保存 API key 明文。探测逻辑升级、记录过期、凭据变化或 profile 变化后，Bridge 不会继续把旧结果当作当前能力。服务启动和 `config check` 会只针对第三方 profile 提示需要重新验证的模型。

`model-capabilities.json` 和 `model-slots.json` 使用同一套跨进程状态写入规则：写入前获取锁，锁内重新读取并合并，再通过原子替换保存。多个 setup、verify 或服务进程同时运行时，不会用旧快照覆盖其他进程刚写入的模型记录。

`[verification]` 可设置：

- `cache_path`：能力缓存路径；相对路径按配置文件目录解析。
- `max_age_hours`：缓存有效期；不写时为 720 小时。

`profile` 当前支持：

- `default`
- `deepseek`
- `kimi`
- `mimo`
- `openai`

`default` 适合普通 OpenAI-compatible 模型。`deepseek` 适合 DeepSeek 这类对工具调用和补丁格式更挑剔的模型。
`kimi` 适合 Kimi for Coding，会把 Codex 的文件编辑能力翻译成 `write_file`、`replace_text`、`insert_text_at_line`、`insert_text_after_match`、`move_file`、`delete_file` 这组结构化 function tools。
`mimo` 适合 Mimo，保留图片输入能力，并使用和 Kimi 相同的结构化文件编辑工具。
`openai` 用于明确启用 OpenAI 原生 Responses 工具和图片能力；标准 GPT / o 系列模型通常会由 Bridge 自动识别，不必手写。

## 自动发现模型

bridge 可以从上游 `/models` 自动获取模型，并生成 Codex 可直接请求的模型映射：

```toml
[model_discovery]
enabled = true
mode = "merge"
```

`mode` 有三种：

- `config`：只使用手写的 `[models.*]`。
- `merge`：保留手写模型，再把上游 `/models` 里新增的模型补进来。默认推荐这个。
- `upstream`：只使用上游 `/models` 返回的模型，适合想完全免手写模型的人。

自动发现会按确定顺序把模型分配到 Codex App 能稳定识别的兼容槽位。槽位数量有限，超出的上游模型不会自动进入 App 目录；需要替换某个槽位时，手写对应的 `[models.<slug>]`。自动生成的模型使用对应 provider 的 `profile`，上下文窗口按保守值处理，`apply_patch_tool_type` 固定为 `freeform`。

槽位分配默认保存在配置文件同目录的 `model-slots.json`。上游 `/models` 返回顺序变化或服务重启后，已有模型会优先恢复原槽位。可用 `model_discovery.state_path` 修改保存位置；`mode = "upstream"` 在多 provider 场景下只会统一清空一次，不会在处理下一个 provider 时覆盖前一个 provider 的结果。

`codex-bridge codex configure` 和服务启动都会执行一次模型发现。`mode = "upstream"` 且没有手写模型时，只要上游 `/models` 可用，也能自动选出默认模型写入 Codex 配置。

## 模型名与显示名

Codex App 里看到的是模型目录，真正发给上游的是 `upstream_model`。

三层名字各管一件事：

- `models.<slug>`：Codex 侧选择模型时使用的模型 ID。Codex App 当前不能稳定显示任意 slug，setup 和自动发现会按确定规则使用少量 GPT 兼容槽位。
- `display_name`：Codex App / CLI 里显示给人的名字，可以写成 `DeepSeek V4 Flash`、`Qwen3 Coder` 这类人能看懂的名称。
- `upstream_model`：实际发给上游 API 的模型名，比如 `deepseek-v4-flash`。

兼容槽位只解决 Codex App 可见性，不参与上游模型身份和执行模式判断。日志中的 `model` 可能是 `gpt-5.3-codex`，`upstream_model` 仍是 `kimi-for-coding`；Bridge 根据 `upstream_model`、profile 和模型能力决定实际协议。模型发现会先排序，再优先分配真实 GPT、Kimi 和 Mimo，剩余模型按名称分配空槽位，避免 `/models` 返回顺序变化导致映射漂移。

## 诊断日志保留

Bridge 启动时只清理自己管理的 `sessions/` 目录，不会删除同级其他文件或用户目录：

```toml
[diagnostics]
retention_days = 14
max_total_mb = 1024
```

不配置时默认保留 14 天，session 目录总量上限为 1GB。清理结果会写入 `session_logs_pruned` 启动日志。全局 `tool-calls.jsonl`、`incidents.jsonl`、`recoveries.jsonl` 和 `sessions/index.jsonl` 单文件达到 64MB 后自动轮转，保留 3 份历史文件。

示例：

```toml
[models.deepseek-v4-flash]
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
providers = ["duckduckgo_html", "jina"]
max_results = 5

[search_providers.duckduckgo_html]
type = "duckduckgo_html"

[search_providers.jina]
type = "jina"
search_base_url = "https://s.jina.ai"
reader_base_url = "https://r.jina.ai"
api_key = ""
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

[search_providers.duckduckgo_html]
type = "duckduckgo_html"

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

Codex 是否允许上传图片，先看模型目录里的 `input_modalities`。如果模型只声明 `["text"]`，图片会在 Codex CLI / App 侧被拦住，bridge 收不到请求。

上游模型支持 image input 时，在对应 `[models.*]` 里显式声明：

```toml
input_modalities = ["text", "image"]
```

声明后，bridge 会按 Chat Completions `image_url` 把图片传给上游模型。不要给不支持图片的上游模型声明 `image`；否则上游会直接报错。

上游是 text-only 模型时，不声明 `image`。后续如需“图片转文本再交给文本模型”，再启用 `[capabilities.vision]` 这类兜底能力。

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
| `models.*.upstream_model` | 发给上游的真实模型名。 |
| `models.*.execution_mode` | 可选的模型级执行模式覆盖。 |
| `models.*.supports_responses_options` | 上游 Responses 是否支持 reasoning 和 verbosity。 |
| `models.*.supports_responses_structured_output` | 上游 Responses 是否原生支持 JSON Schema。 |
| `models.*.default_reasoning_level` | Codex 默认思考强度；默认 `high`。 |
| `models.*.supported_reasoning_levels` | 模型实际支持的思考档位，顺序会保留到 Codex 模型目录。 |
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
- `[model_providers.codex_bridge.auth]`

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

[model_providers.codex_bridge.auth]
command = "/home/you/.codex-bridge/bin/codex-bridge"
args = ["auth", "token", "--config", "/home/you/.codex-bridge/config.toml"]
timeout_ms = 5000
refresh_interval_ms = 0
```

需要让 Codex 默认使用 bridge 模型时，可以手动加：

```toml
model_provider = "codex_bridge"
model = "deepseek-v4-flash"
```

Codex 官方要求自定义 provider 放在用户级 `~/.codex/config.toml`。项目里的 `.codex/config.toml` 不能覆盖 `model_provider`、`model_providers` 或 provider 认证配置。`model_catalog_json` 是启动时读取，改完模型目录后需要重启 Codex App，或新开 Codex CLI 会话。
