# 大文档上下文与检索设计

> 语言： [English](../../docs/large-document-context-design.md) | 简体中文

状态：A 部分（按上下文推导的内容预算）已于 2026-11-03 在源码中实现，尚未部署。B 部分
（`large_indexed` 结构优先检索策略）依据第 7 节记录的 owner 决定实施，三个阶段均已于同日在
源码中实现：B1（文字层 PDF、DOCX 和文本）、B2（扫描版 PDF 的分析阶段 OCR）和 B3（PPTX/XLSX
结构与整篇文档的章节摘要）。均未部署。

后续设计，2026-11-05：[统一文档结构与评估证据](document-structure-and-evaluation-design.md)
提出所有文档都可结构化访问和按需读取，替代按大小选择能力的方式，同时保留上下文与资源预算；
并定义每次运行的证据，供 owner 在外部完成评审，程序不调用评审模型。这是未来设计，不改变
本文件记录的实现状态。

范围：文档读取路径（`internal/document`、`internal/toolhub` 文档工具）、Workflow 证据
供给、操作决策与最终化（`internal/agent`），以及 runtime 预算配置。保留
[Observation 压缩](observation-compression-redesign.md) 中的 observation 信封契约。
[Workflow 执行](workflow-execution.md) 仍是运行时权威。

## 1. 问题

### 1.1 文档被一次性整篇读取

`document.read` revision 4 先确认目标，再直接调用一个格式 reader，把完整抽取结果交给
Fast 最终化模型。当前只注册了 `small_file_v1`：

- 源文件超过 8 MiB（`SmallFileMaxBytes`）或抽取文本超过 200,000 字节
  （`SmallExtractedMaxBytes`）时返回 `strategy_deferred`
  （"no registered large-document strategy"）。agent 对这个错误码没有恢复路径，用户只能
  看到失败。
- pipeline envelope 已经预留了检索词汇（`strategy`、`context_mode`、`index_status`、
  `keyword_index_id`、`vector_index_id`），但只有 `small_direct` / `full_text` /
  `skipped` 三个取值。
- 测试用的 71 MB《计算机网络（第 7 版）》PDF 共 464 页，没有文字层（每页抽取 0 字），
  带 246 条书签目录，`pypdf` 读取目录只需 0.1 秒。对这类文件，有效策略必须先按结构定位，
  只对需要的页做 OCR；当前 PDF adapter 每次读取最多 OCR 8 页。

### 1.2 字节上限是固定值，没有按模型推导

A 部分之前，各内容通道无论选择哪个 capacity profile 都使用固定数值：

| 通道 | dual-light 默认（32K Fast） | 托管产品（256K Fast） |
|---|---|---|
| 发给最终化模型的文档正文 | 8,000 字节 | 200,000 字节（env 手工固定） |
| 供给单个阶段 / 决策的证据 | 8,000 字节 | 200,000 字节（同一配置项） |
| 读取接受的完整抽取 | 200,000 字节 | 200,000 字节 |

在 32K profile 上，8,000 字节（约 2,700 个汉字）的最终化证据只覆盖文档开头一小段，而
上下文窗口实际能装下约六倍内容。在 256K profile 上，介于 200,000 字节与窗口可承载量
之间的文档被直接拒绝。产品的固定值还有超窗风险：同样的 200,000 字节放到 32K 模型上约为
其输入窗口的三倍，而这个数值与实际使用的 profile 没有任何绑定。

## 2. 参考：DeepSeek Harness 0.2.1-rc.3

已以只读方式解包检查本机安装的 DeepSeek Harness。它**没有**实现文档结构识别或检索：

- 上传的非图片文件按原样存盘，模型只收到一行"文件已保存在某路径，需要时用文件工具
  读取"的提示。
- `read(file_path, offset, limit)` 按行分页读取 UTF-8 文本（每次 2,000 行 / 50 KB），
  末尾带 `Use offset=… to continue` 这类续读提示。PDF 等二进制文件会被拒绝；它自己的
  README 把 PDF 文本支持列为推迟项，内置 Python 也没有 PDF 库。
