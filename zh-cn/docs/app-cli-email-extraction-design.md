# 复用 App-CLI 架构的应用控制扩展设计

> 语言：简体中文 | [English](../../docs/app-cli-email-extraction-design.md)

- 状态：**R3 实施中；公共生命周期核心首批完成，邮件迁移尚未完成**
- 日期：2026-09-29
- 首批应用：QQ 邮箱、Gmail、Outlook
- SparkClaw 调查基线：`6f4c97e4d0d7e03fab2de48661ddd6aed3034cd9`
- App-CLI 调查基线：`c9e49acd486fcbbbed0c0c3f398d6c7041a5a66f`

本文取代此前“通过 npm 包直接抽离邮件实现”的提案。目标是基于 App-CLI 已有架构建设可持续扩展的应用控制能力，邮件用于验证第一条完整链路。实现范围由目标架构决定，不能以少改文件为理由保留两套注册核心或绕开 App-CLI 的执行入口。

用户已授权开始编写代码。本 fork 已实现公共生命周期核心与显式 Runtime v2 客户端传输，具体范围见第 14 节；这不代表上游已合并。常驻执行服务、BrowserHostPort、邮件适配与生产部署仍为后续工作，不能从设计描述推断已实现。

## 1. 架构决策

1. **Python CLI / Registry 保持唯一的公共命令入口与能力目录。** 新应用通过现有 Manifest、Adapter、Registry、Schema 校验和 TaskResult 接入。
2. **JavaScript 是执行后端。** 复用邮件 JavaScript 实现，通过 RuntimeAdapter 调用；不创建与 Python Registry 并列的 JS 公共注册框架。
3. **新增能力进入 App-CLI 的正式扩展契约。** 写操作授权、异步任务和控制操作遵循与后端无关的统一生命周期扩展；RuntimeAdapter / Runtime v2 首先实现，不能经 npm 导出绕过 Registry 的准入。
4. **SparkClaw 保留通用浏览器宿主，应用业务层只作上层调用。** 应用控制逻辑归 App-CLI；任务页所有权、profile、Browser Controller / Bridge 和基础浏览器设施留在 SparkClaw，不维护供应商脚本、页面协议或邮件任务执行器。
5. **执行器拥有操作任务，SparkClaw 拥有产品任务。** 前者记录一次应用操作的受理、执行、证据和恢复；后者负责用户目标、审批、邮件同步/入库、模型分析与 UI。
6. **邮件是应用适配器的一组。** 后续应用使用同一公共契约；纯 API、原生函数或现有 CLI 适配器无需依赖浏览器、邮件模块或 Node 执行服务。

## 2. 现有架构的复用依据

