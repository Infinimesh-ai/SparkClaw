# 邮件链路诊断与修改边界

> 语言：简体中文 | [English](../../docs/email-diagnostics-and-change-control.md)

## 基线与目的

本文作为邮件排障的统一入口：当前局限、缺少的证据、每次修改具体补哪个缺口。
详细证据保留在原事故记录；后续更新本文的局限和修改表，不再另堆一份按时间追加的发布流水账。

[当前阶段稳定化方案](application-runtime-stabilization-plan.md)规定本阶段目标、实施顺序，
以及需求提高后再设计的内容。本文保留该方案各步骤引用的邮件具体缺口、修复边界和修改卡，
不另立一套路线。

审计：**2026-10-26，Asia/Shanghai**，源码 `c2b85311`。
仓内[发行清单](../../vendor/app-cli/release.json)固定 App-CLI `.41` / `bcc55506`；
[验收记录](desktop-browser-follow-up-2026-10-23.md)最新留存证据仍是 `.39` 运行、`.41` 未激活。
本轮没有读取实机；这是历史观察，不是新的健康检查。

**实施状态：S1–S4 source_verified（第 7–10 节），S5 未授权；D05/D09 延期。** 没有真实发信、部署、接口或 UI 变更。

## 1. 先看完整链路

| 边界 | 已有证据 | 必须区分 |
|---|---|---|
| 原撰写 UI / Agent → 准入 | 草稿、冻结快照、send key、invocation；适用时有 agent run | 意图/准入与服务商派发 |
| Desktop → ISCP → Gateway | RPC/request ID、执行回执、可选耗时 | 传输成功与邮件业务成功 |
| Gateway → Controller → App-CLI executor | invocation/task、lifecycle、任务事件 | 本地采集与发信；执行状态与效果确定性 |
| Host → 已登录页面 → 原生请求 | Host phase/code/reason、服务商结果/证明 | 点击/进程退出与通过验证的服务商效果 |
| 发送结果 → Store finish → ACK/清理 | 回执、草稿状态、lifecycle ACK、清理诊断 | 业务完成、持久化、ACK、资源清理 |
| 监听/轮询 → 发现 → 采集 | 邮箱/checkpoint、job、同步失败/历史、原件清单 | 收到通知与合格范围内完整采集 |
| 解析/入库 → 分析 → 发布 | source/capture/job/revision、模型/分析证据 | 原件字节、解析正文、派生结果 |
| API/对象传输 → 原有 UI | 邮件/附件引用、展示状态 | 已入库与实际展示/下载字节 |

报告分别表达 **执行结果、业务结果、证据完整性、清理结果、ACK 结果**，不替换现有状态机。
`unknown` 必须保留操作种类与原身份；采集 unknown 不等于发信 unknown，不能为补证据重发不确定邮件。
`not_sent` 仍需既有机制认可的否定证明。发送回执、独立 Sent 原件、收件方到达、自动入库、
正文/附件字节、原界面交互各自验收。

## 2. 源码已确认的局限

这些是源码事实，不代表已经证明它们造成每一次历史事故。行号变化后按文件/函数定位。