- `grep` 基于 ripgrep，最多返回 250 条不排序的匹配，不带上下文行。
- 任何超过约 12,500 token 的工具结果只保留头尾，全文落盘，并提示模型对该文件做
  `read` 或 `grep`。
- Office 文件靠 skill 指导模型自己写 python-docx / python-pptx / openpyxl 脚本。

可借鉴三点：给模型文件指针而不是整篇内容；分页读取并给出明确续读提示；超长结果落盘后
仍可检索。下面的"结构优先检索"是 SparkClaw 自己的设计。

## 3. A 部分：按上下文推导的内容预算（已实现）

### 3.1 公式

内容预算是消费该内容的模型输入窗口的一个份额：

```text
input_tokens = context_tokens(lane) - output_budget(lane, operation class)
budget_bytes = input_tokens x share x 3 bytes/token
```

每 token 3 字节对中文接近下界（一个汉字占 3 个 UTF-8 字节，约消耗 1 个 token），对拉丁
文字偏保守。常量与函数集中在 `config/context_budget.go`：

| 通道 | 消费方 operation（output class） | 份额 | 256K 产品 Fast | 32K dual-light Fast | 64K dual-light Deep |
|---|---|---|---|---|---|
| 最终回答证据 | `workflow.final_answer`（`answer`） | 60% | 457,113 B | 51,609 B | 103,218 B |
| 阶段 / 决策证据 | `workflow.step`、`workflow.decision`（`workflow_structured`） | 30% | 228,555 B | 25,803 B | 51,609 B |
| 完整抽取上限 | Fast/Deep 中较大的最终证据预算，下限 200,000 B，上限 2 MiB | — | 457,113 B | 200,000 B | 200,000 B |

lane 取实际发起模型调用的那条：文档 Workflow 用 Fast，其他 Workflow 用 Deep，单步使用
阶段的 lane hint。

### 3.2 保证不超出窗口

字节估算对高密度内容可能失准，数字表格可能每字节就是一个 token。因此每个消费方都会核对
渲染后的完整提示词：

- **最终化与操作决策**调用 `fitEvidencePrompt`：按推导预算渲染，用该 lane 的 tokenizer
  统计完整请求（`CountProfileChatInput`，接近边界时精确计数）。若超过"输入窗口减 256
  token 预留"，就按 `budget x window / tokens x 0.9` 重新渲染。四次尝试后，或预算降到
  512 字节时，以 `InputTooLongError` fail closed，绝不发出超窗请求。决策证据在每个预算下
  重新投影，结构化切片不会被从中间截断。
- **阶段供给**本来就经过 ContextBuilder admission，会先把供给证据从 full 降到 compact
  （一半）再到 minimal（四分之一），仍装不下才拒绝。

文档大于最终化预算时，最终化不再只取开头，而是在整篇文档上取均匀分布的窗口：最多 64 个、
每个至少 2 KB，从空行（段落或 PDF 分页）边界开始，最后一个窗口结束于文档末尾，每段省略的
内容都标注字节数。标头带有 `model_evidence_truncated=true`、
`model_evidence_sampling=evenly_spaced_windows` 和 `limitation_required=true`，回答会说明
只检查了部分内容。

### 3.2.1 每 token 字节数实测（2026-11-03）

使用托管 Fast tokenizer（`Qwen/Qwen3.6-35B-A3B-FP8`，与 dual-light 模型同属 Qwen3.6
系列）实测：

| 样本 | 每 token 字节数 | 每 token 字符数 |
|---|---|---|
| 中文实验报告（DOCX 正文） | 4.77 | 1.97 |
| 中文设计文档 | 3.63 | 1.93 |
| 英文设计文档 | 3.94 | 3.92 |
| 数字表格（8 列小数） | 1.00 | 1.00 |
| 含数字的商品编码 | 1.13 | 1.13 |

每 token 3 字节对正文是安全的。数字按每字节一个 token 切分，所以数字表格按推导预算渲染时，
实际 token 数是估算的三倍；这种情况下 tokenizer 校验收缩两次渲染即可收敛。

