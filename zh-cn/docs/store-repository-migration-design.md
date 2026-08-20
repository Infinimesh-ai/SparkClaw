# Store Repository 迁移设计

> 语言：[English](../../docs/store-repository-migration-design.md) | 简体中文

> 状态：S2 pilot 设计审查候选 revision 1，2026-08-20。只有本文档和关联的
> File durability 设计都获得独立 `GO` 后才开始 pilot；其余 repository code
> 仍由 S2 实现验收门禁控制。

## 目标与阶段边界

把 141-method `store.Store` 以小型、可审查的 domain repository 分波迁移。
S2 只迁移已接受的 `ISCPOnboardingRepository` pilot，同时证明 File transaction
model。S3 逐个迁移其余 repository，S4 删除临时 broad interface，Runtime 和
Supervisor 仍属于 S5。

S2 不按职责拆分 `file.go`、`memory.go`、`postgres.go` 或其他大型 Store module。
只有 Store 迁移和监督完成后，才重新审查文件尺寸与职责拆分。

## 迁移单元

一个 repository 是一个实现阶段，通常对应一个 behavior-change commit。完成的
阶段必须：

1. 确认 S0 接受的方法、command、reconciliation 与 consumer row；
2. 定义一个 typed repository interface 和全部 backend assertion；
3. 为每个可能 backend failure 的方法增加 caller context 与 error result；
4. 仅在 record shape 需要时更新 Memory、File、PostgreSQL 与 File `Snapshot`；
5. 更新每个 caller，使其传递 request、operation、worker、startup 或 shutdown
   context；
6. 按需新增 shared contract、File failure、PostgreSQL classification、timeout、
   cancellation 与 race test；
7. 删除该 repository 的旧签名；
8. 在开始下一个 repository 前获得实现审查。

完成的 repository path 不得保留 compatibility adapter、optional type assertion、
duplicate method、dynamic repository map、string-based dispatch 或
`context.Background()`。

## S2 Pilot Contract

在 `internal/store` 中引入：

```go
type ISCPOnboardingRepository interface {
    SaveISCPOnboarding(context.Context, app.ISCPOnboarding) (app.ISCPOnboarding, error)
    GetISCPOnboarding(context.Context, string) (app.ISCPOnboarding, bool, error)
    ListISCPOnboardings(context.Context, string) ([]app.ISCPOnboarding, error)
}
```

contract 如下：

- save 校验并 normalize receipt，以 ID 只创建一次；duplicate 继续满足
  `errors.Is(err, ErrISCPOnboardingConflict)`；
- get 只有 normal absence 才返回 `(zero, false, nil)`；
- list 只有成功且确实为空时才返回 empty non-nil slice 和 nil；按 `CreatedAt`
  newest-first，并用 ID 作确定性 tie-break；
- cancellation 和 backend failure 都是 error，不能变成 absence 或成功空列表；
- receipt 仅有 scalar/time field，不暴露 mutable backend alias；
- repository 不拥有 audit row；`iscppairing.Service` 仍在 receipt save 成功后由
  caller append `iscp.onboarding.ticket_issued`。

ID 是 idempotency/conflict 边界。caller 使用 `GetISCPOnboarding(ctx, id)` 对账
uncertain save；对账完成前不得为同一 logical receipt 生成或提交另一次 save。

## 有限 Operation 边界

S2 引入 package-private finite operation metadata，并由 pilot 立即消费：

| Operation ID | Mode | Timeout class | Reconciliation |
|---|---|---|---|
| `iscp_onboarding.save` | write | write | 按 ID get |
| `iscp_onboarding.get` | read | read | 自身 |
| `iscp_onboarding.list` | read | read | 无 |

`OperationSpec` 绑定 ID、repository ID、method/mode 和 timeout class。registration
test 拒绝 duplicate ID、缺少 pilot method、未知 timeout class 和未引用 spec。
Operation ID 是常量；record ID、owner ID、query、path、DSN 与 content 不能成为
operation name 或 label。

S2-S3 期间，该边界只负责 deadline composition 与 typed error classification，
不暴露 health、metrics、repository lookup、Runtime 或 Supervisor。S5 包装这些
原有 call site，不再改 repository 签名。

## Timeout 配置

S2 只引入 pilot 实际消费的 timeout class：

| Typed field | JSON | Environment | Default | Valid range |
|---|---|---|---|---|
| `StateConfig.ReadTimeoutSeconds` | `state.read_timeout_seconds` | `SPARKCLAW_STATE_READ_TIMEOUT_SECONDS` | 10 秒 | 1-900 秒 |
| `StateConfig.WriteTimeoutSeconds` | `state.write_timeout_seconds` | `SPARKCLAW_STATE_WRITE_TIMEOUT_SECONDS` | 30 秒 | 1-900 秒 |

环境变量格式错误时，`config.Load` 失败并指出精确变量；配置文件或环境值越界时，
normalization 失败并指出 JSON field。两个值在同一 pilot commit 中加入
`config.Default`、`configs/sparkclaw.default.json`、File/Memory/PostgreSQL backend
option、command assembly 与 config test。它们不是 credential；只有现有 public
redacted config 已暴露 `state` object 时才可随之出现，S2 不新建 public Store
settings endpoint。

