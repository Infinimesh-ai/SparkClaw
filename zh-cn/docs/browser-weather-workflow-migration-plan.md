# 浏览器天气工作流迁移计划

> 语言：[English](../../docs/browser-weather-workflow-migration-plan.md) | 简体中文

状态：已于 2026-07-17 在方案确认后实施。本文档记录已交付契约及验证边界。

## 1. 目标

把天气查询作为正式能力放入 Browser 能力分支，与互联网搜索并列，并通过固定 Workflow 执行。Workflow 先用规范化后的原问题直接查询 Infinimesh Info，再由 deep 模型从 Info 返回内容中提取有证据的天气字段并固化为结构化 payload，然后调用纯渲染工具生成卡片，最后通过现有统一 Delivery Gateway 发送到冻结的返回渠道。删除旧 TaskHint/ReAct 天气链路以及渲染工具内部的 Open-Meteo 查询。

目标能力树如下：

```text
root
└── browser
    ├── browser.internet_search
    ├── browser.weather
    └── browser.automation
```

本次迁移同时保持现有模型分层策略：

- 能力路由阶段使用 `fast` 模型通道。
- 进入 Workflow 后的所有模型步骤统一使用 `deep` 模型通道。
- 上下文组织和 tool result 组装保持原流程不变。

## 2. 迁移前状态

天气目前不是正式 Capability，也没有注册为 Workflow，而是由过渡链路处理：

1. 能力路由器先尝试已注册的 Browser 和 Document Workflow。
2. 一部分天气表达也会命中通用 `browser.internet_search` 识别器。
3. 如果没有 Workflow 命中，TaskHint 启发式逻辑识别天气意图，选择 `weather_lookup`，暴露 `media.render_weather_card`，然后进入旧 ReAct 循环。
4. `media.render_weather_card` 当前同时负责 Open-Meteo 地理编码、天气查询、字段解析和 PNG 渲染。
5. 现有 grounding 和渠道适配器把结果转换为 WebChat 图片或微信图片消息。

迁移前，Info 只作为通用 `web.search` provider 使用，系统没有可治理的天气输入。虽然 `general_research` / `agent_context` 响应包含 `summary`、`key_facts` 和 `sources`，但已实施的 SparkClaw 边界有意只消费 `summary`。旧系统因此既有两条相互竞争的天气入口，也缺少“Info summary → 模型提取 → 类型化天气 payload → 卡片”的固定链路。

## 3. 已实施契约

### 3.1 Capability 与 Workflow 标识

已实施以下规范标识：

| 契约 | 已实施值 |
| --- | --- |
| Capability | `browser.weather` |
| Workflow | `browser.weather` |
| Info 语义工具能力 | `info.question.read` |
| Info 工具 | `info.query` |
| 结构化语义工具能力 | `weather.payload.structure` |
| 结构化工具 | `weather.structure_payload` |
| 渲染语义工具能力 | `weather.card.render` |
| 渲染工具 | `media.render_weather_card` |
| Info 结果资源 | `info_answer` |
| 天气结果资源 | `weather_payload` |
| Info 成功信号 | `info_answer_available` |
| 结构化成功信号 | `weather_payload_available` |
| 渲染成功信号 | `weather_card_available` |

实现使用 `browser.weather`，而不是 `browser.weather_query`，因为其他能力标识描述的是能力本身，不是用户问题的句式。新增叶子能力时已提升 catalog revision。

### 3.2 路由状态

天气路由命中后，应当在 Workflow 执行前持久化一个类型明确、不可变的地点：

```text
Status:       matched
Path:         browser -> browser.weather
Operation:    read
Query:        规范化问题加确定性的天气卡片取数要求
TargetKind:   location
TargetRef:    从用户原文中取得并规范化的地点
Format:       默认 image
Facts:
  location_source: current_turn
```