实测还发现一个缺陷：admission 把精确计数请求发到 `<base>/v1/tokenize`，而 vLLM 在服务
根路径提供 `/tokenize`，因此任何字节长度超过输入预算的提示词都会以"模型不可用"失败。
router 现在会先去掉末尾的 `/v1` 再拼接 `/tokenize`（commit `815f2ed6`）。

### 3.3 运维上限

`runtime.workflow_stage_evidence_max_bytes` 的含义调整为：

- `0`（Go 默认值、`configs/sparkclaw.default.json`、compose fallback 和托管产品 env 的
  新取值）表示按 capacity 推导。
- 正数是上限，只能调低推导出的阶段与最终化预算，供需要控制 prefill 成本的运维使用；
  它永远不能把预算抬到超出模型窗口。
- 它不限制完整抽取上限，因为抽取内容不直接进入模型。

部署 profile 校验现在把产品取值固定为 `0`，不再是 `200000`。旧版二进制读到 `0` 会回落到
自己的 8,000 字节默认值，所以 gateway 与 env 必须一起发布。

### 3.4 有意保持固定的预算

| 预算 | 取值 | 不按上下文推导的原因 |
|---|---|---|
| `observation_summary_max_bytes` / 证据摘录 | 2,400 B / 1,400 rune | 信封是持久化证据的索引，对所有工具统一（[Observation 压缩](observation-compression-redesign.md) §3.1）；内容经由供给和最终化通道传递 |
| `observation.read` 窗口 | 默认 8,000 B，最大 32,768 B | 辅助兜底通道，结果同样经过信封 |
| `workflow_run_max_observation_bytes` / 压缩阈值 | 48,000 / 36,000 B（产品 96,000 / 72,000） | 信封累计的停止条件，不是内容通道 |
| 会话历史裁剪 | 每个工具摘要 4,000 / 1,200 | 跨轮历史是索引（observation 设计 §3.6） |
| 浏览器快照 requirement | 8,000 B | 浏览器证据的组件上限 |
| 源文件大小门槛、PDF OCR 页数 | 8 MiB、8 页 | 解析与 OCR 成本上限；大文件由 B 部分接管 |

## 4. B 部分：`large_indexed` 策略（已在源码实现）

### 4.1 范围与入口

上传上限保持不变：WebChat 上传每个文件 25 MiB（`gateway/documents_endpoints.go`），桌面端
ISCP 执行输入每个文件 8 MiB（`iscpobjects/store.go`），不做分块上传。因此大文档只会经由
两种途径进入本策略：

- **上传文件**：不超过 25 MiB，但正文无法一次完整读取。
- **工作区文件**：没有经过上传的文件（直接放入工作区、桌面端落地的输入、编辑输出）。这类
  文件可以超过 25 MiB，71 MB 的《计算机网络》PDF 只能通过这种方式进入。

### 4.2 策略选择

策略由读取结果决定，而不是注册进 pipeline。源文件超过 8 MiB（`SmallFileMaxBytes`）或完整
抽取超过 §3.1 的上限时，完整读取策略会推迟处理；对 PDF、DOCX、PPTX、XLSX 和文本，
`files.read` 与 `pdf.extract_text` 以 §4.7 的目录指针回应这次推迟。图片等其他格式，以及调用方
主动要求小于抽取上限的读取，仍返回 `strategy_deferred` 错误。启用 OCR 时，无文字层页数超过
单次完整读取 OCR 预算（8 页）的 PDF 也会转入检索，该次读取已识别的页写入索引缓存，不会重复
识别；未启用 OCR 时保留部分读取结果，因为检索同样无法识别剩余页面。今天能处理的文件继续走
`small_direct` / `full_text`，行为不变。

### 4.3 生命周期：导入预建 + 分析兜底

**导入阶段（后台、轻量）。** WebChat 文档上传保存后，gateway 把它放入 ToolHub 索引服务的
队列：单 worker、32 项队列（满时丢弃请求）、随 hub 关闭而取消。worker 对 PDF、DOCX 和文本
执行纯文本索引抽取；源文件不超过 8 MiB、正文不超过抽取上限且没有缺文字层的 PDF 页时，说明
完整读取足以处理，不保留索引，否则持久化结构图、文字层分块和关键词索引。导入阶段：

