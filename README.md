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
```

启动 ADK console：

```bash
go run . console
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

图片和严格 JSON Schema 属于协议级支持，最终能否使用取决于所选服务商和模型。

## 更换服务商

只需修改 `.env` 中的以下三项，无需改 Go 代码：

```dotenv
BASE_URL=https://你的兼容端点/v1
API_KEY=你的密钥
MODEL_NAME=模型名称
```

`.env` 已加入 `.gitignore`，请勿将真实密钥提交到 Git。

## 验证

所有自动测试都使用本地模拟服务器，不消耗 API 配额：

```bash
go test ./...
go test -race ./...
go vet ./...
```
