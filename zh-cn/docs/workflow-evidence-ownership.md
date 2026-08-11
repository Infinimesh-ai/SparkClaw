# Workflow 证据所有权与复用设计

> 语言： [English](../../docs/workflow-evidence-ownership.md) | 简体中文

状态：2026-08-07 迁移中。第一批文档/浏览器投影与 Runtime binding 已实现，且没有新增
持久化 schema。下文更广泛的逐 Profile source-event、typed completion、测量与清理仍是
迁移契约，不表示所有 Runtime 行为已经完成。

范围：`services/gateway/internal/agent` 中的 Workflow Profile、直接工具调用、
模型决策、outcome 适配、runtime 证据供给、最终化和审计引用。本提案细化
[Workflow 执行](workflow-execution.md)中的所有权边界，并建立在
[观测压缩重构](observation-compression-redesign.md)已经实现的归档和供给路径上。

## 1. 决策摘要

证据是由 runtime 管理的数据生命周期，不是默认的一类 Workflow 节点，也不是
由每个消费者重新创建的 payload。

目标契约如下：

1. 每次 source acquisition 都创建一个拥有独立 provenance 的不可变 observation
   event。Runtime 只为该事件归档一次完整的不可信 payload。
2. 只要能够通过类型化解析、registry 查询、状态机状态、版本比较、hash 或精确
   结构规则确认，Runtime 就负责提取、校验和绑定该事实，并从模型输入和输出契约中
   剥离这些无需再次判断的字段。
3. Workflow 保留确有必要的模型目标判断、语义工具选择和内容生成。每次模型调用只
   接收解决当前语义变量所需的最小 source projection、候选 ID 或 eligible operation，
   不接收与该判断无关的 Runtime 事实。
4. Runtime 把模型选择的候选或 operation 解析回 source event，重新校验，并绑定
   identity、locator、scope、hash、freshness 和其他可证明的执行参数。模型仍可提供
   operation-specific semantic argument 和新内容，但模型 prose 不是 locator。
5. Workflow 状态、模型上下文、审计、审批和最终化引用同一个 observation event
   或明确的 derived assertion。它们使用的摘要和切片只是 projection，不是新的
   证据记录。
6. 节点依据类型化 predicate 完成。通用 `CompletionEvidence` 不能继续同时表示
   工具成功、语义判断、effect 验证和最终答案就绪。
7. 复用用于移除同一事件的重复表示和不必要重读，绝不因为 payload 或 claim 相同
   就合并两个独立 acquisition event。可选的物理 blob 去重只是存储优化，不是逻辑
   证据身份。

规范性总原则是：**Runtime 剥离并绑定可证明事实；Workflow 保留必要的模型目标判断、
工具选择和内容生成，但只向每次模型调用提供解决当前语义变量所需的最小 projection。**
这一边界可以减少冗余模型判断和 prompt 体积，同时让过期或含糊的资源绑定在 Runtime
内 fail closed。

## 2. 当前词汇为何过载

当前 runtime 用“证据”描述了多个不同事物：

| 当前界面 | 实际职责 |
|---|---|
| 使用 `CompletionEvidence` 的节点 | 宽泛的完成规则，常常只表示某个工具 outcome 被接受 |
| `ToolOutcome.Refs` / `WorkflowNodeState.OutcomeRefs` | 供状态转换和后续绑定使用的持久化引用 |
| 已归档的工具 observation | 完整的不可信源 payload |
| `ObservationSummary` | 指向 payload 的小型跨步骤或跨 run 索引 |
| `EvidenceRequirements` / `PROVISIONED_EVIDENCE` | 面向某个模型消费者的持久化源内容切片 |
| Profile assessment | 判断 observed outcome 是否满足节点 predicate 的 verdict |
| 最终化证据 | 用于生成用户可见结果的输入 |
| 审计字段 | 记录哪些输入和决策影响了执行 |

这些层都必要，但把每一层都当成独立生成的“证据”会产生三个问题：

- Workflow 出现只负责复制、改写格式或复述先前结果的节点；
- 确定性的位置和完整性事实明明已经由 Runtime 掌握，却仍经过模型序列化；
- 同一 payload 或 claim 在 outcome ref、observation summary、provisioned prompt、
  decision prompt、最终化和审计中重复出现。