路由选择前，规范化阶段只追加一次天气卡片取数要求：当前天气状况和温度、可选的当日最低/最高温，以及最多五个可获得的未来小时记录，每条带具体日期时间、天气状况和温度；同时要求 Info 对不可获得的数据明确说明缺失，不能推测或替代。得到的 canonical request 存放在 `route.Slots.Query`，Info 查询唯一业务参数绑定这个精确 query。精确地点作为另一个资源放入 `route.Slots.TargetRef`，不重复拼入 Info 参数，只用于后续结构化阶段的一致性校验。两者都必须在 Workflow 执行前冻结，后续模型不得改写。

地点提取器可以规范化无语义影响的表面差异，例如空格或行政区后缀，但必须保留来自用户问题的可追溯文本片段。它不能推断另一个城市，也不能在当前有依据的上下文中没有地点时自行补地点。

### 3.3 固定 Workflow

天气 Workflow 使用一个节点、三个顺序 scope。使用同一个节点是为了让 Info answer 和结构化 payload 的 outcome ref 始终处于同一个冻结资源边界：

```mermaid
flowchart LR
    Q["冻结的 query + location"] --> I["Stage 1: info.query"]
    I --> A["Info answer + sources"]
    A --> S["Stage 2: deep 提取 + weather.structure_payload"]
    S --> P["受验证的 weather_payload ref"]
    P --> R["Stage 3: media.render_weather_card"]
    R --> W["image-only WorkflowResult"]
    W --> D["Delivery Gateway 按 ReturnRoute 发送"]
```

```text
Node: weather

Stage 1: query_info
  Required capability: info.question.read
  Required tool:       info.query
  Binding:
    query <- route.Slots.Query
  Allowed risk:        read
  Success evidence:    info_answer_available
  Transition:          replace scope with Stage 2

Stage 2: structure_weather
  Required capability: weather.payload.structure
  Required tool:       weather.structure_payload
  Bindings:
    info_answer_ref <- Stage 1 outcome ref (kind=info_answer)
    location        <- route.Slots.TargetRef (kind=location)
  Allowed risk:        draft
  Success evidence:    weather_payload_available
  Transition:          replace scope with Stage 3

Stage 3: render_card
  Required capability: weather.card.render
  Required tool:       media.render_weather_card
  Binding:
    weather_payload_ref <- Stage 2 outcome ref (kind=weather_payload)
  Allowed risk:        draft
  Success evidence:    weather_card_available

Maximum attempts: three total; each stage may execute once
Procedure source: 版本化 Workflow Profile 与持久化 Active Scope
```

每个阶段的 Workflow scope 必须只实体化当前阶段的一个工具：查询阶段只能看到 `info.query`，结构化阶段只能看到 `weather.structure_payload`，渲染阶段只能看到 `media.render_weather_card`。三个工具不能同时暴露，也不能看到 `web.search`、`browser.read`、文档工具或通用兜底工具。

在策略校验和工具执行之前，runtime 从持久化状态实体化 `query`、`location`、`info_answer_ref` 和 `weather_payload_ref`。如果 deep 模型添加日期、扩展地点或引用另一个 outcome，runtime 都应用冻结值覆盖。天气字段本身由 Stage 2 的 deep 模型从 Info 结果提取，但必须通过结构化工具的 schema、范围和证据校验。

Stage 1 完整的已映射结果会持久化，而与 query 相关的有界证据投影通过专用 Info observation 进入下一次 deep 模型调用。选中的 summary、key fact 与 source snippet 使用稳定的 `summary:0`、`fact:N` 和 `source:N:snippet:M` ref。Stage 2 模型只做提取和组织结构；任何证据文本中的指令仍是非可信内容，不能改变 Workflow、工具、地点、query 或返回渠道。

### 3.4 Info 原问题查询契约

新增 `info.query` ToolHub 工具，直接复用 Infinimesh Info 客户端、凭据、token wallet、超时和重试边界。它用冻结的 `route.Slots.Query` 原样提问，继续使用 Info 现有 `general_research` / `agent_context` 响应，不添加“输出 JSON”等后置提示，也不经过通用搜索 Workflow。

