# 文档结构树与分层展开设计

> 语言： [English](../../docs/document-structure-tree-design.md) | 简体中文

状态：T1–T5 已在本地源码实现，2026-11-09。本文扩展
[统一文档结构设计](document-structure-and-evaluation-design.md)，与
[2026-11-09 位置与视觉评估](document-workflow-evaluation-20261109.md)分开评估。

## 1. 问题与范围

读取范围的质量有两个相互独立的维度：

1. **结构供给**：程序能否把源文件划分成清晰、真实的结构，并以模型可导航的方式提供。
2. **模型选择**：模型能否从该结构中选对范围，并识别已读内容是否不足。

本文只处理第一个维度。只有标题的目录导航信号太少；内嵌正文的目录会超出阶段预算，
而且仍要求能力有限的模型做一次全局选择。本设计从源文件构建一棵结构树并逐层提供，
让每一步都成为有界的局部选择，从而同时避开这两种问题。

读后充分性检查、信息需求拆分、候选生成和相关性扫描属于第二个维度，不在本文范围内；
第 8 节只预留它们需要的 facts。

## 2. 已核实的源码基线

基于 `296b3389` 观察：

| 方面 | 当前行为 | 后果 |
|---|---|---|
| 两套结构 | `index.Outline`（[chunk.go](../../services/gateway/internal/docindex/chunk.go)）保存带 chunk 与页范围的标题/书签章节；`index.Nodes`（[structure.go](../../services/gateway/internal/docindex/structure.go)）保存平铺的 canonical 单元，`ParentID` 仅由标题层级推出 | PDF 书签只存在于 `Outline`；模型可见的 PDF 节点是没有层级的页 |
| 模型可见目录 | `OutlinePage`（[traversal.go](../../services/gateway/internal/docindex/traversal.go)）按源顺序遍历所有节点，清空 `Text`/`Values`，按 `MaxOutlineNodes = 200` 或字节预算分页 | 模型看到的是开头的若干段落，常常只是 `document.p[N]` 定位符文本，后面的章节必须线性续页才能发现 |
| 旧消费方 | 检索的章节命中、`section` 选择器和 `SummaryUnits` 读 `index.Outline`；`include_descendants` 和 source locations 读 `index.Nodes` | 同一文档存在两种不一致的“章节” |
| DOCX 标题 | `heading_level`（[docx_index.py](../../services/gateway/internal/toolhub/scripts/docx_index.py)）只认 `Title`、`Heading N` 样式和 `w:outlineLvl` | 用加粗或编号正文段落充当标题的文档没有结构树 |
| PDF 标题 | `pdf_index.py` 通过 pypdf 读取书签；以页为单元 | 无书签即无层级；没有基于版面的标题证据 |
| 文本与幻灯片 | Markdown 识别 `#` 标题；纯文本没有；PPTX 幻灯片和 XLSX 工作表是一级单元 | 纯文本以及较长的幻灯片/工作表是平铺的 |

[structure.go](../../services/gateway/internal/document/structure.go) 中
`StructureSchemaVersion` 为 `1`，`StructureParserVersion` 为 `native_structure_v1`。

## 3. 目标与非目标

目标：

- 每个源版本只有一棵 canonical 树：每个叶子按源顺序恰好属于一个容器，无空缺、无重叠。
- 结构只按原生证据划分；没有原生结构的范围降级为带标注的物理分组。本阶段程序不推断标题。
- 第一次 outline 调用在有界预算内显示整份文档的顶层形状；后面的章节不依赖线性分页。
- 任一容器均可按需展开。内容信号来自预算允许时每个容器附带的有界抽取首句，以及最底层容器的
  有界叶子预览，而不是倾倒正文。
- 选中容器后通过现有 range 路径读取其完整子树。

非目标：

- 不调用模型构建或标注结构，不生成章节标题。
- 本阶段不根据加粗、字号或编号模式推断标题（第 5.1 节）。
- 不改变 coverage、冻结范围和 continuation 契约。
- 不新增 UI、传输或存储模式；已持久化的 revision 保持原有行为。
- 不声称更好的结构树能让模型的范围选择变得正确。

## 4. 树契约

### 4.1 节点类别

| 类别 | 类型 | 作用 |
|---|---|---|
| 根 | `document` | 每个源版本一个 |
| 容器 | `section`、`bookmark`、`sheet`、`slide`、`slide_section`、`table_region`、`page_group`、`group`、`front_matter` | 导航与选择单元 |
| 叶子 | `paragraph`、`heading_label`、`table`、`row`、`shape`、`page` | canonical 内容；文本/值契约不变 |

