# Pojun 第三轮源码评估：运行数据、运维与前端一致性

日期：2026-11-12。ARTEX 源码基线 `edf7c99`（前两轮文档提交 `938d83e`）；Pojun 固定为 `dc9b83ea125bb54c69dd38be5c29c26d8e83b811`（v0.4.63）。实施决策以 [方案 v2](POJUN_INTEGRATION_PLAN.md) 为准；前序依据见 [第一轮](POJUN_SOURCE_REVIEW.md)、[第二轮 E01–E18](POJUN_SOURCE_REVIEW_EXTENDED.md)。

**本轮最需要先处理的是 ARTEX 自身的归档与执行恢复衔接，其次是工作区系统目录保护。** Pojun 的通知路由、意图用量归属和缺失引用处理值得借鉴，但均有需要适配的边界。报告补写不能直接塞进现有通用 Agent 触发队列。

## 范围与证据等级

扩大检查到 traffic、notify、llmrec、mcphttp、selfupdate、config、enrich、evidence、sidequestion、归档/恢复、工作区接口、任务列表/旁路问答请求、CI/发布。按模块名与品牌字符串归一后，选定目录的文件清单共 585 项：246 相同、170 有差异、95 仅 Pojun 存在、74 仅 ARTEX 存在，包含测试。**这是差异筛选数，不代表逐行审完 585 个文件，也不是全仓覆盖率。**

清单与只读差异报告保存在忽略目录 `.local/pojun-wide/`，没有应用差异文件。下面标注“静态确认”表示能从函数和约束直接核对；“待复现”表示尚未通过专门测试测出实际影响。没有运行外部扫描、自检登录、代理探测、真实通知或升级操作。

| 编号 | 结论 | 实施映射 |
| --- | --- | --- |
| O01 | 通知显式路由，补齐 Transport 兜底守卫 | A25 |
| O02 | 意图级用量有价值；角色聚合存在列名错误，统计口径需统一 | A03/A15/A23 |
| O03 | thinking 兼容必须覆盖旁路摘要、压缩和 judge | A05 |
| O04 | 缺失引用可部分接收，但必须区分索引不存在与正文损坏 | A17 的独立子项 |
| O05 | 工作区应保护系统管理的数据，保留 ARTEX 的 os.Root | A26 |
| O06 | ARTEX 归档遗漏执行回执及恢复历史，存在级联删除链 | A27，最高优先 |
| O07 | 保留 ARTEX 的资产集合归档、范围恢复和 ID 重映射 | A27 兼容验收 |
| O08 | 代理池可选；现成实现的失败回退和并发归属不宜照搬 | X04 |
| O09 | 升级响应限额值得引入；私有发布认证和 Windows 流程应保留 | A28 |
| O10 | 把已有发布测试前移到 PR，保留嵌套 Norma 测试 | A28 |
| O11 | 列表请求竞态两边都有；旁路问答已有 epoch/sequence 防护 | A24 |
| O12 | 保留 Windows 证据发布；缩短锁时间须同时解决 GC 竞争 | A17/A21 |
| O13 | trigger_runs 是 Agent 会话队列，报告需独立业务状态 | A22，修正 v1 |

## O01：通知路由与 Transport 兜底

依据：[Pojun notify/http.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/notify/http.go)，本地 [notify/http.go](notify/http.go)。

静态确认：ARTEX 注释声明通知直连且不复用目标流量代理，但 Clone(DefaultTransport) 仍继承 ProxyFromEnvironment；Pojun 显式设置 Proxy=nil。两边在 DefaultTransport 不是 *http.Transport 的兜底分支都直接返回新 Transport，没有装 blockInternalDial；默认运行不一定触发该分支，不能夸大成所有通知都无守卫。

建议让通知路由选择明确可查，构造 Transport 时所有分支都装相同拨号守卫、超时与重定向检查。保留当前局域网 SMTP/IM 场景和 ARTEX_NOTIFY_ALLOW_LOCAL 的语义。升级时显式保留依赖环境代理的旧模式，直连作为新配置默认；不复用目标代理池。环境代理模式下 DialContext 检查的是代理连接，不能宣称能验证代理端最终解析的目的地址。

notify 目录归一后 25 项相同、仅此文件不同。投递重试、脱敏与已有历史存储不值得整套重写。

## O02：用量归属与聚合错误

依据：[llmrec/llmrec.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/llmrec/llmrec.go)、[db/llm_usage.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/db/llm_usage.go)；本地 [llmrec/llmrec.go](llmrec/llmrec.go)。

Pojun 把 session 后缀 i<n> 解析为 intent_id，能回答“哪个意图消耗了多少”。但 TrimPrefix 后 Atoi 不能证明后缀真的以 i 开头，也不能验证任务、探索、意图关系。优先沿现有 WithTaskID/WithWorker 增补类型化调用上下文；旧 session 仅作严格格式回退，不能作为写入授权。

