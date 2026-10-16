# App-CLI 请求授权与存储生命周期设计

> 语言：简体中文 | [English](../../docs/app-cli-storage-lifecycle-design.md)

- 状态：设计提案，关联 InfiniCenter ProjectGroup-2 决策 **0035 proposed**。已实现为成套发行 `0.3.0-sparkclaw.24`（App-CLI 分支 `codex/lifecycle-v3`，Lifecycle 3.0、ledger 2），包括 SparkClaw 产品发件箱、ACK、用户结案及独立 `progress-anchor` 服务；尚未部署。
- 日期：2026-10-15；2026-10-16 修订（进度锚仅用于发送放行与回滚检测；产品排空后封闭代次；区分已预留与已放行发送；用户可结案未知发送；参数由实测推导）。
- 核对基线：SparkClaw `b637c5c8`，成套 App-CLI `0.3.0-sparkclaw.23`，runtime digest `734f49377026d9a6977e505147cd16693d0abf68c7236aa042e7266c17457eb0`。
- 范围：公共请求、任务恢复、授权续期、事件、结果、发送 journal、临时制品、垃圾回收、容量控制、迁移和回退。业务邮箱、用户主动保留的文件及聊天历史继续遵循产品留存策略。

本方案把长期防重复执行的保证放在少量单调递增的请求代次围栏上，把恢复所需内容保留在尚未结案的任务中。已经交付的终态内容、逐条防重记录和过期授权可在围栏生效后回收。容量随活跃任务、恢复窗口及未决问题增长，不再随安装以来的请求总量增长。

“不会重复发信”指同一用户发送操作在重试、重启、清理及受支持的恢复流程中不会再次触发发送。供应商不提供幂等发送接口时，无法同时保证结果未知的发送自动完成和绝不重复；只有发送 token 已实际放行给发送 worker 的尝试才可能进入 `uncertain`，此后只对账，允许请求用户处理；放行之前崩溃可证明未派发，自动继续。用户明确创建的第二次发送是新的操作，不按正文相同推断为重复。

## 1. 当前缺口与设计目标

当前 [成套发行配置](../../configs/app-cli-release.json) 与 [实现验收](app-cli-implementation-validation.md) 对应的实现存在以下增长路径：

| 对象 | 当前行为 | 目标 |
| --- | --- | --- |
| `tasks.request/data` 与请求键 | 每次 invoke 保留完整行，没有终态回收 | 活跃任务完整保留，已结案任务按交付与代次回收 |
| events | 每任务最多 1,000 条；任务数仍增长 | 每任务与 owner 总字节双重滚动窗口，过期 cursor 明确 gap |
| 授权及 `.next` 索引 | 创建、续期追加文件和链 | 每个活跃任务保留固定数量的授权版本及直接索引 |
| 发送 journal、采集制品 | 与任务分目录，没有统一回收事务 | 引用计数、发送证据固定保留条件、可恢复删除 |
| 512 MiB 准入门 | 计算数据库已分配页加新请求；不覆盖全部存储 | 覆盖实际占用、待完成任务预留及文件系统余量 |
| 请求受理 | 客户端授权索引先落盘，服务端可能尚未受理 | 显式 prepare，区分已受理、确定拒绝与未知结果 |

保留 Python Registry 唯一公共入口、应用适配器权责、Browser Host 的页面/租约所有权和现有 UI。不得用直接调用 handler、重置数据库或新建请求 ID 来绕过恢复。

目标包括：持续运行后可回收数据进入稳态；任何清理不重新开放旧发送；崩溃后恢复同一个任务；授权续期存储与续期次数无关；容量不足时优先完成、取消及对账已受理任务；只有外部发送放行依赖独立进度锚，锚故障不阻塞受理、读取、ACK 或清理。

## 2. 必须保持的约束

1. 发送的产品操作 ID、请求身份、不可变意图及 task ID 一一绑定；重试、更新凭据、续期、升级与对账不能分配新的发送身份。
2. 删除明细之前，旧请求所属代次已经持久关闭；所有准入端先验证代次围栏，缺失记录永远不等于“未执行”。
3. 关闭代次只禁止新增执行受理，不删除其中的活跃任务，也不禁止对原任务的合法 status/cancel/resume/reconcile。
4. 外部副作用之前，持久化不可重复消费的 dispatch 记录。部署独立进度锚时，由它确认该记录放行给发送 worker；其他状态变更不等待锚。放行之后结果未知时禁止自动再次 dispatch，除非第 7 节证明该次尝试从未派发。
5. 非终态、清理未证实、发送结果未知和尚未可靠交付的发送回执，不因 TTL 或磁盘压力失去恢复证据。
6. 授权到期停止执行；保留任务不等于继续获得执行授权。读取/取消/对账可以通过当前合法身份重新签发限权授权。
7. 原始内容删除不改变原任务状态或发送事实。历史已过期必须明确返回，不能伪装成 completed、not_found 或 not_sent。
8. 任意总量的永久未决任务与有限磁盘无法同时无条件容纳。产品持有业务副本后，未决发送压缩为小型对账记录，用户可显式结案（第 7.3 节）；仅当记录仍超出预算时，按账户/owner 暂停新受理并给出可处理原因。

## 3. 请求身份与代次围栏

### 3.1 身份

新请求身份为 `(owner_id, namespace_generation, request_epoch, nonce)`，编码为不含业务数据的 `operation_id`。generation 和 epoch 单调递增且不可复用，耗尽时拒绝分配，不回绕到零。nonce 是可信产品发件箱用密码学安全随机源生成的 128 位值，按定长小写十六进制传输。由于没有分配计数器，恢复旧产品数据库不会重新签发已用过的身份。作用域同时绑定主体、应用、命令及资源摘要。

