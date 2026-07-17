# SparkClaw Web 前端功能需求与后端融合清单

> 语言： [English](../../docs/frontend-requirements.md) | 简体中文

> 面向专职前端团队的交接文档。Gateway 是执行、策略、审批状态、Trace 和持久化的事实来源。当前 TypeScript 契约见 `apps/webchat/src/api/client.ts` 与 `apps/webchat/src/api/types.ts`；后端路由定义见 `services/gateway/internal/gateway/server.go`。

## 1. 产品目标

SparkClaw WebChat 应当是本地优先 Agent Runtime 的 owner 工作台，不是营销页，也不是普通聊天壳。界面必须把 Agent Loop 展示清楚：session、用户消息、模型回复、工具调用、审批门禁、记忆审查、Trace、Artifact 和 Audit。

前端负责：

- 准确、及时地展示 Gateway 返回的状态。
- 收集用户在聊天、审批、记忆和设置中的输入。
- 在调用审批 API 前，把高风险动作清楚展示给 owner。
- 不改写 API 数据，例如 ID、路径、工具名、用户消息、Assistant 消息和模型输出。
- 为长运行本地任务提供稳定、低噪声、可持续扫描的控制界面。

前端不负责：

- 不在浏览器里重写 Gateway 的 policy 判断。
- 不绕过 Gateway API 直接执行工具。
- 不隐藏审批、风险、Trace、模型调用和 Audit 状态。

## 2. 交付优先级

### P0：前后端融合必须完成

- Gateway 启动、健康状态、API token 和 pairing 流程。
- Session 列表、创建、切换和消息历史。
- Chat composer 与消息发送流程。
- 基于 session events 的刷新，以及 polling 兜底。
- 当前 session 的工具时间线。
- 审批收件箱，支持 approve、reject 和 JSON 参数修改。
- Memory candidates 与 accepted memories 审查。
- Trace 列表与 Trace 详情。
- Runtime 状态和明确的错误状态。
- 原生简体中文与英文界面。
- 桌面与移动端响应式布局。

### P1：完整工作台体验

- Assistant 消息反馈：有用、无用、修正。
- Model-call telemetry、Audit events 和 Episode summaries。
- Artifacts catalog。
- Smoke eval 运行器和 eval history。
- Skills registry 视图。
- Owner profile 编辑。
- Paired clients 列表与 revoke 操作。
- Tool policy 查看与编辑。
- Model profiles、adapters、sandbox、storage、state、memory config 的只读展示。

### P2：后续增强

- Tool catalog 和手动工具调用 UI，对接 `/api/tools` 与 `/api/tools/{name}/invoke`。
- Session、Approval、Trace、Memory、Artifact、Eval 的搜索和过滤。
- 后端提供 Artifact 下载/打开 API 后，再做 artifact open/download。
- Approval 参数修改的 JSON diff。
- 持久化面板布局偏好。

## 3. 信息架构

桌面端采用三段式工作台：

- 左侧导航：品牌、语言切换、Gateway 健康状态、auth/token 状态、新建 session、session 列表。
- 中间对话区：当前 session 标题、运行状态 chips、消息流、starter prompts、composer。
- 右侧 Inspector：tab 承载 timeline、approvals、memory、traces、status、settings。

窄屏折叠为单列，顺序为：导航、对话、Inspector。Inspector tabs 必须始终可达，不能出现横向溢出。

## 4. 应用启动与认证

启动流程要求：

1. 从 `localStorage` 读取语言；浏览器 locale 以 `zh` 开头时默认中文，否则默认英文。
2. 调用公开接口 `GET /readyz`；失败时展示 Gateway unavailable。
3. 如果 `readyz.auth_required` 为 true 且没有 token，先展示 token 输入和 pairing 操作，不要直接打私有 `/api/*`。
4. token 来源优先级：`VITE_SPARKCLAW_API_TOKEN` 高于 `localStorage` key `sparkclaw.api_token`。
5. 配置 token 后，私有 `/api/*` 请求带 `Authorization: Bearer <token>`。
6. auth 可用后再加载 global state 和 sessions。
7. 如果没有 session，通过 `POST /api/sessions` 创建默认 session。

认证与 pairing UI 要求：

- token 支持保存、清除、重试。
- pairing 使用 `POST /api/pairing/start` 后接 `POST /api/pairing/claim`；当 pairing 未启用或非本地请求失败时，要清楚展示失败原因。
- `401` 作为认证问题处理，`429` 作为 rate limited 处理，网络错误作为 Gateway unavailable 处理。
- Gateway 不可用时仍然可以切换语言。

