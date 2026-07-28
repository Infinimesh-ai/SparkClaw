# 文档编辑 Operation 选择节点

> 语言： [English](../../docs/document-edit-operation-selection.md) | 简体中文

本设计记录已于 2026-07-27 实施：把文档编辑器 operation 的选择从 fast 二次路由调用中移出，改为放在
证据定位之后的专用 `select_edit_operation` workflow 节点。它修订
[文档 Workflow](document-workflows.md)，把 `document.edit` 从 revision 3 升级到
revision 4。

Revision 5 保留同一四节点 Plan，但把 `document_locate_evidence` 标记为 `direct_once`：
Runtime 在任何 Deep operation 选择调用前，使用冻结 path 直接且只调用一次按格式限定的
reader，模型不再决定是否执行定位读取。

## 问题

`document.edit` revision 3 只有一个 `document_edit` 节点、两个 stage。结构化读取
（`read_for_edit`）完成后，scope transition 激活 `edit_by_type`，工具 materialization
按冻结格式检索目录中所有已注册 editor。由于该视图包含多个条目（DOCX 有
`replace_text`、`replace_paragraph`、`insert_paragraph`、`delete_paragraph`、
`set_text_style`），Runtime 在 `workflowDirectorySelection` 内用一次内联的
`ChatWithProfile("fast", ...)` 调用消解歧义。

这个 fast 二次路由有三个结构性缺陷：

1. **能力档位错误。** 区分 replace 与 insert 是基于 owner 请求加完整结构化 observation
   的语义判断。fast lane 多次在请求（"修改/完善/润色"已有内容块）要求 `replace_*` 时
   选成了 `insert_*`。
2. **证据不足。** 选择 prompt 只拿到裁剪至 6000 字符的紧凑 observation 摘要，而不是
   deep 执行器随后能看到的定位证据。
3. **对 plan 不可见。** 选择发生在工具 materialization 内部：没有 plan 节点、没有独立的
   attempt 上限、没有专属 audit 轨迹，冻结 plan 也无法表达"选择必须先于 mutation"。

## 决定

operation 选择成为冻结 plan 中的一等**决策节点**，由 Workflow Runtime 在 workflow 执行
model lane（`deep`）上执行，位于证据定位之后、editor stage 之前。`document.edit` 的
fast 二次路由路径被关闭：如果 edit 节点在没有持久化决策的情况下进入 materialization，
workflow 显式 block，而不是回退到 fast 模型调用。

### 新 plan 形态（`document.edit` revision 4）

```text
confirm_document_target        （确定性）
  -> document_locate_evidence  （evidence：结构化读取 + 定位）
  -> select_edit_operation     （decision：精确选择一个已注册 editor）
  -> document_edit             （evidence：经所选 editor 的有界 mutation）
```

- `document_locate_evidence` 承接原 `read_for_edit` stage：通过格式限定的
  `document.read` capability 读取冻结的受治理 path，在 `content_available` 时完成。
- `select_edit_operation` 是使用新完成规则 `decision` 的新节点。它的 capability scope
  与 editor stage 相同（格式限定的 `document.edit` requirement），候选边界保持冻结，
  且与随后 materialize 的边界完全一致。
- `document_edit` 保留 `edit_by_type` stage、argument binding、approval 风险和校验行为，
  但从自身节点的 scope revision 1 开始，而不再继承 transition 后的 scope。

### 决策节点契约

`CompletionDecision`（`"decision"`）是新的 `NodeGoal` 完成规则，并带有自己的 plan
校验规则：

- 必须声明非空的初始 capability scope（候选边界）；
- 不得声明 transition、argument binding 或 stage capability 规则；
- 绝不 materialize 模型可见的步骤工具——由 Runtime 直接消解；
- 必须依赖至少一个 evidence 节点，确保定位证据先于选择存在。

Runtime 在计算下一个执行 hint 之前消解处于 active 状态的决策节点：

1. 用节点的冻结 scope 检索工具目录（与其他节点相同的 `ExposureRequest` audit 路径）。
2. 零候选：block workflow（`no registered editor matches the requested document
   change`）。