Info 工具返回已映射的类型化证据与请求元数据：

```text
request_id
query
summary
provider
key_facts
sources
citations
retrieved_at
took_ms
untrusted
```

Summary、key fact claim 与 source snippet 分别作为稳定 `summary:0`、`fact:N` 和 `source:N:snippet:M` ref 下的非可信证据。成功 outcome 发布 `info_answer` ref，引用当前 tool call 的持久化结果，并保留 Info request ID 和 untrusted 标记。状态型或空 summary 不会遮蔽可用 fact 或 snippet；只有没有任何已映射证据、断连或超时时才明确失败。

### 3.5 Deep 提取与结构化契约

Stage 2 的 deep 模型只读取 tool-result 消息中与 query 相关的有界 Info 证据投影，并调用唯一可见的 `weather.structure_payload`。它只能复制所引用 summary、fact 或 source snippet 中实际存在的引用文本，且每条 evidence ref 都必须使用对应证据项列出的 ref；结构化工具再用该 ref 回到完整持久化 Info 结果校验。校验只允许去除 Markdown 排版标记或折叠空白，字段值与单位必须一致。模型不能翻译、改写、拼接无关数值或凭常识补齐。

结构化输入至少包含：

```text
info_answer_ref
location                    # runtime 绑定
current:
  condition?
  temperature_c?
  feels_like_c?
  humidity_pct?
  wind_kmh?
  precipitation_mm?
hourly[0..5]?
daily[]?
missing_fields[]            # current.condition/current.temperature_c/daily/hourly
evidence[]:
  field_path
  evidence_ref              # 已列出的 summary:0、fact:N 或 source:N:snippet:M ref
  evidence_text             # 必须是所引用 Info 证据项中的原文子串
```

`location` 仍然必填。当前天气状况、当前温度、当日范围和未来小时每类要么包含有依据的数据，要么进入 `missing_fields`，不能同时存在。`hourly` 数组允许零到五个可获得的未来小时，因此 Info 部分返回或完全没有这些天气字段时仍可渲染，缺失小时绝不补造。每个提交的非派生天气字段都必须有证据映射。结构化工具必须验证：地点等于冻结地点；`evidence_ref` 属于当前 `info_answer_ref`；`evidence_text` 是对应证据的精确子串；数值和单位与证据相容；温度、湿度、风速、降水和概率在合理范围；小时/日期不超出用户请求的时间范围。验证失败则不产生 payload ref。

结构化工具可以把单位统一为卡片 schema，但不能平滑、修正或推测数值。穿衣、带伞等建议由已验证字段通过本地确定性规则生成，不要求模型从常识补造。成功后工具持久化版本化 payload，并只发布 `weather_payload` outcome ref。

### 3.6 纯天气卡片渲染契约

保留 `media.render_weather_card` 的绘图、PNG 写入和 ArtifactObject 持久化部分，但删除地理编码、Open-Meteo HTTP 请求和天气字段解析。渲染器只接受 `weather_payload_ref`，通过受治理的前序 tool call/outcome 找回已验证 payload；不接受模型手写的 location、raw JSON 或单个天气字段。

渲染工具声明语义能力 `weather.card.render`、风险 `draft`、效果 `workspace.write`、输出类型 `image`。只有 PNG 与持久化 artifact 均有效时，结果适配器才产生 `weather_card_available` 和图片 path/artifact 引用。

Info 错误、schema 校验失败、地点不一致、payload ref 非法、图片生成失败和 artifact 持久化失败，都必须得到明确的 blocked 或 failed Workflow 结果，不能退回互联网搜索、Open-Meteo 或 TaskHint。

### 3.7 结果发送边界

“发送”不是天气 Workflow 的第三个渠道专用工具步骤。渲染成功后，Workflow 生成携带图片 ArtifactID/ResourceRef 的 channel-neutral `WorkflowResult`，沿请求进入时已经冻结的 `ReturnRoute` 转换为 `DeliveryRequest`，再由现有 Delivery Gateway 发送给 WebChat、微信或其他 provider。

