# PPTX Workflow 优化实施记录

> 语言：[English](../../docs/pptx-workflow-optimization-plan.md) | 简体中文

状态：已于 2026-08-05 实现并完成确定性验证。本文记录随 `document.edit` revision 6 一起
交付的前六项 PPTX Workflow 优化。当前用户可见契约维护在
[文档 Workflow](document-workflows.md) 与 [Workflow 能力矩阵](workflow-capabilities.md) 中。

仍有一个跨格式集成项不属于本次 PPTX 改动：共享 `document.Pipeline` 尚不能分别保留
`reread` 与 `preserve` timeout stage。PPTX 边界已经提供统一端到端 deadline、稳定错误码、
子进程取消、清理以及 adapter 层 `read`/`apply` stage，且没有改变其他文档格式行为。

## 已交付架构

PPTX 继续位于既有 `document` capability branch 下。没有新增 PPTX 顶层 route、关键词 fallback、
通用演示文稿 mutation tool 或第二份 capability map。Catalog revision 为
`2026-08-05.v16`，当前 edit profile 为 revision 6。

```text
semantic document.edit match
  -> 确定性 document 与 PPTX scope grounding
  -> confirm_document_target
  -> document_locate_evidence（一次格式限定读取）
  -> select_edit_operation（一个持久化精确 entry）
  -> document_edit（一次 approval-gated 原子 mutation）
  -> 完整 reread 与 preservation verification
  -> 一个可追溯输出副本
```

既有安全边界继续生效：一个受治理输入、预分配输出命名族、不覆盖、精确 format/operation
注册、可逆 approval、完整输出 reread、原文件 hash 不变，以及对不支持内容显式失败。

## 1. 路由与可执行 Scope

PPTX scope 是 semantic `document.edit` candidate 胜出后记录的类型化 grounding 结果。它只收窄
operation directory，不在 route 中选择 operation。

| Grounded scope | 当前行为 |
|---|---|
| `single_slide` | 要求显式 1-based 页码，只暴露 `pptx.update_slide`。 |
| `whole_deck` | 要求显式整份演示文稿意图，只暴露原子 `pptx.update_deck`。 |
| `exact_text` | 要求显式替换意图，只暴露 `pptx.replace_text`。 |
| `structural` | 对显式结构请求暴露 `pptx.add_slide`、`pptx.duplicate_slide` 与 `pptx.delete_slide`。 |
| `unspecified` | 在 mutation read 或创建 approval 前要求澄清。 |
| `unsupported_target` | 以 `pptx_edit_target_unsupported` 阻断 SmartArt、动画、图表数据、母版和宏编辑。 |

英文数字形式、常见 slide/page 表达、阿拉伯数字与中文序数都会确定性规范化。显式页 scope 会
重新绑定并覆盖模型参数，冲突的模型页码还会被当前读取验证拒绝。读取、新建演示文稿、发送及
文件删除请求都是 PPTX edit path 的 hard negative。

整份更新是一次可逆 operation。当前上限为 12 页、64 个更新形状和 32 KiB replacement text。
重复页码、陈旧目标、超限 batch 与部分成功都会被拒绝。

## 2. 富文本与段落保真

PPTX reader 在稳定 shape path 上暴露有界 paragraph/run 树。paragraph 记录 level、bullet 状态、
alignment、spacing、soft break 与 run 顺序；run 记录文本及受支持的 font、size、emphasis、
color、language 与 hyperlink 属性。含不支持 field 的文本会标为不可编辑，不会虚假声明可保真。

当前有两种 shape update mode：

- `exact_span` 替换一个证据绑定区间。跨 run replacement text 会被确定性重新分配，未受影响
  run 与 style 保持原位。
- `rewrite_shape` 替换已选 shape 文本，同时保留 paragraph skeleton 与受支持 run style。
  `break_mode=soft_break` 和 `break_mode=paragraph` 显式定义换行语义。

`pptx.replace_text` 使用相同的 run-aware 替换路径。Preservation verification 比较 paragraph
property、run style、hyperlink target、仅目标文本 delta 及显式报告的 layout change。格式丢失、
含 field 目标或无关变更会返回 `preservation_mismatch`；非法输出被删除，源文件保持 byte-identical。

## 3. 目标证据与可编辑性

8,000-byte PPTX operation projection 识别 scope。它先输出冻结 document identity 与 slide count，
随后是完整目标页记录和可编辑 shape 记录，最后才是可选 layout inventory。靠后的目标页优先于
前部非目标内容。

Group child、`editable=false` 记录、无可用 text frame 的 shape 以及不支持文本不会进入 editor
argument；其只读上下文仅在有用时保留。记录以原子方式打包，绝不截断 JSON 或 shape record。
无法安全提供必需证据时，Workflow 分别以稳定的 `pptx_target_evidence_missing`、
`pptx_target_evidence_exceeds_budget` 或 `pptx_whole_deck_exceeds_batch_bound` 阻断。