下述设计将 acquisition event、payload 存储、确定性事实、语义 verdict 和消费者
projection 分开。

## 3. 规范词汇

| 术语 | 含义 | 所有者 |
|---|---|---|
| **Observation event** | 一次 acquisition attempt，以及工具、adapter、owner handoff 或外部 provider 返回的原始输出。每个事件都有独立 provenance，始终是不可信数据。 | 源 adapter 与 Runtime 归档 |
| **Payload blob** | Observation event 引用的不可变归档字节。相同字节可以共用物理存储，但不能共用 provenance。 | Artifact store |
| **Locator** | 再次定位或绑定 subject 所需的类型化身份，例如 document ID 与 source hash、cell address、browser generation 与 control ref、schedule ID 与 version。 | Runtime |
| **Fact** | 由确定性解析或状态证明的值，例如“hash 匹配”“entry 属于该 directory revision”或“工具调用完成”。 | Runtime |
| **Derived assertion** | 增加了可执行 claim 的确定性 fact 或 semantic verdict，并记录 rule/predicate version 与输入 event ID。 | Runtime 记录；确定性 validator 或模型产生 |
| **Candidate** | 经过 Runtime 校验、可用 ID 定位、允许语义决策选择的候选项。 | Runtime 生成；模型可以选择 |
| **Semantic variable** | 某一次模型调用必须解决、且 Runtime 不能由当前类型化事实确定的值，例如 target candidate、eligible operation、goal verdict 或生成内容。 | Workflow/Profile 声明；模型求解；Runtime 校验边界 |
| **Semantic verdict** | 不能通过结构规则确定的有界判断，例如当前渲染内容是否满足 owner 的自然语言目标。 | 模型，在 Runtime 约束内 |
| **Projection** | 面向单个消费者的 observation event 或 assertion 有界视图。Projection 可以省略内容，但不能改变身份或 provenance。 | Runtime |
| **Completion predicate** | 节点或 Workflow 成功前必须具备的精确 fact、effect verification、semantic verdict 或 output。 | Profile 声明；Runtime 执行 |

Observation event 并不自动等于“真相”。它说明观测到了什么、来自哪里、对应哪个
版本，以及后续 assertion 由哪一种确定性或语义过程派生。即使两个事件报告相同
payload 或 subject version，后发生的事件仍然是独立事件。

## 4. 所有权边界

### 4.1 只属于 Runtime 的决策

只要 Runtime 拥有足够的类型化输入，下列数据就绝不能委托给模型：

- resource identity、规范化 path/URL、provider endpoint、owner/session/run、
  Workflow node、scope revision、tool call 和 directory revision；
- document format、source hash、paragraph/block/cell/row/slide/shape/page
  locator、parent lineage 和 output-copy path；
- browser profile、tab、hidden/visible mode、session/page generation、snapshot
  digest、control ref membership、settled state、route consistency 和 before/after
  transition identity；
- schedule/task/message/remote-object ID、record version、compare-and-swap match、
  approval state、delivery receipt 和 idempotency key；
- 工具注册和 qualifier、schema validation、capability scope、Policy result、
  allowed risk/effect 和精确 argument binding；
- success/error/timeout status、byte 或 item count、source coverage、truncation、
  supported-feature gate 和 integrity/preservation check；
- 依据 Profile 明确规则，从 version、generation、timestamp 或 hash 得出的
  freshness 与 staleness；
- event identity、lineage、访问控制、prompt projection、artifact retrieval，以及
  可选的物理 blob 去重。

这些都是可执行事实。让模型再次复述只会增加延迟和新的失败模式，不会增加信息。
Runtime 必须在构建模型 schema 和 projection 时移除这些字段，并在模型返回后从权威
state、source event 或 derived assertion 绑定它们；不能只靠 prompt 要求模型正确抄写。

### 4.2 允许交给模型的决策

只有未解决的 predicate 属于语义问题，而且确定性规则会明显不完整时，模型调用才
合理，例如：