静态确认：UsageByWorkerTask 查询 SUM(cache_read_tokens)，建表及 ALTER 使用的实际列为 cache_read；该方法按仓库声明的 schema 调用会出现不存在列错误，未用 PostgreSQL 运行复现。TokenByIntent 无条件相加 input+cache_read+cache_write，又与不同协议 input 是否含缓存有关。不能直接用该结果做预算停止条件。

A03 先统一协议用量事实和 intent/role/attempt 归属；A15 再定价。llmrec 是计量事实来源，activity/harness 的展示统计不能二次入账。真实第二次请求是新 attempt；一次请求的重复事件、入库重试不是新收费调用。旧归档聚合缺少模型维度时标 unknown，不补造历史成本。

## O03：thinking 兼容不只在 Worker

依据：[sidequestion/context.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/sidequestion/context.go)；本地 [旁路摘要](sidequestion/context.go)、[压缩](agent/compaction.go)、[judge](server/intercept.go)。

静态确认：Pojun 取消旁路摘要请求里硬编码的 Thinking:"disabled"；ARTEX 三处仍显式传 disabled。因此只改 taskLLMRuntime 的默认 provider 不足以覆盖请求 override。需要调用目的、profile 参数和请求参数的有效值契约及同一单次降级规则。

不能全局打开 thinking，也不能对任意 400 重试。明确参数拒绝、尚无输出/工具执行时才可降级一次。Pojun 的 IsSummaryRequest 用系统提示词完全相等识别测试请求，这适合测试辅助，不应升级成运行时识别身份的机制。

## O04：缺失引用与正文损坏必须分开

依据：[Pojun evidence/store.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/evidence/store.go)、[traffic/evidence.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/traffic/evidence.go)。

Pojun 逐个调用 ReadEvidence；sql.ErrNoRows 放进 SkippedRefs，其余错误终止。这能保留正确引用，但缺失原因的判定过宽：readEvidence 查询 exchanges 不存在会返回 sql.ErrNoRows，查询 exchange_bodies 不存在且无 legacy 路径也会返回同一错误。**有索引却没正文是存储不完整，不应被当成模型输错 ID 跳过。** 这是静态错误分类缺口。

拟在读取层引入明确的 missing-index 结果；正文缺失、哈希错误、权限失败、取消均为硬错误。旧接口默认严格；新入口明确选择部分接收，持久保存缺失引用与完整性状态，并在返回值、详情和导出中可见，不靠一条日志。全缺失不能变成“证据齐备”。

目前 ARTEX 的批量 ReadEvidence 内部也逐 ID 查询。优化应是有界分块读索引/正文元数据，保持现有读取期间防删除保护，再流式复制文件；不是简单把 Pojun 的外层 for 循环换个位置。批量上限、查询次数和锁时间需测量。

## O05：保护系统数据，保留路径能力边界

依据：[Pojun workspace.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/workspace.go)、[workspace_protected_test.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/workspace_protected_test.go)；本地 [workspace.go](server/workspace.go)、[路径隔离测试](server/workspace_test.go)。

Pojun 给 traffic/evidence/nuclei-templates 增加写入、上传、mkdir、删除限制，思想值得引入。其注释部分仍写“只拦删除”，实际 handler 已拦多种写操作；不能只读注释。任务/sessions/transcripts 没有进入名单。

ARTEX 已有 os.Root、限量读取和 multipart 临时文件清理；这些实现需要保留。当前限制“不能越出工作区”并不等于“不能破坏工作区内系统文件”。新策略需要覆盖 traffic、evidence、archives、transcripts（含 execution-receipts）及实际由服务拥有的配置/密钥/会话文件；任务工作目录里的用户产物不能被一刀切成只读。

Pojun 的绝对路径字符串前缀判断不能替代 ARTEX 的 Root，也不足以处理 Windows 大小写、junction 和工作区内部别名。落地时系统存储与用户可写目录使用明确能力根；不能用一次 EvalSymlinks 检查后普通 os.Open 宣称消除了竞态。普通文件操作保留，系统数据管理走相应业务接口，服务内部写入不经过通用文件管理器。

## O06：本地归档遗漏执行恢复数据

直接证据：[db/task_archives.go](db/task_archives.go)、[db/task_archives_restore.go](db/task_archives_restore.go)、[db/execution_recovery.sql](db/execution_recovery.sql)、[server/task_archive_package.go](server/task_archive_package.go)、[intercept/execution.go](intercept/execution.go)。

静态链路可复核：

