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
原审计引用的[验收记录](desktop-browser-follow-up-2026-10-23.md)为历史 `.39` 状态，未读取实机。
当前 `.41` 已加载版本和安装证据见第 12–16 节，不反向改写原审计。

**实施状态：S1–S4 source_verified 且已部署。第 14–16 节记录 Linux 原生只读资格、已部署的通用 manual-call 外键修复及精确制品故障验收。授权 unknown 清理完成，最终观测为 0。完整 S5 邮件验收仍未通过：QQ 正文校验、Outlook 额外 LF、QQ/Outlook 独立 Sent 原件及锁屏后的最终桌面复核待完成。D05/D09 延期。** 不改 App-CLI `.41`、UI、公开码、调度，不增加自动过期，不推送。

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

S1–S4 为 **source_verified 且已部署**；S5 当前验收和未覆盖实机用例的边界见第 13 节，第 12 节保留历史状态；D05/D09 仍延期。独立业务修复和诊断/schema 工作分开；不相关的新失败另开条目。

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

## 11. S5 部署前置修改卡 — 未部署

- 负责方/状态：SparkClaw 发行验收，2026-10-26。负责人已授权 S5 部署及 Mac 安装验收；不推送、不发真实邮件、不改 App-CLI `.41` 约束仍有效，unknown 永不重发。S1–S4 保持 `source_verified`，不是 `deployed` 或 `accepted`。
- 目标/失败：安装精确的当前阶段组合，验证恢复、诊断和原 UI。旧 `.41` stage 包含 `2396381e`，不能证明 S1–S4。完整保全流水线目前在解码或停止服务前的资源预检受阻；环境门槛失败不算产品失败。
- 准确范围：不可变 SparkClaw `0f46702a18e9b8fc8bb4e4fbe43341e097fd7af3` 与不变 App-CLI `bcc55506cc933f981668e3b2d1ed1eba1bf4c6a3`。私有构建/暂存/备份证据在 `~/.cache/sparkclaw-upgrades/20261026-runtime-s5-0f46702a`；仓库改动为本双语检查点、方案第 2 节、跟进记录和[公开证据摘要](../../docs/evidence/runtime-stabilization-s5-2026-10-26.json)。不改 UI、公开码、调度、公共核心或运行时源码。
- 准备/结果：已构建 Go Gateway/bridge、Linux/macOS 独立诊断二进制；Node 26 Mac DMG/ZIP 构建及包审计通过。全部 2,964 个受控源文件仍匹配 pin。work2 四个缓存制品 SHA 和 bundle 校验通过；Gateway 与诊断 `-h` 成功，未 bootstrap 运行时。Mac 6 个 profile、97 张表冷备逐项一致；旧/新应用清单各 296 项，仍安装旧应用。
- 制品资格：实际 Darwin 诊断二进制完成 25 次合成调用（24 次预期 exit 0、一个无效参数 exit 2），owner/principal/ledger owner 隔离、unknown `inspect_without_resend` 和 file/static ledger/active WAL 的字节/权限/mtime/清单一致性均通过。10 项 OS 拒写探针通过，没有生产 ACK 或服务商操作。4 项 harness 负控制先失败；首轮合成 draft/snapshot 不一致产生冲突证据，夹具失败已单独保留。根重新校验 89 个证据文件并独立核验前后快照一致。Mac 包 82 个源文件、24 个已构建 UI 文件匹配，仅正常 package 元数据删减不同。私有准入/维护原型在保留红测试后分别通过 21 / 9 项，但仍不可执行生产操作。全部 134 个双语文档检查通过；TeamSkills 仍有相同 37 项既有元数据错误，不计通过。
- 当前实机证据：05:44:17 UTC，work2 仍为 `9eab082c` / `.39`，同一个健康 Gateway 容器/镜像和三个 active unit。这是新鲜健康观察，不是完整数据保全或 unknown 集合检查。Mac 在安装前已显示 HTTP 503，不能用远端健康覆盖这一问题。
- 资源边界：05:48:37 UTC 本机可用 3.76 GiB；05:44:17 UTC 远端可用 5.28 GiB。完整校验仍分别要求至少 4 / 8 GiB，保留至少半数可用内存且不少于 2 GiB，以及 12 GiB 磁盘余量。已按明确授权正常退出 Chrome/Claude/WPS；WPS 云后台仍在，没有强制终止。未解码保存响应、未获取新的完整基线、未 freeze、未重启服务、未切换源码或安装 Mac。单独重启浏览器不能替代排空与保全，未保存页面状态能否保全仍无证明。
- 局限/下一门槛：制品身份核对不能当成实际加载版本验收。私有准入草案保持关闭，尚无部署执行适配器。资源达标后先完成相同保存响应接收、健康态完整新基线与复核，再受控 freeze/激活/健康交接和 Mac UI 验收。S1 拒绝回执补 ACK 必须有精确 task/revision/digest 证明且额外派发为零；unknown 保持完整原行保护。历史缺失行和失败运行继续保留。
- 回退：没有已激活运行时或应用替换需要回退。保留原 `.39` 服务、旧 Mac 应用、源数据、备份和全部证据；不得用旧数据库恢复掩盖差异。无新增邮件、重发或推送。S5 尚未完成。