- 在经过 Runtime 校验的 candidate 中，根据 owner request 的含义选择目标；
- 在两个或更多语义不同的 eligible operation/tool 中选择；
- 即使结构候选数量很少，owner request 的意义仍不明确，需要结合上下文判断目标；
- Runtime 已经校验 route、generation 和 transition 后，判断当前 page content 在
  语义上是否满足请求目标；
- 对非结构化内容做摘要、比较、翻译、语义提取、改写或生成；
- 在没有受支持的确定性 extractor 时解释视觉内容。

模型输出契约必须与当前 semantic variable 对齐：target/goal 判断返回 supplied bounded
set 内的 verdict 或 candidate ID；工具选择返回 eligible entry ID；内容生成只返回所选
operation 需要的 semantic argument 和新内容。Runtime 拒绝未知、过期、越界或结构
无效的选择，并忽略或拒绝模型重新提供的 Runtime-owned 字段。因此，“模型只返回 ID”
只适用于选择类调用，不是对所有 Workflow 模型调用的限制。

### 4.3 混合决策

混合工作对**每一个 semantic variable**遵循固定顺序，而不是据此跳过整个 Workflow：

1. Runtime 从 source event 生成候选，移除 identity、scope、schema、freshness、safety
   或结构规则校验失败的 entry。
2. 如果精确规则和 owner request 已唯一确定当前变量，Runtime 记录或绑定结果，并在
   现有 Workflow 中推进；只跳过这一个变量的模型判断。
3. 如果意义不明确或目标不唯一，模型接收 opaque candidate/operation ID 和区分它们
   所需的最少上下文；候选数量本身不是唯一的调用条件。
4. 当前变量解决后，Workflow 继续执行后续节点。下游若仍有目标判断、多个工具选择或
   内容生成变量，仍然调用模型。
5. Runtime 解析模型输出、重新校验当前版本，只回绑可证明的 locator、scope、hash、
   freshness 和 execution argument；模型生成的语义参数保持可审计并受 schema/Policy
   约束。

对一个必须选择既有对象的变量，如果没有 eligible candidate，Runtime 直接 block 或
clarify；如果“目标不存在”是插入或创建操作的合法前提，则由 Profile 明确定义该事实
如何推进。模型绝不发明 Runtime 没有提供的 path、hash、browser ref、remote ID 或
mutation scope。确定性 acquisition 也绝不能越过后续 decision/edit 节点或创建第二个
executor。

## 5. Source observation 生命周期

```text
tool / adapter / owner handoff
  -> 为本次 acquisition 创建一个不可变 observation-event identity
  -> 只为该事件归档一次完整的不可信 payload
       -> 提取确定性 fact + locator
       -> 为当前 semantic variable 生成最小 projection
            -> 模型返回 candidate/verdict、eligible tool 或生成内容
            -> Runtime 校验语义输出并绑定可证明的执行事实
       -> Workflow completion predicate
       -> Policy / approval / 精确 argument binding
       -> final result 与 audit 引用同一个 event 或 assertion
```

Source-observation event 至少必须保留：

- event ID 和 kind；
- source identity：session、run、Workflow、node、scope revision，以及 tool call 或
  等价的 owner/runtime event；
- subject locator 与 source version/generation；
- 解释该事件所需的 acquisition mode、request binding、coverage 与 adapter/contract
  version；
- 存在 payload 时的 artifact reference 和原始 payload digest；
- provenance、trust classification、creation time 和明确的 freshness rule；
- fact 或 semantic verdict 依赖早期记录时的 derivation link；
- partial observation 的 coverage 与 omission metadata。

每次独立执行的 source acquisition 都创建不同事件，即使它与早期调用返回相同字节
或 claim。对同一个已持久化 tool call result 的 crash-safe replay 可以复用该调用的
event；新的 retry attempt 不可以。这样才能保留 observation time、retry history、
credential/scope context 和 provenance。

这是逻辑契约，不代表必须增加 evidence graph、Go struct 或 store table。实现首先应
复用 `ResourceRef`、`ToolOutcome`、artifact metadata 和已有的类型化 Workflow
state。新增持久化结构必须先通过第 10 节的阶段 0 决策门槛。

