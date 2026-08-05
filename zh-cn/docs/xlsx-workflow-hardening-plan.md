# XLSX Workflow 加固方案

> 语言：[English](../../docs/xlsx-workflow-hardening-plan.md) | 简体中文

状态：拟议中的实施方案。本文描述的 runtime 行为只有在对应代码、测试、
评测和现状文档全部落地后才算已实现。

本方案在扩展更多电子表格功能前，先加固现有 XLSX 路径。范围包括四项：
XLSX 专用结构化证据、安全的 `update_row` 语义与证据绑定、失败关闭的包级
保真校验，以及可度量的操作选择。它补充[文档 Workflow](document-workflows.md)
的实施方向，但不创建第二套路由器或新的用户可见能力叶子。

实施完成后，把长期有效的契约合并进 `document-workflows.md`、
`workflow-capabilities.md` 和架构手册，再删除本方案及其英文版本。

## 目标结果

方案落地后：

- `document.read` 和 `document.edit` 获得带规范工作表、行、单元格位置的
  有界 XLSX 证据，不再依赖制表符拼接的工作簿文本；
- `xlsx.update_row` 只更新显式提供的行首单元格，不会静默清空尾部单元格
  或公式；
- 每次 XLSX 修改都绑定到授权该修改的精确读取证据；陈旧或冲突的工作簿、
  行、单元格证据会在审批前阻断；
- 成功的 XLSX 修改具有已验证的包级保真状态；含不支持或未经验证特性的
  工作簿在修改前阻断，不再生成 `package_preservation=unknown` 的输出；
- 现有 `select_edit_operation` 节点用明确的目录边界在六种 XLSX 操作间
  选择，并通过标注评测衡量真实 Fast 模型，而不是只注入预期 `entry_id`。

语义路由保持不变：

```text
owner 请求
  -> document.read r4 或 document.edit r6
  -> 确定性文档目标与 XLSX 格式绑定
  -> direct_once files.read
  -> select_edit_operation（仅编辑）
  -> 一个精确 XLSX 编辑器
  -> Policy 与 Approval
  -> 输出副本写入
  -> 类型化回读与包级保真校验
```

`document.edit` 从 revision 5 升到 revision 6，因为编辑器输入 schema、
修改语义、证据绑定、操作选择规则和成功条件都会变化。`document.read`
保留 revision 4：它的计划和用户可见边界不变，只提高内部 XLSX 证据投影精度。

## 当前缺口

### XLSX 已解析，但没有被正确供给

`xlsx_read.js` 已记录工作表名、行号、A1 地址、显示值、公式、数字格式、
隐藏状态、样式提示、合并范围、批注、超链接和嵌入图片。但
`blocksFromSheets` 把每个非空单元格压缩成文本和位置；结构化 Workflow
证据切片只投影文档操作、段落、表格和页面证据，没有工作表投影。因此 XLSX
只能退回为一段大的内容字符串；字符串超过阶段证据预算时，可能被整体省略。

操作选择和编辑器阶段因此可能只收到工作簿元数据，却拿不到选择和执行修改所需
的精确行或单元格。

### `update_row` 清除范围超过公开契约

注册描述声明 `xlsx.update_row` 替换一行开头的单元格。适配器当前先清空
`row.values` 再写入传入数组，因此未提供的尾部单元格也会被删除。现有保真
校验允许目标整行变化，且只校验传入值前缀，无法发现这种数据损失。

### 包级保真不是成功不变量

当前高层回读检查可见内容和解析器公开的 enrichment。公式身份、完整样式、
表格、图表、条件格式、透视表、外部链接、连接和其他 OOXML part 尚未被完整
覆盖。因此成功修改会把高层保真标记为已验证，但包级保真仍为未知。

### 操作选择验证了结构，却没有完成标定

决策节点能够冻结一个目录条目，并对无效选择失败关闭。但 XLSX Workflow
测试主要注入目标 `entry_id`，只能证明编排和持久化正确，不能证明 Fast 模型
能基于真实工作簿证据区分 `replace_text`、`update_cell`、`update_row`、
`insert_row`、`append_row` 和 `delete_row`。

## 不变量

实施必须保留以下现有边界：

