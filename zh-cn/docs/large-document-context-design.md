# 大文档上下文与检索设计

> 语言： [English](../../docs/large-document-context-design.md) | 简体中文

状态：A 部分（按上下文推导的内容预算）已于 2026-11-03 在源码中实现，尚未部署。B 部分
（`large_indexed` 结构优先检索策略）是已采纳、尚未实现的设计，所依据的 owner 决定记录在
第 7 节。

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

文档大于最终化预算时沿用现有行为：只投影开头部分，标记
`model_evidence_truncated=true` / `limitation_required=true`，回答会说明只检查了部分内容。

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

## 4. B 部分：`large_indexed` 策略（已采纳，尚未实现）

### 4.1 范围与入口

上传上限保持不变：WebChat 上传每个文件 25 MiB（`gateway/documents_endpoints.go`），桌面端
ISCP 执行输入每个文件 8 MiB（`iscpobjects/store.go`），不做分块上传。因此大文档只会经由
两种途径进入本策略：

- **上传文件**：不超过 25 MiB，但正文无法一次完整读取。
- **工作区文件**：没有经过上传的文件（直接放入工作区、桌面端落地的输入、编辑输出）。这类
  文件可以超过 25 MiB，71 MB 的《计算机网络》PDF 只能通过这种方式进入。

### 4.2 策略选择

`inspectAndSelect` 已经会遍历策略。在 `small_file_v1` 之后注册 `large_indexed_v1`。满足
任一条件即需要它：

- 源文件超过 8 MiB（`SmallFileMaxBytes`）；
- 完整抽取会超过 §3.1 的抽取上限；
- PDF 中没有文字层的页数超过单次有界读取能 OCR 的页数（当前为 8）。

今天能处理的文件继续走 `small_direct` / `full_text`，行为不变。

### 4.3 生命周期：导入预建 + 分析兜底

**导入阶段（后台、轻量）。** 上传文件保存后，由 gateway 持有的后台队列（单 worker，关闭时
取消）判断它是否需要 `large_indexed`。源文件超过 8 MiB 直接判定需要；较小的文件做一次关闭
enrichment 的纯文本解析，测量抽取大小并统计无文字层的 PDF 页数。需要的文件建立结构图、
文字层分块和关键词索引。导入阶段：

- 不做 OCR，也不调用任何模型；
- 对用户不可见，界面不新增任何状态字段；
- 失败只记录在索引 manifest 和审计日志中，由分析阶段重试。

**分析阶段（按需）。** 请求读取该文档时：

| 索引状态 | 行为 |
|---|---|
| `ready` | 直接检索 |
| `building`（导入仍在进行） | 等待这次构建完成，不另起第二次 |
| 不存在或 `failed`（非上传来源，或预建失败） | 在本次运行中现场构建，然后检索 |

OCR 只在这个阶段进行（§4.5）。

**进度。** 只有请求必须等待索引时，用户才会看到相关提示：运行等待或构建索引期间，本次
运行已有的工具进度显示"正在建立文档索引"。索引已就绪时不显示任何内容，也不新增常驻指示
（AGENTS.md 的 UI 规则）。

### 4.4 索引内容（每个源文件 SHA-256 只建一次）

1. **结构图**（`outline.json`），按格式：
   - PDF：书签目录生成标题树和页码范围；没有书签时按固定页窗口，并以大字号行作为候选
     标题。
   - DOCX：标题样式和大纲级别；表格作为独立节点。
   - PPTX：每张幻灯片一个节点，以标题为键。
   - XLSX：每个 sheet 一个节点，记录表头行和已用区域。
   - 文本 / Markdown：`#` 标题，否则按固定行窗口。
2. **分块**（`chunks.jsonl`）：沿结构节点切分，每块约 1,000 字，带少量重叠。每块带锚点
   （页码、标题路径、稳定 block / 段落 / 单元格 ID），沿用现有 citation 词汇。
3. **关键词索引**：只用纯 Go BM25，本设计不含 embedding。中文按字二元组切分，拉丁文字按词
   切分，标题权重高于正文。不引入新运行时依赖。
4. **页码映射**：没有文字层的 PDF 页标记为 `unindexed`，等 OCR 补齐。

按页分批构建，保证单次 adapter 调用不触及 60 秒子进程超时。每批不超过 §3.1 的 2 MiB 抽取
上限，并流式写盘，不在内存中持有全文。

### 4.5 OCR 规则

OCR 只在分析阶段进行，使用现有 OvisOCR2 运行时及其单次页数限制；识别出的每一页都写回索引
缓存，同一页不会识别两次。

- **定位类问题**只 OCR 检索选中的页。扫描版 PDF 在 OCR 之前不知道正文内容，因此按目录标题
  及其页码范围选页。