产品在自己的持久事务中保存用户发送意图和 operation_id，再发起网络调用。App-CLI 的发送 `invocation_id` 必须等于该 operation_id；旧格式通过显式 legacy 导入表处理，不能把旧 invocation_id 包装进新的请求代次。每次明确的新用户发送动作分配新身份；自动重试只读已有发件箱记录。

产品入口也校验原始操作凭证及 generation。业务记录已经过期的旧客户端重试返回 `OPERATION_RETIRED`，不重新“创建一次发送”。删除产品历史不能清除 generation 围栏。多个设备向同一个可信产品分配器提交；禁止每台设备各自重新解释旧发送。

定时轮次不使用随机 nonce，其 operation_id 由 `(owner_id, namespace_generation, schedule_id, schedule_revision, occurrence_time)` 确定性推导；时区换算后的 occurrence_time 在排程时固定。schedule_id 本身是在创建排程所在代次中分配的普通身份。运行时为每个存活排程保存一行 `schedule_fences`，记录最高 revision 和 `fired_through`。已有记录的轮次返回原任务；不晚于 `fired_through`、不晚于 owner 的 `schedule_floor_time`（第 10 节）或来自旧 revision 的轮次返回 `OPERATION_RETIRED`；否则受理并在同一事务中推进 `fired_through`。产品按时间顺序提交每个排程的轮次；较早的错过轮次晚于较新轮次到达时返回 retired 并显示为错过，手工重跑必须成为明确的新用户动作。

恢复到触发前的旧产品数据库会重新推导出同一身份，只能得到原任务或 retired，因此定时发送不需要产品进度锚。删除排程后，须等分配其 schedule_id 的代次关闭，才删除对应围栏行；此后“无围栏行且创建代次已关闭”同样视为 retired。

### 3.2 每个 owner 的常量状态

`owner_meta` 保存当前 generation、唯一开放的 request_epoch、至多一个排空中代次、`sealed_through`、关闭原因、`schedule_floor_time`、execution_epoch、commit_sequence、授权版本高水位及 GC 进度。只接受当前 generation 中开放代次的新 prepare，以及排空中代次内已签发身份的 prepare；较旧 generation 整体拒绝新增执行。不同应用/账户仍保留授权隔离，不为每个历史请求创建新的 owner。

轮转分两阶段，由产品分配器驱动，因为只有它知道自己签发过哪些身份：

1. 切换签发：开放代次 E 达到候选时长 24 小时，或其可回收终态明细达到 owner 可用预算的 10% 时，运行时在一个排他写事务中开放 E+1 并把 E 标为 `draining`。产品此后只在 E+1 签发新身份。排空中代次仍接受产品已在其中签发的身份的 prepare。
2. 排空后关闭：产品发件箱中不再有“已分配未提交”的 E 身份（均已受理、得到确定拒绝或在提交前被用户取消）时，产品调用 `seal(E, drained_digest)`；运行时设置 `sealed_through=E`，原因为 `drained`。
3. 强制关闭：只有超过最长离线窗口（第 9 节），或 95% 存储压力无法以其他方式缓解时，运行时才可不经产品确认关闭排空中代次，原因为 `forced`。

同时至多一个代次处于排空中。E+1 达到轮转时长而 E 仍未关闭时，E+1 继续开放；强制关闭窗口限定这一延迟。时间只触发轮转，是否关闭由持久整数围栏决定，时钟回退不能重新打开代次。长监听与未决发送不阻止轮转。此前已经 prepare 的任务继续用原 task ID。

因此，用户已确认、离线时在产品本地排队的发送，只要在最长离线窗口内到达，就以原身份受理；超出窗口的长时间离线不作笼统承诺。强制关闭后产品收到 `OPERATION_RETIRED(forced)`，请用户再次确认；确认后创建带 `supersedes=<旧 operation_id>` 的新身份。若产品认为某身份尚未提交，却收到原因为 `drained` 或 `recovery` 的 retired，说明产品自身状态被回滚或运行时执行过恢复，应显示为“可能已发送”，而不是普通的重新确认。产品永不自动换键。定时发送到期时才分配执行身份。

### 3.3 准入判定顺序

先认证 owner/主体及请求签名，再按身份查找已存在任务：存在则比较 intent/resource 摘要，相同返回原任务，不同返回冲突。不存在时，定时轮次先检查排程围栏与下限；其他请求在旧 generation、关闭的 epoch 或未知的未来 epoch 下都不得创建任务，只有当前开放或排空中代次可在容量预留成功后创建。retired 回复携带原因 `drained`、`forced`、`recovery`、`schedule` 或 `legacy`。

```text
authenticated request
  existing operation -> matching intent ? same task : REQUEST_KEY_CONFLICT
  absent + scheduled occurrence -> fence/floor/revision passed ? OPERATION_RETIRED(schedule) : validate + reserve + atomic prepare
  absent + retired generation / sealed epoch -> OPERATION_RETIRED(reason), no execution
  absent + open or draining epoch -> validate + reserve + atomic prepare
  absent + future/invalid epoch -> REQUEST_EPOCH_INVALID, no execution
```

已关闭代次没有逐请求墓碑也能拒绝重放。活跃例外表保存旧代次中仍需恢复的任务；其存在只开放原任务控制，不开放新的 invoke 语义。关闭代次后再签发一个旧键的授权，也无法绕过准入围栏。

## 4. 公共生命周期与持久接收

提议版本为 Lifecycle **3.0**、Host **2.0**、execution binding **2.0**、ledger **2**，成套发行声明所需能力。具体 JSON Schema 与跨语言夹具在实施第一阶段冻结；当前 2.0 消费者不得静默解释新语义。