## 12. S5 定向部署修改卡 — 已部署，验收阻塞

**这是签发器维护前的历史检查点。** 下文容量证据与失败均保留。负责人随后明确要求先取消存储上限，再一次性清理无用过期记录，不增加自动过期机制，也不以 InfiniCenter 方案为前置条件。第 13 节记录已完成的修复与当前 UI 结果；下文阻塞状态不再代表现状。

负责人确认浏览器没有未保存内容，并明确允许先清理再升级，只保留本次验证需要
的证据。此授权替代前一 S5 检查点中的全历史保全/解码前置；其失败作为历史保留，
不再作为当前准入条件。保留当前全部 send/unknown 原行及相关证据、draft/send
快照、邮箱/身份配置和精确回退组件。不删除或 resolve unknown、不重发邮件、
不改 App-CLI `.41`、不推送，不改产品调度、UI 或公开码。新的有界定向采集器
证明自己的资源限制，不沿用旧全量导出的 4/8 GiB 门槛。

已记录精确路径、大小、哈希后删除八个本机旧合成导出及 `2396381e` DMG/ZIP/
源码 tar，共 2,191,551,482 字节（2.041 GiB）。小型失败回执、脚本、原核心证据、
所有线上数据与当前 `0f46702a` 制品/冷备均保留。这是测试制品清理，不是宣称
生产业务数据已被垃圾回收。浏览器正常停止后，另清理三个缓存目录的 98 个文件，
共 607,399,085 字节；非缓存文件的字节和权限前后完全一致。Cookies、IndexedDB、
local storage、sessions 和 service-worker storage 均保留。

- 范围/机制：以私有发行工具部署不可变 `0f46702a` + 不变 `.41`。定向快照约
  1.1 MB，远端采集峰值 RSS 约 60 MiB；PostgreSQL 只读事务和外层只读挂载保护
  采集。外层采集资格不能冒充生产 S4 Linux ledger 拒写保护。不改产品 UI、源码、
  调度或共享契约。
- 保全：全部 47 个 send、153 个 unknown（重叠一个），合计 199 个保护任务、
  998 个事件、101 个 draft/send_snapshot、10 个设备、邮箱绑定、release/anchor
  身份，以及一条精确的过期 prepared 采集任务及事件，在 freeze、清缓存、激活、
  诊断及 07:01:04 UTC UI 检查点均一致。该过期行已按未变的构造/恢复源码复核；未调用、取消、
  删除，也不是通用豁免。
- 激活：先正常排空，再选择源码/运行时/组件。原生 Reader 导入首次超时，唯一
  一次正常重启后通过；`.41`、Tampermonkey `5.5.0.21`、Bridge `1.0.29`、原生
  握手及 installed-release 检查均通过，临时恢复会话 override 已移除。首次超时
  的根因未证明。选定用户偏好保留；36 项浏览器元数据变化完整留存，其中一个站点
  的自动 HTTPS 升级例外变化尚无具体原因证明。
