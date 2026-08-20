# File Store 持久性设计

> 语言：[English](../../docs/store-file-durability-design.md) | 简体中文

> 状态：修复候选补上 destination-read 与 directory-open/close 失败证据并通过
> 独立复审后，S2 实现已于 2026-08-20 在 `42b62bd` 获得接受。

## 问题与 S2 声明

File 后端当前先修改 `MemoryStore`，之后才序列化，并丢弃 48 个 legacy
持久化结果。它的 mutex 只保护 snapshot 写入，不保护之前的修改和普通读取。
因此 read 可能看见尚未持久化的状态，之后一次成功 snapshot 还可能把更早静默
失败留下的脏状态写入磁盘。

S2 有意区分两层声明：

| 表面 | S2 保证 |
|---|---|
| 每个 FileStore interface 方法 | 一个进程内 admission gate，防止 read 或另一个 command 穿插到 command 的 Memory 修改与持久化尝试之间。 |
| 已迁移的 `ISCPOnboardingRepository` 方法 | caller context、typed backend error、提交前完整回滚、持久替换、unknown-outcome fence 和对账。 |
| 其余 legacy repository | 原签名和持久化错误行为继续作为 S3 的已知缺陷；S2 不声明其失败后的状态已经持久化。 |

因此，强 commit-visibility invariant 只适用于已迁移 repository，且 File 后端
没有被未解决的 submitted outcome fenced：它的 read 只能看见 command 前的完整
状态或已经持久提交的完整 command 后状态。仅有全方法 gate 不能证明 legacy
repository 已经获得这个 invariant。

## 单一 Admission Gate

`FileStore` 把 `golang.org/x/sync/semaphore.Weighted` 提升为直接依赖。
semaphore 用一个固定私有容量初始化；read 获取权重 1，command 获取全部容量。
依赖的 FIFO waiter 队列会阻止新 reader 绕过已经排队的 writer，`Acquire` 能返回
等待 context 的 cancel 或 deadline error。

gate 规则如下：

- S0 接受的全部 141 个 public FileStore 方法只分类一次为 read 或 command，且在
  访问 `inner` 前获取此 gate；
- command 从捕获 Memory pre-state 前开始持有独占 admission，直到持久化、回滚或
  unknown-outcome 注册完毕；
- 已迁移方法使用其 effective operation context 获取 admission；
- legacy 方法没有 error channel，因此使用 `context.Background()` 获取 admission，
  在 S3 迁移所属 repository 前可能无 deadline 等待；
- legacy 方法不能为了模拟 admission cancel 而编造 zero result、静默跳过 command
  或 panic；
- 删除旧 File 持久化 mutex；已持有独占 admission 的 helper 不能递归获取 gate；
- `inner` 保持 private，生产代码不能在 admitted File 方法之外访问它。

fence observation 与 semaphore acquire 使用显式 double-check loop，不能作为两个
互不关联的检查：

1. 在 `fenceMu` 下读取当前 fence pointer；
2. 若存在，legacy 方法在 semaphore 外等待 completion channel 后重试，已迁移方法
   进入 reconciliation；
3. 若不存在，获取所需 semaphore weight；
4. 在 `fenceMu` 下再次读取 fence pointer；
5. 只有第二次仍为 nil 才返回 admission lease；
6. 否则立即释放 semaphore，在 lease 外等待或对账，再从第一步重试。

fence 在独占 admission 与 `fenceMu` 下安装，publish 后 payload immutable。清除时
在 `fenceMu` 下比较同一 pointer，移除并且只 close 一次 completion channel。
专用 reconciliation path 不要求普通 fence precondition，而是获取全部 semaphore
capacity，再次核对 pointer identity 后检查 destination；它在报告或重试前释放
admission。这是 fence 存在时唯一可以获取 admission 的 operation。

因此，pilot 释放独占 admission 后，fence 安装前已排队的 legacy waiter 可能被
wake，但其强制 post-acquire check 会看见 fence，立即释放，并在 gate 外等待。
它既不能穿透 fence，也不能占有 reconciler 需要的 gate。

gate commit 单独审查，其中不包含 pilot 签名或错误分类变化。AST source guard
枚举 S0 接受的方法集，并证明每个 public File 方法在访问 `inner` 前恰好进入一个
read 或 command wrapper。聚焦并发测试把 command 阻塞在 Memory 修改和持久化
之间，证明 read 和另一个 command 都无法越过。

这是进程内边界。SparkClaw 产品拓扑只允许一个 Gateway 使用一个 File path；
S2 不新增跨进程文件租约。两个进程写同一路径仍不受支持。

## 已迁移 Command 算法

持有独占 admission 时，已迁移的 `SaveISCPOnboarding` 执行：

1. 计算 caller deadline 与 30 秒 write fallback 中更早的 effective context；如果
   已结束则拒绝；
2. 解决此 FileStore 更早的 submitted outcome，否则不修改并返回对应 typed error；
3. 捕获完整 rollback state、当前磁盘文件的精确 bytes 和 existence bit，以及当前
   snapshot JSON shape；
