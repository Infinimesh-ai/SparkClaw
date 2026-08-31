# 上下文组装优化方案

> 语言：[English](../../docs/context-assembly-plan.md) | 简体中文

状态：2026-08-31 完成实施。Observation 去重、滚动 observation 压缩、稳定 prefix 排序、
ContextBuilder、统一 observation envelope、`observation.read`、Profile 容量集成、有界历史
获取、fixed owner text 拒绝与合法结构化数据 variant 均已生效。

本文负责 `services/gateway/internal/agent` 内的语义 prompt section 与合法降级。
[模型容量契约](model-capacity-contract-design.md)负责可执行模型容量与 Router 最终准入；
[上下文历史设计](context-history-query-design.md)负责有界 Store 读取与 invocation snapshot 生命周期；
[Tree 上下文设计](tree-session-context-parity-design.md)负责 Tree-specific section policy。

Owner 决策：Memory 是产品空壳，不是 Agent 上下文来源。上下文组装不能调用 `MemoryRepository`，
也不预留 Memory section。

## 1. 设计结果

当前已实施流程有意保持简单：

```text
selected profile + typed operation
        |
        v
context_tokens - output_class_budget
        |
        v
Agent ContextBuilder
  fixed semantic sections
  legal full/compact/minimal/drop variants
        |
        v
complete rendered request
        |
        v
Model Router final token check
        |
        v
provider physical window
```

它分开三种职责：

- Agent 知道哪些语义内容必须保留、可以压缩或可以丢弃；
- Model Router 依据一份已解析物理容量契约，检查完整 system/user/schema/image 请求；
- Provider 是最后一道物理防线，而不是第一次发现超限的位置。

模型窗口扩大时，本文不选择更多历史。历史选择仍固定为 8 条 message、6 条 tool call、4 条
episode 和 3 张 image。更大窗口只允许同一批选中信息保留更丰富的合法 variant。

## 2. 当前基线

### 保留的已实施行为

- `buildAgentContextSnapshot` 选择固定 8/6/4/3 session context，并提供 Workflow 与 routing render
  路径；
- 每个 step 的 observation 只在一个模型可见位置出现，不再同时出现在 system 与 user prompt；
- `adaptToolResult` 产生统一 summary/structured/evidence envelope；完整输出作为 artifact 存储，
  并由 `artifact_uri` 或 `ObservationRef` 引用；
- 较旧 current-run observation 原地压缩，最新两条受保护；只有不可继续压缩的 observation 超限才
  结束 run；
- loop 内稳定内容位于逐 step observation 之前，允许模型 prefix cache 复用；
- ContextBuilder 支持显式 section variant，不再只有单一 prompt string；
- `observation.read` 在既有 read limit 与 Policy 检查下，提供 support-scope-governed 的持久化证据恢复。

### 已实施变更

- Workflow admission 从所选 profile 解析 typed operation 容量，旧常量与兼容算术已移除；
- Workflow、Tree 与 conversation-answer builder 在早期超长问题 gate 后，把原样 owner question
  注册为 fixed section；
- Owner text、当前 resource、已解析 document、history、observation、schema 与 output contract
  保持为独立语义 section；
- JSON、resource、document、evidence 与 tool-schema section 只在完整注册 variant 之间切换；
  ContextBuilder 不再提供任意 hard truncation policy；
- Agent 每个 invocation 只执行一次有界 Store 获取，recent-document resolution 复用这些候选；
- 所选 catalog、typed operation registry、ContextBuilder、Router 与本地模型 entrypoint 共享同一
  容量契约，不存在 fallback 来源。

## 3. ContextBuilder 的容量输入

每个生成 operation 在代码中映射到一个输出能力等级和允许的 lane 集合。所选 profile 提供一个物理
窗口与各等级正数预算：

```text
output_budget = selected_lane.output_budgets[operation.output_class]
input_budget = selected_physical.context_tokens - output_budget
```