| 操作 | 拟议语义 |
| --- | --- |
| `prepare` | 校验可信授权、完整意图、身份与资源预算；原子创建 task、恢复输入和预留；尚不触发副作用 |
| `invoke` | 只启动已 prepare 的原 task；重复调用返回其当前状态，不能创建隐式新任务 |
| `lookup` | 返回 `known`、`not_admitted`、`retired` 或 `unresolved`；不得互相混用 |
| `status/events` | 查询原任务/有界事件；只读控制不创建新 task 或授权版本 |
| `renew/resume` | 更新原任务授权；resume 必须满足恢复状态、原意图及安全检查 |
| `cancel/reconcile` | 记录停止意图／查询证据；均不构成重新发送的许可 |
| `ack_result` | 持久接收端确认 task、result digest、terminal revision 和 receiver identity，允许按策略回收内容 |

`not_admitted` 仅在权威存储健康且开放或排空中代次中确实没有受理记录时返回；回复含 generation/epoch/commit_sequence，并以该 epoch 内重试同一 prepare 为限。代次关闭后失效。从旧备份恢复的运行时可能对曾受理的操作回答 not_admitted；由于产品只重试同一身份，且每次放行都需要锚确认（第 10 节），最坏只会重建同一任务，不会第二次放行发送。无法完成权威查询返回 unresolved 或可重试的服务错误。容量拒绝不生成“已受理”行；回复丢失仍需 lookup，不能由超时推断拒绝。

最小新增信封字段：`operation_id`、`request_epoch`、`namespace_generation`、`intent_digest`、`resource_digest`、`task_id`（prepare 后）、`authorization_revision`。结果增加 `terminal_revision`、`result_digest`、`retention_until`、`recovery_class`；lookup 的 retired 携带原因且不携带成功 TaskResult。ACK 包含上述任务/结果身份及持久 receiver 的接收记录 ID。所有字段均须 Schema 校验和签名绑定。

产品接收顺序为：同一业务数据库事务提交结果、发送状态与 ACK outbox → 异步发送 ack_result → App-CLI 记录 ACK。丢 ACK 可重发；先显示结果、仅存内存或只收到 WebSocket 包都不算持久接收。Gateway 负责邮箱结果接收；客户端负责的非邮箱内容不借此改变决策 0031。

产品不需要进度锚：其身份为随机或确定性推导，且永不自动换键。恢复旧产品数据库可能丢失最近的历史条目，但不会产生另一次发送：恢复出的待处理操作以原身份 lookup，得到任务、结果或 retired；恢复后的数据库不认识的操作结果，作为孤儿结果导入历史后再 ACK。retired 的发送操作显示为“已归档、可能已发送”，从不显示为可重发。产品仍需常规业务备份。

产品与 App-CLI 跨库不假装有分布式事务。两侧用原身份、outbox、幂等 prepare/ACK 和启动扫描收敛。prepare 回复丢失时由 lookup 找回 task；客户端崩溃后禁止先造新 ID。

## 5. 存储分层及回收条件

单 owner 状态服务作为唯一写入者，使用 SQLite WAL/FULL 保存表及事务，不允许客户端或 Host 任意写 SQL。可信授权方签名与权限边界独立存在；数据库合并不赋予 Executor 自行扩权的能力。批量数据放入受控 blob 根，通过 digest 和引用记录关联。

binding 显式声明 `pure_read`、`replay_safe_local`、`external_write` 或 `subscription` 恢复类，由已审核的适配器能力及签名绑定决定；不能仅凭命令名叫 read 判断其无副作用。拒绝受理不持久创建 grants/索引/临时副本；确需临时上传时先取得有界上传预留，超时释放未被任何任务引用的内容，失败请求不能成为另一条无界增长路径。

| 表或对象 | 必要字段或责任 | 生命周期 |
| --- | --- | --- |
| `owner_meta` | generation、开放/排空中/关闭代次及关闭原因、`schedule_floor_time`、执行权、放行序列、提交与迁移/GC 高水位 | 常量规模，永不被普通清理删除 |
| `operations` | 原身份、task、不可变摘要、状态、phase、deadline、恢复类、结果引用、ACK | 活跃完整；终态按代次和交付回收 |
| `authorization_heads` | task 当前/候选/过渡授权、revision、撤销状态 | 每活跃任务固定槽位 |
| `effect_records` | task、attempt、effect token、dispatch 阶段（reserved/consume_pending/released）、草稿与提供方关联、证据摘要 | 未决压缩后固定保留；结案后按交付及代次回收 |
| `schedule_fences` | 每个存活排程的最高 revision 与 `fired_through` | 排程删除且其创建代次关闭后删除 |
| `events` | task、sequence、紧凑 payload | 每任务/owner 双重滚动窗口，保留 gap 水位 |
| `blobs` / `blob_refs` | 文件摘要、大小、用途、引用及删除阶段 | 无活跃/交付引用后可删除 |
| `gc_jobs` / `reservations` | 可重入删除进度、已受理资源承诺 | 完成后压缩到高水位 |
| 产品 `operation_outbox` | 原操作身份、task、用户意图/结果、投递及 ACK 状态 | 与产品自己的 generation 围栏协调回收 |

下列数值是版本化策略中的初始占位值，部署值由第 9 节按影子实测推导；不可放宽上述安全条件：

