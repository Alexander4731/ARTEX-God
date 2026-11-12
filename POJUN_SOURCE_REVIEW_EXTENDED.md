# Pojun 扩展源码评估

日期：2026-11-12。ARTEX 基线：`edf7c99`；Pojun 基线：`dc9b83ea125bb54c69dd38be5c29c26d8e83b811`（v0.4.63）。

后续补充：[第三轮运行数据、运维与前端一致性评估 O01–O13](POJUN_SOURCE_REVIEW_OPERATIONS.md)。实施方案已更新为 v2，修正队列复用边界并调整依赖。
本轮补充第一轮的 [源码评估](POJUN_SOURCE_REVIEW.md)，重点扩大到上下文查询、可观测性、数据契约、配置迁移、前端兼容、宿主诊断、凭据和情报模块。实现顺序和兼容设计统一见 [引入方案](POJUN_INTEGRATION_PLAN.md)。本轮是源码分析和方案设计，没有将下列候选实现或其默认配置装进运行服务。

## 新增的 18 个评估项

“移植”指保留其主要实现并适配；“改写”指借鉴机制，在 ARTEX 现有架构实现；“可选”不在基础版本默认启用。

| 编号 | 源码依据 | 当前 ARTEX 与增量价值 | 取舍 |
| --- | --- | --- | --- |
| E01 | [agent/tools.go: workerOverviewData](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/agent/tools.go)、[worker.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/agent/worker.go) | 当前 Worker 调用 graphOverviewData 构建完整图；专用 Worker 快照可省去冷图、全血缘、覆盖查询等无关计算 | 高优先改写。Pojun 仍读最多 1000 条 facts/findings 再计数、取前 10/20 条；需 SQL 聚合与真正有界查询，保留约束、来源任务和恢复信息。 |
| E02 | [worker.go: workerToolOutputChars](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/agent/worker.go)、[tools.go: clampIntentSummary](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/agent/tools.go) | 上下文长度有独立控制价值；当前运行信息不应被海量正文占满 | 改写。按字节/估算 token 定义预算，摘要限额仅作用展示；完整命令、意图 detail 和证据仍保存。避免把字符数等同 token 数或直接裁掉约束。 |
| E03 | [guard/guard.go: AttributionStats](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/guard/guard.go)、[task_llm.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/task_llm.go) | ARTEX 有 Guard 计数和执行记录，Pojun 增加最近样本并给 Planner 使用 | 高优先改写。以 execution_id/intent_id 和结构化原因汇总；区分平台阻止、审批等待、环境故障和目标响应，避免看到 blocked 就认定是 WAF。 |
| E04 | [engine_guard.go: repeatTracker / intentBudgetWatcher](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/engine_guard.go) | 同意图重复调用和软预算提示有助于减少空耗 | 机制可用，实现需重写。默认提示，不自动取消；只有稳定、可比较的读操作才考虑去重，轮询和有副作用工具不得套用“同输入同输出”。 |
| E05 | [server/cost.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/cost.go)、[db/llm_usage.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/db/llm_usage.go) | ARTEX 已有常开用量台账，但缺少可靠的价格版本与成本视图 | 高优先改写，复用台账。精确 profile/model 定价、缓存费率、价格时间区间、未知价格明确缺失，按 task/agent/intent/profile 分析。 |
| E06 | [input_validation.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/input_validation.go)、[server_mgmt.go: pgSaveMCP / pgSaveProfile](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/server_mgmt.go) | 当前部分 enum 直接进数据库，坏输入会变成 500；前置校验可让用户直接修正配置 | 适配移植。校验后使用归一化结果；REST 和 agent 配置工具复用同一校验，保持旧客户端缺省字段行为。 |
| E07 | [db/asset_dsl.go: buildLeaf](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/db/asset_dsl.go) | 当前 `service_name==""` 只匹配空串，遗漏 NULL；Pojun 已补齐 | 优先移植并补数据库行为测试。单独定义空值等值，保持 `=` 的模糊匹配和现有 `!=` 行为，别顺带改 DSL 全部运算符。 |
| E08 | [db/vulnclass.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/db/vulnclass.go) | 同义类别会分散统计和检索；稳定分类有用 | 改写为规范键与原文并存。Pojun 将多种注入合为一个大类，不能作为精确指纹或直接重写历史证据；CWE 与自由文本独立保存。 |
| E09 | [hosttools/hosttools.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/hosttools/hosttools.go)、[agent/envcaps.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/agent/envcaps.go)、[proxyprobe](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/proxyprobe/proxyprobe.go) | 启动时告知实际工具和环境能力，减少重复失败；诊断代理配置更直观 | 仅引入只读诊断。Linux capability 说明不能代替 Windows 权限/可执行文件/运行时探测；不捆绑 hostd 自动安装。 |
| E10 | [web/app/error.tsx](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/web/src/app/error.tsx)、[global-error.tsx](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/web/src/app/global-error.tsx)、[media-query.ts](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/web/src/lib/media-query.ts)、[json.ts](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/web/src/lib/json.ts) | ARTEX 无这两个错误边界，多个组件直接调用现代媒体查询监听；老 WebView 有具体风险 | 优先适配。cloneJson 仅限 JSON 数据；根边界少依赖，错误详情按需展开；兼容检查不等于支持任意古老浏览器的全部 Next/CSS 能力。 |
| E11 | [intercept_builtin_migration.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/db/intercept_builtin_migration.go)、[config.go: SeedPromptVersioned](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/db/config.go) | 内置项有版本、用户修改可保留，能避免升级“代码改了但用户收不到” | 借鉴版本归属，重写为事务与完整旧值指纹。不得引入 Pojun 将全局 deny 改 judge 的内容迁移。 |
| E12 | [guard.go: JudgeRuleWithTimeout](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/guard/guard.go)、[server/intercept.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/intercept.go) | 规则显式交给模型判断、规则级超时语义有产品价值 | 条件引入。当前规则格式、UI 和失败策略一起升级，保存规则 ID 及有效配置快照；配置失败不能静默放行。 |
| E13 | [agent/tools_diff.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/agent/tools_diff.go) | 本地响应结构差分可复用已捕获证据，减少重复请求和肉眼比较 | 引入纯离线核心，输入为当前任务两份证据引用；差异仅表示观察事实，不推断越权或可枚举。URL 请求分支不在该批。 |
| E14 | [db/task_auth.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/db/task_auth.go)、[server/authcheck.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/authcheck.go) | 任务级登录配置、凭据可用性反馈有价值 | 可选，需显著改写。任务凭据引用与普通 DTO 分离；检查走任务取消、范围、代理和流量记录；200 登录页不能当“已认证”。 |
| E15 | [vulnintel/link.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/vulnintel/link.go)、[sync.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/vulnintel/sync.go)、[ratelimit.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/vulnintel/ratelimit.go)、[ghsa_range.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/vulnintel/ghsa_range.go) | NVD/KEV/GHSA 只读情报、页级断点、共享限速、倒排候选检索有用 | 可选独立批。先做数据查询和弱线索，不自动上报 finding 或运行模板；版本解释按生态区分，无法比较返回 unknown。 |
| E16 | [server/artifact_autobind.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/artifact_autobind.go)、[db/task_artifacts.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/db/task_artifacts.go) | 给未关联证据提供归属建议，减少整理成本 | 改写。同主机不是同一漏洞的证明；execution/intent 关系为强依据，文本主机交集仅建议。人工归属不被后台覆盖。 |
| E17 | [server/skill_tags.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/skill_tags.go)、[db/task_playbook_seed.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/db/task_playbook_seed.go) | 技能可检索、模板可按环境/阶段筛选，提升发现入口 | 适配元数据结构，复用现有任务模板。预置内容不自动启用，不复制全部技能和提示词硬尾巴。 |
| E18 | [engine.go: runPlannerRound](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/engine.go)、[engine_timeout.go](https://github.com/chu0119/Pojun/blob/dc9b83ea125bb54c69dd38be5c29c26d8e83b811/server/engine_timeout.go) | 超时和晚到结果的生命周期边界需要统一；现有 ARTEX 已有收尾、删除屏障，但只清内存标志不足以证明旧轮写入无害 | 作为本地可靠性增强单独设计 run_generation 和受管理协调器；先加故障注入测试。未声称已在线复现。 |

## 对代码优雅和效率有直接影响的核查

### 1. repeatTracker 不能原样拷贝

`observe` 先 `CompareAndSwap(cur, next)`，随后才 `next.blocked++`。发布的对象已能被其他 goroutine 读取；CAS 不保护发布后的普通字段写。这里不需要复杂的无锁算法：小型 mutex 保护一次有界记录，或在 CAS 前构造完整不可变值即可。该判断来自静态并发路径，本轮未运行 race 检测器，不写成“race 测试已证实”。

`json.Compact` 只去掉空白，不排序对象键；`{"a":1,"b":2}` 与相反键序会得到不同 hash。更关键的是相同参数的动态查询、轮询和外部操作不一定返回相同内容。优化重点应是给出重复证据和原因，不是自动阻断一切重复调用。

### 2. 专用 Worker 快照需要同时减少读取量和正文量

Pojun 去掉了 planner 专用字段，却仍把最多 1000 个 fact/finding 节点加载为 Go 对象，用 len 当总量。超过限额时计数也不再是真实总量。当前 ARTEX 的完整图还会读取血缘和冷区。改进应直接落到查询投影：准确 count、有界 recent 列表、相关依赖 ID、受限继承摘要；缺少数据要显式标记，不能把 SQL 故障当作“没有事实”。

### 3. 成本是派生数据，用量台账是事实来源

Pojun 用 `strings.Contains` 匹配静态价表，用 float64 累加；命名重叠、代理别名和未来改价会影响结果，也没有按缓存读写分别计价。ARTEX 已有 input/output/cache_read/cache_write 和真实调用归属，无需再加第二套计数。应用可编辑价格版本，金额定点计算，历史按当时价格解释；未知价不得显示为免费。

### 4. 内置迁移的“用户修改保护”需要以 SQL 为准

Pojun 的注释称修改 pattern 或停用不会受影响；实际条件只对有 oldRulePatterns 记录的部分规则比较 pattern，且没有统一比对 enabled、priority、target/type 等完整旧值。它也逐条提交后才写完成标志。可保留“识别未修改默认项”的思路，但本地设计必须用稳定 builtin_key、完整旧值 fingerprint、同一事务和版本记录；对无法确认来源的旧项只提出更新建议。

### 5. 凭据 API 的隐藏字段不覆盖所有秘密路径

TaskAuthConfig 对 Password/TOTPSecret 使用 json:"-"，但 Headers/ExtraFields 也可能包含秘密。`runAuthCheck` 自建 `http.Client`、未接受 context，默认跟随重定向，以 2xx/3xx 为缺省成功条件。直接接入会形成第二套出网和认证判定逻辑。方案保留集中配置的价值，凭据解析只在执行时进行，结果用 authenticated/unauthenticated/unknown 三态。

### 6. 证据自动关联的实现弱于注释

artifact_autobind.go 注释说结合“锚定资产域名”，实际循环提取的是 finding.Name/Summary/Evidence，没有在此处读取 asset_ids 对应资产。对附件则从 name/note/source_url/storage 文本提取，恰好命中一个 finding 就写关联。误关联会影响后续报告；本地首先用真实执行来源、任务归属和显式证据引用，域名只做人工可复核的建议。

### 7. 情报索引与版本匹配应解耦

`link.go` 的 token/别名倒排适合召回候选，不能当产品身份确认。例如通用 http/http_server 词不能证明某个厂商组件。`ghsa_range.go` 是自实现的三段式版本比较，不能统一解释 npm、Maven、PEP 440、RPM/Debian 等生态。收集来源与版本原文、保留解析失败原因、使用精确生态标识后再判断影响区间。

### 8. “超时返回”不等于停止执行

Pojun 用 goroutine 包装 planner.Plan，超时让外层返回；内部若不响应取消，仍可能运行甚至晚到写回。ARTEX 应保留已有 beginTaskOperation/inflight/删除屏障，并用 run_generation 拒绝上一轮的任务状态写入。执行回执晚到仍必须接收，这是与普通任务状态写入不同的处理。该项要用确定性交错测试验证，不能仅依据一次普通全量测试承诺解决。

## 现有能力优先保留

ARTEX 已有的执行生命周期回执、持久触发队列、任务局部权限、数据库证据版本、来源任务隔离、会话失效、受管理后台任务和 Windows 便携发布链路是集成基座。Pojun 新模块不得反向替换这些链路。

本轮没有把全部新网络工具列为基础必选项；C2/监听器/SSH 中继、自动安装宿主软件、自动模板扫描、整套预置攻击流程属于独立扩展。继续保留手动复测入口，不因为引入报告补写而自动发起新的目标请求。

## 本轮验证范围

完成了上述源文件和关键调用位置的静态核对，以及当前 ARTEX 对应实现的对照。没有运行 Pojun 服务、真实模型、登录检查或外部目标；性能阈值是下一阶段的验收标准，还不是已测得的改进结果。文档中的新增接口、字段和组件均为拟实施设计；上一轮已落地的三项修复仍以原评估和测试记录为准。