ContextBuilder 从不可变的所选容量 catalog 接收已解析 `input_budget`。调用方不传数值输出额度。目标态
没有独立 `max_input_tokens`、profile-wide 输出上限，也没有每个 Workflow/step/repair invocation
一个字段。

容量 catalog 在加载时一次性解析与校验。Runtime 不为每个请求重新评测或规划一个独有上限，只做
不可变的 operation-to-class 查找与减法，也可以预计算。每个不同请求必须重复的是对实际渲染 system
content、user content、response schema、template option 与 image 的 token 计数。

Profile 值缺失、为零、为负、未知或关系非法时，在 Agent 或 Router 构造前加载失败。语义降级不能
修复非法容量。当前执行路径不得采用旧常量、其他 class、其他 lane、环境默认或省略后的 provider 行为。

## 4. Section 契约

Section 是语义单元，不是任意字符串切片：

```go
type ContextSection struct {
    Kind      SectionKind
    Policy    SectionPolicy
    Variants  []RenderedVariant
}
```

每个消费者注册自己的 section 顺序与优先级。ContextBuilder 提供选择合法 variant 的通用机制，
不为 Tree、Workflow、conversation answer 和 direct chat 强制一套全局降级顺序。

当前已实施的 Workflow 契约为：

| Section | 必须行为 |
|---|---|
| System safety 与 execution rule | fixed；不做内容截断 |
| Output schema/contract | fixed 且结构完整 |
| 原样 owner question | fixed；不截断、不摘要、不与 resource 合并 |
| 当前受治理 resource | protected `full -> minimal`；当前显式 resource 不静默消失 |
| Tool definition | 合法 `full -> compact`；每个选中定义保持有效 JSON/schema |
| Current-run observation | `full -> compact`；保护最新执行证据并保留 artifact reference |
| 选中 session tool result | `full -> compact -> drop` |
| 选中 recent conversation | `full -> compact -> drop` |
| 选中 image 与 episode | consumer-specific compact/drop variant |

Tree 使用 Tree 文档中的独立 policy。Final-answer assembly 可以采用不同优先级，因为它需要 evidence
与用户连续性，而不是 route ambiguity。所有消费者从同一批选中历史记录开始，但不必渲染相同 section。

### Fixed Content

Fixed 表示精确保留语义，不表示 provider 必须接纳它。当 optional section 已处于最小合法 variant，
而 fixed instruction、原样 owner question、完整 schema 与必要结构化 metadata 仍无法容纳时，assembly
以类型化 input-too-long error 失败，绝不截断 fixed content 强行 dispatch。

早期 `owner_question_too_long` gate 继续独立存在，因为 Guard 与 Embedding 必须在历史或路由前解析
原文。后续 whole-request check 仍然必需，因为 system rule、schema、resource、history、observation
与 image token 同样占用容量。

### 结构化 Variant

JSON、schema、resource、tool definition 与 evidence section 在每一级都必须保持语法和语义有效。
允许的转换包括：

- 通过类型化 compact projector 删除 optional field；
- 用有界 summary 加 `artifact_uri` 替换 evidence body；
- compact schema 只保留必填 tool argument；
- 选择完整的 routing-minimal resource record；
- 显式丢弃标记为 optional 的 section。

禁止 byte/character prefix、suffix、中间截断、非法 JSON fragment 和静默 owner-text trim。Per-section
byte ceiling 可以继续约束已经合法的 variant，但不能授权 malformed output。

## 5. 组装与 Admission 算法

每次模型请求由 Agent 执行：

1. 从所选不可变容量 catalog 解析 typed operation、allowed lane、output class 与 `input_budget`；
2. 创建 fixed section 以及 optional/protected section 的所有合法 variant；
3. 按消费者定义顺序渲染完整 full request；
4. 用共享 model-aware counter 计数；
5. 如果超限，把最低优先级 eligible section 切到下一个合法 variant，并重新计数；
6. 请求可容纳或没有合法降级时停止；
7. 把完整渲染请求与 typed operation 传给 Model Router；
8. Router 在全部 transport option 与 schema 确定后重复权威计数。

