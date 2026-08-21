# Store Connector Repository 设计

> 语言：[English](../../docs/store-connector-repository-design.md) | 简体中文

> 状态：设计候选。精确文档提交未获得独立 `GO` 前，不得编写生产实现。

## 范围

本阶段迁移 Connector 所属的八个 Store 方法、三个后端及全部生产调用方，
并闭合已验收的 [CredentialRepository 设计](../../docs/store-credential-repository-design.md)
刻意留下的重启缺口：任何 provider 调用或凭据 Seal 之前，binding 身份必须先
持久化；取消、Store 结果不确定或进程重启后，持久化的非 active 状态负责授权
凭据清理。

本阶段不迁移其他 Store repository，不移除宽泛 `Store` 依赖，不监管无关 runtime，
不拆分 Store 大文件，也不改变 ASR/PostgreSQL CI 拓扑。这些工作仍属于后续
S4-S6 和 CI 阶段。

## Repository 契约

```go
type ConnectorRepository interface {
    GetConnectorSetting(context.Context, string, string) (app.ConnectorSetting, bool, error)
    ListConnectorSettings(context.Context, string) ([]app.ConnectorSetting, error)
    ListAllConnectorSettings(context.Context) ([]app.ConnectorSetting, error)
    UpdateConnectorSetting(context.Context, app.ConnectorSetting, int64) (app.ConnectorSetting, error)

    CreateNotificationBinding(context.Context, app.NotificationBinding) (app.NotificationBinding, error)
    GetNotificationBinding(context.Context, string) (app.NotificationBinding, bool, error)
    ListNotificationBindings(context.Context, string, string) ([]app.NotificationBinding, error)
    UpdateNotificationBinding(context.Context, NotificationBindingUpdateCommand) (app.NotificationBinding, error)
}
```

`Store` 只嵌入一次该接口；Memory、File、PostgreSQL 分别直接断言实现。八个旧签名
同时删除，不保留兼容接口、动态类型断言或丢弃错误的 wrapper。
`RevokeNotificationBinding` 被删除，因为撤销是多步骤持久化生命周期，而不是一次
repository 写入。

`ListNotificationBindings` 保持现有 `(channel, status)` 过滤，以便共享 channel worker
枚举所有 owner；空过滤代表全部。owner 授权仍由 Gateway/connector 所有者层负责并
由测试覆盖；repository 错误不得伪装为空列表成功。

### Binding 数据

`app.NotificationBinding` 新增：

```go
Version        int64  `json:"version"`
CredentialKind string `json:"credential_kind,omitempty"`
```

`Version` 从一开始，每次 binding 变更都递增，包括自动取消默认项。
`CredentialKind` 不属于秘密；新 Seal 的凭据必须设置它。旧 active 且带 ref 的记录
允许为空，因为按精确 ref 删除不需要 kind。非空且不以 `config:` 开头的 ref 属于
Vault；在包括终态在内的所有 retained binding 中，一个 Vault ref 只能属于一个
binding ID。`config:` ref 由 operator 持有，可以共享。公共投影不暴露 kind。

binding ID 必须由调用方在 create 前生成，最长 256 字节，并且永不复用。owner、
actor、channel、ID、创建时间及非空 provider 在 create 后不可变。所有输入、输出和
快照边界都复制 scopes 与时间指针。

### 不透明条件更新

唯一更新工厂为：

```go
func NewNotificationBindingUpdate(
    previous app.NotificationBinding,
    replacement app.NotificationBinding,
) NotificationBindingUpdateCommand
```

命令字段全部私有。工厂验证完整 prior 记录，对所有持久化字段生成带 domain、长度
分隔的 SHA-256 摘要，并携带规范化 replacement。ID 或不可变身份字段不同则命令
无效。调用方不能指定下一版本和 repository 时间；repository 分配
`previous.Version + 1`，并使用严格高于该 binding 时间高水位的 UTC PostgreSQL
微秒精度 `UpdatedAt`。

后端在命令锁内先验证持久化行，再执行业务判断，并要求与冻结 prior 完全一致。
缺失为 `not_found`，其他有效版本/值为 `conflict`，损坏状态为 `corrupt`。不存在
无锁 get/update 间隙。

## 稳定语义

每个方法都将调用方 context 与注册的 read/transaction timeout 组合，并在等待锁前
和获得锁后检查 context。取消、超时、传输、耐久性、不确定结果、扫描和行迭代错误
均保持为类型化 Store 错误。

Connector setting 保持现有规范化和 CAS 行为：

