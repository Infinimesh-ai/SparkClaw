# Issue #18 文档操作契约设计

> 语言： [English](../../docs/issue-18-document-operation-contract-design.md) | 简体中文

> 状态：针对 [issue #18](https://github.com/Infinimesh-ai/SparkClaw/issues/18)
> 的已实现且已验证设计，基于 `main` 的 `74f561c`。该 issue 没有评论；时间线只有来自
> `096f1cf` 的一次引用，后续 `d3e4245` 已修复 alias-aware 且确定性的错误包装查找。
> Owner 于 2026-08-18 确认全部产品边界，并确认当前部署不存在使用旧参数契约的非终态
> document edit。

## 决策摘要

SparkClaw 在 `internal/app` 中维护唯一一份有序的文档 format-to-operation catalog。
ToolHub provider、document lifecycle policy 与 Agent routing policy 都从该 catalog 派生
operation key set 和顺序，同时继续拥有各自 package-specific behavior。每个本地 registry
constructor 必须拒绝缺失实现、额外实现、未知格式或重复 operation。测试比较精确集合，而不再只
验证一个 registry 是否是另一个的子集。

`document.EditRequest.SourceSHA256` 是唯一的 whole-source hash 类型化入口，
`document.Pipeline.Edit` 是唯一把它与新鲜 source metadata 比较的组件。ToolHub format
provider 不再执行自己的 whole-source hash 比较。Agent 仍负责证明 runtime-bound hash 来自当前
Workflow localization observation；这项 provenance check 与 filesystem freshness enforcement
不是同一职责。

公开参数名确定为 `source_sha256`，并在 `internal/app` 中只定义一次。`source_evidence` 与
`evidence_targets` 是仅供 Agent runtime 使用的 provenance，不再作为公开 ToolHub input。
Source SHA-256 只要求用于 DOCX、XLSX 和 PPTX operation。本次更新执行 hard contract
cutover，不增加 legacy decoder 或 persisted-state migration。

## 实现前缺口

同一套可执行 operation matrix 目前重复在四处：

- `document_{docx,xlsx,pptx,pdf}_registry.go` 中的 ToolHub provider，以及
  `document_format_registry.go` 中的 text provider；
- `document/format_policy.go` 中的 document lifecycle policy；
- `agent/workflow_document_format_policy.go` 中的 Agent routing policy；
- document 与 Agent registry test 中的 literal expected map。

现有单向 parity test 只能证明每个 ToolHub provider operation 都有 document preservation
policy。它无法发现额外 document policy、Agent drift、顺序漂移，或只出现在一个 registry 中
的 format。`096f1cf` 已让缺少 lifecycle policy 的 runtime 路径 fail closed，但 drift 仍可能在
到达 runtime 后才被发现。

Whole-source hash 行为同样不一致：

| Format/path | 参数 | 当前 enforcement |
|---|---|---|
| DOCX | `source_document_sha256` | ToolHub validator 比较 inspected metadata；provider 不填写 `EditRequest.SourceSHA256` |
| XLSX | `source_sha256` | ToolHub 检查是否存在，随后 `Pipeline.Edit` 比较 provider 提取的 hash |
| PPTX | `source_document_sha256` | ToolHub 比较 inspected metadata，随后 `Pipeline.Edit` 再次比较 |

共享 `office.replace_text` schema 同时公开两个 hash 名。它还公开 `source_evidence` 与
`evidence_targets`，但只有 Agent Workflow binding policy 会解释这两个字段。Direct ToolHub
caller 可以传入它们，但 ToolHub 既不能建立也不会校验其 provenance。

两个相关缺陷已经修复，本文不重复实现：

- `096f1cf` 让 lifecycle policy miss fail closed，并新增现有 ToolHub-to-document coverage
  test；
- `d3e4245` 让 `errorWrapper` 支持 `ToolAliases`、按确定顺序遍历 format，并避免第二次
  DOCX source-file inspection。

## 目标

- 每个受支持的可编辑 `(format, operation)` pair 只定义一次。
- 为 directory materialization 与测试保留确定性的 operation 顺序。
- 当 package behavior 未精确覆盖 canonical catalog 时，三个 registry 都在 construction 或
  test 阶段失败。
- 使用一个 whole-source hash 参数和一个类型化 pipeline 字段。
- 对每个要求 localization evidence 的 operation，只在 `Pipeline.Edit` 中检查一次
  whole-source freshness。
- 每个公开的 ToolHub input 都必须有 ToolHub 自己拥有的语义与校验。
- 保留现有 operation selection、approval、output-copy、preservation、error mapping 与
  post-approval safety 行为。

## 非目标

- 新增文档格式或编辑 operation。
- 把三个 behavior registry 合并成跨 package mega-registry。
- 把 parser、editor、preservation hook、routing prompt 或 evidence projector 移入
  `internal/app`。
- 改变文档 mutation approval policy。
- 未经单独确认就把 source-hash requirement 扩展到 plain-text replacement 或 PDF
  transform。
- 删除 paragraph、cell、row、sheet、shape 或 page 等 target-level evidence。

## Canonical Catalog

### App Contract

`internal/app` 提供按副本返回、不可被 consumer 修改、顺序确定的 catalog。具体 API 可以使用
等价命名，但契约如下：

```go
type DocumentOperationSpec struct {
    Name                 string
    RequiresSourceSHA256 bool
}

type DocumentFormatOperationSpec struct {
    Format     string
    Operations []DocumentOperationSpec
}

func DocumentFormatOperationSpecs() []DocumentFormatOperationSpec
func DocumentOperationsForFormat(format string) ([]DocumentOperationSpec, bool)
func DocumentOperationFor(format, operation string) (DocumentOperationSpec, bool)
```

返回 slice 必须是副本，consumer 不能修改全局 authority。Format 和 operation name 是 canonical
lowercase wire value。Operation-name constant 与 catalog 放在一起，consumer 不需要为了行为
switch 而重新手写 literal。

初始 catalog 包含当前五种可执行 pipeline format：

| Format | 有序 operations | 要求 source SHA-256 |
|---|---|---|
| `text` | `replace_text` | 否 |
| `docx` | `replace_text`, `replace_paragraph`, `insert_paragraph`, `delete_paragraph`, `set_text_style` | 每项都要求 |
| `xlsx` | `replace_text`, `update_cell`, `insert_row`, `delete_row`, `update_row`, `append_row` | 每项都要求 |
| `pptx` | `replace_text`, `add_slide`, `update_slide`, `update_deck`, `duplicate_slide`, `delete_slide` | 每项都要求 |
| `pdf` | `extract_pages`, `delete_pages`, `rotate_pages`, `split` | 否 |

`image` 仍是没有可执行 document edit operation 的 Agent routing format，因此不进入该 catalog。
这样不会错误宣称 ToolHub 或 document lifecycle registry 拥有 image editor。

### Consumer Construction

每个 package 继续拥有自己的行为，并通过 canonical `(format, operation)` key 接入 app
catalog：

- ToolHub 提供 parser、editor、target builder、tool name/alias、result projection 和 error
  mapping。`OperationOrder` 直接复制 canonical catalog 的顺序。
- `document` 提供 normalization、lifecycle、preservation 与 package verification hook。
- Agent 提供 route grounding、schema projection、evidence slicing、argument binding、approval
  revalidation 与 result projection。

每个 registry constructor 执行 exact join：

1. 按顺序遍历 canonical format 与 operation。
2. 为每个 key 解析 package-owned behavior。
3. behavior 缺失时 panic，并给出 package 与缺失 key。
4. 本地 behavior 中存在 catalog 之外的 pair 时拒绝构建。
5. 拒绝重复 format、重复 operation 与空的 normalized key。

这能保持 fact single-sourced，同时不让 `app` 依赖实现 package，也不把 function hook 放入共享
contract。

## Whole-Source Hash Contract

### 职责

`internal/app` 定义唯一一个公开 ToolHub argument-name constant。推荐值为：

```go
const DocumentSourceSHA256Argument = "source_sha256"
```

Tool definition、Agent runtime-bound argument list、binding code、approval argument、test 与文档都
使用该 constant 或其 wire value。ToolHub provider 不再拥有 per-format
`SourceSHA256 func(map[string]any) string` extractor。

`executeDocumentOperation` 为每个 operation 把 canonical argument 写入
`EditRequest.SourceSHA256`。`Pipeline.Edit` inspect format 后解析 canonical operation spec，随后：

1. 当 `RequiresSourceSHA256` 为 true 时拒绝缺失 hash；
2. 非空 hash 不匹配时，以 `CodeResourceInvalid`、`StageConstrain` 拒绝；
3. 然后继续现有 read、target localization、output-path validation 与 apply stage。

Apply 前已有的再次 inspection 保留。它用于防止当前 Pipeline call 内文件发生 TOCTOU 变化，
不是对 Workflow-provided hash 的第二次验证。

### Agent Provenance

Agent 从唯一完成的 localization read 绑定 canonical hash，并拒绝与该 persisted observation 冲突
的 caller/model value。这项检查证明 provenance 并冻结 approval argument；它不 inspect
filesystem，也不替代 `Pipeline.Edit` freshness enforcement。

Post-approval revalidation 继续检查 exact approval argument，以及安全恢复所需的 target-level
evidence。审批等待期间 source file 发生变化时，最终必须由 canonical pipeline hash check 在
mutation 前失败。Format-specific code 不得再次添加 whole-file hash comparison。

## 仅限 Workflow 的 Evidence

`source_evidence` 标识精确的 localization call、run、node、scope revision、path 与 operation。
`evidence_targets` 把 Office text replacement 绑定到 target block。它们是 Agent provenance
record，不是 direct ToolHub caller 能够建立的输入。

因此推荐：

- 从公开 ToolHub input-schema properties 与 required list 中删除这两个字段；
- 不让它们进入 model-visible schema projection 和 directory contract；
- Agent 在 approval 前根据 persisted Workflow state 派生或覆盖这些值；
- 仅作为 Agent-owned provenance 在 execution/resume 前校验；
- ToolHub 绝不能因为任一字段而增加 authority 或跳过 target validation。

Direct ToolHub 和 manual invocation 依赖可以自我防御的 ToolHub contract：canonical source hash、
operation-specific target field、需要时的 target-level hash、exact locator resolution、
output-copy boundary 与 Pipeline preservation check。传入未知且形似 provenance 的字段不会产生
效果。

如果 owner 要求这些字段继续公开，ToolHub 必须定义完整 schema，从 Store 重建其 authoritative
source，并在每次调用中拒绝 forged、stale、cross-run 或 incomplete value。这会让 ToolHub 耦合
Agent-owned Workflow provenance，因此不推荐。

## Alias 与错误解析

以 `d3e4245` 行为为 baseline：error lookup 同时使用 primary tool name 与 `ToolAliases`，并按
稳定顺序遍历 format。Catalog construction 还要拒绝重复的 `(accepted tool name, operation)`
error-wrapper match，避免用确定性遍历掩盖 ambiguous registration。

`office.replace_text` 继续作为 DOCX、XLSX 的共享公开 tool，以及 PPTX 的 accepted alias。
本 issue 不重命名 tool，也不改变 capability qualifier。

## 失败语义

- 未知 catalog pair：registry construction 阶段失败；持久化的未知 pair 在 runtime 以
  `CodeMutationUnsupported` 失败。
- 缺少必需 source hash：在 read/apply 前以 `CodeResourceInvalid`、`StageConstrain` 失败。
- Source hash 不匹配：在 read/apply 前以 `CodeResourceInvalid`、`StageConstrain` 失败。
- Workflow provenance 不匹配：Agent 在 approval 或 resume 前阻断。
- Target evidence 不匹配：保留现有 format-specific typed failure。
- Alias registration 有歧义：ToolHub registry construction 阶段失败。

Raw internal diagnostic 继续只进入 audit；公开失败仍由已有稳定 ToolHub error mapping 控制。

## 兼容与发布

Owner 确认当前部署不存在使用旧参数契约的非终态 document edit、pending document approval 或
可恢复 document ToolCall。已经完成的 message、run、approval、audit record、artifact、
document record 与 output file 不需要迁移。

因此 rollout 使用 hard contract cutover：

1. Tool schema、Agent binding、approval argument 与 direct ToolHub call 只公开、生成并接受
   `source_sha256`。
2. 不增加 `source_document_sha256` decoder 或 store migration。
3. 如果意外遇到非终态 legacy document operation，它以明确的 retired/unsupported-contract
   failure 终止，不能重新解释为新 operation。
4. 失败时保留现有 run、approval、ToolCall、message 与 audit history，绝不删除或修改 source/
   output document。
5. 已完成的历史 record 继续作为 history 可读；它们不是可恢复 execution input，也不重写。

## 验证

Focused test 必须证明：

- app catalog 顺序稳定、返回 defensive copy，constructor test 拒绝 duplicate/empty entry；
- ToolHub、document 与 Agent operation set 都与 canonical catalog 精确相等，不允许 extra；
- synthetic missing/extra package behavior 会 panic，并指出精确 key；
- `OperationOrder`、directory capability、definition、editor、preservation policy 与 Agent
  operation policy 覆盖相同 pair；
- 每个要求 hash 的 Office edit 都通过 `Pipeline.Edit` 拒绝缺失或 stale canonical source
  hash；
- DOCX、XLSX、PPTX 不再执行 provider-owned whole-source hash comparison；
- 共享 `office.replace_text` schema 只公开一个 hash name，且不公开 Workflow-only provenance
  field；
- direct ToolHub invocation 无法通过提供 `source_evidence` 或 `evidence_targets` 获得额外行为；
- 三种 Office format 的 Workflow binding、approval persistence、冲突 model argument 拒绝和
  post-approval resume 仍正确；
- PPTX 通过 `office.replace_text` alias 调用时保留 typed error mapping；
- synthetic 非终态 legacy operation 在 memory、file、PostgreSQL store 中一致失败，不删除
  history，也不触碰文件。

Focused test 之后，执行项目 SOP 中的完整 Gateway build/test/vet、document-tool setup 与测试、
document routing/editing golden eval、默认 file backend validation 和双语文档检查。

## 实现切片

1. 新增 ordered app catalog、operation constant、lookup helper 与 test。
2. 把 document lifecycle policy construction 和测试改为 exact catalog join。
3. 转换 ToolHub provider/order 与 parity test，保留 alias 和 directory metadata。
4. 转换 Agent operation policy 与 exact-set test。
5. 引入 canonical hash argument，把 required/mismatch enforcement 移到 `Pipeline.Edit`，删除
   provider-owned whole-source check。
6. 应用选定的 evidence-field 与 compatibility 决策。
7. 更新 current-state architecture/document workflow 文档并执行完整验证矩阵。

机械性的 catalog consumption 与 hash/evidence 行为变化应分成不同 commit。

## 已确认的 Owner 决策

Owner 于 2026-08-18 确认：

- canonical 公开 hash argument 使用 `source_sha256`；
- 从公开 ToolHub schema 删除 `source_evidence` 与 `evidence_targets`，它们只保留为 Agent
  runtime provenance；
- source SHA-256 只要求用于 DOCX、XLSX、PPTX operation；本 issue 不改变 text 与 PDF
  行为；
- 使用 hard contract cutover，不增加 legacy decoder，因为当前部署不存在使用旧契约的非终态
  document edit；意外 legacy work 明确失败，同时保留 history 与文件。

## 实现状态

已于 2026-08-18 实现并完成验证。Canonical catalog、registry 精确 join、统一 source-hash
enforcement、仅限 Runtime 的 provenance 边界和 hard cutover 已同步到当前架构与文档工作流
指南。完整 Gateway build/test/vet、WebChat 测试/构建、双语文档检查、默认 file backend 和
47 个隔离 golden case 均通过。

## 待决事项

无。