## 5. 刷新模型

刷新分为两个作用域：

- Global scope：readiness、config、owner、clients、approvals、memory candidates、accepted memories、skills、eval runs、artifacts、trace metadata。
- Active-session scope：messages、tool calls、model calls、audit events、episode summaries。

事件刷新要求：

- `GET /api/sessions/{id}/events/stream` 提供 server-sent events。
- 原生 `EventSource` 不能设置 `Authorization` header。bearer auth 开启时，使用 polling 或支持带认证 header 的 EventSource 替代方案。
- polling 作为兜底，每隔数秒刷新 active-session scope 和 global scope。
- chat send、approval resolve、approval modify、memory action、feedback save、owner update、client revoke、policy update、eval run 后必须立即刷新相关数据。

当前 store 会产生的重要事件类型：

- `message.created`
- `tool_call.started`、`tool_call.completed`、`tool_call.approval_pending`、`tool_call.running_after_approval`、`tool_call.completed_after_approval`、`tool_call.failed_after_approval`、`tool_call.rejected`
- `approval.pending`、`approval.approved`、`approval.rejected`
- `memory_candidate.created`、`memory_candidate.accepted`、`memory_candidate.rejected`
- `episode_summary.saved`

## 6. 功能模块

### 6.1 导航与 Session 列表

功能要求：

- 展示 Gateway ready/offline 和 model mode。
- 展示 session 数、pending approvals 数、pending memory candidates 数。
- 支持新建 session。
- 支持切换 active session，切换时不丢失 global state。
- session item 展示 title、短 ID、更新时间。
- 长标题、长 ID 必须可截断或换行，不能撑破布局。

后端接口：

- `GET /readyz`
- `GET /api/sessions`
- `POST /api/sessions`

### 6.2 Chat

功能要求：

- 按时间顺序展示 user 和 assistant messages。
- 展示 role、timestamp；assistant message 有 `run_id` 时提供打开 Trace 的入口。
- 当前 session 下发送非空消息。
- 消息处理中展示 busy state。
- 发送后刷新 messages、tools、approvals、traces、status。
- 提供 starter prompts：文件搜索、browser.read、记忆提议、沙箱命令。
- assistant message 有 `run_id` 时支持反馈：有用、无用、修正答案。

后端接口：

- `GET /api/sessions/{id}/messages`
- `POST /api/sessions/{id}/messages`
- `POST /api/runs/{id}/feedback`

### 6.3 工具时间线

功能要求：

- 展示当前 session 的所有 tool calls。
- 每个工具项展示 icon、tool name、risk、status、observation summary、error、approval ID、Trace 入口。
- 可展开查看 arguments 和 result，使用格式化 JSON。
- 风险等级保持一致视觉语义：`read`、`draft`、`reversible`、`dangerous`。
- `approval_pending` 和 `dangerous` 必须醒目，不能弱化。

后端接口：

- `GET /api/sessions/{id}/tool-calls`
- `GET /api/tool-calls/{id}`
- `GET /api/traces/{run_id}`

### 6.4 审批收件箱

功能要求：

- pending approvals 排在前面，resolved approvals 排在后面。
- 展示 tool、risk、status、summary、reason、resources、session ID、run ID、created/resolved time。
- pending approval 支持 approve 和 reject。
- approve 前支持编辑 JSON arguments。
- 以 `_` 开头的参数，例如 `_verifier`，视为系统/验证器元数据：只读展示或从可编辑区拆出。
- 前端先做 JSON 校验，再调用 modify。
- modify 后展示 Gateway 返回的新 resources/arguments。
- approve/reject 后刷新 approvals、当前 session timeline、traces、audit。

后端接口：

- `GET /api/approvals`
- `GET /api/approvals?status=pending`
- `POST /api/approvals/{id}/approve`
- `POST /api/approvals/{id}/reject`
- `POST /api/approvals/{id}/modify`

### 6.5 记忆审查

功能要求：

- Memory candidates 和 accepted memories 分区展示。
- candidate card 展示 kind、sensitivity、status、reason、content、session ID、run ID。
- pending candidate 支持 accept/reject。
- accepted memory 支持 edit/delete。
- memory edit 提交前校验 kind 和 content 非空。
- Gateway 返回 sensitive-memory 错误时，前端展示原因，不做无意义遮蔽。
- 支持 archive/export memory snapshot，并展示返回的 artifact reference。