- 空 owner 变为 `app.DefaultOwnerID`，channel trim 后转小写；
- 空 channel 读取属于正常缺失；
- 列表成功时返回非 nil 空 slice，并按 channel 或 owner/channel 排序；
- update 要求非负 expected version，仅 expected version 为零时创建，否则必须匹配；
- repository 分配版本/时间，并原子提交 setting、现有类型化 audit 和 event；
- 并发 create/update 返回 `conflict`，不得 last-writer-wins。

Binding create 要求完整的调用方生成身份、`starting` 状态、输入版本零、无生命周期
时间、无 credential ref、无 provider session/外部身份/cursor/二维码数据且无错误。
repository 分配版本一和创建/更新时间，并原子写入
`notification_binding.starting` audit/event。任何已存在 ID（包括终态）均为
`conflict`。

空或不存在的 binding ID 读取属于正常缺失。binding 列表按 `UpdatedAt DESC`、
`ID ASC` 排序；空成功必须非 nil。所有扫描行必须先验证，之后才能过滤或判断状态。

### 状态机

repository 仅接受下列迁移：

```text
starting
  -> waiting_scan | waiting_confirm | active | failed | revoking
waiting_scan
  -> waiting_scan | waiting_confirm | credential_pending | active | expired | failed | revoking
waiting_confirm
  -> waiting_scan | waiting_confirm | credential_pending | active | expired | failed | revoking
credential_pending
  -> active | failed | revoking
active
  -> active | revoking
revoking
  -> revoked
failed | expired | revoked
  -> 不允许迁移
```

waiting 同态更新只能改变 provider 轮询元数据及安全的展示/错误字段。active 同态更新
可改变投递身份、context、cursor、默认项、scopes 和稳定错误，但不能改变 credential
ref/kind 或不可变身份。

只有进入 `active` 时可以安装 credential ref。新记录的 `cred_` ref 要求非空 kind；
配置持有的 ref 可以为空 kind，且永不传给 Vault 删除。从 active 进入 `revoking`
必须保留 ref/kind。`revoked` 清除默认项并记录 repository 分配的 `RevokedAt`，但保留
非秘密 ref/kind 作为终态证据。`failed` 和 `expired` 永远不能恢复为可轮询或 active。

安装或保留 Vault ref 时，任何其他 binding ID（包括 failed/expired/revoked）都不得
携带同一 ref。Memory/File 在全局命令锁内检查；PostgreSQL 在同一事务内使用部分
唯一索引和 credential-ref advisory barrier 强制执行。重复项为 `conflict`；启动时
已经存在的重复项为 `corrupt`。因此终态保留也会永久保留旧 ref 的所有权，与 binding
ID 不复用规则一致。该不变量才是后续精确 `revoking` 记录调用 `Delete(ref)` 的跨
binding 所有权证明；仅 envelope 相等不构成所有权。

若 active candidate 成为默认项，同 owner/channel 的其他 active 默认项必须在同一
命令内取消。每个被取消记录都获得新版本/时间及独立的脱敏 audit/event。PostgreSQL
在 readiness 校验排除旧重复项后，增加每个 owner/channel 仅一个 active 默认项的
部分唯一约束。

## Operation 与结果

| Operation | 模式 | 超时 | 不确定结果协调 |
|---|---|---|---|
| `connector_setting.get` | read | read | 自身/barrier |
| `connector_setting.list` | read | read | 无 |
| `connector_setting.list_all` | read | read | 无 |
| `connector_setting.update` | write | transaction | 精确 candidate |
| `notification_binding.create` | write | transaction | 精确 candidate |
| `notification_binding.get` | read | read | 自身/barrier |
| `notification_binding.list` | read | read | 无 |
| `notification_binding.update` | write | transaction | 精确 candidate |

candidate 形成后的未知写入会同时返回规范化 candidate 和类型化错误；确定失败返回
零值。协调通过同一记录 barrier 后的新 context read 执行。精确 candidate 证明成功；
精确 prior 证明回滚并允许构造新的条件重试；缺失或其他记录仍未解决。生命周期
调用方在清理前还必须执行下述更严格的凭据规则。

## 后端规则

### Memory

Memory 在同一锁内执行命令及其 audit/event，复制全部 slice/指针，使用 setting/binding
各自不可回滚的时间高水位，并且不暴露部分结果。context 在等待锁前和获得锁后检查。

### File

Connector 方法使用已验收的 admission 与 `runFileCommand`；Connector 字段不再经过
旧 `persist()`。提交前确定失败恢复 setting、binding、audit 和 event；rename/目录
sync 后的不确定结果安装标准 File fence，后续迁移读写必须协调或失败。时间高水位
位于 rollback 之外。