### 消费者 projection

每个消费者接收同一个 source event 或 derived assertion 的 projection 或类型化引用：

- Workflow transition code 接收类型化 fact 和 event/assertion ID；
- argument binding 从权威 state/event 接收 locator、scope、hash、version 和 generation；
  operation-specific semantic argument 与生成内容可以来自受约束的模型输出；
- 模型只接收当前 semantic variable 所需的有界 content slice、candidate/evidence ID、
  eligible operation 描述或生成上下文；不同模型调用不接收彼此无关字段的并集；
- approval 接收它所授权的精确 source ID、subject version/generation 和 bound argument
  digest；
- audit 接收 ID、decision code、digest、count 和 lineage，而不是另一份 payload；
- finalization 接收回答所需的最小 source projection，并保留对 source ID 的 citation。

因此 `ObservationSummary` 和 `PROVISIONED_EVIDENCE` 仍然有用，但它们只是传输视图，
不会产生新的 claim、locator 或 evidence identity。

### 消费时 freshness

Acquisition 和归档是历史事实，freshness 不是。Runtime 在创建 approval 时检查 bound
locator、version/generation、scope 和 argument digest，并在 approval 或 resume 后
真正执行 effect 之前再次检查。如果任何权威值发生变化，旧 approval 不能授权变化
后的 operation；Runtime 必须 block 或重新采集 source，并在需要时重新请求 approval。

等待后不需要把已经完成的 acquisition node 改回未完成状态。需要 fresh binding 的
消费者负责在使用前重新校验。

## 6. 复用与重复规则

### 6.1 逻辑身份

一个 observation event 表示一次 acquisition attempt：

```text
(event ID, source call 或 handoff, owner/session/run, Workflow node 与 scope)
```

独立事件绝不能因为 payload digest、prose、locator 或 source version 相同就合并。
针对同一个事件的不同 byte budget、excerpt、prompt section、audit event、approval
view 或 finalization format 都是 projection，并复用该事件的引用。

只有增加了新的可执行 claim 时，derived fact 才拥有自己的 identity，并记录 input
event/assertion ID 和带版本的 derivation rule。仅仅改写格式、截断、摘要或复制已有
claim 不会产生另一个 assertion。Semantic verdict 是 decision event；两个独立决策
返回相同模型 prose 也不能作为合并理由。

实现可以依据归档原始字节的 cryptographic digest 对 payload blob 做物理去重。该优化
只改变存储：每个 observation event 仍保留独立 provenance、access scope 和 timestamp。

### 6.2 Payload 规则

- 一个 observation event 只归档一份完整工具输出。不能因为另一个 stage、model
  call、finalizer、approval view 或 audit consumer 需要消费，就再次归档或持久化另一
  份大 payload。
- `OutcomeRefs` 只持久化引用，不在 ref attribute 中嵌入第二份大 payload。
- Observation summary 和 model projection 从 source event 按明确预算生成，且必须
  保留 source ID、coverage 和 omission marker。
- Audit event 引用 evidence 与 verdict ID。敏感或大体积 source content 不复制到
  audit field。
- 后续消费者需要更多内容时，从同一 artifact 读取另一个 slice。除非 source 可能
  已改变且 Workflow 明确要求 fresh observation，否则不重复调用 source tool。
- 如果测量表明 source 未变化、只是早期 projection 太小而导致重复调用，应修复
  projection 或使用 `observation.read`，不能通过合并事件来掩盖重复调用。

### 6.3 必须保持独立的记录

复用不能合并：

- 任何两个独立执行的 acquisition event，即使来自同一 provider 且 payload 相同；
- 不同 source version、hash、page generation 或 directory revision 的 observation；
- mutation 前后的 observation；
- hidden 与 visible browser observation；
- 不同 provider、credential、authority 或 owner scope 下的 observation；
- payload 文本相同但 subject 不同的记录；
- complete result 来自新采集时的 partial 与 complete coverage；
- raw observation 与基于它产生的 model semantic verdict。

冲突记录必须带 provenance 并列保留。Runtime 应用 Profile 的 freshness 与 authority
规则，而不是通过合并相似 prose 消除分歧。