标题段落成为容器，其自身的标签叶子保留原生文本和定位符。叶子节点 ID 沿用现有派生方式，
其中包含 schema 和解析器版本：同一版本内稳定，第 7 节升级版本后全部改变。容器 ID 由源
hash、schema 版本、类型、首个叶子和深度派生。

### 4.2 容器字段

`StructureNode` 增加可选字段，叶子不携带。

| 字段 | 含义 |
|---|---|
| `level` | 相对根的树深度 |
| `structure_source` | `native_style`、`outline_level`、`markdown_heading`、`pdf_bookmark`、`slide_section`、`slide`、`sheet`、`physical` |
| `span` | 首/末叶子块、页范围和序号范围 |
| `stats` | 子容器数、叶子数、文本字节、估算 token、表格数、不可用/未识别叶子数 |
| `children_complete` | 当前 generation 下是否已知全部子节点 |
| `lead` | 可选的抽取预览：容器第一个正文叶子的首句，按字符数设上限，标注为预览；仅按第 6.3 节规则 4 附带 |

标题仍为原生文本，受 `maxHeadingRunes` 限制。`group` 或 `page_group` 的标题是
`p.12–18`、`document.p[200]–p[260]` 这样的事实范围标签，不使用编造的措辞。

### 4.3 不变量

运行时构建时校验，不满足则拒绝该树：

1. 先序遍历的叶子顺序等于源顺序，且每个 canonical 单元恰好覆盖一次。
2. 容器范围等于其子节点范围之并。
3. 第一个顶层容器之前的内容归入 `front_matter`。
4. 同名标题保持为不同容器，各有定位符。
5. 物理容器不得成为原生容器的父节点。

`index.Outline` 变为该树面向旧消费方的投影；同一 generation 不会出现两套章节划分。

## 5. 结构划分

每种格式使用有序的原生证据梯度。较低一级只在较高级别对该范围没有提供结构时生效，并记录来源。
物理分组是所有格式的最后一级。

| 格式 | 梯度 |
|---|---|
| DOCX | `Title`/`Heading N` 样式和 `w:outlineLvl` → 物理分组 |
| PDF | 书签映射为页范围 → 页组 |
| Markdown | `#` 标题 → 物理分组 |
| 纯文本 | 以空行块为边界的物理分组 |
| PPTX | `p:sectionLst` 节 → 幻灯片 → 标题与形状作为叶子 |
| XLSX | 工作表 → 行区间分组；表格区域暂缓（第 10 节） |

### 5.1 本阶段不推断标题

程序不根据加粗、字号、`第X章`、`一、`、`1.1` 等编号文本或列表编号，把正文段落提升为标题。
这类文档得到物理分组，由首句和叶子预览展示其开头内容。推断留给之后单独的设计，依据评估中
原生结构缺失的实际频率决定；若采用，必须带来源标注、受阈值约束，并从属于原生结构。OCR 不产生标题。

### 5.2 物理分组

直接叶子数超过展开预算的容器，按叶子边界和字节大小切成连续的 `group` 子节点。分组不切断
表格或工作表行。共享表头、合并锚点和原生标题可以位于其他分组；每次内容读取都补齐其显式
依赖闭包，不重复分配规范叶子的归属。没有可用标题的 PDF 使用 `page_group` 容器。它们明确是导航窗口
而非章节，与统一设计第 4.2 节一致。

## 6. 提供方式：分层展开

### 6.1 `document.outline` 请求

新增可选参数（仅 revision 9 投影）：

| 参数 | 含义 |
|---|---|
| `node_id` | 展开该容器；默认为根 |
| `depth` | `node_id` 以下请求的深度，由运行时封顶 |
| `cursor` | 继续同一容器、同一 generation 下较宽的子节点列表 |

已持久化的 revision 以及不传这些参数的直接调用方，保留原有线性分页。

### 6.2 响应

- 被请求的容器及其祖先路径，随后按广度优先列出其子树。
- 每个容器：`id`、`kind`、`title`、`level`、`structure_source`、`span`、`stats`、`expandable`、
  `children_complete`，以及按第 6.3 节规则 4 附带的 `lead`。
- 在没有子容器的容器处，列出其叶子及有界的原生预览（文本前若干字符、表格表头行），
  标注为预览。预览不计为内容获取，也不计入 coverage。
- 与现在相同的 `source_sha256`、`structure_generation`、`next_cursor`，以及 scope 为
  `structure_discovery` 的 coverage。

### 6.3 预算规则

1. 结构预算为 Fast 阶段证据预算（`StageEvidenceBytes`）的四分之一。该比例可配置，写入响应，
   并根据评估结果复核。
