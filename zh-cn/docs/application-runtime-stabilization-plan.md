# 应用执行底座：当前阶段稳定化方案

> 语言：简体中文 | [English](../../docs/application-runtime-stabilization-plan.md)

## 1. 结论与范围

**状态：实施中，2026-10-26，Asia/Shanghai；S1–S4 source_verified，S5 未授权。**

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
| G3 最小只读排障 | **S4 source_verified（2026-10-26）**：独立的 draft/operation/job 作用域报告，支持 file、只读 PostgreSQL、受保护 ledger 与既有日志。拒绝/首次 ACK 失败/重启及 unknown 重复查询后源字节、ACK/派发次数不变；owner/回执/冲突/隐私与资源上限验证通过。 | 已支持读取器的源码出口满足。预期/实际版本、执行/业务/效果/ACK 与证据缺口分列；实际加载版本、旧 job owner 绑定、trace/分析索引或合格 ledger 拒写保护缺失时仍 `incomplete`。未部署，非 S5 验收。 |
| G4 安装版本验收 | `.41` 已合格打包、未激活。 | 另行取得授权后，按延期验收清单在支持配置上完成，结论分别记录。 |

各 D 项的准确修复边界见[邮件诊断与修改边界](email-diagnostics-and-change-control.md)，本文不重复。
D05（job 完成与业务结果分离）和 D09（分析 attempt 索引）不阻塞本阶段出口，按邮件文档单独推进。

## 3. 实施顺序

1. **S1 修 D08**：先写复现测试（拒绝 → 首次 ACK 失败 → 重启/维护轮），再让维护循环覆盖
   带合格拒绝证明的终态结果。独立提交，不混入诊断改动。
2. **S2 保留归因**（邮件 M1）：D01、D03、D04。按本次授权，在 S1 后实施。
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

## 7. S4 已确认设计与实现 — source_verified

### 7.1 入口与范围

本地运维命令为 `sparkclaw-email-diagnose`，必填 `--owner`，并在 `--draft`、
`--operation`、`--job` 中选一个；用 `--format text|json` 输出到 stdout。
使用 `--operation` 时必填 `--identity-kind invocation|request-key`，避免隐式套用旧身份映射。
输入源为显式指定的本地路径或既有 PostgreSQL 连接配置；凭据仅从已有受保护配置或环境读取，
不打印、不接收命令行密码。ledger 输入另需 `--ledger-principal` 和 `--ledger-owner` 精确限定作用域；
不假定 Gateway owner 等于 ledger owner，缺少有依据的映射就记证据缺口。

这是 SparkClaw 邮件诊断适配器，位于 `services/gateway/cmd/sparkclaw-email-diagnose` 与
`services/gateway/internal/emaildiagnostics`；Node 26 SQLite 读取器唯一源码是
`internal/emaildiagnostics/ledger_reader.mjs`，嵌入 Go 命令，`tools/browser-controller/scripts/` 仅薄包装。
Go 以受限环境执行嵌入代码，不从 CWD 加载 helper，也不继承会提前执行模块或写覆盖率/缓存的 Node 环境。沿用现有 Go/Node 运行时和 schema fixture；不启动运行服务、
不新增外部 API/UI、不改 App-CLI `.41`。操作者须已有源数据读取权限；`--owner` 只收窄关联，
不授予权限。Memory Store 只作为进程内测试 fixture，不新增在线内存读取入口。

报告不 ACK、resolve、retry、reconcile、prune、迁移、续租、创建 epoch，也不导入生产 bootstrap。
恢复建议仅作说明，不包含可执行动作；unknown 永远禁止重发。D05/D09 不纳入本实现：
按现有证据分开显示 worker 完成与业务结果；分析 attempt 缺失就如实缺失，不改 attempt 索引或调度。

### 7.2 读取方式与资源上限

| 来源 | 已实现读取路径 | 缺失或不支持时 |
|---|---|---|
| Gateway PostgreSQL | 只读 repeatable-read 事务；参数化 owner/kind/id、parent/related 查询，使用已有 `email_management_records` 索引，仅取所需安全投影。不创建 Store 或运行迁移。 | operation → snapshot 没有专用 invocation 索引：按 owner 对 `send_snapshot` 做有界扫描并披露。超时/超限/不支持 schema 则 incomplete，不新增索引或无界扫描。 |
| 默认 file Store | 只读打开既有快照，流式解析 JSON，只保留目标 owner 的必要记录/字段。单体格式无随机访问索引，必须有界扫描。不调用 `NewFileStore` 或带迁移的 Store 读方法。 | 文件变更/截断、预算耗尽、不支持 schema 均 incomplete。加密快照首版记 `encrypted_source_unsupported`，不纳入密钥处理、整库明文导出或解密改造。 |
| App-CLI `.41` ledger | 独立 Node 原生 SQLite 只读连接、query-only 模式、固定 SELECT/schema-read 白名单。精确查询 `(principal, owner, request_key)` / task 和 `(task, sequence)`；投影安全字段，不整取 request/data/payload。 | 不实例化 `Ledger`：构造函数会改权限、schema、epoch 和未完成任务。不创建文件、切换 journal、checkpoint、恢复，也不使用忽略活动 WAL 的 immutable 模式。若 DB/WAL/shared-memory 组合无法在不写入/创建源文件的情况下打开，就返回 incomplete。 |
| 回执、Host/worker 日志、trace、健康信息 | 操作者指定已保留文件，有界流式读取、schema/字段白名单、精确身份关联；读取已有健康事件。 | 不爬远程 journal、不查服务商、不调用会初始化 sink 的在线 `latencytrace.Status()`，不调用 runtime 生命周期。不可用/过期/格式损坏/未读证据明确列出。 |