普通天气查询的成功结果声明为 `outputs_only`：只发送一张天气卡片，不重复发送 Markdown 路径、成功提示或同内容文字。失败结果才发送简短文本错误。该结果投影由 Workflow/Profile 的类型化策略声明，不在通用 runtime 中按 Workflow ID 写分支。

## 4. 路由规则

天气识别器和通用互联网搜索识别器必须互斥。Workflow registry 在多个 profile 同时命中时会报告歧义，因此不能只依赖注册顺序。

已实施优先级如下：

| 用户问题 | 路由 | 原因 |
| --- | --- | --- |
| `今天杭州天气` | `browser.weather` | 有明确地点的直接天气查询 |
| `查一下杭州天气` | `browser.weather` | “查一下”不能覆盖天气意图 |
| `杭州未来三小时天气` | `browser.weather` | 天气预报查询 |
| `北京会下雨吗` | `browser.weather` | 天气状态问题 |
| `weather in Hangzhou` | `browser.weather` | 英文直接天气查询 |
| `今天天气` | clarify | 缺少地点 |
| `杭州天气预警官方来源` | `browser.internet_search` | 需要权威来源和当前公告 |
| `杭州天气新闻` | `browser.internet_search` | 是新闻检索，不是天气预报读取 |
| `对比三个网站的杭州天气` | `browser.internet_search` | 明确要求多来源比较 |
| `打开 https://example.com/weather` | `browser.automation` | 明确 URL 导航 |
| `杭州空气质量` | v1 使用 `browser.internet_search` | 当前天气工具不读取空气质量 |

通用搜索 profile 必须明确排除普通直接天气查询。天气 profile 必须明确排除新闻、官方预警来源、多来源比较、URL 导航和当前不支持的环境数据请求。

### 4.1 地点提取

V1 从当前规范化用户消息中进行确定性提取，至少支持：

- `今天杭州天气`
- `杭州今天的天气`
- `查一下上海天气`
- `北京会下雨吗`
- `weather in Hangzhou`
- `Hangzhou forecast`

如果天气意图明确但无法从用户输入中获得有依据的地点，返回缺少地点原因的 clarify 路由。不能静默使用服务器位置、账号位置、IP 位置或模型生成的默认地点。

对于 `那上海呢` 这类上下文追问，在 context snapshot 提供带来源证明的类型化地点契约前保持暂缓；V1 因此 fail-closed。

## 5. 旧天气链路删除范围

新 Workflow 测试通过后，从过渡 TaskHint/ReAct 链删除天气行为：

- 删除 TaskHint prompt 中的天气专用指令。
- 删除 TaskHint normalization 中的天气强制覆盖。
- 删除 TaskHint heuristic 中的天气分支。
- 删除或迁移只为 TaskHint 服务的天气识别辅助函数。
- 从 TaskHint 候选工具别名和兜底暴露中删除 `media.render_weather_card`。
- 用 capability route 和 Workflow 测试替换旧 TaskHint 天气测试。
- 删除已经迁移的 `weather_lookup` Skill 包，避免未匹配的 ReAct 请求启用它。

保留以下组件：

- `media.render_weather_card` 的 Go 绘图和 PNG 产物生成部分。
- Infinimesh Info 客户端、凭据、token wallet、超时和重试边界。
- 天气产物持久化。
- 通用媒体 grounding 和图片链接生成。
- WebChat 媒体服务。
- 微信图片上传和发送。
- 版本化 `browser.weather` Workflow Profile、固定 Stage Scope 与受治理 Argument Binding，作为完整规程来源。Plan 不再保存 Skill ID，Workflow Prompt 也不加载 Skill 文本。

同时删除渲染器中的 Open-Meteo 地理编码、天气查询、解析函数及其专用测试。这个边界保留卡片视觉实现和渠道交付能力，但把天气数据来源统一迁移到 Info。