后端接口：

- `GET /api/memory-candidates`
- `GET /api/memory-candidates?status=pending`
- `POST /api/memory-candidates/{id}/accept`
- `POST /api/memory-candidates/{id}/reject`
- `GET /api/memories`
- `GET /api/memories?query=...`
- `POST /api/memories/{id}/update`
- `POST /api/memories/{id}/delete`
- `GET /api/memories/export`
- `POST /api/memories/export`

### 6.6 Trace 与运行诊断

功能要求：

- 展示最近 trace metadata，并可按 `run_id` 打开详情。
- Trace summary 展示 run state、risk、model lane、model、model-call count、token count、平均 latency、tool count、approval count、feedback count、audit count、artifact reference。
- Trace detail 展示 model note、model calls、messages、tool calls、approvals、feedback、audit、episode summary。
- Assistant message、tool call、approval 都应能跳转到对应 run trace。
- Gateway 已经做过 redaction；前端按返回值展示，不要擅自恢复或改写。

后端接口：

- `GET /api/traces`
- `GET /api/traces?limit=...`
- `GET /api/traces/{run_id}`
- `GET /api/sessions/{id}/model-calls`
- `GET /api/sessions/{id}/audit`
- `GET /api/sessions/{id}/episodes`

### 6.7 Runtime 状态

功能要求：

- 展示 Gateway binding、model mode、workspace root、trace dir、state backend/path/DSN status、rate limit。
- 展示当前 session 的 recent model-call telemetry。
- 展示当前 session 的 recent audit events。
- 展示 artifact catalog：kind、backend、URI/path、content type、size、run/eval/session reference、created time。
- 展示 episode summaries：goal、outcome、risk、model lane、tools、approvals、failures、repair flag。
- 展示 registered skills：name、description、risk、allowed/denied tools、dependencies、eval cases、path。

后端接口：

- `GET /readyz`
- `GET /api/config`
- `GET /api/artifacts`
- `GET /api/skills`
- `GET /api/sessions/{id}/model-calls`
- `GET /api/sessions/{id}/audit`
- `GET /api/sessions/{id}/episodes`
- 可选原始诊断入口：`GET /metrics`

### 6.8 Eval 面板

功能要求：

- 从 UI 运行 smoke profile。
- 展示当前 eval run 的 status、summary、cases、duration、failure archives。
- 展示 eval history，支持选择历史 run。
- 请求进行中禁用 run button。
- eval 完成后刷新 artifacts。

后端接口：

- `POST /api/evals/run`，body 为 `{ "profile": "smoke" }`
- `GET /api/evals`
- `GET /api/evals/{id}`

### 6.9 Settings

功能要求：

- Owner profile：查看和编辑 display name、email、preferences。
- preferences 可以用 key-value rows 或经过校验的结构化编辑器。
- Paired clients：展示 active/revoked clients、created time、last seen、revoke action。
- Tool policy：展示 policy path、risk counts、definition approval tools、configured approval-required tools、denied tools、browser allow hosts。
- Tool policy edit 提交前要阻止同一个 tool 同时出现在 denied 和 approval-required；同时保留 Gateway validation errors 的展示。
- Model profiles：展示 fast、deep、embedding、reranker、guard 的 profile name、model、base URL、context tokens、max tokens、MTP。
- Runtime boundaries：展示 gateway bind/port/remote access、workspace allowlist、sandbox config、storage、state、adapters、memory config、skill dirs。

后端接口：

- `GET /api/owner`
- `POST /api/owner`
- `GET /api/clients`
- `POST /api/clients/{id}/revoke`
- `GET /api/config`
- `POST /api/tool-policy`

### 6.10 原生双语

功能要求：

- 支持简体中文 (`zh`) 与英文 (`en`) 本地词典。
- 语言持久化到 `localStorage` key `sparkclaw.language`。
- 浏览器 locale 以 `zh` 开头时默认中文，否则默认英文。
- 本地化静态界面文案、tab label、placeholder、starter prompt、empty state、action label、validation message、error fallback、status summary。
- 不翻译 API 数据、ID、工具名、路径、用户消息和 Assistant 消息。

## 7. API 对照表