## 7. 各领域 Locator 契约

| 领域 | Runtime 所有的 locator 与 proof | 模型可见的语义输入 |
|---|---|---|
| 文档 | Document ID、冻结的受治理 path、format、source hash、稳定 block/paragraph/cell/row/slide/shape/page locator、package feature gate、parent lineage | 当前调用所需的 candidate 周围文本/结构、eligible editor 描述或内容生成上下文；绝不接收自由生成的替代 locator |
| 浏览器 | Profile/tab、规范 URL、hidden/visible mode、session/page generation、settled snapshot digest、control ref membership、transition before/after digest | 当前 goal/control/tool 判断所需的有界 rendered text、control label/state 和 opaque candidate ref；不接收 generation/digest 转录任务 |
| 定时任务与消息 | Owner-scoped record ID、version、endpoint、return route、idempotency key、CAS result、delivery receipt | 只有 owner 表述导致 target 歧义时才提供 candidate label 或 content |
| 外部 MCP 与 coding agent | 配置的 endpoint identity、credential scope snapshot、catalog revision、namespaced entry ID、remote object ID/version、mutation class | 有界 eligible operation/object 描述和不可信 returned content |
| 搜索与天气 | Provider、request/result ID、query binding、source URL、observation time、response/card status | 比较、综合或解释所需的 result snippet 或 payload fact |
| Artifact 与 multipart message | Session ownership、artifact URI/key、media kind、digest、ordered part index、governed source message | 解释所需的 content；ordering 与 ownership 绝不依赖模型输出 |

新增领域时，必须先定义 locator 和 staleness rule，模型才能选择或作用于其中的资源。

## 8. Workflow 节点与完成设计

### 8.1 合法的节点目的

节点可以用于：

- 采集新的外部 observation；
- 执行并验证 effect；
- 在 Runtime 生成的候选上解决真正的目标判断或工具选择；
- 生成、改写或转换所选 operation 所需的内容；
- 等待 owner 控制的 handoff 或 approval state；
- 生成最终 model answer 或 governed message result。

如果节点作为持久化状态机边界确实有价值，纯 Runtime preparation 可以在没有模型和
工具的情况下完成，例如冻结一个 document identity。但它记录的是类型化 fact 和
reference，而不是“证据” prose payload。

### 8.2 不合法的节点目的

不要增加仅用于以下目的的节点：

- 把先前 observation 改名为 evidence；
- 把 artifact 复制进 prompt text；
- 让模型复述或校验 Runtime 所有的 ID、path、hash、version、generation、schema
  check 或 Policy result；
- 把 tool success status 转换成另一个 success status；
- 为 finalization 或 audit 创建第二份 evidence record；
- 仅仅因为先前 projection 太小，就重新读取未变化的 source；
- 因为上游 acquisition 是确定性的，就越过仍有 semantic variable 的下游 decision、
  tool-selection、content-generation 或 effect 节点。

如果先前 projection 太小，应扩展该 projection 或使用 `observation.read`，而不是重新
获取未变化的 source。

### 8.3 类型化完成 predicate

Profile 最终应把通用 evidence completion 替换为能说明真正完成条件的 predicate，
概念上包括：

- `observation_available(kind, coverage)`；
- `fact_bound(kind, evidence_id)`；
- `semantic_verdict(kind, evidence_ids)`；
- `effect_verified(kind, before_id, after_id)`；
- `output_ready(kind)`。

精确 schema 和 enum name 留到实现设计决定。不变要求是：每个节点都必须说明所需
record、validator、coverage/freshness rule 和 consumer。除非声明的 predicate 明确
认为工具成功已经足够，否则成功 tool call 本身不代表完成。

“已经采集 observation”这类历史 predicate 是单调的；freshness 与 authorization
predicate 在消费时求值，在 run 等待 approval 或 owner handoff 期间可能变为 false。
只有当类型化完成条件能移除过载规则或已测量失败时才清理对应 Profile，不能机械地
替换全部 Profile。

## 9. 应用于当前 Profile

