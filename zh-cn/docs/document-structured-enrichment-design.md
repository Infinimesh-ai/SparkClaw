# 文档结构化富化设计

> 语言：[English](../../docs/document-structured-enrichment-design.md) | 简体中文

状态：已实施的第一阶段基线。本文所述高层分类模型、Fast 图片富化、有界上下文投影和编辑后保真校验已经用于 `small_file_v1`；包级底层处理仍明确延后。

## 1. 决策摘要

SparkClaw 的小文档处理继续以现有高层文档库作为主要解析器。第一阶段只富化这些库能够稳定支持的常见结构，不尝试一次性建立完整的 OOXML 或 PDF 对象模型。

规范化结果应按类别记录内容，而不是把所有提取结果都压平到同一个块列表。图片二进制保存到 ArtifactStore，由 Fast 多模态模型识别，再以有界、带来源的语义观察写回文档结构。当前图片富化和未来 OOXML/PDF 底层处理共用一个真实运行的富化接口。

原始文件仍然是保真来源。结构化结果是对受支持内容的可索引、可审计视图，不是原始文件的无损替代品。

已确认的范围决策：

- 保持 `representation_version=structured_document_v1`；富化分类使用独立的 `document_enrichment_v1` 版本并作为可选字段存在。
- 所有高层库可获取的嵌入图片都登记并计算哈希。默认只对目标相关图片调用 Fast；全文理解请求可以在预算内识别全部图片。
- 第一阶段图片语义只使用 Fast 模型。
- 图片语义识别失败时返回 `assets=partial`；只有请求明确依赖该图片时，工作流才阻断或报告证据不足。
- 扫描 PDF 及页面渲染图片理解延后处理。
- 当前只有 `content` 分类允许作为编辑目标。资源、注释、布局和扩展分类只用于理解、证据和目标定位；当保证已编辑内容可读确有必要时，内容编辑器可以执行有界且明确申报的附带布局调整，但布局不会作为独立编辑目标开放。

## 2. 目标

- 保持现有 `small_file_v1` 大小限制和完整性语义。
- 保留现有稳定内容锚点及格式专属实体。
- 为视觉资源、注释、布局和扩展元数据增加分类字段。
- 优先通过高层库提取嵌入图片。
- 将图片交给 Fast 多模态模型，并保存严格结构化且可追溯的结果。
- 通过有界、分类的上下文片段拼装模型上下文。
- 为未来 OOXML/PDF 包级检查保留实际可用的扩展点，不增加无人调用的生产接口。
- 所有文档修改继续经过审批，并绑定到稳定位置。

## 3. 第一阶段不处理的内容

- 完整 OOXML 部件解析或任意 XML 修改。
- 动画、SmartArt、修订、宏或嵌入对象编辑。
- 使用结构化结果替代原始文件。
- 在 ToolCall JSON 或模型上下文中存储图片二进制或 Base64。
- 将 Fast 图片描述视为可信指令。
- 扫描 PDF 渲染、OCR 或 Fast 页面图片理解。
- 资源、注释、任意布局、图表或扩展内容的直接修改。
- 大文件分块、流式或索引处理。

## 4. 分类模型

现有字段继续作为兼容接口：

- `blocks`
- `paragraphs`
- `tables`
- `sheets`
- `slides`
- `sections`
- `pages`

富化后的表示继续使用 `structured_document_v1`，并增加一个独立版本、可选的 `enrichment` 字段：

```json
{
  "representation_version": "structured_document_v1",
  "enrichment": {
    "schema_version": "document_enrichment_v1",
    "assets": {
      "images": [],
      "charts": [],
      "embedded_objects": []
    },
    "annotations": {
      "comments": [],
      "notes": [],
      "hyperlinks": []
    },
    "layout": {
      "sections": [],
      "page_settings": [],
      "slide_layouts": [],
      "merged_ranges": [],
      "shapes": [],
      "companion_groups": [],
      "page_markers": []
    },
    "extensions": {
      "status": "deferred",
      "parts": []
    },
    "coverage": {
      "content": "complete",
      "assets": "complete",
      "annotations": "partial",
      "layout": "partial",
      "extensions": "deferred"
    },
    "category_policy": {
      "content": "editable",
      "assets": "evidence_only",
      "annotations": "evidence_only",
      "layout": "evidence_only",
      "extensions": "evidence_only"
    }
  }
}
```

