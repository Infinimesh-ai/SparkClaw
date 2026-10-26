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

**本次仅文档变更。** 下文修复和诊断字段均待实现；没有发信、部署、接口或 UI 变更。

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
| D01 | [`page_intake.go`](../../services/gateway/internal/emailmanagement/page_intake.go) 的 `commitListFailure`（523–539）不写 `InvocationID`；成功提交（426）有。 | 失败历史携带真实失败 invocation；区分调用前失败、采集失败、拆分查询失败，不能伪造未发生调用；不改 checkpoint/重试语义。 |
| D02 | [`compose.go`](../../services/gateway/internal/emailmanagement/compose.go)（275–282）把多数发送错误压为 `email_send_outcome_unknown`；[`playwright_runner.go`](../../services/gateway/internal/emailautomation/playwright_runner.go) 的 `mapPlaywrightError` 也合并底层错误。 | 另存安全 cause/stage/source layer；保留对外错误行为与 unknown-send 保护。 |
| D03 | [`host-driver.mjs`](../../tools/browser-controller/src/host-driver.mjs)（212–213、270–271）有 phase/code/reason/command，无 task 标识。 | 加入验证过的 task/operation 关联；并发操作不能可靠地靠时间拼接。 |
| D04 | [`service.go`](../../services/gateway/internal/emailmanagement/service.go) 的 `execute`（380–382）续租失败只 cancel；`safeCode`（434–462）合并未列举的解析/来源/模型错误。 | 对外文案之外保留类型化内部原因和阶段，区分租约/存储失败、取消、超时。 |
| D05 | [`endpoint_rpc.go`](../../services/gateway/internal/iscpworkbench/endpoint_rpc.go)（174–176）仅用 `callCtx.Err()`记 handler 状态；`commitListFailure` 成功保存业务失败后返回 nil，job 因而可成功。 | 传输/作业完成与业务结果分开；不能为日志变红而改变调度语义。 |
| D06 | [`latencytrace/trace.go`](../../services/gateway/internal/latencytrace/trace.go) 需手动开启，队列 1,024/文件 64 MiB，有 dropped/limit 标记但初始化/写失败可能静默；[`latency-trace.mjs`](../../apps/desktop/src/main/latency-trace.mjs) 达 100,000 条或写失败静默停止。 | 显式报告启用/关闭/降级、覆盖及丢失；没记录不能证明没执行。 |
| D07 | [`agent.go`](../../services/gateway/internal/agent/agent.go)（1007）忽略 trace 写失败；[`trace/writer.go`](../../services/gateway/internal/trace/writer.go)（76–78）吞制品写失败；[`app-cli-client.mjs`](../../tools/browser-controller/src/app-cli-client.mjs)（56）吞维护错误。 | 使证据/维护失败可见，不改写邮件结果；Agent trace 本就不覆盖所有直接撰写或后台同步。 |
| D08 | [`compose.go`](../../services/gateway/internal/emailmanagement/compose.go)（293）将有效服务商拒绝记作 `failed`，首次 ACK 失败忽略（306）；[`lifecycle_maintenance.go`](../../services/gateway/internal/emailmanagement/lifecycle_maintenance.go)（86）只补偿 `sent`。 | 源码确认 ACK 恢复覆盖缺口；先隔离复现，再包含合格的拒绝终态结果，零再次派发。尚未证实为实机事故原因。 |
| D09 | [`analysis.go`](../../services/gateway/internal/emailmanagement/analysis.go) 保留失败分析文件，但失败 [`EmailJob`](../../services/gateway/internal/app/email_management.go) 无直接 execution/artifact/ModelCall 索引；`SaveModelCall` 失败仍可能返回生成的 ID。 | 按分析 attempt 建索引，区分持久化成功与仅分配引用；不能把不存在的记录当证据。 |
| D10 | [`compose.go`](../../services/gateway/internal/emailmanagement/compose.go)（258–264）忽略 epoch 查询错误，可能回退 legacy invocation。 | 记录 identity mode 与安全 epoch 错误；取消 fallback 属另一个行为决策，不能混入加日志。 |

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

全部批次为 **待实现**。独立业务修复和诊断/schema 工作分开；不相关的新失败另开条目。

| 批次 | 准确改动 | 验收出口 |
|---|---|---|
| M1：保留归因 | D01/D03/D04：失败 invocation、Host 安全任务关联、续租首因 | 并发失败各自关联；区分调用前/拆分失败；memory/file/Postgres 保留失败身份；取消后原因仍在；无敏感载荷、调度不变。 |
| M2：如实表达证据 | D02/D05–D07/D10：安全内部原因、结果分离、诊断写入状态、trace/维护失败、身份模式 | HTTP 成功与业务拒绝可区分；unknown 不变；关闭/满/坏日志源显示证据不完整，业务结果不变；epoch 成功/不支持/失败可区分。 |
| M3：一份报告 | D09 及有索引的只读聚合、版本、阶段覆盖 | 按 attempt 找失败分析；ModelCall/制品缺失明确；重启后重建原操作；重复/乱序事件及不同 owner 不串证据/伪造完成；查询零副作用。 |
| M4：独立业务修复 | 先复现/修 D08；QQ/正文/Sent 各有当前版本复现与修改卡 | 服务商拒绝 + 首次 ACK 失败 + 重启后补 ACK，零再次派发；sent/unknown 保护不变；准确失败点及下游结果均验证。 |
| M5：安装版本与 UI | 合格组件组合，再执行明确授权实机用例 | 记录实装版本、派发次数；Sent/Inbox/正文/附件/UI 分别结论；尽可能只读复用成功邮件；局部矩阵不称全部成功。 |

**下一次实现先做 D08 修复（M4）或 M1，顺序不限**：二者互不依赖，分别提交。
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