- 不做 OCR，也不调用任何模型；
- 对用户不可见，界面不新增任何状态字段；
- 失败时除一行日志外不写入任何内容，由分析阶段重试构建。

**分析阶段（按需）。** 请求读取该文档时：

| 索引状态 | 行为 |
|---|---|
| `ready` | 直接检索 |
| `building`（导入仍在进行） | 等待这次构建完成，不另起第二次 |
| 不存在或 `failed`（非上传来源，或预建失败） | 在本次运行中现场构建，然后检索 |

OCR 只在这个阶段进行（§4.5）。

**进度。** 只有请求必须等待索引时，用户才会看到相关提示：读取等待或构建索引期间，agent 会把
运行中的工具调用以 `progress: "正在建立文档索引"` 重新保存，这条说明经由现有的
`tool_call.started` 流事件到达 WebChat，流状态行用它替换通用的"工具开始"文字。索引已就绪时
不显示任何内容，已完成的调用记录不带这条说明，也不新增常驻指示（AGENTS.md 的 UI 规则）。

### 4.4 索引内容（每个源文件 SHA-256 只建一次）

1. **结构图**（`outline.json`），按格式：
   - PDF：书签目录生成标题树和页码范围；没有书签的 PDF 暂无目录，直接检索正文。
   - DOCX：标题样式（`Heading N`、`标题 N`、`Title`）和大纲级别；每个表格行是一个文本单元。
   - 文本 / Markdown：`#` 标题；段落以空行分隔。
   - PPTX（`scripts/pptx_index.py`）：每张幻灯片一个一级节点，标题为 `Slide N: <标题>`，其后是
     文本框、表格行和讲者备注；幻灯片编号作为锚点页码，范围标头显示 `slide N`。
   - XLSX（`scripts/xlsx_index.js`）：每个 sheet 一个一级节点；每个非空行以该 sheet 首行为表头
     写成 `表头: 值` 对，检索列值时能连同列名一起命中。
2. **分块**（`chunks.jsonl`）：沿结构节点切分，每块约 1,000 字，带少量重叠。每块带锚点
   （页码、标题路径、稳定 block / 段落 / 单元格 ID），沿用现有 citation 词汇。
3. **关键词索引**（`keywords.json`）：只用纯 Go BM25，本设计不含 embedding。中文按字二元组
   切分，拉丁文字按小写词切分，标题路径中的词计两次。正文与查询都做 NFKC 规范化，因为 PDF
   文字层常带兼容字形，例如以康熙部首 `⽅` 代替 `方`；两个汉字之间的 PDF 换行会被合并，避免
   词被拆开。不引入新运行时依赖。
4. **页码映射**：没有文字层的 PDF 页标记为 `unindexed`，等 OCR 补齐。整本扫描的 PDF 也是有效
   索引，只是暂无分块（状态 `partial`）；目录节点同时带页码范围和分块范围。
5. **抽取结果**（`source.json`）：上一次抽取的文本单元。合并识别出的页时据此重建分块和关键词，
   不必重新解析文档。

检索片段以命中词中最罕见的一个为中心，因此按商品编码找到的行会显示该编码。检索还会返回标题
与查询匹配的目录章节，使扫描书籍在任何页面有文字之前就能按章节定位。

PDF 按 40 页一批构建（`scripts/pdf_index.py`，pypdf，仅文本），保证单次 adapter 调用不触及
60 秒子进程超时；DOCX 一次调用 `scripts/docx_index.py`（python-docx，按正文顺序，不读资源）；
文本文件逐行流式读取，超过 64 KiB 的行会被切开。构建使用服务自身的 context，因此某个请求
停止等待不会取消其他请求可能需要的构建。

### 4.5 OCR 规则

OCR 只在分析阶段进行，按配置的并发度使用现有 OvisOCR2 运行时。页面渲染方式与完整读取相同
（`scripts/pdf_index.py` 的 `render` 操作，pypdfium2 2 倍缩放，JPEG 最大 4 MiB）。每批完成后
立即把识别结果缓存到 `ocr/<page>.json`，中断的请求不会丢失已完成的工作，同一页也不会识别
两次。运行中的调用在本次运行的进度行显示 `正在识别扫描页（n/m）`。