| 数据 | 候选规则 |
| --- | --- |
| 普通只读终态输入/结果 | 持久 ACK 后保留 1 小时；未 ACK 最多 24 小时，之后仅明确结果过期，不重执行原 ID |
| 本地采集结果/文件 | 等待 Gateway 验证 digest 并持久入库/接管引用；ACK 后 1 小时清理临时副本 |
| 发送终态正文/临时附件 | 持久接收发送结论及必要业务副本后 24 小时；不得删除仍被其他活跃任务引用的文件 |
| 发送终态最小回执 | ACK 后保留 30 天；代次关闭后方可删除逐条控制记录；产品保存用户可见发送历史 |
| 未 ACK 的发送回执 | 保留紧凑已知结论与恢复资料，不因 30 天到期抹掉唯一发送证据 |
| 未完成/清理未证实 | 无自动淘汰 TTL；原任务及必要输入/证据/引用保持 pinned |
| uncertain 发送 | 无自动淘汰 TTL。产品持久持有正文与附件的业务副本后，压缩为对账记录（身份、摘要、收件人摘要、草稿/提供方关联、attempt 与阶段、时间戳；目标不超过 4 KiB）；对账不需要正文 |
| 用户已结案的未知发送 | 立即删除正文、授权槽位、事件与 effect 明细；结案结论以紧凑行保留至代次关闭；产品按业务留存保存用户可见结论 |
| 事件 | 每任务最多 1,000 条且最多 2 MiB，owner 事件总预算 32 MiB；超出裁剪并报 gap |
| 授权过渡版本 | 当前、一个候选和一个尚未过期旧版本；过渡结束删除旧版本 |
| 诊断日志/审计细节 | 有界轮转，候选 7 天且 64 MiB；安全事实另在任务/锚中，不依赖日志 |

终态任务明细只有在满足其内容策略、发送交付义务、所属代次已关闭、无活跃执行/Host lease/文件引用后才删除。开放或排空中代次的短期终态至少保留紧凑防重记录，直到代次关闭。事件不是恢复真相来源；关键阶段必须先写 operations/effect_records。

业务历史和运行缓存必须区分：用户要求永久保存的邮件不会被运行 GC 删除，也不能声称其无限增长已被解决。产品应有独立可见的保留/导出策略；运行时不得通过无限“冷归档”把自身增长转移到另一个目录。

## 6. 授权固定槽位与安全续期

将 `.next` 链替换为 `task_id -> authorization_head` 直接索引。签名授权包含 owner/generation、原 operation/task、intent/resource digest、revision、操作范围、执行/访问期限及当前凭据代次；资源中只存受控凭据引用，避免长期复制原始 token。

续期使用 `(task_id, expected_revision, renewal_id)` 做 CAS：签发并持久化 r+1 候选 → Registry/Executor/Host 验签、确认同任务/意图/资源和有效执行许可 → 原子切换 head → 返回确认。丢回复查询 head；仍在三个槽位中的重复 renewal_id 返回同一个 r+1，不继续生成版本。只允许一个未完成候选；未收到需要的 Host 确认前不再开启下一次续期。

只在当前/候选/过渡槽位中记录 renewal_id，不另建永久续期去重表。更旧 expected_revision 返回 `AUTHORIZATION_STALE` 并指向当前 head；客户端读取 head 后收敛，不重新发布老版本。任务满足回收条件后其授权 head 一并删除；旧签名即使数学验签成功，没有当前任务/head 或有效 lease 也不得获得资源。纯访问凭据使用短期签名许可，不为每次 status/lookup 创建持久授权行。

终态立即禁止执行续期并撤销执行 head，待旧 lease 退出/到期且清理证实后释放版本槽位；结果保留 30 天不要求执行授权文件也保留 30 天。终态查询和回执交付使用当前主体的独立访问授权。

Host 的存活 lease 固定引用具体 revision；旧授权只允许原有 lease 在原到期时间内结束，不能再获取新 lease。过渡版本在所有 lease 确认退出或原到期且清理证据成立后删除；缺失确认保持固定槽位并停止推进，不能扩大版本列表。Host 缓存必须受短租约和撤销高水位约束，恢复连接先同步 revision，不允许失联缓存延长执行。

保留当前 5 分钟 watch 执行窗口、提前 60 秒续期和 30 秒最大 Host lease 作为起点。授权 head 可长期前移，但没有有效持续授权时停止；到期后获得新的明确合法授权才 resume。30 天查询凭据可以换发，任务身份和历史原意图不变；换发不自动恢复原执行许可。取消和只读对账使用独立权限。

撤销先持久增加相应 owner/account/task 的 revocation 高水位，通知资源端并停止新执行；离线 Host 最迟在已有 lease 到期停止。账号重绑定增加账户代次；删除账户明细前先使旧账户凭据整体失效。全 owner 重建必须增加 generation，不能删除 owner 后复用旧身份。授权审计只保留当前控制事实及有界详细历史。

## 7. 未完成任务与发送恢复

### 7.1 按阶段恢复

| 持久阶段 | 重启后的动作 | 清理条件 |
| --- | --- | --- |
| prepared，尚未开始 | 恢复原 task；合法执行授权/业务 deadline 内可排队，否则等待重新授权或明确失败 | 不作为终态清理 |
| 只读 running | 先隔离旧执行与页面；同一 task 从检查点继续或重新读取，返回明确 gap | 业务结果持久交付后 |
| 本地制品写入中 | 确定性 artifact ID、临时文件+校验+原子提交；校验已存在结果，继续原步骤 | 引用交接完成后 |
| 外部写入，dispatch 尚未保留 | 旧 worker 已隔离且状态可证明未派发后，按原许可继续 | 不抹除原请求 |
| dispatch 已保留或 consume_pending，尚未放行 | 推进 execution_epoch，使旧 token 永远无法放行；该尝试可证明未派发，以同一操作的新 attempt token 自动继续 | 不抹除原请求 |
| 已放行/结果未知 | 进入 uncertain；只执行无副作用 reconcile，没有第 7.2 节所述证据时禁止重发 | 取得可信结论或用户结案，并完成交付后 |
| 用户已结案 | 终态，不再执行 | 见第 7.3 节 |
| 已知发送成功但结果未交付 | 重建/读取原回执并重试交付，不调用 send | 持久 ACK 加保留窗口 |
| waiting_confirmation / cleanup blocked | 保存任务；等待原有审批或资源恢复流程 | 证实结束后才进入终态规则 |

### 7.2 发信临界区

