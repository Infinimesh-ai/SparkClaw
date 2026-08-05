# 文档 Workflow

> 语言： [English](../../docs/document-workflows.md) | 简体中文

本文档描述当前结构化文档读取与编辑 pipeline，替代第一阶段 structured-enrichment 设计记录，
同时保留长期有效的格式、证据和保真契约。

## Workflow 边界

`document.read` revision 4 读取、总结一个明确的受治理 workspace 文件，或逐字提取图片内原文。其格式限定 reader
是 `direct_once` 节点：Runtime 使用冻结路径直接调用唯一 reader，Fast 只根据已完成证据生成
最终回答。`document.edit` revision 6 读取一个明确文件，通过显式 Workflow 决策节点解析一个受支持
operation、为 reversible edit 获取 approval，并写入新的同级
`<name>-sparkclaw-edit.<ext>` 输出副本。如果该名称已存在，preflight 会选择第一个可用的
带编号同级名称，例如 `<name>-sparkclaw-edit-2.<ext>`。继续编辑其中一个副本时，会沿用同一
编号序列。两个文档 Profile 拥有的所有模型调用当前都使用 Fast；非文档 Workflow 仍默认使用 Deep。

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

`document_locate_evidence` 是 `direct_once` 节点。Runtime 使用冻结 path 直接调用唯一一个
按格式限定的 reader，且只调用一次；定位前不存在模型 `action | final` 步骤。该调用产生的
结构化 observation 是 operation 选择使用的唯一证据。读取失败会直接 block，不会回退到
另一次读取。

在模型驱动的工具 stage 中，模型在调用已 materialize 工具前返回 `final` 属于协议违例，
不代表完成。Runtime 会返回一次限定在当前 stage 的纠正提示；如果模型再次提前返回
`final`，则以 `required_tool_not_called` 阻断当前节点，不再发起第三次模型调用。

`select_edit_operation` 不会向步骤模型暴露工具。Runtime 直接检索它按格式限定的
`document.edit` scope：单候选确定性选中；多候选由一次有重试上限的 Fast 模型决策处理，
输入是 owner 请求、operation 专属目录边界，以及由
`workflow_stage_evidence_max_bytes` 限制的依赖证据，默认 8,000 bytes。DOCX decision 会
优先选择显式稳定 location、段落序号、引号文本、有界 neighbor、story-part 样本和
operation context，再使用确定性 head/tail fallback。XLSX 的 operation 选择与 editor
参数生成使用同一份 `xlsx_sheet_evidence_v1` 投影。选中的 directory entry、capability、
format、operation 与选择路径写入该节点的 `OutcomeRefs`，编辑节点只能 materialize 这一
entry。决策缺失、过期、有歧义、不受支持或无效时 Workflow 会显式 block。原内联目录二次
路由已经删除；其他多候选 scope 也必须声明自己的决策节点。详见
[operation 选择设计记录](document-edit-operation-selection.md)。

对于 PPTX 编辑，确定性 grounding 会在读取前冻结一个类型化 scope：`single_slide`、
`whole_deck`、`exact_text` 或 `structural`。英文、阿拉伯数字及中文页序数都会规范化为
稳定的 1-based 页码。若请求没有明确指定某一页、整份演示文稿、精确替换文本或结构动作，
则在 mutation 前要求澄清；SmartArt、动画、图表数据、幻灯片母版和宏编辑会作为不支持的目标阻断。
该 scope 只把决策目录收窄到对应的精确 operation，不会新建第二条 route，也不会让
grounding 直接选择 operation。

## 持久文档记录

`DocumentRecord` 是受治理文档的一等身份与活动记录，保存稳定 ID、owner/session 范围、
受治理 path、名称、content type、格式、可获得的 size/hash、status、来源 message/run/tool
ID、可选 parent document ID，以及最近 activity ID/time。memory、file snapshot 和
PostgreSQL Store 实现相同契约。

附件在 owner message 持久化后立即登记，早于解析。确定性 preflight 丰富记录；成功读取更新
其活动；每个成功编辑产物都创建一条新记录，并通过 `parent_document_id` 关联输入。单个编辑
产物会保留为“继续修改刚才编辑好的文件”等后续请求的最近文档候选，其身份、血缘、来源和
`edited` 活动都会投影到路由上下文。只有当前请求通过语义路由选中文档 Workflow 时才会绑定
该候选；无关对话不会继承它。split operation 的全部产物共享同一 activity ID，因此后续引用
解析会把这一组保持为 ambiguous。

文档身份和 provenance 必须持久化。解析文本、摘要、layout enrichment 和其他派生
representation 刻意不属于 `DocumentRecord`：它们可以不完整、作为 tool observation 归档、
被替换或重新生成。

## Pipeline