| 编号 | 源码发现 | 准确修复边界 |
|---|---|---|
| D01 (S2 source_verified) | [`page_intake.go`](../../services/gateway/internal/emailmanagement/page_intake.go) 的 `commitListFailure`（523–539）不写 `InvocationID`；成功提交（426）有。 | 失败历史携带真实失败 invocation；区分调用前失败、采集失败、拆分查询失败，不能伪造未发生调用；不改 checkpoint/重试语义。 |
| D02 (S3 source_verified) | [`compose.go`](../../services/gateway/internal/emailmanagement/compose.go)（275–282）把多数发送错误压为 `email_send_outcome_unknown`；[`playwright_runner.go`](../../services/gateway/internal/emailautomation/playwright_runner.go) 的 `mapPlaywrightError` 也合并底层错误。 | 另存安全 cause/stage/source layer；保留对外错误行为与 unknown-send 保护。 |
| D03 (S2 source_verified) | [`host-driver.mjs`](../../tools/browser-controller/src/host-driver.mjs)（212–213、270–271）有 phase/code/reason/command，无 task 标识。 | 加入验证过的 task/operation 关联；并发操作不能可靠地靠时间拼接。 |
| D04 (S2 source_verified) | [`service.go`](../../services/gateway/internal/emailmanagement/service.go) 的 `execute`（380–382）续租失败只 cancel；`safeCode`（434–462）合并未列举的解析/来源/模型错误。 | 对外文案之外保留类型化内部原因和阶段，区分租约/存储失败、取消、超时。 |
| D05 | [`endpoint_rpc.go`](../../services/gateway/internal/iscpworkbench/endpoint_rpc.go)（174–176）仅用 `callCtx.Err()`记 handler 状态；`commitListFailure` 成功保存业务失败后返回 nil，job 因而可成功。 | 传输/作业完成与业务结果分开；不能为日志变红而改变调度语义。 |
| D06 (S3 source_verified) | [`latencytrace/trace.go`](../../services/gateway/internal/latencytrace/trace.go) 需手动开启，队列 1,024/文件 64 MiB，有 dropped/limit 标记但初始化/写失败可能静默；[`latency-trace.mjs`](../../apps/desktop/src/main/latency-trace.mjs) 达 100,000 条或写失败静默停止。 | 显式报告启用/关闭/降级、覆盖及丢失；没记录不能证明没执行。 |
| D07 (S3 source_verified) | [`agent.go`](../../services/gateway/internal/agent/agent.go)（1007）忽略 trace 写失败；[`trace/writer.go`](../../services/gateway/internal/trace/writer.go)（76–78）吞制品写失败；[`app-cli-client.mjs`](../../tools/browser-controller/src/app-cli-client.mjs)（56）吞维护错误。 | 使证据/维护失败可见，不改写邮件结果；Agent trace 本就不覆盖所有直接撰写或后台同步。 |
| D08 (S1 source_verified) | [`compose.go`](../../services/gateway/internal/emailmanagement/compose.go)（293）将有效服务商拒绝记作 `failed`，首次 ACK 失败忽略（306）；[`lifecycle_maintenance.go`](../../services/gateway/internal/emailmanagement/lifecycle_maintenance.go)（86）只补偿 `sent`。 | 源码确认 ACK 恢复覆盖缺口；先隔离复现，再包含合格的拒绝终态结果，零再次派发。尚未证实为实机事故原因。 |
| D09 | [`analysis.go`](../../services/gateway/internal/emailmanagement/analysis.go) 保留失败分析文件，但失败 [`EmailJob`](../../services/gateway/internal/app/email_management.go) 无直接 execution/artifact/ModelCall 索引；`SaveModelCall` 失败仍可能返回生成的 ID。 | 按分析 attempt 建索引，区分持久化成功与仅分配引用；不能把不存在的记录当证据。 |
| D10 (S3 source_verified) | [`compose.go`](../../services/gateway/internal/emailmanagement/compose.go)（258–264）忽略 epoch 查询错误，可能回退 legacy invocation。 | 记录 identity mode 与安全 epoch 错误；取消 fallback 属另一个行为决策，不能混入加日志。 |

已有保护包括不可变发送快照、持久回执、类型化对外错误、未发送证明、同步失败类别/历史、
revision/lease fencing、原件清单、清理隔离、私有耗时记录。应保留并贯通；仅增加通用 logger 不够。

## 3. 已留存实机证据仍不能证明什么

[验收记录](desktop-browser-follow-up-2026-10-23.md)保留了以下历史事实：

| 历史用例 | 已证明 | 缺失证据 / 局限 |
|---|---|---|
| `.39` QQ → Outlook | Sent 基线点击目标缺失；零派发 | hydration 还是 selector 原因；需要绑定任务的阶段证据，不能直接加超时。 |
| `.39` Outlook → Gmail、Gmail → QQ | 各发送一次；收件 Inbox 原件和附件字节通过 | 严格全链路验收不完整；不能为了补诊断重发成功邮件。 |
| Outlook → Gmail 尾部 LF | 额外 LF 已在原生原件；另观察到 MessageView 裁剪 | 哪个阶段加入 LF；对比冻结输入、原件、解析及展示；`.41` 本地修复不等于部署验收。 |
| 后台采集 unknown | 受保护任务行和类型化网络/扩展错误 | 准确原生请求/失败阶段；时间相邻日志不能单独证明因果。 |
| 历史终态任务行缺失 | 完整缺失集合；所引比较中与受保护 send/unknown 无交集 | 逐行删除原因；没有证据不能称为 GC。 |
| 升级/导出/回放 | 资源/codec 失败与后续固定文件回放成功分别保留 | `.41` 实际激活和剩余本地验收；验证脚本成功不等于邮件成功。 |

