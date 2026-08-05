# DOCX 编辑优化方案

> 语言： [English](../../docs/docx-editing-optimization.md) | 简体中文
>
> 状态：拟议实施规范。本文不描述已经交付的行为。引入本文的纯文档变更不会修改
> runtime 代码、schema、配置或测试。

## 1. 目标与范围

本方案强化现有 DOCX 读取与编辑链路中优先级最高的六个缺口：

1. 对齐 `docx.set_text_style` 的输入、回读与保真契约；
2. 将每个 DOCX mutation 绑定到当前定位证据和源文件版本；
3. 文本替换时保留 run 级格式，无法可靠保留时显式失败；
4. 如实报告 DOCX 内容覆盖度，并暴露高价值的遗漏 part；
5. 在统一字节预算内为 operation 选择提供目标感知证据；
6. 增加多语言路由、operation 选择、审批与 mutation 评测。

现有语义路由与 Workflow 架构继续作为权威边界。本方案不增加关键词路由、第二份能力映射、
通用文档修改工具或另一套模型驱动目录选择器，也不增加表格单元格编辑与大文档策略；后二者
仍属于后续扩展。

## 2. 保持不变的 Workflow 边界

编辑链路保持为：

```text
semantic fusion
  -> document.edit revision 5
  -> confirm_document_target
  -> document_locate_evidence（direct_once files.read）
  -> select_edit_operation（持久化一个 directory entry）
  -> document_edit（只 materialize 已选择的 editor）
  -> Policy 与 Approval
  -> 输出副本
  -> 重新读取并执行保真校验
```

实现必须保持以下不变量：

- 受治理的输入输出 path 由 Runtime 绑定，模型不能选择。
- operation 选择始终位于冻结的 DOCX scope 内，并且只持久化一个合格 directory entry。
- 授权 mutation 的证据只能来自同一 run、session、节点 revision 和受治理 path 中唯一完成的
  `document_locate_evidence` 调用。
- Approval 只授权 owner 已审阅的持久化 operation、参数、源版本和证据绑定目标。
- 原文件保持不变；系统只接受新的同级输出副本。
- run 成功前必须通过同一个 parser 重新读取输出。

## 3. 缺口一：补全 Text Style 契约

### 当前缺口

`docx.set_text_style` 接受 `builtin_style`、`bold` 和 `font_size_pt`，但规范化 DOCX
representation 与 preservation validator 只能证明段落内置样式名称。仅设置粗体或字号的
编辑因此没有有效的 round-trip 成功条件；同时提交三个属性时，也可能在未分别证明粗体和
字号的情况下通过。

输入 schema 还允许空 `style` 对象，也不强制提供有效的段落 locator alternative，导致
无效调用进入过深的执行阶段。

### 目标输入契约

- `style` 是 strict object，至少包含 `builtin_style`、`bold`、`font_size_pt` 之一；
  未知字段直接拒绝。
- `font_size_pt` 保持为 `1..200` 的闭区间整数。
- 至少提供 `paragraph_index` 或 `location` 之一；两者都存在时，Runtime 要求它们指向
  同一段落。
- 在显式注册其他可编辑 story part 前，`location` 只能指向 DOCX 正文顶层段落。
- style 或 target 参数无效时，在 Policy 前失败且不创建 approval。

### Parser 与规范化 Representation

DOCX reader 必须暴露足够的确定性证据，以便保存并重新打开后验证三个属性：

```json
{
  "index": 3,
  "style": "Heading 1",
  "runs": [
    {
      "index": 1,
      "text": "季度总结",
      "bold": true,
      "font_size_pt": 18.0
    }
  ]
}
```

run 字段表示文档重新打开后的有效值，而不是 editor 子进程报告的值。缺失值或继承值必须
明确表示为 `null` 或单独命名的 effective field，不能根据显示文本猜测。

### 编辑后校验

- `builtin_style` 与重新读取的段落样式执行忽略大小写比较。
- 请求的 `bold` 必须与目标段落的每个非空 run 一致。
- 请求的 `font_size_pt` 必须与每个非空 run 一致，并使用适合 OOXML point 转换的小范围
  数值容差。
- 未请求的样式属性必须保留编辑前 fingerprint。
- 目标文本与 location 必须保持不变。
- 不一致时返回现有 typed preservation failure，并移除生成的输出。

### 验收用例

- 仅内置样式、仅粗体、仅字号以及所有支持的组合。
- 显式 `bold: false` 与未提供 `bold` 能够区分。
- 空 style、缺失 target、两种 target 冲突和越界字号在 approval 前失败。
- 即使 editor 报告成功，只要保存并重新读取后的值不一致，preservation 仍失败。

