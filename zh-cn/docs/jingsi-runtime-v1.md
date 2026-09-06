# JingSi Runtime v1 提供方

> 语言：[English](../../docs/jingsi-runtime-v1.md) | 简体中文

SparkClaw 实现了 ProjectGroup-2 已接受的 `JingSi → SparkClaw` Runtime v1
契约中的提供方。权威来源仍是 InfiniCenter 决策 0007，以及其中的中央 JSON Schema、
HTTP 绑定和一致性测试夹具。此提供方独立于历史 JingSi-LAN Web 展示路由。

## 启用方式

该接口默认禁用，通过常规 Gateway 监听器提供服务。启用时，`gateway.bind` 必须是
字面形式的回环 IP。通过环境变量或仅 owner 可读取的普通文件提供且仅提供一个专用
服务凭据：

```bash
export SPARKCLAW_JINGSI_RUNTIME_V1_ENABLED=true
export SPARKCLAW_JINGSI_RUNTIME_V1_BEARER_TOKEN='<random service credential>'
export SPARKCLAW_JINGSI_RUNTIME_V1_STATE_DIR='/var/lib/sparkclaw/jingsi-runtime-v1'
```

也可以设置 `SPARKCLAW_JINGSI_RUNTIME_V1_BEARER_TOKEN_FILE`；该文件不能是符号链接，
且不能授予 group 或 other 任何权限。该 token 只作为秘密使用：不会序列化到公共配置、
响应、记录或错误中。

`SPARKCLAW_JINGSI_RUNTIME_V1_MAX_CONCURRENT` 将活跃 Runtime v1 工作限制为
1–64 个并发执行（默认 4）。`SPARKCLAW_JINGSI_RUNTIME_V1_RETENTION_DAYS`
（`jingsi_runtime_v1.retention_days`，默认 30，0 表示永久保留，最大 3650）限制终态记录和
negative fence 在状态目录中的保留时长；见下文"运行边界"。五个 POST 操作使用媒体类型
`application/vnd.infinimesh.sparkclaw-runtime.v1+json`：

- `/v1/executions:submit`
- `/v1/executions:lookup`
- `/v1/executions:status`
- `/v1/executions:cancel`
- `/v1/execution-events:list`

## 持久化对账

在任何 Agent Runtime 工作开始前，Submit 会以原子方式持久化已认证的
caller/request-key 绑定、规范化语义摘要、稳定 execution ID、授权、有界输入，
以及初始 accepted/queued 事件。完全一致的重放会返回该 execution；发生漂移时返回冲突，
且不会创建工作。查询从未绑定的 key 会提交一个不可逆的 `not_started` negative fence，
因此后续 Submit 无法重新激活该 key。契约禁止跨 caller/space 复用 request key，因此 key 绑定到已认证 caller；同一 key 从另一个 space 重放会冲突，不会创建新工作。

记录是仅 owner 可访问的 JSON 文件，通过文件同步、原子重命名和目录同步写入。记录包含
进程重启后恢复已接受 execution 所需的有界 goal 和 Memory Context，因此状态目录属于个人
runtime 数据，必须与其他 SparkClaw 状态处于相同的加密备份和访问控制边界内。bearer
永远不会存储在其中。

启动时，非终态的 accepted/queued/running 记录会使用相同的 execution/run ID 重新进入
执行流程。已有 Agent run 会被幂等读取。无法安全恢复的工作会转为明确的 failed 结果；
不会在新身份下静默重放。需要 Approval 的工作仍停留在现有审批流程中。取消意图会先于
活跃 context 的取消进行持久化，终态取消的重放结果保持稳定。

## 授权与输出投影