- **定位类问题**只 OCR 检索选中的页。扫描版 PDF 在 OCR 之前不知道正文内容，因此按目录标题
  及其页码范围选页：对章节或页码范围调用 `document.read_range` 时，先识别该范围内无文字的页，
  每次最多 8 页，其余页以 `unrecognized_pages` / `missing_page_indexes` 返回。
- **全文类请求**（`document.summarize`），以及对没有目录的扫描件的检索，在源文件不超过现有
  25 MiB 上传上限时分批识别全部页面。超过该大小的工作区文件仍只 OCR 命中页，回答中说明哪些
  页未识别。

### 4.6 存储、隔离与保留

- 索引放在文档 pipeline 工作区根目录的 `.sparkclaw/state/doc-index/<sha256>/` 下
  （`manifest.json`、`outline.json`、`chunks.jsonl`、`keywords.json`、`source.json`、
  `ocr/<page>.json`、`summaries/<key>.json`）。该根目录是 hub 配置的工作区根目录，或执行范围（execution scope）中
  owner 的工作区。查询需要对具有该哈希的文件拥有已授权路径，因此索引访问权限跟随文件访问
  权限。同一文件重复上传复用同一份索引，最近使用的四份索引常驻内存。
- `.sparkclaw/state` 是控制目录：输出路径不能指向它，`files.search` 跳过 `.sparkclaw`，现在
  所有读取路径也会拒绝它，使索引只能通过文档工具访问。
- manifest 记录源文件 SHA-256、大小、策略与解析器版本、状态（`building`、`ready`、
  `partial`、`failed`）、尝试次数和时间戳。版本变化时重建索引。
- 索引记录的源路径都已不存在或大小不符时删除该索引；LRU 清理把目录控制在每个工作区 512 MiB
  以内。每次构建完成后执行一次清理。
- 使用 Postgres 状态后端时，索引仍放在工作区卷上，因为它是派生数据，丢失只需重建。

### 4.7 模型工具（read 风险，各自独立的 capability）

| 工具 | 返回 | 上限 |
|---|---|---|
| `document.outline(path)` | 标题树、页码范围、规模、索引状态、未索引页 | 阶段预算 |
| `document.search(path, keywords[], top_k, section?)` | 带锚点和得分的排序片段，以及标题匹配的目录章节；对 25 MiB 以内无目录的扫描件先整篇识别 | `top_k x 片段` 不超过阶段预算 |
| `document.read_range(path, chunk_ids \| page_start..page_end \| section, context)` | 带位置标头（`PDF page N`、`slide N`）的该范围正文；先识别范围内最多 8 个扫描页；被截断时给出 `next_chunk_id`；预算紧张时先舍弃相邻上下文，再舍弃请求的分块 | 不超过阶段预算 |
| `document.summarize(path)` | 每个顶层章节一份摘要，以 Markdown 和列表返回；有抽样章节或未识别页面时 `read_complete` 为 false | 每次章节调用不超过最终回答预算 |

这些工具使用各自的 capability（`document.outline`、`document.search`、`document.read_range`、
`document.summarize`），不会与 `document.read` 的格式 reader 竞争。检索和范围读取的超时分别为
15 和 10 分钟，摘要为 20 分钟，因为它们可能要识别页面或按章节调用模型。它们的预算是 Fast lane 的阶段证据预算，也就是文档
Workflow 运行的 lane。对已建索引的文档，`files.read` 与 `pdf.extract_text` 返回目录（最多
8,000 字节），并带 `retrieval_required: true`、`read_complete: false` 和使用
`document.search` / `document.read_range` 的提示。

### 4.8 Workflow 与路由

`document.read` revision 5 对小文档保持 revision 4 的路径，并增加大文档分支。已持久化的
revision 4 Plan 恢复时保持原样。

