# 按 Owner 的 Connector 启用与 Runtime 协调设计

> 语言：[English](../../docs/connector-owner-runtime-design.md) | 简体中文

> 状态：已于 2026-08-17 为
> [issue #13](https://github.com/Infinimesh-ai/SparkClaw/issues/13) 接受并实现。
> 六项产品裁决记录在[已解决的决策](#已解决的决策)中。

## 决策摘要

SparkClaw 保留 `ConnectorSetting` 作为 owner 范围内的持久化显式选择。一个 owner 的设置必须
控制该 owner 的 inbound polling、endpoint 可见性、binding setup 和 outbound delivery，且不得
改变另一 owner 的访问能力。

Telegram 与微信的物理 worker 仍保持每个 channel 一个共享进程。当前两个实现都会扫描该
channel 的全部 active binding，并拥有 channel 级 polling、并发、inbox 和 cursor 协调状态。
按 owner 重复启动同一 worker 会造成重复 polling 与 dispatch。因此 Connector Registry 根据聚合
desired state 协调 channel worker，并向 worker 提供强制应用的 owner-aware gate，用于取得或
分发工作前的检查。

启动后，进程内 Connector Registry 成为 owner 有效启用状态的读取权威。它在启动 worker 前加载
全部持久化 `ConnectorSetting`，每次 Compare-and-Swap 更新成功后刷新缓存。Delivery 与 endpoint
解析不得为每条消息同步读取 Store。

`/api/config` 继续通过 `enabled` 返回当前 owner 的有效状态，并通过 `operator_enabled` 如实返回
channel 静态配置值；不能因为安装了 Connector Controller 就把该字段覆盖为 `true`。尽管沿用
`operator_enabled` 这一 public 字段名，该静态值只是尚无持久化 setting 的 owner 默认值，
并非 owner 无法覆盖的 operator gate。

受支持的部署是一个家庭共用一个 Gateway，并在其中实现逻辑 owner 隔离。Owner setting、binding、
endpoint 和 delivery authorization 不得跨 owner，但进程本身不是 hostile-tenant 安全边界：家庭
owner 共享同一个 Gateway 进程、Store、operator 和 channel worker pool。

Credential vault 告警不在范围内，因为提交 `e752c55` 已使其不再依赖 Telegram 静态开关。

## 问题

当前 Registry 按 `(owner_id, channel)` 持久化设置，却只按 `channel` 管理 runtime worker：

- `Start()` 只检查 `app.DefaultOwnerID`，所以重启后不会回放其他 owner 的持久化启用状态；
- 一个 owner 关闭 channel 会取消所有 owner 共用的 worker；
- Telegram 与微信 worker 枚举所有 active binding 时不检查所属 owner 当前的 ConnectorSetting；
- `Enabled()` 在 endpoint 和 delivery 热路径同步调用 `GetConnectorSetting`；
- public config projection 在取得 managed Connector status 后把 `operator_enabled` 覆盖成 `true`。

这把 owner 范围的 desired state 与 channel 范围的进程状态混在一起，既会造成跨 owner 干扰，
也会造成静默的重启恢复失败。

## 不变量

1. 一个 ConnectorSetting 只属于一个规范化 owner 和一个规范化已注册 channel。
2. Owner A 关闭 channel 后，A 的新 endpoint 解析、binding setup 和 outbound delivery 立即被阻断，
   但 owner B 的相同路径不受影响。
3. 一个共享 channel worker 在进程内最多运行一次。
4. 共享 worker 只有在 Registry 当前 owner gate 允许时，才能取得或分发该 owner 的工作。
5. 重启时必须先加载所有 owner 的持久化设置，再执行首次 runtime 协调。
6. Store 更新继续使用 Compare-and-Swap；冲突不能改变缓存或 runtime 状态。
7. Owner 关闭 connector 时保留 binding 与加密 credential。
8. Registry 启动后，delivery 与 endpoint 热路径只读进程内存。
9. 已关闭的 owner 不能通过 `ConnectorStatus.Running` 或 `LastError` 得知另一家庭 owner 的
   channel worker 是否在运行。
10. 默认 file backend、memory backend 和 PostgreSQL backend 实现相同的 ConnectorSetting
    全量枚举契约。
11. 家庭 owner 在同一可信 Gateway 内逻辑隔离；本设计不承诺 hostile tenant 之间的进程或
    基础设施隔离。

## 状态模型

对 owner `o` 和 channel `c` 定义：

- `configured(c)`：静态 `NotificationChannelConfig.Enabled` 值；
- `persisted(o,c)`：可选的持久化 ConnectorSetting；
- `effective(o,c)`：binding、endpoint、delivery 和 runtime gate 使用的 owner 级有效值。

最终采用以下兼容模型：

```text
effective(o,c) = persisted(o,c).enabled，若记录存在
                 configured(c)，否则

runtime_wanted(c) = configured(c)
                    OR 任一持久化 owner 的 enabled=true
```

静态配置因此仍是未持久化选择 owner 的默认值，而任一 owner 的显式 opt-in 可以启动默认关闭的
channel。即使共享 worker 因配置默认值或其他 owner 而继续运行，显式 opt-out 仍会阻断该 owner。

该公式是当前单 Gateway runtime 的权威语义。

## 职责与 API 变更

### Store

新增明确的全 owner 枚举操作，不把空 owner ID 解释成特殊含义。现有代码会把空 owner 规范化为
`DefaultOwnerID`，因此复用 `ListConnectorSettings("")` 会产生歧义。

新操作按稳定 `(owner_id, channel)` 顺序返回全部 ConnectorSetting，并区分查询失败与空结果。
以下 backend 必须同时实现：

- `MemoryStore`：读取现有 ConnectorSettings map；
- `FileStore`：透传读取其 MemoryStore snapshot；
- `PostgresStore`：对 `connector_settings` 执行一次有序查询。

记录已经存在，因此不需要新增 Snapshot 字段或 SQL schema migration。必须增加 Store contract 与
backend parity 测试。

### Connector Registry 缓存

Registry 持有独立 settings lock，以及以规范化 `ownerID + channel` 为 key 的 map。缓存记录与
无记录必须区分，因为显式 `enabled=false` 需要覆盖配置默认值。

启动时执行一次全 owner 加载，原子安装 snapshot，随后才协调已注册的 channel worker。
`Enabled(ownerID, channel)` 读取缓存；已知 channel 没有记录时回退静态配置。没有调用 `Start`
的 Registry 测试可以使用串行化 lazy fill，但生产热路径会在 HTTP server 监听前完成预加载。

`SetEnabled` 与 `SetMCPTransports` 会让记录读取和 CAS update 与 cache fill 串行。Store 更新成功后，
先把返回的完整记录写入缓存，再执行协调或 status projection。写入失败不改变旧缓存。Registry 是
生产环境中 ConnectorSetting 的唯一 writer；启动后直接写 Store 无法使进程状态失效，因此不受支持。

本设计不增加 TTL。TTL 会重新引入周期性同步 PostgreSQL 读取，却不能解决跨进程 writer 一致性。
若将来支持多个 Gateway writer，应增加明确的失效事件流或 revision protocol，而不是在消息热路径轮询。

### 共享 Runtime Scope

Runtime contract 同时携带 owner gate 和 Gateway lifecycle：

```go
type RuntimeScope struct {
    Channel          string
    OwnerEnabled     func(ownerID string) bool
    LifecycleContext context.Context
}

type Runtime interface {
    Run(acquisitionContext context.Context, scope RuntimeScope) error
}
```

Registry 为一个固定 channel 创建具体 scope。Scope 不暴露 Store，也不能检查或修改其他 connector
的状态。

Telegram 在生成 polling binding 列表时应用 `OwnerEnabled`，并在分发持久化 inbox item 前再次
检查。微信在每次 Tick 生成 binding 列表时应用 gate，并在分发已取得 batch 前再次检查。第二次
检查封闭 owner 在 polling 后、Agent dispatch 前关闭 connector 的竞态窗口。

MCP 没有注册 polling Runtime，继续在 endpoint 和 delivery 路径使用相同的 owner gate。

### Runtime 协调

`runtimeRuns` 与 `runtimeErrors` 继续按 channel 建 key。这与物理 worker 职责一致，并避免重复
provider polling。

启动或 setting 成功更新后，`reconcileChannel(channel)` 从内存 snapshot 重新计算
`runtime_wanted(channel)`：

- false -> true：只启动一次已注册 worker；
- true -> true：保留当前 worker，由 owner gate 读取新的 cache 值；
- true -> false：取消 worker；
- false -> false：不执行操作。

取消会停止 acquisition，但已接纳工作排空期间该 run 仍保留在 Registry 中。若 owner 在这段时间
重新开启 channel，协调逻辑不会并行启动第二个 worker；旧 run 返回后，Registry 根据最新聚合
desired state 再启动一个 replacement。Gateway shutdown 会同时取消 acquisition 与 lifecycle
context，因此进程关闭仍是 hard stop。

启动、停止或等待 worker 时不持有 settings lock。协调方法重新读取最新聚合状态，因此不同 owner
的并发更新会收敛到最新 snapshot，而不会应用陈旧的引用计数。

Worker 意外退出仍是 enabled owner 共享的 channel 级故障。自动重启策略不属于 issue #13；除非
另行设计，否则保留现有错误行为。

### Status 与 Public Config

`ConnectorStatus.Enabled` 表示调用 owner 的有效状态。只有该 owner 已启用且共享 channel worker
正在运行时，`ConnectorStatus.Running` 才为 true。`LastError` 只向 enabled owner 投影。这避免
opt-out owner 观察到另一 owner 的 worker 活动，同时为依赖该 worker 的 owner 保留有用错误。

对 `/api/config`：

- `enabled` 是调用 owner 的有效状态；
- `operator_enabled` 直接复制 `NotificationChannelConfig.Enabled`，不再由 controller 是否存在覆盖；
- `available`、`binding_status`、`startable` 和 `disabled_reason` 继续来自 owner 范围 Connector status。

Issue 不要求前端行为变更，因为 WebChat 当前只声明类型，未消费 `operator_enabled`。仍需增加聚焦的
API projection 测试。

## 关闭边界

现有 owner gate 会立即阻断所有新解析的 outbound work。Inbound runtime 在下一次选择 binding 时
停止为 disabled owner 取得 provider update，并在 Agent dispatch 前再次检查。

Setting 更新前已经 dispatch 的工作继续完成 Agent turn，并投递到其精确 source reply。Inbound
adapter 会在该 return route 上冻结 `SourceAdmitted` 标记；endpoint 和 Provider 层仍检查 binding
身份与撤销状态，但不会把 owner 后续关闭追溯应用到这一条已接纳回复。只有 Telegram、微信和 MCP
ingress 会创建该标记；没有 source admission 的新构造发送仍会被阻止。

已经持久化但尚未 dispatch 的 provider work 保持 pending。Telegram 跳过 disabled owner 的 inbox
record，且不改变其状态；微信把 provider cursor 保留在被跳过 batch 之前。Owner 重新开启后会恢复
这些记录。这是由 eligibility 实现的暂停，不是删除或 terminal cancellation；acquisition loop 仍有
边界，不会对暂停 owner 形成 busy polling。

## 启动失败

PostgreSQL 全 owner 加载失败不能被误认为 Store 为空。因此新枚举操作返回 error；Gateway 在
HTTP listener 打开前启动失败，进程以非零状态退出。回退静态默认值可能违背 owner 的持久化选择，
意外启用或关闭 inbound channel。

## 兼容与迁移

- 原有 ConnectorSetting 记录和 version 原样复用。
- 原有 file snapshot 不需要重写。
- 原有 PostgreSQL table 不需要 migration。
- 没有记录的 owner 使用静态配置作为默认值。
- Runtime 仍是每 channel 一个进程，因此不会复制 provider cursor 与 bounded worker pool。
- Binding、credential 和 inbox record 不会被删除或重新分配。
- Public `operator_enabled` 从 controller-presence 常量改为配置值；该行为变化记录在中英文
  Changelog 中。

## 验证矩阵

### Store 与缓存

- Memory、file reload 和 PostgreSQL integration 按稳定顺序列出全部 owner，并区分查询失败与空集。
- 显式 false 作为记录缓存，并覆盖配置默认值。
- 启动后重复 `Enabled`、endpoint lookup 和 delivery check 不调用 `GetConnectorSetting`。
- CAS 成功刷新 cache；陈旧 CAS conflict 不改变 cache 或 worker state。
- `SetMCPTransports` 保留 `Enabled` 并刷新完整缓存记录。

### Lifecycle 与隔离

- 静态默认 false 时，重启后非默认 owner 的持久化 enablement 启动 channel 一次。
- 两个 owner 启用仍只启动一个 worker。
- 关闭 owner A 后 worker 保持运行，owner B 仍然 eligible。
- 关闭最后一个 enabled owner 会停止默认关闭的 channel。
- 新 owner 启用后，无需重启已运行 worker 即可变为 eligible。
- Telegram 与微信在 acquisition 时跳过 disabled owner binding，并在 dispatch 前再次检查。
- Race 测试覆盖 owner 并发更新、`Enabled` 读取、worker 退出和 Gateway cancellation。

### API 与行为

- Connector list/status 继续保持 owner scope。
- Disabled owner A 无法通过 owner B 的 active runtime 解析 endpoint 或 delivery。
- 无论静态值 true/false，`/api/config.operator_enabled` 都等于静态 channel 值，而 `enabled`
  反映调用 owner。
- Changelog、architecture、messaging/integration guide 及中文镜像更新为最终语义。

### 命令

完成 document-tool setup 后，实现验证包括：

```bash
cd services/gateway && go test ./internal/store ./internal/connector ./internal/telegram ./internal/weixin ./internal/messagecontrol ./internal/gateway ./cmd/sparkclaw
cd services/gateway && go test -race ./internal/connector ./internal/telegram ./internal/weixin ./internal/messagecontrol
cd services/gateway && go test ./...
cd services/gateway && go vet ./...
npm --workspace @sparkclaw/webchat test
npm --workspace @sparkclaw/webchat run build
bash scripts/doctor.sh
```

仓库的双语 Markdown mirror 和本地 link check 同样是 gate。默认 file backend 与 PostgreSQL 产品
配置都要覆盖，只用 memory 证明不充分。

## 已解决的决策

1. `NotificationChannelConfig.Enabled` 是没有持久化 setting 的 owner 默认值；owner setting
   可以覆盖它。
2. Owner 关闭 connector 时，已经 dispatch 的工作继续完成并投递精确的已接纳 source reply；
   Gateway shutdown 仍会取消该工作。
3. 已持久化但尚未 dispatch 的 provider work 保留，重新开启后恢复；关闭不会终止取消它。
4. 全 owner setting 预加载失败会在 listen 前令 Gateway 启动失败，并产生非零进程退出状态。
5. Connector Registry 是唯一受支持的 setting writer；runtime 期间直接 SQL 更新不受支持，
   直到重启才会被读取。
6. 多租户指家庭成员在同一 Gateway 内的逻辑 owner 隔离，不是 hostile tenant 的进程、Store
   或基础设施边界。

这些裁决消除了剩余产品歧义，使设计把握超过用户要求的 90% 阈值。本记录中的实现与验证矩阵
共同编码最终契约。