浏览器原生能力依赖服务商会话与页面/请求形状。资格须注明服务商、账号类别、文件夹、操作、
附件形态、组件版本。Inbox 成功不代表 Sent/Spam/回复/所有附件路径；QQ 通过不代表 Gmail/Outlook。

## 4. 拟实现范围

- 关联**已有身份**：request/RPC、draft/snapshot、invocation/task、job/attempt、
  mailbox/checkpoint、capture/source/analysis。重连/对账保留原业务身份；attempt 仅补充它。
  后台工作不伪造用户 request。
- 增加版本化边界元数据：组件版本、stage/父关系、操作及效果类别、安全原因、各结果维度、
  本进程单调时钟耗时与时间戳。跨主机时钟相减不能可靠代表耗时。用统一内部词表与隐私测试，避免自由文本。
- 拟默认保留有界边界/失败元数据，详细耗时按需开启；显式报告关闭、丢失、过期、不可用。
  不承诺永久完整历史，不能让日志失败控制发送语义。
- 先做**本地只读运维报告**：输入有权限的既有 operation/job，输出最后已证明边界、首个失败、
  效果确定性、缺失证据、允许的恢复方式。缺证据为 `incomplete`，不能猜根因；读取不得 ACK、
  resolve、重试、修改任务。后续若加 API，需授权/owner 隔离测试及契约评审。
- 普通诊断排除地址、主题、正文、文件名、原始 URL/会话值、headers、凭据、原始服务商 ID。
  私有证据引用/不透明 ID 仍按 owner 隔离，导出时按需替换。原制品沿用访问控制。
  常规排障使用有界索引读取，不为每次失败重新全库冻结/导出。
- 保留原 UI，不增加常驻技术状态区。范围为 SparkClaw 与自有 App-CLI；本文不提议改变共享 ISCP
  wire、授权或回执契约。

## 5. 修改顺序与验收

S1–S4 为 **source_verified**；S5 与 D05/D09 待实施。独立业务修复和诊断/schema 工作分开；不相关的新失败另开条目。

| 批次 | 准确改动 | 验收出口 |
|---|---|---|
| M1：保留归因 | D01/D03/D04：失败 invocation、Host 安全任务关联、续租首因 | 并发失败各自关联；区分调用前/拆分失败；memory/file/Postgres 保留失败身份；取消后原因仍在；无敏感载荷、调度不变。 |
| M2：如实表达证据 | D02/D05–D07/D10：安全内部原因、结果分离、诊断写入状态、trace/维护失败、身份模式 | HTTP 成功与业务拒绝可区分；unknown 不变；关闭/满/坏日志源显示证据不完整，业务结果不变；epoch 成功/不支持/失败可区分。 |
| M3：一份报告 | D09 及有索引的只读聚合、版本、阶段覆盖 | 按 attempt 找失败分析；ModelCall/制品缺失明确；重启后重建原操作；重复/乱序事件及不同 owner 不串证据/伪造完成；查询零副作用。 |
| M4：独立业务修复 | 先复现/修 D08；QQ/正文/Sent 各有当前版本复现与修改卡 | 服务商拒绝 + 首次 ACK 失败 + 重启后补 ACK，零再次派发；sent/unknown 保护不变；准确失败点及下游结果均验证。 |
| M5：安装版本与 UI | 合格组件组合，再执行明确授权实机用例 | 记录实装版本、派发次数；Sent/Inbox/正文/附件/UI 分别结论；尽可能只读复用成功邮件；局部矩阵不称全部成功。 |

**本次授权先做 D08 修复（M4），再做 M1**：二者互不依赖，分别提交。
S4 已确认设计与读取器局限见[稳定化方案](application-runtime-stabilization-plan.md)第 7 节。
M1 使下一次失败能明确归属，M2/M3 使证据可直接使用。D08 不能混入诊断补丁。部署和验收脚本另用修改卡，不计为邮件运行态改善。

