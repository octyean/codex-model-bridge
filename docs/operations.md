# 管理命令

下面的 `codex-bridge` 可以替换成实际二进制路径，例如 `~/.codex-bridge/bin/codex-bridge`。

## 配置检查

```bash
codex-bridge config check --config config/config.toml
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