3. 单候选：确定性选中，不发起模型调用（例如文本编辑只注册了 `replace_text`）。
4. 多候选：在 `deep` profile 上发起 attempt 有界的 `workflow_operation_selection`
   模型调用。prompt 携带 owner 请求、节点 goal、依赖 evidence 节点在更大预算下的
   完整结构化 observation，以及合格条目。严格的单字段 `{"entry_id":"..."}` 输出契约
   与最小改动语义（修改/完善/润色 → replace；除非目标不存在或明确要求新增，否则绝不
   insert）沿用已退役 fast prompt 的规则。当冻结视图仍有候选时，空选择会被审计并携带
   明确反馈重试；只有重复空选择才以无匹配 editor block。
5. 选中条目作为 outcome reference（`kind=tool_directory_entry`，带 capability、格式、
   operation 属性）持久化到决策节点上，节点完成，editor 节点激活。无效模型输出消耗
   一次 attempt；`MaxAttempts` 耗尽则 block workflow。

消解过程发出既有的 `tools.directory.selected` audit 事件（actor `workflow-decision`）
和新增的 `workflow.decision_resolved` 事件，并追加
`workflow_stage: edit_operation_selected operation=...` observation，让 deep 执行器
知道冻结了哪个 operation 及其原因。

### 消费决策

`workflowDirectorySelection` 能识别依赖决策节点的节点：持久化的
`tool_directory_entry` reference 是唯一可接受的选择，它必须仍然存在于当前目录视图中；
reference 缺失或有歧义是硬错误。通用 Fast 模型 fallback 已删除。现在只有
`MaterializeAll`、精确持久化决策和确定性单候选三种目录选择路径；其他多候选 scope 会
fail closed，并必须新增显式决策节点。

## 稳定性

- operation 选择在 deep lane 上进行，看到的证据与执行器一致，并被冻结格式 scope 约束。
- 选择结果被持久化、可审计、attempt 有界且强制生效：edit 工具调用必须匹配已决策条目，
  否则被既有的 materialized-boundary 校验拒绝。
- `document.edit` 不会再静默退化到 fast 二次路由；每个歧义要么经决策节点消解，
  要么显式 block。
- 单候选格式完全跳过模型调用，新节点不给文本编辑增加任何延迟。

## 实施记录

重构已分五个可评审步骤落地；每一步都保持构建与 `internal/agent` 测试套件为绿。

### 1. 契约（`internal/app` 与 plan 校验）

- `internal/app/workflow.go`：新增 `CompletionDecision CompletionRule =
  "decision"`。
- `internal/agent/workflow_plan.go`（`validateWorkflowPlan`）：决策节点必须声明非空的
  冻结 `InitialScope.Requirements`，不得声明 transition、argument binding、stage
  capability 规则或 `MaterializeAll`；必须依赖至少一个 `CompletionEvidence` 节点。

### 2. Profile（`internal/agent/workflow_profiles.go`）

- `documentEditProfile.Revision()` 3 → 4。
- `Resolve()` 产出上文的四节点 plan。原 `document_evidence_resolved` scope
  transition 移除；`document_edit` 直接以 edit scope 从 `edit_by_type` 起步，保留
  path/`output_path` binding、允许风险与 `MaxAttempts: 2`；read binding 移到
  `document_locate_evidence`。
- `Assess()` 改按 `outcome.NodeID` 而非节点 stage 分派：locate +
  `content_available` → 完成（`document_evidence_located`）；edit +
  `edit_completed` → 完成（`document_edit_completed`）；其余 block。
- `Hint()` 依据 `state.ActiveNodeIDs[0]` 判定 `inspect`/`modify`；原
  `TransitionInstruction` 文案退役，由决策完成 observation 取代。
- profile 实现新的可选接口
  `workflowDecisionSemantics { DecisionRules(app.WorkflowNode) []string;
  DecisionResolvedInstruction(app.ToolDirectoryEntry) string }`，承载来自已退役
  fast prompt 的 replace-vs-insert 最小改动规则。

### 3. Runtime 决策执行器（新文件 `internal/agent/workflow_decision.go`）