| Profile 范围 | 目标处理方式 |
|---|---|
| `conversation.answer` | 保留 no-tool answer path。它不需要外部证据，也不能增加仪式性的 evidence node。 |
| 联网搜索和天气 | 每次 Provider 调用产生一个 source-observation event。Runtime 校验 query binding、provider status、result/card identity 和 freshness metadata。只有 grounded projection 无法直接完成综合时才使用模型。 |
| `document.read` | `confirm_document_target` 保持确定性。Direct reader 产生一个带类型化 locator 和 coverage 的 source observation。Finalization 读取该记录的 projection，不再创建“最终化证据”。 |
| `document.edit` | 把当前 `document_locate_evidence` 的工作视为“为编辑读取文档”：reader 与 format policy 产生权威 locator、hash、coverage 和结构候选。精确规则可直接确立 target assertion，但只能推进到现有 `select_edit_operation`/`document_edit` 节点。一个 eligible editor 可确定性选择；多个语义不同的 editor 由模型选择。编辑模型继续负责尚未解决的目标语义、operation-specific argument 和内容生成；Runtime 只绑定 path/output、identity、locator、scope、hash 与 freshness 等可证明参数。 |
| Browser r2 | Runtime 负责 acquisition、settle、snapshot、generation、ref membership、route check、transition digest 和 hidden/visible 区分。模型保留 goal assessment、control selection、存在多个语义工具时的 tool selection，以及 visual/semantic interpretation。每个 assessment/action 调用只接收当前目标判断所需的 control/state projection；Presentation evidence 与 hidden evidence 因 mode 和 generation 不同而保持独立。 |
| 定时任务管理 | Runtime 负责 list result、ID、version、due state、CAS 和 mutation outcome。模型可以消歧有界且 owner 可见的候选，但不能发明或复制 record ID。 |
| 外部 MCP 与 coding-agent 管理 | Runtime 负责 endpoint identity、credential scope、catalog revision、eligible namespaced tool、approval class 和 remote ID。模型可以在有界 operation 中选择或解释 returned content；returned content 不能授权另一个 operation。 |

迁移必须逐 Profile 进行并通过阶段 0 决策门槛，不能引入第二个通用 executor、平行
evidence store 或全局 evidence graph。

## 10. 迁移计划

### 阶段 0：决策门槛

- 盘点每个 Profile 的 node、completion rule、model call、`OutcomeRef`、evidence
  requirement、finalization read 和 audit payload。
- 为每个 model call 列出它负责的 semantic variable、允许输出和所需 source 字段。
  没有未解决变量的调用都是转入 Runtime 的候选；仍有目标判断、工具选择或内容生成
  变量的调用必须保留并收窄 projection。
- 测量没有未解决 semantic predicate 的模型调用、对未变化 source 的重复调用、重复
  持久化 payload bytes、prompt projection bytes、stale locator 或 approval 后版本
  变化失败，以及 projection 不一致造成的 finalization 信息缺失。
- 改变 type、persistence 或 plan shape 前，先定义该 Profile 预期下降的指标和正确性
  assertion。
- 只有至少一个问题被测量到，该 Profile 才进入后续阶段。如果指标基本为零，则停止：
  保留 ownership 规则，不做大范围 type 或 schema 变更。

### 阶段 1：Source 引用与 projection

- 定义能够由现有 state 与 artifact contract 表示的最小 source-event reference。
- 让 outcome、provisioning、decision、approval、audit 和 finalization 一致携带该
  reference。
- 从 ref 和 audit 移除嵌入 payload copy；保留由 source artifact 生成的有界
  projection。只有在所有消费者都改用 source reference 后，才移除冗余的大体积
  `ToolCall.Result` 持久化。
- 改变模型行为之前，先建立各领域 locator 与 staleness validator。

### 阶段 2：把确定性工作移入 Runtime

- 先转换确定性 target confirmation、candidate filtering、可证明的 target selection、
  Runtime-owned argument binding、freshness、transition 和 completion check。某个变量被
  确定性解决只会跳过该变量的模型调用，不会跳过下游 Workflow 节点。
