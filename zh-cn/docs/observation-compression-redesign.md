# 观测压缩与证据供给重构设计

> 语言： [English](../../docs/observation-compression-redesign.md) | 简体中文

状态：2026-08-03 已实施。统一观测信封、运行时证据供给、
`observation.read`、滚动压缩、ContextBuilder 整合和多观测最终合成都已
交付；上下文方案 1.3（模型生成的情节摘要）仍是独立待办。
Issue #17 于 2026-08-18 收紧了已交付实现的准入、scope、配额和硬停止语义；当本文原始
设计与当前实现不同时，以 [Workflow 执行](workflow-execution.md)为准。

范围：工具结果压缩（`services/gateway/internal/agent/tool_result_adapter.go`）、
按工具区分的观测预算（`agent.go` 的 `toolResultObservationBudget`）、workflow
步骤 prompt 组装、workflow 最终答案证据，以及
[上下文组装方案](context-assembly-plan.md) 中的待完成项。不改外部 API、存储
schema 或 artifact 归档写入语义；内部 artifact store 读取接口扩展为支持有界
读取。`modelrouter.Chat(ctx, task, system, user)` 双字符串
接口与 JSON-in-text 步骤协议保持不变。

与上下文组装方案的关系：本文档是该方案待完成项 0.2（滚动压缩）、1.1
（ContextBuilder）、1.2（`observation.read`）的设计依据，并新增该方案未覆盖
的两项决策：统一观测信封与运行时证据供给。1.3（模型生成的情节摘要）仍按原
方案执行，不受影响。

## 1. 设计原则

正确的压缩机制会消除大预算工具存在的必要。

目前有两个工具（`files.read`、`browser.snapshot`）被豁免于观测信封上限，
可以向模型可见的观测列表注入最多约 44 KB 的内容。这个豁免之所以存在，只是
因为压缩是有损且不可逆的：信封丢掉的内容模型永远无法再看到，于是 workflow
真正需要其内容的工具被赋予了"不压缩"待遇。豁免是症状，不可逆才是病根。

本重构反转这份契约：

- **观测信封对所有工具统一为小尺寸**。它是持久化证据之上的索引——状态、
  关键结构化字段、简短摘录、截断标记和 artifact 引用——绝不是大块内容的
  载体。任何工具、capability 或配置都不能将其放宽。
- **内容只通过刻意设计、按消费者定尺的通道进入模型步骤**：
  1. **运行时证据供给**（主通道，确定性）：workflow 阶段声明自己需要哪份
     持久化证据、预算多大；运行时从 artifact 存储物化对应切片注入步骤
     prompt。模型既不请求也不转述这些内容。
  2. **`observation.read`**（辅通道，模型驱动）：只读工具，返回当前会话
     任意 artifact 的有界窗口，覆盖 profile 未预料到的场景。

这与运行时既有的参数绑定哲学一致：执行所依赖的值从持久化状态物化，而不是
托付给——或受制于——模型可见文本。

## 2. 现状问题

- **P1 —— 大预算豁免是一份写错了的名单。**
  `toolResultObservationBudget`（`agent.go`）按工具名给 `files.read` 与
  `browser.snapshot` 放大预算。`pdf.extract_text` 与 `images.inspect` 注册
  的是同一个 `document.read` capability（`internal/toolhub/registry.go`），
  却落入默认 2400 字节信封、1400 字节证据上限：PDF 正文以约 1.4 KB 的碎片
  到达模型，而文本文件可达约 44 KB。下游判断（编辑操作选择、内容问答）恰
  好在名单遗漏的格式上失败。
- **P2 —— 信封同时服务两个尺寸需求相反的消费者。** 同一个
  `ObservationSummary` 字符串既是（a）当前步骤的工作证据，又是（b）run 与
  会话的历史记录。按（a）定尺会撑爆预算；按（b）定尺会饿死（a）。单一
  尺寸不可能同时正确。
- **P3 —— 大信封摧毁 run 预算。** 一次被豁免的 `files.read` 就能吃掉
  48 KB `workflow_run_max_observation_bytes` 上限中的约 44 KB，迫使多步
  文档/浏览器任务提前进入紧凑模式或以
  `workflow_step.budget_stopped` 终止。
- **P4 —— 压缩对模型不可逆。** 完整输出带 `ObservationRef` 归档，但只有
  `files.read` 有重读语义。被截断的 PDF、网页、浏览器证据无法恢复——这正
  是 P1 豁免被引入的根本原因。