Router 不做语义选择。它拒绝超限，但不删除 message、不截断 string、不选择更大 output class，也不
切换 lane。Provider 接收显式 class budget，只负责最后的物理窗口 enforcement。

Model-aware counter 先用经测试的保守上界，在请求接近边界时使用所选模型 tokenizer。旧 chars/3 与
bytes/4 estimator 只保留在隔离的 snapshot 测试中，不决定生产 admission。

## 6. 稳定 Prefix 与 Current-Run Observation

同一个有界 Workflow loop 内，prompt 顺序保持：

```text
system: static rules -> selected tool definitions -> frozen session context
        -> frozen Workflow stage context

user:   step header -> current-run observation tail -> fixed output contract
```

Loop 内不变内容在初始 variant 选中后保持 byte-stable；增长的 observation 位于尾部。如果后续 prompt
必须使用更小的 frozen-context variant，则显式转换并记录 prefix change。Prefix-cache 性能不能覆盖
正确 admission。

Observation compaction 保留 tool identity、status、关键 structured field 与 artifact reference。
最新两条 observation 不压缩，较旧条目可以压缩。合法压缩后仍超过独立 run observation-byte ceiling
时，保留现有确定性 budget stop。

本容量项目不改变 `observation.read` 暴露。其已实施的 support-scope authorization、read count、byte
window、artifact ownership 与 audit 行为继续作为权威。未来可以把条件化暴露作为独立 tool-surface
优化评测；容量正确性不依赖它。

## 7. 历史 Snapshot 集成

Owner-question gate 通过后，Runtime 构建一份有界 `InvocationHistory`。Agent 只选择一次 8 条
message、6 条 tool call、4 条 episode 和 3 张 image，并把不可变选中值传给 Tree、Workflow 与
final-answer 消费者。Recent-document fallback 复用已经加载的有界候选。

ContextBuilder 负责该选中值的渲染和合法降级。它不查询 Store、不继续分页，也不根据物理模型窗口
改变选择数量。Tree 与 Workflow 共享选中 record identity，而不是逐字节一致的最终 prompt。

Resume 时，Runtime 从原始 `AgentRun.StartedAt`、`Intent.SourceTurnID` 与 `RunID` 最多重建一次
snapshot。不增加持久化 history anchor、选中记录副本或 prompt cache。

Memory repository 结果不出现在 acquisition 或 section registry 中。现有 MemoryStore backend 只是
Store backend，不能与未使用的 Memory 产品功能混淆。

## 8. Episode Summary 决策

撤回先前“每个 run 结束后调用一次 Fast 模型生成 episode summary”的提案。它会增加 queue load、
latency、failure state、capacity classification 与评测工作，但没有证据表明当前机械 summary 已导致
实质性跨 run 失败。

保留当前有界机械 episode summary。后续改进必须先有独立的可测问题，并与更简单的确定性 projection
调整比较。本文不新增 async summary queue、model operation、output class、retry 或 model-error
fallback。

## 9. 明确的非目标

- 转换为 native messages array 或 provider-native function calling；
- Embedding-based history retrieval 或 full-history RAG；
- 按 task、step、candidate count 或 prompt size 动态规划输出；
- 自动处理超长 owner 问题；
- 激活 Memory 或把 Memory 投影进上下文；
- 第二个 ContextBuilder、Router、capacity registry 或 global prompt cache；
- 修改 Workflow time、step、action、duplicate、observation、support-read 或 concurrency limit。

## 10. 失败与可观测性

失败原因保持类型化且相互独立：