- 当 tool 与 arguments 已冻结时，source acquisition 保持 direct。
- 保留由 focused test 保护的现有模型行为，直到每个 Runtime predicate 被证明等价或
  有意更严格。

### 阶段 3：收窄剩余模型契约

- 对目标选择用 candidate-ID selection 取代自由 locator generation；对工具选择只允许
  eligible entry ID；对内容生成只允许 operation-specific semantic field。
- 只提供 named semantic variable 所需的有界内容，并从 schema/projection 移除由
  Runtime 回绑的字段和与当前判断无关的 observation 内容。
- 持久化 semantic verdict 的 predicate version、owner-request binding、candidate/source
  ID、projection coverage、model-call provenance 和 reason code；不复制 source
  payload，也不合并独立 decision event。
- 移除冗余“证据”节点，并按实际工作重命名保留的 acquisition node。

### 阶段 4：清理类型化完成条件

- 只在通过决策门槛的 Profile 中，用明确 completion predicate 替换 catch-all
  `CompletionEvidence`。
- 只有在每个已注册 Profile revision 的 persisted-run resume 行为都定义并测试后，
  才移除 compatibility field。
- 实现完成后，把长期有效的所有权规则合并到[架构](architecture.md)和
  [Workflow 执行](workflow-execution.md)，更新能力矩阵，然后删除本提案。

## 11. 验收标准

只有满足以下条件，实现才算完成：

- 每个迁移的 Profile 都记录阶段 0 问题和 before/after 结果；
- 每个 model call 都明确指出 Runtime 无法确定性完成的 semantic variable、允许输出和
  最小输入 projection；
- execution/finalization model 不可用时，确定性 stage 仍能正确推进；
- 由精确规则唯一解决的当前 semantic variable 不调用模型，但 Workflow 仍执行后续必要
  的目标判断、工具选择、内容生成和 effect 节点；
- 多个语义不同的 eligible tool 仍由有界模型调用选择，模型生成内容仍能进入所选
  operation 的 schema，而 Runtime-owned 字段不进入该模型契约；
- 模型输出不能引入 candidate set 中不存在的 locator 或 scope value；
- 一次 source acquisition 只产生一个 observation event 和一个权威 archived payload
  reference；Workflow state、model projection、approval、audit 和 finalization 复用
  该 reference；
- 独立 retry 即使 payload 相同也保持为独立 observation event；任何物理 payload 去重
  都保留每个事件的 provenance 与 access scope；
- 同一记录的两个 projection 不计作两份证据；
- approval 绑定 source ID、subject version/generation 和精确 argument digest；stale
  hash、version、generation、directory view、remote scope 或变化后的 argument 在
  approval creation 和 approval/resume 后真正执行 effect 前都必须 block；
- before/after、hidden/visible、不同 provider 和不同 version observation 保持独立；
- 在不减少必要 source coverage 的前提下，迁移 Profile 中已测量的冗余模型调用、重复
  source call、重复 payload bytes、stale-binding failure 或 projection omission 下降；
- 现有 Policy、Approval、artifact retention、resume、backend parity 和用户可见能力
  契约保持不变；
- 每个迁移的 Profile 都有 focused unit、persisted-resume、默认 file backend scenario
  和 golden eval coverage。

## 12. 非目标与延后选择

本提案不会：

- 把外部、document、browser、MCP 或 model content 变成可信输入；
- 移除 artifact、provenance、audit、approval 或模型可见 source content；
- 要求把每个 semantic decision 改造成手写 heuristic；
- 因为 Runtime 已确立 evidence fact 而移除必要的模型目标判断、工具选择或内容生成；
- 根据相同 payload、prose、locator 或 source version 合并独立 observation event；
- 创建全局 evidence graph 或平行 evidence store；
- 在阶段 0 没有测到显著重复 payload 成本时强制实施物理 blob 去重；
- 定义最终 Go type、store table、API projection、enum name 或持久化迁移格式；
- 在对应 focused migration gate 通过前，声称未迁移 Profile、typed completion predicate
  或持久化 source identity 已经实现。

后续实现选择必须保持本文定义的 Runtime 所有权、逐 acquisition event 身份、消费者
复用和 fail-closed binding 不变量。