1. SnapshotTaskArchive 的表清单包含 intercept_pending，未包含 tool_execution_receipts、tool_execution_events、execution_recoveries、execution_recovery_decisions。
2. 四张表的 approval_id 均引用 intercept_pending(id) ON DELETE CASCADE。
3. CompleteTaskArchive 在包写入后执行 DELETE FROM intercept_pending；因此关联的回执、事件和决策行随之删除，而包内未保存。
4. 文件 staging 只取 tasks/<id> 及 transcripts 下 exp<exploration>- 前缀；spool 实际在 transcripts/execution-receipts/<approval_id>，没有匹配。
5. RestoreInterceptRows 只能恢复审批快照，不能重建缺失的权威回执/历史。spool 重放还要求已有 receipt 的 run_id/tool_use_id 完全匹配，不能凭剩余文件保证自动恢复。

**这是本地新增恢复能力与旧归档的兼容缺口，不是 Pojun 已替我们解决的功能。** 当前未执行归档复现，也没有证据表明用户某条历史已受影响。task_execution_policies/task_tool_policies 依靠热库 task stub 可能仍在，但归档包不自包含这些策略，需明确保留规则。

A27 先在隔离 fixture 复现，再补格式版本、表顺序、spool 身份清单及恢复验证。含新执行数据的包必须声明必需能力，旧 reader 明确拒绝，不能假装兼容。归档时排空已管理写入并撤销恢复领取；仍有不确定写入时保留屏障和状态，不能借归档洗成成功。恢复清除旧租约，保留决策历史及版本，不自动重放工具、不扩大任务策略。

## O07：资产集合归档应以 ARTEX 为准

依据：[Pojun task_archives_restore.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/db/task_archives_restore.go)，本地同名文件和 [task_archives.go](db/task_archives.go)。

相较 ARTEX，Pojun 没有 asset_collection_members 的独占资产删除保护、task_scope 的资产 ID 重映射，以及 collection_name/value 快照恢复和集合缺失后的范围保留处理。它不具备本地这套集合语义，不能整段移植归档源码。

A27 和后续附件生命周期测试必须包含：集合被多个任务共用、集合已删除、资产去重造成 ID 变化、任务继承依赖、来源任务只读、finding 留存。恢复成功不能只看任务列表重新出现。

## O08：代理池只作为条件模块

依据：[pool.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/traffic/pool.go)、[traffic.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/traffic/traffic.go)。

静态确认：池为空或全部熔断时 Pick 返回 nil，上层回退到单代理，未配置单代理则直连；lastPick 按 host 记录最近出口，同 host 并发请求可能把结果算给另一出口；hostPick/lastPick 没有容量期限；random 实际用轮询计数器。SetPool 在锁外读取旧 entries；但当前 SetProxyPool 每次先新建 selector，不能据此断言生产路径已触发同实例并发重载。当前可以确认的问题是：新 selector 没有旧 entries，注释声称的熔断状态继承在该调用链不生效，保存配置会丢失旧状态。未来若复用 selector，需先修正锁边界并跑 race 测试。

ValidateProxyURL 改善主机检查，但只检查端口是否为数字，未限制 1–65535；错误中插入 raw URL，可能含 userinfo。可以先独立修正现有单代理校验和错误脱敏，不必引入轮换。

X04 只在确有多出口使用需求时做：请求持有 route_id/config_revision，结果按请求归因；会话身份固定、连接池按出口隔离；无可用出口返回可解释的 route_unavailable，不能隐式直连。配置用不可变快照，亲和映射有界；只对连接层可归因故障熔断，不能把目标 4xx/5xx 全算成代理坏了。默认 off，不影响通知、模型或发布下载。

## O09：升级边界，保留私有发布和便携运行

依据：[github.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/selfupdate/github.go)、[bundle.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/selfupdate/bundle.go)、[stage.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/selfupdate/stage.go)。

Pojun 给 Release JSON 加 4MiB LimitReader，值得变成准确的 max+1 限额检查，拒绝超大或尾随第二份 JSON。tag 的 v 前缀兼容也有价值，但目前主要服务 hostd 指定版本升级；ARTEX 暂无对应调用方，不先新增无用途 API。将来只有 404 才尝试另一种 tag，不能因认证错误/限流/取消再次请求。

Pojun 不含 ARTEX 的 setUpdateAuth、API asset downloadURL 与私有仓库错误语义；不能拿它替换本地 selfupdate。包内取首个同 basename 成员的歧义在两边都存在，可独立改为唯一合法候选校验；必须在创建暂存目标前完成判定。无需为单二进制升级引入多组件 manifest 平台。

ReplaceBinary 是多步 rename，不能把函数注释“原子”当成多组件事务保证。保留 ARTEX Windows 启动/更新/回退逻辑、同盘 staging、SHA256 与实际便携包 smoke，不引入 hostd/systemd。

## O10：PR 检查前移，复用现有验证