## 4. 缺口二：为每个 DOCX Mutation 绑定证据

### 目标证据契约

用 Workflow Runtime 拥有的一份通用 DOCX mutation binding 替换仅服务于
`docx.replace_paragraph` 的特殊逻辑。持久化 binding 包含：

```text
source_tool_call_id
source_node_id 与 scope_revision
session_id 与 run_id
受治理输入 path
source_document_sha256
operation
target 或 anchor location
适用时的 target 或 anchor source_hash
适用时的规范化 before text
```

`source_document_sha256` 表示整个输入文件版本；`source_hash` 表示规范化段落或 match
fingerprint。二者是不同事实，不能混用。

binding 只能从当前 `document_locate_evidence` 节点已完成的 `files.read` observation 派生。
模型可以省略证据所有的字段，由 Runtime 补齐；模型提供的值只有在与当前证据相等时才接受。

### Operation 矩阵

| Operation | 必须绑定的证据 |
|---|---|
| `replace_text` | 输入 SHA-256、精确 match location 与 hash、预期 match 数量 |
| `replace_paragraph` | 输入 SHA-256、段落 location、段落 source hash、可选精确 old-text guard |
| `insert_paragraph` 的 `before`/`after` | 输入 SHA-256 与 anchor 段落 location/hash |
| `insert_paragraph` 的 `start`/`end` | 输入 SHA-256 与相应文档边界 |
| `delete_paragraph` | 输入 SHA-256、段落 location/hash、规范化 before text |
| `set_text_style` | 输入 SHA-256、段落 location/hash、编辑前格式 fingerprint |

Schema validation 必须表达 target alternative。`before` 和 `after` 要求 anchor；`start` 和
`end` 拒绝无关 anchor。删除、样式与整段替换始终要求解析出唯一段落。

### 两个验证时点

1. **Policy 与 Approval 前：**验证来源、path、operation、locator 唯一性、source hash 以及
   模型参数一致性。失败时不创建 approval。
2. **Approval 通过后、adapter 执行前：**重新加载 run 与 call，重新计算受治理输入
   SHA-256，再次解析绑定目标并比较 hash 与 before value。源已过期时，不调用 editor，
   也不写输出。

第二次检查关闭等待审批期间的竞态。已批准调用不能仅因为更早持久化的参数曾经有效就直接执行。

### 验收用例

- 每个 DOCX operation 都拒绝来自其他 run、session、节点、path 或 scope revision 的证据。
- 证据缺失或冲突时在 approval 前失败。
- 请求 approval 后修改源文件或绑定段落，审批执行必须显式失败且不产生输出。
- 无关的已批准调用不能复用此前的 DOCX 定位 artifact。
- 首尾插入无需虚构 paragraph index 仍然可用。

## 5. 缺口三：保留 Run 级格式

### 替换算法

DOCX 文本替换必须只编辑最小受影响 run span，不能清空所有 run 后把全文写入第一个 run。

对每个段落：

1. 构建逻辑段落文本，以及字符 offset 到 run index/offset 的映射。
2. 在修改段落前解析所有不重叠的 exact match。
3. match 位于单个 run 内时，只 splice 该 run 文本；保留其 run property 与所有相邻 run
   中 parser 支持的 OOXML property。
4. match 跨 run 时，仅当所有受影响文本 run 的格式 fingerprint 与 relationship boundary
   相同时才允许替换。
5. match 跨越混合格式、hyperlink、field、drawing、tracked change 或其他不支持的边界时，
   显式失败，不能压平段落。

整段替换保留 paragraph property；只有源段落的文本 run fingerprint 单一且一致时才沿用现有
run style。混合格式段落在公共契约增加显式 replacement style policy 前一律拒绝。

### Preservation Fingerprint

reader 和规范化 representation 必须暴露稳定 run span，以及检测格式破坏所需的 parser
可见格式，至少包括：

- bold、italic、underline、font name、font size 与 color；
- hyperlink 或 relationship ownership；
- paragraph style 与影响 layout 的 paragraph property；
- field、drawing 与 tracked change 的不支持边界 marker。

编辑后校验比较未受影响 run fingerprint 和 relationship boundary。只有预期文本 span 与
显式请求的 style delta 可以变化。Parser coverage 必须明确；未知格式不能报告为已保留。

### 验收用例

- 在单个粗体或带链接 run 内替换时，保留该 run 与相邻 run。
- 跨同质 run 替换时保留公共格式。
- 跨粗体和非粗体 run 的替换显式失败且不留下输出。
- 源 run 格式同质时，整段替换保留 paragraph style、编号、缩进和间距。
- 未变化的 hyperlink、field、image 与 relationship 在保存并重新读取后仍存在。