提供方会在每个操作中验证并持久化完整的 v1 授权信封。对于未知 execution 或授权不匹配的
execution，Status、events 和 cancel 会统一返回 `not_found`。Agent 入口接收精确的 task
身份，以及排序后的 tool/data/network/approval/grant 投影。Runtime 工具暴露要求
`tool_scope` 精确匹配；`approval_policy=deny` 会移除需要 Approval 的工具。每个请求的
deadline、最大运行时间、最大工具调用次数和最大输出字节数只能收紧现有的全局 Runtime
策略。契约接受最高 1 MiB 的 `budget.max_output_bytes`，但每个响应上限为 131072 字节，因此结果摘要无论请求多少都钳制在 64 KiB。

只有 JingSi 提供了有界 v1 `memory_context` 时才会包含 Memory。goal 仍是 risk、guard、
语义路由、消息控制和能力准入唯一的 owner intent 输入。Memory summary 保存在独立的
task-context 字段中，用于确定性恢复；只有在路由和授权边界冻结后，才会在明确的
data-only 标记下加入 workflow prompt。因此，恶意或仅仅偏离主题的 Memory Context
无法选择能力、添加返回端点或扩大工具权限。结果只暴露粗粒度状态、有界 summary 和不透明的
版本化 trace/artifact 引用；内部路径和 store 标识符不会越过该接口边界。

## 运行边界与日志

在 Gateway 绑定生命周期之前，提供方不提供任何服务：`Start` 之前的每个已认证操作都会收到
可重试的 `runtime_unavailable` Problem（`side_effects=none`），因此不会有 execution 在
不可取消的 context 下运行或逃脱进程关闭。

状态目录受保留期限约束。绑定到提供方生命周期的每小时清理（首次在 `Start` 时运行）会删除
终态完成时间超过 `retention_days` 的 bound 记录，以及提交时间超过该期限的 negative fence；
每次清理最多删除 5000 条记录，积压会分多次清理完成。非终态工作（accepted、queued、
running、approval_required）永远不会被清理。删除记录意味着遗忘该 key：之后再 Lookup 已清理
的 key 会提交一个新的 `not_started` fence（fence ID 按确定性规则保持相同），之后的 Submit 会
被视为首次请求。契约本身没有对对账设置时间上限，但 JingSi 只通过 Lookup 解决 unknown
Submit，在得到 `bound` 或 `not_started` 之前保持阻塞，并原子绑定已接受的 execution。因此该
窗口只需超过 JingSi 可能仍持有未对账 key 的最长中断时长；30 天留有充分余量，必须无限期
遵守 fence 的运营者可以将该值设为 0 并接受无界增长。

运行日志写入进程的 `slog` logger。bearer 被拒绝时记录累计失败次数与远端地址，绝不记录
所提交的凭据；幂等冲突记录 request ID、request key 和原因码（`negative_fence`、
`authorization_drift`、`semantic_drift`）；持久化记录写入失败记录操作名和不透明的记录 ID；
每个终态记录 execution ID、结果与事件数。按契约对日志的要求，任何一行都不包含 goal、
Memory Context、结果摘要或 bearer。

## 证据与剩余边界

`internal/contracttest` 校验中央 conformance manifest、HTTP binding 和 fixtures。它优先从
`SPARKCLAW_JINGSI_CONTRACT_MANIFEST` 解析 manifest，否则查找同级的 `InfiniCenter` 检出；两者都
不存在时（全新克隆、CI），该门禁会跳过而不是让 `go test ./...` 失败。

提供方测试覆盖完全一致的重放与漂移、跨重启持久化的 negative fence、响应丢失后的查询、
单调事件分页、统一授权、幂等取消、专用 bearer 路由、`return_nowhere`、data-only
Memory Context，以及分发到现有 Agent Runtime。JingSi 还负责一个开发门禁：独立启动
PostgreSQL 18、IMMS、SparkClaw、JingSi 和真实 JingSi-Node 进程，然后证明成功的 Task
结果对账、Observation 回写以及来源通知/ACK。该证据不能证明生产凭据配置、断电恢复、真实
网络或 GB10 实机验收；这些仍是跨仓库退出门禁。
