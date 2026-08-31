# Tree 同 Session 上下文一致性设计

> 语言：[English](../../docs/tree-session-context-parity-design.md) | 简体中文

状态：设计经接受与最小架构复审后，于 2026-08-31 完成实施，包括同 session 上下文集成与
第 8 节的 Tree 结构化输出加固。

本文细化[意图路由](intent-routing.md)中的 Fast/Tree 上下文契约。
有界 Store 获取与 invocation snapshot 生命周期由
[上下文历史设计](context-history-query-design.md)负责；物理容量、输出能力等级与最终准入由
[模型容量契约](model-capacity-contract-design.md)负责。

目标是选中历史事实的一致性，不是 prompt 字节一致。Tree 与 Workflow 消费同一份 invocation-owned
选中记录，再根据各自不同的模型契约独立渲染所需语义 section。

## 1. 已接受决策

| 主题 | 已接受决策 |
|---|---|
| 历史来源 | Tree 与 Workflow 从一次有界获取中接收同一份不可变 8/6/4/3 选中记录集合 |
| Prompt 等价 | 不要求 prompt、section label、channel 或最终降级子集逐字节一致 |
| 选择扩张 | Fast 物理窗口扩大时不增加历史选择数量 |
| 消费者策略 | Tree 拥有 Tree-specific section variant 与优先级；Workflow 可以从同一源 snapshot 作不同语义选择 |
| 资源元数据 | 只向 Tree 暴露路由相关字段，权威 binding 字段保留在 Runtime |
| 容量 | Tree 使用 Fast `context_tokens` 减去 `compact_structured` 输出等级预算；不增加 Tree 专属输入上限 |
| 初次与 Repair | 二者使用相同 strict response schema 和 `compact_structured` 等级；repair 是 attempt，不是新容量族 |
| 结构化输出 | 保留 candidate array、强制关闭 thinking、请求 strict JSON Schema、保留 Runtime 校验且最多 repair 一次 |
| Memory | Memory repository 记录不进入 Tree 或 Workflow 上下文 |

## 2. 范围与非目标

本文覆盖一个人类 owner session 内的自然语言路由：

- 选中的先前 message、tool result、episode summary 与 image；
- 当前受治理 resource 与已解析 document reference；
- 完整 Tree prompt 组装与 admission；
- 已实施的 Tree candidate-scoring JSON 契约。

本文不改变：

- 跨 session retrieval 或 Memory 行为；
- 固定的 8 条 message、6 条 tool、4 条 episode、3 张 image 选择；
- Embedding 输入，仍为未经修改的当前 owner 问题；
- 语义图所有权、fusion weight、threshold 或 route binding；
- Policy、Approval、artifact authorization 或 external-MCP isolation；
- temperature、seed、多次采样、consensus 或 score calibration；
- 根据 candidate count 动态计算输出预算；
- Workflow prompt 布局或降级策略。

路由完成后才产生的 observation 不是 Tree 历史输入。Inbound external-MCP 路由不继承 session
message、tool、image 或 episode；已审批的 current-run evidence 继续走现有受治理路径。

## 3. 已实施边界

Tree 与 Workflow 现在从同一份 invocation-owned snapshot 开始；Tree 只在全部 fixed 与可降级
section 已知后，才对完整路由请求执行 admission：

| 关注点 | 已实施行为 |
|---|---|
| 历史获取 | 一次有界 invocation 获取，再由 Agent 固定选择 |
| 历史预算 | 不设置独立历史 token allowance |
| Resource/document | 同一个 Tree builder 内的类型化 section |
| 超限 | 完整合法的语义 variant；不任意截断结构化数据 |
| 最终权威 | Router 在 provider dispatch 前检查完整请求 |

该边界避免历史过早压缩后又因后置 resource 溢出。结构化 resource projection 在所有合法
variant 下都保持有效，同时没有引入第二个历史源或 prompt 副本。

## 4. 共享 Invocation 上下文

早期 owner-question gate 和受治理 resource resolution 完成后，Runtime 从 invocation-owned 历史值
构造一份路由上下文：

```go
type TreePromptContext struct {
    CurrentQuestion  string
    CurrentResources ResourceRoutingProjection
    Documents        DocumentRoutingProjection
    History          agentContextSnapshot
}
```

`History` 包含传给 Workflow 的相同选中 record identity：

- 最多 8 条先前 eligible user/assistant message；
- 最多 6 条先前 run 的 terminal tool call；
- 最多 4 条 episode summary；
- 最多 3 张近期 session image。

当前 source message 与当前 run 按[历史设计](context-history-query-design.md)排除。Runtime 不为 Tree 与
Workflow 分别重新读取历史。

这是源一致性，不是渲染等价。Tree 可以在超限时丢弃 episode 而 Workflow 保留，可以使用 routing-only
compact projection，也可以把数据放在不同 label 下。两个消费者仍从同一冻结记录开始，因此这些
选择不会造成历史事实不一致。跨消费者共享降级顺序会错误耦合两种 prompt 契约，本文不引入它。

历史继续是不可信数据。先前 user/assistant 文本、tool output、image metadata 与 episode text
永远不能获得 system instruction 权威。Memory 产品仍为空壳期间，绝不查询 `MemoryRepository`。