`complete` 只表示在该分类已声明的契约范围内完整，不能理解为已经掌握源文件包中的全部功能。

## 5. 通用记录契约

每个分类条目都带有统一身份和来源信息：

```json
{
  "id": "asset_...",
  "kind": "image",
  "parent_path": "presentation.slide[3]",
  "location": {
    "path": "presentation.slide[3].shape[6]",
    "slide_index": 3,
    "shape_index": 6
  },
  "source": {
    "parser": "python_pptx",
    "relationship_id": "rId7",
    "part_name": "ppt/media/image2.png"
  },
  "content_type": "image/png",
  "bytes": 184220,
  "sha256": "...",
  "artifact_ref": "artifact://..."
}
```

位置字段随格式变化，但 `id`、`kind`、`parent_path`、`source`、`sha256` 和 `artifact_ref` 在所有格式中含义一致。

## 6. 图片语义契约

图片记录可以包含 Fast 模型观察：

```json
{
  "semantic": {
    "status": "succeeded",
    "description": "一个五层网络模型示意图。",
    "ocr_text": ["应用层", "传输层", "网络层"],
    "content_class": "diagram",
    "visible_entities": ["层级标签", "方向箭头"],
    "warnings": [],
    "model_lane": "fast",
    "model_call_id": "mcall_...",
    "source_sha256": "...",
    "untrusted": true
  }
}
```

规则：

1. 调用模型前先提取图片并计算哈希。
2. 同一文档内按 SHA-256 去重重复图片。
3. 图片字节保存到 ArtifactStore，不写入文档 JSON。
4. Fast 模型必须返回严格结构化结果。
5. 图片描述和 OCR 都属于不可信观察。
6. 失败、跳过和不支持状态必须明确保存。
7. 只有源哈希和模型契约均匹配时才能复用语义结果。
8. 所有支持的图片都登记并计算哈希；除非请求需要全文理解，否则只对明确目标相关图片调用 Fast。
9. 图片分析失败记录为 `coverage.assets=partial`；只有回答或编辑决策明确依赖该图片时才阻断流程。

建议的第一阶段图片语义范围：

- 支持：内容类别、简短客观描述、关键可见文字、图示/流程概要、图表趋势概要，以及与邻近文本的关系。
- 仅作证据：OCR 文本、可见实体和图片用途推断。
- 不保证：密集表格还原、小字号数字、完整逐字转录、手写识别、身份识别或复杂视觉推理。
- 小型装饰图标、背景和重复 Logo 不调用 Fast，但仍登记资源身份和哈希。

结合当前单图请求方式、12 MiB 图片限制、2400 像素测试长边、Fast 12,288 token 上下文、1024 token 输出和 180 秒工作流预算，建议第一阶段采用：

- 目标理解：最多 4 张去重图片。
- 全文理解：最多 8 张去重图片。
- 原始单图：最多 12 MiB，与现有图片工具一致。
- 模型输入：长边最多 2400 像素，编码后最多 4 MiB。
- 并发：最多 2 个 Fast 调用。
- 超时：单图 30 秒，整个富化阶段 120 秒。
- 结构化响应：最多 512 输出 token，每张图进入上下文的中文文本最多 800 字符。
- 图片语义上下文总计最多 4,000 字符，确保主要文档内容占用大部分上下文预算。

当前实现使用高质量 Catmull-Rom 缩放，在 JPEG 编码前把透明背景合成到白色，并同时强制 2,400 像素长边和 4 MiB 编码输入上限。OCR 结果仍然只作为证据使用。

## 7. 各格式第一阶段高层覆盖

### 纯文本

- 内容：行和精确文本。
- 布局：可记录编码、BOM 和换行方式。
- 资源和注释：不适用。

