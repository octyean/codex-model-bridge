# 排障

## Codex 里看不到 bridge 模型

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

[model_providers.codex_bridge.auth]
command = "/home/you/.codex-bridge/bin/codex-bridge"
args = ["auth", "token", "--config", "/home/you/.codex-bridge/config.toml"]
```

缺少配置时执行：

```bash
codex-bridge codex configure --config config/config.toml
```

如果想自动展示上游模型，确认：

```toml
[model_discovery]
enabled = true
mode = "merge" # 或 "upstream"
```

服务启动日志里应出现 `model_discovery_completed`。如果上游 `/models` 不可用，bridge 会继续使用手写 `[models.*]`，并在日志里写 `model_discovery_failed`。

## 请求返回 401

Codex 会通过 `[model_providers.codex_bridge.auth]` 调用：

```bash
codex-bridge auth token --config ~/.codex-bridge/config.toml
```

这条命令输出的值必须和 bridge 服务读取到的 `codex.local_token` 一致。如果改过 `codex.local_token`，重新执行：

```bash
codex-bridge codex configure --config ~/.codex-bridge/config.toml
```

## 服务启动时报配置权限错误

Unix 上配置文件需要限制权限：

```bash
chmod 600 config/config.toml
```

Windows 不按 Unix 权限位检查。

## 端口被占用

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

## 图片上传被 Codex 拦住

Codex CLI / App 会先看模型目录里的 `input_modalities`。如果模型只声明 `["text"]`，请求不会进入 bridge，第三方视觉 provider 也不会触发。

给对应模型加：

```toml
input_modalities = ["text", "image"]
```

然后重启 bridge 或手动刷新模型目录：

```bash
codex-bridge catalog generate --config config/config.toml
```

如果上游模型本身不支持图片，继续保持原来的 `profile`，不要改成 `openai`。bridge 会用 `[capabilities.vision]` 配置的视觉 provider 把图片转成文本。

## `apply_patch` 生成了错误格式

确认模型目录里该模型的：

```json
"apply_patch_tool_type": "freeform"
```

并确认 Codex 使用的是 bridge 生成的模型目录。DeepSeek adapter 已经对 `apply_patch` 提示和输入归一做了处理。模型偶发输出不合规 patch 时，可以让 Codex 重试当前工具调用。

## 工具调用没跑起来

先看 bridge 配置里的 `profile`，以及生成模型目录里的 `apply_patch_tool_type`。

- `profile = "deepseek"`：适合 DeepSeek。
- `profile = "default"`：适合普通 OpenAI-compatible 模型。
- `apply_patch_tool_type = "freeform"`：让 Codex 把 `apply_patch` 当成自由格式补丁来传。

如果模型目录写对了，工具还是没出现，再看 bridge 日志里有没有 `request_started`、`tool_call_translated` 或 `catalog_written`。

## `--search` 没有效果

检查三处：

1. `capabilities.search.enabled = true`
2. `capabilities.search.providers` 至少有一个可用 provider
3. Codex 请求里带了 `web_search` 或 `web_search_preview`

bridge 会把 Codex 的 `web_search` 转成同名 Chat function tool。工具名保持为 `web_search`，模型更容易按预期调用搜索。

## Windows 双击后没有看到 bridge 模型

打开 PowerShell，进入 exe 所在目录后手动运行：

```powershell
.\codex-bridge-windows-amd64.exe config check --config .\config.toml
.\codex-bridge-windows-amd64.exe --config .\config.toml
```

这样可以看到完整错误信息。常见原因是端口被占用、`api_key` 仍是占位值，或 Codex 配置没有写入成功。
