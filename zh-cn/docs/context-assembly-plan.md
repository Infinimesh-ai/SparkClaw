# 上下文组装优化方案（阶段 0–1）

> 语言： [English](../../docs/context-assembly-plan.md) | 简体中文

状态：2026-07-27 部分实施。阶段 0.1、0.3 的代码部分和阶段 0.4 已实施；0.3
在 dual-light 上的 prefix-cache 性能测量、0.2 滚动压缩和全部阶段 1 项目仍待
完成。本文档继续作为待完成改动的设计依据；全部交付后其长期决策并入
[架构文档](architecture.md)，本方案按文档规则移除。

范围：`services/gateway/internal/agent` 内的 prompt 组装与工具结果拼装。不改
外部 API、存储 schema 或 Workflow Profile 契约。`modelrouter.Chat(ctx, task,
system, user)` 双字符串接口与 JSON-in-text 步骤协议**有意保留**：当前 Qwen
双通道对该协议的遵循度可接受，且 parse-error 恢复路径已覆盖其失败模式。

## 1. 本方案实施前基线

- 模型接口：每次调用一段 system 字符串加一段 user 字符串
  （`services/gateway/internal/modelrouter/router.go` 的 `Chat`）。没有
  messages 数组，没有原生 function calling。受限工具循环从模型原始文本中解析
  JSON action（`agent/workflow_step_output.go`）。
- 会话上下文：`buildAgentContextSnapshot`（`agent/context_snapshot.go`）按固
  定窗口选取——最近 8 条 user/assistant 消息（每条截断 360 字符）、最近 6 条
  跨 run 工具结果（摘要上限 4000 字符）、4 条 episode 摘要、4 条记忆、3 张图
  片——并按变体（`ForWorkflowStep` / `ForWorkflowStepCompact` / `ForTaskHint`）渲染成一整
  块文本。
- 工具结果：`adaptToolResult`（`agent/tool_result_adapter.go`）构建结构化
  JSON envelope（summary / structured / evidence），按工具类别抽取证据，三级
  降级压缩，单条 tool message 默认上限 1600 字节。完整输出以 artifact 落盘，
  由 `artifact_uri` / `ObservationRef` 引用。
- 循环预算（`agent/workflow_step_loop.go`）：自设 prompt 上限
  `defaultWorkflowStepContextTokens = 12288`；可用输入预算 80% 处做一次性 compact 压
  缩；run 级 observation 上限 48000 字节；token 估算是 chars/3 与 bytes/4 取
  大的启发式。

## 2. 方案制定时识别的问题（P0）

- **P0-1 —— observation 每步双份发送。** 非 compact 模式下，完整 observation
  列表既嵌入 system prompt（`contextualSystemPromptForWorkflowStep`，
  `agent/workflow_step_loop.go` 634 行附近），又出现在 user prompt
  （`workflowStepUserPrompt`）。每个工具步骤把整个列表重复发两遍，消耗 12k 预
  算，更早触发压缩，更早撞上硬终止。
- **P0-2 —— observation 增长导致 run 终止而非压缩。** `shouldStopWorkflowStepLoop`
  在累计 observation 达到 `MaxObservationBytes`（48000）后直接结束 run。单次
  `files.read` 的文档 envelope 就可能贡献 5–6 KB 证据，多步文档/浏览器任务会
  在中途以预算提示失败。**不存在 run 内压缩路径**——只有"停"。
- **P0-3 —— prompt 前缀每步变化，vLLM prefix cache 失效。** system prompt 每
  步重建且内含 observations，跨步不存在稳定前缀。在单 GB10 的
  `dgx-spark-dual-light-v1` 配置下（串行生成、fast 通道仅 8 GB KV cache），每
  步重算 8–10k token 的 prefill 是用户可感知的延迟。
- **P0-4 —— token 预算与部署 profile 脱节。** `effectiveWorkflowStepPromptBudget`
  把所有通道钳制在 12288 token，而 profile 实际提供 32k（fast）和 64k
  （deep）；chars/3 估算对中文为主的 prompt 未校准。

**有意不列为 P0**（推迟到阶段 1）：固定条数窗口、机械拼接的 episode 摘要、散
落的分段字节上限。它们降低长会话质量，但不会让任务失败。