## 6. 每次修改必须填的卡片

放在 issue/PR 或既有实施记录中，不为每次尝试新建日期报告。

```text
编号 / 负责人 / 状态：planned | source_verified | deployed | accepted | blocked
现象：操作、版本、环境、第一个失败边界
证据：原记录引用；最后已证明事实；还缺什么；根因还是假设
目标：一句可观察的“修改前 → 修改后”
范围：准确模块/存储/适配器；保持的业务约束与 UI 行为
机制：为何本次覆盖所有受影响路径/后端
验收：先有复现；成功 + 失败 + 恢复；涉及 UI 时验证原交互
结果：源码版本 / 部署版本 / 证据版本分开
剩余局限：准确缺失证明与下一份证据/动作
回退：源码边界和持久数据兼容要求
```

本次范围验收通过、原失败保留、局限更新、真实状态填明，才关闭补丁。单测可证明 `source_verified`，
不能直接变成 `deployed` 或 `accepted`。只有新变更、新失败或未解决疑点才扩大/重跑测试。
继续执行[工程底线](engineering-baseline.md)；跨项目改动先有 InfiniCenter accepted 决策。

## 7. S1 / D08 修改卡 — source_verified

- 负责方：SparkClaw 邮件生命周期维护；2026-10-26，仅本地源码。
- 失败与证据：基线 `e580fc01`，隔离文件 Store 与假服务商。`TestRejectedReceiptACKRecoversAfterFileStoreRestartWithoutRedispatch` 先失败：拒绝回执已持久化，重启后仍无 ACK。不是实机历史事故归因。
- 目标与范围：仅在 `services/gateway/internal/emailmanagement/lifecycle_maintenance.go` 恢复合格拒绝终态的待补 ACK；无 schema、UI、对外错误码、调度或 App-CLI 改动。
- 机制：failed + not_sent + provider/invocation/task 一致 + 原有有效拒绝证明；保留 sent 路径，排除 unknown、无证明和已 ACK 回执。复用各后端共同的 ACK 命令。
- 验收与结果：复现先红后绿；两轮只 ACK 一次，拒绝状态/错误码/身份不变、不发布 sent mail，同 key 重放后总发送次数仍为 1。Compose 与证明围栏、隔离 Linux 全量 Go（75 个有测试包）、build/vet、中英文链接检查通过。之前 macOS 路径/tmpfs 和不完整容器配置失败保留在本地日志，不计为通过。证据为同提交 `rejected_ack_recovery_test.go`；源码版本为本卡提交，部署/实机证据：无。
- 局限：无安装版本证据；S2/S3/S4 独立推进。
- 回退：回退本提交；无持久格式变化，已有 ACK 标记继续有效。

## 8. S2 / D01、D03、D04 修改卡 — source_verified

- 负责方/状态：SparkClaw 采集、Host、邮件 worker；2026-10-26，仅源码。
- 失败与证据：S1 源码 `23526369` 下，采集/拆分失败未保留 invocation，Host 并发/页面复用失败无任务关系，续租首因被取消覆盖；三个复现均在修复前先失败。
- 目标/范围：`page_intake.go` 仅将真实调用身份传入既有 sync 命令；`host-driver.mjs` 关联当前 activity；`service.go`、`job_diagnostics.go` 保留安全首因；新增对应测试与 `email_failure_attribution_test.go`。不改 Store schema、公开码、重试/退避、原 UI 或 App-CLI。
- 机制：准入/本地恢复/目标准备失败不伪造 invocation；拆分使用失败分段自身 ID。Host 认证 grant 的 request key 校验后以 UTF-8 SHA256 摘要与真实 task UUID 联查，复用页面不借用旧任务。typed 续租取消及独立正常清理 sentinel 保留首因，Finish/RetryAt 仍只取原 dispatch error。安全诊断白名单不输出原始内容或伪造 typed code。
- 验收/结果：memory/file 实际采集与拆分先红后绿；memory/file/隔离 PostgreSQL 重启后失败身份与完整解码记录一致；Host 并发和真实固定 .41 页面复用；续租/取消/超时/source/model/隐私及调度不变测试与 race 通过。Controller 326 通过 / 148 个原有可选跳过；隔离 Linux 全量 Go 75 包、build/vet 通过。评审追加拆分准入/契约阶段和已知恢复/source 原因的红→绿测试，最终定向 race 覆盖这些补充。源码版本为本卡提交；部署/实机证据：无。
- 局限：没有新增持久通用诊断流，首因/Host 事件依赖既有日志保留；缺失或身份无效仍为 `incomplete`。operation 通过 task ID + `operation_key_sha256` 联查，不按时间相近推断。D05/D09 延期。
- 回退：回退本提交；既有可选 InvocationID 字段向后兼容，无迁移或状态机变更。