- 已加载服务检查点：新 Gateway 在精确暂存镜像上 healthy。原准入验收器错误要求
  两种服务都在 `ExecStart` 携带运行时路径，因此中止：Controller 使用既有环境
  变量，Executor 使用命令参数；实际进程证据均匹配 `.41`。06:40:48 UTC 独立只读
  续验通过，绑定原失败、容器 ID、PID/启动时间、实际环境、Python→Node 交接及
  继承锁 FD。实际交接先复现红测试，再独立通过全部 13 项续验测试；未重复准入、
  重启服务或伪造原成功回执。
- 真实数据诊断：精确 Linux 二进制 5 次调用通过重复 unknown 报告、owner 隔离
  （0 行）、隐私和 Linux ledger 保护缺失表达（ledger 0 行/0 字节）。当前全部
  101 个 draft/send_snapshot 行和另行采集的 9 项保护集完全一致。服务商拒绝类别
  不可用；报告缺失的版本/日志/清理/trace 不以单独健康回执补造。
- Mac 安装：296 项应用文件匹配候选；替换时 6 个 profile、111 张既有表完全一致
  （原统计 97 张，另有 Default 中原已存在的 14 张），旧应用保留。CUA 实际观察
  通过原布局/入口、旧会话历史、设置/返回、日程导航、侧栏快捷键、空输入框聚焦、
  正常退出/进程归零/重启；未输入或发送消息、未保存草稿。
- 未完成验收：既有 Mac 连接仍阻塞邮件/浏览器交互。已安装 helper 的只读
  `-check` 返回 `enrollment Relay refresh credential expired`；两次空 stdin
  正常启动均返回 `control: authorization_unavailable`，业务帧为零，profile 文件
  无变化；未切换现有 profile。原入口重启后，邮件/浏览器入口仍保留并禁用。源码测试/合成验证不替代真实服务商拒绝、首次 ACK
  失败恢复、原浏览器交互或投递验收。本轮没有真实邮件授权。
- 连接容量阻塞：管理身份/客户端/设备只读查询、原模板 TCP/TLS 握手，以及一次
  签名 Authority 查询（HTTP 200、响应签名验证）均通过。签发器只读事务发现
  547 个成功控制记录及 parent，历史 complete/open，无 running，writer 配置匹配；
  两次读取的加密状态一致。明文序列化为 4,193,235 字节；只在内存加入下一次恢复
  的必需 record，不含 parent/grant/predecessor，已达 4,195,320 字节，超过
  4,194,304 字节 seal 上限。该大小计算未签发凭据、发请求或持久化。这证明容量
  阻塞；原通用 503 缺少细粒度 trace，不将所有历史失败都归为此因。保留原身份
  和全部授权历史；InfiniCenter 提案 0050、ISCP inbox 015 已请求独立有界存储设计
  评审。开放 namespace 去重及签名结果至少七天的保留契约，不允许按升级缓存清掉
  这些记录。本轮未实施签发器修复或契约变更；S5 完整验收等待原身份恢复和剩余 UI 检查。
- 回退边界：保留旧源码/镜像/容器、运行时/组件、unit 与 Mac 应用；必要时只恢复
  匹配发行版本，不用旧业务数据库/profile 备份掩盖差异。无推送、真实发送、unknown
  重试或 App-CLI 源码修改。见[定向证据摘要](../../docs/evidence/runtime-stabilization-s5-scoped-2026-10-26.json)。

## 13. S5 签发器容量修复与一次性清理 — 已部署，UI 检查通过

- 负责人/状态：SparkClaw，2026-10-26。用户最终指示是先取消签发器存储容量限制，
  再明确执行一次无用过期凭证记录清理。**没有增加自动过期/GC 机制或 pruned-generation
  元数据。** S5 部署与可执行的原 UI 检查通过；正常退出重启和最终阅读邮件后的保全比对通过，9 项保护集完全一致。