- `activeWorkflowDecisionNode(state)` 与
  `resolveActiveWorkflowDecisions(ctx, *run, profile)`；当唯一 active 节点是决策节点时
  Runtime 调用消解器。
- 消解流程：在节点冻结 scope 下检索目录（沿用 `tools.directory.searched` audit）；
  零候选 block；单候选不发模型调用直接选中；多候选最多发起节点 attempt 上限次数的
  `ChatWithProfile(ctx, "deep", ...)` `workflow_operation_selection` 调用。
- prompt 使用 `WORKFLOW_OPERATION_SELECTION_REQUEST` 头与 owner 请求段；mock-router
  注入通道为 `MOCK_OPERATION_SELECTION_RESPONSE`，输出使用严格的
  `parseWorkflowDecisionSelection` 契约。依赖节点 observation 以 20000 字符预算进入
  prompt（`workflowDirectoryEvidence` 的泛化）。
- attempt 记账使用 `node.MaxAttempts`；无效输出会重试，冻结视图仍有候选时空
  `entry_id` 也会重试。重复空选择以 `no_registered_editor_matches` block，其他无效
  输出耗尽 attempt 后以 `edit_operation_selection_invalid` block。
- 完成时持久化 outcome reference（`kind=tool_directory_entry`，属性
  capability/format/operation/via），经 `activateReadyWorkflowNodes` 激活后继节点，
  发出 `tools.directory.selected`（actor `workflow-decision`）与
  `workflow.decision_resolved`，并返回
  `workflow_stage: edit_operation_selected operation=...` observation。

### 4. 消费与 fail-closed 接线

- `internal/agent/workflow_directory_selection.go`：
  `workflowDirectorySelection` 对带决策依赖的节点改用持久化的
  `tool_directory_entry` reference；reference 缺失、有歧义或不再合格是硬错误。
  旧 Fast selector 及其 6000 字符紧凑证据路径已删除；意外多候选 scope 直接 fail closed。
- `internal/agent/workflow_registry.go`（`materializeActiveWorkflowTools`）：
  active 决策节点直接报错——必须先消解，绝不 materialize。
- `internal/agent/workflow_runtime.go`（`runWorkflowWithSeedAndStream`）：每个
  stage 的状态检查之后消解 active 决策；追加返回的 observation 并把消解视作一次
  transition；消解导致 block 时经既有 `workflowBlockedMessage` 路径退出。
- `internal/agent/workflow_dispatcher.go`（`resumeMatchedWorkflow`）：计算 hint 与
  materialize 之前先消解 active 决策，使定位与选择之间的崩溃可恢复。

### 5. 测试与文档

- 更新 `workflow_preflight_test.go`（`advanceDocumentEditToEditor` 把 read 调用记到
  `document_locate_evidence`，随后调用消解器；edit 节点 scope revision 2 → 1；
  模型调用断言改为 `deep` 上的 `workflow_operation_selection`），以及
  `document_edit_workflow_test.go`、`message_control_routing_test.go`、
  `web_workflow_test.go` 中的节点 ID / scope revision 引用。
- 新增覆盖：deep lane 多候选选择；单候选文本编辑断言零次选择模型调用；空
  `entry_id` 重试后 block；决策缺失时 materialization fail-closed；无效输出重试后
  block；plan 校验拒绝缺 evidence 依赖或缺 scope 的决策节点。
- [文档 Workflow](document-workflows.md) 及其英文原文现已更新到 revision 4 并回链本记录。

验证：在 `services/gateway` 下执行 `go build ./...` 与
`go test ./internal/agent/ ./internal/app/`（2026-07-27 已记录基线为绿），并跑 CI
workflow 中的文档语言镜像检查。

## 范围之外

- 不新增 editor operation、格式或保真规则。
- `document.read`、browser、weather、schedule 与 conversation workflow 保持既有 plan；
  当前 scope 都通过 `MaterializeAll` 或单一精确候选解析，不需要模型目录 fallback。
- intent router 的第一遍路由（`task_hint`、capability 匹配）不变；本设计只移除文档编辑
  workflow 内部的第二跳 fast 路由。