以 App-CLI 的 [Architecture](https://github.com/Infinimesh-ai/App-CLI/blob/c9e49acd486fcbbbed0c0c3f398d6c7041a5a66f/docs/ARCHITECTURE.md)、[Runtime](https://github.com/Infinimesh-ai/App-CLI/blob/c9e49acd486fcbbbed0c0c3f398d6c7041a5a66f/docs/RUNTIME.md) 和对应代码为基线。

| 现有组件 | 保留职责 | 所需扩展 |
| --- | --- | --- |
| `cli.py` | apps / describe / schema、命令解析、JSON 输出、退出码 | 增加明确版本的机器调用入口，将执行上下文与业务参数分离 |
| `Registry` | 唯一应用目录、显式注册、Manifest 快照、输入/输出校验、分发 | 在现有准入点增加可信执行上下文与受控 mutation 准入；保留默认拒绝 |
| `Adapter` | 应用 Manifest 与业务调用边界 | 保留旧 invoke；显式扩展可信上下文和统一 LifecycleAdapter 能力 |
| Manifest 1.0 | 应用、命令、平台、输入/输出 Schema、副作用类别 | 保持原格式；执行绑定与宿主要求用独立、版本化的 binding 描述 |
| `RuntimeAdapter` | 受信任固定执行程序、JSON 传输、响应归属校验 | 增加显式选择的 v2 分支，复用现有组件 |
| Runtime protocol 1.0 | 一次 subprocess 调用、只读执行、严格消息校验 | 原样支持；v2 增加任务受理、查询、取消、对账、事件及授权绑定 |
| `TaskResult` | completed / pending / running / waiting_confirmation / uncertain / failed / cancelled / blocked | 保留八种状态及“仅 completed 可有业务 data”的规则 |
| `builtin_adapters()` | 显式组合可信应用 | 增加经审核的应用适配器与安装 profile；不扫描或执行任意插件 |

调查基线版本的 Python Registry 和 RuntimeAdapter 都拒绝 mutation。Runtime v1 请求上限为 64 KiB、subprocess timeout 最多 300 秒；SparkClaw 现有邮件正文上限为 200 KiB、`collect_page` 预算为 1800 秒，监听还需要跨调用驻留。**这些是扩展 App-CLI 执行契约的原因，不能靠重命名为 read_only 或提高一个超时值解决。**

应用适配器、接入后端和传输继续遵循上游的分层：邮件定义业务语义；浏览器后端完成页面操作；Runtime transport 将命令送到执行器。使用 RuntimeAdapter 的应用声明 `adapter.kind=runtime`，实际 browser 后端记录在执行 binding 中，不混淆两个层次。

## 3. 目标调用链与运行拓扑

```text
SparkClaw Workflow / emailmanagement / 其他产品能力
                         │ 业务命令 + 受控执行上下文
                         ▼
App-CLI Python CLI → Registry → 应用 Adapter
                         │
                RuntimeAdapter v2
                         │ 固定 Node 客户端，单次 stdin/stdout
                         ▼
App-CLI owner-local Executor
  操作任务记录 / 命令执行 / 结果验证 / 状态与事件
                         │
          QQ Mail / Gmail / Outlook / 其他应用实现
                         │ 通用 BrowserHostPort（browser 后端）
                         ▼
SparkClaw Browser Controller / Desktop / Browser Bridge
  任务页所有权、资源租约、基础浏览器操作、通用清理
                         │
                     实际应用
```

### 3.1 为什么需要驻留执行器

本方案选择一个按本地 owner 隔离的 App-CLI 执行进程，承接长操作和监听。它落实上游“executor 拥有持久任务”的边界：CLI 退出不意味着监听必须退出，查询任务不需要重新执行发送，30 分钟采集不需要把 Runtime v1 子进程阻塞 30 分钟。

RuntimeAdapter 仍启动固定客户端进行一次有界交换；该客户端连接已配置的 owner-local 执行服务，不能从业务参数接收程序路径或任意服务地址。执行服务由部署程序显式管理，不在 apps / describe / schema 或模块 import 时隐式启动。服务不在线时明确返回 unavailable。

执行服务是本次拟新增组件，而不是现有 App-CLI 功能。它只管理被调用的应用操作，不增加另一个 Agent、工作流规划器、邮件同步调度器或业务数据库。原生/Python/普通 CLI 适配器继续走已有直接调用路径。

### 3.2 避免依赖环

- SparkClaw 上层消费者只依赖 App-CLI 公共命令及机器协议。
- App-CLI 浏览器后端只依赖 BrowserHostPort 协议，不 import SparkClaw 源文件。
- SparkClaw 宿主只执行通用、受权限限制的浏览器请求，不回调产品 Workflow 再次执行同一命令。
- 授权提供方接口与浏览器操作接口分开；适配器不能通过页面内容获得更多权限。
- npm 包只负责交付执行器、后端和 JavaScript 制品；不向产品层暴露直接调用 provider handler 的替代入口。

### 3.3 已确认的产品边界

用户已采纳“应用控制逻辑归 App-CLI，浏览器页面所有权和基础设施归 SparkClaw”的划分。本文“只进行上层调用”指 SparkClaw 产品业务不再直接执行供应商脚本，并非迁走整个浏览器子系统。Browser Controller、Bridge、Desktop 宿主集成及用户页面保护继续归 SparkClaw；App-CLI 通过通用协议使用它们。此次不迁移浏览器基础设施，也不复制另一套生产浏览器宿主；独立参考宿主仅用于协议和可移植性验收。

## 4. 唯一能力目录与应用扩展方式

每个应用在 App-CLI 中拥有一个 Manifest 和一个显式注册的 Adapter。首批使用 `qq-mail`、`gmail`、`outlook` 作为拟议公共应用 ID；SparkClaw 现有 `qq_mail` 等值通过消费端兼容映射转换。ID 映射是机械投影，不含供应商页面实现。

拟议命令包括 `account.inspect`、`messages.discover`、`messages.capture`、`threads.inspect`、`messages.mark-read`、`messages.send`、`mailbox.watch`。当前 collect_page 可作为一个有明确完成条件的批量采集命令；最终支持清单以现有代码和测试逐项盘点，不因文档举例而增加未实现业务。

命令示意（属于目标接口，不是现有可执行用法）：

```text
app-cli apps
app-cli describe gmail
app-cli schema gmail messages.send
```

新应用的接入步骤固定为：

1. 定义业务参数、输出、效果分类和完成证据，编写 Manifest。
2. 选择已有 Adapter / RuntimeAdapter 与 API、CLI、浏览器等后端；已有后端确实不能表达时才扩展公共后端契约。
3. 为需要外部执行器的应用编写执行 binding 和操作实现。
4. 将应用加入审核过的显式注册集合，提交应用与公共契约测试。

使用现有宿主能力的新应用，不应修改 SparkClaw Controller 的 provider/operation 白名单。应用新增产品入口、UI 或新的宿主原语时，允许有对应消费端工作，不能把“任何新应用都零修改”作为承诺。

### 4.1 权威来源

App-CLI 仓库是公共 Manifest、Runtime Schema、execution binding 和 conformance fixtures 的唯一来源。Python 从这些文件注册；Node 从相同发行版本加载、校验受理请求，并根据固定 handler mapping 执行。Node 内部 handler mapping 不是第二个对外能力目录。

生成 Go 类型、文档、错误码投影或宿主资产清单时记录来源版本和摘要，禁止手写重复定义。校验可以在不同进程重复执行，规则与 fixtures 必须一致。JSON 重复键、非有限数值、未知字段和无法精确表达的数据类型在共同协议中明确处理，不依赖不同语言的默认解析行为。

## 5. Manifest 与执行 binding

Manifest 1.0 的闭合 Schema 保持不变。拟新增 `execution-binding/v1`，按应用 Manifest 摘要绑定以下内容：

| 字段组 | 内容与约束 |
| --- | --- |
| 应用身份 | app ID、应用版本、manifest digest；不匹配则不注册执行 |
| 执行方式 | 显式 runtime protocol major、可信 executor 类型/发行身份、backend kind |
| 命令映射 | 公共 command → 内部固定 handler；不能由调用者提交脚本路径 |
| 参数与预算 | 参数摘要规则、最大请求/结果、业务 deadline、RPC budget |
| 宿主能力 | host protocol/version、所需原语、页面/账户/会话模式 |
| 资源声明 | 允许 origin、下载 origin、资源互斥键的结构、共享/独占要求 |
| 效果声明 | 本地页面/文件变化、远端写入、需批准的业务效果及证据要求 |
| 初始化资产 | 有版本和摘要的 Reader、导航前脚本、执行环境及阶段 |
| 兼容范围 | 支持的应用/浏览器后端版本、已验证的宿主组合 |

side_effect 反映整个操作的效果：页面准备或写文件不能隐藏为 read_only；包含远端变化的操作按 remote_mutation 准入，binding 补充本地效果。“读取”的业务名称不决定效果级别。

能力发现只读取本地已安装契约，不连接邮箱、不验证登录、不打开页面。环境/账户预检是单独调用；改变本地页面状态的预检同样需要对应授权。

## 6. 公共入口与 Runtime v2

### 6.1 入口与上下文

复用 Python CLI，拟增加显式 `--machine` 入口：stdin 读取一个版本化请求，stdout 返回一个结果。现有命名参数、--input、发现命令和 v1 输出保持原语义。机器请求与旧参数不能混用；凭据、正文和完整授权材料不得放入 argv。

Registry 增加受信任的可选执行上下文，旧调用不传上下文时维持现有限制。需要上下文的 Adapter 显式实现扩展接口；Registry 不向旧 invoke 偷塞参数，也不靠捕获 TypeError 猜测兼容性。

机器调用至少包含以下逻辑字段；准确 JSON Schema 须在实施前经决策 0030 评审冻结：

| 字段 | 责任 |
| --- | --- |
| protocol_version、operation | 明确机器协议版本与操作，不自动降级 |
| app、command、arguments | 业务身份与参数，使用 Manifest 校验 |
| request_key | 按已认证主体/owner 隔离的一次原始意图稳定键；超时不换键 |
| authorization_ref | 可信授权提供方可验证的不透明授权引用，不是自报 approved |
| deadline、limits | 普通任务的固定预算，执行器只能缩小；仅显式可续期 watch 可省略总 deadline，仍受有限授权窗口及资源租约约束 |
| task_id、cursor | 对原任务的控制/查询使用，不作为独立访问权限 |

调用者身份来自已认证连接/授权提供方。浏览器 socket 地址、可执行程序、文件根目录来自受信任配置或授权解析，不能通过业务参数替换。

### 6.2 操作集合

| v2 操作 | 行为 |
| --- | --- |
| invoke | 校验并持久受理一次原始业务意图；可快速完成或返回 task ID |
| lookup | 用原 request key 查找是否已受理；不重新提交 |
| status | 查询原任务；完成时携带通过原命令输出 Schema 的业务结果 |
| cancel | 请求停止原任务；确认受理取消不等于已经 cancelled |
| resume | 为原 waiting_confirmation / blocked 任务提交更新的授权引用；重新准入后才继续，不能改原业务参数或重放 uncertain 的写入 |
| renew | 仅对声明可续期的活跃任务更新授权窗口；绑定原 task/意图/账户范围，不改变参数，不复活已失效任务，不延长固定业务 deadline |
| events | 按 task-local sequence/cursor 获取有界事件，不返回原始页面或秘密 |
| reconcile | 对原任务补查独立完成证据；不得创建第二次业务写入 |

全部经过同一个 App-CLI 公共入口和授权路径；所有应用复用统一任务控制 API。事件页/取消 ACK 的控制结果与“原任务已 completed”分开表达。

这些操作的语义来自统一生命周期契约，Runtime v2 是跨进程映射。其他适配器经审核实现同一扩展时，Registry 直接分发到其生命周期接口，不必伪装为 runtime 后端。

机器入口区分 RPC 成功与业务状态：合法的 pending/running 等结果仍可作为成功控制响应，业务是否完成只看 TaskResult。新增 Registry 控制接口返回完整状态；原 execute / execute_with_metadata 将非完成状态转为 AppCLIError 的行为保持兼容。稳定失败原因放在 v2 错误元数据，不向旧 TaskResult 随意加字段。查询/取消使用独立的任务访问权限；原执行授权过期阻止新副作用，不应让合法拥有者失去查看或停止任务的能力。

### 6.3 有界传输与长任务

- Runtime v1 的 64 KiB 请求、1 MiB 响应与原 timeout 规则不变。
- v2 首批机器请求上限拟为 2 MiB，序列化业务参数上限 1.5 MiB；邮件正文沿用各入口现有上限（最高 200 KiB）。预算必须覆盖 JSON 转义膨胀，不能把 200 KiB 正文等同于 200 KiB 传输；序列化后的实际字节数也必须过整体预算。文件及附件使用经验证的引用，不内联请求。
- v2 单次受理/控制 RPC 最长 15 秒，固定客户端 subprocess 预算 30 秒；业务 deadline 独立，例如 collect_page 最高仍为已授权的 1800 秒。未完成时查询原 ID，不让 RPC 占用到业务结束。
- v2 单次响应最多 1 MiB；stdin/stdout 和本地 socket 在读取过程中限流，不能捕获无限输出后才检查长度。
- Executor 将任务状态与日志保存在 owner 私有目录；发送内容按任务所需最小范围保存，沿用私有文件权限；日志只记录脱敏诊断。凭据引用失效后阻止继续执行，不扩大权限恢复。

具体限额是拟议 v2 合同，需通过多字节正文、边界参数和实际邮件 fixtures 确认；不得缩小现有业务允许范围而不记录兼容变化。

## 7. 写操作准入与责任归属

### 7.1 正式扩展现有默认拒绝规则

现有 mutation 拒绝保留为默认策略。写操作只有同时满足以下条件才允许执行：

1. 显式注册的受信任 Adapter 实现经一致性验证的统一生命周期扩展（本文称 `LifecycleAdapter`），契约与安装发行身份匹配。Registry 检查扩展能力及审核注册信息，不按 `RuntimeAdapter` 类名或 adapter.kind 放行。
2. Registry 的准入策略解析可信授权，绑定调用主体、app/command、规范化参数摘要、账户/资源范围、deadline 和 request key。
3. 对应执行后端验证相同授权绑定并持久受理，在资源领取及实际副作用前再次检查执行权和权限；浏览器后端由 Executor / Browser Host 完成，其他后端承担等价责任。

`LifecycleAdapter` 是现有 Adapter 的显式扩展，不另建 Registry。公共契约覆盖可信上下文、持久意图/task 绑定、目标串行化、执行权隔离、deadline、取消、授权恢复、完成证据和对账；长期监听再声明 renew/events 能力。仅增加一个自报 capability 布尔值不构成合格实现，注册集须绑定实现、版本及一致性测试证据。

RuntimeAdapter v2 是首个生产实现，Node Executor 是浏览器应用的首个执行后端。未来 native-api / http-api / cli 适配器可在 Python 或自己的受控执行后端实现同一契约，复用 Registry 策略，不被迫依赖 Node、BrowserHostPort 或改为 runtime 类型。单次 Python 调用不得留下无明确所有者的后台任务；异步任务必须有持久后端接管。现有只读适配器不因本次设计强制改造。

单纯添加 --allow-write、参数 approved:true、更换 adapter.kind，或直接 import handler 都不构成准入。默认无上下文、授权失效、旧 v1 Runtime 和无生命周期支持的适配器继续拒绝写操作。

SparkClaw 实现授权提供方，将已有用户批准与策略结果投影为受约束授权引用。网页内容、模型生成参数和错误消息不能成为授权来源。其他产品可实现同一提供方协议，App-CLI 不读取 SparkClaw 内部数据库。

授权按效果区分：在用户已允许的账户和任务范围内，普通本地页面准备可由既有策略授予；远端发送仍绑定精确收件人、正文等批准内容。local_mutation 的正确分类不意味着每次读取都要弹出人工确认。容器/宿主连接也须认证实际调用主体，不能把“能连接同一 socket”视为拥有全部 owner 权限。

### 7.2 不重复拥有同一种状态

| 状态/资源 | 权威所有者 | 另一方可保存什么 |
| --- | --- | --- |
| 用户目标、审批、业务同步 job、分析/入库状态 | SparkClaw | App-CLI 只持本次授权与必要参数 |
| 应用操作 task、request-key 绑定、执行尝试、事件与业务回执 | App-CLI Executor | SparkClaw 保存 task ID、最后观察状态与结果引用 |
| 任务页、profile、宿主 generation、资源锁、进程清理 | Browser Host | Executor 持有受限且可撤销的 lease |
| 供应商发送 journal、捕获格式、完成证据解释 | App-CLI 应用实现 | SparkClaw 验证公开结果/制品，不另写恢复解释器 |
| 制品存储根、访问授权与保留/删除策略 | 宿主/产品配置 | Executor 仅访问获得授权的任务空间 |

通用 task ledger 记录执行身份与状态；供应商 journal 记录提交/证据步骤，由同一个 Executor 管理并引用。不能让两端分别决定再次发送。页面清理不删除持久 task ledger 或发送 journal。

## 8. 任务、超时、取消和对账

受理前建立 request_key → canonical intent digest → task_id 的持久绑定，随后才可排队或申请任务页。相同键、相同语义返回同一任务；相同键语义变化明确冲突。调用者只重取原任务，不自动建立第二次尝试。

绑定按主体/owner 隔离；正文和制品过期可按策略删除，但保留阻止重复执行所需的最小记录或墓碑。历史过期不能把旧键当新请求；账本丢失/损坏时停止相关写操作，不能用空账本推断过去未受理。具体保留期、事件容量和存储配额须在冻结协议时明确。

| 情况 | 对外结果与后续动作 |
| --- | --- |
| 输入/授权/兼容不通过，尚未受理 | 明确拒绝，零任务执行；不能混同传输故障 |
| 受理 RPC 回包丢失，尚无 task ID | 错误保留原 request key，要求 lookup；不伪造无 ID 的 TaskResult uncertain |
| lookup 找到原任务 | 返回同一 task ID，继续 status/events |
| lookup 不能确定历史受理 | 保持 unresolved，禁止重新 invoke；只有持久的未受理证明才能结束该请求 |
| 已受理、尚未执行/正在执行 | TaskResult pending / running，必须有 task ID，无成功 data |
| 需要批准或授权更新 | waiting_confirmation；原任务停在副作用之前，不新建意图 |
| 副作用可能发生但结果不明 | uncertain，保留原 task 和 journal，通过 reconcile 查询证据 |
| 请求取消 | ACK 只表示 stop intent 已记录；Host 停止或清理后再判定状态 |
| 取消/超时在可能提交之后 | 未获得独立证据前保持 uncertain，不能直接声称 cancelled 或未发送 |
| 已有成功证据，取消稍后到达 | 保持 completed，终态与回执不能改写 |
| 业务完成且输出有效 | completed + 原命令 Schema 校验后的 data；入库/分析属于 SparkClaw 后续阶段 |

复用 TaskResult 八态，不新增竞争的成功布尔值。deadline 后的已知失败映射 failed 并带稳定原因；无法判断副作用时映射 uncertain。重启后逐任务恢复：能证明未开始的任务重新验证授权后调度，可能已执行的任务先对账；不能一律重放 invoke。

mailbox.watch 活跃时为 running，事件以有界序列交付；取消和清理完成后为 cancelled。CLI 拿到 task ID 不等于监听已完成。SparkClaw 的同步调度继续消费提示，Executor 不自行定期扫描全邮箱。

事件允许至少一次交付，消费者按 task/sequence 去重；事件压缩、队列溢出或 cursor 过期必须返回显式 gap。SparkClaw 收到 gap 后通过原有同步机制补查，不能把丢失提示视为“没有新邮件”。事件、日志和终态结果的保留与删除不得破坏上述请求键防重约束。

### 8.1 执行器交接与旧执行隔离

首版只支持单机、每个 owner 一个有效执行器，不增加多机选主。执行权由同一 owner 状态目录的进程级排他锁和持久单调递增的 `execution_epoch` 共同确定；不能仅凭 PID、socket 文件或启动时间判定。进程退出可释放锁，但幸存子进程和已发出的页面操作仍须隔离。

接管顺序固定：

1. 取得排他锁；旧执行器仍持锁时返回 busy，不能删除锁文件强行并行启动。
2. 在原持久状态中原子分配并落盘新 execution_epoch，保留安装身份、请求键及 journal；状态不可验证时停止接管。
3. 与 Host 握手。Host 持久记录该 owner / 安装身份的有效 epoch，原子撤销旧 epoch 的 lease 和排队动作；每个调用、事件及任务状态提交都校验代次。epoch 本身不是授权凭据。
4. Host 停止旧观察 hook、受控子进程和相关页面动作。已发出的远端请求不能靠撤销收回：保存证据并将结果不明的原任务置为 uncertain，先对账。清理未证实完成时隔离对应资源，禁止新任务在同一冲突目标上执行。
5. 旧执行已隔离后，恢复合格原任务并允许新操作。恢复不新建发送意图；旧代次结果不能覆盖新账本，但可作为带来源的对账证据。

Host 重启时更新自己的 host generation，使之前的所有租约失效；重新握手、核验遗留任务页后才发新租约。回退软件不回退 execution_epoch 或删除执行权记录。账本备份过旧、Host 与账本代次冲突等情形必须进入显式恢复，不能更换安装 ID 后当全新系统运行。其他 LifecycleAdapter 后端须提供等价的旧执行隔离；无法阻止旧执行时阻止新执行，不宣称实现了 exactly-once。

### 8.2 长期监听、续期与失联处理

区分三种有效期：业务 deadline、执行授权窗口、Host 资源租约。普通命令的 deadline 不可续长；仅 binding 显式声明可续期的 watch 可无固定总时长，但每次授权和资源租约都有限期。“一直监听”不等于无限期授权。

首版拟采用以下默认预算，作为发布配置和契约测试输入，正式值在阶段 1 冻结：Executor 向 Host 每 10 秒发心跳，资源租约最多 30 秒；watch 授权窗口最多 5 分钟，SparkClaw 在到期前 60 秒申请续期。Host 用单调时钟控制本地租约时长，实际有效期不得超过授权或固定业务 deadline；授权绝对过期检查按授权提供方契约执行。

| 事件 | 必须执行的行为 |
| --- | --- |
| 正常续期 | SparkClaw 核验监听仍启用、账户/权限仍有效后，调用公共 renew 提交新授权引用；绑定原 task、参数摘要与资源范围，Executor / Host 验证后更新窗口 |
| Executor 心跳 | 仅证明当前 execution_epoch 活跃；可在现有授权窗口内维持 Host 租约，不能创造新授权或延长业务 deadline |
| renew 回包丢失 | 按原 task 和授权版本查询已生效窗口；无确认时不能假定已延长，不建立第二个 watch |
| 执行授权过期 | 停止新的页面采集/事件转发并清理观察 hook，原 task 进入 waiting_confirmation；有效任务访问权限仍允许查询/取消，重新授权后走 resume |
| 凭据代次变化或授权撤销 | 收到或校验发现变化后立即撤销受影响活动的租约，不等待心跳宽限；凭据更新经重新核验后才可 resume。产品明确停用监听则记录取消意图并清理 |
| Executor / Host 连接中断 | 不再接收新动作；租约到期最迟触发 Host 清理与资源隔离。可观察到的任务状态为 blocked 并带原因；状态不可达时客户端报告不可达，不能猜测已停止 |
| 重连 | 同一 epoch 在租约有效期内核验后可继续；租约已失效必须完成清理、重领和页面校验，旧 lease 不可复活。新 epoch 按 8.1 接管 |
| 超过恢复预算 | 首版连接恢复预算拟为 60 秒，自检测到故障时计；期间也不得超过租约/授权期限继续工作。预算耗尽且已证实停止则 failed；结果不明或清理未完成则保持可查询的 blocked/uncertain 与资源隔离，不假报终态 |

并发故障按证据决定状态：已确认的 completed 不改写；可能产生远端副作用优先 uncertain；清理/旧执行隔离未证实则 blocked 并保持资源隔离。只有已证实停止或隔离后，才能按具体原因报告 waiting_confirmation、failed 或 cancelled，不能仅因授权到期或取消 ACK 宣称已停止。

续期以授权版本防止旧请求覆盖新窗口。active watch 使用 renew，waiting_confirmation / blocked 使用 resume；已 cancelled / completed / failed 的 task 不复活。终态后如监听仍应启用，由 SparkClaw 明确创建新的监听会话，保留前一 task 的关联及补查边界；仅适用于监听，不成为自动重发远端写入的机制。

每段暂停、重领页或代次变化都记录事件 gap 和最后持久游标；恢复后 SparkClaw 使用原同步水位/时间回看机制补查，再继续消费提示。共享页面上的 read 与 watch 分别持有活动授权和租约引用，watch 失效只撤销其活动；其他活动仍有效且清理成功时保留页面。无法隔离清理失败时隔离整页并通知受影响任务，不关闭用户页面。清理不依赖 SparkClaw 或 Executor 仍在线，Host 必须能够在租约到期后自主执行。

到期停止还必须落实在实际持有资源的受控执行端：页面观察 hook、Bridge/preload 和受控 worker 需要租约看守或可验证的监督退出，不能只有 Controller 内存中的定时器。Controller 崩溃或挂起也不能让旧任务无限运行。已发出的远端请求仍按 uncertain/对账处理，不宣称到期能撤回请求；后端无法证明按期隔离时，不得通过长期监听能力验收。

## 9. 浏览器宿主协议与任务页时序

BrowserHostPort 是通用、版本化、owner-local 协议，只接受已认证 Executor 的受限请求，并绑定 owner / 安装身份、execution_epoch、task ID、资源范围、发行身份、host generation 和 lease。不暴露任意 owner tab 枚举或整个浏览器控制对象。

### 9.1 公开原语

- capabilities：返回 Host protocol、支持的原语、浏览器/Bridge/preload 能力与发行摘要。
- acquire：按已批准资源声明申请宿主拥有的任务页；返回 opaque lease 与 generation。
- handshake / heartbeat / renew_lease：核验执行权及宿主代次，在已验证授权内维持有期限的活动租约；不能替代公共 renew 获取新授权。
- install_assets：按固定 asset ID/digest 和声明阶段安装受信任脚本；不接受任意来源 URL。
- navigate / read / input / execute / download：在 lease 范围内执行；限制 origin、输出、文件根目录和预算。
- subscribe：向 Executor 交付有界页面事件，带文档身份、序列和丢失提示。
- effect：执行前验证精确批准内容和权限仍有效，记录副作用尝试；发送业务验证由适配器完成。
- park / release / revoke：由宿主统一处理资源保留、释放、撤销及失败后的禁止复用。

这些是协议职责，实际绑定复用 Controller/Bridge 的实现。通用原语由 Host 实现，供应商初始化与操作代码由 App-CLI 发行资产提供。脚本是经审核的可信代码，不把 npm 模块或局部接口误称为操作系统沙箱。

### 9.2 固定调用顺序

| 阶段 | App-CLI Executor | SparkClaw Browser Host |
| --- | --- | --- |
| 0. 准入 | Registry/Executor 校验命令、授权、版本和任务绑定 | 校验自身能力与执行发行兼容 |
| 1. 声明 | 从 binding 读取资源和初始化资产要求 | 尚不导航，不运行供应商代码 |
| 2. 领页 | 用 task/grant 申请 lease | 创建或匹配任务页，保留 owner 页面隔离 |
| 3. 导航前 | 指定已注册初始化资产及其阶段 | 安装 dormant observer、后台准备和 Outlook early bridge |
| 4. 导航 | 指定允许的入口与就绪条件 | 导航；保持 Reader document-start 和正确执行环境 |
| 5. 准备 | 账户/Reader 检查与 resetRound | 校验文档和 generation，拒绝失效 lease |
| 6. 执行 | 业务 handler、journal、完成证据验证 | 受限浏览器动作；副作用前检查 effect gate |
| 7. 保留/结束 | 声明继续 watch 或任务结束 | 决定共享/park/release，承担最终清理 |

复用页跳过重新创建与导航，但不能跳过账户、文档身份、凭据代次和发行摘要校验。创建/销毁通过 Host 完成，Executor 不直接启动另一份浏览器或关闭用户页面。

### 9.3 必须保留的现有行为

- 原 Chromium/Playwright、Browser Bridge、profile 与 Electron preload 执行环境不随代码归属调整而替换。
- QQ/Gmail 的有界原生读取与 Outlook 的 UI 等待采用不同完成策略；策略由应用 binding 声明，Host 执行通用机制。
- 读取与监听继续共享合格任务页；两种启动顺序均可用，读取结束不误关监听页面。
- 保留账户/owner/凭据代次、document nonce 和脚本/监听摘要检查；失效时撤销旧 lease。
- 页面关闭、CLI 停止、daemon 回收、临时状态移除沿用受控顺序；失败资源在成功清理前不可再分配。
- 升级改变代码/资产摘要时排空旧执行并重建后台页，不承诺热替换零中断。

## 10. 代码归属与消费端改造范围

| 现有位置 | 目标归属/处理 |
| --- | --- |
| scripts/email、Reader 构建源码 | App-CLI 应用实现、后端与资产构建 |
| provider-scripts.mjs | 业务定义进入 Manifest/binding；固定 handler mapping 归 Executor |
| mail-notification-rules.mjs、mail-observer-page.mjs | 应用的通知解析与资产 |
| mail-observer-runtime.cjs、awaited-mail-read.cjs | 供应商策略归 App-CLI；宿主只实现通用 hook 与安装点 |
| cli-task.mjs | 应用桥接、供应商等待和查询迁出；通用 Playwright 执行留 Host |
| controller.mjs | 移除三家 provider / 邮件 operation 硬编码，按授权 binding 分配通用资源；不能直接删白名单而取消准入 |
| cli-runtime.mjs | 邮件秘密字段定义与输入转换迁入应用 binding/实现；进程与秘密槽通用机制留 Host |
| cli-client.mjs | 应用 dispatch、Reader 映射及邮件流程迁出；连接、通用调用和资源交付留 Host |
| mail-observers.mjs、mail-read-pool.mjs、mail-observer-feed.mjs | 监听语义归 Executor；Host 保留通用租约、共享资源、事件传输与背压 |
| tools/browser-userscripts/*-mail-reader.user.js | App-CLI 发行资产；SparkClaw 消费制品，不保留可编辑副本 |
| Go emailautomation 的执行接入 | 改为 App-CLI 机器客户端与结果映射；审批/账号配置/产品错误映射保留 |
| provider_scripts.json、错误码源码扫描测试 | 从 Manifest/binding/fixtures 生成投影，不依赖旧脚本源码 |
| Browser component / Desktop preload 构建 | 消费同一发行清单的资产；宿主可保留生成物 |
| Gateway Dockerfile、host setup、CI | 配置 CLI/固定客户端/执行服务，清除旧脚本路径依赖 |
| emailmanagement、Store、模型分析、WebChat | 保留产品职责，调整 task ID 绑定和事件消费，不迁到 App-CLI |

原 Go RunScript 协议是内部兼容面，可由薄映射层过渡。最终产品调用只能进入 App-CLI Registry；不能永久保留直连 Node handler 的旁路。现有脚本 ID/revision 用于追溯与过渡，不作为新公共业务 API 的中心。

## 11. 仓库结构、安装与一致性

fork：ZZZZJJJ0928/App-CLI，upstream：Infinimesh-ai/App-CLI。现有本地分支 codex/sparkclaw-email 表示本批工作，不限定后续能力；SparkClaw 分支为 codex/extract-email-app-cli。没有迁移提交被推送。

拟议结构：

```text
App-CLI/
  src/app_cli/
    core.py, cli.py, tasks.py          # 继续作为公共核心
    adapters/runtime.py               # 显式支持 v1 / v2
    adapters/...                      # 经审核的应用注册与绑定
    manifests/...                     # 发布后的权威 manifest/schema 副本
  schemas/                            # Manifest、Runtime、binding、Host schemas
  applications/
    gmail/manifest.json, binding.json
    qq-mail/manifest.json, binding.json
    outlook/manifest.json, binding.json
  executors/node/
    stdio-client.mjs                   # RuntimeAdapter 固定调用目标
    service/                          # 操作 ledger、控制协议与恢复
    backends/browser/                 # HostPort client，不 import SparkClaw
    applications/...                  # 供应商实现、Reader、journal、验证
    assets/                           # 已构建脚本及摘要
  tests/conformance/                  # 核心、执行器与宿主共同 fixtures
  release/                            # 成套发行构建说明/清单
```

这是一套公共核心和多个后端，不设 js/core 平行能力目录。npm 可分发 Node executor；Python wheel 与 Node 包由同一发行版本构建。具体 npm 名称不影响命令契约，也不要求先发布到公共 npm registry。

### 11.1 成套发行

发行清单至少包含源 commit、Python 包身份/hash、Node executor 包身份/hash、Manifest/binding digest、Runtime/Host 协议版本、Reader/初始化资产 digest 及测试证据版本。固定源 SHA 不能替代最终构建产物摘要。

构建时生成并校验 Manifest/schema 的 Python 包内副本、Go 投影、Reader 与 Desktop preload。安装顺序为准备全部制品 → 校验清单 → 停止接收新操作并排空旧执行 → 安装匹配组件 → 兼容握手 → 恢复消费。失败保留原匹配发行组，不运行混合版本。

启动/重连检查 Registry 所用 manifest、Executor 加载 binding、Host 能力及实际 Reader/preload 身份。主版本或必需能力不兼容时，阻止对应 adapter 创建任务页并返回稳定错误；普通非相关浏览器功能不因某个邮件 adapter 不可用而停摆。

### 11.2 部署与回退

Go Gateway 沿用挂载到容器的、已鉴权的 owner Controller socket。owner-host Controller 调用固定 App-CLI Python 客户端与 owner-local Executor，Python/Node 安装在宿主发行目录。容器不执行任意宿主路径；保留既有产品边界，同时使 Registry 成为应用准入权威。

回退恢复整组匹配制品，同时检查 ledger/journal 可读兼容版本。未知状态格式阻止接管，不能删除记录、清空 capture 或重发任务来恢复。授权、profile、登录态和用户原文不进入源码仓库或发行包。

迁入的 SparkClaw 源码、测试和衍生资产保留 Apache-2.0 与来源；原有 App-CLI MIT 代码保留原许可。两者在目录与发行清单中明确标识。

## 12. 六项问题的重新评估

| 旧问题 | 架构原因 | 本版处理 |
| --- | --- | --- |
| Python / JS 两套核心 | 以 npm 文件抽离为起点，未贯穿现有入口 | Registry 唯一公共目录，Node 仅作 Runtime 执行器 |
| 宿主仍硬编码邮件 | 未盘点消费协议和调度 | controller、cli-runtime、cli-client 及 Go 执行入口纳入调整 |
| 生命周期只有原则 | 声明、领页与导航前安装关系不明 | HostPort 原语与固定阶段顺序，明确宿主所有权 |
| 写操作和恢复模糊 | 绕过 mutation 拒绝，直接调用旧脚本 | Runtime v2 准入、受理、lookup/取消/对账与状态所有权 |
| 版本固定但可混用 | 单一 Git pin 不覆盖实际加载状态 | 成套摘要、启动/重连握手、整组回退 |
| 验收只证明邮件迁出 | 未证明新应用复用入口与后端 | 非邮件、非浏览器适配器与独立宿主验收 |

先前遗漏的请求大小、长操作和长期监听也已转为协议约束。以上是文档层面的解决方案，不能表述为实现或测试已通过。

## 13. 实施与验收顺序

| 阶段 | 交付物 | 验收出口 |
| --- | --- | --- |
| 1. 冻结扩展合同 | 统一 LifecycleAdapter、Runtime v2 映射、上下文、binding、HostPort、授权与状态 fixtures | 决策 0030 accepted；执行代次、续期/失联预算及身份、效果与状态责任无歧义 |
| 2. 扩展现有核心 | Registry/CLI/RuntimeAdapter 兼容扩展与单一目录 | 原 60 项测试通过；无授权 mutation 仍拒绝；v1 回包不变 |
| 3. 执行器与宿主 | Node 客户端/Executor、持久任务、HostPort、独立参考宿主 | 非邮件应用从 app-cli 走通；受理丢包、重启、取消、清理符合合同 |
| 4. 邮件应用 | 三家 Manifest/binding、脚本/Reader/journal/验证迁入 | 现有 provider 测试迁入；独立安装不依赖 SparkClaw checkout |
| 5. SparkClaw 切换 | 通用客户端、结果映射、旧脚本删除与部署 | 上层操作走 Registry；任务页/产品回归通过；发行组可回退 |

这是按架构依赖推进的顺序。旧路径只在切换期保留，不作为长期双实现；不能用“后续统一”维持两套公共执行入口。

### 13.1 架构验收

- apps / describe / schema 可发现新应用；发现不启动执行服务或浏览器。
- 新增使用已有 HostPort 的非邮件 fixture 应用，不修改 SparkClaw 调度和公共 Registry 逻辑即可注册、执行和验证。
- 原生/CLI fixture 无需安装浏览器和邮件依赖，验证可选后端边界。
- Python 原生适配器使用自有本地可变 fixture 实现同一 LifecycleAdapter 准入/任务契约，授权后可写、缺授权被拒绝，且无需 RuntimeAdapter、Node 或浏览器；未实现该契约的适配器仍拒绝写操作。
- 使用 App-CLI 自有真实本地 fixture 页面及参考 HostPort，从仓库外安装后运行完整命令，证明不依赖 SparkClaw 内部模块。
- Python 与 Node 使用同一 Schema/fixtures；结果身份、大小、错误和状态校验一致；Node 内部调用不能接受未注册、未授权业务。

### 13.2 可靠性验收

- 相同键重放、参数漂移、受理回包丢失、无法确认的 lookup、重启均不得产生第二次远端写入。
- 已知未执行、已知失败、结果不确定、成功不能因 timeout/cancel 混淆；原 task 和 journal 可追溯。
- 200 KiB 正文、多字节内容、附件引用、30 分钟操作与长期监听有独立边界测试；RPC 与任务 deadline 分开验证。
- 授权恢复仅继续原任务；过期执行授权下仍可合法查询/取消；事件 gap 补查、跨 owner 隔离和防重记录保留均有契约测试。
- 执行器重复启动、崩溃后幸存子进程、Host 重启、旧 epoch 延迟请求/结果和旧账本回退，均不得取得重复执行权；清理失败阻止冲突资源重新分配。
- watch 续期成功/回包丢失、SparkClaw 离线导致授权过期、心跳失联、凭据更新、撤销、恢复预算耗尽及 gap 补查均有确定状态与时间边界；watch 清理不误关仍有有效 read 租约的共享页。
- 升级及组件混用时拒绝不兼容 adapter；失效授权、错误账户、过期 lease 和未知任务不能继续操作页面。

### 13.3 浏览器与产品验收

- 三家邮箱冷启动、连续复用、读取/监听两种启动顺序、Outlook 导航前注入、QQ 文档替换、断连与清理失败恢复。
- 发送精确批准绑定、未知结果对账、下载完整性、取消后的实际页面/进程状态。
- 普通浏览器任务、用户页面、原登录态、邮件入库/分类/UI 与 JingSi Runtime v1 行为保持。
- 干净安装、容器客户端到宿主执行器连通、Reader、Desktop preload、生成投影和回退完整验收。

离线 fixtures 只能证明相应合同与模拟场景。真实邮箱发送、下载、监听的范围和结果单独记录；设计评审和文档检查不执行真实邮箱操作。

## 14. 已实现发行与验收边界

已接受的 R3 实现已完成，SparkClaw 分支为 `codex/extract-email-app-cli`，App-CLI fork 分支为 `codex/sparkclaw-email`。成套发行身份为 `0.3.0-sparkclaw.1`，Python 版本为 `0.3.0+sparkclaw.1`。

- Python Registry 仍是唯一公共能力目录/准入点。显式 LifecycleAdapter 注册、签名授权和 Runtime 2.0 扩展原接口，保留 Manifest 1.0、Runtime v1 与默认拒绝写入。
- 常驻 Executor 拥有持久 task、按主体/owner 隔离的不可变请求键、事件、效果围栏与恢复；POSIX 锁、持久 epoch/高水位、Host generation 和实际 daemon 租约到期共同约束执行权。
- QQ/Gmail/Outlook 脚本、Reader 源码/资产、通知规则、发送 journal、Schema 和 binding 已迁入 App-CLI。SparkClaw 旧应用脚本和私有执行分支已删除。
- 产品通过固定客户端调用 Python 公共入口；Controller/Bridge/Desktop 保留通用页面所有权与调度。read/watch 共享资源但各持有限租约，导航前资产、账户/文档校验和清理失败隔离保持。
- 成套 wheel/npm/依赖制品、安装文件校验、Go/Reader/preload 生成投影、服务安装和整组回退均已实现，兼容回退保留状态与请求历史。

执行结果及可复跑命令见[实现与验收记录](app-cli-implementation-validation.md)。实际隔离 Electron 验证非邮件应用复用、普通浏览器任务与个人页隔离；邮件 fixtures 验证迁入供应商语义。真实邮箱业务验收和生产激活属于用户最终验收步骤，本轮未发送真实邮件、未部署共享服务。