## 6. 已完成实施阶段

### 阶段 A：Info 证据与天气 payload 契约

- 定义 `info_answer` ref、稳定证据索引和结果大小边界。
- 定义 `weather_payload` schema、最低字段、可选字段、单位和范围。
- 定义每个提取字段到其所列 `summary:0`、`fact:N` 或 `source:N:snippet:M` 证据项引用文本的映射规则，只允许 Markdown 标记与空白规范化。

### 阶段 B：契约测试

- 添加 catalog、route contract、识别器、地点提取和歧义测试。
- 添加三阶段 scope、两个 outcome ref 的传递、参数实体化、结果投影和失败测试。
- 添加断言，证明普通天气问题不再进入 TaskHint。

### 阶段 C：类型化能力和工具元数据

- 增加 capability、Workflow、target kind、语义工具能力、结果适配器和信号常量。
- 在 Browser catalog 下加入 `browser.weather` 并提升 revision。
- 注册 `info.query`、`weather.structure_payload`，并把 `media.render_weather_card` 改成纯渲染工具。

### 阶段 D：天气路由

- 在路由选择完成前，规范化并提取有依据的地点。
- 增加天气 profile，并让通用搜索明确排除直接天气查询。
- 缺少地点时返回 clarify；只有真正冲突的证据才返回 ambiguity。

### 阶段 E：固定 Workflow 和交付投影

- 注册一个节点、三个顺序 scope 的天气 Workflow。
- 实体化 `query`、`location`、`info_answer_ref` 和 `weather_payload_ref`。
- 复用现有上下文和 tool result 组装流程。
- 验证所有 Workflow 模型调用都使用 `deep` 通道。
- 验证 Stage 2 deep 模型只能提取带 Info 证据的字段，不能补齐缺失数据。
- 生成 image-only `WorkflowResult`，并通过现有 ReturnRoute 和 Delivery Gateway 发送。

### 阶段 F：旧链路删除

- 删除 TaskHint 天气选择、别名、启发式和激活关键词。
- 删除渲染器中的 Open-Meteo 查询和解析，保留绘图、grounding 和渠道适配器。

### 阶段 G：文档、评估和验证

- 更新中英文架构和开发文档。
- 更新受影响的 eval fixtures 和 golden prompts。
- 运行聚焦包测试、`go test ./services/gateway/...`、WebChat build、`bash scripts/doctor.sh` 和 `bash scripts/run-eval.sh`。
- 使用 `.github/workflows/ci.yml` 中的内联 Python 执行中英文镜像检查；仓库当前没有独立的 `scripts/i18n_docs_check.sh`。

## 7. 测试矩阵

最低测试覆盖要求：

| 层次 | 必须验证的内容 |
| --- | --- |
| Catalog | Browser 下有三个并列叶子；天气要求 location target |
| 规范化 | 每条用户消息只在路由前规范化一次；天气卡片取数要求只追加一次；route 和 Workflow 看到同一个 canonical request |
| 识别 | 中英文正例；预警/新闻/AQ/URL 反例；与互联网搜索无重叠 |
| Slots | 精确 query 和 location 被冻结；缺少地点时 clarify |
| 模型通道 | 能力路由使用 fast；每个 Workflow 模型步骤使用 deep |
| Info 请求 | 把冻结 query 原样交给 `info.query`；不添加格式提示，不进入通用搜索 Workflow |
| Info 结果 | 持久化完整已映射结果，并只在 Deep 上下文中暴露带稳定 ref 和 untrusted 标记、与 query 相关且有界的 summary、fact、source snippet 与 citation 投影 |
| Scope | 三个阶段分别只暴露 `info.query`、`weather.structure_payload`、`media.render_weather_card` |
| 参数 | 模型添加日期、扩展地点或替换 answer/payload ref 都不能改变绑定资源 |
| 提取 | deep 模型只提交有证据的值；当前天气状况、当前温度、当日范围和未来小时每类要么有值，要么进入明确的 `missing_fields` |
| 证据校验 | 每个天气字段的 evidence ref/text 必须属于当前 Info answer 且值、单位一致 |
| 阶段转换 | `info_answer_available` 进入结构化 scope；`weather_payload_available` 进入渲染 scope |
| 渲染 | 渲染工具不发起网络请求，只消费受验证的前序 payload ref，支持跨午夜过滤过去时间，绝不以其他天气值替代缺失数据，并把明确缺失渲染为“暂无数据” |
| 工具结果 | 有效持久化媒体产生 `weather_card_available`；畸形输出 fail-closed |
| 旧链删除 | TaskHint 不能选择或暴露天气工具 |
| WorkflowResult | 成功结果只含一个受治理的 image part；失败结果为简短文本 |
| 渠道 | 同一个 WorkflowResult 经 ReturnRoute 在 WebChat 展示图片、在微信发送图片 |
| 失败 | Info 超时、断连或 schema 错误不能通过 web search、Open-Meteo 或 ReAct 重试 |

