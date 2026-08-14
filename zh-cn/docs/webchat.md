# WebChat

> 语言： [English](../../docs/webchat.md) | 简体中文

WebChat 是 SparkClaw 面向 owner 的控制界面。本文档以当前已实现职责和扩展规则替代最初的
前端 handoff requirements。

## 产品边界

React/Vite 应用展示 Gateway 状态并发送 typed owner action。Gateway 仍是执行、路由、Policy、
approval、trace、persistence、delivery、schedule 和 connector binding 的权威。WebChat 不得
重复实现这些决策，也不能直接执行 tool。

当前工作台包括：

- session navigation、chat、stream response、upload 和 assistant attachment；
- 带当前 schedule 和 typed edit/delete 的任务栏；
- 普通消息 composer 上按 session 选择第三方 result destination，覆盖 text、upload、workspace
  file 和 voice draft；
- 把 microphone transcription 插入 draft；
- tool timeline、approval inbox、memory review、trace、model call、audit、episode summary、
  artifact、eval、status、owner/client setting、connector activation/binding 和 policy setting；
- 简体中文和英文 UI。

## 状态与刷新

启动先加载 readiness，再处理 authentication 和 private state。Bearer token 来自
`VITE_SPARKCLAW_API_TOKEN` 或本地 token flow。pairing/token 失败保持可见，语言切换不依赖 Gateway。

状态分为 global data 和 active-session data。条件允许时由 session event 触发刷新，并用有界
polling 兜底。原生 `EventSource` 不能附带 bearer header，因此 authenticated mode 使用当前实现的
兼容路径，不打开 unauthenticated stream。

mutation 后立即刷新相关状态：chat send、schedule change、delivery、approval resolution、
memory action、feedback、owner/client/policy change、connector activation/binding 和 eval run。

## Typed 控制面

结构化 owner action 不会重新转换为歧义文本：

- 任务栏提交包含选中 ID 和观察到的 `updated_at` 的 `schedule_action`；Agent Runtime 校验并执行注册 Workflow。
- delivery target picker 随普通 session message 提交一个可选 opaque `target_endpoint_id`；它不改变或
  复制 composer、attachment、streaming、routing 或 Workflow path；仅附件发送提交空文本，由
  Message Runtime 路由 typed media part。
- 普通 tool approval modification 校验 JSON，并让 verifier-owned field 保持只读；Happy Team
  plan approval 展示 typed task/goal/plan，只提交编辑后的计划文本，不暴露 raw remote tool argument。
- workspace file 通过 authenticated document API 上传和读取。
- speech transcription 只返回 draft text，绝不调用 message send。
- connector setting 从 `/api/connectors` 渲染已注册渠道列表；版本化 toggle 只改变 activation，
  credential/QR binding 保持独立 action，并且只有渠道开启后才可操作。
- External MCP 访问记录把撤销授权与永久删除记录分开。Owner 可以删除任意 ticket 或 binding，
  包括已过期、已使用或已撤销记录，也可以一次删除该 owner 的全部访问记录；删除活跃 binding
  会立即使对应访问失效。

UI 使用 Gateway 提供的具体软件、账号、接收人、会话和 status 展示提醒端和 delivery target。
第三方 destination 不可用时绝不默认切换到 WebChat。

## API 权威

typed client 和 response contract 位于：

```text
apps/webchat/src/api/client.ts
apps/webchat/src/api/types.ts
```

Gateway route 和 public projection 位于 `services/gateway/internal/gateway`。前端消费这些 typed
projection，不读取 Store record，也不重建后端规则。

主要 API 组包括 session/message/event、schedule、delivery endpoint、connector
setting、notification binding、speech、document、approval、memory、trace、artifact、eval、
owner/client setting、config 和 policy。

## UX 与安全规则

- 使用安静的工作控制台，不做 marketing page。
- risk、pending、failed、unavailable 和 unknown-outcome 状态必须可见。
- live plan 不可用时禁用 Happy plan 批准和编辑，保留拒绝操作并展示重试状态；plan 文本不得
  被当成 UI 指令。
- 普通纯媒体消息发往当前选中的第三方 endpoint 时，不显示来源 WebChat assistant result，且该明确
  发送无需审批。纯文本和其他第三方 Workflow result 仍由 Gateway 管理 send approval；显式
  direct-send API client 仍必须确认 send 和 retry。
- API ID、path、tool name、user text 和 model output 原样保留，不翻译或规范化。
- 只本地化静态 UI copy，并持久化选定语言。
- icon-only control 必须有 accessible label、tooltip 和 visible focus。
- 长 path、ID、model name、endpoint label 和 JSON 必须 wrap/truncate，不能造成整页横向 overflow。
- mobile layout 必须让 navigation、conversation、composer、task control 和 inspector 可达且不重叠。

## 开发

```bash
npm install
npm --workspace @sparkclaw/webchat run dev
```

dev server 使用 `18790`，并把 Gateway request 代理到本地 Gateway。API request 放在 shared
client，shared domain type 放在 `api/types.ts`，具体行为放进 feature/component module。
除非现有复杂度确实需要，否则不要增加 state 或 localization dependency。

## 验证

```bash
npm --workspace @sparkclaw/webchat test
npm --workspace @sparkclaw/webchat run build
```

用户可见改动还要运行对应 Gateway contract test，并在运行中的本地 Gateway 上检查 desktop/mobile。
验证 loading、empty、offline、unauthorized、disabled、pending、success、failed、approval 状态，
以及长中英文 label 和 Web/第三方 target 下的 multipart attachment。
