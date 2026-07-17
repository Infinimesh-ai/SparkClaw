# Web 外发消息 Worktree 计划

> 语言：简体中文 | [English](../../docs/web-outbound-messaging-worktree-plan.md)

## 1. 状态与确认门禁

状态：**仅为提案**。

本文定义如何通过三个用户可见的 Codex 任务及其独立 worktree，实现已完成的 [Web 端向第三方连接器发送消息设计](web-outbound-messaging-design.md)。当前阶段只允许编写和评审文档。所有者另行授权了一次 WIP 提交，用于保存主 worktree 中原本已存在的状态和本次规划文档；该保存提交不构成新实现授权。在所有者明确确认本计划前，不得创建新任务、worktree、功能分支、合并或 push。

确认后只创建三个可见任务，不使用隐藏子 Agent，也不增加额外实现 worktree。

## 2. 规划时观察到的仓库状态

初次规划时，主 worktree 位于 `main` 的 `9e6afdb`，包含大量既有未提交改动。所有者随后授权用一个 WIP 提交原样保存这些状态。不得 reset 该状态，也不能直接用它派生三个功能分支；保存提交不表示上一轮并行成果已经完成集成。

上一轮 router-first 架构工作已经留下三个干净的物理 worktree：

| 现有 worktree | 分支 | 当前 head | 含义 |
|---|---|---|---|
| `01-workflows` | `codex/architecture-workflows` | `0a5486d` | 上一轮路由/Workflow 成果 |
| `02-connectors` | `codex/architecture-connectors` | `65cfb5e` | 上一轮消息控制/投递成果 |
| `03-integration` | `codex/architecture-integration` | `8b65098` | 前两者的干净集成结果 |

这些现有分支只作为参考输入，并不构成继续执行的授权。建议代码基点为干净的集成 head `8b65098`，再叠加一个只包含已确认设计和本计划的独立文档提交。三个可见任务开始前，必须记录最终冻结 SHA。

确认后如果检查发现 `8b65098` 已不再包含预期的干净集成，执行必须停止，并先在本文中重新选择基点。不得退而使用脏的主 `main` worktree。

## 3. 可见任务布局

三个任务从同一个冻结基点创建。

| 编号 | 可见任务 | 建议分支 | 职责 |
|---:|---|---|---|
| 1 | 消息接收和发送层 | `codex/web-outbound-message-io` | 统一消息收发、连接器投递、Gateway API、持久化和 WebChat 外发 UI |
| 2 | 路由和 Workflow 层 | `codex/web-outbound-routing-workflow` | Router-first Agent 路径、WorkflowResult 构造、返回路由保持和路由/Workflow 不变量 |
| 3 | 集成与验收 | `codex/web-outbound-integration` | 评审并合并任务 1、2，处理共享装配，执行兼容矩阵并完成双语文档 |

任务 3 与前两个任务同时创建以保持可见，但记录基线后进入 `waiting for inputs`。它不得合并未完成代码、复制未提交文件，也不得自行重写任务 1 或任务 2 缺失的核心功能。

## 4. 任务 1：消息接收和发送层

### 目标

实现 WebChat 到所选第三方端点的完整结构化链路，同时保持既有第三方入站行为。每个统一 Part（`text`、`image`、`audio`、`file`）都必须先预检，再完成投递或明确拒绝；不得从 Markdown 猜测 Part，也不得静默丢失 Part。

### 主要所有权

- `services/gateway/internal/app/message_architecture.go` 中的投递和 endpoint 契约；
- `messageplane/`、`messagecontrol/`、`delivery/`；
- `connector/`、`notification/`、`telegram/`、`weixin/` 中的渠道投递实现；
- Gateway 端点发现、artifact 解析、直接投递 handler、类型化错误、幂等和审计；
- `store/` 的投递持久化，memory/file/PostgreSQL 三个后端、file `Snapshot` 和迁移；
- WebChat API、第三方发送模式、多 Part 上传、目标选择、审核、回执和重试状态；
- 聚焦的渠道、Gateway、Store 和前端测试。

### 排除项

- 不修改语义意图分类、WorkflowProfile、ToolExposure 或 Agent 规划行为；
- 不修改 `cmd/sparkclaw` 的进程级装配，除非任务 3 请求一个窄范围交接提交；
- 不编辑任务 2 所有的 Agent Workflow 文件；
- 不自动扩大已有仅提醒绑定的 scope。

