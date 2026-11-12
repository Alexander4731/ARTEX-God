# Pojun 源码对比与选择性吸收

日期：2026-11-12。

后续扩展：[18 项新增源码评估](POJUN_SOURCE_REVIEW_EXTENDED.md)；完整实施设计：[引入、优化与兼容方案](POJUN_INTEGRATION_PLAN.md)。

结论：有值得吸收的更新，适合按功能移植。当前先直接修复了三项可独立验证的问题；模型兼容、证据预览和报告交付是下一批最有价值的候选。Pojun 的版本号较高不代表每个子系统都优于当前 ARTEX。

## 对比基线与方法

- ARTEX：`3a82997`（包含 0.3.20 执行回执以及之后的六项恢复修复）。本次改动叠加其上。
- Pojun：`dc9b83ea125bb54c69dd38be5c29c26d8e83b811`，v0.4.63，2026-11-10。
- 两仓库源代码有明显继承关系，但当前 Git 历史没有共同祖先，不能依赖整分支 merge 判断适用性。
- 对 agent/server/db/guard/llmpool/evidence/docpreview 和前端 components/lib 做模块名、品牌名归一化，清单中有 125 个新增、129 个变更、224 个一致文件。数量只覆盖这些目录，且包含测试；不是逐行审计了整个仓库或全部 749 个提交。
- 以下结论核对了实现、调用位置和相关测试；已吸收的生命周期问题在本地隔离 PostgreSQL 中先复现失败，再修改源码验证。
- Pojun 的完整 go.mod 使用 `replace github.com/Autumn-27/norma => github.com/chu0119/Norma v0.4.4`。只看 require 的 v0.4.3 会误判。已下载并比较该依赖源码与我们的 `third_party/norma`，没有改动依赖解析。
- 两仓库 LICENSE 均为 AGPL-3.0。下列链接固定到对比提交，保留变更来源。

## 本次已直接改进源码

### 1. 所有终态任务重新启动时重置上一轮时钟

Pojun 参考：[goals.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/goals.go)、[manager.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/manager.go)、[任务恢复测试](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/task_revival_deadline_test.go)。

ARTEX 原先仅在 `timeout → running` 时清理 `first_run_at/deadline_at` 和 Engine 的 settling/deadline/stamped/coordStarted。`done` 或 `failed` 任务追加工作后仍持有旧预算；再次调度时可能立即到期或无法重新盖章。

本次把同一套清理扩展到 `done/failed/timeout`，数据库更新继续使用原有状态条件和原子提交，内存清理在提交成功后执行。进入 FIFO 之前即清理旧时钟，新预算仍从下一次实际运行开始。普通暂停/继续保持原有绝对截止时间。

源码：`server/manager.go`、`server/goals.go`、`server/engine_timeout.go`。未移植 Pojun 根据文本重开 canceled 意图的逻辑，继续保留本地执行回执、未知执行结果屏障及用户停止语义。

### 2. 任务获准运行后持久化 running

Pojun 参考：[启动准入](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/goals.go)、[Engine.Run](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/engine.go)。

ARTEX 的新任务可能已经被调度，数据库仍为 `created`，界面再根据运行状态推导为 running，形成两套不一致的状态。

本次在 `Manager.ApplyTaskAdmission` 的同一次提交中处理 `created && !queued → running`，覆盖首次准入、FIFO 晋升以及已经存在 Engine 循环的旧 created 任务。仍在排队、尚未获准运行的新任务保留 created。不采用 Pojun 在 Engine.Run 中先读内存、再单独写状态的做法，避免额外覆盖并发暂停或完成状态。

源码：`server/manager.go`。此前三条路径均可在隔离数据库测试中复现失败。

### 3. 内嵌前端明确缓存策略

Pojun 参考：[webui_embed.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/webui_embed.go)。

HTML 入口、图标、路由回退使用 `Cache-Control: no-cache`；存在于 `_next/static/` 的非 HTML 构建资源允许一年 immutable 缓存。

本次依据实际返回的文件决定缓存策略。Pojun 是先根据请求路径中是否包含 `_next/static/` 设置响应头；缺失旧资源回退到首页时，会把首页也标为一年 immutable。本次避免该问题，同时保留现有路由回退行为。

源码：`server/webui_files.go`，`server/webui_embed.go` 负责注入内嵌文件系统。测试覆盖入口、路由、真实构建资源、旧资源缺失和嵌套伪匹配路径。

## 其他值得吸收的候选