- 现象/目标：第 12 节保留的大小复现证明，累积签名历史可能超过既有 aggregate 上限，
  阻碍下一次凭据恢复。取消持久化与条数阻塞，保留 wire/request 验证、writer 围栏、
  设备身份与既有恢复语义。原通用授权错误不足以把所有历史失败都归为容量问题。
- 准确源码边界：`services/gateway/internal/iscpnodeissuer/store.go` 取消 seal/open
  的 4 MiB 检查及 SQL `bounded_ciphertext` CHECK，包含既有表迁移；`control_v2.go`
  取消 1024 record 准入上限；`store_capacity_test.go` 覆盖两者。frame/request/
  私有文件的其他限额不变。清理是独立的一次性私有 operator，不接入服务循环。
  不改 App-CLI `.41`、Gateway/Controller/Executor 调度、原 UI 或公开错误码，
  不向公共核心增加邮件分支。
- 红→绿/源码验证：四项新增容量测试覆盖大历史加密与读取、超过 1024 条后准入及稳定重放、
  旧表迁移/重启/事务回滚/writer 围栏。独立验证发现测试 DSN 隔离错误，修正后增加
  精确 `current_database()` 断言；实际旧 `22da89f` 基线按预期报
  `legacy constraint remains`，修复版四项连续两轮共 8 次通过，其中包含两次真实隔离
  PostgreSQL 验证。一次性 selector/operator 的 27 项顶层测试、45 项子测试及 race/vet
  通过。完整 Linux gateway 共 5606 项测试事件通过、197 项条件跳过、77 个测试包通过、
  零失败，build/vet 通过。该离线 Linux arm64 运行使用 Go 1.25.5、禁网、3 GiB/3 CPU，
  现有 module cache 复制有 SHA 验证，源码前后不变；早期 harness/cache/fixture 失败单独保留。
- 制品/来源：实际旧 native issuer 来自 ISCP
  `22da89f329a88bde8e12219c359e82063064798b`；其生产 issuer/control 源码与
  SparkClaw 副本在 import 路径正规化后相同。私有隔离导出精确 ISCP tree，只移植两个
  生产文件和容量测试，不改 ISCP 工作树。补丁 SHA-256 为
  `eb1b94f559407d97c43d445eb9d74c41b7353d3e78505e96f89f312ab78b384d`。
  Go 1.25.12 / Linux amd64 / CGO=0 构建并部署的 native 二进制 SHA-256 为
  `a866beae2809fe30511b9e0ab010ef333c2ae2ceb7fa80a98b1cd6f36b2b383f`。
  全源文件哈希、buildinfo 与日志保留在私有 `build-issuer-02` manifest；导出 tree 的
  buildinfo 为 `(devel)`，以 manifest 绑定来源，不伪造嵌入的 VCS revision。
- 维护/保全（UTC）：08:29:31 仅正常停止 issuer，私有冷备核实 100 个 node 文件、
  10 个加密行、writer/schema、storage key 和旧 binary 完全一致。08:30:19 仅删除
  `bounded_ciphertext`；08:31:07 启动新 native issuer。08:31:30 实际 binary、
  config、identity、writer 与 guard 精确匹配；Gateway、Controller、Executor、browser
  PID 均未变。08:31:31 仅 daemon-reload 恢复原 `Restart=on-failure`，额外重启为零。
- 一次性清理：原始精确计划 SHA-256 为
  `5df235707d0894a59709135e18048cfad7e462430e227cfc4dfaa3244d635c19`。
  单事务删除两个活动设备共 865 个 Records 与 865 个 Parents（319 + 546），各保留最新
  一条；其余 8 个非 active 行不动。密文总量由 6,974,116 降到 393,828 字节，主凭据
  generation 在清理前后保持 548；后续计划为零候选。管理端三个 GET 均 HTTP 200，
  原 active device/client/invitation 身份匹配，未靠替换身份或新增邮件操作使连接通过。