### 必需交接

任务 1 必须报告准确提交列表、相对冻结基点的 diff、API 与契约变更、Store 迁移说明、渠道降级行为、聚焦测试、WebChat build 和干净的 `git status`。交接前必须清除生成状态、上传 fixture、截图、trace 和 artifact。

## 5. 任务 2：路由和 Workflow 层

### 目标

收紧中间链路，使普通 Agent 对话稳定遵循 `MessageEnvelope -> RouteDecision -> WorkflowProfile -> WorkflowResult`；用户在 Web 的直接外发保持为绕过 Agent 路由的显式投递命令。两条路径可以共享 Delivery Gateway 契约，但不能共享或伪造语义 Workflow 状态。

### 主要所有权

- `services/gateway/internal/capability/`；
- `services/gateway/internal/agent/intent_router.go`；
- `services/gateway/internal/agent/workflow_*.go` 及聚焦测试；
- Agent 侧类型化 `WorkflowResult` 的创建与恢复；
- success、clarify、blocked、approval、resume、failure 各状态中的 return route、owner、authorization、causation 和幂等元数据；
- 结构化 Workflow 输出 Part 和 reference，不从 Assistant 展示文本解析投递资源；
- 证明 Web 直接投递 API 不调用 Agent Runtime、不创建伪 Workflow 的回归测试。

### 排除项

- 不实现渠道 API、凭据、轮询、媒体上传、绑定、Gateway HTTP、Store 后端、迁移或 WebChat UI；
- 冻结基点后不修改任务 1 所有的消息和投递契约；需要不兼容改动时向任务 1 提交书面交接；
- 不增加 provider/tool 名称分支、平行 capability 列表，也不允许权威匹配 Profile 回退到旧 TaskHint；
- 除非所有者单独批准扩大范围，本版本不增加用户可见的 Agent 驱动 `message.send` Workflow。

### 必需交接

任务 2 必须报告准确提交列表、相对冻结基点的 diff、路由不变量、行为变化、权威与旧路径边界、聚焦 Agent/capability 测试、可行时的完整 Gateway 测试和干净的 `git status`。

## 6. 任务 3：集成与验收

### 目标

在不接管两个功能核心的前提下形成一个可评审集成分支。任务 3 负责合并纪律、共享装配、兼容测试、最终文档，以及决定后续是否可以合入 `main` 所需的证据。

### 主要所有权

- 执行开始后的本文集成台账；
- `services/gateway/cmd/sparkclaw` 中让 HTTP、连接器、提醒和 WorkflowResult 返回共用一个 Delivery Gateway 的装配；
- 无法干净归属单一功能分支的 `connectorruntime`、配置、策略、公开状态或测试装配窄范围改动；
- 跨层集成测试、默认 file 后端、故障隔离、秘密扫描，以及最终中英文架构与开发文档。

### 限制

- 冲突处理期间不重写渠道投递、WebChat 核心、意图路由或 WorkflowProfile 算法；
- 可定位到任务 1 或任务 2 的缺陷必须退回对应可见任务补提交；
- 共享文件冲突必须保留两侧已测试行为，不能整文件选择一侧；
- 未经后续明确授权，不合并到 `main`、不 push、不移除 worktree、不删除分支。

## 7. 共享契约与文件规则

1. 已确认设计文档是契约唯一事实来源。
2. 任务 1 负责 `MessageContent`、`DeliveryRequest`、capability、端点发现、逐 Part 回执和 provider delivery 接口变更。
3. 任务 2 把这些契约视为冻结，只消费已有字段；不兼容需求必须请求任务 1 处理。
4. 任务 2 负责 Workflow 语义和 Agent 结果构造；任务 1 不得在 Gateway 或连接器中添加语义路由捷径。
5. 任务 3 仅在两个分支提供干净提交后负责装配边界，可以增加 adapter，不得复制领域逻辑。
6. Store 接口新增必须同时落入 memory、file、PostgreSQL 和 `Snapshot`，拒绝不完整的可选 type assertion。
7. Web 直接外发是 owner 显式操作，不调用 Agent Runtime，不创建 `RouteDecision` 或 `WorkflowState`。
8. Agent WorkflowResult 与 Web 直接外发只在类型化 Delivery Gateway 汇合，并保留不同的授权和审计来源。