- `capability.Catalog` 仍是唯一能力拓扑；不得添加 XLSX 路由叶子、关键词
  fallback、模型所有的 `RouteDecision` 或第二套操作映射；
- recent-document 解析和确定性 preflight 继续冻结一个受治理路径、格式、
  document ID 与输出路径；
- `document_locate_evidence` 继续使用 `direct_once`，只调用格式限定的
  `files.read`，绝不询问模型是否应该读取；
- `select_edit_operation` 仍是唯一多候选编辑器决策，并在物化前持久化一个
  精确目录条目；
- 编辑模型不能替换已绑定的路径、源哈希或目标哈希；
- 每次修改仍可逆、需要审批、写入新输出、回读校验、关联父 `DocumentRecord`，
  并在任一校验失败时清理输出；
- 完整工具观测继续保存为 artifact；模型上下文只接收有界、面向消费者的投影，
  不接收无限工作簿转储。

## 工作流 1：XLSX 结构化证据

### 契约

新增一个内部投影 `xlsx_sheet_evidence_v1`。它从
`structured_document_v1.sheets` 派生，不是新的持久化领域记录，也不是新的
ToolHub capability。

投影结构如下：

```json
{
  "schema_version": "xlsx_sheet_evidence_v1",
  "source_complete": true,
  "selection_complete": true,
  "sheets": [
    {
      "name": "Data",
      "index": 1,
      "state": "visible",
      "rows": [
        {
          "index": 12,
          "hidden": false,
          "source_hash": "sha256:...",
          "cells": [
            {
              "address": "B12",
              "column": 2,
              "value_kind": "number",
              "raw_value": 42,
              "display_text": "42.00",
              "formula": "",
              "number_format": "0.00",
              "hidden": false,
              "style_hash": "sha256:...",
              "merge_anchor": ""
            }
          ]
        }
      ]
    }
  ],
  "omitted": {
    "sheets": 0,
    "rows": 0,
    "cells": 0,
    "reason": ""
  }
}
```

`value_kind` 只能是 `blank`、`string`、`number`、`boolean`、`date`、
`error`、`formula`、`rich_text` 或 `unknown`。日期的原始值使用 ISO-8601。
公式单元格把公式和缓存/显示结果分开保存。rich text 可以作为显示证据读取，
但本 revision 不允许把它作为修改值。

每行和每个单元格都有规范 source hash。哈希输入是修改和保真校验所使用语义
字段的稳定 JSON；对象键顺序、易变解析元数据和仅用于展示的警告不参与哈希。
行哈希包含有序单元格地址、值类型、原始值、公式、数字格式、样式哈希、隐藏
状态和合并锚点。

### 选择与预算

操作选择和编辑器参数生成共用一个确定性投影器。输入冻结的 owner 请求和完整
结构化观测后，按以下顺序装配证据：

1. 工作簿清单和所有工作表名称/状态；
2. 原始 owner 请求中显式命名的工作表、A1 单元格、行或范围锚点；
3. 完整结构表示中的精确文本/值匹配；
4. 相关工作表前两个非空行，用于列名和表头上下文；
5. 当请求表达追加、末尾或最后一行时，加入最后两个非空行；
6. 目标单元格或行锚点的相邻行；
7. 按稳定工作表/行顺序填入剩余行，直到达到字节预算。

显式解析的目标必须出现。如果请求目标存在于完整表示中，却无法放进阶段预算，
投影器返回 `selection_complete=false`，决策或编辑阶段随即阻断，不能静默替换为
不相关前缀。必须公开 omitted 数量，防止模型从切片声称完整工作簿覆盖。

投影器保持 candidate-neutral：它提取电子表格锚点和值，但不选择
`document.read`、`document.edit` 或编辑器操作。

### 集成点

- 规范化工作表单元格时保留类型化 XLSX 值、公式和格式字段；
- 在 `documentReadEvidence` 中加入 XLSX 工作表证据，供普通观测使用；
- 在 `sliceDocumentStructuredEvidence` 中加入同一投影，确保
  `workflow_operation_selection` 和编辑步骤收到一致锚点；
- 完整结构化输出继续留在观测 artifact 中，供 trace/read-back 使用；
- 在现有证据供给审计事件中记录选中和省略的工作表、行、单元格数量。

