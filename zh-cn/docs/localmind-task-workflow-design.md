# LocalMind Workflow

> 语言：[English](../../docs/localmind-task-workflow-design.md) | 简体中文

> 状态：2026-08-26 已实现的当前组件指南。

## 已确认的产品边界

LocalMind 在 MCP 边界内负责任务规划、文档发现、Agent 执行和任务状态流转。
SparkClaw 只负责路由、参数绑定、授权、调用、等待/恢复、结果校验和交付。

LocalMind 仅支持显式调用，永远不是默认执行方。owner 请求必须明确指定
LocalMind。只提到或讨论 LocalMind 而没有分配工作时，仍是普通对话。

初始转交只支持文字，并且只包含本次消息的文字。不附带历史对话、memory、
tool output、隐藏上下文、图片、音频、视频或文件附件；省略 `documentIds`。

## 能力拓扑

最新的 Approval 拆分覆盖此前三个叶子的方案。`localmind` 仍是与 `document`
同级的一级分支，但现在包含四个功能叶子和四个 r1 Workflow Profile：

```text
capability
  |- conversation
  |- browser
  |- document
  |- localmind
  |    |- localmind.read
  |    |- localmind.write
  |    |- localmind.query
  |    `- localmind.cancel
  |- schedule
  `- coding
```

leaf ID 与 Workflow Profile ID 相同。

| 叶子/Profile | 业务功能 | Approval | 远端操作 |
|---|---|---|---|
| `localmind.read` r1 | 委派被请求为不产生修改的回答、阅读、调研或总结任务 | 不需要 | `delegate_to_localmind` |
| `localmind.write` r1 | 委派可能创建、更新、重命名或以其他方式修改 LocalMind 文件/文档的任务 | 需要 | `delegate_to_localmind` |
| `localmind.query` r1 | 读取一个 LocalMind 任务的当前状态或结果 | 不需要 | `get_localmind_task` |
| `localmind.cancel` r1 | 请求取消一个未完成的 LocalMind 任务 | 需要 | `control_localmind_task(action=cancel)` |

## Workflow 形态

每个 Profile 只有一次产生 effect 的业务工具调用，不增加模型工具选择、模型可见的
directory search、LocalMind 业务规划或其他能力 fallback。Runtime 仍通过普通 ToolHub scope
边界物化唯一精确 capability，这不构成额外 Workflow 节点。两个委派 Profile 在 delegation 后
额外使用一个只读 `query_current_task` 节点等待任务终态。

```text
localmind.read / localmind.write
  -> 调用一个精确委派工具
  -> terminal=true：校验并投影实际终态
  -> terminal=false：持久化 task ref，进入 query_current_task
  -> 使用最新已知 state version 调用 get_localmind_task
  -> terminal=false：持久化新状态并重复 query_current_task
  -> terminal=true 且 status=completed：把结果作为成功交付
  -> terminal=true 且 status=failed/cancelled：交付该实际终态

localmind.query
  -> 绑定一个 task ref
  -> 调用一次 get_localmind_task
  -> 返回当前 state/result

localmind.cancel
  -> 绑定一个 task ref
  -> Approval
  -> 调用一次 control_localmind_task
  -> 返回取消 state
```

`query_current_task` 是 `localmind.read` 和 `localmind.write` 内部的节点，
不会路由到独立的 `localmind.query` 叶子。该节点使用与 query 叶子相同的只读
`localmind.task.get` ToolHub capability，但 task ID 和 state version 来自 Runtime
冻结绑定，不产生新的意图路由决策。在委派 Workflow 等待期间，query 和 cancel
仍是可由独立意图路由选择的能力。

## 读写路由

两个委派叶子都要求明确把任务交给 LocalMind，然后按请求的 effect 分类：

| Route | 正向语义 | Hard negative |
|---|---|---|
| `localmind.read` | 在不创建或修改 LocalMind 内容的前提下回答、查看、查找、调研、比较或总结 | 创建/更新/重命名/删除/导出文件；effect 不明确 |
| `localmind.write` | 创建、更新、重命名、转换或以其他方式修改 LocalMind 内容 | 纯回答/阅读/调研/总结；查询 task；取消 task |

可能产生修改但语义不明确的委派不能进入免审批 read Profile。它应要求澄清；只有
owner 请求明确要求 mutation 时才选择需要审批的 write Profile。