- 当前任务保护：维护前快照共 47 个 send、154 个 unknown、1 个重叠 unknown-send。
  相比 08:00，新增一条 QQ `collect_page` 在 08:08:56 UTC 成为 uncertain，原因码
  `EMAIL_NETWORK_LIST_UNQUALIFIED`，dispatch/token 均 null，有 5 个事件，发生于
  **issuer 维护前**。旧 153 个 unknown 与 send 原行完全一致，新行已纳入保护、不重试；
  它不是新发送，底层根因仍未证明。
- 原 UI 实测：正常 `getApp` 恢复 Mac 原选定 profile 的连接；原邮件入口、列表、既有
  验收邮件详情及正文展开通过。正常阅读改变一封邮件的 viewed 状态，未回复、存草稿、
  发送或重解析。个人浏览器打开公开 Apple 页面，462 px 面板、wide/restore、新建/关闭
  标签页以及返回 `about:blank` 均通过。08:40 UTC 正常退出后应用进程为零，原 main device/invitation、workbench/
  connection/profile 及身份哈希精确匹配。只有 5 个凭证/Relay journal 文件变化：
  `grant.json`、`relay.enrollment.json` 及其 `.credentials.v2`、`.duplex.pending.v1`
  journal，以及 `identity/node-control/operations.v2`。正常恢复后 generation 从 548
  到 550，pending pins 为零；已安装 helper `-check` 退出 0，17 个 profile 文件不变。
  正常重启后邮件列表重新载入 121 项并回到 Owner 工作台；正常阅读后 unviewed 从 118
  到 117。**最终阅读邮件后保全比对通过**，精确数量见下项。
- 最终阅读后保全：约 08:41 UTC，freeze 前快照
  `fbab80b978b8f9d4b3e95414ec2fb0ad274ca7f271b071d8463776057296eb4f`
  与 UI 后快照 `6443ddfbfa65bd18caa719dc8610ae534206b5755cd0b1e6229b5e6b1fd30628`
  的 9 项保护集全 exact：47 send / 154 unknown / 1 个重叠 unknown-send，200 个 task 行、
  1003 个事件、101 个 draft/send-snapshot 行、10 个设备。维护后没有新增 unknown 或 send
  变化。其他差异为 observation time、正常 watch 的 mailbox/checkpoint/queue/metadata
  推进、runtime health 滚动日志与 Mounts 顺序；排序 Mounts 并排除滚动 Health.Log 字段后，
  完整 runtime_identity 精确一致，原始差异仍保留。私有冷备已离机下载核验：16,019,552
  字节，SHA-256 `bf908136693d44721d0c4a54eea1af5e5d5c6888d6a32ce8bf9d7177f5c5cbf2`。
- 局限/回退：没有实机注入服务商拒绝或首次 ACK 失败，没有真实发信，不将 S1 源码覆盖
  冒充这些实机用例。S4 Linux ledger 读取器仍为 `incomplete`、零打开；无推送。
  保留冷备和旧 binary，但不得恢复已删凭证历史、旧 generation 或业务数据库来掩盖
  差异。换回旧 binary 需逐 aggregate 验证满足其 4 MiB/1024 record 行为，并有下一次
  必需更新空间；不静默恢复已取消的 SQL 限额。
  公开脱敏证据：[签发器容量清理 S5 摘要](../../docs/evidence/issuer-capacity-cleanup-s5-2026-10-26.json)。

## 14. S4 Linux 只读账本保护 — 已安装，限定范围验收通过

- ID / 负责人 / 状态：S4 最小 M3，SparkClaw 诊断适配器；source_verified，独立 CLI 已安装，
  在实际 Linux amd64 主机通过资格及读取验收。仅当内置保护合格时，替代此前 Linux 零打开限制。
- 故障与目标：原 Linux reader 恒返回 `ledger_write_guard_unavailable`；初版保护清空全部
  capability 后，又无法穿过实际 Ubuntu 所有的 0700 目录读取 0600 账本。现在合格来源可读，
  写入仍由内核拒绝。