## 3. 阶段 0 —— 投入使用前的修复

每项为独立的按主题提交。

### 0.1 observation 单份化（2026-07-27 已实施）

从非 compact system prompt 中移除 observation 块；user prompt
（`workflowStepUserPrompt`）成为唯一载体，与 compact 模式现有行为一致。保留的那
份格式不变，不引入模型行为再适应风险。

验收：每步 prompt 体积减少整个 observation 载荷；`agent` 包现有测试保持通过。

### 0.2 滚动压缩替代硬终止

`observationsBytes` 达到预算时，就地压缩最老的一半 observation，而不是停止：

- 每条被压缩的条目经现有 `compactObservationSummaryForContext` 逻辑降为单
  行，保留 `tool`、`status`、关键 structured 字段与 `artifact_uri`，并标记
  `compacted=true`。
- 最近 2 条 observation 永不压缩（当前执行状态必须保持原文）。
- 顺序保持不变。发出审计事件 `workflow_step.observations_compacted`，携带压缩前后字
  节数。
- 仅当所有可压缩条目压缩后仍超预算，才按现有提示终止 run。

仅将完整信息作为 artifact 落盘还不够，模型必须能够恢复被压缩的证据。因此本
项不得早于阶段 1.2 的统一回读工具交付。

验收：脚本化长 run（16+ 步文档/浏览器读取）能够完成而非停在
`workflow_step.budget_stopped`；审计中可见压缩事件。

### 0.3 稳定 prompt 前缀排序（代码于 2026-07-27 实施）

固定 prompt 布局：单次受限循环内不变的内容全部靠前，每步变化的内容只追加在
尾部：

- system prompt：静态规则 → skills → 工具定义 JSON → 会话上下文快照（run 开
  始时冻结）→ TaskHint。同一循环各步之间 system prompt 不得有任何变化。
- user prompt：step 头 → observation 列表（增长尾部）→ 输出契约。

仅调整顺序，内容不变。vLLM 的 automatic prefix caching 随后可在每步复用静态
前缀的 KV。压缩兜底路径（切换 compact 变体）仍会使前缀失效——这是可接受的
取舍，因为压缩是例外路径。

验收：dual-light 配置下，模型日志中第 1 步之后的每步 prefill token 数下降；
将前后测量记录到[模型基线](../benchmarks/model_baseline.md)。

### 0.4 通道对齐的 token 预算与校准估算（2026-07-27 已实施）

- `effectiveWorkflowStepPromptBudget` 改为读取当前通道 profile 的
  `context_tokens` 乘以 0.85 安全系数，替代 12288 钳制。12288 仅作为 profile
  未声明上限时的兜底。
- 用 vLLM `/tokenize` 端点对代表性中文、英文、JSON 样本做一次离线校准，得出
  `estimatePromptTokens` 的系数；运行时仍用系数估算（不引入在线 tokenizer 依
  赖），在常量旁注明校准日期与脚本。
- 0.80 压缩阈值不变。

风险说明：放宽后的 prompt 增大 prefill 成本；0.3 的前缀缓存抵消静态部分，
adapter 的单条上限仍约束尾部。

## 4. 阶段 1 —— 可扩展的组装结构

### 1.1 ContextBuilder

目前预算决策散落在多个常量（360 / 4000 / 1600 / 1400 / 48000……）和三个手写
渲染变体里。阶段 1 将其收敛为 `agent` 包内一个显式 builder：

- **section** 是注册单元，包含：类型、优先级、渲染函数、降级链
  `full → compact → drop`。
- 默认注册表（优先级从高到低）：

| Section | 降级链 | 现状对应 |
|---|---|---|
| 输出契约与安全规则 | 永不降级 | 步骤输出契约规则块 |
| 工具定义 JSON | full → compact（名称+必填参数） | compact 工具定义 |
| 当前 run 的 observations | full → 滚动压缩（0.2） | observation 列表 |
| 会话工具结果 | full → compact → drop | `formatContextToolResults` |
| 最近对话 | full → 尾部 4 条 → drop | `formatContextMessages` |
| Skills | full → compact → drop | skill 块 |
| 记忆 / 图片 / episodes | compact → drop | 其余 section |

