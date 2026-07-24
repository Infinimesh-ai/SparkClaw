# 文档 Workflow

> 语言： [English](../../docs/document-workflows.md) | 简体中文

本文档描述当前结构化文档读取与编辑 pipeline，替代第一阶段 structured-enrichment 设计记录，
同时保留长期有效的格式、证据和保真契约。

## Workflow 边界

`document.read` revision 1 读取或总结一个明确的受治理 workspace 文件。
`document.edit` revision 2 读取一个明确文件、解析一个受支持 operation、为 reversible edit
获取 approval，并写入新的同级 `<name>-sparkclaw-edit.<ext>` 输出副本。

输入输出 path 都是确定性 binding，模型不能替换。path 必须位于配置 workspace 内，解析成
regular non-symlink file，并同时通过 extension 与 file signature/package type 检查。
已有输出绝不覆盖。

## Pipeline

```text
inspect path 和 format
  -> 通过 small_file_v1 high-level adapter 解析
  -> normalize 为 structured_document_v1
  -> enrich 支持的 evidence category
  -> 持久化完整 representation
  -> 构建有界 context segment
  -> 选择一个 format/operation editor
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

完整 representation 被持久化，模型上下文只接收带 category、anchor、priority 和有界文本的
选定 segment。category budget 防止 image semantic/OCR 挤掉主要文档内容，重复图片按 source
hash 去重。当前图片限制和 budget 由代码与测试约束，修改它们属于契约变化。

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
- 每次 mutation 限制在选定 format/operation schema 和冻结 path 内。
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