```text
记录或解析持久文档身份
  -> inspect 受治理 path 和 format
  -> 持久化 confirm_document_target 证据
  -> 无模型步骤地调用一次已绑定 reader
  -> 通过 small_file_v1 high-level adapter 解析
  -> normalize 为 structured_document_v1
  -> enrich 支持的 evidence category
  -> 把成功的扫描页 OCR 提升为 PDF page block 和正文
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
文本使用 native adapter。扫描 PDF 页会通过有界 `pypdfium2`/Pillow 资源栅格化并交给
可选 OvisOCR2 adapter。项目不声明拥有完整 OOXML/PDF object model。

## 结构化 Representation

规范化记录以稳定 location 分离 content、layout、asset、annotation 和 chart category。
`document_enrichment_v1` 在支持范围内增加 Fast image semantic、可选 OvisOCR2 Markdown
和有界 layout evidence。OvisOCR2 是专用 document adapter，不是 Model Router lane；Fast
仍负责视觉描述和 Workflow 推理。成功的扫描页 OCR 会提升到对应稳定 PDF page/block，
公式和表格 markup 保留在 Markdown 证据中。模型生成的 image/OCR observation 始终标记
`untrusted` 并保留 model-call provenance。

直接检查图片时，`images.inspect` 会并行运行可选 OCR 与 Fast 视觉理解。清理后的非空
Markdown 设置 `text_detected=true`，并与 Fast layout/semantic 证据一起保留；清理后为空则
设置 `text_detected=false`，且不输出任何 `ocr_*` 字段。OCR 禁用或失败时通过
`ocr_status` 明确呈现，失败还带有有界 warning。在文档上下文中，成功且非空的 OCR 是逐字
文本来源，因此 Fast semantic segment 不再重复 `Visible text`；OCR 禁用、失败或为空时仍由
Fast 文字提取兜底。

完整 tool observation 可以为可追溯性归档，模型上下文只接收带 category、anchor、priority
和有界文本的选定 segment。文档身份不要求解析 representation 被精确保存。category budget
防止 image semantic/OCR 挤掉主要文档内容，重复图片按 source hash 去重。当前图片限制和
budget 由代码与测试约束，修改它们属于契约变化。

### XLSX 证据

XLSX 读取把类型化 cell value 与 display text、formula、number format、style、hidden state 和
merge anchor 分开规范化。每个 sheet、row 和 cell 都有基于修改与保真字段的稳定 source hash。
有界 `xlsx_sheet_evidence_v1` 投影始终声明 source completeness、selection completeness 和省略的
sheet/row/cell 数量；它优先保留显式命名的工作表、A1 cell 与行、精确值、表头行、末尾上下文和
目标相邻行。如果已定位的强制目标无法放入证据预算，则 `selection_complete=false` 会阻断选择或
编辑，而不是改用不相关的工作簿前缀。

完整结构化读取继续保存在 tool observation artifact 中；模型上下文只接收面向当前消费者的有界
投影，证据供给 audit 会记录选中与省略数量。

### PPTX 证据

PPTX 读取还会为可编辑顶层文本形状暴露稳定的 slide/template 与 layout 引用，以及有界的
paragraph/run 树。该树记录段落层级、项目符号、对齐与间距，以及受支持的 run 字体、颜色、
语言和 hyperlink 属性。含 field 的文本和 group child 会明确标记为不可编辑。对于按页
限定的编辑，8,000-byte operation 证据投影会先放入目标页所有必需记录，再放可选 layout
inventory；它排除不可编辑形状且绝不截断记录。缺失或超出预算的必需证据会直接阻断，
不会截断目标或要求模型猜测。

### PDF 页级覆盖与 OCR Runtime

每个 PDF 页面在选择 OCR 前都经过确定性的 `pdf_native_text_quality_v1` 策略分类。
最终页面状态为 `native`、`ocr_succeeded`、`ocr_disabled`、`ocr_failed`、
`render_failed` 或 `budget_omitted`；`ocr_pending` 只作为 parser 中间状态。混合页面
分别保留 native 和 OCR block，只有规范化文字完全相同时才合并。

PDF read 会输出 `read_complete`、`coverage_status`、`page_status_counts` 和排序后的
`missing_page_indexes`。只有全部页面为 native 或 OCR 成功时读取才完整。有可用证据的
partial read 只能在明确说明限制后总结；unavailable read 会 block。finalizer 单独接收
coverage manifest 和有界 8000 rune 正文摘录，因此摘录截断不会被误报为源页面丢失，
缺失页面也不会被摘录预算隐藏。

公共 adapter 状态会区分 `configured_enabled`、`adapter_ready` 和 `runtime_status`
（`disabled`、`ready` 或 `degraded`），同时隐藏 OCR endpoint 与 allowlist。`ready` 只表示
构造成功，不表示 provider 已预热或未来请求必然成功；fresh-call health 单独报告。

经校验的 OCR success 和 no-text result 使用进程内、owner 隔离的 LRU cache，固定限制为
128 个 entry 和 32 MiB。无 path 的 key 包含准备后内容 hash、配置 provider/model，以及
prompt、preprocessing 和 normalization version。同一 owner/key 的并发 miss 会合并，
瞬时失败不缓存。fresh call 会持久化 operation=`document_ocr` 的真实 `ModelCall`；cache
hit 复用其 provenance，不创建虚假调用。Audit/trace metadata 不包含 OCR 正文；
`/metrics` 提供有界 OCR page/cache/duration/queue 指标和 PDF classification/coverage
指标，不把正文或 run identifier 作为 label。

## 当前 Operation

| 格式 | 支持的 edit operation |
|---|---|
| Text | `replace_text` |
| DOCX | `replace_text`, `replace_paragraph`, `insert_paragraph`, `delete_paragraph`, `set_text_style` |
| XLSX | `replace_text`, `update_cell`, `insert_row`, `delete_row`, `update_row`, `append_row` |
| PPTX | `replace_text`, `add_slide`, `update_slide`, `update_deck`, `duplicate_slide`, `delete_slide` |
| PDF | `extract_pages`, `delete_pages`, `rotate_pages`, `split` |

### XLSX 编辑边界

- `replace_text` 要求明确 old/new 文本，并修改匹配的文本值 cell；已定位 cell/row 或非文本
  typed value 使用结构化 editor。
- `update_cell` 只修改一个证据定位的 cell。`update_row` 只修改现有行已提供的行首前缀；省略的
  尾部 value、formula、format、comment、hyperlink、row height 和 hidden state 都不属于目标。
- `insert_row` 要求明确 before/after 行锚点；`append_row` 写在最后结构行之后；`delete_row`
  要求明确删除完整行，清空 cell、删除列或删除工作簿都不是行删除。
- 六个 entry 各有独立目录边界。否定编辑、仅引用指令、仅排障、目标歧义和不支持的 operation
  返回空 entry 并阻断，不执行修改。

每次 XLSX 编辑都会在 Policy 与 Approval 前绑定到当前 run 唯一已完成的定位读取。Runtime
拥有 `source_sha256` 以及适用的 `source_cell_hash`、`source_row_hash` 或
`source_sheet_hash`，规范化 sheet 名和 A1 地址，并在不创建 approval 的情况下拒绝冲突或物理
过期证据。

XLSX 修改还受 package gate 约束。OOXML inspector 会在编辑前校验 content type、relationship、
package part、feature class 和 opaque hash。table、chart、conditional formatting、data
validation、pivot、slicer、external link、connection、embedded object、custom XML、macro、
signature、protection、encryption、calculation chain（`calc_chain`）、unknown part 和其他未
验证特性会阻断修改。目标 sheet 的 insert/delete 在 formula、merged range、comment、
hyperlink 或 image 需要未经验证的锚点/引用重写时也会阻断；读取仍可执行，并明确报告
partial coverage。

成功写入后，只允许证据绑定的 worksheet 以及必要时的 `sharedStrings.xml` 变化；package part
集合、content type、relationship graph、无关 worksheet、style、theme、workbook property 和
opaque part 必须保持 hash。输出会重新读取并校验类型化或结构化目标修改；成功返回
`package_preservation=verified` 和已检查 feature class。任何未声明差异返回
`preservation_mismatch` 并删除输出副本。

### PDF Transform 边界

每个物化的 PDF transform 都使用 strict operation-specific schema。页码数组必须非空、
唯一、为从 1 开始的正整数；rotation 只能是 `-270`、`-180`、`-90`、`90`、`180`
或 `270`；`split` 拒绝 page、rotation 和 input 字段。无关字段与 qualifier 冲突会在
审批前失败。由于尚无有序多文档 grounding 和多 parent lineage，`merge` 不注册。

`pptx.update_slide` 有两个显式 layout policy：

- `preserve` 修改准确文本并保留 geometry；文本无法保持可读时拒绝。
- `coordinated` 可以调整已验证 companion background 和 peer body column，报告全部 layout
  change；仍无法容纳时拒绝输出。

PPTX 文本 mutation 支持 `exact_span` 和 `rewrite_shape`。精确区间替换会保留未受影响的
run，并在跨 run 替换时重新分配文本而不压平段落。形状重写会保留 paragraph skeleton 和
受支持的 run style；`break_mode` 把显式换行映射为 PowerPoint soft break 或 paragraph。
含 field 的目标会 fail closed。编辑后验证会比较 paragraph/run 树及 hyperlink 目标，因此
未请求的格式丢失会成为 preservation mismatch。

Runtime 独占全部确定性布局决策。在 `coordinated`
策略下，同组 text box 使用一致的所需高度与字体，已验证 background 随 body text
增长，贯穿卡片高度的 accent bar 也随 card background 延伸。系统根据整页修改前后证据
报告每一项 geometry、font 或 `word_wrap` 变更。如果结果文本仍无法容纳、companion
关系不一致，或修改后的 shape 会越过相邻内容或 slide canvas，则编辑显式失败。模型只
选择由证据绑定的 shape target 并填写 replacement text，不选择 layout value。

`pptx.update_deck` 会把一个有界 batch 作为单次原子编辑执行。当前上限为 12 页、64 个更新
形状及 32 KiB replacement text；任一陈旧目标或失败更新都会删除整个输出。
`pptx.add_slide` 必须使用当前读取中的一个 `layout_ref` 或 `template_slide_ref`，接受证据绑定
的插入位置，并能在同一次调用中克隆受支持的文本、group、图片、chart、hyperlink 与 package
relationship，同时应用 template text update。含 speaker notes 的模板或复制来源会被拒绝，
不会带损复制。结构编辑会重新计算物理页证据，并把陈旧 page-marker 文本报告为 warning，
不会隐式重写。

不支持的 asset、annotation、chart、animation、SmartArt internal、macro、tracked change 和
package extension 可以作为 partial evidence 读取，但不是隐式 mutation target。adapter 开启时，
扫描 PDF 会自动调用 OvisOCR2；页面栅格化、OCR 或现有预算不可用时，读取会通过
`coverage_status=partial` 或 `unavailable`、`scanned_unsupported=true` 和标准原因
精确报告缺失页面。

## Mutation 安全

- Image semantic 可以辅助定位，但不能单独授权 edit。
- 每次 mutation 都必须匹配持久化 operation 决策、选中的 format/operation schema 和冻结
  path。
- 每个 DOCX mutation 都会从唯一已完成的 `document_locate_evidence` read 绑定当前输入
  SHA-256，以及精确 match、paragraph、anchor、boundary 或编辑前格式证据。缺失、冲突、
  来自无关节点、跨 run/session、path 错误或过期的 evidence 会直接阻断，不创建 approval。
  Approval 通过后，Runtime 会在 adapter 执行前重新计算文件版本并再次解析绑定 target。
- XLSX editor 同样要求当前工作簿与目标 hash；工作簿或目标变化会在 approval 前被拒绝。
- PPTX 的 slide、shape、old text、layout、template 和插入引用必须全部存在于当前 run 唯一一次
  已完成读取中。陈旧、不可编辑、分组、跨 scope 或含 notes 的 clone 目标会在创建 approval
  记录前阻断。
- 原文件 SHA-256 必须不变。
- 输出通过同一 normalize pipeline 重新读取。
- 校验 expected after-value 和 operation-specific delta。
- DOCX 文本替换会保留 parser 可见的 run 与 paragraph 格式；混合格式或不支持的
  relationship/field/drawing/tracked-change 边界会显式失败，不会压平内容。
- 已知 evidence-only asset、annotation 和 layout fingerprint 必须保留，除非 operation 明确允许变化。
- 任何未报告或无关变化返回 `preservation_mismatch`，并删除非法生成输出。
- 不支持的 category 报告为 `unknown` 或 `partial`，不虚假标记为 preserved。

所有已注册 PPTX edit operation 共用一个 125,000 ms 端到端工具 deadline，子进程继承调用方
剩余时间中更短的 deadline。超时会删除部分输出，并映射为稳定的
`document_operation_timeout` tool error。reader 和 mutation adapter 超时会保留 `read` 与
`apply` stage 证据。精确区分 `reread` 与 `preserve` 仍需共享 document Pipeline 支持；发生在
PPTX adapter 之外的 parent deadline 当前会保守地报告为 read-stage operation timeout。

## 扩展规则

1. 暴露 editor 前先扩展 format inspection 和 high-level parsing。
2. 为新 evidence 增加稳定 location 和有界 context projection。
3. 按准确 format+operation 注册 editor，不暴露 generic document mutation tool。
4. 定义 operation-specific argument binding、approval risk、delta allowlist 和 post-edit verification。
5. 测试 malformed package、path escape、output conflict、model-derived evidence、preservation failure 和成功 reread。
6. 用户可见 operation 变化时更新 [Workflow 能力矩阵](workflow-capabilities.md)。

核心契约位于 `internal/document`；`internal/documentocr` 负责有界 OvisOCR2 HTTP contract；
ToolHub 负责具体 reader 以及 Fast/OCR enrichment；Workflow Runtime 负责 staged tool exposure、
binding、Policy 和最终 `WorkflowResult` projection。