启动仅接受以下明确兼容规范化：

- 空旧 setting owner 变为默认 owner，空 binding actor 变为其 owner；
- 旧 setting/binding 的 version 零变为一；
- 旧 active/ref-bearing binding 允许 credential kind 为空，但每个非 `config:` ref
  仍必须只有一个 binding owner；
- 已发布的旧状态（`waiting_scan`、`waiting_confirm`、`active`、`expired`、`failed`、
  `revoked`）仍有效。

map key 必须等于内嵌 ID；身份、创建/更新时间、现有指针时间、版本、状态关系、非空
scope、全局 Vault-ref 所有权及唯一 active 默认项必须验证。新恢复状态必须具备规定
字段。其他损坏值不得规范化；Store 对外服务前启动即以 `corrupt` 失败。

### PostgreSQL

schema 新增/回填 binding `version`、增加 `credential_kind`，并在校验后加入 active
默认项唯一约束及覆盖所有非空、非 `config:` credential ref 的部分唯一索引。CI
service、workflow 拓扑和 `SPARKCLAW_TEST_POSTGRES_DSN` skip 行为不变。

命令使用自有连接和显式事务。setting 写使用 domain-separated owner/channel advisory
barrier；binding 写按唯一记录的固定顺序获取 owner/channel、binding-ID，并在存在时
获取 Vault-ref barrier。
协调读使用 `READ COMMITTED`，在一条语句获取匹配 barrier，再以另一条语句查询。
mutation、自动默认项取消、audit 和 event 必须原子提交。

连接获取后不安全的 statement、context、transport 或 commit 失败均为
`unknown_outcome`；连接必须 terminate，不能 release。安全的服务端拒绝和业务冲突
回滚；rollback 失败必须 terminate，并保留原始、rollback 与 termination cause。
必须检查 rows-affected 和 `rows.Err()`。

readiness 前，每个 setting/binding 行都通过与 File 相同的兼容 validator，包括 active
默认项唯一性。运行期扫描也使用该 validator；损坏行不得变成缺失或空列表。

## 凭据生命周期集成

Connector 阶段同时引入 public recovery surface 及其首个生产调用方：

```go
type CredentialLifecycleRecovery interface {
    Delete(context.Context, string) error
    AbortSeal(context.Context, string, string) error
}
```

`Delete` 接受确定性 `cred_` ref，以及已发布的精确旧 Weixin QR 命名空间
`provider:openclaw-weixin-qr:<bindingID>`。新 ref 必须是已认证 envelope。只有该旧
命名空间允许保存旧 Weixin kind 的迁移前明文值或已认证的重新封装 envelope，并且
Connector 只有同时持有精确 `revoking` 记录、与该记录 binding ID 相同的 ref 后缀和
repository 全局唯一 ref 不变量时才可条件删除。其他 provider ref 或明文 ref 一律
fail closed。
`AbortSeal` 从 binding ID 推导确定性 ref，要求 expected kind，只条件删除该 envelope。
缺失属于幂等成功；
conflict、replacement、损坏 envelope 或未解决 Store 结果均映射为稳定 credential
unavailable。两者都不返回 repository candidate 或 secret。

Connector Registry 持有 binding 生命周期协调器，并显式接收 `ConnectorRepository` 与
`CredentialLifecycleRecovery`，不得通过类型断言发现。单个 binding 的生命周期变更
在进程内串行，但 repository CAS 仍是持久化权威。

### Start

1. 解析已注册 adapter、生成不可变 ID，并在 adapter/provider 调用或 Seal 前持久化
   `starting`。注册 adapter 在 create 前提供 provider 和非秘密 credential kind；永不
   Seal 的 adapter 使用空 kind。
2. create 的确定失败或未解决结果直接返回 unavailable，不得调用 adapter。
3. Telegram provider 验证和 Seal 只能基于精确 create 返回的 `starting` candidate；
   条件 active 更新安装返回 ref。
4. Weixin start 条件记录 `waiting_scan`/`waiting_confirm`。poll 返回明文时，先条件记录
   带 provider 元数据与 kind、不可轮询的 `credential_pending`，再 Seal，最后条件记录
   带 ref 的 active。
5. provider/Seal 失败只输出稳定错误。只有 barrier 证明精确 pre-active 状态后才能
   清理；否则保留持久化状态供启动恢复。