## 5. Resource 与 Document 投影

当前 resource 和已解析 document 是独立的类型化 section，因为它们的 provenance、freshness、
precedence 与 authorization 不同于对话历史。

Tree-visible 当前 resource 只包含路由相关字段：

```text
kind, name, ref, content_type, caption
```

Tree-visible resolved document 只包含：

```text
name, ref, content_type, format, source, activity, provenance
```

Hash、byte count、dimension、内部 message-part ID、document ID、parent ID 和 source ID 保留在
权威 Runtime state。Tree 不能把可见 reference 变成授权，也不能绑定最终 target。

两类投影都使用结构化 serializer。Variant 必须是 `full`、`minimal` 等完整合法值，任意字符前缀
永远不是 variant。Minimal 形式在对应值存在时保留 `name`、`ref`、`kind` 或 `format`，以及
`provenance`。

当前 turn resource 优先于 resolved recent-document metadata，后者优先于历史 reference。冲突对
模型保持显式，最终 binding 权威仍属于确定性 Runtime 逻辑。

## 6. Tree Prompt Builder

只有在 graph、question、resource、document、history、response schema 与 call option 全部确定后，
Tree 才组装完整请求：

| Section | Policy |
|---|---|
| Tree instruction 与 injection boundary | fixed system content |
| 完整 eligible 语义图及 revision | fixed structured data |
| Source kind 与原样 owner question | fixed data |
| 当前受治理 resource | protected `full -> minimal`；不允许任意截断 |
| 已解析受治理 document | protected `full -> minimal`；不允许任意截断 |
| 同 session 历史 | Tree-specific `full -> compact -> drop` variant |
| Strict output contract/schema | fixed tail contract |

语义图、当前问题和输出契约永不截断。每个 eligible semantic candidate 恰好出现一次。输出契约保持
最后一个 prompt section，防止其后出现不可信数据。

Tree 首先用 full 合法 variant 渲染全部选中历史。只有整体 prompt 真正超限时，才执行 Tree-local
降级：

1. 丢弃或压缩 episode summary；
2. 丢弃较旧 image projection；
3. 压缩或丢弃较旧 tool-result projection；
4. 压缩或丢弃较旧 conversation projection。

只要仍保留任意可选历史，最新两轮对话和最新一条相关历史工具结果就受保护。该顺序不是全局
ContextBuilder 规则，也不约束 Workflow。

全部可选历史已移除，受保护 resource/document 已为 minimal，而 fixed content 仍无法容纳时，Tree
在 provider dispatch 前以类型化 prompt-overflow error 失败。

## 7. 容量与 Admission

Tree 初次与 repair 的类型化 operation 都映射到 Fast lane 的 `compact_structured` 输出能力等级：

```text
tree_output_budget =
  selected_fast_lane.output_budgets["compact_structured"]

tree_input_budget =
  selected_fast_physical.context_tokens - tree_output_budget
```

两个值都是必填正数 profile 事实，且输出预算必须小于物理窗口。容量缺失、为零、格式错误、未知或
关系非法时，在 Router 或 Agent 构造前阻止所选 profile 加载。

当前计算中没有 `max_input_tokens`、profile-wide 输出上限、固定 3,000-token Tree history allowance、
调用方输出数值、provider 默认或旧常量。因此 Fast 物理窗口变化会自动改变 `tree_input_budget`，
而选中历史数量保持固定。

Agent 选择 Tree variant 时可以使用共享 model-aware counter。Model Router 对完整 system content、
user content、strict response schema、chat-template option 与图片 token reserve 重复执行权威检查。
Router 不压缩内容也不切换 lane。Provider 物理拒绝是最后防线，不是正常 admission。

初次与 repair 的响应形状相同，因此共享一个输出等级，但保留不同 operation/audit identity。更大的
候选集合由评测覆盖到语义图配置的最大值；Runtime 不为每次请求重新计算输出预算。修改该最大值或
response schema 时，需要重新进行代表性能力等级评测。

## 8. 已实施的 Tree 评分 JSON 加固

受治理输出是初次 Fast/Tree score 调用及可选 repair 的原始响应：

```json
{
  "graph_revision": "...",
  "candidates": [
    {"candidate_id": "...", "tree_score": 0.0}
  ]
}
```

它不是语义图输入、Workflow action/final JSON、持久化 fusion record 或 File Store JSON。

两个调用都请求由冻结 graph revision 和 eligible candidate set 派生的同一 OpenAI-compatible
strict JSON Schema。Schema 要求：

- graph revision 与当前 revision 完全一致；
- array length 等于 eligible candidate 数；
- ID 只能来自 eligible set；
- 每个 item 有一个 `[0, 1]` 范围的数值 `tree_score`；
- 所有声明字段必填，且不允许额外字段。

两个调用都通过 per-request chat-template option 强制关闭 thinking。Endpoint 拒绝或忽略 structured
output 时，不得 fallback 到无约束文本。