effective deadline 取非零 caller deadline 与 configured fallback 中更早者。
`context.Canceled` 绝不重新标记为 timeout。60 秒 multi-record transaction setting
在 S2 不增加，因为 pilot 没有 multi-record transaction；由第一个实际消费它的
S3 repository 引入。已接受的 180 秒 startup setting 保持不变。

现有 convenience constructor 使用已接受 default，让 test 和 local caller 保持
确定性。生产 assembly 通过 backend option 显式传入 validated config；不存在
package-global mutable timeout。

## Typed Error Contract

pilot 引入 `StoreError` 与符合 S0 contract 的 finite `StoreErrorCode`：

| Code | Pilot 用法 |
|---|---|
| `not_found` | 预留给要求 target 的 command；onboarding get 使用 normal absence |
| `conflict` | duplicate onboarding ID，并保留 `ErrISCPOnboardingConflict` |
| `invalid` | 确定性 receipt contract violation |
| `canceled` | 已知 effect 完成前 caller context canceled |
| `timeout` | 已知 effect 完成前超过 effective deadline |
| `unavailable` | backend 当前无法服务 operation |
| `durability_failed` | File candidate 确定在提交前/提交点失败，且 Memory 已恢复 |
| `unknown_outcome` | 已提交 File/PostgreSQL effect 需要对账 |
| `corrupt` | persisted payload 无法 decode 或违反 receipt invariant |
| `internal` | 未分类失败；fail closed 并审查分类 |

`StoreError` 包含 code、finite operation ID 与 wrapped cause，并实现 `Unwrap`；
helper 支持 `errors.As`、stable code extraction 和无需解析字符串的安全分类。
label 或 public copy 不能包含 record data。domain sentinel 保留在 error chain 中；
`pgconn.PgError`、filesystem path、DSN 与 raw payload 等 backend error 只作内部
cause。

## Backend 规则

### Memory

- 在获取 lock 前检查 effective context，并在 lock 内、mutation 或 read 前再次
  检查；
- 保留 normalization、duplicate、ordering 与 scope 行为；
- 在一个 lock 下只修改 receipt，不修改 caller-owned audit；
- 成功空列表返回 empty non-nil slice；
- 不返回 backend-owned mutable data。

pilot 不替换 Memory 现有 mutex。其 critical section 只在进程内执行且无 I/O；
等待期间发生的 cancel 会在任何 effect 前于 lock 内观察到。

### File

- 使用已接受的 [File Store 持久性](store-file-durability-design.md) gate 与 command
  state machine；
- 使用 effective caller context 获取 read/write admission；
- 只有 replacement 和 parent-directory sync 成功后才返回成功；
- 确定的 pre-submit failure 恢复完整 pre-snapshot 与 volatile sidecar；
- 不确定的 submitted replacement 返回并 fence `unknown_outcome`；
- get/list 在返回 data 前执行已定义的 reconciliation；
- 拒绝 missing state path，且保持 snapshot/encryption schema。

### PostgreSQL

- 把 effective context 传给每个 `Exec`、`Query`、`QueryRow`、scan loop 与
  reconciliation call；pilot 文件不含 `context.Background()`；
- SQLSTATE `23505` 映射为 conflict，同时保留
  `ErrISCPOnboardingConflict`；
- 只在 get 中把 `pgx.ErrNoRows` 映射为 normal absence；
- 返回 query、scan、JSON decode/validation 与 `rows.Err()` failure；
- 失败 query 绝不能变成成功空列表；
- 一行 save 使用一个 `Exec`，不包含 caller-owned audit；
- 同时依据 effective context 与 PostgreSQL cause 分类 context outcome，不使用
  string matching。

PostgreSQL 单行 autocommit 可能在 submission 后报告 connection failure。driver
无法确认 insert 是否 commit 时，结果为 `unknown_outcome`，必须按 ID get。确定性
unit test 通过 backend seam 验证此分类；configured integration test 验证真实
insert、duplicate、absence、list/rows handling、cancellation 与 restart。

## Consumer 与 Assembly 迁移

`iscppairing.Service` 不再保存 `store.Store`。它接受 consumer-owned minimal
composite，其中包含 `store.ISCPOnboardingRepository` 和现有 caller-owned audit
append capability。audit 部分只是等待 S3 `AuditRepository` 的临时依赖，不能让
service 获得无关 Store 方法。

consumer 精确变化：

- `Start` 把 request context 传给 save；
- `List` 变成 `List(ctx, ownerID) ([]app.ISCPOnboarding, error)`；
- GET handler 传入 `r.Context()`，且不能把 backend failure 序列化成空 `200`
  list；
- backend timeout 映射为 stable gateway-timeout response；unavailable、durability、
  unknown、corrupt 与 internal failure 映射为不暴露 raw Store cause 的 stable
  service-unavailable response；