无需新增配置项；投影遵守现有 `workflow_stage_evidence_max_bytes` 预算。

## 工作流 2：安全行更新与证据绑定

### `xlsx.update_row` 语义

保留公开工具名，并让执行符合现有描述：

- `values[0]` 更新第 1 列，`values[1]` 更新第 2 列，以此类推；
- 数组不能为空；
- 显式 `null` 清空对应传入单元格；
- 未传入的尾部单元格在字节和语义层面保持不变；
- 传入前缀以外的公式、样式、批注、超链接、合并、行高和隐藏状态都不是修改目标；
- 结果报告精确变更的单元格地址及类型化前后值，而不是固定的
  `changed=1` 行摘要。

删除适配器的整行清空。未来如果需要整行替换，必须注册独立 `replace_row`
操作和完整行 schema；该能力不属于本方案。

### 冻结证据

Revision 6 把编辑器调用绑定到当前 run 已完成的 XLSX 读取：

- 所有 XLSX 编辑器接收 Runtime 所有的输入工作簿 `source_sha256`；模型提供
  冲突值时，在 Policy/Approval 前阻断；
- 已存在的 `update_cell` 目标接收 `source_cell_hash`；
- `update_row`、`delete_row` 和基于位置的 `insert_row` 接收精确锚点行的
  `source_row_hash`；
- `append_row` 接收 `source_sheet_hash`，其中包含计算追加位置所用的最后结构行；
- 缺失的必要 source hash、其他节点/run 的 hash，或与编辑器新鲜回读不一致的
  hash，都会在不创建审批的情况下阻断。

这些绑定与输入/输出路径一样由 Runtime 所有。模型可以选择新值，但不能编写或
替换证据身份。

执行前从读取证据规范化工作表名。匹配可以不区分大小写，但适配器始终接收工作簿
中的精确工作表名。单元格地址规范为大写 A1 形式。

### 类型化修改校验

不得只把请求值与 ExcelJS `cell.text` 比较。校验复用工作流 1 的类型化单元格
投影：

- 原始标量按类型和值比较；
- 显示文本和数字格式是独立字段；
- 写入公式需要未来的显式公式操作；标量 `update_cell` 只有在请求精确定位该公式
  单元格，且审批展示这项破坏性差异时，才允许替换公式；
- `update_row` 校验每个传入单元格，并校验尾部单元格 before hash 不变；
- 保真校验不再把目标行所有未修改单元格整体排除。

把 `update_row` 从整行替换改为仅更新前缀，是用户可见缺陷修复，必须使用独立
commit 和 release note。

## 工作流 3：失败关闭的 XLSX 包级保真

### 包清单

在 `internal/document` 下新增 XLSX 包检查器。它集中读取 OOXML ZIP，并返回
供 preflight 和编辑后校验共同使用的类型化清单。工具脚本不得创建第二套特性分类。

清单记录：

- content types 和 relationship graph；
- worksheets、shared strings、styles、themes、workbook properties 以及
  calc chain 是否存在；
- 批注、超链接、合并范围、drawings 和图片；
- 表格、图表、条件格式、数据验证、pivot/cache、slicer、外部链接、连接、
  embedded object、custom XML、宏、签名和保护/加密标记；
- 操作必须原样保留的 opaque part 原始哈希；
- 已知可变 worksheet/style part 的规范语义指纹。

### 能力门禁

定义一张由代码所有、以“特性类别 + 操作”为键的支持表，作为当前 ExcelJS 路径
能否安全 round-trip 某项包特性的唯一事实来源。

初始策略保持保守：

- 只有 fixture 证明每个受影响操作都能读取、写入、回读并保持包级保真后，特性
  才标记为 `verified`；
- 未知、不支持、加密、签名、含宏、外部链接、连接、pivot、slicer、embedded
  object 或未经验证的图表/表格特性，在审批前阻断修改；
- 只读 Workflow 仍允许执行，并报告 partial/unsupported 覆盖；
- 解析器缺失或 relationship 数据损坏是显式包错误，不能只警告后成功。

支持表由实现注册派生，不在 parser、editor 和 Workflow package 之间同步名称列表。

### 差异校验

编辑后，用操作 allowlist 比较前后包清单：