- 范围与机制：仅独立诊断命令、Go/Node 账本桥和测试。子进程建立私有 PID/mount/network/IPC
  namespace、私有 proc、递归只读/nodev/nosuid 挂载；no-new-privileges/seccomp 禁止 socket、
  文件 handle 和 namespace/mount 逃逸。仅保留 `CAP_DAC_READ_SEARCH`（mask 4），没有
  `CAP_DAC_OVERRIDE`。Node 在访问源前独立检查实际条件，不能用调用参数代替保护。
  权限不合格时零打开。不改 App-CLI、运行时、公开业务码、调度、owner 规则或 UI，不提供 ACK/重发。
- 红→绿：先复现 Linux 不可用、已有 PID namespace 逃逸、私有用户来源不可读，再逐项修复。
  干净 Linux Go 全套 5613 个通过事件/77 个测试包，race/build/vet 通过；Linux/macOS Node
  各 20 通过、3 条件跳过，macOS Go 通过。保留并排除最初旧镜像覆盖污染的测试结果。
  部署主机原生测试 7 通过、零跳过；64 次真实写入全部被拒，handle syscall 被拒，私有用户来源
  可读，不合格路径零打开。
- 实读验收：10:26:17 UTC，对新出现的 Gmail 采集 unknown 读取两次，每次 6 行/970 字节；
  所选 task 与五条事件完全一致，规范化报告一致，恢复建议保持 `inspect_without_resend`。
  异 owner 无法关联任务（内部有限检查一行，不输出任务）。缺版本/日志/trace 仍 incomplete。
  正常运行时写者未停，因此不声称整个实时 DB/WAL/SHM 字节不变；诊断零发送、零 ACK。
- 结果与来源：源码主题 `dfd9428e` + `2b866299` 在 `d511a1e0` 上合为一次 main 修改。
  10:32:37 UTC 新安装 `/usr/local/bin/sparkclaw-email-diagnose`，目标为
  `/opt/sparkclaw/releases/20261026-email-diagnostics-ad7ae6d7/bin/sparkclaw-email-diagnose`，
  SHA-256 `ad7ae6d7c8af85721d73823c642ed64893903e50cf76ba84fd66c42c24b386cf`。
  Gateway 仍为 `0f46702a`、App-CLI 仍 `.41`，本次 CLI 安装无需重启服务。
  [脱敏验收证据](../../docs/evidence/s4-linux-readonly-acceptance-2026-10-26.json)
  分开记录源码、原生资格和实际读取。
- 剩余限制：要求合格 namespace/mount/seccomp 权限及 Node 26，不自动提权。
  缺 WAL/SHM 的冷态 WAL 数据库可能仍 incomplete；不创建源 sidecar、不自动采用 immutable。
  本项不修复新出现的 Gmail 采集故障。
- 回滚：仅移除本次精确的新 symlink/发布二进制，或回退诊断源码；无需业务迁移或数据恢复。

## 15. S5 手工工具结果持久化 — source_verified

- ID / 负责人 / 状态：S5 验收发现，Gateway 通用 manual 调用；source_verified，部署及最终
  业务验收另行记录。
- 故障：正常 typed Gmail Sent 读取已在不变 `.41` 上完成，但 Gateway `0f46702a`
  保存完成态 tool call 时 HTTP 500：`tool_calls_run_id_fkey` / SQLSTATE 23503。
  成功分支先保存子 call、后保存父 run。普通策略拒绝、执行失败、等待批准路径已先保存 run；
  不笼统声称其它异常分支均已验证。
- 证据：一条 completed 原生读取、一个完成态 observation artifact，以及精确的
  session→admission audit→observation→ledger capture→native manifest 关联，证明原操作
  已完成。独立 Sent 原件的完整正文、已观察头字段及附件字节均通过。没有再次 read/send；
  缺失的 run/call 行与原 HTTP 失败仍作为证据保留。
- 目标 / 范围 / 机制：成功的 manual 调用先保存父 run，再保存子 call，满足 PostgreSQL
  现有外键，使相同结果可持久化。只改通用成功路径顺序；不新增邮件分支、schema 迁移、
  Store 契约、公开错误码、调度、App-CLI、UI 或权限变更。