单次查询上限：工作截止时间 10 秒，选中 domain/event 记录 2,000 条，快照输入 256 MiB，
日志/trace 输入合计 32 MiB，单条解析记录 1 MiB，输出 1 MiB。跳过大 JSON 值也必须有界流式处理。
结果列出上限、检查的来源、已读条数/字节和截断位置；任何上限命中均 `incomplete`。
不自动放宽预算、不整库导出、不落盘原始输入。这些只是诊断资源上限，不是业务超时或保留策略。
各来源分别记录快照/证据出处，不声称运行中系统存在跨存储原子快照。CLI 监督自有进程组，
覆盖 SQLite 子进程；清理/超时输出另有 200/100 ms 有界等待。字节统计指逻辑源数据/投影字节，
不是 SQLite 物理页 I/O。变化/截断的快照、变化的日志/ledger 不作为证明；截断或冲突证据不能
提供回执 owner 绑定或补 ACK 资格。

生产 ledger 读取器在 macOS 使用 `sandbox-exec`，先拒绝文件写入及网络，再打开证据。
Linux 没有合格防护时返回 `ledger_write_guard_unavailable`，不打开 ledger；Linux 核心读取器
另在禁网、只读容器挂载下通过夹具验证，这不是生产绕过入口。Gateway file/PG/日志读取仍可用。
仅 SQLite read-only/query-only 不能保证 SHM 字节不变。

### 7.3 关联与解释

- draft → 冻结 send snapshot → 精确 invocation/request key → 限定作用域的 ledger task → 有序 task events。
  历史 snapshot 分别作为 attempt；当前 draft 不覆盖历史身份或结果。仅按 operation 查询时，可能找到了
  task 但 Gateway 反查不完整；这不证明 draft 从未存在。
- invocation 和 request key 分字段记录：`.41` 的 `op3` 直接传递，legacy invocation 则按固定版本的
  canonical digest 或已有 alias/head 记录映射。只读这些记录并验证 owner/app/command 绑定；不调用
  可能分配 alias/epoch 的 `MailboxClient.requestKey()`。缺失/冲突映射为
  `legacy_identity_mapping_missing`/`incomplete`。Host 散列取已验证的 ledger request key，
  不取未映射的 Gateway invocation。
- job → 已有 attempt/generation。旧 v1 job 日志缺少 owner 绑定，不能只凭 job ID/attempt 关联，
  明示 `job_log_owner_binding_missing`；operation 查询可单独读取精确持久采集 invocation 历史。
  只有既有边界证据能证明调用前失败时，
  才能表示未建 task；缺少 task 行本身不足以证明。D09 分析 attempt 索引缺失如实记录，不从邻近日志拼造。
- Host 必须同时匹配 task ID 与 S2 的 UTF-8 request key SHA256。Gateway draft 诊断使用
  owner + NUL + draft ID 的 SHA256。散列用于关联，不用于授权；排除其他 owner/principal 和非法身份。
  还须用 task 已有的 `request.arguments.owner_scope` 或等价留存绑定验证 Gateway owner；
  共用 ledger owner 本身不能证明产品 owner 隔离。request scope 为 SHA256(Gateway owner)。
  `.41` send 缺少该字段，替代绑定必须来自 owner 内持久回执，并精确匹配 task、terminal revision、
  result digest；显式存在但不匹配的 scope 不允许退回回执绑定。
- 使用 task sequence、receipt revision 和显式因果关系；按原始身份/revision 去重，冲突副本和乱序缺口
  仍可见。不按时间相近关联，不相减跨主机时钟得出耗时。
- `first_safe_cause` 指精确 attempt 因果链中最早有证据的原因；多个原因无法排序时列候选并标
  `incomplete`，不臆造首因。Controller/extension 错误只证明该边界报告的失败，不证明服务商/网络根因。

### 7.4 输出约定（本地报告 v1）