| 领域 | 接口 |
|---|---|
| Health/auth | `GET /readyz`、`POST /api/pairing/start`、`POST /api/pairing/claim` |
| Config/settings | `GET /api/config`、`GET /api/owner`、`POST /api/owner`、`GET /api/clients`、`POST /api/clients/{id}/revoke`、`POST /api/tool-policy` |
| Sessions/chat | `GET /api/sessions`、`POST /api/sessions`、`GET /api/sessions/{id}`、`GET /api/sessions/{id}/messages`、`POST /api/sessions/{id}/messages` |
| Events | `GET /api/sessions/{id}/events`、`GET /api/sessions/{id}/events/stream` |
| Runs/telemetry | `POST /api/runs/{id}/feedback`、`GET /api/sessions/{id}/tool-calls`、`GET /api/sessions/{id}/model-calls`、`GET /api/sessions/{id}/audit`、`GET /api/sessions/{id}/episodes` |
| Approvals | `GET /api/approvals`、`POST /api/approvals/{id}/approve`、`POST /api/approvals/{id}/reject`、`POST /api/approvals/{id}/modify` |
| Memory | `GET /api/memories`、`GET /api/memories/export`、`POST /api/memories/export`、`POST /api/memories/{id}/update`、`POST /api/memories/{id}/delete`、`GET /api/memory-candidates`、`POST /api/memory-candidates/{id}/accept`、`POST /api/memory-candidates/{id}/reject` |
| Trace/artifacts | `GET /api/traces`、`GET /api/traces/{run_id}`、`GET /api/artifacts` |
| Evals/skills/tools | `GET /api/evals`、`POST /api/evals/run`、`GET /api/evals/{id}`、`GET /api/skills`、`GET /api/tools`、`POST /api/tools/{name}/invoke` |

## 8. UX 与视觉要求

- 整体气质是安静的 operator console，不是装饰型 landing page。
- 字体密度高但可读；不要用 viewport width 直接缩放字号。
- 使用中性表面，辅以克制的绿色、琥珀色、红色、蓝色和石墨色。
- Card 只用于重复对象：messages、tool calls、approvals、traces、memories、clients、eval cases、artifacts。
- 控件和卡片圆角控制在 8px 以内，除非未来设计系统另有规定。
- 紧凑操作优先用 icon button，并提供 tooltip/accessible label。
- 长路径、ID、模型名、JSON payload 必须能合理换行或截断。
- 每个 panel 都要有 empty、loading、success、failed、disabled、offline 状态。
- Dangerous approval 和 failed-after-approval tool call 必须醒目。

## 9. 可访问性与响应式

- 键盘用户必须能切换 session、发送消息、切换 tabs、approve/reject、编辑 JSON、保存 settings。
- 纯 icon 控件需要 accessible label 和清晰 focus state。
- 颜色不能是唯一的风险/状态表达。
- 移动端不能出现整页横向滚动。
- JSON/code blocks、长 URI、模型名不能溢出容器。
- Composer 和 approval editor 在窄屏下仍需可用。

## 10. 建议前端结构

当前 `App.tsx` 过大，不利于专职前端长期维护。建议按职责拆分：

```text
apps/webchat/src/
  api/
    client.ts
    types.ts
  i18n/
    dictionaries.ts
  app/
    AppShell.tsx
    useBootstrap.ts
    useRefresh.ts
  features/
    sessions/
    chat/
    timeline/
    approvals/
    memory/
    traces/
    status/
    evals/
    settings/
  components/
    Button.tsx
    EmptyState.tsx
    JsonBlock.tsx
    RiskPill.tsx
    StatusChip.tsx
    Tabs.tsx
```

状态管理保持轻量，除非复杂度真实上升。现有依赖为 React、Vite、TypeScript、`lucide-react`；不要为了 i18n 或状态管理引入重型依赖，除非团队明确接受成本。

## 11. 验收清单

- `npm --workspace @sparkclaw/webchat run build` 通过。
- mock Gateway 下 UI 可以启动、创建 session、发送消息并展示 assistant response。
- 使用工具的消息能更新 timeline，并能打开对应 run trace。
- dangerous/reversible action 能进入 approvals，支持 modify、approve、reject，timeline 能反映结果。
- memory candidate 可以 accept/reject；accepted memory 可以 edit/delete；memory export 能创建 artifact reference。
- Trace view 在有数据时展示 model calls、tools、approvals、feedback、audit、episode。
- Smoke eval 可以启动、查看详情，并从 history 选择。
- Owner profile、client revoke、tool policy edit 能对接 Gateway API。
- auth-required 模式支持 token 保存、清除/重试和 pairing 失败状态。
- 中文和英文可在 Gateway 不可用时切换。
- 桌面和移动端没有文字重叠、按钮裁切或横向溢出。