Info 测试使用本地 HTTP fixture 覆盖 token、冻结 canonical query、agent-context answer、超时和断连。提取测试覆盖 current-only answer、零到五个未来小时、第六条拒绝、缺字段、省略可选字段、证据不存在、证据与数值不符、越界数值、错误地点和提示注入文本。可以保留一个凭据门控的 live smoke，但它不能是唯一端到端覆盖。渲染测试必须证明在禁网环境中仍能从 fixture payload 生成卡片。

## 8. 可观测性与回滚

增加或保留以下结构化字段：

- 规范化 query 的脱敏摘要或 digest；不要把原始 Info query 写入公开日志。
- 选中的 capability 和 Workflow ID。
- 冻结的 location 及其来源。
- 路由和执行调用所用的模型通道。
- 当前 stage、实体化的工具名和参数绑定来源。
- Info request ID、证据条目数、延迟和错误类别。
- deep 提取阶段使用的模型通道、payload schema version 和校验失败原因。
- weather payload ref 及其 provenance，不记录完整外部 payload。
- 持久化 artifact ID 和 media path。
- Workflow 终态、结果信号和 Delivery receipt。

实现应拆分为可独立关闭新 capability/profile 的步骤。回滚应通过代码或配置恢复上一版本行为，不能长期同时保留两条路由链路，否则会重新产生本次迁移要消除的歧义。

## 9. 已确认决策

实现遵循以下已确认决策：

1. **规范标识：** Capability 和 Workflow 都使用 `browser.weather`。
2. **缺少地点：** 返回 clarify，永远不推断默认城市。
3. **权威预警、新闻、多来源比较：** 继续走 `browser.internet_search`。
4. **空气质量：** 在天气 provider/tool 有明确 AQ 契约前继续走互联网搜索。
5. **上下文追问：** 在得到带来源证明的类型化上下文地点之前，暂不支持从上一轮解析 `那上海呢`。
6. **Info 查询：** 直接使用现有 `general_research` / `agent_context` 原问题查询，不要求 Info 返回天气专用 schema。
7. **提取约束：** 使用 deep 模型提取天气结构；每个提交字段必须绑定其所列 `summary:0`、`fact:N` 或 `source:N:snippet:M` 证据项中的引用文本，只允许 Markdown 标记与空白规范化；不可获得的类别写入 `missing_fields`，确定性校验通过后才生成 payload ref。
8. **成功输出：** v1 使用 image-only，一张卡片沿原 ReturnRoute 自动发送；明确要求纯文本的天气问题暂不进入该卡片 Workflow，待单独定义 `format=text` 结果投影。
9. **失败策略：** Info 断连或证据校验失败时明确失败，不回退到通用搜索、Open-Meteo 或旧 ReAct。Info 已完成但天气字段不可获得时生成明确的缺失数据卡片。

以上决策、天气字段证据规则及第 5 节的删除边界均在实现前确认。
