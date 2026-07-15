# ADK Go v2 OpenAI-Compatible Demo

无需 clone ADK Go 仓库即可体验 `google.golang.org/adk/v2`。项目包含一个面向 OpenAI Chat Completions 协议的 adapter，可连接 DeepSeek、火山方舟、阿里云百炼等兼容端点。

## 环境要求

- Go 1.25+
- 一个 OpenAI-compatible API Key

## 快速开始

项目已创建本地 `.env`，填写其中的 `API_KEY`：

```dotenv
BASE_URL=https://api.deepseek.com/v1
API_KEY=你的密钥
MODEL_NAME=deepseek-v4-flash
CONTEXT_WINDOW=1000000
MAX_TOKENS=384000
THINKING_MODE=auto
# REASONING_EFFORT=high
```

启动项目自带的 Web Workbench：

```bash
go run . web
```

浏览器打开 `http://localhost:8080`。这个界面使用固定、可审计的产品组件呈现会话、流式回答、模型思考、工具调用与人工确认，不再加载 ADK 自带 Web UI。若只需要终端交互，仍可运行 `go run . console`。

Web Workbench 默认使用 SQLite 将会话和聊天事件保存在用户目录的 `~/.adk/sessions.db`，重启进程后仍可继续访问。可以通过参数指定其他位置，或临时切回仅内存存储：

```bash
go run . web --session-db /path/to/sessions.db
go run . web --session-db=
```

可以尝试：

```text
只用一句话介绍 ADK Go。
现在 Asia/Shanghai 是几点？请调用工具，不要自己计算。
```

`MAX_TOKENS` 是单次生成的最大输出 token 数。如果服务商不接受 `384000`，请按该模型的限制调低；这不影响 adapter 的上下文处理。

## 已支持

- 多轮 system/user/assistant/tool 消息
- 普通与流式文本响应
- function tools、并行 tool calls 和工具结果回传
- 图片 URL 与 base64 图片
- JSON Object 与严格 JSON Schema 输出
- temperature、top-p、stop、penalty、seed、max tokens
- reasoning content（保存在 ADK `CustomMetadata`，不会混入最终答案）
- usage、finish reason、取消传播和敏感信息脱敏
- 本地 shell/CLI 执行与结构化结果

图片和严格 JSON Schema 属于协议级支持，最终能否使用取决于所选服务商和模型。

## 更换服务商

只需修改 `.env` 中的以下三项，无需改 Go 代码：

```dotenv
BASE_URL=https://你的兼容端点/v1
API_KEY=你的密钥
MODEL_NAME=模型名称
```

`.env` 已加入 `.gitignore`，请勿将真实密钥提交到 Git。

## Thinking / Reasoning

Adapter 支持 OpenAI-compatible Chat Completions 返回的 `reasoning_content`。Web Workbench 会把它作为可折叠的“模型思考”展示，思考和之后的正式回答都会按照服务商 chunk 实时流式输出：

```bash
go run . web
```

`THINKING_MODE` 支持：

- `auto`（默认）：不发送服务商私有 `thinking` 参数，但只要响应包含 reasoning 就展示；
- `enabled`：发送 `thinking: {"type":"enabled"}`；
- `disabled`：发送 `thinking: {"type":"disabled"}`。

`REASONING_EFFORT` 可留空，或设置为 `high`、`max`。`THINKING_MODE=disabled` 时不能同时设置 effort。

带工具调用的 assistant reasoning 会通过 `reasoning_content` 原样回传，支持 DeepSeek thinking → tool call → tool result → thinking/answer 的多步流程。不返回 reasoning 的服务商保持普通流式回答行为。

> **隐私提示：** 模型思考是服务商返回的输出，不保证是完整或忠实的内部计算审计记录，其中可能包含提示词、工具计划或其他敏感上下文。不要将这个本地开发界面暴露给不受信任的访问者。

## Project Agent Skills

项目启动时会自动发现当前项目下的 `.agents/skills/<skill-name>/SKILL.md`，并使用 ADK Go v2 官方 Skills Toolset 完整预加载。当前版本只读取项目级目录，不读取用户级 `~/.agents/skills/`。

目录示例：

```text
.agents/skills/
├── concise-writer/
│   └── SKILL.md
└── follow-builders-lite/
    ├── SKILL.md
    ├── references/
    ├── assets/
    └── scripts/
```

ADK 会向模型提供三个标准工具：

- `list_skills`：列出可用 Skill；
- `load_skill`：按需加载完整 `SKILL.md` 指令；
- `load_skill_resource`：按需读取 `references/`、`assets/` 或 `scripts/` 中的资源。

这遵循渐进式披露：初始请求只包含 Skill 的名称和描述，完整指令不会在每轮对话中全部塞入 prompt。Skills 在进程启动时形成内存快照，修改文件后需要重启程序。