1. 验证当前任务、用户批准的不可变邮件内容、账户和附件摘要；确认旧执行已隔离。提供方支持草稿时，先把已审核内容保存为草稿并记录草稿关联。
2. 在同一状态事务创建 attempt 编号、唯一 `effect_token` 与 `dispatch_reserved`。此步不等待锚。
3. 在实际 dispatch 边界，Host 携带 token、执行代次与 lease 向唯一状态服务请求放行。状态服务依次：(a) 以放行序列 n+1 持久写入 `consume_pending`；(b) 部署了锚时，以 CAS 把锚从 n 推进到 n+1，绑定 operation_id、attempt 和 token 摘要；(c) 持久写入 `released`；(d) 此后才回复 Host。Host 随后只执行一次发送。重复消费不授予执行权；Host 本地缓存不能代替放行。不能透明重试发送点击、网络提交或 SDK 自动重试。
4. 获取提供方回执后持久写 `sent` 和证据，再完成业务结果交付。
5. `released` 落盘之前崩溃可证明未派发，因为 Host 从未收到放行。重启后：锚仍为 n 时作废该待定放行；锚为 n+1 且摘要相同时，采用 n+1，并把 token 作废为“已消费但从未放行”。两种情况都推进 execution_epoch，并以新 attempt token 继续。只有 `released` 之后、第 4 步之前崩溃才进入 uncertain。每个操作的尝试次数有上限（候选 3 次），耗尽后以未发送失败结束。

提供方查询不到邮件、超时、过期凭据、journal 缺失都不能证明未发送。放行之后，`not_sent` 需要绑定原任务、attempt 与执行代次的证据，且旧执行已隔离：

- Host 针对该 token 的中止记录，在任何发送输入事件之前持久化（例如现有在效果边界前停止的 `EMAIL_REPLY_TARGET_UNVERIFIED`），并且 lease 已撤销。
- 经过资格验证的草稿证据：经过提供方特定的稳定期后，记录的草稿仍原样留在草稿箱，发件箱与已发送中没有匹配项，旧页面与 lease 已隔离。只有资格测试已证明该证据可靠的提供方才能使用；其他提供方只把它作为展示给用户的信息。

Host 中止记录允许以同一操作的新 attempt 自动继续。草稿证据只在提供方策略明确启用时才允许自动继续；否则向用户展示证据，用户可选择“继续本次发送”，以新 attempt 恢复同一操作，而不是创建新操作。没有这类证据时该尝试保持 uncertain。提供方拒绝同一草稿二次发送时，按 API 草稿 ID 发送可使放行本身具备幂等性；这属于后续另行批准的扩展，本版不启用。

### 7.3 用户结案未知发送

对账在策略上限内（候选：24 小时内三轮）仍无结论时，产品通过原发送历史条目请用户决定一次；不会更早询问，也不增加常驻状态展示。用户可在核对邮箱后记录 `confirmed_sent`、`confirmed_not_sent` 或 `accept_unknown`。组合动作“结案并重新发送”结案后创建带 `supersedes` 的新发送操作，并明确提示原邮件可能已经送达；恢复逻辑从不代替用户做这个决定。结案需要当前主体对该账户的发送权限，签名并绑定 operation_id 与 attempt。

结案使任务进入终态 `resolved_by_user`，记录 `{operation_id, attempt, resolution, principal, time, evidence digests}`。运行时不会把 `confirmed_not_sent` 变成自动重发，只用于结案。正文、附件、临时制品、授权槽位、事件以及结案记录以外的 effect 明细可以立即删除。任务移出活跃例外表；代次关闭前由紧凑行拒绝重放，关闭后由代次围栏（或排程围栏）拒绝，无需墓碑。结案后到达的证据只标注产品历史，不触发运行时动作。

原 journal 在迁移到 effect_records 前必须保留；迁移后供应商证据与任务阶段由同一个 Executor 管理。恢复不依赖进程内对象、旧授权文件路径或已裁剪事件。每个检查点记录 adapter/binding 版本；不兼容升级只允许原证据对账，不能用新脚本猜测重跑。

## 8. 清理事务与崩溃一致性

后台单写者负责 GC；查询路径只读，不在 status/lookup 内顺手删数据。每批有字节、行数及时间预算，前台恢复和取消优先。执行、续期、ACK 与 GC 使用同一任务 revision/CAS，防止读到旧候选后删除刚被引用的内容。

共享 blob 另有自身 revision 和 `LIVE -> DELETING -> DELETED` 状态。在同一事务中确认 refcount=0、无上传/执行 pin 后 CAS 到 DELETING，随后所有新增引用都必须校验 blob 仍为 LIVE；不得复活删除中的对象。相同 digest 的新需求创建新的 blob instance ID/物理路径，待重新校验后引用。删除工作绑定 instance ID 与 revision，不能仅凭 digest 路径删除新文件。这样不同任务之间的引用竞态也被覆盖。

```mermaid
flowchart LR
    A[任务终态与接收确认] --> B[关闭已排空或强制关闭的请求代次]
    B --> C[事务标记可回收并解除引用]
    C --> D[删除无引用文件并同步目录]
    D --> E[删除明细并保留代次围栏]
    E --> F[分批 checkpoint 与空间回收]
```

先在事务中标记待删对象、写 gc_job 及引用变化，再删文件；最后标记批次完成并清理行。任何阶段失败都可幂等重试；只有数据库已经记录不再需要的内容才允许物理删除。文件存在但未完成注册的孤儿，必须超过创建宽限期并证明无上传/准备任务引用后处理。不得扫描未知目录并按文件年龄删除。

DB 与文件不使用伪原子性：待删对象缺文件视为该删除步骤已完成；活跃引用缺文件则是恢复错误，任务保持 blocked/uncertain，不重新发信。GC 错误阻止该批完成，不改写任务结论。