| 优先级 | 候选与源码 | 价值 | 适配要求 / 当前判断 |
| --- | --- | --- | --- |
| 高 | Norma v0.4.4：`llm/openai.go`、`types.go`、`accumulate.go`、`harness/query.go`、`noaadapter/project.go` | 保留 Gemini 工具调用 thought_signature，减少多轮工具调用被模型接口拒绝 | 需要全链路带签名，并验证并行调用索引、历史恢复和跨 provider 切换。不能只替换 llm 包或覆盖整个 harness，否则丢失执行回执。尚未合入。 |
| 高 | Norma v0.4.4：`llm/anthropic.go`、`Usage.MergeEcho` | 兼容 message_start 输入用量为零、直到 message_delta 才报告真实用量的网关 | 要同时验证原生接口不重复计数，以及各流事件消费者的累计方式。尚未合入。 |
| 高 | Norma v0.4.4：`tool/helpers.go`、`task.go`、`shell_session.go` | 完整输出使用绝对路径，截断不切坏 UTF-8 字符；有 capture/utf8_boundary 回归用例 | 适合从上游逐项移植到已固定的 Norma 源码，保留本地生命周期观察器及嵌套工具执行入口。尚未合入。 |
| 高 | [thinking_fallback.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/thinking_fallback.go)、[task_llm.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/task_llm.go)、[provider.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/agent/provider.go) | 某些模型拒绝 disabled thinking 时，通过重建 provider 后有界重试恢复 | 当前 ARTEX 没有同类兜底。需保留显式配置和计费记录，收紧错误识别；仅凭 `2013)` 匹配可能误认其他错误。流已经输出内容后不能随意重放。尚未合入。 |
| 高 | [artifact_data_preview.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/artifact_data_preview.go)、[artifact-data-viewer.tsx](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/web/src/components/artifact-data-viewer.tsx) | JSON/CSV/JSONL/文本在线查看，gzip 内容和 ZIP 清单预览 | ARTEX 已有证据与流量链路，但没有这一整套任务附件表和展示。宜复用现有证据归属与下载鉴权；补齐限额截断标志、UTF-8/大整数保真以及不一致行的显示。尚未合入。 |
| 中高 | [report_export.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/report_export.go)、[report 目录](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/report) | 在 ARTEX 已有 Markdown/CSV 导出之外提供 Word、证据图片、复测记录和资产可读名，便于交付 | 必须先修正全局资产附录问题，默认排除 risk_accepted 也应成为可选择的导出策略。报告内容继续按当前证据版本校验。尚未合入。 |
| 中 | [zoomable-image-dialog.tsx](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/web/src/components/zoomable-image-dialog.tsx) | 证据图片缩放、拖拽、重置，依赖少 | 可独立接入现有证据查看入口；需补键盘/触屏交互并实测滚轮事件。尚未合入。 |
| 中 | [SeedPromptVersioned](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/db/config.go)、[prompt_seed_test.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/db/prompt_seed_test.go) | 内置提示词随版本更新，用户编辑版本保持不动 | 当前 ARTEX 只在空白时播种。方向有价值，但启动读取和 SavePrompt 之间存在编辑竞态，需事务内锁定并复查 current_prompt_id；不宜顺便全局下发 Pojun 提示词。尚未合入。 |
| 中 | [技能索引](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/assembly.go)、[agent assembly](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/agent/assembly.go) | 系统提示可见已绑定技能的一行说明，描述缓存一分钟 | 当前 ARTEX 已有 Skill 工具与绑定加载；这项改善发现入口。需限制索引长度、处理禁用/解绑后缓存和描述来源。尚未合入。 |
| 中 | [compaction.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/agent/compaction.go) | 压缩失败退避、大片节点分块、有界并发 | 可减少重复失败开销。但默认 65536 输出 token、20 分钟、每块 6 节点来自特定模型经验，不宜全局照搬；保留我们的受管理后台任务及取消链路。源码中的“成功子块保留”注释不等于具备跨次运行持久缓存。尚未合入。 |
| 中 | [mcp_failure_cache.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/mcp_failure_cache.go) | MCP 连接失败后冷却，减少反复启动失败进程 | 需要按配置版本而非仅名称缓存，配置修复/改名/手动重试应立即失效，并保留失败可见性。尚未合入。 |
| 中 | [tool_outputs.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/agent/tool_outputs.go) | 用实际工具输出检查报告引用是否有出处，默认 warn | 是辅助检查，不是事实证明：内存台账只有本轮最近 200 项、单项 16 KiB，旧回执/附件中的证据可能不在其中，短证据会跳过。应绑定持久证据 ID/版本，先只提示。尚未合入。 |