可以尝试：

```text
请使用 concise-writer 精简这段文字：由于目前这个功能在现阶段仍然处于尚未完全完成的状态，因此我们暂时还不能立即发布。
使用 $follow-builders-lite 生成一份中文 AI builders 摘要。
```

如果 Skill 要求执行 `scripts/...` 或其他 CLI，Agent 会调用现有的 `run_command`，并将 `working_directory` 设为对应的 `.agents/skills/<skill-name>`。Skill 激活和本地命令执行都不要求确认，也没有沙箱、白名单或 `allowed-tools` 强制检查；请只运行你信任的项目 Skills。MCP 工具仍然独立执行人工确认。

`skills-lock.json` 属于安装元数据，`agents/openai.yaml` 属于客户端展示元数据；当前运行时都不会读取它们。

## MCP 工具

项目启动时会自动读取根目录的 `.mcp.json`，将其中的 Streamable HTTP 或 stdio MCP server 转换为 ADK Toolsets。配置结构参考并兼容 Claude Code 项目级 `.mcp.json` 的 HTTP/stdio 子集；每一个 MCP tool 在真正执行前都会要求人工确认，拒绝确认不会执行工具。

可以从安全模板开始：

```bash
cp .mcp.example.json .mcp.json
```

Streamable HTTP 配置：

```json
{
  "mcpServers": {
    "example": {
      "type": "http",
      "url": "https://mcp.example.test/mcp",
      "headers": {
        "Authorization": "Bearer ${MCP_HTTP_TOKEN}"
      }
    }
  }
}
```

stdio 配置：

```json
{
  "mcpServers": {
    "local-example": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "YOUR_MCP_SERVER_PACKAGE"],
      "env": {
        "SERVICE_TOKEN": "${SERVICE_TOKEN}"
      },
      "cwd": "."
    }
  }
}
```

`command` 是可执行文件，`args` 是参数数组；程序不会用 shell 拼接或解释它们。`env` 可选，会继承当前进程环境并覆盖同名变量。为了兼容 Claude Code 的旧式 stdio 配置，有 `command` 时可以省略 `type: "stdio"`。

配置值支持 Claude Code 风格的环境变量展开：`${VAR}` 要求变量存在，`${VAR:-default}` 在变量未设置或为空时使用默认值。展开适用于 `command`、`args`、`env`、HTTP `url` 和 `headers`。本项目额外支持可选的 `cwd`，也可使用相同的变量展开语法；相对路径从程序启动目录解析。

连接在 Agent 首次获取工具列表时懒创建，因此启动程序本身不会连接 HTTP server 或启动 stdio 子进程。stdio 子进程的启动属于工具发现，不会额外弹出确认；之后每次实际工具调用仍会确认。不同 server 应避免暴露同名 tool。

Claude Code 将项目级 `.mcp.json` 设计成可提交的共享配置，并在首次执行项目 MCP server 前要求用户信任。本项目目前还没有这层“信任项目配置”确认，因此 `.mcp.json` 仍然加入 `.gitignore`，仓库只提交 `.mcp.example.json`。程序不会打印 headers、stdio command/args、完整 query 参数或原始 MCP 配置。stdio server 是以当前程序用户的权限运行的本地进程，请只配置你信任的命令和包。

格式参考：[Claude Code MCP 文档](https://code.claude.com/docs/en/mcp)。

Console 和 Web Workbench 都会加载同一份 MCP 配置：

```bash
go run . console
go run . web
```

## 本地 Shell / CLI

Agent 内置 `run_command`，通过当前 `$SHELL` 执行非交互命令。它支持管道、重定向和环境变量，也可以调用 PATH 中已经安装的 Bash、Python、Node.js、npm/npx、Go 及其他 CLI。

可以尝试：

```text
请调用 run_command，执行 printf 'hello from shell'。
请调用 run_command，执行 python3 -c 'print(6 * 7)'。
请调用 run_command，分别显示 node 和 go 的版本。
```

`working_directory` 为空时使用程序启动目录；相对路径从该目录解析，也允许绝对路径。命令不提供 TTY 或密码输入界面。Python、Node.js 等运行时必须已安装并位于 PATH 中。

> **安全警告：** `run_command` 不要求人工确认，不使用沙箱或白名单，并继承当前进程的文件、网络和环境权限。模型可以修改或删除文件、安装依赖、访问网络或启动其他进程。只应在你信任模型服务、提示内容和项目文件时运行本示例。

stdout 和 stderr 分开返回；非零退出码不会被隐藏。每个输出流最多保留 64 KiB 的开头和结尾。Ctrl-C 或请求取消会终止命令及其子进程。

## 验证

所有自动测试都使用本地模拟服务器，不消耗 API 配额：

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```