2. 广度优先：一层完整输出后才输出更深一层。首次调用只在下一整层放得下时自动加深。
3. 若连第一层都放不下（例如数百张幻灯片），运行时在该层之上插入绑定源文件的 `group` 导航窗口直到放下。
   窗口是连续兄弟节点的视图，不成为原生容器的新规范父节点；窗口 ID 选择同一完整叶子区间。
   模型总能在一次调用中看到整份文档的范围。
4. 只有整层连同首句都放得下时，该层才附带首句；否则该层所有容器都不带首句，不会只给部分
   容器附带。附带首句优先于继续加深下一层。
5. 叶子预览先统一按叶子截短，再考虑丢弃叶子；被丢弃的叶子列表对同一容器返回 cursor。
6. 首句和预览均不计为内容获取，也不计入 coverage。

### 6.4 读取选中容器

`document.read_range` 使用 `node_ids` 加 `include_descendants=true` 时已经沿 `ParentID` 展开。
容器节点加入该关系后，选中章节、分组或工作表即可在现有冻结选择器、cursor 和 coverage
规则下读取完整子树及其依赖。`section` 选择器通过 `index.Outline` 的树投影解析。

## 7. 版本与兼容

- `StructureSchemaVersion` 升为 `2`，解析器版本改为 `structure_tree_v1`。新树使用独立的 `-tree-v1` 缓存目录。
  版本 1 的索引、generation 和 cursor 继续供已持久化旧版任务使用；跨版本 cursor 明确拒绝，
  不会静默 rebase。
- `document.read` revision 9 在指令和投影中使用树展开。revision 4–8 保持已持久化行为，
  其存储的 generation 不 rebase 到新树。
- `SummaryUnits` 保持单元数上限，但改为以树的顶层容器为输入，使书签容器和物理容器
  无需新的总结器即可改善总结。
- 检索的章节命中和 source-location relations 改为读取树；返回的 ID 仍是可直接复制的精确节点 ID。
- 工具名和工具策略键不变。

## 8. Workflow 集成边界

revision 9 只改变结构发现：

- 结构阶段与现在一样直接调用根 outline。
- 范围阶段可以在任何内容读取之前用 `node_id` 调用 `document.outline`；展开调用不冻结
  `task_mode` 或选择器。
- 运行时把展开路径（`expanded_node_ids`）和结构预算降级情况记录到 `document_read_` facts。
  第二维度的机制将使用这些 facts；本设计不增加它们的决策。

## 9. 验证

结构质量用确定性检查验证，不依赖模型：

| 检查 | 夹具 |
|---|---|
| 树与预期层级及来源一致 | 有样式的 DOCX；不同父节点下的同名标题；有/无书签的 PDF；带节的 PPTX；多工作表 XLSX |
| 不产生推断容器 | 只有加粗/编号标题的 DOCX 和含 `一、/1.1` 的纯文本只得到物理分组 |
| 首句按层统一且有界 | 在配置比例下的宽层与窄层 |
| 第 4.3 节不变量 | 全部夹具，以及 `test-files/` 下现有的 owner 文件副本 |
| 首次调用可见全文范围 | 长平铺 DOCX、300 页幻灯片、500 页 PDF |
| 展开有界且完整 | 每个容器的 cursor 遍历恰好返回每个子节点一次 |
| 选中容器读取完整子树 | 章节、分组、工作表和幻灯片选择 |
| 旧行为不变 | revision 4–8 冷恢复，以及现有 outline/search/summary 测试集 |

在现有评估 harness 上的真实模型运行，只作为新结构树下范围选择的观察记录，与上述检查
分开，不作为“结构工作修复了模型选择”的证明。

## 10. 实现阶段

| 阶段 | 内容 | 主要文件 |
|---|---|---|
| T1 | 树契约、容器字段、不变量、`Outline` 投影 | `document/structure.go`、`docindex/structure.go`、`docindex/chunk.go`、`docindex/index.go` |
| T2 | 第一批格式梯度：DOCX 样式/`outlineLvl`、Markdown 标题、PDF 书签入树、PPTX 节、XLSX 工作表、所有格式的物理分组 | `toolhub/scripts/*_index.*`、`toolhub/document_index_extract.go`、`docindex/structure.go` |
| T3 | 分层 `document.outline`、预算规则、首句、预览、容器读取 | `docindex/traversal.go`、`toolhub/document_retrieval*.go` |
| T4 | revision 9 profile 与 facts；旧消费方迁移到树 | `agent/document_read_profile.go`、`agent/document_read_scope.go`、`docindex/summary.go` |
| T5 | 确定性夹具与检查；双语文档 | `docindex`/`toolhub` 测试、`eval/document-workflow/` |
| 后续 | 带表头行的 XLSX 表格区域；标题推断仅通过单独设计引入 | `toolhub/scripts/xlsx_index.js`、adapters |