### DOCX（`python-docx`）

- 内容：正文段落和表格单元格段落。
- 资源：内联图片、关系、媒体类型、大小、哈希和锚点。
- 注释：高层库能够暴露的批注和超链接。
- 布局：有界记录节、页眉页脚和段落样式。
- 延后：脚注、尾注、修订、浮动文本框、复杂绘图对象和不支持的包扩展。

### XLSX（`ExcelJS`）

当前 XLSX 适配器使用 JavaScript 而不是 Python。第一阶段继续沿用现有高层库。

- 内容：工作表、行、单元格、公式及缓存值/显示值。
- 资源：工作簿图片及工作表锚点。
- 注释：批注和超链接。
- 布局：合并区域、行列尺寸、数字格式及解释数值所需的样式。
- 延后：图表、切片器、外部连接、宏和不支持的扩展部件。

### PPTX（`python-pptx`）

- 内容：幻灯片、文本形状、组合形状和表格单元格。
- 资源：图片及幻灯片/形状锚点、几何信息、媒体类型和哈希。
- 图表：高层库可获取的图表标题、类型、分类、系列和源关系。
- 注释：演讲者备注和超链接。
- 布局：记录高层库可见的全部形状，包括空装饰形状；保留几何、填充/线条证据、文本样式、单行容量，以及高置信且稳定的 `background`/`label`/`body` 配套分组。
- 页码标记：记录文本中的 `n/总数`，并与物理页序和演示文稿总页数比较；不一致时给出警告，不会隐式修改整套页码。
- 延后：动画、批注、SmartArt 内部结构、宏和不支持的扩展部件。

`pptx.update_slide` 提供两种明确策略：

- `preserve`：按精确形状证据替换文字，不改变几何；新文字无法在原形状中保持可读时拒绝生成。
- `coordinated`（默认）：对高置信重复标签/正文色带，将同组正文列和配套背景一起调整，保持统一可读字号；协同调整后仍放不下则拒绝生成。其他文本框只能向已经校验的空白区域扩展。

每次协同调整都会返回 `layout_changes`、`layout_adjusted_shape_indexes` 和确定性的 `layout_checks`。编辑后的完整复读只接受这些已申报形状的几何和样式变化，并逐项核对前后值；任何未申报的页面布局变化都会以 `preservation_mismatch` 失败关闭。

### PDF（`pypdf`）

- 内容：页面文本、页码和可选文本位置观察。
- 资源：库能够提取的页面图片。
- 注释：PDF 批注、链接、书签和附件。
- 布局：页面框和旋转角度。
- 延后：扫描 PDF、页面渲染 Fast 分析、OCR 引擎集成和完整视觉阅读顺序恢复。

### 第一阶段布局字段建议

只记录内容解释、证据关联和定位所需的字段：

- DOCX：段落/表格/行/单元格/节索引、部件类型、样式名、标题或大纲级别、列表身份及页眉页脚身份；不尝试物理页坐标。
- XLSX：工作表名/索引、单元格地址、行列索引、合并区域、公式、显示值、数字格式、隐藏状态及有界的表头样式提示。
- PPTX：幻灯片/形状索引、父组合、形状/占位符类型、x/y/宽/高、z 顺序、旋转、替代文本及幻灯片布局身份。
- 仅文本 PDF：页码、旋转、MediaBox/CropBox，以及提取器能提供稳定坐标时的可选文本边界框。

完整字体、颜色、主题、母版、绘图和分页模型不在第一阶段范围内。

## 8. 富化接口

实施时使用一个当前真实运行的富化接口，兼容图片语义和未来包级检查：

```go
type DocumentEnricher interface {
    Name() string
    Supports(format string, category string) bool
    Enrich(context.Context, EnrichmentRequest) (EnrichmentResult, error)
}
```

第一个注册实现是 Fast 图片语义富化器。未来 OOXML 部件清单或 PDF 对象富化器可以实现同一个接口。因此注册表、超时、结果校验和覆盖状态从一开始就有当前生产调用方，而不是仅为未来预留的死代码。