active 迁移不确定时，精确 active 证明成功；精确 `starting` 或
`credential_pending` 证明可授权 `AbortSeal`，成功 abort 后再条件改为 `failed`。
其他或未解决状态保持 unavailable，绝不清理。

### Poll API

`GET /api/notification-bindings/{id}` 改为纯读取。provider poll 与状态推进迁移到
`POST /api/notification-bindings/{id}/poll`；WebChat 在 setup pending 时调用该命令。
这消除当前 GET 副作用，同时保持可见 setup 流程不变。

### Revoke

撤销先条件记录保留凭据证明的 `revoking`，再取消本地/provider 工作。Vault ref 使用
`Delete`；带 kind 的 pre-active 记录使用 `AbortSeal`；配置持有或无凭据记录不调用
Vault。只有清理成功后才条件记录 `revoked`。失败保留 `revoking` 并返回稳定
unavailable。

终态记录持续保留，其 ID 不能重新创建。对 `revoked` 重复撤销幂等；`failed`/
`expired` 保持各自终态。

### 启动恢复

Connector Registry recovery 在 setting preload 后、任何 connector worker 启动前、
Gateway listen 前运行，并列出和解决：

- `starting`：kind 非空时执行 `AbortSeal(bindingID, kind)`，否则不清理 credential，
  之后条件改为 `failed`；
- `credential_pending`：同样 abort 后条件改为 `failed`；
- `revoking`：根据保留证明执行 `Delete(ref)`、`AbortSeal` 或 no-op，再条件改为
  `revoked`。

Store/Vault recovery 失败必须保留非 pollable 记录并导致 connector readiness 失败。
waiting 记录保持可轮询，active 记录可供 worker 使用，终态记录保留。Audit 绝不能
充当生命周期 receipt。

## 调用方迁移

Gateway、Connector Registry、binding setup、Telegram、Weixin、Notification、Message
Control、Reminder Target、Delivery、schedules 和 ToolHub 调用方必须传递自身 context
并处理类型化错误。迁移后的生产调用方不得使用 `context.Background()` 或丢弃
Connector 返回值/错误。

active binding 的操作性更新（claim、context token、cursor 和稳定 last error）使用
精确 CAS。单调 cursor 写入方只有在重新加载并重新判断单调性后，才能有限重试冲突；
不得重放旧的完整记录。生命周期迁移不进行通用重试。

宽泛 `store.Store` 构造依赖可以保留到 S4，但调用方只使用嵌入的
ConnectorRepository 方法。测试使用真实 repository fixture 或共享的 create/transition
helper，不为测试保留已删除 upsert API。

## 验证门禁

实现只有同时满足下列条件才可 `GO`：

- 精确 interface/signature/operation/source guard，Store 仅嵌入一次，三个后端直接断言；
- Memory/File/PostgreSQL 在 context、缺失、排序、CAS、状态迁移、终态 ID 保留、默认
  项取消、audit/event 原子性、全局 Vault-ref 所有权/冲突、损坏、别名和时间高水位
  方面一致；
- File 所有提交前/提交后故障注入、重启兼容、fence 协调，且 Connector 不再使用
  `persist()`；
- PostgreSQL acquisition、barrier、每个 statement、commit、rollback/termination
  ownership、rows affected、row iteration 和启动验证故障矩阵；
- 通过 DSN 跳过的真实 PostgreSQL 并发 create/update/default/ref-ownership 测试，且
  CI 拓扑不变；
- Telegram/Weixin 的 create-before-provider、pre-Seal 状态、active 更新不确定、安全
  abort、revoking/delete 及 worker 前启动恢复窗口测试；
- 真实 File 重启保持未完成清理所有权和终态 ID，拒绝重复旧 Vault ref；未解决或共享
  所有权证明绝不删除 credential；
- GET 纯读取、显式 POST poll 的 Gateway/WebChat 测试，以及稳定公共错误/脱敏断言；
- `go test ./...`、`go build ./...`、`go vet ./...` 和聚焦 race 测试通过。

Connector 实现 `GO` 后，还要执行一次最终 Credential/Connector 集成审查，移除
foundation 临时豁免；只有该门禁可以宣布 credential 生命周期完成，之后才开始下一个
S3 repository。

## 提交边界

1. 本中英文设计与独立设计门禁。
2. Connector 契约、operation、数据兼容、三个后端及 repository contract/fault 测试。
3. Connector 生命周期协调器、public Vault recovery、显式 poll 命令、生产调用方迁移
   和集成测试。
4. 独立实现门禁和最终 Credential 集成门禁。

这些提交不混入 Store 大文件拆分。