SQLite 使用分批删除、WAL checkpoint 和可控 incremental vacuum；迁移时确认 auto_vacuum 模式。空闲页可被后续事务复用，但仍计入物理占用。禁止在低磁盘时执行需要整库副本的全量 VACUUM；维护前预留额外空间并可暂停。GC 作业和已完成迁移标记也压缩成高水位，不产生另一套无限历史。

## 9. 容量预算与保护已受理任务

不设置累计请求条数门。使用当前资源占用与资源承诺控制准入：

`admissible = used_actual + reserved_unwritten + request_peak + maintenance_headroom <= owner_budget`

同时校验文件系统可用字节、inode/文件数量水位及全宿主所有 owner 的承诺。实际占用包括 DB/WAL/SHM、授权、journal、临时/最终制品、GC 隔离文件、日志和本轮维护副本；共享文件按明确的物理归属计费，不能重复释放。产品接管文件通过事务化引用交接改变归属，实际文件仍占宿主磁盘。

每个 binding 声明输入、最大结果、临时制品、恢复证据及 WAL 峰值。prepare 先预留最坏合理峰值，实际写入消耗预留；超出声明前安全停止。发信在 dispatch 前必须有足够空间保存最小最终回执和未知结果证据，不能先发送再争取空间。连续 watch 预留固定事件环及固定授权槽位。

本设计固定公式而不固定预算值。P1 阶段在现有部署上运行至少 14 天的影子测量，记录每个 owner 按恢复类划分的请求速率、按绑定划分的单任务峰值字节、ACK 延迟、uncertain 发送比例、产品到运行时的断连时长、WAL 峰值及锚往返延迟。部署值按以下公式推导：

- `owner_budget = max(min_budget, ceil(P99(活跃 + pinned 工作集) × 2)) + recovery_reserve + maintenance_headroom`
- `recovery_reserve = max(64 MiB, owner_budget × 10%)`，包含在总预算中，新任务不可使用
- `max_offline_window = max(24 小时, P99(断连时长) × 2)`
- `receipt_retention = max(max_offline_window, 客户端重试期限, 用户追溯窗口)`

测量完成前，owner 2 GiB、事件 32 MiB、日志 64 MiB、回执保留 30 天及离线窗口 7 天只作为测试占位值。部署值连同测量报告以版本化策略发布。制品上限按绑定和宿主部署配置；预算不是累计寿命配额。迁移预估旧 pinned 数据超过预算时先调整规划，绝不能为了适配默认值删旧证据。

| 水位 | 行为 |
| --- | --- |
| 70% | 提前执行有资格的 GC、代次轮转及 checkpoint |
| 85% | 合并同账户可合并的探测/同步工作，延后非必要新请求；继续原任务控制 |
| 95% 或承诺不足 | prepare 返回 `STORAGE_PRESSURE` 和 retry-after，不创建任务；恢复、取消、ACK 使用独立预留 |
| 磁盘被其他进程写满 | 停止新副作用与需要持久化的状态推进；保留未知并等待恢复 |
| 锚不可用 | 只暂停放行；已保留发送退避等待，仍可证明未派发；prepare、读取、续期、ACK 与 GC 继续 |

水位按实际占用加未写预留计算，并设置恢复滞回。回收失败时退避，禁止高速新建请求重试；已受理请求始终使用原身份。压缩后的未知发送记录很小，其上限是安全信号而非存储问题：某账户未结案发送超过策略数量（候选 20 个）时，暂停该账户新发送并请用户结案（第 7.3 节）。owner 或宿主总预算耗尽时扩大准入限制。

可观察量包括总/分类字节、未写预留、最老未决任务、未 ACK 回执、GC 延迟、开放代次年龄、每 task 授权版本数和剩余磁盘。UI 沿用原入口，只在需用户处理时展示“空间不足”“待确认发送结果”等信息；不添加常驻技术状态栏。

## 10. 进度锚与备份恢复

owner 排他进程锁、execution_epoch、Host generation 和 authority 高水位继续存在。请求代次与 execution_epoch 是不同概念：前者拒绝旧请求，后者隔离旧 worker。

普通崩溃可用 WAL 与本机独立 authority 标记恢复；若整组本机文件都被回滚，它们无法自行证明发生过回滚。在本设计下，这种回滚唯一可能造成的危害是对同一操作第二次放行发送。准入或授权被回滚最多重建同一任务，GC 被回滚只会恢复更多记录，ACK 被回滚会以原身份重试。因此独立进度锚只覆盖发送放行。

### 10.1 锚接口

提议由 SparkClaw 的独立控制存储实现 `compare_and_advance(owner, generation, previous_seq, next_seq, release_digest)`，release_digest 绑定 operation_id、attempt 与 token 摘要。每个 owner 只有一个 runtime 槽位，保存 generation、放行序列、摘要、最近 K 次放行 `(seq, operation_id, attempt)` 的定长环（候选 K=256）以及最低存储协议，规模恒定且不保存请求正文。接口认证 runtime 写入身份，拒绝跨 owner 更新、同序列异摘要及未经授权的 generation 增长；相同 CAS 重复提交幂等返回成功。不设 product 槽位（第 4 节）。

只有第 7.2 节第 3 步推进锚；准入、授权、ACK、GC、查询和事件读取都不等待锚。每次放行多一次往返，P1 以 p99 不超过 1 秒为目标实测其延迟。超时以同一 CAS 重试，永不视为已放行。锚不可达时任务停留在 consume_pending 或 reserved，仍可证明未派发；锚故障不产生 uncertain 发送。超过业务期限的已保留发送按未发送正常失败。

### 10.2 回滚检测与自动向前封闭

运行时在启动时及任何 CAS 失败后读取锚：

