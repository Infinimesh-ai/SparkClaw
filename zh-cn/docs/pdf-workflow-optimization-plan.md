# PDF Workflow 优化实现记录

> 语言：[English](../../docs/pdf-workflow-optimization-plan.md) | 简体中文

状态：已于 2026-08-05 实现并通过确定性验证。

本文记录普通、扫描及混合文字层 PDF Workflow 的优化项 1、3、4、5、6。第 2 项
分页与大文件策略继续明确排除。现有源文件大小边界和 finalization 的 8000 rune
摘录仍保持有界；本次工作只保证覆盖声明真实，不增加页面窗口、continuation token
或文档索引。

稳定产品契约也同步记录在[文档 Workflow](document-workflows.md)、
[意图路由](intent-routing.md)和
[Workflow 能力矩阵](workflow-capabilities.md)中。

## 已交付边界

- `document.read#read` 负责对一个受治理 PDF 执行读取、总结、按页提取文字和扫描页
  OCR。OCR 继续作为内部不可信证据，不是 route、Workflow leaf 或 Model Router lane。
- `document.edit#transform` 在确定性文档 grounding、操作选择、审批、输出副本写入和
  重读验证后执行 `extract_pages`、`delete_pages`、`rotate_pages` 和 `split`。
- ToolHub definition、executor、Python adapter、Workflow directory 和用户可见能力矩阵
  都不再包含 `merge`。只有未来完成多文档 grounding 与 lineage 设计后才可重新引入。
- OCR 证据不能授权 mutation。原 PDF 始终保持不变，每次 transform 写入一个或多个
  受治理输出副本。

## 真实的页级读取

`pdf.extract_text` 使用类型化 workspace-read outcome adapter。最终结构化结果直接
报告页面证据和覆盖状态，不再通过 `pdf.*` 工具名前缀推断 read 语义。

每一页都暴露 `text_source`、`text_status`、确定性的原生文字质量证据，并在适用时
保留 OCR provenance。文档统计包含：

```json
{
  "read_complete": false,
  "coverage_status": "partial",
  "page_status_counts": {
    "native": 2,
    "ocr_succeeded": 1,
    "ocr_failed": 1
  },
  "missing_page_indexes": [4],
  "scanned_unsupported": true
}
```

标准页面状态为：

| 状态 | 含义 | 已覆盖 |
|---|---|---|
| `native` | 版本化原生文字分类器接受了提取文字层 | 是 |
| `ocr_pending` | OCR enrichment 完成前的内部 parser 状态 | 否 |
| `ocr_succeeded` | 经校验的 OCR 提供了可用页面证据 | 是 |
| `ocr_disabled` | 页面需要 OCR，但 runtime adapter 已禁用 | 否 |
| `ocr_failed` | OCR 失败、超时、busy 或没有返回可用文字 | 否 |
| `render_failed` | 页面无法在 renderer 契约内栅格化 | 否 |
| `budget_omitted` | 页面超出现有 OCR 页数或渲染字节预算 | 否 |

只有每一页最终都是 `native` 或 `ocr_succeeded` 时，`read_complete` 才为 true。
缺失页码经过排序且精确。至少有一页已覆盖的 partial result 可以进入 finalization；
完全没有可用页面证据时结果为 unavailable，并明确 block，不让模型补写内容。

归档 tool output 仍是 finalizer 的正文来源。最终证据先给出精简 coverage manifest，
并把页面覆盖与 `model_evidence_truncated` 分开。当覆盖不完整时，finalizer 必须列出
缺失页面及有界原因类别，而且只能总结已覆盖证据。

## 确定性扫描页分类

PDF adapter 在决定是否渲染前，以 `pdf_native_text_quality_v1` 对每页分类。该分类器
完全本地且确定性运行，检查 trim 后字符及有效字符数量、replacement/control 字符、
重复 glyph run，以及页面图片上只有稀疏文字的情况。

分类结果为 `usable`、`empty` 或 `degraded`，并带 reason code 和有界数值 feature。
`usable` 页面保留原生文字；`empty` 和 `degraded` 页面进入有界的
`pdf_page_render_v1` OCR 路径。

对于 degraded 混合页，原生文字 block 与 OCR block 保留独立 provenance，页面报告
`text_source=native+ocr`。只有规范化后完全相同的 block 才会合并。空结果或只含
image tag 的 OCR 输出可作为已校验 no-text 结果进入缓存，但不能算作可用 PDF 文字，
因此页面仍以 `ocr_failed` 和 `no_usable_text` 报告。

## 精确 Transform 契约

`select_edit_operation` 持久化唯一 directory entry 后，Runtime 只暴露该操作的 strict
schema：

| 操作 | 必填参数 | 约束 |
|---|---|---|
| `extract_pages` | `operation`、`path`、`pages`、`output_path` | `pages` 是非空、唯一、正整数且从 1 开始的数组 |
| `delete_pages` | `operation`、`path`、`pages`、`output_path` | 同一页码契约；输出不能为空 PDF |
| `rotate_pages` | `operation`、`path`、`pages`、`rotation`、`output_path` | rotation 只能是 `-270`、`-180`、`-90`、`90`、`180` 或 `270` |
| `split` | `operation`、`path`、`output_path` | 拒绝 `pages`、`rotation` 和 `inputs` |

