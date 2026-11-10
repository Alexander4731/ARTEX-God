## 0.3.19

### 本次改进

- LLM 配置新增「模拟官方客户端」开关，默认关闭，各渠道独立保存；Anthropic 使用 Claude Code 兼容请求头，OpenAI / Responses 使用 Codex 兼容请求头。
- 模型列表、连接测试、聊天、任务及旁路问答统一应用该选项。表单中的开关可直接用于测试，显式关闭可覆盖已保存的开启状态。
- 直接使用现有 HTTP 请求层，无需额外中转服务。保留原鉴权、提示词、工具调用和 SSE 实时输出；会话标识稳定且不同会话相互隔离。
- 增加请求头、流式输出、数据库持久化及接口配置覆盖测试。真实 Responses 渠道的模型列表、连接测试、流式工具调用和工具结果续聊验证通过。

该选项提供 HTTP 客户端标识兼容，不包含 CLI 的 OAuth 登录、TLS 指纹或私有协议；Codex 渠道建议选择 Responses API 格式。详见 [客户端兼容说明](https://github.com/Alexander4731/ARTEX-God/blob/0.3.19/CLIENT_COMPAT.md)。

### 下载与升级

- Windows x64 便携包内置 PostgreSQL、Node.js、Python、Chrome 和运行库；解压到可写目录后双击 `start.bat`。支持中文和带空格的路径。
- Linux / macOS 分别提供 amd64 与 arm64 压缩包，包含应用、前端、技能及启动脚本；这些包需要自行配置 PostgreSQL 和工具依赖。
- 升级前备份并保留 `config.json`、`data` 和 `jwt.key`。数据库自动增加兼容开关字段，已有配置默认关闭。
- 所有安装包均由 GitHub Actions 构建；Windows 便携包通过隔离 PATH、中文路径、首次启动及重启测试后发布。`SHA256SUMS` 提供附件校验值。