根据 owner 决定，`localmind.read` 保持可用，并且无需 Approval 即可调用现有
`delegate_to_localmind` 契约。server-enforced read-only delegation 不属于 r1 边界，
也不阻塞实现。read/write 区分只控制 SparkClaw 的路由和审批行为，不会转换成
LocalMind MCP 的 effect-mode 参数。

## 委派完成状态查询

只有 LocalMind 任务进入终态后，owner 才收到最终结果。queued 或 running 响应
以及 approval-required 等其他非终态响应都不是 SparkClaw 的终态回复。

每次 delegation 或状态查询返回非终态后，Runtime 都通过冻结 route、Workflow state、
outcome ref 和 tool call 持久化：

- `taskId`、endpoint identity 和精确 MCP contract revision；
- 当前 `stateVersion`、status 和 terminal 标志；
- 来源 session、run 和 tool-call provenance；
- 冻结的来源 return route 和 Workflow identity。

第一次 `query_current_task` 调用绑定 delegation 返回的 `taskId`，把最新
`stateVersion` 绑定为 `known_state_version`，并绑定有界的 `wait_ms`。adapter
将它们映射为 LocalMind 的 `taskId`、`knownStateVersion` 和 `waitMs` 字段。后续
调用使用最新校验通过的 state version。每个 Workflow 同时最多保留一个 long-poll
请求，且 `wait_ms` 不得超过 MCP 契约上限 30,000 毫秒；非终态结果重新进入同一个
节点，不新建 Workflow，也不重新路由。

每个结果必须保持相同的 task ID，冻结 capability scope 也必须继续匹配相同的
endpoint/contract identity。malformed result、MCP `isError`、认证失败、task ID 变化或
注册 snapshot 变化都不能完成 Workflow。`terminal=true` 只表示停止查询，不等于成功：
只有成功的 completed 终态才能把 LocalMind 结果作为成功交付；failed 和 cancelled 必须
按其真实终态投影。

每次尝试后都持久化最新 query-node state，因此 Gateway 重启后可以使用同一个冻结
task 和 return route 恢复 `query_current_task`。Runtime 不能保留无界 goroutine
或开放的 MCP 调用。总体等待期限从 LocalMind 成功接受 delegation 起计算 10 分钟；
达到上限后返回包含 task ID 和最新已校验状态的明确 timeout，绝不报告成功。

## 上下文任务引用

r1 不新增 LocalMind task 持久化仓库，使用现有已持久化的 session、Workflow、
result 和 tool-call record 派生上下文索引。

每个经过校验的 delegate/query/cancel 结果都产生 `localmind_task` ResourceRef。
query 和 cancel 按以下顺序解析目标：

1. 当前 owner 消息中原样出现的精确 `taskId`；
2. 对“最近的任务”“刚才那个任务”等表达，选择同一 session 最近一次委派的
   LocalMind task；
3. 仍无法确定时要求澄清，不调用 MCP。

目标只能来自 owner 原文或已经校验的 Runtime evidence。模型可以分类意图，但不能
发明或改写 task ID。r1 不支持跨 session 的相对引用；跨 session 时 owner 必须
提供精确 task ID。

## 查询与取消

`localmind.query` 只调用一次 `get_localmind_task`。建议 r1 使用 `wait_ms=0`
立即读取，用户可以再次显式查询。它不自动轮询、不启动、不重试、不恢复，也不
修改任务。

`localmind.cancel` 先经过 Approval，再使用 Runtime 生成的稳定 idempotency key 调用一次
cancel。r1 Workflow 不发送独立 reason，也不先查询。运行中任务可能返回“已经请求协作式取消”
而不是终态取消；SparkClaw 原样返回该状态，不自动轮询。

## 输入契约

2026-08-26 已通过 MCP `2025-06-18` 查询运行中的 `localmind-ai` 3.2.1。
`delegate_to_localmind` 当前只接受：

- 必填文字 `request`，长度 1 到 12,000 字符；
- 可选 `documentIds`，最多包含 20 个已有 LocalMind document ID；
- 必填、由调用方生成的 `idempotencyKey`。

初始 Workflow 把本次消息文字绑定为 `request`，省略 `documentIds`，并让现有
adapter 生成 `idempotencyKey`。本地路径、文件名、附件或 SparkClaw document ID
永远不会被转换成 LocalMind document ID。

两个 delegation Profile 都调用 `delegate_to_localmind`。SparkClaw 对 read Profile
不要求 Approval，对 write Profile 要求 Approval；不增加远端参数来表达该区分。

该 schema 设置 `additionalProperties: false`，服务端也没有声明 MCP Resources
capability。多媒体仍不支持并在远端调用前失败；SparkClaw 不通过 OCR 或 ASR 转换。