4. 在 Memory lock 下执行 command，domain/validation error 原样保留且不开始持久化；
5. 捕获 candidate snapshot，进行 JSON encode，并按配置加密；
6. 确保 parent directory 存在，再在同目录以 `O_EXCL` 和 `0600` mode 创建唯一
   temporary file；
7. 写完全部 bytes，fsync temporary file，然后 close；
8. 在提交前再检查一次 effective context；
9. 把 temporary file 原子 rename 覆盖 state path；
10. open 并 fsync parent directory，然后 close；
11. 清理临时状态、释放 admission 并返回成功。

replacement rename 是 effect-submission point。算法在其之前检查 cancel；已提交
rename 后观察到的 cancel 不能报告为确定回滚。配置的 File 后端以及两个 File
constructor 都要求非空 path；需要有意使用非持久状态的测试改用 `MemoryStore`。

新 primitive 不修改 `Snapshot`、JSON field name、omit 规则、plaintext format 或
encrypted-envelope version。plaintext 和 encrypted path 使用相同的 create/write/
file-sync/rename/directory-sync 阶段。

## Filesystem Seam

package-private `fileCommitOps` 依赖只拥有算法需要的操作：encode/encrypt、mkdir、
create-exclusive temp、write、file sync、file close、rename、read destination、
remove temp、open directory、directory sync 和 directory close。生产实现使用
`os` 和现有 encryption；测试注入确定性结果与 partial write，不依赖权限、磁盘
写满、mount 行为、sleep 或 race。

temporary file 唯一且位于 destination directory，因此 rename 不跨 filesystem。
rename 前的每个失败都尝试 cleanup；cleanup failure 会 join 到内部诊断，但不能
覆盖主 classified outcome。进程 crash 可能留下未引用 temp file，但 startup 只读
精确配置的 state path。

## 失败状态机

| 阶段 | 内存动作 | 返回结果 |
|---|---|---|
| Memory 修改前 | 无 | 保留的 `canceled`、`timeout` 或 validation error |
| Memory command 拒绝 | 按 command contract 保持不变 | 保留 typed domain error，包括 `ErrISCPOnboardingConflict` |
| rename 前 encode/encrypt/mkdir/create/write/file-sync/file-close 失败 | 恢复完整 rollback state | 带 cause 的 `durability_failed` |
| rename 前 context 结束 | 恢复完整 rollback state | `canceled` 或 `timeout` |
| rename 报错且 destination 已证明是 previous bytes/absence | 恢复完整 rollback state | `durability_failed` |
| rename 报错但 destination 是 candidate 或无法识别 | 保留 candidate 并安装 submitted-outcome fence | `unknown_outcome` |
| rename 成功，之后 directory open/sync/close 失败 | 保留 candidate 并安装 submitted-outcome fence | `unknown_outcome` |
| replacement 和 directory sync 成功 | 保留 candidate | nil |

`durability_failed` 表示 candidate 确定不是当前 durable state，caller 可正常 retry。
`unknown_outcome` 表示 effect 可能已经生效，完成对账前禁止正常 retry。任何路径
都不能只修改 Memory 就宣称已提交 rename 被回滚。

## Submitted-Outcome Fence 与对账

fence 不保存 business label，只保存 operation ID、candidate byte digest、previous
精确 byte digest/existence、rollback state 和 completion channel，不修改 snapshot
JSON。

任何后续 operation 继续前：

- 已迁移方法可以获取独占 admission，在其 effective context 内对账；
- legacy 方法在 admission gate 外等待 fence completion channel，从而让已迁移的
  reconciliation call 仍能获取 gate；
- 未解决 fence 期间，legacy command 不能修改 Memory 或写入更新 snapshot。

这些规则由上述 admission double-check loop 实现，不能只做一次 pre-acquire fence
test。fence state 只由 `fenceMu` 拥有；semaphore ownership 保护 Memory/snapshot
transition，但不能兼任 fence pointer lock。

对账读取 destination 的精确 bytes。若与 candidate 相同，只重试 parent-directory
sync；成功即确认 candidate 并清除 fence。若与已捕获的 previous bytes 或 previous
absence 相同，Memory 恢复 rollback state，fence 解析为确定失败。若两个版本都
无法确认，或 directory sync 继续失败，fence 保留，已迁移 call 按情况返回
`unknown_outcome` 或 `corrupt`。

对于 pilot，`GetISCPOnboarding(id)` 和 `ListISCPOnboardings(ownerID)` 是 caller
可见的 reconciliation read。fence 未解决时，它们绝不以 nil error 返回 candidate
data。S5 才增加主动 recovery probe；S2 有意只提供 reconciliation-on-operation。

## 完整 Rollback State

rollback 捕获完整 persisted `Snapshot` 和 typed volatile sidecar。当前 sidecar 包含
`passiveNotificationRevs` 的 clone。恢复时调用与 startup 相同的 `loadSnapshot`
normalization，重建 notification、artifact URI index 和其他 derived map，再在
Memory lock 下恢复 volatile revision sidecar。