依据：[Pojun ci.yml](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/.github/workflows/ci.yml)、[release.yml](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/.github/workflows/release.yml)；本地 [release.yml](.github/workflows/release.yml)、[Windows smoke](.github/workflows/windows-portable-smoke.yml)。

Pojun 有 push main / pull_request 的构建、vet、PG 测试和前端静态导出。ARTEX 在 release 中已有 PG 全量、Norma harness/tool race、前端类型/导出和 Windows 便携流程，但该工作流由 tag 触发。真正增量是测试提前到 PR，不是移植 Linux-only 的发布矩阵。

复用检查 job/命令，PR 权限 contents:read，不传发布令牌；保留根模块与嵌套 Norma 分开测试。并行 job 独立数据库，单库包间保持已验证的 -p 1。发布前所有必需检查通过；第三方 Action 升级与本次功能变更分开审查。

## O11：前端请求一致性，避免重复建设

依据：[Pojun tasks/page.tsx](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/web/src/app/%28main%29/function/tasks/page.tsx)、[use-side-questions.ts](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/web/src/hooks/use-side-questions.ts)；本地 [旁路 hook](web/src/hooks/use-side-questions.ts)。

两边任务列表都用 setInterval + fetch + JSON.stringify 比较响应；相等只是不 setState，并没有减少请求或避免慢请求覆盖新请求。归档列表还依赖中文错误文案判断“归档不存在”。A24 应增加 signal、同 key 单在途/请求序号、变更后失效和稳定错误 code；已发起的写请求不能按 GET 策略自动重试。

旁路问答已具备 parent epoch、单条 sequence、提交幂等 ID 和 EventSource 清理，应该保留，优先补隐藏页降频及重叠 history 请求控制。仅请求序号不足以让较旧的 HTTP 快照覆盖较新的 SSE 状态，仍需业务版本比较。Pojun 去掉 withCredentials 不是本地需要的优化；继续用 HttpOnly cookie/session epoch，不恢复 URL 或 localStorage bearer token。

## O12：证据发布和锁时间

依据：本地 [evidence/store.go](evidence/store.go)、[publish_windows.go](evidence/publish_windows.go)、[db/finding_traffic.go](db/finding_traffic.go)，Pojun 的 evidence/store.go 见 O04。

ARTEX 在 Windows 用 MOVEFILE_WRITE_THROUGH 发布已 sync 文件；Pojun 改用 Rename 并跳过 Windows 目录 sync，不替换本地实现。当前 WithEvidenceTx 持有数据库全局证据 advisory lock，Record/Bind 在锁内读流量、复制正文，GC/导出也走该锁。这保证了现有一致性，但慢文件复制可能放大等待；实际程度待基准测量。

方案先保持锁语义完成 O04；如实测等待超预算，再将文件复制移至私有 staging，提交阶段在锁内校验拥有关系/版本并发布引用。不能简单把 publish 挪到锁外：GC 可能把新文件当无引用文件回收。A21 同样复用现有 StageFindingsExport 的冻结/私有复制机制，避免再造快照持有体系。

## O13：报告队列的复用边界

依据：本地 [db/trigger_queue.go](db/trigger_queue.go)、[trigger_queue.sql](db/trigger_queue.sql)。

静态确认：trigger_runs 必填 agent_key/title/message，状态为 pending/queued/running/done/failed/interrupted；ClaimTriggerRuns 不按 job_kind 过滤，StartTriggerConversation 会创建 Agent 会话，RecoverTriggerRuns 对全部 queued/running 记录改状态。它不是已有的通用租约任务表。

因此撤销 v1“给 trigger_runs 加报告元数据即可复用”的方案。A22 使用同一 PostgreSQL 中的小型 report_recovery_jobs/attempts 业务表，借鉴现有事务领取、管理后台生命周期和版本 CAS；不引入新调度平台，不把只读报告工作误送到通用 Agent。只有两个实际调用方出现相同数据库原语时才提取小函数，不强改旧队列状态机。

## 本轮取舍与下一步

A01–A24 保留编号，新增 A25–A28；X01–X03 保留，代理池新增为 X04 条件模块。O02/O03/O04/O11 的改进并回原条目，不为每个字段新增一整套模块。config、mcphttp、enrich 和 traffic/archive.go 等归一后相同的实现记录为“无需引入”；重复检查发现同源代码不算新增收益。

优先验证并修复 A27 的归档数据链，A26 的管理目录边界；通知路由、参数校验、模型协议及 UI 小修复独立交付。Word 基础导出不用等价格表/分类体系；observe 不用等 Word；持久压缩缓存和转换队列待测量及真实产品需求成立后再加。实现时直接修改源码，不应用补丁或 monkey patch。

本轮仅新增评估与优化设计，未修改应用源码或运行配置；性能提升、并发风险和归档影响仍按上述证据等级呈现。