## 6. 缺口四：如实报告 DOCX Coverage

### Coverage 语义

`coverage.content = complete` 表示 package inspection 检测到的每个含文本 DOCX story part
都已进入规范化证据，不能仅表示 adapter 完成了正文段落读取。

满足该条件前，reader 必须报告 `partial` 并列出原因。结构化结果增加按 scope 的状态和遗漏
part 证据，例如：

```json
{
  "coverage": {
    "content": "partial",
    "content_scopes": {
      "body": "complete",
      "tables": "complete",
      "headers": "complete",
      "footers": "complete",
      "footnotes": "unsupported",
      "endnotes": "unsupported",
      "text_boxes": "unsupported",
      "tracked_changes": "partial"
    }
  },
  "extensions": {
    "status": "partial",
    "unparsed_parts": ["word/footnotes.xml"]
  }
}
```

具体 vocabulary 必须与现有 coverage normalization 共用，不能新增平行 coverage enum。

### 交付阶段

1. package inspection 无法证明完整文本覆盖时，先把当前声明修正为 `partial`。
2. 用稳定 section/story-part location 提取 header/footer 的段落、表格、hyperlink 与 image。
   跨 section 共享的 linked header/footer 按 package part identity 去重，同时保留 section 引用。
3. 盘点含文本 OOXML part 与 marker，包括 footnote、endnote、text box、tracked insertion/deletion
   和 `altChunk` 内容。
4. 按价值顺序增加 parser。不支持的 part 始终在 coverage 中可见，且不会成为隐式 mutation target。

普通 `content` 可以继续以正文为主以保证回答质量，但结构化 representation 与 operation 选择
投影必须在存在 header/footer 时包含带标签的相应证据。

### 验收用例

- 只有当 package inventory 证明不存在遗漏的含文本 part 时，纯正文 fixture 才能标记 complete。
- Header/footer 文本获得稳定 location；跨 section 链接时内容只出现一次。
- 包含 footnote、text box 或 tracked change 的文档在这些 part 被表示前报告 partial。
- 输出重新读取后的 coverage 状态不得变差；parser 遗漏不能静默变成 preservation 成功。

## 7. 缺口五：目标感知的 Decision Evidence

### 一个预算与一种单位

由 `workflow_stage_evidence_max_bytes` 支持的 `Runtime.StageEvidenceMaxBytes` 是唯一事实来源。
Operation selection 不能再硬编码另一份 `8000` 限制，文档也应描述可配置字节预算，而不是
单独的 20,000-rune 契约。

本方案保留 8,000-byte 默认值。优化改变的是证据选择，不是模型上下文大小。

### DOCX Decision 投影

增加文档 decision projection，输入冻结的 owner 请求、route target、合格 operation entry
和结构化 read result，并按以下顺序打包完整 evidence record：

1. source metadata、format、coverage 与 truncation state；
2. 精确 route-bound location 和显式引号文本 match；
3. 匹配的 paragraph/table anchor 及其有界相邻 block；
4. 区分 replace、insert、delete 与 style 所需的 operation context；
5. 没有 target anchor 时使用确定性的 head/tail 结构 fallback。

projector 可以排序现有证据，但不能选择 capability 或 operation。它不是关键词路由器，不能
增加第二份 catalog、调用另一模型，也不能让未经过 Workflow binding 的 mutation target
获得权威性。

只打包完整 UTF-8 record。投影报告选中与遗漏 record 数量、遗漏 location range、字节使用量
以及触发优先级的 anchor。Compact 和 minimal 投影在更小预算下重复相同排序，不能退化为
raw prefix。

### 验收用例

- 长 DOCX 末尾的目标段落在默认预算内仍然可见。
- 中文与英文显式 anchor 选择相同稳定 location。
- 无 anchor 请求获得 metadata、operation context 与 head/tail 样本。
- 调整无关的前部段落顺序不会挤掉显式指定的后部段落。
- full、compact 与 minimal 投影始终是合法 UTF-8 且不超出各自字节上限。

## 8. 缺口六：路由与端到端评测矩阵

必须在五个层次提供覆盖。只有 ToolHub 测试不能证明用户请求能够完成路由、选择、审批、执行、
重新读取并保留 DOCX。

### A 层：Parser、Schema 与 Preservation 单元测试

- Strict style object 与 target alternative。
- Run 提取、effective style 回读、coverage inventory 与稳定 location。
- Operation-specific expected delta 与无关 run/relationship 变更拒绝。
- full、compact 与 minimal 预算下的目标感知 evidence packing。

