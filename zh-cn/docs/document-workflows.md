# 文档 Workflow

> 语言： [English](../../docs/document-workflows.md) | 简体中文

本文档描述当前结构化文档读取与编辑 pipeline，替代第一阶段 structured-enrichment 设计记录，
同时保留长期有效的格式、证据和保真契约。

## Workflow 边界

`document.read` revision 2 读取或总结一个明确的受治理 workspace 文件。
`document.edit` revision 4 读取一个明确文件，通过显式 Workflow 决策节点解析一个受支持
operation、为 reversible edit 获取 approval，并写入新的同级
`<name>-sparkclaw-edit.<ext>` 输出副本。

输入输出 path 都是确定性 binding，模型不能替换。path 必须位于配置 workspace 内，解析成
regular non-symlink file，并同时通过 extension 与 file signature/package type 检查。
已有输出绝不覆盖。

每个文档 Workflow 都从 `confirm_document_target` 开始。确定性 preflight 已经选择唯一的
持久 document ID、受治理 path、格式、provenance 和来源 ID；该节点先把这些证据写入
`OutcomeRefs`，然后才激活读取或编辑节点。因此它是真实状态迁移，不是 prompt 指令或装饰性
Plan entry。

编辑 Plan 固定为：

```text
confirm_document_target
  -> document_locate_evidence
  -> select_edit_operation
  -> document_edit
```

`select_edit_operation` 不会向 ReAct 暴露工具。Runtime 直接检索它按格式限定的
`document.edit` scope：单候选确定性选中；多候选由一次有重试上限的 Deep 模型决策处理，
输入是 owner 请求和最多 20,000 字符的依赖证据。选中的 directory entry、capability、
format、operation 与选择路径写入该节点的 `OutcomeRefs`，编辑节点只能 materialize 这一
entry。决策缺失、过期、有歧义或无效时 Workflow 会显式 block。原 Fast 目录二次路由已经
删除；其他多候选 scope 也必须声明自己的决策节点。详见
[operation 选择设计记录](document-edit-operation-selection.md)。

## 持久文档记录

`DocumentRecord` 是受治理文档的一等身份与活动记录，保存稳定 ID、owner/session 范围、
受治理 path、名称、content type、格式、可获得的 size/hash、status、来源 message/run/tool
ID、可选 parent document ID，以及最近 activity ID/time。memory、file snapshot 和
PostgreSQL Store 实现相同契约。

附件在 owner message 持久化后立即登记，早于解析。确定性 preflight 丰富记录；成功读取更新
其活动；每个成功编辑产物都创建一条新记录，并通过 `parent_document_id` 关联输入。split
operation 的全部产物共享同一 activity ID，因此后续引用解析会把这一组保持为 ambiguous。

文档身份和 provenance 必须持久化。解析文本、摘要、layout enrichment 和其他派生
representation 刻意不属于 `DocumentRecord`：它们可以不完整、作为 tool observation 归档、
被替换或重新生成。

## Pipeline

```text
记录或解析持久文档身份
  -> inspect 受治理 path 和 format
  -> 持久化 confirm_document_target 证据
  -> 通过 small_file_v1 high-level adapter 解析
  -> normalize 为 structured_document_v1
  -> enrich 支持的 evidence category
  -> 按需归档/投影可替换的解析证据
  -> 用结构化 observation 完成 document_locate_evidence
  -> 在冻结 format scope 内消解 select_edit_operation
  -> 持久化一个精确 tool_directory_entry 决策
  -> 只 materialize 已决策的 format/operation editor
  -> Policy approval
  -> 写入新输出
  -> 重新读取并校验目标修改与保真
```

DOCX/PPTX 使用 Python high-level library，XLSX 使用 ExcelJS，PDF 使用 Python PDF tooling，
文本使用 native adapter。项目不声明拥有完整 OOXML/PDF object model。

## 结构化 Representation

规范化记录以稳定 location 分离 content、layout、asset、annotation 和 chart category。
`document_enrichment_v1` 在支持范围内增加 Fast image semantic 和有界 layout evidence。
模型生成的 image/OCR observation 始终标记 `untrusted` 并保留 provenance。

完整 tool observation 可以为可追溯性归档，模型上下文只接收带 category、anchor、priority
和有界文本的选定 segment。文档身份不要求解析 representation 被精确保存。category budget
防止 image semantic/OCR 挤掉主要文档内容，重复图片按 source hash 去重。当前图片限制和
budget 由代码与测试约束，修改它们属于契约变化。

## 当前 Operation

| 格式 | 支持的 edit operation |
|---|---|
| Text | `replace_text` |
| DOCX | `replace_text`, `replace_paragraph`, `insert_paragraph`, `delete_paragraph`, `set_text_style` |
| XLSX | `replace_text`, `update_cell`, `insert_row`, `delete_row`, `update_row`, `append_row` |
| PPTX | `replace_text`, `add_slide`, `update_slide`, `duplicate_slide`, `delete_slide` |
| PDF | `extract_pages`, `delete_pages`, `rotate_pages`, `split` |

`pptx.update_slide` 有两个显式 layout policy：

- `preserve` 修改准确文本并保留 geometry；文本无法保持可读时拒绝。
- `coordinated` 可以调整已验证 companion background 和 peer body column，报告全部 layout
  change；仍无法容纳时拒绝输出。

不支持的 asset、annotation、chart、animation、SmartArt internal、macro、tracked change、
scanned-PDF OCR 和 package extension 可以作为 partial evidence 读取，但不是隐式 mutation target。

## Mutation 安全

- Image semantic 可以辅助定位，但不能单独授权 edit。
- 每次 mutation 都必须匹配持久化 operation 决策、选中的 format/operation schema 和冻结
  path。
- 原文件 SHA-256 必须不变。
- 输出通过同一 normalize pipeline 重新读取。
- 校验 expected after-value 和 operation-specific delta。
- 已知 evidence-only asset、annotation 和 layout fingerprint 必须保留，除非 operation 明确允许变化。
- 任何未报告或无关变化返回 `preservation_mismatch`，并删除非法生成输出。
- 不支持的 category 报告为 `unknown` 或 `partial`，不虚假标记为 preserved。

## 扩展规则

1. 暴露 editor 前先扩展 format inspection 和 high-level parsing。
2. 为新 evidence 增加稳定 location 和有界 context projection。
3. 按准确 format+operation 注册 editor，不暴露 generic document mutation tool。
4. 定义 operation-specific argument binding、approval risk、delta allowlist 和 post-edit verification。
5. 测试 malformed package、path escape、output conflict、model-derived evidence、preservation failure 和成功 reread。
6. 用户可见 operation 变化时更新 [Workflow 能力矩阵](workflow-capabilities.md)。

核心契约位于 `internal/document`；ToolHub 负责具体 adapter 和 Fast image enrichment；Workflow
Runtime 负责 staged tool exposure、binding、Policy 和最终 `WorkflowResult` projection。
