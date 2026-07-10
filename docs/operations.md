# 管理命令

下面的 `codex-bridge` 可以替换成实际二进制路径，例如 `~/.codex-bridge/bin/codex-bridge`。

本地调试、替换正在运行的二进制、按 Codex 会话 ID 反查日志，见 [诊断日志与本地调试](diagnostics-and-local-debug.md)。

## 配置检查

```bash
codex-bridge config check --config config/config.toml
```

## 探测上游协议

只探测，不修改配置：

```bash
codex-bridge probe \
  --upstream-base-url https://api.example.com/v1 \
  --upstream-api-key sk-xxx \
  --model kimi-for-coding
```

输出里重点看：

- `models_ok`：上游 `/models` 是否可用。
- `responses_stream_ok`：上游 `/responses` 是否能完成一轮流式响应。
- `responses_tools_ok`：上游 `/responses` 是否能返回非流式 function call。
- `responses_tool_stream_ok`：上游 `/responses` 是否能完整流式返回 function call 名称和参数。
- `responses_tool_continuation_ok`：提交 `function_call_output` 后，上游能否继续生成最终回答。
- `responses_options_ok`：上游是否接受 Codex 使用的 reasoning 和 verbosity 选项。
- `responses_structured_output_ok`：上游是否原生支持 `text.format=json_schema`。
- `chat_stream_ok`：上游 `/chat/completions` 是否能完成一轮流式响应。
- `chat_tools_ok`：上游 `/chat/completions` 是否能返回非流式 tool call。
- `chat_tool_stream_ok`：上游是否能完整流式返回 tool call 名称和参数。
- `recommended_protocol`：建议写入配置的协议。
- `failures`：未通过的探测阶段和真实错误。部分可选能力失败时，仍可能推荐 Responses。

## 验证已配置模型

`probe` 用命令行传入的地址和 key 做一次临时检查；`verify` 读取现有配置，逐模型验证并更新能力缓存：

```bash
codex-bridge verify \
  --config ~/.codex-bridge/config.toml \
  --provider-name mcodex \
  --models kimi-for-coding,mimo-v2.5-pro
```

`--models` 可写 Codex slug 或真实 upstream model。为避免误测其他模型，命令要求显式传 `--models`，或者明确传 `--all` 验证全部已配置模型；两者不能同时使用。输出是完整能力矩阵和缓存路径；任一模型的 Responses 与 Chat 主路径都不可用时，命令返回非 0。该命令不会修改 `config.toml` 或 Codex 默认模型。

能力缓存记录 `probe_version`、`verified_at` 和 `expires_at`。探测版本变化或记录过期后，重新运行 `verify` 即可更新；Bridge 会保留旧记录用于诊断，但不会把它当作当前能力结论。

## 生成或更新配置

首次生成配置：

```bash
codex-bridge setup \
  --config ~/.codex-bridge/config.toml \
  --upstream-base-url https://api.example.com/v1 \
  --upstream-api-key sk-xxx \
  --model kimi-for-coding \
  --yes
```

已有配置默认保留。需要更换上游时：

```bash
codex-bridge setup \
  --config ~/.codex-bridge/config.toml \
  --upstream-base-url https://api.new.example/v1 \
  --upstream-api-key sk-new \
  --replace-upstream \
  --yes
```

## 启动服务

```bash
codex-bridge --config config/config.toml
```

看到类似日志表示服务已启动：

```json
{"level":"INFO","msg":"catalog_written","path":"/home/you/.codex/models.codex-bridge.json"}
{"level":"INFO","msg":"server_started","listen":"127.0.0.1:8787"}
```

## 健康检查

```bash
curl -sS http://127.0.0.1:8787/health
```

## 检查模型列表

```bash
curl -sS \
  -H 'Authorization: Bearer codex-bridge-local-token' \
  http://127.0.0.1:8787/v1/models
```

## 最小 Responses 请求

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

## DeepSeek 缓存诊断

`profile = "deepseek"` 的请求会在 `upstream_usage` 日志里输出缓存诊断字段：

- `cached_input_tokens` / `fresh_input_tokens`：上游返回的缓存命中和未命中输入 token。
- `cache_hit_rate_permille`：本次请求命中率，`800` 表示 80.0%。
- `prefix_hash`、`system_hash`、`tools_hash`：请求前缀形状指纹，用来判断 system prompt 或工具 schema 是否变化。
- `prefix_changed`、`prefix_change_reasons`：和同一模型、同一 profile 的上一条请求相比，前缀是否变化，以及变化来自 `system` 还是 `tools`。
- `tool_schema_tokens`、`tool_count`、`message_count`：排查工具 schema 体积和请求增长用。

示例：

```json
{"msg":"upstream_usage","model":"deepseek-v4-flash","profile":"deepseek","cached_input_tokens":8000,"fresh_input_tokens":1200,"cache_hit_rate_permille":869,"prefix_changed":false}
```

如果 `fresh_input_tokens` 突然升高，先看 `prefix_change_reasons`。`tools` 变化通常说明 Codex 本轮暴露的工具集合或工具参数 schema 变了；`system` 变化通常说明 instructions、bridge 策略提示或能力边界提示发生了变化。

## 刷新模型目录

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

自动发现的兼容槽位记录在 `model-slots.json`，逐模型验证结果记录在 `model-capabilities.json`。两者默认和 Bridge 配置文件放在同一目录。

## Linux 服务命令

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

## macOS 服务命令

```bash
launchctl print "gui/$(id -u)/com.codex-bridge"
launchctl kickstart -k "gui/$(id -u)/com.codex-bridge"
launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.codex-bridge.plist"
```

## Codex CLI 验证

确保 bridge 已启动，Codex config 已写入：

```bash
codex --search exec --json --skip-git-repo-check -C /tmp \
  "请使用网页搜索查询 OpenAI Codex GitHub 仓库页面标题，并用一句中文回答。"
```