- 红→绿：新增普通 `memory.search` 回归用合成空库并禁用 retention。原 memory/file
  通过，真实隔离 PostgreSQL 精确失败于上述外键。最小顺序修复后三后端通过；file/PG
  重新打开后验证一个完成态 run、恰好一个 call 和输出一致，定向 race 通过。测试不访问
  真实服务商；保留源码测试和首次失败日志。
- 结果：源码主题 `adaca175d2a0bccdf3da753c901a6d750f08783f` 单独集成至 main。
  原 Sent 原件证据独立成立，不把持久化失败改写成 HTTP 200。
- 剩余限制：不新增跨记录事务，也不补造此前失败请求的 run/call 行。QQ 派发前账号证据
  失败、Outlook 原文多 LF、采集资格失败仍属独立问题。安装后使用 `reminders.list`
  验收，避开 `memory.search` 的 retention 副作用，不重复已经完成的服务商读取。
- 回滚：回退成功路径顺序或恢复旧 Gateway 镜像；无需数据迁移、来源删除、App-CLI 替换
  或 UI 回滚。

## 16. S5 继续验收——已部署并通过范围内检查，邮件矩阵仍未关闭

- 编号/负责人/目标：S5，SparkClaw 执行链路与一次性验收运维。验证实际制品、完成授权业务
  检查并删除指定 unknown 历史；部署/运维结果与第 14–15 节源码修复分开。未加自动过期或重试机制。
- 文件/机制：本次验收提交只包含脱敏证据与双语方案、清单、修改卡；私有运维脚本、删除前
  精确备份及原失败记录留在 Git 外。通用 manual-call 父记录先于子记录持久化修复仅替换
  Gateway；无邮件分支、schema、UI、公开错误码、调度或 App-CLI `.41` 改动。
- 先红后绿：真实 PostgreSQL 外键回归先失败，修复后 memory/file/PostgreSQL、重新打开读回
  及 race 通过。最终 Linux 全量 5,610 pass / 203 skip / 77 包，build/vet/交叉编译通过。
  前两轮遇到原有临时目录清理与到期时钟边缘失败，保留日志；单项复跑及最终串行全量通过，
  未改这些产品路径。一次性清理/放行脚本也保留修复前失败测试。完整行 canonical SHA 与操作
  protocol digest 不同；两次本地复核断言在远端动作前停止，改为完整行相等与正确 SHA 后通过。
  request epoch 13 与 task ledger epoch 73 分开核对。
- 精确制品：源码 `adaca175d2a0bccdf3da753c901a6d750f08783f`，与 main `d97cfcf1` 的产品修复相同；
  可执行文件 SHA-256 `2bb5267d2908c98908cb4cad9babb7350067cb8a86001c0a192b65665f9e2fec`；
  镜像 `sha256:f388b1958ed13bb299a372dfdb269a5437f890326598ce6350593a39fc9d5a4d`。
  Controller/远端 checkout、Mac 保持 `0f46702a`；`.41` manifest 保持
  `e315727dfe2802b92ef97f3fc0828825e343b3056f5a8d3214207c32840a7db4`。
- 制品故障验收：8 项隔离用例、12 次正常进程停止通过，自有测试容器已清理。拒绝→ACK 503→
  重启后补 ACK，总派发 1；unknown 重启/同 key 零新增派发、零 ACK，新 key 409。并发原因、
  正常取消、原 60 秒间隔下真实文件续租失败、trace 失败、latency 初始化失败及真实 ENOSPC
  写入降级通过。Controller/服务商为模拟，不冒充真实服务商拒绝、实际 `.41` 账本恢复、
  Browser Host D03、桌面 recorder 或 PostgreSQL 续租故障覆盖。
  见[制品证据](../../docs/evidence/s5-installed-fault-acceptance-2026-10-26.json)。
- 部署证据：第一次启动在语义索引 embedding 第 4/5 批触及原有 20 秒截止时间而失败；
  独立审查后额外正常启动一次转为健康，未改产品超时或调度。Docker 创建态的
  `OomKillDisable` null/false 差异、健康启动时正常进行的三项采集均曾使运维校验停止，
  原失败保留。最终原始 HostConfig、保护数据及其他服务一致；不重启恢复 `unless-stopped`。
  正常 `reminders.list` 返回 200，PostgreSQL 保存恰好一个完成的 run 和 tool call，输出一致；
  QA 撤销 200、旧凭证 401、凭证文件删除。