- conflict/invalid 保持 stable client/domain failure；
- assembly 在 `newISCPPairingService` 接受 minimal composite；只有 backend factory
  和临时 broad assembly value 继续保留 `store.Store`。

authority call 仍先于 receipt save，因为 receipt 来自已签名 authority response。
authority contract 没有 revocation 或 idempotent request recovery。因此，确定的
local save failure 可能留下未披露 remote ticket；unknown save 可能留下可 list 的
receipt，但 signature 没有披露。service 在 persistence error 时 fail closed 且不
暴露 ticket。解决 remote/local atomicity 需要 authority protocol 变化，不能藏进
Store。

audit append 仍发生在 receipt persistence 成功之后，并在 `AuditRepository` 迁移
前保留 legacy failure behavior。S0 已明确把 audit 分配给 caller，因此 pilot 不
宣称 receipt 与 audit 原子。

## 临时 Broad Interface

S2 期间，broad interface 只 embed 一次 `ISCPOnboardingRepository`，并只声明其余
138 个 legacy method，不重复三个 migrated signature。`MemoryStore`、`FileStore`
和 `PostgresStore` 分别 assertion small interface，同时继续 assertion 临时 broad
interface。

新生产 consumer 不能接受 `store.Store`。source guard 证明 onboarding 旧签名和
`context.Background()` 已消失，并证明 `iscppairing.Service` 没有 broad Store
field 或 constructor parameter。

## S3 规划顺序

S2 实现并经人工验收后，推荐风险顺序保持为：

1. Owner、Client、Credential 与 Session；
2. Conversation、Run、Document、Approval、Audit、Evaluation 与 artifact
   metadata；
3. Schedule、Connector、Delivery Record、Passive Notification 与 External Chat；
4. MCP、Browser State 与 Memory。

每次只激活一个 repository。Session deletion、MCP redemption 和其他 cross-record
command 必须有显式 transaction case；小型 interface 名称不代表它们只是简单
CRUD。

## Pilot 验证与 Commit 边界

S2 实现使用两个可独立审查的 commit：

1. 全 File method 的机械 admission，不改 pilot 签名或 error behavior；
2. timeout/error operation boundary，以及 onboarding repository 在三个 backend、
   consumer、assembly 和 test 中的完整迁移。

pilot gate 要求：

- Memory/File shared contract test：success、absence、order/scope、duplicate
  conflict、cancellation、timeout 与 non-nil empty list；
- File 设计规定的默认 File injected-failure、rollback、fence、reconciliation、
  encryption、restart 与 race test；
- PostgreSQL unit classification：query/scan/rows/context 与 uncertain submission，
  再加 real-DSN integration evidence；
- `iscppairing` service test：context propagation、safe failure copy、persistence
  failure 后不披露 ticket，以及 caller-owned audit order；
- Gateway test：`r.Context()` propagation 和 list failure 非 200；
- 两个新 timeout 的 config default/environment/range/assembly test；
- source guard：interface 只 embed 一次、无 pilot 旧签名、无 pilot
  `context.Background()`、无 ignored pilot persistence/rows error，consumer 无 broad
  Store dependency；
- `go test ./...`、`go build ./...`、`go vet ./...`、聚焦 Store race、默认 File
  production-entry test、WebChat test/build 与双语 docs CI；
- 使用现有 `SPARKCLAW_TEST_POSTGRES_DSN` opt-in 的真实 PostgreSQL run；CI service
  topology 和 skip behavior 保持不变。

只有 interface scaffolding、operation registry 或 File gate 不能获得 S2 实现
`GO`；生产 caller 与三个 backend 必须全部使用新 contract。

## S4 Broad Store 删除

每个 S3 repository 实现都获得 `GO` 后：

1. 用 minimum repository 或 consumer-owned composite 替换其余 constructor
   parameter 与 field；
2. 删除 broad interface 与 global backend assertion；
3. 保留三个 backend 的 per-repository assertion；
4. 要求生产代码零 `store.Store` reference、零 repository type assertion、零
   dynamic repository map；
5. 验证 assembly 仍构造一个 selected backend，且没有 service locator。

S4 独立审查。最后一个 repository 仅仅 compile 不能开始 S5 supervision。

## 回滚

pilot 不修改 File snapshot 或 PostgreSQL schema shape。若被拒绝，可以回退
behavior commit 而不删除已独立接受的 mechanical gate，但仍以其单独审查决定为
准。S1 forward PostgreSQL migration 保持不变。

## 审查记录

| 审查 | Revision/commit | 决定 | 证据与未解决风险 | Reviewer/date |
|---|---|---|---|---|
| S2 pilot/S3 设计 | revision 1 candidate | pending | 精确 pilot signature、timeout/config flow、error、三后端行为、consumer 迁移与 remote-effect 风险等待独立审查 | pending |
| 每个 repository 实现 | pending | pending | 迁移期间为每个已接受 repository 增加一行 | pending |
| S4 Store 删除 | pending | pending | pending | pending |