1. `confirm_document_target` 不变。
2. `document_read` 的 `read_by_type` 阶段仍直接调用格式 reader（因为节点现在有 transition，
   改用 profile 的 direct stage 而不是 `direct_once`）。能完整读取的文档照常完成该节点；被推迟
   的可索引文档返回目录指针，其 `retrieval_required` 结果携带 `document_outline` 资源引用，
   并把节点切换到 `retrieve` 阶段。该阶段的 scope 是三个检索工具，每个工具的 `path` 都绑定到
   冻结的目标。因此路由无需 preflight 探测大小，只有解析后才知道大小的文件同样能处理。
3. 在 `retrieve` 阶段，Fast step 模型把用户请求转成关键词（含中英文同义词），作为它自己调用
   `document.search` 时的参数，不为改写查询额外调用模型。最新的目录和检索结果以紧凑文本形式
   供给（每个命中一行，含分块 ID、页码、章节和片段）。检索与目录结果最多重新进入该阶段四次，
   指令要求最多检索三次；返回正文的 `document.read_range` 完成该节点，节点允许六次尝试。
4. 在最终化预算内基于读取结果和检索到的范围作答，引用页码或标题，并说明答案基于检索到的
   章节。

全文类请求（例如总结整本书）通过 `document.summarize` 走章节级 map-reduce。单元为顶层目录
章节，外加第一个标题之前的正文；超过 16 个时合并为 16 组连续章节，没有目录的文档按约 40 个
分块切成窗口。每个单元由一次 Fast 调用（`document.section_summary`，`answer` 输出类别）生成
摘要，最多四个并行。超过 Fast 最终证据预算的单元分段摘要（最多 8 段，超出时均匀抽样）后再合并；
tokenizer 仍拒绝的分段会对半切开。摘要按提示词版本、标题和正文缓存，重复请求不再调用模型。
最终化合并各章节摘要，并说明被抽样或未识别的部分。

一旦有了检索或摘要证据，最终化就不再纳入目录指针：它的 `read_complete: false` 和缺失页描述的
是用于路由的那次读取，而不是之后读取的段落或摘要。检索查询失败（例如章节 ID 不存在）按"未命中"
处理，step 模型可以在该阶段有限的 transition 次数内重新查询。

**编辑不在范围内。** 在 `document.edit` 中，定位读取若返回 `retrieval_required`，节点以
`large_document_edit_unsupported` 阻断，用户收到"这个文档太大，暂不支持直接修改，原文件保持
不变。可以先就其中的内容提问或查找相关段落。"（或对应英文），不运行任何编辑器，源文件保持
不变。`Pipeline.Edit` 仍会整篇重读源文件。

### 4.9 追问

跨轮历史仍然只是索引（§3.4）。对同一文档追问时，通过现有的 recent-document resolver 重新
定位文件，按哈希复用持久化索引，并重新检索（代价很小）。不复用上一轮模型可见的摘录，但会
复用已缓存的 OCR 页和章节摘要。

### 4.10 Envelope 词汇

在 `app/document_pipeline.go` 新增 `DocumentStrategyLargeIndexed = "large_indexed"`、
`DocumentContextRetrieved = "retrieved"`，以及索引状态 `building`、`ready`、`partial`、
`failed`，现有 wire 取值保持不变。

## 5. 交付与验证