### B 层：ToolHub Adapter 集成

五个已注册 DOCX editor 都需要针对真实 fixture 提供成功、malformed input、target not found、
ambiguity、stale hash、preservation mismatch 和保存后重新读取用例。Fixture 应包括混合 run、
hyperlink、多 section、header/footer、table 与不支持的 OOXML part。

### C 层：Workflow 与 Approval 集成

对每个 operation 执行：

```text
route -> confirm target -> direct_once read -> operation decision
      -> one materialized editor -> approval_pending -> approve
      -> adapter -> output reread -> completed WorkflowResult
```

增加 cross-run evidence、locator 冲突、approval 前源变化、等待 approval 时源变化、拒绝审批、
adapter 失败与 preservation 清理等负向用例。至少一个已批准路径使用默认 file-backed 状态配置。

### D 层：多语言语义与 Operation 选择

维护确定性的中英文标注用例：

| Intent | 中文样例 | 英文样例 | 预期 Operation |
|---|---|---|---|
| 替换文本 | 把“旧名称”改成“新名称” | Replace "Old Name" with "New Name" | `replace_text` |
| 替换段落 | 把第三段改写为这句话 | Rewrite paragraph three with this sentence | `replace_paragraph` |
| 插入段落 | 在结论前新增一段 | Insert a paragraph before the conclusion | `insert_paragraph` |
| 删除段落 | 删除重复的第二段 | Delete the duplicated second paragraph | `delete_paragraph` |
| 设置样式 | 把标题设为一级标题并加粗 | Make the title Heading 1 and bold | `set_text_style` |

补充同义表达、引用最新编辑文档的 follow-up、长文件末尾显式 location，以及 replace vs insert、
replace vs style 等 confusion pair。Hard negative 覆盖 table-cell edit、接受 tracked change、
替换 image 和其他未注册 operation。这些请求可以进入 `document.edit`，但 operation selection
必须返回无匹配 editor，明确 block，不能 materialize 替代工具。

真实模型 calibration 继续作为 opt-in 证据；确定性 mock-router 与 golden case 才是合并门禁。

### E 层：Golden Owner Workflow

为五种 operation 增加已批准端到端 golden case，并为不支持 mutation、过期 approval、长文档
末尾证据、混合 run 拒绝和 partial coverage 披露增加负向 golden case。断言选中的 Workflow、
operation、approval surface、输出 lineage、preservation result 与最终 owner-facing status，
不能只匹配工具名称。

## 9. 交付顺序与 Ownership

分六个可审查的行为变更实施，每项都配套 focused test 和双语当前状态文档更新：

1. **先修正 Coverage：**防止后续 preservation 工作信任错误的 completeness 声明。
2. **增加 Run-aware Representation：**为样式和替换保真提供共享回读证据。
3. **补全 Style Contract：**对齐 schema、adapter result、重新读取与 preservation。
4. **通用 DOCX Evidence Binding：**对每个 mutation 应用 approval 前与 approval 后的
   source/target validation。
5. **Run-preserving Editor：**只有 parser 能证明保真后才改变替换行为。
6. **目标感知投影与完整 Eval Matrix：**消除 evidence budget 不一致并补齐路由和端到端覆盖。

职责保持在现有边界内：

| 关注点 | Owner |
|---|---|
| DOCX schema、adapter、parser script | `internal/toolhub` |
| 规范化 run/coverage 契约与 preservation | `internal/document` |
| evidence binding、approval revalidation、decision projection | `internal/agent` |
| runtime budget 默认值与 validation | `internal/config` 和现有默认配置 |
| 语义、Workflow、ToolHub 与 golden case | 现有 package test 和 `eval/golden` |

不需要新增 package 或跨层 import。

## 10. 完成门禁

只有全部满足以下条件时，优化才算完成：

- 五个 DOCX operation 在 approval 前全部完成证据绑定，并在批准执行前重新验证；
- 仅样式请求能够 round-trip，并验证每个请求属性；
- 支持的文本替换保留 parser 可见 run 格式，混合格式歧义变更显式失败；
- 检测到遗漏文本时，DOCX content coverage 绝不声明 complete；
- operation selection 在配置的字节预算内使用目标感知证据，文档不再声明独立 rune 限制；
- 确定性的中英文 route、decision、approval、mutation、reread 与负向 golden case 全部通过；
- validation 包含默认 file-backed runtime；
- 行为落地后更新双语当前状态文档，并按文档维护规则删除这份临时方案。