## 11. 风险与待定问题

- 只用加粗或编号正文充当标题的文档，以及没有书签的 PDF，本阶段只得到物理分组，首句和预览
  是其唯一的内容信号；在做任何推断设计之前，需要先测量这类情况的出现频率。
- 首句是开头一句而非摘要：对以套话开头的章节可能信息量很低。它只是导航辅助，不作为证据。
- 分层展开会增加发现调用次数。首次自动加深和现有尝试预算对其设有上限，实际往返成本
  需要在评估 harness 上测量。
- 更清晰的结构树不保证选择正确；第 1 节已把这部分留给另一份设计。


## 12. 实现与验证记录

树构建和不变量校验位于 `docindex/tree_build.go`、`tree_contract.go`，
`tree_outline.go` 实现受预算约束的广度优先视图。邻接关系、叶子序号和节点 ID 索引在构建或
加载时初始化一次，供读取方不可变共享。`index.Outline`、摘要单元、章节检索和源位置均从树
派生；摘要位置元数据保留原生容器，物理导航窗口不挤占摘要正文预算。

`runtime.document_structure_share_percent` 默认 25，1–100 以外的值回填为 25。响应记录有效
字节上限、比例和降级情况。展开深度上限为 8，单层交付上限为 200 项，首句/预览上限为
120 字符；物理分组以 24 个叶子或 16 KiB 为目标，同时保持表格和行完整。PDF 分组按页数
划分，不随 OCR 文本大小变化，因此补充识别不会移动已经选中的导航区间。原生深度超过 64
时明确拒绝。预算不足以容纳最小元数据或继续展开时返回错误，不返回自循环窗口或截断 JSON。
已受结构预算约束的树投影在模型压缩证据版本中保持完整。

revision 9 绑定 `structure_version=2`，并开放 `node_id`/`depth`；直接调用方可以通过这些参数
启用新契约。不传这些参数的调用方以及 revision 4–8 保留 v1 线性行为。
`document_read_expanded_node_ids` 记录每次展开，`document_read_structure_budgets` 记录预算
决策，`document_read_outline_node_id` 将叶子列表 cursor 绑定到所属容器。结构发现不冻结
内容模式，不将预览计入已获取叶子。没有 UI、传输或数据库 schema 变更。

确定性测试覆盖 DOCX 原生样式/大纲级别、重复标题、不推断标题、PPTX 节、XLSX 工作表/行边界、
Markdown 围栏和无损文本、PDF 原生书签/未识别页、300 页幻灯片、500 页 PDF、完整 cursor
遍历、选中子树读取、同页空书签、OCR 稳定性、小预算预览和摘要，以及 v4–8 FileStore 冷恢复。
使用 FileStore 的运行时测试完成根 → 容器展开 → 内容读取，并验证持久化 facts。独立复核发现的
空源、窗口进度、预览、摘要边界/元数据及重复扫描全文问题均已修正并复验。

2026-11-09 验证结果：

- 隔离 Linux `go test -json ./...`：82 个包通过，6,230 项测试/子测试通过，216 项条件性跳过，
  零失败；另有 12 个包没有测试。`go build ./...`、`go vet ./...` 通过。
- 补充边界用例后的最终 Linux 受影响包复测：`docindex`、`document`、`config`、`capability`、
  `toolhub`、`agent` 通过，共 1,801 项测试/子测试通过、11 项条件性跳过；全量 build/vet 通过。
- macOS 受影响包通过；结构树、文档读取和旧版恢复的聚焦 `-race` 检查通过。macOS 全量运行
  保留了修改前就存在的 `cmd/sparkclaw`、`browserhost`、`gateway` 临时路径/Host 准入失败；
  两处新增的版本预期失败已更新为 revision 9，并通过后续受影响包复测。
- CI Markdown 链接及语言镜像检查通过，共 144 篇源文档。

构建和展开编排函数把不变量/发布及广度优先预算事务保持在一起，长度超过常规 100 行函数
建议；格式适配、投影、导航索引、选择、校验和兼容辅助函数保持独立。

本工作区没有 `test-files/` 目录，因此未执行
owner 副本检查。本次未新增真实模型评估，不宣称模型范围选择已有改善。仅修改本地源码，
未推送、安装或部署。

## 13. Revision 10：模型视图与统一的规模口径