Runtime 校验仍是最终权威，因为 JSON Schema 不能保证动态 candidate 每个恰好出现一次。Runtime
拒绝未知字段、过期 revision、错误 candidate 数量或集合、重复 ID、缺失 score 与越界 score。
初次响应无效时，最多使用相同 schema 和输出等级 repair 一次；第二次仍无效时，Tree 在正好两次
模型调用后失败，不让任何 Tree score 进入 fusion。

即使前缀可以解析，`finish_reason=length` 也属于未完成，不能作为 score set 接受，也不能换用更大
输出等级。现有 repair policy 可以用同一等级消耗其一次 repair attempt，否则 Tree 失败。

该契约只加固结构。Temperature 保持 `0.2`；不增加 seed、多次采样 consensus、calibration、variance
threshold 或请求时输出扩张。

## 9. 可观测性与失败

Tree 改变任何 section variant 时，Runtime 发出 `intent_tree.prompt_compressed`。安全 audit 字段包括
profile、物理 context、output class、input budget、初始/准入 token count、选中 variant、前后 byte
count 与 section digest；不保存被丢弃的历史正文或 owner-question 内容。

失败类型保持分离：

- `owner_question_too_long`：早期 Guard 或 Embedding gate 在历史获取前失败；
- `model_input_too_long`：合法语义降级后完整 Tree 请求仍无法容纳；
- structured-output invalid：初次及可选 repair 未通过 schema 或 Runtime candidate validation；
- transport/provider error：按 Model Router 的显式同 lane retry contract 处理。

这些错误都不能触发更大输出等级、跨 lane 重试、任意截断或无约束 Tree 响应。

## 10. 实施记录

### 已实施：结构化输出

- 通过 Model Router 传递 strict JSON Schema 与 non-thinking option；
- 派生一份 dynamic schema，由初次与 repair 调用复用；
- 保留精确 candidate validation 与单次 repair 的 fail-closed 边界；
- 覆盖 transport、schema construction、初次成功、repair 成功和 repair 失败终止。

### 已实施：有界共享源

- 完成有界历史 query 与 invocation snapshot selection；
- 锁定 eligibility、8/6/4/3 selection、当前 turn exclusion 与 external-MCP 空历史 fixture；
- 把同一选中 snapshot 值传给 Tree 与 Workflow，不要求逐字节渲染一致。

### 已实施：类型化 Tree 组装

- 用类型化 graph、resource、document 与 history 输入替换预渲染 routing-context string；
- 增加完整 Tree builder 与合法结构化 variant；
- 删除 3,000-token history 约定与字符截断；
- 保持 owner question 与 output contract fixed。

### 已实施：容量集成

- 把两个 Tree operation 都映射到 Fast 的 `compact_structured`；
- 消费所选 Fast 物理窗口与能力等级预算；
- 对 initial 与 repair 应用 Router 最终准入；
- 增加 invalid-profile、fixed-overflow 与 section-degradation 测试。

### 实施后验证与当前状态文档

- 在本地与 Hosted Fast 运行 routing golden evaluation，以及歧义、纠正、无关历史、多语言与注入用例；
- 测量 prompt size、prefill latency、Tree timeout、repair rate 与 routing change；
- 实施后把长期行为合并进中英文 intent-routing 与 architecture 手册。

## 11. 验收标准

- 一次 invocation 内，Tree 与 Workflow 从相同选中历史 record identity 开始；不要求 prompt 字节等价；
- 固定 8/6/4/3 selection 不随物理模型容量变化；
- 完整 Tree prompt 可容纳时，不降级历史；
- Graph、question、resource、document 与 response schema 确定前，不单独 admission 历史；
- 降级后的每个结构化 section 仍有效，owner question 保持原文；
- 代词追问、省略 target、纠正与先前 tool reference 的路由质量不低于 baseline holdout；
- 当前 owner input 优先于冲突的旧上下文；
- 不可信 resource、document、tool、assistant、image 与 episode text 不能注入 Tree 指令；
- External-MCP 路由不继承先前 session derivative，且不查询 Memory；
- 所选 profile 容量非法时，在任何 Tree 调用前失败，且不使用默认或借用值；
- Router 在 HTTP dispatch 前拒绝超限的完整 Tree 请求；
- Initial 与 repair 使用同一 strict schema 和 `compact_structured` 等级，同时保留独立 audit identity；
- Runtime 校验精确 candidate membership 与 uniqueness；
- 两次 malformed response 只产生两次调用、Tree 失败且没有 score 进入 fusion。

## 12. 所有权边界

- `internal/agent/context_snapshot.go`：消费者共享的有界选中源；
- `internal/agent/context_builder.go`：通用合法 variant admission 机制，不拥有全局消费者优先顺序；
- `internal/agent/intent_router.go`：Tree-specific section、priority、prompt tail 与 candidate validation；
- `internal/modelrouter`：类型化 operation/class mapping、最终 request admission、strict schema、
  non-thinking option、finish reason 与 transport；
- message/resource projection 代码：routing-only 字段，Runtime 保留 binding 权威；
- 上下文历史设计：Store read 与 invocation snapshot 生命周期；
- 容量设计：profile 事实与共享计数契约。

本文不增加第二个 history store、capacity registry、router、model lane 或 model-owned route decision。