删除只恢复 onboarding map entry 的手写 rollback。未来 command 可能同时修改
index、event、revision 或关联 record，因此不接受局部恢复。测试在每个注入的
pre-submit failure 前后比较完整 snapshot 与 volatile sidecar。

## Legacy 过渡规则

机械 gate 阻止全部 File 方法的并发穿插，但 S0 接受的 48 个 discarded-persistence
site 继续作为显式 defect evidence。其返回值无法表达 admission timeout 或
durability failure。因此它们使用 unbounded admission，保留当前失败后 contract，
且不使用已迁移 rollback helper。

S2 source guard 相应收窄：禁止已迁移 onboarding 方法忽略 commit error 或使用
手写 rollback，同时单独断言已知 legacy defect inventory 没有增加。每个 S3
repository 在签名和 caller 一起迁移时，用正向 error、rollback 和 reconciliation
assertion 替换自己的 defect evidence。

## 验证与 Commit 边界

第一个实现 commit 只做机械 admission coverage。它必须通过 Store test 和
`go test -race ./internal/store`，且不产生 pilot API 或 snapshot diff。

第二个实现 commit 迁移 pilot，并包含以下确定性测试：

- admission 前和 rename 前 cancel；
- reader 无法看见 tentative mutation；
- 每个 pre-rename failure 都恢复完整 snapshot 和 volatile revision；
- partial-write cleanup 且保留 primary error；
- rename failure 按 destination 精确对账分类；
- directory-sync failure fence 所有 legacy command，并在对账前拒绝 migrated data
  read；
- fence publish 前已排队的 legacy read/command waiter 在 post-acquire check 失败，
  释放 admission，且 reconciler 保持可运行；
- candidate 和 previous-state 两个 reconciliation branch；
- 成功 commit 后 restart 精确重现 onboarding receipt；
- 并发 duplicate save 只保留一个 winner 并返回 `ErrISCPOnboardingConflict`；
- plaintext/encrypted stage parity 和 state-file mode `0600`；
- snapshot JSON key 不变且无 plaintext secret；
- read、command、rollback、fence 和 reload 的 race coverage。

默认 File backend production-entry test、完整 Go build/test/vet、WebChat test/build、
双语 docs CI 和真实配置的 PostgreSQL Store run 是 S2 gate。PostgreSQL CI topology
与 `SPARKCLAW_TEST_POSTGRES_DSN` skip 语义不变。

## S2 审查接受的残余风险

- submitted outcome 可能阻塞 legacy File command，直到一次已迁移 onboarding read
  完成对账。这是在 S5 主动 recovery 前的有意 fail-closed；静默覆盖 uncertain
  state 更不可接受。
- gate 不防护第二个进程使用同一 File path。
- ISCP authority effect 先于本地 receipt 持久化，且没有 revocation/idempotent-
  recovery operation。确定的本地失败可能留下未披露的 authority ticket；Store
  不能让远端与本地 effect 原子化。pilot 让失败可见，但不宣称能补偿。
- 未迁移 repository 在各自 S3 stage 前保留已知静默持久化失败。

## S2 审查门禁

设计 `GO` 要求接受 scoped invariant、semaphore strategy、legacy behavior、
submission/fence state machine、rollback state、filesystem seam、pilot
reconciliation 和残余风险。实现 `GO` 要求两个 commit、确定性 failure/race
evidence、snapshot JSON 不变、默认后端无回归且无未使用 gate 或 operation helper。
仅有 gate scaffolding 不能开始 S3。

## 审查记录

| 审查 | Revision/commit | 决定 | 证据与未解决风险 | Reviewer/date |
|---|---|---|---|---|
| 设计审查 1 | `3aff151` | `REVISE` | fence observation 与 semaphore acquire 缺少原子握手，pre-queued legacy waiter 可能穿透 fence 或让 reconciliation 死锁 | Independent gatekeeper / 2026-08-20 |
| 设计审查 2 | `49b0858` | `GO` | double-check admission、immutable fence ownership 与专用 reconciliation lease 关闭 queued-waiter 穿透/死锁窗口；后续关联审查关闭 pilot/PostgreSQL blocker | Independent gatekeeper / 2026-08-20 |
| 实现初次审查 | `0e7817b`, `9d86c50` | 已被取代的 `GO` | 全方法 admission、rollback/fence/reconciliation、明文/加密 restart、race、默认 File 和完整回归证据均通过；之后的新审查取代了这一决定 | 独立 gatekeeper 和获 owner 授权的 primary agent / 2026-08-20 |
| 实现重新审查 | `9d86c50` | `REVISE` | 除 pairing-service blocker 外，审查还发现 destination-read 与 directory-open/close File failure branch 未执行 | Context-isolated gatekeeper / 2026-08-20 |
| 修复实现 | `6f4c1bf`, `42b62bd` | `GO` | 确定性测试覆盖 destination-read isolation，以及明文/加密对账一致的 directory-open/close uncertainty；focused/race 与独立重复的真实 PostgreSQL S2 门禁通过 | Context-isolated gatekeeper 和获 owner 授权的 primary agent / 2026-08-20 |
