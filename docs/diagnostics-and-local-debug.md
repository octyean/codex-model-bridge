# 诊断日志与本地调试

这份文档用于排查 Codex 通过 `codex-bridge` 调第三方模型时的真实问题。重点看真实请求、真实工具调用和真实文件副作用，不用 fake provider 结论替代现场证据。

## 日志模块边界

持久化诊断日志统一走 `internal/diagnostics`：

- JSONL 写入、会话目录、安全文件名由 `internal/diagnostics` 负责。
- 工具行为语义由 `internal/toollog` 负责。
- 事故语义由 `internal/incidentlog` 负责。
- 上游请求 dump 由 `internal/requestdump` 负责，文件名清洗走 `internal/diagnostics`。

这样做的目的是让所有诊断文件使用同一套落盘规则，同时保留每类日志自己的业务语义。

## 关键环境变量

```bash
export CODEX_BRIDGE_TOOL_LOG="$HOME/.codex-bridge/logs/tool-calls.jsonl"
export CODEX_BRIDGE_DUMP_UPSTREAM_REQUEST="$HOME/.codex-bridge/logs/upstream-requests"
export CODEX_BRIDGE_INCIDENT_LOG="$HOME/.codex-bridge/logs/incidents.jsonl"
```

`CODEX_BRIDGE_INCIDENT_LOG` 可以不显式设置。未设置时，bridge 会优先从 `CODEX_BRIDGE_TOOL_LOG` 推导出同目录下的 `incidents.jsonl`。

## 日志文件

全局日志：

```text
~/.codex-bridge/logs/tool-calls.jsonl
~/.codex-bridge/logs/incidents.jsonl
~/.codex-bridge/logs/upstream-requests/
```

按 Codex 会话拆分后的日志：

```text
~/.codex-bridge/logs/sessions/index.jsonl
~/.codex-bridge/logs/sessions/<codex_session_id>/requests.jsonl
~/.codex-bridge/logs/sessions/<codex_session_id>/tool-calls.jsonl
~/.codex-bridge/logs/sessions/<codex_session_id>/incidents.jsonl
```

`sessions/index.jsonl` 用来从会话 ID、请求 ID、模型和用户最后一段提示词反查。拿到 Codex 的 `thread_id` 后，优先看对应目录。

## 关键字段

每条工具和事故日志尽量保留这些字段：

| 字段 | 含义 |
| --- | --- |
| `request_id` | bridge 为每次 `/v1/responses` 请求生成的 ID |
| `codex_session_id` | Codex 会话 ID，通常来自 `X-Codex-Turn-Metadata.thread_id` |
| `model` | Codex 侧看到或请求的模型名 |
| `upstream_model` | bridge 实际发给上游的模型名 |
| `profile` | bridge 使用的 adapter profile，如 `kimi`、`mimo`、`deepseek` |
| `call_id` | Codex 工具调用 ID |
| `tool` | 模型选择的工具名 |
| `kind` | bridge 归一后的工具类型 |
| `raw_arguments` | 模型原始工具参数 |
| `model_call` | 工具失败、改写或改道时挂回的模型原始工具调用 |
| `failure_kind` | 已识别的失败类型，如 `context_mismatch`、`tool_search_empty` |
| `request_summary` | 请求摘要，包含最后一段用户提示词预览和 hash |
| `upstream_request_dump` | 对应上游请求 dump 文件路径 |

排查模型映射问题时，重点对比 `model` 和 `upstream_model`。例如 Codex 侧选择 `gpt-5.3-codex`，实际发给上游可能是 `kimi-for-coding`。

## Tool Runtime Broker

`internal/toolruntime` 负责工具运行时治理。它不按工具名写黑名单，也不靠不断追加错误字符串挡问题，而是在 bridge 可控边界做三件事：

- 对工具名、副作用和参数做能力画像，形成稳定的工具签名：`tool_key + capability + arguments_hash`。
- `tool_key` 会归一 MCP 暴露名和短工具名，例如 `mcp_xxx__browser_navigate` 与 `browser_navigate` 会落到同一个工具身份。
- 记录同一 Codex 会话内每次工具输出的签名、结果 hash、是否成功、是否产生进展。
- 在模型下一次工具调用投射给 Codex 前判断：默认允许；同一签名已经失败且无进展，或同一响应内重复提交同一签名时，才停止重复调用。

这个 Broker 不限制 MCP 的能力边界。未知工具默认允许，所有工具都走同一套签名账本。它不判断用户“该用哪个工具”，只阻止同一工具签名在没有进展后继续原样重放。停止时会通过本地 `exec_command` 回传结构化提示，要求模型换一个实质不同的动作。

新增日志事件：

| 事件 | 含义 |
| --- | --- |
| `tool_broker_decision` | Broker 对模型工具调用的运行时决策，包含 `action`、`reason`、`profiled_tool`、`progress_key` |
| `runtime_outcome` | 工具输出观察结果，挂在 `tool_output` 中，包含 `ok`、`category`、`progress`、`output_hash` |

排查工具重复调用时，优先看：

```bash
SESSION_ID="019fxxxx"
LOG_DIR="$HOME/.codex-bridge/logs/sessions/$SESSION_ID"
rg 'tool_broker_decision|runtime_outcome|TOOL_RUNTIME_NO_PROGRESS|progress_key|arguments_hash' "$LOG_DIR/tool-calls.jsonl"
```

判断口径：