`get_localmind_task` 必填 `taskId`，并接受可选的 `knownStateVersion` 和 0 到
30,000 毫秒的 `waitMs`。经过校验的 `localmind.task.v1` 结果至少包含 `taskId`、
`stateVersion`、`status` 和 `terminal`；`query_current_task` 根据这些字段流转。

## 来源与用户界面

当前所有 human/message-runtime 入口都可以在消息显式要求 LocalMind 时使用这些
能力，包括 WebChat、现有人类消息 connector，以及内容中明确要求 LocalMind route
的 Timer 消息。

external-AI principal（包括 inbound third-party MCP）即使文本中明确写出 LocalMind，
也不能使用任何一个叶子。资格根据经过认证的 source/principal context 强制执行，
不依赖 provider 名称或 prompt 文本。

r1 只提供意图驱动的聊天界面，不增加 query/cancel 按钮、task list 或 LocalMind
管理面板。结果包含 task ID 和 state，后续自然语言 query/cancel 可以引用。

## 失败行为

| 条件 | 必须返回的结果 |
|---|---|
| 未显式要求 LocalMind | 不选择 LocalMind 叶子 |
| source principal 是 external AI | 拒绝 route eligibility |
| 当前输入包含不支持的媒体 | 远端调用前失败，不省略也不转换 |
| query/cancel target 缺失 | Workflow 调用前要求澄清 |
| 内部任务查询返回 `terminal=false` | 持久化已校验状态并重复 `query_current_task` |
| 内部任务查询改变 task ID，或注册 endpoint snapshot 不再匹配 | 拒绝该结果，不完成也不交付 |
| terminal status 是 failed 或 cancelled | 返回该实际终态，绝不报告成功 |
| 超过总体等待期限 | 返回包含 task ID 和最新状态的明确 timeout，绝不报告成功 |
| MCP result malformed、unauthorized 或 `isError` | 返回失败 tool outcome，不算成功完成 |

## 验收条件

1. Catalog 的一级 `localmind` 分支包含 read、write、query、cancel 四个叶子，每个
   连接一个 r1 Profile。
2. 所有叶子都要求显式 LocalMind 请求，并拒绝 external-AI principal。
3. read-intent delegation 保持可用且无需 Approval；write delegation 和 cancel
   需要 Approval；query 不需要。
4. delegation 只发送当前文字，省略 `documentIds`，并且只调用一次委派工具。
5. 每个非终态 delegation 都进入内部 `query_current_task` 节点，通过有界的
   `get_localmind_task` long poll 查询到终态。
6. query-node 等待从成功 delegation 起最多 10 分钟，并且持久、可跨重启恢复，
   同时绑定原始 task、Workflow 和 return route。
7. query/cancel 从精确 ID 或同 session 最近任务上下文解析，不新增 task repository，
   也不允许模型发明 ID。
8. 只有 `status=completed` 才作为成功交付；failed、cancelled、malformed、
   unauthorized 和过期等待都不能变成成功结果。
9. 任何 Profile 都不使用模型工具选择、模型可见 directory search、跨叶 fallback 或媒体转换。
10. routing eval 覆盖 read/write 混淆、task-state 意图、external-AI 排除、当前每种
    message ingress 和相邻本地能力。

## 决策历史

| 日期 | 决策 | 状态 |
|---|---|---|
| 2026-08-26 | LocalMind 是与 document 同级、仅显式调用的一级能力 | 已确认 |
| 2026-08-26 | 初始输入只包含本次消息文字，并省略 `documentIds` | 已确认 |
| 2026-08-26 | delegation 拆为 read/write Profile；read 不审批，write 审批 | 已确认 |
| 2026-08-26 | read 通过当前 delegation 工具保持可用；服务端强制只读不阻塞 r1 | 已确认 |
| 2026-08-26 | query 不审批，cancel 审批 | 已确认 |
| 2026-08-26 | 两个 delegation Profile 都在委派后进入内部状态查询节点，总体最多 long-poll 10 分钟，并且只在 completed 终态后反馈成功 | 已确认 |
| 2026-08-26 | “最近/刚才”从同 session 上下文解析，不新增 task repository | 已确认 |
| 2026-08-26 | 不增加 task 按钮或面板，由意图路由选择四个叶子 | 已确认 |
| 2026-08-26 | 除 external-AI principal 外，当前所有 message-runtime 入口都可用 | 已确认 |