| 字段组 | 含义 |
|---|---|
| 查询/证据出处 | 重映射后的 owner 内引用、来源种类/schema、读取区间和观察出处；普通输出不含私有路径、凭据、原始查询文本。 |
| 版本 | 清单/配置中的 `expected` 与留存 task/run/process 证据支持的 `observed_loaded` 分列。安装文件或 `.41` pin 本身不证明执行时加载版本；缺失的实际版本为 incomplete。 |
| 阶段/原因 | 每次 attempt 最后有证据的边界；首个安全 code/stage/source 或无序候选；每个结论关联精确来源引用。 |
| 结果 | 执行状态、已持久化业务状态、观察到的业务结果、副作用确定性、ACK 状态、清理状态分开。HTTP/worker 成功不等于 sent；ACK 失败不是新的发送失败。 |
| 证据 | 各边界固定的缺失/冲突原因、已埋点覆盖和读取截断，缺失为 `incomplete`。`present` 只表示找到该项证据，不宣称端到端历史完整。健康转换计数是进程内当时快照，不是最终总数或重启连续计数。 |
| 恢复 | 仅根据有效既有证明说明资格：已拒绝结果的 pending ACK 可符合已有维护条件；unknown 可检查但不得重发。报告调用不执行两者。 |

解释示例：持久业务结果 `failed/not_sent`，task 有证据时执行结果 `failed`，ACK `pending`，
ACK 边界安全原因 `browser_extension_unavailable`，缺失 Host/版本事件的部分 `incomplete`。
这不证明发送因 extension 失败：原因绑定的是 ACK；不能压成一个成功/失败标记。

输出不含地址、主题、正文、文件名、原始 URL、凭据/session、服务商 payload 或原始 provider ID。
读取器在聚合前过滤私有内容；任意不安全 ID 做重映射，不拷入错误文本。原私有 artifact 访问另循原权限。
不新增常驻诊断存储或自动导出。

### 7.5 测试先行验收与修改卡

负责人确认后，先加入失败 fixture 再实现，验证：

1. S1 拒绝/ACK 失败/重启 fixture 被报告为持久拒绝与 pending ACK；重复查询不改变 ACK/派发次数、
   operation 身份和 unknown fence。
2. 静止 fixture 查询前后，domain 记录、ledger 行/events/epoch/authority marker、DB/WAL/shared-memory
   字节及源文件清单相同；权限/内容不变，操作系统读取导致的访问时间变化不算业务写入。
   拒写 fixture 和 mutation spy 阻止包括构造副作用在内的全部源写入；另测活动 WAL，无法无写读取时
   必须 incomplete，不能修复来源。
3. memory fixture、默认 file、隔离 PostgreSQL 输出等价安全证据；principal/owner 不匹配不能关联；
   并发 attempt、重启、重复/冲突、乱序事件不能相互借用原因或编造完成。
   覆盖直接 `op3`、legacy digest、已有 alias、缺失/冲突 alias、共用 ledger owner；查询不得创建或删除 alias/head。
4. 关闭/满载/损坏/缺失/过期日志、未知 schema、加密快照、超大输入、查询超时、实际加载版本缺失均有
   明确 incomplete 原因。安全原因白名单与恶意 payload fixture 证明隐私；超大字段下资源上限仍有效。
5. 预期/实际版本分离；读取不实例化 App-CLI/Store runtime；原公开错误、retry/backoff、unknown 保护、
   UI 源码保持不变。

**S4 实施修改卡：**见[邮件边界文档第 10 节](email-diagnostics-and-change-control.md#10-s4--最小-m3-修改卡--source_verified)。源码基线 `3793bfac` 保留已确认设计，实现另作一个提交。D05/D09 与 S5 仍不在范围内。

### 7.6 本地用法与证据局限

在仓库根目录，使用已有 Go 工具链，并将 Node 26 放在 PATH：

```sh
cd services/gateway
go run ./cmd/sparkclaw-email-diagnose --owner OWNER --draft DRAFT --snapshot /existing/state.json --format json
```

已有 PostgreSQL 连接用 `--postgres-env EXISTING_DSN_ENV` 替换 `--snapshot`，只需具有 SELECT 权限的凭据。
`--operation KEY --identity-kind invocation|request-key` 或 `--job JOB` 可替换 `--draft`。
可选既有输入：重复 `--log /existing/events.log`、`--release-manifest ../../vendor/app-cli/release.json`，
以及 `--ledger /existing/ledger.sqlite` 配合 `--ledger-principal`、`--ledger-owner`；legacy 身份另需
`--index-directory`。不下载或初始化来源；源路径/密码不进入报告输出。
退出 0 仅表示生成了有界报告，不表示邮件成功或证据完整；2 为输入无效，1 为命令/输出失败；
超时且输出可写时返回 incomplete 报告。

v1 始终声明 `evidence=incomplete` 与已埋点边界覆盖。没有可信留存加载版本 schema、清理历史、
trace/run 关联或分析 attempt 索引，这些字段保持 incomplete；预期 `.41` 清单不能证明安装。
加密快照、缺失/过期历史、不支持 owner 映射明确记缺口。不含服务商验证、在线 memory 入口、
新增审计库、恢复动作或 S5 验收。
