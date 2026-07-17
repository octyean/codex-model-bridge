# Verification

## 环境

- Go 版本以 `go.mod` 为准。
- 真实 smoke 需要本机 Codex CLI、已运行的 Bridge，以及一个已配置的第三方模型别名。

## 静态检查

```bash
rtk go vet ./...
rtk git diff --check
```

## 测试

```bash
rtk go test ./...
rtk go test -race ./...
```

## 构建与配置

```bash
rtk go build -o /tmp/codex-bridge-local ./cmd/codex-bridge
rtk go run ./cmd/codex-bridge config check --config "$HOME/.codex-bridge/config.toml"
```

## 真实 Codex

```bash
rtk scripts/real-codex-smoke.sh "$CODEX_BRIDGE_SMOKE_MODEL" continuity
```

验证后检查：

```text
~/.codex-bridge/logs/sessions/<thread_id>/prompt-requests.jsonl
~/.codex-bridge/logs/sessions/<thread_id>/prompt-responses.jsonl
~/.codex-bridge/logs/sessions/<thread_id>/bridge-responses.jsonl
~/.codex-bridge/logs/sessions/<thread_id>/tool-calls.jsonl
~/.codex-bridge/logs/sessions/<thread_id>/recoveries.jsonl
```

确认任务完成后才返回最终消息，Bridge 内部的 `codex_bridge_task_end` 没有出现在 Codex 输出中。

## 数据与界面

- 本项目不依赖数据库。
- 本项目没有需要浏览器验收的可见 UI。