无关字段、重复页、非整数页、零/非法旋转，以及 qualifier 与 operation 矛盾都会在审批前
失败。越界页码需要读取源 PDF 的真实页数，因此由获批后的受治理执行拒绝，但拒绝发生在
创建或写入输出之前。页面选择类 transform 保持源文件顺序。执行后校验原文件 hash，并
重新读取每个输出，检查操作专属 preservation delta。

## 路由校准

路由继续保持现有 candidate 职责：

- `document.read#read` 负责读取、总结、识别扫描文字和按页提取文字。
- `document.edit#transform` 负责通过页面导出、删除、旋转或拆分生成变更或派生 PDF。

双语语义语料现已覆盖页面文字与页面文件导出、扫描识别、否定、引用、已完成动作、
故障排查、最近文档追问、merge 请求，以及 `提取 report.pdf 的第 3 页` 等歧义表达。
歧义请求进入澄清；引用、否定、历史描述或故障排查文字不会授权 transform。没有新增
关键词 fallback，也没有修改 candidate ID、fusion weight 或 threshold。

## OCR Runtime 就绪状态

公共 adapter 投影继续隐藏 endpoint 和 host allowlist，同时区分请求配置与实际构造状态：

```json
{
  "configured_enabled": true,
  "adapter_ready": false,
  "runtime_status": "degraded",
  "reason_code": "constructor_failed",
  "provider": "openai-http",
  "model": "ATH-MaaS/OvisOCR2"
}
```

`runtime_status` 为 `disabled`、`ready` 或 `degraded`。`ready` 只表示 adapter 已构造且
可以接收工作，不证明首次推理已经预热，也不保证下一次 provider 请求成功。fresh call
之后会更新有界 last-call status、reason 和 timestamp，但不会改写 configured state。

## Owner 范围缓存与 Provenance

OCR 缓存为进程内、owner 隔离，并固定限制为 128 个 entry 和 32 MiB。它没有新增 Store
方法或第四套持久化契约；Gateway 重启会自然清空缓存。

逻辑 key 只包含：

```text
准备后图片 SHA-256
+ 配置 provider 与精确 model
+ OCR prompt/response contract version
+ render/preprocessing version
+ output-normalization version
```

key 不含 path。owner ID 是内部查找 scope 的一部分，因此不同 owner 即使提交完全相同
的字节也不会共享 entry。只缓存成功结果和已校验 no-text 结果；busy、timeout、cancel、
render 或 provider failure 不进入缓存。

同一 owner 与逻辑 key 的并发 miss 会合并。每个消费者报告 `hit`、`miss`、`coalesced`
或 `bypass`。fresh adapter call 会生成真实 `ModelCall` ID，并在当前 session/run 下通过
现有 Store 持久化 operation=`document_ocr` 的 `ModelCall`。cache hit 不创建虚假调用，
而是引用 cache record 及其原始 model call。

## Audit、Trace 与指标

OCR audit 只记录有界 status、reason code、cache result、duration、queue wait、页码、
classifier/preprocessing version、model-call/cache ref 及 source/prepared hash，不记录 OCR
Markdown 或页面文字。现有 trace export 会带出这些 owner 范围 audit 和 model-call record；
受治理的归档 tool output 继续作为正文证据。

`/metrics` 暴露低基数的进程指标，不把 path、正文、hash、session ID 或 run ID 作为 label：

- `sparkclaw_document_ocr_pages_total{status,cache_result}`
- `sparkclaw_document_ocr_duration_seconds{provider,model,status}`
- `sparkclaw_document_ocr_queue_wait_seconds{provider,model}`
- `sparkclaw_document_ocr_cache_total{result}`
- `sparkclaw_pdf_page_classifications_total{classification}`
- `sparkclaw_pdf_reads_total{coverage}`

## 验证证据

确定性测试覆盖：

- 原生、纯扫描和稀疏混合文字层的真实 PDF fixture；
- OCR success、disabled、constructor-degraded、failed、timeout、busy、trivial output、
  render failure 和页面预算遗漏；
- complete、partial、unavailable coverage 及有界 final evidence；
- owner 隔离、cache hit/miss、并发合并、失败不缓存、no-text 缓存、version invalidation
  和 cache 上限；
- fresh `ModelCall` 持久化、cache-hit provenance、audit 正文排除、readiness 投影及六组指标；
- 四种 transform、操作专属 schema、qualifier 冲突、非法页码/rotation、merge 缺失、输出
  副本、重读、原文件保真、审批恢复及路由边界。

普通确定性 gate 使用 fixture OCR adapter 和生成的 PDF，不加载 OvisOCR2。live OvisOCR2
评测继续单独调度，因为共享模型加载不得与其他文档格式工作重叠。

## 保留边界

- 第 2 项没有实现。不存在页面窗口 API、continuation token、retrieval index 或新的
  大文件策略。
- OCR 不会让不受支持的 PDF asset、annotation、form、signature、extension 或布局重写
  获得完整保真。
- PDF merge 继续不可用，直到有序多文档 grounding、冻结 hash、多 parent lineage、审批、
  冲突和 preservation 契约被统一设计。
- 缓存内容刻意保持临时。持久 model-call 和 audit provenance 通过现有 Store backend
  保留；OCR 正文只存在于受治理 tool evidence，不进入 audit field。
