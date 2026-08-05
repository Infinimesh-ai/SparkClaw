# DOCX 编辑

> 语言： [English](../../docs/docx-editing-optimization.md) | 简体中文
>
> 状态：现行行为。本文描述已经交付的 DOCX 读取、编辑、审批、保真和评测契约。

## 范围

SparkClaw 支持五种需要审批的 DOCX operation：

| Operation | 当前可编辑范围 |
|---|---|
| `replace_text` | 正文顶层段落中的精确文本 |
| `replace_paragraph` | 一个正文顶层段落 |
| `insert_paragraph` | 文档首尾，或一个正文段落的前后位置 |
| `delete_paragraph` | 一个正文顶层段落 |
| `set_text_style` | 一个正文段落的内置样式、粗体和字号 |

Header、footer、table、hyperlink、image、field、comment 和不支持的 OOXML
part 会按下文说明读取或盘点，但不会隐式成为 mutation target。Table cell、header/footer、
footnote/endnote、text box、tracked change、field、drawing、image replacement 和其他未注册
mutation 会在 operation 选择阶段 block，不会 materialize 替代 editor。

SparkClaw 不使用关键词路由、第二份 capability catalog、通用文档 mutation 工具或模型拥有的
资源路径。语义图、Workflow profile、ToolHub directory、Policy 和 Approval 是唯一权威。

## Workflow 边界

每次 DOCX 编辑都经过同一条 staged path：

```text
semantic fusion
  -> document.edit revision 5
  -> confirm_document_target
  -> document_locate_evidence（direct_once files.read）
  -> select_edit_operation（持久化一个 directory entry）
  -> document_edit（只 materialize 已选 editor）
  -> Policy 与 Approval
  -> 重新验证源文件和 target
  -> 新的同级输出副本
  -> 重新读取并执行保真校验
  -> completed WorkflowResult
```

Runtime 冻结受治理的输入路径和下一个可用的同级输出路径；模型不能替换任一路径。定位 reader
只执行一次，其归档后的结构化 observation 是 operation 选择和 mutation binding 的唯一证据源。
原文件始终保持不变。

## 结构化读取与 Coverage

DOCX reader 暴露正文段落、table cell、run span、hyperlink、image、section layout、comment，
以及去重后的 header/footer story part。正文与 story block 都有稳定 location，例如
`document.p[25]` 或包含 story-part 身份的 header/footer path。跨 section 共享的 linked
header/footer 按 package part identity 只表示一次，同时保留全部 section reference。

每个段落 run 都报告 parser 可见的文本 offset 和格式：

```json
{
  "index": 1,
  "text": "季度总结",
  "bold": true,
  "italic": false,
  "underline": null,
  "font_name": "等线",
  "font_size_pt": 18.0,
  "font_color": "1F4E78",
  "effective_bold": true,
  "effective_font_size_pt": 18.0,
  "relationship_id": "",
  "boundaries": []
}
```

显式值和 effective value 保持分离；缺失或继承值不会根据渲染文本猜测。

只有 package inventory 没有发现规范化 representation 之外的含文本内容时，reader 才报告
`coverage.content = complete`。Coverage 会分别报告 body、table、header、footer、footnote、
endnote、text box 和 tracked change。Footnote、endnote、text box、tracked change、
`altChunk`、content control、nested table 以及无法识别的 Word 文本 part，在 parser 尚未表示
它们时会保留在 `content_omissions` 和 `unparsed_parts` 中。它们会使内容或受影响 story scope
标记为 `partial`/`unsupported`，绝不会静默变成 complete。

## Text Style 契约

`docx.set_text_style` 接受 strict `style` object，其中至少包含一个属性：

- `builtin_style`；
- `bold`，包括显式 `false`；
- 闭区间 `1..200` 内的整数 `font_size_pt`。

未知属性、空 style、缺失 target、`paragraph_index` 与 `location` 冲突、非正文 location 或
非法字号都会在 Policy 前失败，且不创建 approval。

编辑后，系统通过同一 reader 重新打开输出。Validator 忽略大小写比较内置样式，要求目标段落
每个非空 run 都匹配请求的粗体和字号，保留未请求属性，并验证文本与 location 未改变。任何
不一致都会返回 typed preservation failure，并移除生成的输出。

## 证据绑定的 Mutation

Approval 前，Runtime 只从同一 run、session、node、scope revision 和受治理 path 中已完成的
`document_locate_evidence` 调用派生一份 binding。Binding 持久化源 tool call、源 node、
run/session、operation、输入 SHA-256 和适用的 target 或 boundary 证据。模型提供的证据字段
只有与当前证据相等时才接受。