| 阶段 | 内容 | 验收 |
|---|---|---|
| A（源码已完成） | 按上下文推导预算、tokenizer 校验收缩、产品 env 设为 `0` | 公式、上限、重渲染与 fail closed 路径的单测；能装入 32K 窗口的文档完整进入最终化；抽取上限跟随最终化模型 |
| A2（源码已完成） | 真实 tokenizer 每 token 字节数（§3.2.1）；修复 tokenizer 地址；最终化用均匀分布窗口代替只取开头 | 记录实测比例；被截断的 40 章样例从第一章到最后一章都有窗口 |
| B1（源码已完成） | 索引、后台导入预建、分析阶段现场构建或等待、outline/search/read_range、revision 5 路由、`files.read` 指针、编辑拒绝，覆盖文字层 PDF、DOCX 和文本 | 单元与端到端测试（mock 模型）：大型 Markdown 书籍依次完成读取 → 检索 → 范围读取 → 最终化，且最终证据包含目标段落；编辑请求被拒绝且文件不变；构建时只有一个进度事件，就绪时没有；上传预建只为大文件保留索引。56 页 Chrome 打印 PDF 与对应 DOCX 都把正确章节排在第一。托管 Fast 真实模型（产品 profile）：394 KB 的书被整篇读取并正确作答；790 KB 的书依次完成读取 → 检索（模型自行选择 `慢开始门限 ssthresh 拥塞窗口 cwnd 增长`）→ 一次范围读取，15.5 秒内（含建索引）正确作答并给出章节。打包客户端验收仍未完成 |
| B2（源码已完成） | 扫描版 PDF 的分析阶段 OCR、按标题匹配章节、小型扫描 PDF 的路由 | 使用假 OCR 适配器的单测覆盖：按 8 页一批识别章节、无目录扫描件整篇识别、OCR 预算用尽的读取向索引预置已识别页，以及未启用 OCR 的路径。托管 Fast 与 OCR 真实模型：对 71 MB 的《计算机网络》工作区文件，"根据 计算机网络.pdf，总结一下 TCP 拥塞控制里慢开始和拥塞避免的内容"依次完成读取 → 检索（按标题命中章节）→ 一次章节读取，只识别了 §5.8.2 的页面，用时 33 秒。20 页无目录的扫描节选被整篇识别，并基于运输层页面在 44 秒内作答 |
| B3（源码已完成） | XLSX/PPTX 结构与章节 map-reduce | 单测覆盖幻灯片标题与备注、按商品编码命中 6,000 行工作簿、摘要单元的分组与窗口、缓存，以及端到端整书请求。真实模型："把 book.md 全文总结一下"对 790 KB 的书依次完成读取 → `document.summarize`，50 秒内返回 20 章摘要 |

每个阶段开始前记录 `internal/config`、`internal/document`、`internal/toolhub`（先执行
`npm run setup:document-tools`）和 `internal/agent` 的基线，结束后重跑。按仓库 UI 规则，
UI 验证须覆盖原有文档交互和新增进度行。

## 6. 风险

- **prefill 成本随窗口增长。** 256K 最终化现在可能收到约 450 KB 证据。暂时接受，待实际体验
  后再评估；运维上限仍可使用，证据投影记录会保留实际载荷与归档字节数。
- **tokenizer 往返。** 接近边界的提示词在收缩时和派发时各精确计数一次，为正确性接受这项
  开销。
- **改写式提问导致关键词落空。** 以 step 模型检索参数中的同义词、标题加权和目录浏览缓解。
  在评测显示差距之前，有意不引入 embedding。
- **全文 OCR 延迟。** 对不超过 25 MiB 的扫描件做整篇 OCR 可能需要数分钟。分批执行、按页
  缓存，只通过本次运行的进度行可见。
- **后台解析负载。** 导入预建会让每个符合条件的上传多一次纯文本解析。队列只有一个 worker，
  按需构建在其旁边并行运行，而不是排在其后。
- **摘要成本。** 整篇请求第一次最多调用 16 次 Fast（章节被切分时更多），之后由缓存免费复用。
- **提问措辞。** 输入的中文文件名现在能被识别（此前不能），但以常识提问的说法，例如
  "计算机网络.pdf 里 … 是怎么工作的？"，仍可能被路由到对话回答而不是 `document.read`。这属于
  本设计之外的路由决策。

## 7. 决定（owner，2026-11-03）

| 问题 | 决定 |
|---|---|
| 上传上限 | 保持 25 MiB（WebChat）和 8 MiB（ISCP）；不做分块上传 |
| 检索方式 | 暂时只用关键词（BM25） |
| 大文档编辑 | 暂不支持；给出明确的"不支持"答复 |
| OCR | 在现有文件大小限制内允许整篇 OCR；否则只 OCR 检索选中的页 |
| 建索引时机 | 导入预建 + 分析兜底（§4.3）；预建对用户不可见，失败不打扰用户 |
| 进度 | 仅当请求需要等待索引时，在本次运行已有的工具进度中显示 |
| 上下文成本 | 暂时接受推导出的预算、不设上限；待实际体验后再定 |