- **全文类请求**，以及没有可用目录的扫描件上的定位类问题，在源文件不超过现有 25 MiB 上传
  上限时，可以分批 OCR 全部页面。超过该大小的工作区文件仍只 OCR 命中页，回答中说明哪些页
  未识别。

### 4.6 存储、隔离与保留

- 索引放在所属工作区根目录的 `.sparkclaw/state/doc-index/<sha256>/` 下（`manifest.json`、
  `outline.json`、`chunks.jsonl`、`keywords.bin`、`ocr/<page>.json`）。工作区根目录本来就
  按 owner 或 session 隔离，索引的隔离随之继承；同一工作区内重复上传同一文件复用同一份
  索引。
- `.sparkclaw/state` 已经是控制目录：输出路径不能指向它，`files.search` 也会跳过
  `.sparkclaw`。读取工具目前还不拒绝该路径，B1 补上这一限制，使索引只能通过文档工具访问。
- manifest 记录源文件 SHA-256、大小、策略与解析器版本、状态（`building`、`ready`、
  `partial`、`failed`）、尝试次数和时间戳。版本变化时重建索引。
- 工作区中已没有任何文件对应某个哈希时删除该索引；LRU 清理把目录控制在每个工作区的大小
  上限内（初始 512 MiB）。清理在队列空闲时和 gateway 启动时运行。
- 使用 Postgres 状态后端时，索引仍放在工作区卷上，因为它是派生数据，丢失只需重建。

### 4.7 模型工具（read 风险，`document.read` capability）

| 工具 | 返回 | 上限 |
|---|---|---|
| `document.outline(path)` | 标题树、页码范围、规模、索引状态、未索引页 | 阶段预算 |
| `document.search(path, keywords[], top_k, section?)` | 带锚点和得分的排序片段 | `top_k x 片段` 不超过阶段预算 |
| `document.read_range(path, anchor \| pages \| chunk_ids, max_bytes)` | 该范围的正文，被截断时附续读提示 | 不超过阶段预算 |

对 `large_indexed` 文档，`files.read` 与 `pdf.extract_text` 不再返回
`strategy_deferred`，改为返回目录以及使用 `document.search` / `document.read_range`
的提示。

### 4.8 Workflow 与路由

`document.read` revision 5 对小文档保持 revision 4 的路径，并增加大文档分支。已持久化的
revision 4 Plan 恢复时保持原样。

1. `confirm_document_target` 不变。它的确定性 preflight 已经知道路径和格式，现在还会读取
   索引 manifest 和源文件大小：索引为 `ready` / `building`，或源文件超过 8 MiB 时，直接
   进入大文档分支。
2. 否则照常执行现有的 `document_read` 节点。若返回 `strategy_deferred`，评估结果转入大
   文档分支而不是阻断，因此只有解析后才知道大小的文件同样能处理。
3. 大文档分支：先执行 `document_outline`（`direct_once`，等待或构建索引），再进入检索
   阶段。检索阶段由 Fast step 模型把用户请求转成关键词（含中英文同义词），作为它自己调用
   `document.search` 时的参数，不为改写查询额外调用模型；每个阶段最多检索三轮。检索到的
   范围在阶段预算内经由供给通道进入该步骤。检索落空时退回按目录标题浏览。
4. 在最终化预算内基于检索到的范围作答，引用页码或锚点，并说明覆盖了哪些章节。

全文类请求（例如总结整本书）走章节级 map-reduce：每个目录节点生成一份摘要，按节点和源文件
哈希缓存在索引目录中，最后以这些缓存摘要为证据合并。

**编辑不在范围内。** 对 `large_indexed` 文档的 `document.edit` 请求给出明确的"不支持"答复，
源文件保持不变。`Pipeline.Edit` 仍会整篇重读源文件。

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
| A2 | 在产品与 dual-light 模型上实测中文正文、英文正文和数字表格的真实 tokenizer 每 token 字节数；把最终化的"只保留开头"改为按章节取样 | 记录实测比例；被截断的总结覆盖每一章，而不只是开头 |
| B1 | 索引、后台导入预建、分析阶段现场构建或等待、outline/search/read_range、revision 5 路由、`files.read` 指针，覆盖文字层 PDF、DOCX 和文本 | 300 页文字层 PDF 与 1 MB DOCX 在 dual-light 和产品 profile 上回答定位类问题，页码引用正确；索引就绪时不出现任何可见进度；索引仍在构建时只显示本次运行的进度行 |
| B2 | 扫描版 PDF 的分析阶段 OCR | 《计算机网络》（工作区文件）章节问题只 OCR 目录选中的页，重复提问复用结果；不超过 25 MiB 的扫描版 PDF 可以整篇总结 |
| B3 | XLSX/PPTX 结构与章节 map-reduce | 整书总结说明覆盖范围 |

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
  并为交互式读取让路。

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