在 Policy 与 Approval 前，Runtime 会把 input path、slide/shape coordinate、`old_text`、精确替换
target、layout/template ref、插入位置、冻结 scope、read ownership、run ID、node ID 与 scope
revision 对照当前唯一一次完成的 localization read。陈旧、分组、不可编辑、跨 run 或跨 scope
target 不会创建 approval 或输出。

## 4. 模板感知新增页

`pptx.add_slide` 必须接收一个证据所有的 source：

- 来自稳定当前读取 layout inventory 的 `layout_ref`；或
- 来自同一次完成读取中既有 slide 的 `template_slide_ref`。

`after_slide_index` 定义物理插入位置；省略时 append。Template clone 及可选
`template_updates` 在同一次 adapter invocation、输出副本与 approval 中执行。系统重映射
relationship ID，并复制受支持的 text、group、image、chart、hyperlink 与 package relationship；
克隆 template shape 前移除自动创建的 destination placeholder。含 speaker notes 的 template 或
duplicate source 会在 approval 前被拒绝，因为当前不能无损克隆 notes。

插入、复制与删除会重新计算物理 slide marker。Editor 不改写无关 footer text；declared total
不再匹配 deck 的 marker 会作为 preservation warning 呈现。

## 5. 端到端 Timeout 治理

每个已注册 PPTX edit definition 使用同一个 125,000 ms deadline，覆盖 input inspection、完整
read、constrain、adapter mutation、output inspection、完整 reread、preservation verification 与
cleanup margin。每个 Python subprocess 继承不晚于 caller deadline 的 child deadline。挂起 adapter
会被及时终止；超时 operation 会删除部分输出且不触碰 source。

PPTX timeout 保留 document code `operation_timeout`，并映射为 tool code
`document_operation_timeout`。Reader 与 mutation adapter 保留 `read` 和 `apply` stage 证据；
发生在这些 adapter 之外的 parent deadline 当前会保守分类为 `read`。

精确区分 `reread` 与 `preserve` timeout 需要在共享 `document.Pipeline` 内拥有 stage。该变更会
改变公共跨格式契约，因此作为集成任务保留，而不在本次 PPTX 专属实现中扩散。

## 6. Route-To-Output 验证

确定性 gate 覆盖以下层次。

| 层次 | 已覆盖行为 |
|---|---|
| Catalog/profile/semantic graph | Revision 一致、不新增 PPTX leaf、中英文 scope case 及 read/create/send hard negative。 |
| Grounding | 阿拉伯与中文序数、整份 scope、歧义澄清及不支持目标阻断。 |
| Directory decision | replace/add/update/update-deck/duplicate/delete 精确 scope filtering 与持久 entry 强制执行。 |
| Reader/evidence | rich run、bullet、hyperlink、group、image、chart、notes、稳定 layout ref、后部页优先及完整记录 overflow。 |
| Editors | run-aware replacement、exact-span 与 paragraph rewrite、整份原子成功/失败/边界、插入顺序及保留 relationship 的 template clone。 |
| Preservation | source hash、仅目标 text/style delta、layout allowlist、hyperlink、asset、chart、relationship、warning 及非法输出清理。 |
| Policy/approval | 冻结 input/output/operation/scope、受影响页摘要及 approval 前陈旧证据拒绝。 |
| Timeout | 过期 parent deadline、挂起 subprocess 终止、稳定 code 及无残留输出。 |
| End to end | route -> read -> operation selection -> approval -> execute -> reread -> output attachment -> 持久 parent lineage。 |
| Golden eval | 确定性单页、整份、歧义、不支持目标、后部证据及 route-to-lineage case。 |

真实 PPTX fixture 含十页，覆盖混合 style run、bullet、多 paragraph、soft break、hyperlink、group
shape、image、chart、speaker notes、复杂 layout relationship 以及位于第 10 页的目标。测试读取
source、编辑输出副本、重新读取、比较 preservation evidence 并验证 source integrity。另一个
12 页 fixture 覆盖整份 batch 的成功上限。测试使用独立临时 workspace 与 artifact path，
不启动共享模型或服务。

## 用户可见变化

- 显式单页与整份请求现在进入匹配其声明 scope 的 operation；泛化演示文稿润色会要求澄清。
- PPTX 文本编辑保留受支持的富文本结构，不再压平 paragraph 和 run。
- 后部页与仅可编辑顶层 shape 会作为 edit target 提供。
- 新增页可以在一次审批输出中使用当前 layout 或既有 slide template，并插入到绑定位置。
- PPTX edit 具有现实的统一 timeout 与稳定 timeout failure code。

## 明确非目标

- 从零创建演示文稿、自由设计 slide、生成 theme、animation、SmartArt、chart data 编辑、master
  编辑、macro 或任意 OOXML mutation。
- PPTX 专属顶层 capability、关键词 router、fallback executor 或 generic mutation tool。
- 静默缩小字体、隐式 page-marker 改写、覆盖 source 或整份部分成功。
- Approval UI、WebChat result card 或 attachment surface 重设计。
- 共享 `document.Pipeline` stage 重构；它仍是上文所述集成项。
