# 意图路由工具暴露契约

> 语言：[English](../../docs/intent-routing-tool-exposure-contract.md) | 简体中文

本文是[路由重构方案](intent-routing-workflow-refactor-plan.md)中渐进式 Tool Directory 检索和 schema 物化的权威契约。Profile 与 transition 语义由[工作流 Profile 目录](intent-routing-workflow-domain-profiles.md)定义。

## 注册权威

一条 ToolHub 注册记录同时拥有执行器、schema、capability 描述、可信目录文本、risk/effect 和 outcome adapter。逻辑目录在内存中从启用的注册记录派生，既不是第二份 manifest，也不是 ToolHub meta-tool。

```go
type ToolDefinition struct {
    // Existing execution and schema fields remain.
    Capabilities  []CapabilityDescriptor `json:"capabilities"`
    OutcomeAdapter ToolOutcomeAdapter     `json:"outcome_adapter"`
    Directory     ToolDirectoryMetadata  `json:"directory"`
}

type ToolDirectoryMetadata struct {
    Summary      string       `json:"summary"`
    WhenToUse    string       `json:"when_to_use"`
    WhenNotToUse string       `json:"when_not_to_use,omitempty"`
    InputKinds   []TargetKind `json:"input_kinds,omitempty"`
    OutputKinds  []OutputKind `json:"output_kinds,omitempty"`
    Effects      []ToolEffect `json:"effects"`
}

type ToolOutcome struct {
    ID         string          `json:"id"`
    ToolCallID string          `json:"tool_call_id"`
    NodeID     WorkflowNodeID  `json:"node_id"`
    Status     string          `json:"status"`
    Signals    []OutcomeSignal `json:"signals,omitempty"`
    Refs       []ResourceRef   `json:"refs,omitempty"`
    Retryable  bool            `json:"retryable,omitempty"`
}

type NodeAssessment struct {
    OutcomeID  string           `json:"outcome_id"`
    NodeID     WorkflowNodeID   `json:"node_id"`
    Status     AssessmentStatus `json:"status"`
    Signals    []OutcomeSignal  `json:"signals,omitempty"`
    ReasonCode string           `json:"reason_code,omitempty"`
}
```

`ToolOutcome` 只报告客观执行事实，不声明用户目标已经完成。Profile 专属 assessor 将结果与冻结的 `NodeGoal` 比较并生成 `NodeAssessment`。因此同一个 outcome 可以满足一个 Profile，同时让另一个 Profile 请求更多证据。

## 收束接口

Agent Runtime 只有一个工具可见性接口：

```go
type ToolExposure interface {
    Search(context.Context, app.ExposureRequest) (app.DirectoryView, error)
    Materialize(context.Context, app.MaterializeRequest) (app.ExposureView, error)
}

type ExposureRequest struct {
    RunID         string         `json:"run_id"`
    WorkflowID    WorkflowID     `json:"workflow_id"`
    NodeID        WorkflowNodeID `json:"node_id"`
    ScopeRevision int            `json:"scope_revision"`
    ActorRef      string         `json:"actor_ref"`
    Limit         int            `json:"limit"`
}

type ToolDirectoryEntry struct {
    ID            ToolDirectoryEntryID `json:"id"`
    Capability    CapabilityDescriptor `json:"capability"`
    Summary       string               `json:"summary"`
    WhenToUse     string               `json:"when_to_use"`
    WhenNotToUse  string               `json:"when_not_to_use,omitempty"`
    Effects       []ToolEffect         `json:"effects"`
    Risk          RiskLevel            `json:"risk"`
    RelevanceRank int                  `json:"relevance_rank"`
}

type MaterializeRequest struct {
    ViewID        string                 `json:"view_id"`
    RunID         string                 `json:"run_id"`
    WorkflowID    WorkflowID             `json:"workflow_id"`
    NodeID        WorkflowNodeID         `json:"node_id"`
    ScopeRevision int                    `json:"scope_revision"`
    EntryIDs      []ToolDirectoryEntryID `json:"entry_ids"`
    ActorRef      string                 `json:"actor_ref"`
}
```

caller 不能提交 `CapabilityScope`、`NodeGoal` 或 outcome 文本。`Search` 加载已持久化 Run，校验 workflow/node/scope revision，并从冻结计划与节点状态取得活动 scope 和 goal。

eligibility 必须先于 relevance 计算：

```text
LOAD active scope and node goal from persisted WorkflowState
MATCH enabled ToolDefinitions by capability name and qualifier subset
FILTER node allowed risks and scope denied effects
FILTER Policy.MayExpose using actor/workflow/node context
RANK trusted compact registration descriptions
RETURN a bounded DirectoryView without full schemas
```

语义排序只能重排 eligible set。Skill 正文、TaskHint candidate、工具 observation 和模型输出都不能添加目录项。requirement qualifier map 必须是注册 capability qualifier 的子集，因此增加 qualifier 只会收窄 eligibility。

每个目录项精确对应一条具体注册定义和一个匹配 capability。工具名在物化前只存在于 Runtime 内，模型只看到精简目录项。只有结构化过滤后恰好剩一项时才允许自动物化。零项明确 blocked；多项时模型只能选择已返回的 ID。

## View 绑定

`ViewID` 是覆盖完整目录视图的不透明 HMAC。Runtime 只保留每个 run/node 的最新视图；`Materialize` 再次校验 actor、workflow、node、scope revision、entry membership、当前注册和 `Policy.MayExpose`。未知、过期、进程重启前或 view 外的选择必须明确失败。

`WorkflowState` 持久化 `DirectoryViewRef` 只用于审计与恢复，不作为可复用授权 token。重启后 Runtime 在相同持久化 scope 内重新执行 `Search`，不会接受旧 `ViewID`，也不会把旧 entry 静默映射到效果已经变化的工具。

目录选择是 Runtime 控制动作，不是 ToolCall，也不是授权决策。产生的 `ToolCall` 必须绑定 workflow ID、node ID、scope revision 和 capability。执行前，参数感知 Policy 仍然评估准确 definition、arguments、actor 和 resources；只有该决策可以允许、要求审批或拒绝。

## 结果驱动扩展

Outcome adapter 由注册元数据选择，不使用按工具名分发的 router switch。它只产生类型化 signal 与受治理 resource ref，不把任意 payload 复制到工作流状态。assessor 只能激活冻结计划中已经存在的 `ScopeTransition`。激活会改变 scope、递增 `ScopeRevision`、清空旧目录选择并重新执行 `Search`，不能直接插入工具。

已应用 outcome ID 和每条 transition 的激活次数都要持久化。重复 outcome 是 no-op；未声明 signal 或次数耗尽必须明确 blocked。每个 revision 上，可见性都继续小于授权范围。

## 当前已迁移目录项

| Capability | 边界 | Definition |
|---|---|---|
| `web.discovery` | URL 未知时查找公共来源，不执行页面交互 | `web.search` |
| `web.page.read` | 读取已知或已选 URL 获取来源证据，不执行可见交互 | `browser.read` |
| `workspace.file.search` | 在已配置 Workspace root 内搜索文件名与有界文本，不包含修改或知识索引语义 | `files.search` |
| `workspace.file.read` | 读取一个确定性 Workspace path，不包含发现或修改 | `files.read` |

已迁移 Web 与 Workspace 切片不读取 TaskHint candidate、Skill allow/deny 清单、fallback 工具清单或 observation 字符串扩展。明确 URL 与 path 参数执行前必须通过冻结 `ArgumentBinding` 校验。未迁移领域只是旧路径的过渡调用者，不能成为迁移 Workflow 失败后的 fallback。