- `action=allow`：本次工具调用没有命中复用或无进展停止条件。
- `action=stop`：同一工具签名已经失败且没有进展，继续重复会扩大循环。
- `reason=same_tool_signature_failed_without_progress`：停止原因来自同一签名的历史失败，不来自工具名黑名单。
- `reason=same_tool_signature_already_requested_in_response`：模型在同一响应里重复提交了相同工具签名，后续重复调用会被本地结果替代。

## 排查顺序

拿到 Codex 会话 ID 后：

```bash
SESSION_ID="019fxxxx"
LOG_DIR="$HOME/.codex-bridge/logs/sessions/$SESSION_ID"

ls -lah "$LOG_DIR"
tail -n 50 "$LOG_DIR/requests.jsonl"
tail -n 100 "$LOG_DIR/tool-calls.jsonl"
tail -n 100 "$LOG_DIR/incidents.jsonl" 2>/dev/null || true
```

没有会话 ID 时，从全局索引按时间、模型或提示词查：

```bash
rg "关键词|模型名|request_id" "$HOME/.codex-bridge/logs/sessions/index.jsonl"
```

定位到 `request_id` 后：

```bash
REQ_ID="req_xxx"
rg "$REQ_ID" "$HOME/.codex-bridge/logs/tool-calls.jsonl"
rg "$REQ_ID" "$HOME/.codex-bridge/logs/incidents.jsonl"
find "$HOME/.codex-bridge/logs/upstream-requests" -type f -name "*$REQ_ID*" -print
```

看工具链时，按这个顺序读：

```text
request_started
model_tool_call
tool_call_rewritten / tool_call_rerouted / tool_output
model_call
failure_kind
raw_output / formatted_output
```

这样能看到模型当时选了什么工具、给了什么参数、bridge 有没有拦截或改道、Codex 工具真实返回了什么。

## 本地构建

```bash
go build -o /tmp/codex-bridge-local ./cmd/codex-bridge
go run ./cmd/codex-bridge config check --config "$HOME/.codex-bridge/config.toml"
```

构建只能证明代码能编译，不能证明 Codex 真实可用。涉及工具协议、模型行为、流式响应和文件副作用时，继续跑下面的真实 Codex 请求验证。

## 替换当前二进制

替换正在运行的二进制前先备份。Linux 上如果直接覆盖运行中的文件，可能遇到 `Text file busy`，所以先停旧进程再替换。

```bash
BIN="$HOME/.codex-bridge/bin/codex-bridge"
CONFIG="$HOME/.codex-bridge/config.toml"
TS="$(date +%Y%m%d%H%M%S)"

OLD="$(pgrep -f "^$BIN --config $CONFIG$" || true)"
if [ -n "$OLD" ]; then
  kill $OLD
  sleep 1
fi

cp "$BIN" "$BIN.bak.$TS"
cp /tmp/codex-bridge-local "$BIN"
chmod +x "$BIN"

export CODEX_BRIDGE_TOOL_LOG="$HOME/.codex-bridge/logs/tool-calls.jsonl"
export CODEX_BRIDGE_DUMP_UPSTREAM_REQUEST="$HOME/.codex-bridge/logs/upstream-requests"

nohup "$BIN" --config "$CONFIG" > "$HOME/.codex-bridge/logs/restart-$TS.log" 2>&1 &
sleep 1
curl -sS http://127.0.0.1:8787/health
pgrep -af "^$BIN --config $CONFIG$"
```

如果是 systemd/launchd 管理的服务，优先用服务命令重启，避免手动进程和服务进程并存。

## 真实 Codex 请求验证

用当前真实 Codex 配置发起一次小任务：

```bash
OUT=/tmp/codex-bridge-smoke.out
WORKDIR="$(mktemp -d /tmp/codex-bridge-smoke.XXXXXX)"
codex -m gpt-5.4-mini \
  --ask-for-approval never \
  --sandbox danger-full-access \
  exec -C "$WORKDIR" \
  --output-last-message "$OUT" \
  --json "请用文件编辑工具创建 bridge-smoke.txt，内容只有 bridge smoke ok。读取文件验证后，只回复 bridge-smoke-ok。" </dev/null

cat "$OUT"
```

验证后清理临时文件：

```bash
rm -rf "$WORKDIR"
```

然后检查会话日志：

```bash
tail -n 10 "$HOME/.codex-bridge/logs/sessions/index.jsonl"
```

如果 Codex CLI 输出了 `thread_id`，对应目录应出现 `requests.jsonl` 和 `tool-calls.jsonl`。

## 常见判断

- `SHELL_FILE_WRITE_BLOCKED`：模型试图用 shell 写文件，被 bridge 拦截。看同一会话后续是否改用文件编辑工具。
- `context_mismatch`：文件编辑上下文不匹配。看 `model_call.raw_arguments` 和工具输出里的目标文件现状。
- `tool_search_empty`：模型搜索工具失败。看后续是否误用 MCP resource 或回到已有可见工具。
- `mcp_resources_empty`：MCP resource 列表为空。不要把它当成所有 MCP 工具不可用，只说明当前没有可读 resource。
- 只有全局日志、没有会话目录：检查请求头或 body 里是否有 Codex thread/session 信息，并看 `sessions/index.jsonl` 是否为空。

## 交付前检查

```bash
go build -o /tmp/codex-bridge-local ./cmd/codex-bridge
go run ./cmd/codex-bridge config check --config "$HOME/.codex-bridge/config.toml"
curl -sS http://127.0.0.1:8787/health
```

涉及真实环境替换时，还要跑一次真实 Codex 请求，并确认：

- `sessions/index.jsonl` 有新记录。
- 对应会话目录有 `requests.jsonl`。
- 工具任务产生 `tool-calls.jsonl`。
- `model` 和 `upstream_model` 都写入日志。