2026-11-09 对 revision 9 的评估发现，模型提示里装的是程序自己的记录。对一份 649 段 DOCX
做全文读取时，最终模型收到的是完整节点 JSON，共 169,100 个 prompt token，其中源文本只占约
7–8% 的字符；其余是 ID、依赖 ID 列表、父节点 ID、偏移、可用性、来源标记和重复的表格值。
outline 却只按正文字节把同一份文档估为约 21,000 token，导航和交付用的是两套口径。

Revision 10 把两种角色分开：

- **程序记录保持完整。** 工具输出、持久化 facts、cursor、覆盖率和执行证据继续保留节点 ID、
  哈希、偏移和可用性。记录“读了什么”的内容一项都不删。
- **模型读的是模型视图**（`document.ModelView`，`document_model_view_v1`）。原生标题渲染为带
  定位的 `#` 行，其他节点按原文顺序各占一行 `[原生定位] 正文`。依赖只出现一次，不属于选中
  内容时标记 `(context)`；被拆分的节点标记 `partial text bytes a-b`；不支持或不可用的节点写明
  状态和原因。类型值只保留会改变含义的事实（公式、缓存结果与是否重算、隐藏行列、合并来源），
  解析器内部字段和与可见文本重复的值被去掉。
- 这一视图用于最终答案证据、有界任务筛选批次（别名仍为 `n1`、`n2`，由运行时映射回节点）以及
  summary contract 5 的分章节输入。位置表保留 `block`、`parent_block`、范围、页码和标题，
  省去节点 ID、序号和结构来源。幻灯片视觉观察省去源文件/PDF/PNG 哈希和模型调用 ID，这些仍
  保留在工具输出里。
- outline 仍是导航界面。运行时绑定 `model_view` 后，其内容是缩进文本树。节点 ID 保留，因为它们
  是 `node_id`、`node_ids` 的句柄；父节点 ID、序号和为零的计数留在结构化输出里。

统一口径：outline 中每个容器报告 `read_tokens`，按其规范叶子的模型视图字节数
（`Index.ModelViewBytes`，随不可变导航索引一次性缓存）除以每 token 3 字节计算。结构树、统计和
generation 均不改变，因此 revision 9 的 cursor 和输出继续有效。确定性测试验证：整篇估算等于
整篇读取实际交付的字节数；子树估算加上作为上下文携带的祖先标题，等于该子树读取的交付字节数。

revision 10 的第一轮评测暴露了一个 JSON 视图里同样存在的位置歧义。文档根节点不是标题，但它的
定位取首个源块，所以顶层标题报告的 `parent_block=document.p[1]` 与首个标题（例如“第21节”）的
block 相同。有一份答案据此推断后面的清单属于该节。在 `model_view` 下，位置表不再把根节点当作
父级，而是标记 `parent_block=none(top-level heading)`；位置关系也明确说明顶层标题不属于任何
父标题。不带 `model_view` 的调用保持原来的位置表和关系。

兼容性：revision 9 冻结为旧版 profile，继续绑定 summary contract 4 和 JSON outline；revision
4–8 不变。Revision 10 绑定 contract 5（缓存键 `document_section_summary_v5`）。最终答案预算、
截断标记和任务筛选触发条件含义不变，只是改为按实际交付的视图计量。最终答案的字节预算仍按每
token 3 字节换算，对文本视图而言偏保守。

2026-11-09 验证结果（本地源码，未提交）：

- 新增确定性测试覆盖：模型视图（标题层级、上下文去重、部分片段、缺口、类型值、表格合并），估算等于
  交付的不变式，revision 10 最终证据与任务筛选不含节点 ID，冻结的 revision 9 投影与绑定，contract 5
  分片（上下文只出现一次、类型值原子性、长文本无损拆分），outline 模型视图，以及顶层标题的位置契约。
- macOS：`document`、`docindex`、`toolhub`、`agent`、`capability`、`config` 通过；`go build ./...`、
  `go vet ./internal/...` 通过。macOS 全量运行 79 个包通过，保留已知的 `cmd/sparkclaw`、`browserhost`、
  `gateway` Host/临时路径失败，这些在未修改的提交上同样失败。未重新执行 Linux 全量验证。
- 真实模型，使用与 revision 9 评测相同的 20 个读取请求，每个请求一次：prompt token 合计从
  1,078,690 降到 491,432；最大的一次最终调用（649 段 DOCX 全文总结）从 169,100 降到 27,098 token；
  总耗时从 655 秒降到 510 秒。粗检 20/20 通过，源文本复核 18/20 通过（revision 9 为 16/20）。仍有一个
  复合请求漏读了说明性章节并作出无依据的“不存在”断言，一份摘要仍复述运行时的提取局限。以上为单次
  结果，不构成准确率估计。