## 9. S3 / D02、D06、D07、D10 修改卡 — source_verified

- 负责方/状态：SparkClaw 邮件适配器和既有诊断 sink；2026-10-26，仅源码。
- 失败/证据：S2 `4cde05d6` 会丢失底层 Controller/脚本原因和 epoch 回退模式；关闭/损坏/满载 trace 缺少健康证据；artifact、Agent、反馈刷新和 Controller 维护失败被吞。均先写失败测试，再修复，包含首次/维护 ACK 的可见性；另补旧 operation 误关联、epoch 后 durable begin 失败、ACK 持久化表达的红→绿测试。
- 目标：补安全原因和证据缺失边界，不改变业务决策，不将无事件解释为未执行。
- 准确产品范围：`internal/emailautomation/{errors,playwright_runner,diagnostics}.go`；`internal/emailmanagement/{compose,job_diagnostics,lifecycle_maintenance,send_diagnostics}.go`；`internal/latencytrace/trace.go`；`internal/trace/{writer,diagnostics}.go`；`internal/agent/agent.go`；`internal/gateway/traces_endpoints.go`（以上均在 `services/gateway/`）；`apps/desktop/src/main/latency-trace.mjs`；`tools/browser-controller/src/app-cli-client.mjs`。对应测试与负责模块放在一起；无 Store schema 或对外 API 改动。
- 机制：安全白名单 cause/stage/source 独立于原公开错误映射；owner+draft 引用散列、operation ID 校验；epoch 回退保持，未开始的新尝试清空旧 snapshot。发送观察记 `business_persisted=false`，成功 finish 和持久结果后的 ACK 失败分别表达；诊断不新增 ACK 或派发。trace artifact 错误保留 `errors.Is/As` 和固定阶段；Agent/反馈调用方标 incomplete，不改业务/HTTP 结果；维护诊断回调失败不会传入业务执行。
- sink 健康：`latency_trace_health` v1 输出 enabled/disabled/degraded、固定原因、`instrumented_boundaries_only`、`evidence=incomplete`、尝试/写入/丢弃/丢失/拒绝元数据计数和首个丢失/最后写入证据。Go 1,024 队列/64 MiB、Desktop 100,000 条上限保留；Go 生产者不等待健康输出，Desktop stderr 失败被约束。转换日志中的计数是当时快照，不是最终总数或持久历史。
- 验收/结果：原因/身份/unknown/ACK/持久化/隐私先红后绿；邮件、trace、Agent、反馈 race；实际 64 MiB/100,000 条上限、阻塞/损坏 observer、实际 `WithFields` 拒绝路径通过。隔离 Linux 全量 Go（75 个含测试包）、build/vet 通过；Controller 331 通过 / 148 原有可选跳过；WebChat 504 通过；Desktop 全量串行 530 通过。Desktop 首次并行出现一个 authority-change 失败，同文件隔离 16 项及完整串行复跑通过；原失败保留，不改写。App-CLI 投影检查通过，renderer/WebChat/发行 pins 不变。源码版本为本卡提交；部署/实机证据：无。
- 局限：诊断依赖进程/日志保留，不是新增持久审计账本；未知原因仍 unclassified，缺失健康/Host/trace 仍 incomplete。不证明 `.41` 已安装、原生服务商行为、原件字节或收件人到达。D05/D09 仍延期。
- 回退：回退本提交；只新增诊断字段，无业务 schema 迁移。旧解析器可忽略新增字段，runtime pins 和已存发送保护不变。

## 10. S4 / 最小 M3 修改卡 — source_verified