- **P5 —— 非文档类最终合成只看一条观测。** `workflowFinalEvidence`
  （`workflow_runtime.go`）对 document-read 调用读取完整持久化结果，但对
  其他 workflow 形状回退为单条观测，拖累多步浏览器/搜索任务的合成质量。

## 3. 目标设计

### 3.1 统一观测信封

- 删除 `toolResultObservationBudget` 中的 `files.read` /
  `browser.snapshot` 分支。所有工具调用都以
  `observation_summary_max_bytes`（默认 2400）与默认证据上限（1400）适配。
  既有的三级降级与 `structured.message_truncated` 标记不变。
- 信封始终携带 artifact 引用（`artifact_uri` / `ObservationRef`），且在
  证据被截断时携带标准化的 `next_step_hint`，指名 `observation.read`。
- `workflow_run_max_observation_bytes` 不再作为信封尺寸的输入，仅保留
  run 级累积预算职责。

### 3.2 运行时证据供给

`workflowStageContext` 增加声明式证据需求：

- 每条需求指定**来源**（具名计划节点已完成的 outcome ref，或本 run 内某
  类别最新的 artifact——与参数绑定使用同一套持久化 ref 词汇）、**切片
  模式**与**字节预算**。
- 切片模式：`head`（前缀字节）与 `structured`（按 artifact 类别感知：
  浏览器快照切片保持元素条目完整、绝不把 ref 切断在条目中间；文档切片
  保持段落/行完整）。模式实现与 `tool_result_adapter.go` 中的证据提取器
  放在一起。
- 步骤开始时，运行时对照持久化状态解析每条需求，读取归档的完整输出，把
  切片渲染进 user prompt 中专用的 `PROVISIONED_EVIDENCE` 小节（位于观测
  列表与输出契约之间，保持"契约在末尾"的不变量），并以
  `workflow_step.evidence_provisioned` 审计事件记录来源 ref、供给字节数
  与 artifact 总字节数，使大 artifact 的供给覆盖率保持可查询。
- 需求像工具计划一样被校验：来源必须解析到活动计划的持久化 ref；必需
  来源缺失时阶段被阻断（fail closed）。profile 可将某条需求标记为可选。
- 每阶段总量由新的 `runtime` 配置键
  `workflow_stage_evidence_max_bytes`（默认 8000）钳制。

转换的消费者（即今天依赖 P1 豁免的那些）：

- `document.edit` —— `select_edit_operation` 与 `document_edit` 阶段改为
  声明已完成的 `document_locate_evidence` outcome 作为供给证据，而不再
  依赖超大的 `files.read` 信封。
- `browser.interaction` / `browser.automation` —— 选择或校验元素 ref 的
  阶段声明当前代次的已稳定快照 artifact，使用 `structured` 切片。
- 最终合成 —— `workflowFinalEvidence` 成为供给消费者：document-read 内容
  保持既有 8000 rune 预算，非文档回退在同一总预算内装入**多条**观测，
  而非恰好一条（修复 P5）。

### 3.3 `observation.read`

按上下文组装方案 1.2 的规格执行，实质不变：参数 `artifact_uri`（必填）、
`offset`、`max_bytes`；read 风险、无需审批；会话内不透明键；注册于
`internal/toolhub/registry.go`，registry 一致性测试自动覆盖；在合格模型步骤 scope 中
通过冻结的通用 `SupportRequirements` 选择，旧持久化 plan 不会被扩大。其结果经过同一个
统一信封返回，请求窗口作为证据摘录。默认每阶段允许执行两次 read；它们计入 observation
byte，但不计入 business tool-call 或重复调用预算。

### 3.4 滚动观测压缩

滚动压缩在 36,000 byte 启动，把合格的较旧条目压为单行类型化状态（tool、status、
关键字段、artifact ref），最新两条永不压缩，顺序保持，以
`workflow_step.observations_compacted` 审计前后字节数。48,000 byte 最大值优先检查，
达到后不再尝试压缩而直接停止。统一信封后最坏情形有界
（32 次调用 × 约 2.4 KB ≈ 77 KB），压缩因此是长 run 的常规路径而非悬崖，
且因 3.3 提供读回而无损。

### 3.5 ContextBuilder 整合

供给证据注册为独立可降级小节和独立预算，排在当前 run 观测与固定输出契约之间。
一个 builder 强制执行通道显式配置的 `max_input_tokens`；未配置时则使用物理
`context_tokens` 减去输出额度。它依次降级命名 variant，并只对明确声明的 section
做 UTF-8 安全截断。每个获准 prompt 都低于该输入阈值；固定 section 超限会在模型
调用前失败。

### 3.6 跨 run 上下文有意保持索引定位