- 业务证据：对两份既有收件原件各重解析一次，旧文件/capture/representation/job 完整保留，
  各新增一个 v6 parse job/representation；展示保留解码后完整原文。Gmail→QQ 正文/附件与批准
  内容一致；Outlook→Gmail 原生原件仍多一个末尾 LF，来源阶段未证实。Gmail 独立 Sent 原件
  通过一次正常 typed read 验证正文/头信息/附件；原 HTTP 500/FK 失败保留，不因原生读取完成
  就改写为 API 成功，也未重复读取或补造旧 run/call 行。
- QQ 证据：两次新授权 API 请求均零实际发出。第一次在 Sent 基线账号证据检查停止，无法
  区分账号 hash 缺失还是不等。后续完成的 QQ 采集提供匹配的账号结果及 profile 证据；
  第二次进入 `prepare_draft`，只因正文预期 106、读回 109 被
  `email_draft_fields_unverified` 拒绝。任务 attempt/effect 为 0，dispatch/token 为 null，
  关联负向 journal 支持未派发。首次结束校验遇到短暂非空闲而停止，保留 STOP，后续仅只读
  归类。继续修复停在不改 `.41` 的边界，未进行第三次尝试。两个 QA 客户端均已撤销、401
  验证并删除凭证。原矩阵实际发信仍 QQ 0、Outlook 1、Gmail 1，未重发第 2/3 封。
- 授权清理：首轮正常冻结删除 154 个 unknown 任务、698 条事件、385 个精确关联文件及
  PostgreSQL 的两条 unknown draft/snapshot，其余数据一致。恢复后 Gmail 新增一条采集
  unknown，诊断后未重试；正常 epoch rotation/维护封存其请求命名空间，最终冻结再删除
  该任务、5 条事件和 3 个文件，一个已缺失关联文件仍作为缺口记录。最终冷读 unknown 0、
  send 48，其余账本/文件/authority 一致；PostgreSQL 23 类共 132,690 行完整一致，第二轮
  不删除 PG。累计 **155 任务 / 703 事件 / 388 文件 / 两条 PG 记录**；限于验证所需的私有
  备份已下载离机保留，无巨量历史恢复或自动 GC。
- 最终状态：12:29:41 UTC 正常恢复健康；12:39:56 UTC unknown 0、unknown-send 0、send 48、
  draft/snapshot 103、devices 10、活动发送和 pending release 均 0。浏览器和签发器未重启，
  源码 manifest、配置、owner 边界保持。第 13 节原 UI 与退出重启验收通过记录保留；最后
  维护后的 Mac 连接复核因锁屏无法执行，已请求解锁，不切换 profile 或另做 UI 绕行。
- 局限/回退：完整 S5 邮件矩阵仍有 QQ 正文校验、Outlook 额外 LF、QQ/Outlook 独立 Sent
  原件未通过。采集失败根因、381 个历史终态缺口和既有 CI 失败仍待独立处理，删除记录不代表
  根因修复。旧 Gateway 容器/镜像停止保留，可经审查回退；不能恢复已删历史、旧 epoch 或
  凭证掩盖变化。删除 alias 后 legacy caller 可能分配新 op3，不能声称所有旧 caller ID 永久
  不可能再次调用；运行时自动 unknown 重发保护不变。不推送、不处理 InfiniCenter。
- 证据：[业务/部署/清理](../../docs/evidence/s5-business-acceptance-2026-10-26.json)、
  [源码资格](../../docs/evidence/s5-manual-persistence-fix-2026-10-26.json)。
- 文档验证：实际 CI 双语镜像/链接检查通过（134 个项目 Markdown），`git diff --check` 通过。独立 Team-Skills 检查仍有 37 个既有 lesson frontmatter 错误和 56 个警告，新 lesson 不在失败列表；未混入无关修复。