Norma 对比来源：[chu0119/Norma v0.4.4](https://github.com/chu0119/Norma/tree/v0.4.4)。

## 不宜原样引入的机制与具体问题

### 报告自动补写需要接入已有持久队列

[report_recovery.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/report_recovery.go) 启动后 45 秒扫描，此后每 15 分钟检查；只选最近 7 天且创建超过 20 分钟的空报告，每批最多 20 个，最多尝试 6 次。

补漏思路有用，但当前实现有这些边界：

- SQL 只排除已删除任务，没有任务暂停条件，也没有与进行中的 reporter 按 finding ID 原子去重。
- 尝试次数和时间分两次写 settings，错误被忽略；随后才尝试启动 reporter。模型未就绪时 `StartTriggeredRun` 可以直接返回，仍已消耗一次尝试。
- 最新 20 条先查出来再检查重试上限，已耗尽重试的条目可能长期占据候选页，较旧的缺报条目无法被选中。
- 7 天以前的缺报不会恢复；空报告原因被提示词统一说成重启/崩溃，实际未必如此。
- [Pojun 的 StartTriggeredRun](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/conversations.go) 把工作加进内存切片。ARTEX 已使用 `db/trigger_queue.go` 持久化入队，不能退回内存实现。

建议在 ARTEX 现有队列上补充 finding ID、证据版本和去重键，原子领取、确认入队再计数，支持按任务暂停/手动重试。此次未开启任何全局补写后台任务。

### 空转和自动收尾需要重新定义进度

[idlestop.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/agent/idlestop.go) 的主要计数来自 PostToolUse，每次工具执行算一轮，**不是每次模型回复算一轮**。三分钟宽限、3 次无写回提示、5 次无写回取消，在并行读工具较多的任务里可能过于激进。写回次数也不代表新信息，重复写入同样可能重置计数。

[auto_finish.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/auto_finish.go) 默认全局启用，最大规划轮数默认 80，达到上限即使仍有活动意图也会请求收尾。数据库读取设置失败时还会回落到默认启用。不能当作无行为变化的性能优化合入。建议先记录观察数据，后续做任务级可配置策略，并让取消结果进入执行回执链路。

### 任务 Word 报告附录会查询全局资产

[exportTaskDocx](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/report_export.go) 的 findings 按任务过滤，但是资产计数和附录调用 `s.m.Assets().QueryByType(...)`。继续核对 [db/assets.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/db/assets.go)，该函数仅按 type 过滤，没有 task_id 条件。因此多任务实例里可能混入其他任务资产。这是源码路径确认的问题，必须修正后才能移植报告导出。

### 范围守卫故障时放行

[scopeGuardError](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/agent/tools.go) 在分类查询出错时返回空串；[FindForeignAssetForTask](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/db/task_scope_guard.go) 将所有查询错误都返回成“未命中”，没有区分 sql.ErrNoRows 与数据库故障。它可以作为归属检查思路，但不是可靠的强制边界。移植必须保留错误，默认阻断未能确认的写回，并覆盖不存在的资产 ID。

### 登录与会话管理不应覆盖本地实现

[auth.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/auth.go) 加入登录限制和换密码轮换 JWT key，但 ARTEX 已有按实际连接 IP 限流、容量上限、过期清理、数据库 session epoch、HS256/subject/expiry 校验、HttpOnly cookie 和来源检查。

Pojun 的实现信任未验证的 X-Forwarded-For/X-Real-IP；少量失败来源的 map 项没有完整过期回收；换 key 落盘失败时旧会话继续有效。整文件移植会丢失我们已有的防护。安全响应头可另行结合嵌入预览和当前来源策略评估。

### 压缩死锁不是当前 ARTEX 同款缺陷

Pojun 的 [compaction_deadlock_test.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/agent/compaction_deadlock_test.go) 针对新增退避逻辑里“持锁 tryStart 调用再次持锁 cooldownFor”的自死锁。当前 ARTEX 直接读固定 cooldown，没有这次递归加锁。其修复值得在未来引入退避时参考，不能把该事故写成当前 ARTEX 已复现的问题。

## 推荐后续顺序

1. Norma 输出路径/UTF-8 修复、用量计数、Gemini 签名、thinking 参数兼容，逐项保留执行观察器并做模拟接口测试。
2. 任务附件在线预览、图片缩放；再引入按任务隔离的 Word 导出。
3. 在已有持久队列上补报告恢复、内置提示词版本迁移和 MCP 失败冷却。
4. 收集进度数据后再考虑空转与自动收尾，配置保持任务级。

未纳入此次移植：品牌/模块/环境变量重命名、Linux hostd/安装升级脚本、默认上下文扩到 1M、新攻击工具和相关预置策略。它们属于独立产品范围，不能随基础可靠性修复一起带入。

## 验证与运行状态

- 失败复现：`.local/pojun-review-red.log`，三条 created 准入路径和 done/failed 时钟重置失败。
- 定向验证：`.local/pojun-review-green.log`，生命周期、暂停保留预算、并发终态保护、静态缓存测试通过。
- 最终 `go test -p 1 ./... -count=1` 在全新隔离数据库 `pojun_review_full` 通过，覆盖 server/db/agent/intercept 等全部根模块包；日志 `.local/pojun-review-full.log`。此轮包含 done/failed/timeout 的立即准入及 FIFO 准入、暂停继续保持预算等测试。
- `go build ./...` 通过；`go build -tags embedui -o .local/artex-pojun-review.exe ./cmd/artex` 通过。
- 本次未改 TypeScript，不重复前端依赖安装/构建；内嵌构建复用本工作区已有静态产物。未运行真实模型、外部目标或 Pojun 的运行服务。
- `git diff --check` 通过。
- 源码直接修改，没有生成 .patch 文件、运行时替换或修改线上数据库。
- 当前运行服务仍是已发布的 0.3.20。本次改动和此前 `3a82997` 后续修复尚未发布部署；全局配置、任务策略和其他任务未改变。