## 8. 合并顺序与门禁

任务 3 使用固定顺序：

1. 任务 1 消息收发分支；
2. 任务 2 路由/Workflow 分支；
3. 集成专属装配、兼容测试和文档提交。

每次合并前，任务 3 必须：

1. 收到分支所有者的完成报告和准确提交集；
2. 确认来源 worktree 干净；
3. 检查 `git log` 和相对冻结基点的完整 diff；
4. 拒绝无关重构、生成输出、运行状态、凭据、转写、上传媒体和无理由依赖漂移；
5. 独立重跑来源分支的受影响测试；
6. 记录当前集成 head 作为回滚点；
7. 使用显式 non-fast-forward merge commit。

每次合并后立即运行受影响测试。任一门禁失败即停止，不在已知失败上继续叠加后续提交。

## 9. 兼容矩阵

| 场景 | 必须结果 |
|---|---|
| 既有 Web Agent 对话 | 流式对话、工具、审批和会话历史不变。 |
| Web 直接外发 | 直接调用 Delivery Gateway，不创建 Agent run、RouteDecision、WorkflowState 或 model call。 |
| Telegram 端点 | 文本、图片、普通音频、语音消息音频和文件遵循设计中的原生/降级映射。 |
| 微信端点 | 文本和图片使用原生 item；音频/语音和普通文件通过明确提示的文件表现完整保留字节。 |
| 第三方入站回复 | 入站只标准化一次，owner/auth/return route 穿过 Workflow 后返回源端点。 |
| 审批与恢复 | 恢复后的 WorkflowResult 保留原 return route，不向其他 endpoint 泄漏投递权限。 |
| 仅提醒绑定 | owner 授予 `message_send_self` 前不出现在直接发送 endpoint 中。 |
| 已撤销或过期绑定 | 即使 Web 缓存过期状态也无法发送，Gateway 在渠道调用前返回类型化错误。 |
| 默认 file 后端 | Endpoint、scope、delivery、receipt 和幂等状态不依赖 PostgreSQL。 |
| 渠道超时或部分失败 | 回执区分 retryable、outcome unknown 和部分成功，不自动重复发送。 |
| 所有可选连接器停用 | 本地 Web Agent 对话无需连接器凭据或新增必需配置即可启动。 |

## 10. 验证计划

创建分支前记录冻结基点的基线：

```bash
npm run setup:document-tools
cd services/gateway && go build ./... && go vet ./... && go test ./...
cd ../.. && npm --workspace @sparkclaw/webchat run build
```

任务 1 运行 `app`、`messageplane`、`messagecontrol`、`delivery`、`connector`、`notification`、`telegram`、`weixin`、`gateway`、`store` 的聚焦测试，以及 WebChat 单元测试和 production build。

任务 2 运行 `capability` 和 `agent` 聚焦测试，分支稳定后运行 `go test ./services/gateway/...`。

任务 3 在每次合并后及最终收口时运行：

```bash
npm run setup:document-tools
cd services/gateway && go build ./... && go vet ./... && go test ./...
cd ../.. && npm --workspace @sparkclaw/webchat run build
bash scripts/doctor.sh
bash scripts/run-eval.sh
```

最终验证还包括默认 Compose config、文档镜像/链接、秘密与生成 artifact 扫描，以及浏览器工具可用时的桌面/移动 WebChat 截图。

## 11. 执行台账

在所有者确认执行前，本表保持未开始状态。

| 项目 | 值 |
|---|---|
| 所有者确认 | 待确认 |
| 主 worktree WIP 保存 | 已单独授权；不是冻结功能基点 |
| 冻结基点 SHA | 待确认 |
| 任务 1 可见任务/worktree | 未创建 |
| 任务 2 可见任务/worktree | 未创建 |
| 任务 3 可见任务/worktree | 未创建 |
| 任务 1 提交集 | 待提供 |
| 任务 2 提交集 | 待提供 |
| 任务 1 merge commit | 待生成 |
| 任务 2 merge commit | 待生成 |
| 最终验证 | 待执行 |