- `update_cell` 只允许目标单元格类型化值/公式状态、所在 worksheet 序列化和
  必要计算元数据变化；
- 前缀 `update_row` 只允许传入单元格变化；
- insert/delete/append 只有在显式支持并验证时，才允许目标工作表行结构、受影响
  merge 和公式引用变化；
- 批注、超链接、图片、opaque part、relationship、content type、无关工作表、
  样式和 workbook property 必须保持不变，除非操作声明并验证该差异。

任何未报告差异都会删除输出并返回 `preservation_mismatch`。成功 XLSX 修改设置
`package_preservation=verified`，不能再返回 `unknown`。change summary 包含
类型化目标差异、已检查特性类别和只读覆盖说明。

## 工作流 4：操作选择边界与评测

### 目录元数据

保留现有精确格式/操作 capability descriptor，把通用 XLSX 目录说明替换为
操作专用元数据：

| 操作 | 使用条件 | 禁止使用条件 |
|---|---|---|
| `replace_text` | owner 提供明确旧/新文本，并希望修改匹配的文本单元格。 | 已明确单元格/行位置、值具有类型，或属于结构变化。 |
| `update_cell` | 只修改一个单元格，或证据定位行中的一个字段。 | 同一行多个单元格变化，或插入/删除/追加行。 |
| `update_row` | 修改一个证据绑定现有行的多个行首单元格，并保留尾部。 | 创建新行、删除行或替换任意工作簿文本。 |
| `insert_row` | 在一个明确现有行之前或之后新增行。 | 请求只表达末尾/追加且没有位置锚点，或修改现有行。 |
| `append_row` | 在一个工作表最后结构行之后新增行。 | 已明确 before/after 锚点，或修改现有行。 |
| `delete_row` | owner 明确删除一整条证据绑定行。 | owner 只清空单元格、删除文本或删除整个工作簿。 |

把相同区分加入 `documentEditProfile.DecisionRules`。可通用的规则保持格式中立，
XLSX 细节来自目录条目。不得向语义路由器增加关键词，也不得把操作冻结进
`RouteDecision`。

### 评测语料

新增由现有 eval 基础设施消费的带标签 XLSX 操作选择语料，至少包含：

- 每种操作 8 条直接/改写用例，中英文各半；
- 显式 A1、行号、表头/值、before/after、工作表末尾和精确旧/新文本表达；
- 单元格与行更新、插入与追加、清空单元格与删除行、文本替换与类型化更新等
  sibling confusion；
- 否定、引用、排障、不支持操作、目标歧义和删除整个文件等 hard negative；
- 小型与接近预算的工作簿证据，以及公式、格式化数字、隐藏行、合并单元格和
  多工作表场景。

确定性单元测试继续验证目录 scope、严格 JSON、持久化决策、重试和失败关闭。
配置 Fast 模型的评测则在不注入 `MOCK_OPERATION_SELECTION_RESPONSE` 的情况下，
执行真实 `workflow_operation_selection` prompt，并记录模型/profile 指纹、
选中条目、重试和原因。

发布门槛：

- `delete_row` 和所有不支持/歧义用例达到 100% 正确选择；
- 带标签 holdout 的整体精确操作准确率至少 95%；
- 跨格式选择为零，空/无效选择后的修改为零；
- 现有 document-vs-conversation、read-vs-edit 和文件生命周期路由语料无回归。

未达到门槛的 prompt 或目录修改不得发布。路由 fusion 权重和阈值不属于该评测，
没有独立标定证据时不得改变。

## 实施文件映射

| 区域 | 主要文件 | 职责 |
|---|---|---|
| XLSX 解析与适配器 | `internal/toolhub/scripts/xlsx_read.js`、`xlsx_structure.js` | 保留类型化值并实施只更新前缀的行更新。 |
| 文档契约 | `internal/document/normalize.go`、`preservation.go`、新增 XLSX 包检查器 | 规范哈希、类型化比较、特性门禁和包差异。 |
| 工具 schema 与执行 | `internal/toolhub/toolhub.go`、`registry.go`、`document_tools.go`、`document_workflow.go` | 精确 schema、目录元数据、可信 source binding 和输出细节。 |
| Workflow 证据与绑定 | `internal/agent/tool_result_adapter.go`、`workflow_evidence.go`、`workflow_profiles.go`、专用 XLSX binding helper | 有界工作表投影、r6 绑定和选择规则。 |
| 验证 | `internal/document/*_test.go`、`internal/toolhub/*_test.go`、`internal/agent/*_test.go`、eval fixture | 安全、包级一致、选择准确率和端到端行为。 |
| 现状文档 | `document-workflows.md`、`workflow-capabilities.md`、`architecture.md` 及中文镜像 | 只发布通过所有门槛的行为。 |

