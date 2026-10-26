# 应用执行底座：当前阶段稳定化方案

> 语言：简体中文 | [English](../../docs/application-runtime-stabilization-plan.md)

## 1. 结论与范围

**状态：实施中，2026-10-26，Asia/Shanghai；S1–S3 source_verified，S4 设计待确认，S5 未授权。**

App-CLI 当前只有一个应用模块（`runtimes/browser/applications/mail`），也没有已立项的第二个
消费者。本阶段目标因此限定为：**让现有邮件链路在已有底座上恢复不丢结果、一次失败可以归因、
排障不必全库导出，并完成 `.41` 安装版本验收。** 不建设“任意模块只改模块、不改核心”的平台能力；
需求提高后按第 5 节另行设计。

审计基线：SparkClaw `db9ca9cf`；固定 App-CLI `.41`，源码
`bcc55506cc933f981668e3b2d1ed1eba1bf4c6a3`。原审计读取源码和既有证据，未重跑测试或检查实机；本轮源码测试见第 2 节与修改卡。
[延期验收清单](desktop-browser-follow-up-2026-10-23.md)仍记录 `.41` 未激活，`.39` 为最后核验部署。

保留现有分工：App-CLI Registry 负责能力/schema/授权，Executor 与账本负责任务身份、效果、结果、
ACK 和恢复，Controller Browser Host 负责页面/profile、资源队列和租约，邮件模块负责服务商操作
及其结果证明，Gateway 负责业务状态和原 UI。各缺口在原负责方内修复，不新增层级。

## 2. 本阶段目标

| 目标 | 现状（源码已确认） | 出口 |
|---|---|---|
| G1 恢复不丢结果 | **S1 source_verified（2026-10-26）**：有效拒绝终态纳入补 ACK；文件 Store 重启复现由红变绿，两个维护轮只 ACK 一次，dispatch 总数保持 1。sent/unknown 与证明身份围栏回归通过。未部署、未实机验收。 | 源码出口满足；部署与安装验收仍须授权。 |
| G2 一次失败可归因 | **S2/S3 source_verified（2026-10-26）**：失败/拆分 invocation 跨三后端保留；当前 Host activity 关联、续租首因、安全发送原因、epoch 身份模式、ACK/trace/维护失败和有界 sink 健康可见。公开码与调度不变。 | 已埋点边界内源码出口满足；日志缺失仍 incomplete。未部署或实机验收。 |
| G3 最小只读排障 | 证据分散在回执、任务事件、Host 日志和 trace，需人工拼接。 | 按 draft/operation/job 查询，输出实际版本、最后确认阶段、首个原因、执行结果、业务结果、ACK、缺失证据；不能证明的部分标 `incomplete`。 |
| G4 安装版本验收 | `.41` 已合格打包、未激活。 | 另行取得授权后，按延期验收清单在支持配置上完成，结论分别记录。 |

各 D 项的准确修复边界见[邮件诊断与修改边界](email-diagnostics-and-change-control.md)，本文不重复。
D05（job 完成与业务结果分离）和 D09（分析 attempt 索引）不阻塞本阶段出口，按邮件文档单独推进。

## 3. 实施顺序

1. **S1 修 D08**：先写复现测试（拒绝 → 首次 ACK 失败 → 重启/维护轮），再让维护循环覆盖
   带合格拒绝证明的终态结果。独立提交，不混入诊断改动。
2. **S2 保留归因**（邮件 M1）：D01、D03、D04。与 S1 互不依赖，可先做任一项。
3. **S3 如实表达证据**（邮件 M2 的必要部分）：D02 内部原因、D06/D07 写失败可见、D10 identity mode。
4. **S4 只读报告**（邮件 M3 的最小版）：复用已有回执/事件/诊断入口，提供命令行或现有诊断入口，
   不新增常驻 UI、不新增对外 API。读取不得 ACK、resolve、重试或修改任务。
5. **S5 安装版本验收**（邮件 M5）：仅在负责人授权后执行；本方案不授予部署或实机权限。

每一步使用邮件文档第 6 节修改卡；源码验证、部署、实机验收分开记录。

## 4. 验收用例

| 用例 | 必须观察到的结果 |
|---|---|
| 服务商拒绝，首次 ACK 失败，重启 | 补 ACK；业务结果仍为拒绝；派发次数不变。 |
| sent / unknown 回归 | 原 ACK 与 unknown-write 保护不变；unknown 不被重发。 |
| 两个并发失败 | 各自关联正确 invocation/task，不靠时间拼接。 |
| 准入前失败 | 明确显示尚未建立 task，不虚构 invocation。 |
| 取消或续租失败 | 首因保留，后续 cancel 不覆盖原因。 |
| 诊断关闭、写满、写失败 | 业务结果不变；报告标 `incomplete` 和缺失边界。 |
| 涉及 Store 的改动 | memory/file/Postgres 三个后端都保留失败身份。 |
| 隐私 | 普通诊断不含地址、主题、正文、文件名、原始 URL、凭据或服务商载荷。 |
| 原 UI | 原有页面、入口和交互不变。 |

## 5. 本阶段不做，需求提高后再设计

本阶段**保持不变**：生产 assembly 继续直接装配邮件 handler；Controller 客户端、安装器、
发行投影继续以邮件为准；不新增通用 facade、测试用 qualification 模块或模块替换验收；
不删除 Python、不合并 Executor/Controller 进程、不改通信模式；不改共享 ISCP/JingSi/IMMS 契约。

唯一的约束是**不加深耦合**：新改动不得在 Executor、账本、Registry policy、HostDriver、HostPage
中新增邮件名或服务商分支（当前这些位置没有）。这样以后泛化的成本仍然很低。

| 触发条件 | 届时再设计的内容 | 起点（已知事实） |
|---|---|---|
| 出现第二个真实应用模块的需求 | 生产装配、安装和投影按受审模块清单驱动；公共应用调用入口；邮件路由转为适配层 | 已知耦合点：`A:runtimes/browser/src/assembly.mjs` 直接 import `mailHandlers()`；[app-cli-client.mjs](../../tools/browser-controller/src/app-cli-client.mjs) 固定构造 MailboxClient；[controller.mjs](../../tools/browser-controller/src/controller.mjs) 创建 MailObserverFeed；[main.mjs](../../tools/browser-controller/src/main.mjs) 混入邮件配置；[install-app-cli.py](../../scripts/install-app-cli.py) 按 `bindings/mail-*.json` 查找；[sync-app-cli-projections.mjs](../../scripts/sync-app-cli-projections.mjs) 固定邮件资产清单。 |
| 用户可感知延迟，或测得明确瓶颈 | 控制/页面通信与等待简化 | 已有常驻 machine session、直连 socket、页面 helper、读批处理的源码和测试，但未在安装版本上合格启用。可能已派发的请求不得 fallback 重发。 |
| 需要模块独立发布或升级替换 | 模块版本替换、旧 pending/uncertain 任务的兼容对账 | 现有恢复类别 `pure_read`、`replay_safe_local`、`external_write`、`subscription` 已表达执行语义。 |
| 需要远程或 API 形式的诊断 | 授权、owner 隔离与契约评审 | 以 S4 只读报告的字段为基础。 |

届时先写对应设计，经评审后实施；涉及跨项目接口或权限，先取得 InfiniCenter accepted 决策。

## 6. 汇报规则

每次实施记录目标、准确文件、保持的语义、复现、结果证据、剩余局限和回退边界，并更新第 2 节目标表的状态。
不能用测试总数宣布目标完成；单测只能证明 `source_verified`，不等于 `deployed` 或 `accepted`。
