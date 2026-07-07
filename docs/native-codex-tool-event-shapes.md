# Codex 原生工具事件形态采样

本文记录 2026-07-07 在本机用真实 `codex exec -m gpt-5.5` 采到的 Codex 原生工具形态。采样没有手写 HTTP 请求，也没有项目测试用例；每条都是独立 Codex 会话。

采样工作目录：`/tmp/codex-native-tool-samples`

## 原生工具表

inventory 会话：`019f3c1d-3028-7e43-a2cc-4d7e683c69eb`

`gpt-5.5` 原生 Responses 请求里的 `tools` 包含：

| 类型 | 名称 |
| --- | --- |
| `function` | `exec_command` |
| `function` | `write_stdin` |
| `function` | `list_mcp_resources` |
| `function` | `list_mcp_resource_templates` |
| `function` | `read_mcp_resource` |
| `function` | `update_plan` |
| `function` | `request_user_input` |
| `custom` | `apply_patch` |
| `function` | `view_image` |
| `function` | `get_goal` |
| `function` | `create_goal` |
| `function` | `update_goal` |
| `tool_search` | tool discovery |
| `web_search` | web search |

本机这套原生工具表里没有本地 `read_file`、`list_files`、`search_files` 一等工具。它们是 Bridge 给第三方 Chat 模型增加的舒适层，最终必须投影回 Codex 已有的原生 item。

## 采样结果

| 能力 | 会话 | 原生 Responses output item | CLI/App 可见 item | 结论 |
| --- | --- | --- | --- | --- |
| 命令执行 | `019f3c22-30bd-7fb3-a44c-301b5bb6f78d` | `function_call`，`name=exec_command` | `command_execution` | 原生命令执行就是 bash/command 卡片。 |
| tool search | `019f3c27-8ea6-7183-9b92-30b31c915090` | `tool_search_call` | CLI 不单独显示工具卡，结果进入后续上下文 | 只能作为工具发现回合，不是文件读取卡片。 |
| 列目录，不准 shell | `019f3c22-98b9-7793-99a4-76296ccf92c1` | 多次 `tool_search_call`，随后 `function_call list_mcp_resources`，再尝试外部 MCP | `mcp_tool_call`，最终 assistant message | 原生 GPT 没找到非 shell 的本地列目录工具。 |
| 读文件，不准 shell | `019f3c23-ab84-7ae1-bca9-fafe2e2e433a` | 多次 `tool_search_call` / MCP 尝试，最后 `function_call exec_command` | 多个 `mcp_tool_call`，最后 `command_execution` | 原生可成功读文件的路径仍是命令执行。 |
| 搜文件，不准 shell | `019f3c25-8ca3-7f40-b039-884ea1f602be` | 多次 `tool_search_call` / MCP 尝试 | 多个失败 `mcp_tool_call`，最终 assistant message | 原生未找到非 shell 的本地文件搜索工具。 |
| MCP resource list/template | `019f3c29-8268-7e92-82a9-f387330904a1` | `function_call list_mcp_resources` / `list_mcp_resource_templates` | `mcp_tool_call` | MCP resource 三件套通过 Codex 原生函数触发 MCP 卡片。 |
| MCP resource read 失败样本 | `019f3c29-e329-7bc2-82d1-00ce044937e0` | `function_call read_mcp_resource` | 失败 `mcp_tool_call` | 本机没有可读资源；不能把本地文件 URI 冒充 MCP resource。 |
| update plan | `019f3c29-13c8-76a1-bf34-f8a444bbee03` | 两次 `function_call update_plan` | `todo_list` | 计划更新是独立 UI item，不应塞进 assistant 正文。 |
| apply patch，多次编辑同文件 | `019f3c27-de53-72a3-be72-8bacc37b1941` | 两次 `custom_tool_call name=apply_patch` | 两次 `file_change` | 文件编辑必须保持 `custom_tool_call -> file_change`，不要转成 shell。 |
| web search | `019f3c2b-0e39-7501-9320-2646e683b0a6` | `web_search_call` | `web_search` | web search 是原生 hosted item。 |
| write stdin | `019f3c2b-7e1f-7b20-bd07-256763c95716` | `function_call exec_command`，后续 stdin 合并进命令输出 | `command_execution` | CLI 上没有单独的 `write_stdin` 卡片，输入回显在同一命令输出里。 |
| request user input | `019f3c2c-61d6-7621-a0a3-3be93e1a2710` | 未产生有效 tool call | assistant message：Default mode 不可用 | Default mode 不应向 Chat fallback 暴露该工具。 |
| goal tools | `019f3c2b-f074-7703-b58f-2302cd80f023` | 未产生有效 tool call | assistant message | 这次未触发成功，不能作为渲染样本。 |
| view image | `019f3c2e-74c6-7fc0-b3f6-b7ece1f27799` | 未产生有效 tool call | assistant message | 模型没有实际调用 `view_image`；只确认工具表存在。 |

## Bridge 对齐规则

Bridge 给第三方 Chat 模型暴露的是“好用的逻辑工具”，但返回给 Codex Harness 时必须落回 Codex 已有原生 item。

| Chat 侧逻辑工具 | Codex 原生投影 | App 预期展示 |
| --- | --- | --- |
| `read_file` | `function_call name=exec_command` | `command_execution` |
| `list_files` | `function_call name=exec_command` | `command_execution` |
| `search_files` | `function_call name=exec_command` | `command_execution` |
| `write_file` / `replace_text` / `insert_text_at_line` / `insert_text_after_match` / `move_file` / `delete_file` | `custom_tool_call name=apply_patch` | `file_change` |
| `tool_search` | `tool_search_call` | 工具发现结果进入后续上下文 |
| `codex_context_resource` 的 MCP 行为 | `function_call name=list_mcp_resources/list_mcp_resource_templates/read_mcp_resource` | `mcp_tool_call` |
| `update_plan` | `function_call name=update_plan` | `todo_list` |
| `web_search` | `web_search_call` 或 Bridge 内部 web proxy | `web_search` 或内部结果回灌 |

目前不能做的事：把 `read_file/list_files/search_files` 伪造成不存在的原生 “Read file/List files” 卡片。原生工具表没有这类工具，硬造 response item 只会让 Codex Harness 无法执行或产生假能力。

## 已对齐和待观察

已对齐：

- 文件编辑保留 `apply_patch -> file_change`。
- 计划更新保留 `update_plan -> todo_list`。
- MCP resource 保留原生 MCP 三件套，不把本地路径伪装成 MCP resource。
- `read_file/list_files/search_files` 保留 Chat 侧舒适工具，但返回 Codex 时投影到原生 `exec_command`。
- Bridge 日志记录逻辑工具到原生 `exec_command` 的投影，方便从 session 反查。

待观察：

- Codex App 未来如果原生加入本地 `read_file/list_files/search_files` item，Bridge 应改为投影到新原生工具，不再借 `exec_command`。
- `view_image` 和 goal tools 本轮未拿到有效调用样本，后续有真实触发后再补充。
- 外部 MCP tool 的显示由 Codex MCP 卡片负责，Bridge 只做工具/资源边界翻译，不维护硬编码能力画像。