预期不需要 Store interface 变化。如果实施中发现新的持久状态需求，必须另行评审，
并同时实现 memory、file 和 PostgreSQL backend。

## 交付顺序

每项行为变化使用独立 topic 和 commit：

1. 先增加 XLSX 工作表证据的失败 fixture 和投影测试；
2. 实现 `xlsx_sheet_evidence_v1` 与阶段证据供给；
3. 增加尾部单元格/公式和陈旧证据失败测试；
4. 修复 `update_row`、加入可信哈希，并标记该缺陷行为变化；
5. 增加包特性 fixture 和保守 preflight manifest；
6. 执行支持门禁和编辑后包差异校验；
7. 增加操作专用目录元数据和确定性决策测试；
8. 增加真实模型标注 eval，并达到发布门槛；
9. 更新英中文现状文档、删除已完成方案，并检查最终 diff 中的生成物和测试产物。

机械证据管道、修改行为、包级门禁和 prompt 元数据不得合并在一个 commit 中。

## 验证矩阵

缺少下列任一项均不算完成：

- XLSX 投影测试覆盖多工作表、保留大小写的名称、A1 锚点、行号、类型化值、
  公式、数字格式、隐藏行/列、合并范围、批注、超链接、图片和预算省略计数；
- 阶段证据测试证明工作簿尾部显式目标在默认 8000 字节预算下仍会出现；
- 修改测试证明 `update_row` 保留尾部值、公式、样式、批注、超链接和合并；
- 陈旧/缺失/冲突 source hash 测试证明不会创建审批；
- 格式化数字、Boolean、日期、空值、错误值和公式单元格校验；
- 每种支持和阻断特性的包 fixture，包括拒绝输出的清理；
- 真实操作选择评测及 sibling/hard-negative 覆盖；
- `document.edit` r6 审批、恢复、输出 lineage 和交付端到端测试；
- 现有 DOCX、PPTX、PDF、文本、文档读取、路由和工具注册测试保持绿色。

实施过程中运行按比例检查，最后执行完整门禁：

```bash
npm run setup:document-tools
cd services/gateway
go test ./internal/document ./internal/toolhub ./internal/agent ./internal/gateway
go test ./...
go vet ./...
cd ../..
bash scripts/run-eval.sh
```

同时运行 CI 的双语文档与本地链接检查。单次 smoke call 不能作为模型质量结论；
必须保留标注语料、模型指纹、重复结果和失败项作为评审证据。

## 验收标准

只有满足以下条件才算方案完成：

1. 两个文档阶段都接收规范 XLSX 工作表/行/单元格证据，显式请求目标不会因字节
   预算而消失；
2. `update_row` 只改变传入单元格，所有 XLSX 修改在审批前拒绝陈旧或冲突证据；
3. 每个成功 XLSX 输出都报告已验证包级保真，不支持的包在修改前阻断且不留下
   输出 artifact；
4. 真实 Fast 模型 holdout 达到选择门槛，控制流测试不再是操作准确率的唯一证据；
5. `document.edit` r6 已有英中文说明，旧 r5 run 不会在新修改契约下恢复，且所有
   按比例与完整检查为绿色。

## 非目标

- 超出现有完整读取上限的大工作簿分块或流式处理；
- 批量/范围编辑、列操作、工作表创建/重命名/删除、公式编写或样式编写；
- WebChat 专用 diff 展示或审批 UI 重构；
- r6 Workflow 路径以外的陈旧通用 fallback 文案清理；
- 路由 fusion 权重或阈值变化；
- 第二个电子表格库或通用任意 OOXML patch 工具。

这些项目必须在本安全基线完成后，基于独立证据和契约另行推进。
