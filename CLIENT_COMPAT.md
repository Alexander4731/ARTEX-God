# LLM 客户端兼容

在「系统 → LLM → 编辑模型配置」启用 **模拟官方客户端**。
默认关闭；每个渠道单独保存，保存后立即生效。

- Anthropic：使用 Claude Code 的 User-Agent、`x-app`、Stainless 平台标识、客户端请求/会话 ID 和 `claude-code-20250219` beta 标识；保留现有 beta 标识。
- OpenAI / Responses：使用 Codex 的 User-Agent、`originator`、请求 ID 和 `session_id`。Codex 渠道建议选择 Responses API。
- 同一 ARTEX 会话使用稳定的 UUID，不同会话分开；每次 HTTP 请求生成独立请求 ID。自定义会话头仍优先使用用户配置。
- 模型列表、连接测试、聊天、任务及旁路问答均使用该设置。测试时可以直接切换开关，不必先保存。

兼容逻辑直接在现有 HTTP 请求层设置请求头，不启动额外进程、不缓冲 SSE、不改写提示词或工具调用，也不修改鉴权方式。
这是 HTTP 客户端标识兼容，不提供 CLI 的 OAuth 登录、TLS 指纹或私有请求协议；OpenAI Chat Completions 不会自动转换成 Responses。
身份版本集中固定在 `agent/client_compat.go`：Codex 0.167.0、Claude Code 2.1.316。

## 验证

- Go 测试覆盖三种协议的开关、鉴权/正文保留、会话隔离和即时 SSE。
- 隔离 PostgreSQL 测试覆盖新增/更新、保留密钥、DTO 和运行时配置，以及测试接口中显式关闭覆盖已保存的开启状态。
- TypeScript 检查、静态前端构建及嵌入前端的 Windows Go 构建通过。
- 真实 Responses 兼容渠道直连验证通过：模型列表、连接回复 `OK`、流式工具调用与工具结果续聊。
- 部署后通过页面保存并重新打开，确认开关状态正确持久化。