- builder 接收该通道的真实输入预算（来自 0.4），按优先级自上而下分配，超预
  算时从最低优先级 section 起沿降级链降级，直到估算通过。
- `ForWorkflowStep`、`ForWorkflowStepCompact`、`ForTaskHint` 变为同一 builder 的三种预算配
  置；`full` 级渲染文本与今天的输出逐字节一致，引入时不改变模型可见行为。
- 未来新增上下文来源（日历、邮件摘要——见
  [暂缓能力](deferred-email-calendar-knowledge.md)）只需注册一个 section，
  而非修改三个渲染函数。

### 1.2 `observation.read` 工具

tool message 已携带 `artifact_uri`，但只有 `files.read` 有重读语义；被截断的
web/浏览器证据对模型不可恢复。新增一个只读工具：

- 名称 `observation.read`；参数 `artifact_uri`（必填）、`offset`、
  `max_bytes`；风险 `read`，无需审批。
- 通过同一 `adaptToolResult` envelope 返回落盘的完整工具输出，受同样的单条
  上限约束。
- 仅限访问当前会话的 artifact；URI 是不透明存储键，不存在路径语义。
- 在 `internal/toolhub/registry.go` 注册（注册表一致性测试随即覆盖），并作
  为受限循环始终可见的读取工具暴露。

这补上"截断可恢复"闭环的最后一块，也让 0.2 的压缩从有损变为无损。

### 1.3 模型生成的 episode 摘要

`summarizeEpisode` 目前是 tool:status 列表加截断的最终回答的机械拼接。将
`Summary` 字段内容升级为 fast 通道生成的摘要：

- run 结束后异步入队一次摘要请求（串行生成队列吸收，不阻塞交互路径）：输入
  为目标、工具列表与最终回答；输出为不超过 200 字、使用 owner 语言的摘要，
  说明做了什么、涉及哪些文件/URL、还有什么未完成。
- 模型出错或超时时保留现有机械摘要作为兜底——字段格式与消费方不变。

成本：每 run 一次小型 fast 请求。收益："继续改那个文件"这类跨 run 引用不再
受 8 条消息窗口限制，因为 episode 摘要变得真正有信息量。

## 5. 明确的非目标

- **原生 messages 数组 + function calling。** 应在 ContextBuilder 稳定后再
  做——届时 builder 输出从两段字符串换成 messages 数组是局部改动。不在本方
  案内。
- **embedding 检索式历史选择。** embedding 通道已存在，但单 owner 会话通常不
  长；仅在出现长会话引用丢失的证据后再评估。
- **全量历史 RAG / 层级记忆图谱。** 27B/35B-A3B 通道无法可靠驾驭复杂的检索
  拼装协议，单用户本地部署也不值得这份复杂度。

## 6. 验证

遵循[开发指南](development.md)与重构手册的基线纪律：任何改动前先记录测试基
线（按既有护栏先运行 `npm run setup:document-tools`），随后按阶段：

- 单元：0.2 的压缩触发/顺序/字节记账；0.4 的预算计算；1.1 的降级顺序与
  full 级逐字节一致性；1.2 由注册表一致性测试自动覆盖。
- 场景：在**默认 `file` 状态后端**上运行长文档与浏览器任务；确认不再出现
  `workflow_step.budget_stopped`，且 `observation.read` 能正确恢复被截断的证据。
- 性能：dual-light 配置下每步 prefill 的前后测量，记录到
  [模型基线](../benchmarks/model_baseline.md)。
- 审计：`workflow_step.prompt_compressed` 继续触发；新增
  `workflow_step.observations_compacted` 事件携带字节数。

## 7. 交付顺序

按主题提交，机械移动与行为变更绝不混在同一提交：

1. 0.1 observation 去重（行为变更，小）。
2. 0.3 前缀重排（机械重排 + 测试）。
3. 0.4 预算对齐 + 校准常量。
4. 1.2 `observation.read`（独立；为无损压缩提供恢复能力）。
5. 0.2 滚动压缩（行为变更，依赖 0.1 和 1.2）。
6. 1.1 ContextBuilder（先机械收敛，再接预算）。
7. 1.3 episode 摘要（独立）。