- 锚等于本机序列：正常。
- 本机恰好领先一个 consume_pending，且其前序列/摘要与锚一致：重试该原 CAS。
- 锚领先本机序列：运行时被回滚。
- 锚落后本机序列，或同序列异摘要：锚丢失，或两者被回滚到不同时点。

后两种情况由运行时在任何新放行之前、在一个排他事务中自动执行向前封闭：

1. 推进 execution_epoch 与凭据代次，所有执行授权失效；可信授权方按其当前撤销状态重新签发，因为回滚可能丢失撤销记录。
2. 以原因 `recovery` 关闭截至当前的所有代次并开放新代次，把 `schedule_floor_time` 设为检测时的锚服务时间（C 档为恢复时间）。
3. 对非终态 external_write 任务分类。锚领先不超过 K 时，operation_id 出现在环中且序列晚于本机的任务进入 uncertain，其余任务可证明自备份以来未放行，正常继续。差距超过 K、锚落后或摘要不一致时，所有非终态 external_write 任务进入 uncertain。
4. 采用锚序列；锚落后或不一致时在新锚 generation 下重新初始化锚，并写入恢复记录。C 档没有锚相关步骤。

向前封闭从不阻塞等待人工操作；用户只会通过原发送历史入口和第 7.3 节看到由此产生的 uncertain 发送。

### 10.3 部署档位

| 档位 | 锚的位置 | 保证 |
| --- | --- | --- |
| A：Gateway 与 runtime 不同宿主 | Gateway 所在宿主的控制库，不纳入 runtime 备份；控制库从自身备份恢复表现为锚落后，触发向前封闭 | 检测整组 runtime 回滚 |
| B：Gateway 与 runtime 同宿主 | 锚放在独立故障/恢复域的另一台主机（如簇中枢），或硬件单调计数器（如 Linux/Windows 的 TPM 2.0 NV 计数器）；macOS 钥匙串和 Time Machine 覆盖的文件不合格 | 检测整组 runtime 回滚 |
| C：无独立锚 | 普通崩溃恢复；备份只能经受支持的恢复命令还原，该命令总是执行向前封闭，并把所有非终态 external_write 任务置为 uncertain | 仅崩溃安全与受支持的恢复；绕过该命令直接还原文件不受支持 |

备份包含一致的任务/授权/引用/制品清单和锚序列；正常备份只保留有限轮次，例如最近两份已验证快照，并计入部署存储预算。旧备份不可直接作为在线账本启动。正文删除后的备份隐私留存由同一策略覆盖；无限保存备份不属于稳态方案。

## 11. 现有数据迁移与回退

1. 清点 ledger、grants/index、journal、captures、Host lease 与产品 outbox，计算任务分类、引用、容量及逐条身份/摘要；建立一致冷备份。将 orphan 授权、无 task 的旧 index、uncertain 和清理围栏分别列出。
2. 在隔离副本构建 ledger 2，并影子计算 GC 候选；此阶段不删除生产文件，不改变当前 Lifecycle 2.0 语义。旧随机请求键统一归入只读 `legacy_generation` 索引，保留原 task ID/意图/状态；禁止给旧写入自动分配新身份。
3. 对旧授权链验证签名、资源与 revision，提取当前 head 和仍被有效 lease 使用的版本；确认未知链不能推断失效。旧发送 journal 与任务逐项绑定，未知文件先保留。
4. 排空新受理、隔离旧 worker/Host，持久关闭 legacy 新准入，建立新 generation 及（如部署）独立锚，再原子切换匹配 Registry/Runtime/Host/产品消费者。现存旧任务通过专用 task-bound 控制访问；未受理的旧请求返回 retired，不能补发。
5. 未完成 legacy 任务继续 pinned；旧授权已过期可经当前合法主体签发限权恢复授权，不更换原任务身份。先完成真实恢复、ACK 与代次拒绝验证，之后启用 GC。
6. 已完成 legacy 内容仅在新契约明确允许、产品已接收、旧命名空间永久禁止新执行且保留窗口满足后回收。无法证明条件的条目保留并报告，不强行压缩到零。

Schema 2 一旦启用清理，不再允许 `.23` 或其他 ledger 1 写入者打开该状态目录。包 manifest 和外部最低存储协议围栏共同阻止降级；旧二进制即使不识别新字段，也不能获得启动锁/Host 执行权。第一次激活前可取消迁移并继续完整 v1；激活后只能使用兼容 v2 存储的修复发行回退。恢复旧数据库/旧 authority 来“回滚版本”禁止。

该围栏由不随应用包/状态备份回滚的 bootstrap/authority gate 执行：固定最低协议与允许发行，所有正式 launcher 经它启动，切换时撤销旧 Host 注册/租约并证明旧进程和页面动作已隔离。新状态与 Host 控制凭据置于受控服务身份下，旧安装用户不能直接写新目录或领页。仅有 manifest、新 DB 字段或一个会随进程退出释放的文件锁不足以保证降级拒绝。没有这层权限与启动约束的部署只能声明受支持启动路径的兼容检查，不能声明任意旧程序都被阻止；管理员蓄意撤销隔离或绕开产品发送不在本协议保证内。

目录备份、历史制品与迁移副本也纳入有界维护策略；旧备份在验收完成、所选部署档位的回滚检测有效并满足约定保留期后才能删除。本设计不触发任何现网清理，也不替代逐节点发布授权。

## 12. 验收与证据