- 负责方/状态：SparkClaw 邮件诊断适配器，2026-10-26，仅本地源码；负责人已确认方案第 7 节。基线 `3793bfac`，源码版本为本卡独立实现提交；部署/安装/实机证据：无。
- 失败/目标：证据需人工拼接，直接构造常规 Store/Ledger 读取器可能写入证据。首个真实 file Store 夹具完成拒绝 → 首次 ACK 失败 → 重启后，因独立报告命令不存在而失败。实现一次有界、限定 owner 的查询，保持拒绝结果、pending ACK 与 unknown 围栏。
- 准确范围：新增 `services/gateway/cmd/sparkclaw-email-diagnose/`、`services/gateway/internal/emaildiagnostics/`、`services/gateway/internal/emailmanagement/diagnostic_report_test.go`、`tools/browser-controller/scripts/email-diagnostics-ledger.mjs` 与 `test/email-diagnostics-ledger.test.mjs`，以及双语方案/本卡。不改现有业务、UI、公共核心实现、schema、公开码、调度或 App-CLI `.41`。
- 机制：必填 owner 与 draft/operation/job；流式有界安全 file 投影、仅 SELECT 的 repeatable-read PostgreSQL、OS 拒写保护下的嵌入 Node ledger 读取器，以及精确身份关联的既有日志。持久/观察业务、执行、效果、ACK、清理、预期/加载版本与缺失证据分开。`.41` send 仅在 request scope 缺失时，以精确持久 task/revision/digest 作 owner 替代绑定。回执冲突、unknown、变化来源、缺映射、超限不能授予恢复资格或编造历史；恢复仅说明，命令没有业务动作回调。
- 先红后绿证据：命令/重启/unknown 夹具，owner/principal 同名隔离，legacy/op3/alias/head，活动 WAL 与缺 SHM 拒写，损坏/变化/超大来源，隐私，回执 revision，乱序/冲突边界，健康计数，超时/子进程输出阻塞。追加失败还复现 CWD helper 执行和 Node preload 写入，分别用嵌入代码与第一层受限环境消除。保留本地日志 `/tmp/sparkclaw-s4-report-red.log`、`-aggregate-red.log`、`-sources-red.log`、`-ledger-edge-red.log`、`-source-consistency-red.log`、`-last-review-red.log`、`-node-env-red.log`、`-boundary-recovery-red.log`（同前缀）；同提交测试是可重现证据，临时日志不作为私有制品入库。
- 验收/结果：重复调用实际命令后 file 字节/权限/mtime/清单不变，ACK 为 0、额外派发为 0；每个 owner 的原始夹具派发各保持 1，同 key 重放不变、unknown 新 key 仍被阻止。真实隔离 PostgreSQL 的 SELECT-only/RLS/事务模式检查和受影响 Go race 通过。Controller 全量 351 通过 / 150 可选或平台跳过；ledger 独立 21 通过 / 1 平台跳过，包含 Linux 静态/活动 WAL 只读挂载验证。Linux 全量 Go build/vet/tests：77 个含测试包通过。App-CLI 投影、134 份双语 Markdown 与 diff 空白检查通过。首次 Linux 源码快照准备因本机 Python 不支持 `tarfile.extractall(filter=...)` 失败，单独保留，不算产品失败或通过。没有 UI 文件变化，不把此前 UI 测试数冒充本轮重跑。外部 TeamSkills 全库检查仍有 37 项既有 lesson 元数据错误，本次更新未新增；不计为通过。
- 局限：未留存历史始终 incomplete；实际加载版本、清理、trace/run、D09 分析索引无证明。旧 job 日志缺 owner 绑定，不关联；加密快照不支持。生产 ledger 当前需合格 macOS 拒写保护，Linux 缺保护时不打开 ledger，核心读取器仅在拒写夹具下验证。统计为逻辑源数据/投影字节；10 秒工作截止后有界清理/输出开销。以上均明示缺口，不构成改 `.41` 或执行 S5 的授权。
- 回退：移除/回退独立命令、读取器、包装及测试；无迁移或持久诊断状态，既有回执、unknown 保护与 S1–S3 行为兼容。未部署、未发真实邮件、未推送；S5 等待另行授权。