- `owner_question_too_long`：原样 owner text 无法通过必经的早期 Guard 或 Embedding admission；
- `model_input_too_long`：全部合法语义降级后，完整请求仍无法容纳；
- `invalid_model_capacity`：所选 profile 缺失或非法，启动失败；
- `model_output_incomplete`：provider 报告 `finish_reason=length`；
- 现有 observation-byte stop：合法压缩后 current-run evidence 仍无法容纳。

Audit 记录 operation、lane、output class、物理 context、input budget、count source、初始/最终 token
count、选中 variant 与前后 byte。不得记录丢弃内容或 owner text。现有
`workflow_step.prompt_compressed` 与 `workflow_step.observations_compacted` 继续作为行为信号。

## 11. 交付顺序

### 已实施并保留

- observation 单份化；
- 滚动 observation 压缩与 artifact 恢复；
- 稳定 prompt-prefix 排序；
- ContextBuilder 机制与统一 observation envelope；
- support-scope-governed `observation.read`。

### 已实施：容量迁移

1. Typed operation、output class 与 selected-profile validation 已生效；
2. 已解析 class input budget 已替换 Agent 常量与 caller budget；
3. Owner text 是独立 fixed section；
4. 合法 projector 已替换任意 structured-data trim；
5. Model Router 在全部入口应用最终 admission 与 finish-reason handling。

### 已实施：有界历史集成

1. 三个有界 recent-query repository method 负责 Agent 获取；
2. 构建一份 invocation-owned candidate 与 selected snapshot；
3. Tree、Workflow、final-answer 与 document resolution 显式接收该值；
4. Agent 热路径已无完整列表与重复历史读取。

### 只做测量

- 在当前本地 profile 记录 prefix-cache prefill 行为；
- 只有 routing 与 workflow 评测证明质量或 latency regression 时，才调整 section policy。

模型生成 episode summary 不在交付路径中。

## 12. 验证与验收

实施只在以下条件全部满足时验收：

- 每个生产模型调用映射到一个 allowed lane 和 output capability class，一个 class 可以服务多个
  operation；
- 所选 profile 容量非法时加载失败，测试中不存在默认或借用值；
- 改变物理 `context_tokens` 会改变可用输入预算，但不改变固定历史数量；
- 原样 owner text 是 fixed section，超长文本在历史或执行前被拒绝；
- 每个 JSON/schema/resource/evidence variant 降级后仍然有效；
- Router 在 provider dispatch 前拒绝超限的完整请求；
- Observation 只出现一次，在当前授权下可恢复，并按确定性顺序压缩；
- Tree、Workflow 与 final answer 复用同一选中历史值，同时保留 consumer-specific prompt policy；
- recent-document fallback 不重复读取 tool history；
- external-MCP 与 Memory context 保持为空；
- `finish_reason=length` 不能产生成功持久化或投递；
- 现有 Policy、Approval、artifact scope、claim coverage、Workflow budget 与 external-MCP isolation
  测试保持通过。

运行聚焦的 config、Agent、Model Router 与 Store contract test，以及完整 Gateway build/test/vet、
默认 File 与可用 PostgreSQL coverage、routing/model golden evaluation、适用时的 prefix-cache
measurement 和双语文档检查。

## 13. 所有权边界

- `internal/agent/context_builder.go`：通用合法 variant 选择机制；
- 各 Agent consumer：自己的 section membership、order、priority 与 fixed content；
- `internal/agent/context_snapshot.go`：选中有界历史值，不拥有 Store acquisition policy；
- `internal/modelrouter`：不可变容量查找、model-aware 最终计数、finish reason 与 transport bound；
- Store repository：有界历史 candidate access；
- `configs/model.profiles.json`：物理 context 与 output-class budget；
- Policy/ToolHub：`observation.read` authorization 与 limit。

长期当前行为已同步到 Architecture、Workflow execution、Intent routing、Store、Model loading 与
Deployment。本文作为 owner 要求的设计记录继续保留。
