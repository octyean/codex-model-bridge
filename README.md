# codex-bridge

`codex-bridge` 是给 Codex CLI / Codex App 用的模型桥。

它把 DeepSeek、Kimi、Qwen、Mimo 这类 OpenAI-compatible 模型接到 Codex 里，让它们能继续用 Codex 给 GPT 预留的原生能力：`apply_patch`、`web_search`、`tool_search`、`local_shell`、`function`、`custom`、图片输入、模型目录和 reasoning 配置。

```text
Codex CLI / App
  -> codex-bridge /v1/responses
  -> OpenAI-compatible upstream
  -> model
```

## 它能做什么

- 让第三方模型在 Codex 里调用 `apply_patch`，能创建、修改、删除文件。
- 让 `web_search`、`tool_search`、`local_shell`、`function`、`custom` 这些 Codex 原生能力继续可用。
- 让 Codex App 识别模型目录里的 `display_name`、上下文窗口、工具能力和图片能力。
- 自动读取上游 `/models`，把可请求的模型补进 Codex 模型目录。
- 让 text-only 模型也能接图片：先读图，再把内容转成文字交给模型。
- 让 OpenAI 原生 GPT / o 系列模型走原生 `/responses`，保留 `reasoning` 和相关参数。
- 让不同模型按自己的脾气走不同 profile，减少工具调用和补丁格式的毛刺。
- 安装时探测上游 `/responses` 和 `/chat/completions` 流式能力，自动选择更合适的协议。

## 快速安装

Linux / macOS：

```bash
curl -fsSL https://raw.githubusercontent.com/octyean/codex-model-bridge/main/scripts/install.sh | bash
```

非交互安装：

```bash
curl -fsSL https://raw.githubusercontent.com/octyean/codex-model-bridge/main/scripts/install.sh | \
  env CODEX_BRIDGE_BASE_URL="https://api.deepseek.com" CODEX_BRIDGE_API_KEY="sk-xxx" bash
```

安装脚本会下载二进制、询问或读取上游 URL/API key、探测流式协议、创建 `~/.codex-bridge/config.toml`、写入 Codex provider，并注册用户级服务。重复执行同一条命令即可更新；已有配置不会被覆盖，也不会重新要求填写上游或探测上游。
脚本只补充 `codex_bridge` provider 和模型目录，不会覆盖你已有的 Codex 默认模型。

更换上游时显式执行：

```bash
curl -fsSL https://raw.githubusercontent.com/octyean/codex-model-bridge/main/scripts/install.sh | \
  env CODEX_BRIDGE_REPLACE_UPSTREAM=1 CODEX_BRIDGE_BASE_URL="https://api.example.com/v1" CODEX_BRIDGE_API_KEY="sk-xxx" bash
```

Windows 用户可从 [GitHub Releases latest](https://github.com/octyean/codex-model-bridge/releases/latest) 下载对应 exe，放到固定目录后双击运行。首次运行会在 exe 同目录创建 `config.toml`。

更多安装方式见 [安装与运行](docs/installation.md)。

## 配置样子

配置文件会由安装向导生成。手写配置时，核心结构是 provider 绑定上游，model 绑定 provider：

```toml
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

如果要让 text-only 模型在 Codex App 里上传图片，需要让模型入口声明图片输入，并开启视觉 provider：

```toml
input_modalities = ["text", "image"]
```

这表示 bridge 会接收图片并转成文本，不表示上游模型原生支持图片。

完整配置见 [配置指南](docs/configuration.md)。

## 常用命令

```bash
codex-bridge config check --config ~/.codex-bridge/config.toml
systemctl --user restart codex-bridge.service
curl -sS http://127.0.0.1:8787/health
```

更多命令见 [管理命令](docs/operations.md)。

## 文档

- [安装与运行](docs/installation.md)
- [配置指南](docs/configuration.md)
- [管理命令](docs/operations.md)
- [排障](docs/troubleshooting.md)

## 排障入口

Codex 里看不到模型、请求返回 401、图片上传被拦、`apply_patch` 格式异常、`--search` 没效果，这些问题都放在 [排障](docs/troubleshooting.md)。