| 场景 | 必须满足的结果 |
| --- | --- |
| 同 ID 重复 prepare/invoke、改参、跨 owner | 同任务、明确冲突或拒绝；外部发送计数不增加 |
| 关闭代次并删除全部可回收逐条记录后重放 | 返回 retired；包括重新签发旧键、旧设备和新进程 |
| 旧 outbox/业务库备份、旧 invocation_id 包装新请求 | 产品围栏或协议绑定拒绝，不生成新发送 |
| 定时任务触发前的产品备份、提交或 ACK 后产品回滚 | 同一 occurrence 推导出同一身份，得到原任务或 retired；恢复出的待处理操作得到任务/结果/retired；不自动换键；未知结果作为孤儿导入 |
| 产品离线跨越代次轮转 | 最长离线窗口内排队的发送以原身份受理；强制关闭后 retired(forced) 请用户再次确认；未提交身份收到 retired(drained/recovery) 时显示为可能已发送 |
| 新代次持续工作、旧代次仍有 pending/uncertain | 原 task 可合法恢复/对账；旧缺失 ID 不能新受理 |
| prepare/DB commit/返回的每个断点 | 找到原任务或保持 unresolved；不重复创建发送 |
| 在 reserved、consume_pending、锚 CAS、released、Host 发送输入及提供方回包处崩溃；提供方回包丢失 | released 之前：以新 attempt 自动继续且恰好发送一次；released 之后：除非有 Host 中止记录或经资格验证的草稿证据，否则 uncertain；重复发送为 0 |
| 锚长时间不可达 | prepare/读取/ACK/GC 继续；已保留发送等待，超期后按未发送失败；故障不产生 uncertain 发送 |
| 旧 worker、Host 重启、lease 失效、SDK 重试 | 同 effect token 最多一次实际 dispatch；幸存执行被隔离 |
| 续期与 GC、撤销、崩溃并发；续期回复丢失 | 原 task、单调 revision、最多三个槽位；旧授权不能新领资源 |
| ACK 前后崩溃、重复/伪造/跨主体 ACK | 仅匹配持久结果被释放；未交付发送证据保留 |
| GC 标记/删文件/删行/checkpoint 每个断点 | 幂等收敛；活跃输入/附件不丢失，重放仍拒绝 |
| 共享 blob 进入 DELETING 时另一个任务引用同 digest | 旧实例拒绝新增引用，新实例不被旧 GC 作业删除 |
| WAL/临时文件/日志/inode 压力与真实 ENOSPC | 不越过无证据发送边界，恢复预算有效，恢复后继续原任务 |
| 全部历史正常结束，重复运行至少两个完整最长留存周期 | 固定速率/大小下数据库、WAL、授权文件数、总磁盘进入平台区间 |
| 持续制造不可对账的发送 | 紧凑证据不丢，请用户结案；账户达到数量上限时暂停新写入，无无限扩容或自动重发 |
| 用户结案未知发送；关闭前后重放；迟到证据 | 正文删除、结案结论保留；重放返回 retired；迟到证据只标注历史 |
| v1 各类数据迁移、失败重启、旧二进制启动 | 原身份/意图/结果逐条一致；legacy 永久封闭；降级拒绝 |
| 整组旧 runtime 备份；锚领先/落后/不一致；C 档恢复命令 | 任何放行前自动向前封闭；按环分类；重新签发授权；重复放行为 0 |
| 原 UI 与真实邮箱 | 原页面/菜单/操作保留，收件采集/长监听/发送回执/断线恢复分别验收 |

先用可计数发送次数的故障注入假提供方穷举关键交错。对第 7.2 节的每个注入点，报告实际发送次数、自动继续次数、uncertain 次数及其中被证据结案的次数；“放行到回执”窗口的 uncertain 比例就是保守防重的实测可用性代价。草稿证据在启用前须按提供方用真实邮箱做资格验证。再使用隔离真实 Registry/Python/Unix socket/SQLite/Host；时间快进覆盖所配置的留存窗口，另跑真实时钟压力与磁盘配额环境。压测报告同时列出逻辑行数、实际文件字节、WAL 峰值、授权文件数、磁盘余量及失败原因，不用“测试没报错”代替稳态证据。

百万级已完成请求可以验证总历史不再决定存储；它不是固定业务吞吐承诺。稳定性的条件为输入速率受服务能力约束、活跃/pinned 工作集在配置预算内、接收与 GC 最终能够推进。底层介质完全不可写/丢失时保持安全停机，不承诺凭空恢复数据。

## 13. 实施顺序与职责

| 阶段 | 交付 | 退出条件 |
| --- | --- | --- |
| P0 契约 | 0035 评审、版本化 Schema/状态/留存公式与放行锚接口、跨语言一致性夹具 | SparkClaw 与 App-CLI 维护侧接受；不默认上游已接受 |
| P1 身份与恢复 | 影子测量；产品 outbox、随机与推导身份、两阶段代次轮转、prepare/lookup、reserved/released 分离、独立锚与向前封闭 | 清理尚未启用时，重放/崩溃/旧 worker/回滚验证通过；发布测量报告与推导参数 |
| P2 授权与接收 | 固定 head、撤销、lease 交接、task-bound 恢复、ack_result、未知发送用户结案、提供方草稿证据资格验证 | 长监听与发送接收故障矩阵通过，版本数有界 |
| P3 容量与 GC | 分类存储、预留、回收事务、WAL/文件回收及监测 | 完整留存窗口稳态与 ENOSPC 测试通过 |
| P4 迁移与灰度 | legacy 导入、影子候选、匹配发行、单节点可控激活 | 原数据核对、真实业务与旧包拒绝通过，再按授权扩展 |

App-CLI 负责公共协议、Registry、Executor、授权 head 验证、证据/制品引用、GC 和成套发行；SparkClaw 负责产品意图/outbox、代次排空确认、用户结案、持久接收/ACK、可信授权、独立进度锚、通用 Host 和原 UI 接入。双方共享协议夹具和故障矩阵，不能各自增加另一套发送重试器。

容量数值是本提案的候选配置；防重、未决保留、ACK、代次关闭和防回滚条件是必须接受并测试的语义。与 [原抽离设计](app-cli-email-extraction-design.md) 的差异由 0035 单独决定；0031 非邮箱交付提案仍独立保持 proposed，0032–0034 的已部署事实保持。