| Operation | 绑定证据 |
|---|---|
| `replace_text` | 输入 SHA-256、精确 match location/hash、预期数量 |
| `replace_paragraph` | 输入 SHA-256、段落 location/hash、可选精确 old text |
| `insert_paragraph` before/after | 输入 SHA-256 与 anchor location/hash |
| `insert_paragraph` start/end | 输入 SHA-256 与对应 document boundary |
| `delete_paragraph` | 输入 SHA-256、段落 location/hash、规范化 before text |
| `set_text_style` | 输入 SHA-256、段落 location/hash、编辑前格式 fingerprint |

证据缺失、冲突、跨 run、跨 session、node 过期、path 错误或 target 有歧义时，会在 Approval 前
失败。Approval 通过后、adapter 执行前，Runtime 会重新加载持久化 call，重新计算受治理文件
SHA-256，再次解析 target，并比较当前 hash 和 before value。等待审批期间源文件发生变化时，
系统不会调用 editor，也不会留下输出。

## Run 级保真

文本替换会把逻辑段落 offset 映射回最小受影响 run span。Match 位于单个 run 内时只 splice
该 run；跨 run match 只有在所有受影响文本 run 具有相同格式 fingerprint 和 relationship
ownership 时才允许。混合格式、跨 hyperlink relationship、field、drawing、tracked change
和其他不支持边界都会显式失败，不会压平段落。

整段替换会保留 paragraph property，并且仅在源文本 run 格式同质时复用源 run 格式。混合格式
段落会被拒绝。输出保存并重新打开后，validator 比较未受影响的 run 文本、run 格式、paragraph
property、hyperlink/relationship、field 和 image；只有请求的文本或 style delta 可以改变。

成功编辑报告 `high_level_preservation = verified` 和 `original_unchanged = true`。任何无关的
parser 可见变化都会返回 `preservation_mismatch`，并删除输出副本。

## 目标感知 Decision Evidence

由 `workflow_stage_evidence_max_bytes` 配置的 `Runtime.StageEvidenceMaxBytes` 是唯一证据预算，
默认值为 8,000 bytes。Decision 和 editor stage 不再携带第二份 DOCX 专属 byte 或 rune 限制。

对结构化 DOCX read，decision projector 按以下顺序排列完整 JSON record：

1. source metadata、format、coverage、truncation 和 parser stats；
2. 显式 `document.p[N]`、英文 `paragraph N`、中文 `第 N 段`、route-bound location 和引号文本；
3. 匹配 block，以及同一 story 中前后各最多两个 neighbor；
4. 有代表性的 header/footer story block 和合格 operation context；
5. 没有显式 anchor 时的确定性正文 head/tail 样本；
6. 预算仍有空间时，按稳定文档顺序加入剩余 record。

Projector 只排序已有证据，不能选择 operation，也不能授权 mutation target。任何 byte ceiling
都使用同一排序，只打包完整 UTF-8 JSON record。首条 record 报告选中/遗漏数量、遗漏正文范围、
精确使用字节数和触发优先级的 anchor。缺少结构化 DOCX map 的旧归档 observation 继续使用有界
通用 evidence fallback。

## 确定性评测

合并门禁包含：

- 每个受支持 style field 的 strict schema 与保存/回读测试；
- 五种 operation 的当前证据 binding、cross-run 拒绝和审批等待期间源变化；
- 覆盖粗体、斜体、下划线、字体、颜色、混合 run、hyperlink、field、drawing、image、缩进、
  间距和 coverage 的真实 DOCX fixture；
- 长文档末尾 target、中英文 anchor、前部无关重排、无 anchor head/tail、story part、UTF-8，
  以及 8K/4K/2K projector 用例；
- 五种 operation 的中英文 route/selection 用例；
- read vs edit、删除段落 vs 删除文件、create vs edit、browser vs 本地文档混淆对；
- 不支持的 header、table cell、footnote 和 tracked-change mutation，确保无 approval block；
- 一条使用默认 file store 的真实 owner path，覆盖 route、direct read、operation selection、
  approval pending、批准执行、输出回读、preservation、Workflow resume、附件结果与 state reload。

这些测试使用确定性 mock model。真实模型 calibration 只是可选补充证据，不是正确性前提。

Document tool setup 后运行聚焦和全量门禁：

```bash
npm run setup:document-tools
cd services/gateway
go test ./internal/document ./internal/toolhub ./internal/agent ./internal/modelrouter ./internal/semanticrouting
go test ./...
go vet ./...
go build ./...
```

## 当前边界

- DOCX mutation 只能作用于正文顶层段落。
- Header/footer 内容可进入 decision evidence，但保持只读。
- Table-cell edit、footnote/endnote edit、接受 tracked change、image replacement 和任意 OOXML
  mutation 均未注册。
- 跨 run 格式保真仅覆盖 parser 可见 OOXML property；不支持边界会显式失败或报告 partial
  coverage。
- 大文档 retrieval/indexing 与这份有界 decision projection 相互独立，本次 DOCX editor 不新增
  大文档策略。

Owner component 契约见[文档 Workflow](document-workflows.md)，用户可见 operation 见
[Workflow 能力](workflow-capabilities.md)。