会话上下文的裁剪（8 条消息 × 360 字符、封顶的工具摘要、紧凑变体）对其
历史索引角色是正确的，不予放大。追问质量的提升来自方案 1.3（模型生成的
情节摘要）与既有的确定性解析器（recent-document 解析重读文件而非信任
摘要），而不是跨 run 携带更多正文。

## 4. 移除内容

- `toolResultObservationBudget` 中按工具名的豁免分支，连同一切使信封超过
  `observation_summary_max_bytes` 的路径。
- `workflow_run_max_observation_bytes` 与单条信封尺寸之间的耦合。
- 最终合成的单条观测回退。

## 5. 配置与审计面

| 键（`runtime` 段） | 默认值 | 本重构后的角色 |
|---|---|---|
| `observation_summary_max_bytes` | 2400 | 唯一的信封尺寸旋钮，适用于所有工具 |
| `workflow_stage_evidence_max_bytes` | 8000 | 新增：每阶段供给证据上限 |
| `workflow_stage_max_observation_reads` | 2 | 每阶段允许执行的 support read 数量 |
| `workflow_run_observation_compaction_bytes` | 36000 | 启动滚动压缩 |
| `workflow_run_max_observation_bytes` | 48000 | 压缩前优先检查的硬停止线；不再放大信封 |

新增审计事件：`workflow_step.evidence_provisioned`、
`workflow_step.evidence_blocked`、`workflow_step.observations_compacted`。
既有事件
（`workflow_step.prompt_compressed`、`workflow_step.budget_stopped`、
`structured.message_truncated` 标记）语义不变；`budget_stopped` 频率预期
下降，并成为回归信号。

## 6. 兼容性

- 旧的放大信封写入的持久化 run 原样加载；读取路径不做重新规范化
  （normalize-on-read 是本仓库已知的存储反模式）。
- artifact 归档（`store.ArchiveToolObservation`）不变。artifact store 接口
  新增有界读取，由 filesystem 与 S3-compatible 后端实现；unsupported 后端
  继续显式报错。
- 当前模型工具 Profile revision 会冻结 support requirement；旧持久化 plan 恢复时保持原 scope。

## 7. 风险

- **本地模型可能用不好 `observation.read`。** 已接受：主内容通道是确定性
  供给；`observation.read` 是逃生通道，由标准化的 `next_step_hint` 提示。
- **结构化切片绝不能产出不可用的碎片**（被切断的元素 ref 比更小的完整
  列表更糟）。切片按 artifact 类别实现并逐类测试。
- **供给改变文档与浏览器行为。** 两者都在默认 `file` 后端上以场景运行与
  golden eval 验证之后，才移除豁免（见交付顺序——供给先落地、移除随后，
  不存在丢失证据的中间态）。
- **更大的供给切片抬高 prefill 成本。** 由
  `workflow_stage_evidence_max_bytes` 约束，并被已实现的稳定前缀排序
  （方案 0.3）抵消。

## 8. 验证

按工程基线与重构 playbook：先记录完整测试基线（评判 `internal/toolhub`
前先运行 `npm run setup:document-tools`），随后逐阶段：

- 单元：所有工具（含 `pdf.extract_text` / `images.inspect`）的统一信封
  上限；需求解析、fail-closed 行为与逐 artifact 类别的切片；压缩的触发/
  顺序/字节记账；registry 一致性自动覆盖 `observation.read`；最终合成的
  多观测装填。
- 场景（默认 `file` 后端）：长 PDF 读取 + 编辑（此前被 P1 饿死）、16+ 步
  文档/浏览器 run 在无 `workflow_step.budget_stopped` 下完成、带供给快照
  的浏览器 ref 点击流程、经 `observation.read` 恢复被截断证据。
- Golden eval 与第 5 节的审计检查。
- 性能：dual-light 配置下的逐步 prefill 测量，记录于
  [模型基线](../benchmarks/model_baseline.md)。

## 9. 交付顺序

按主题分提交；机械移动与行为变更绝不混在一起。顺序是承重的：供给必须先
于豁免移除落地，否则文档/浏览器判断会失去证据通道。

1. `observation.read`（独立；使压缩变为无损）。
2. 证据供给：阶段上下文需求、解析、切片、prompt 小节、审计；转换
   `document.edit`、浏览器 r2 阶段与最终合成（含 P5 的多观测修复）。
3. 移除大预算豁免（小的行为变更；解决 P1 与 P3）。
4. 滚动压缩（行为变更；依赖第 1 步）。
5. ContextBuilder 整合与供给证据小节（先机械整合，再接预算）。
6. 情节摘要按上下文组装方案独立推进。