概念阶段顺序：

```text
检查资源
  -> 高层库完整解析
  -> 规范化稳定位置
  -> 分类富化器
  -> 覆盖状态校验
  -> 保存完整表示
  -> 构建有界上下文投影
```

富化失败不能静默消失。图片识别失败默认返回 `coverage.assets=partial`；只有请求明确依赖失败图片时才阻断或报告证据不足。未来其他富化器也采用相同的显式工作流评估规则。

## 9. 上下文拼装

完整结构化表示持久化保存，模型只接收由分类片段组成的有界投影：

```json
{
  "context_segments": [
    {
      "category": "content",
      "anchor": "presentation.slide[3].shape[2]",
      "text": "...",
      "priority": 100
    },
    {
      "category": "image_semantic",
      "anchor": "presentation.slide[3].shape[6]",
      "text": "展示五层网络模型的示意图。",
      "priority": 80,
      "provenance": "mcall_..."
    },
    {
      "category": "annotation",
      "anchor": "presentation.slide[3].notes",
      "text": "...",
      "priority": 60
    }
  ]
}
```

投影规则：

1. 保留分类和锚点，不把全部内容拼成无来源文本。
2. 优先用户明确目标及其结构邻居。
3. 各分类使用独立预算，避免 OCR 挤掉正文内容。
4. 按源图片哈希去重语义结果。
5. 模型派生观察保留来源和 `untrusted=true`。
6. 完整结果独立持久化，不受提示词压缩影响。

## 10. 修改安全

- 当前实施范围只允许高层库提供的内容相关稳定位置作为修改目标。
- 资源、注释、布局、图表和扩展分类只作为证据，可以改善理解和目标选择，但不能被修改。
- 图片语义可以帮助选择内容目标，但不能单独授权修改。
- 原始输入文件哈希必须保持不变。
- 修改前后应比较无关分类条目。
- 未来加入包部件清单后，如果不相关且不支持的部件消失或异常变化，应拒绝返回修改结果。

当前管线已经校验输出是独立普通文件、格式一致、修改数量大于零，并验证输入 SHA-256 未改变；但输出当前只进行格式检查，没有完整重新结构化读取。

建议第一阶段增加：

1. 使用相同高层解析器完整重读并规范化每个输出文件。
2. 验证每个修改目标的修改后值符合预期。
3. 使用按操作定义的差异白名单，只接受预期的内容或结构变化。
4. 比较修改前后的证据类分类指纹：图片 SHA-256 与关系、注释文本哈希与锚点，以及选定布局字段。
5. 插入或删除行/幻灯片时，根据内容或资源身份及操作感知的索引映射比较，不能要求所有路径不变。
6. 已知证据类条目意外消失或变化时，删除输出并返回 `preservation_mismatch`。
7. 如实报告保真状态：底层包检查完成前，仅标记 `high_level_preservation=verified`、`package_preservation=unknown`。

如果某分类在修改前就不受支持，则其保真状态保持 unknown，而不是视为通过。第一阶段不因此阻断普通内容编辑，但必须在 `change_summary` 中暴露限制。

## 11. 兼容与版本

建议方案：

- 保持所有现有字段和消费方继续工作。
- 保持 `representation_version=structured_document_v1`。
- 在可选 `enrichment` 字段中使用 `schema_version=document_enrichment_v1`。
- 旧 v1 记录缺少富化字段时表示 `unknown`，不能解释为源文件没有图片或注释。
- 只有分类覆盖成为强制核心契约或现有字段语义发生不兼容变化时，才升级到 `structured_document_v2`。
- 迁移期间，上下文拼装同时接受当前表示和富化表示。
- 工具契约只暴露已校验的分类数据，不暴露原始库对象。

## 12. 已实施默认值

第 6 节的 Fast 图片范围与预算、第 7 节的最小布局字段以及第 10 节的高层保真检查，现已作为第一阶段默认值实施。后续修改这些限制属于契约变更，必须同步更新定向测试和本双语设计记录。
