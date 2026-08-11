# 工作流证据最小投影重设计

> 语言：[English](../../docs/workflow-minimum-evidence-projection-design.md) | 简体中文

状态：已于 2026-08-11 实现统一投影审计生命周期、文档操作选择、PPTX 语义修复、文档读取
最终化以及 Browser r2 transition/presentation 路径。下文真实运行结果保留为实现前基线。
阶段 4 的通用 observation 与 typed completion 大范围清理仍然延后。

本文细化[工作流证据归属与复用](workflow-evidence-ownership.md)，不替代原有归属模型。本文定义 Runtime
如何为每个语义消费者构造最小但充分的投影、如何证明投影充分，以及如何在不重新获取源数据的前提下修复无效的语义输出。

## 1. 决策

最小证据不是提示词中能容纳的最少字节，而是：在 Runtime 保留并随后绑定全部确定性身份、定位、版本、哈希和新鲜度事实的前提下，能够让一个消费者不靠猜测地解析一个已声明语义变量的最小类型化投影。

目标契约如下：

1. 一次获取只创建一个不可变观察事件，并只归档一次完整输出。
2. 每次模型调用都必须声明它要解析的唯一语义变量。
3. Runtime 从观察事件派生面向消费者的投影。即使来源相同，选择、生成、验证和最终化也不共用一个通用证据转储。
4. 每个投影都携带来源谱系、消费者覆盖范围、遗漏信息和可由机器强制执行的输出 schema。只有 `source_complete` 不足以证明投影充分。
5. Runtime 将模型选择的不透明 ID 解析回权威定位，并拒绝过期、越界、结构无效、互相矛盾或无实际变化的输出。
6. 可恢复的类型化输出错误最多执行一次同阶段修复，并复用相同投影。除非源数据确实过期或投影明确存在覆盖缺口，否则不能再次读取文档或获取浏览器快照。
7. 工作流完成必须依据类型化完成谓词，而不是工具成功、自由文本理由或出现了一段最终回答。

该设计面向格式和站点的通用能力。文档适配器提供类型化结构，浏览器适配器提供类型化状态转移；两者都不得硬编码页码、段落标题、表格内容、站点文件夹或测试措辞。

## 2. 真实测试基础

测试使用运行中的 Gateway `http://127.0.0.1:18789`、真实工作区附件、当前配置的本地模型、真实审批流程和持久化 QQ 邮箱浏览器 Profile。DOCX 和 PPTX 根据附件中已有编辑副本数量选择。附件中没有 XLSX，因此按用户授权创建并上传了一个小型测试表。

浏览器复测使用的是工作区中已经存在修改并正在运行的部署。其结果描述当前 Runtime，不代表已发布基线；本次设计工作没有编写或修改这些已有浏览器变更。

| 用例 | 请求与来源 | Run | 结果 |
|---|---|---|---|
| DOCX | `完善心得与体会`；`upload_69cfdb7ca13f2b82-file-3.docx` | `run_f88a2970abe051f0` | 审批后成功；写入编辑副本并保留原文件 |
| PPTX | `完善第三页ppt`；`upload_8a1ac66704f22841-file.pptx` | `run_46ae13ce8c3cc478` | 审批前阻塞，因为生成结果中存在一个空替换文本 |
| XLSX | 在 `学生信息` 末尾新增 `2026004 / 陈曦`；`upload_9eebb0ddb7105a2e-student-roster.xlsx` | `run_b7ed0f7334561020` | 两次操作选择都返回空 `entry_id`，随后阻塞 |
| PDF | 总结官方 Structured Outputs 检索结果，并判断 PDF 是否包含完整 JSON Schema | `run_e5f6dab07e9edf3e` | 成功；正确说明这份四页 Kagi 检索快照没有完整 schema 示例 |
| 浏览器，用户报告的失败 | `打开qq邮箱的草稿箱` | `run_47f0f0b33ad1f5b2` | 点击已到达草稿箱路由且转移验证成功，但重复点击导致等待超时 |
| 浏览器，当前复测 | 在新会话中执行同一请求 | `run_91135f1cf7231ad9` | 隐藏和可见浏览器均打开草稿箱路由，但接受了互相矛盾的语义输出 |

XLSX 测试表只有一个可见 Sheet `学生信息`，表头为 `学号 | 姓名`，包含三行数据。文件保持小型是有意为之，用于明确区分“证据不足”和“操作选择失败”。

## 3. 测量与发现

### 3.1 跨用例测量

| 消费者 | 已归档观察 | 模型投影 | 实际覆盖 | 结论 |
|---|---:|---:|---|---|
| DOCX 操作选择 | 94,190 字节 | 7,733 字节（8.2%） | 目标标题和正文均存在 | 正确选择 `replace_paragraph` |
| DOCX 编辑参数生成 | 同一个 94,190 字节事件 | 6,393 字节（6.8%） | 定位段落完整 | 生成一次有效替换，未重复读取 |
| PPTX 编辑生成 | 8,394 字节 | 4,659 字节（55.5%） | 冻结页面上的 14 个可编辑 shape 全部存在 | 投影充分，输出契约不充分 |
| XLSX 操作选择，每次尝试 | 15,773 字节 | 1,650 字节（10.5%） | 1 个 Sheet、4 行、8 个单元格，无遗漏 | 证据充分，但候选选择连续失败 |
| PDF 最终化 | 57,383 字节 | 完整的 6,954 字节提取文本及覆盖清单 | 4/4 页原生文本，无缺页 | 完整内容支持否定性结论 |
| 浏览器快照，当前复测 | 20,375 至 20,545 字节 | 1,705 字节（约 8.3%） | 返回的控件可容纳；状态转移事实不在快照投影中 | 短 ref 改善了执行，但语义一致性仍然薄弱 |

投影字节数是有用的遥测指标，但不是成功标准。PPTX 和 XLSX 都在目标证据完整的情况下失败；旧浏览器用例的投影也很紧凑，但缺少“所选动作与已验证结果之间的判别关系”。

### 3.2 DOCX：成功的事件复用

结构化读取定位到 `五、心得与体会` 及其后正文段落。操作选择与编辑生成复用了同一个归档事件，但因为解析的变量不同，获得了不同投影：

- 选择阶段解析 `eligible_document_operation`；
- 生成阶段解析新的段落内容。

模型输出后，Runtime 补充路径、输出路径、源 SHA-256、段落定位、源哈希、旧文本和源证据身份。源文件只读一次，编辑需要审批，原文件没有被覆盖。这是后续设计必须保留的基线行为。

剩余问题是表示重复：相同目标文本被分别渲染进两个投影。两个视图应共享同一条派生目标记录，但仍保持为不同的消费者投影。

### 3.3 PPTX：证据充分，生成契约无效

Runtime 正确冻结 `single_slide`，绑定第 3 页，确定性选择 `update_slide`，并且只投影该页的 14 个可编辑文本 shape。模型随后：

- 将副标题合并进标题；
- 为原副标题 shape 生成空替换文本；
- 输出多项替换文本与原文相同的更新。

适配器在审批前以 `PPTX slide 3 shape 2 replacement text is empty` 拒绝执行。这一 fail-closed 边界是正确的，但一个可恢复的类型化生成错误直接终止了整个工作流。

通用缺陷不是页面证据不足，而是模型可见 schema 没有充分表达变更不变量，同时 Runtime 没有受限的同阶段修复契约。非删除文本替换必须非空；语义 no-op 必须由 Runtime 确定性移除；剩余无效项应产生类型化修复请求，而不是立即终止工作流。

### 3.4 XLSX：证据完整，候选契约薄弱

两次调用中的 `xlsx_sheet_evidence_v1` 都是完整投影：

- `selection_complete=true`；
- 选择 1 个 Sheet、4 行和 8 个单元格；
- Sheet、行和单元格遗漏数均为 0；
- 表头、当前末行和用户提供的新值均在投影中。

活动目录包含 6 个符合范围的 XLSX 编辑器条目。两次 Fast 操作选择都返回空 `entry_id`，Runtime 最终报告 `no_registered_editor_matches`。增加更多工作簿单元格不能解决该失败。

当前模型需要把语义变更意图直接映射到不透明的注册表条目。新投影应提供规范化且有界的候选契约，例如目标类型、变更类型、位置能力、所需用户内容和保留行为。模型仍然只选择候选 ID，再由 Runtime 把候选 ID 映射到当前目录条目。选择仍然属于语义判断，但不再让无关的注册表表示进入决策。

### 3.5 PDF：最终回答正确，但投影遥测不完整

PDF 阅读器将全部 4 页提取为可用原生文本。因为内容低于 8,000 rune 的边界，最终化器重新加载归档观察并获得全部 6,954 字节提取文本，因此有充分证据说明附件只是 Kagi 搜索结果快照，并不含完整 JSON Schema 示例。

2,366 字节的紧凑工具观察摘要不是最终化器实际使用的证据。这一区分很重要：最终化器目前没有对应的 `workflow_step.evidence_provisioned` 审计记录，因此只能结合源码和 token 数量反推真实最终化投影及其声明覆盖。最终化应与其他语义消费者一样，生成显式投影记录和覆盖审计。

对于更大的文档，`read_complete=true` 不能等同于模型声明覆盖完整。像“文档中不存在示例”这样的否定性结论，只有在最终化投影声明所有相关页或窗口都已检查时才允许生成。

### 3.6 浏览器：功能恢复，但语义完整性仍不成立

用户报告的失败中，第一次点击 `Drafts` 已改变渲染内容摘要、到达 `#/list/4`、完成稳定等待并通过确定性转移验证。动作后投影仍主要呈现相同的持久侧边栏。模型误判点击未生效，再次请求同一语义动作；第二次等待因为不会再产生新状态而超时。

当前复测把每个模型快照投影从 2,833 字节缩小到 1,705 字节，并使用短控件 ref。隐藏与可见会话都到达 `#/list/4` 并最终完成，但这不是干净的语义通过：

- 路由之后共执行 5 次 Deep 工作流模型调用；
- 初始快照被投影两次，分别用于目标评估和点击选择；
- 可见评估阶段第一次返回最终回答，Runtime 因必需评估工具未调用而拒绝；
- 隐藏评估先产生很长的自相矛盾理由，最后选择 `satisfied`；
- 可见评估返回 `verdict=satisfied`，但必填 `reason` 明确说目标尚未满足。Runtime 接受 verdict 并完成。

核心问题是快照投影以控件为中心，而消费者的语义变量以状态转移为中心。Runtime 已知所选控件、前后摘要、路由一致性、稳定状态和隐藏到可见的移交状态，却没有把这些事实组成一条紧凑派生转移断言交给语义消费者。自由文本理由即使与类型化 verdict 冲突，仍被当成审计证据。

## 4. 必需的投影模型

### 4.1 投影记录与模型载荷

Runtime 应为每次语义调用持久化一条 `EvidenceProjectionRecord`：

```text
EvidenceProjectionRecord
  projection_id
  projection_schema_version
  source_event_ids[]
  derived_assertion_ids[]
  consumer
    workflow_id, node_id, stage
    semantic_variable
    consumer_schema_version
  coverage
    source_coverage
    target_coverage
    claim_coverage
    candidate_coverage
    complete_for_consumer
    omissions[]
  model_payload_digest
  model_payload_bytes
  runtime_binding_manifest_ref
  created_at
```

模型只接收 `model_payload`。绑定清单不是提示内容，它保存权威路径、哈希、文档定位、完整浏览器 ref、generation、版本、目录 revision 和新鲜度谓词，供 Runtime 解析并重新验证模型输出。

### 4.2 覆盖范围必须面向消费者

每个投影必须区分以下维度：

| 覆盖维度 | 回答的问题 |
|---|---|
| 源覆盖 | 适配器是否获取了它声称已经读取的全部源单元？ |
| 目标覆盖 | 识别请求目标所需的源单元是否全部进入投影？ |
| 声明覆盖 | 是否有足够内容支持请求中的肯定或否定结论？ |
| 候选覆盖 | 所有当前可用选项是否都以规范化形式呈现？ |
| 转移覆盖 | 动作、前后状态和确定性验证事实是否都已呈现？ |
| 呈现覆盖 | 可见结果是否被证明等价于已验证隐藏结果，或由其安全派生？ |

只有已声明语义变量所需的全部覆盖维度都完整时，`complete_for_consumer` 才能为 true。否则 Runtime 必须获取新的有界窗口、澄清或阻塞，不能让模型替未披露的遗漏做猜测。

### 4.3 按语义消费者构造投影

| 消费者 | 模型可见载荷 | 模型输出 | Runtime 专属数据 |
|---|---|---|---|
| 目标选择 | 用户意图、有界目标候选、用于区分的语义文本、覆盖信息 | `candidate_id` 或类型化 `no_match` | 路径、定位、哈希、版本 |
| 操作选择 | 规范化变更意图和规范化可用操作契约 | `candidate_id` | 目录条目 ID/revision 和完整工具定义 |
| 内容生成 | 已选操作契约、准确目标内容、保持连贯所需邻近结构、输出约束 | 操作特定的语义参数 | 源/输出路径、旧文本保护、哈希、冻结范围 |
| 效果验证 | 冻结目标、相关动作后状态、紧凑的确定性动作/转移断言、矛盾集合 | verdict 和证据候选 ID | 完整 ref、URL、摘要、generation、时间戳 |
| 最终化 | 声明完整的内容/断言和覆盖/限制清单 | 仅用户可见回答 | Artifact 路径、内部 ID、无关工作流历史 |

不透明 ID 只在当前投影中有效。模型选出的 ID 在 Runtime 根据绑定清单将其解析到准确源事件和 scope revision 之前不可执行。

## 5. 投影构造

### 5.1 覆盖优先的预算分配

预算按以下顺序分配：

1. 为投影身份、来源谱系、语义变量和覆盖/遗漏清单预留空间。
2. 纳入全部必选候选或目标单元。必选单元无法容纳时，将 `complete_for_consumer=false`，不得静默跳过。
3. 纳入用户明确提到的单元和结构相邻上下文。
4. 当操作依赖开头、结尾、插入点或前后状态时，纳入边界上下文。
5. 使用剩余预算增加多样化支持上下文，而不是重复相同文本。

该算法适用于段落块、表格行、幻灯片 shape、PDF 页/窗口、浏览器控件和状态增量。各适配器只定义类型化单元及相邻关系，Runtime 算法和遗漏规则保持一致。

### 5.2 复用但不重复表示

一个观察事件可以生成多个投影，但重复消费者必须共享派生记录：

```text
观察事件
  -> 类型化源索引 + 覆盖清单
  -> 派生目标集合
       -> 选择投影
       -> 生成投影
       -> 验证投影
       -> 最终化投影
```

共享派生目标集合可以避免重复解析和复制大段文本，但不能把投影合并为同一份：DOCX 选择器需要操作差异，生成器需要目标内容和写作约束。

### 5.3 浏览器状态转移投影

动作之后，浏览器语义验证器应接收紧凑的派生记录，而不是另一份独立控件列表：

```json
{
  "goal": "<用户目标>",
  "action": {
    "kind": "click",
    "candidate_id": "control_1",
    "semantic_label": "<无障碍标签>"
  },
  "transition": {
    "settled": true,
    "rendered_content_changed": true,
    "route_consistent": true,
    "same_session": true,
    "repeated_action": false
  },
  "after_state": {
    "relevant_controls": [],
    "relevant_status_text": [],
    "selected_state_known": false
  },
  "coverage": {
    "transition": "complete",
    "after_target_region": "bounded",
    "complete_for_consumer": true
  }
}
```

这不是 QQ 邮箱特例，而是适用于所有持久导航栏、Tab、折叠面板、菜单、路由和客户端渲染视图。完整 URL、摘要、snapshot ID、generation 和可执行 ref 继续由 Runtime 独占。

对可见呈现，Runtime 应先确定性比较隐藏结果与可见状态。如果路由、Profile 转移、稳定状态和内容等价满足 Profile 谓词，并且没有发现矛盾，则记录“呈现等价断言”并复用隐藏语义 verdict。只有可见状态出现实质语义差异时才进行第二次模型评估。

## 6. 类型化输出与修复

### 6.1 输出 schema

每个语义变量都使用可判别联合 schema。例如：

- 选择：`{"status":"selected","candidate_id":"..."}` 或 `{"status":"no_match","reason_code":"..."}`；
- 生成：只包含语义字段的操作特定对象；
- 验证：`{"verdict":"satisfied|progress|failed","evidence_ids":[...]}`；
- 最终化：只有在声明覆盖完整后才允许输出纯回答文本。

自由文本理由不得决定执行。优先使用稳定 reason code。若为可观察性保留说明文本，则它不具权威性，也不得保存为派生断言。verdict 与说明文本直接冲突时必须判定为验证错误，而不是成功证据。

### 6.2 确定性验证器

进入 Policy 或审批前，Runtime 必须验证：

- 所选 ID 属于当前投影以及当前目录/快照范围；
- 非删除替换文本规范化后非空；
- 生成的变更数组符合数量和大小限制；
- 使用权威当前值确定性移除语义 no-op；
- 在确实要求变更时，过滤后至少剩余一项有效变更；
- 引用属于绑定的当前事件；
- verdict 与结构化 reason code 兼容；
- 源版本、哈希、generation 和新鲜度仍然匹配。

这些都是通用不变量，不检查具体标题、页码、单元格值或网页目标。

### 6.3 一次受限修复

可恢复验证失败生成类型化 `RepairRequest`：

```text
projection_id
invalid_output_digest
error_codes[]
invalid_item_indexes[]
original_output_schema_version
repair_attempt = 1
```

修复调用复用相同模型载荷，只额外接收无效语义输出和这些类型化错误。Runtime 不重新读取源，也不扩大候选范围。第二次结构错误将阻塞。源过期、覆盖不完整、Policy 拒绝和审批拒绝不属于可修复的生成错误，分别走各自流程。

应用到真实失败：

- PPTX 空文本和过滤后只剩 no-op 的输出触发一次生成修复，而不是重新读文档或立即终止工作流。
- XLSX 在仍存在兼容规范化候选时返回 `no_match`，触发一次基于相同 1,650 字节证据投影和精简候选 schema 的选择修复。
- 浏览器 verdict/理由矛盾将被拒绝；从可执行契约中移除自由文本理由可以直接消除此类失败。

## 7. 工作流完成

当前通用 `CompletionEvidence` 对这些场景过于宽泛。Profile 应声明由断言组合成的类型化谓词：

| 工作流阶段 | 必需完成谓词 |
|---|---|
| 文档定位 | 目标覆盖完整且源版本已绑定 |
| 操作选择 | 选择一个当前可用操作，或形成有依据的类型化 no-match |
| 变更生成 | 输出 schema 有效且至少存在一项实际变化 |
| 文档执行 | 审批后的变更完成且保留性检查通过 |
| 浏览器动作 | 动作绑定当前快照并成功执行 |
| 浏览器效果 | before/action/after 转移有效，并且语义目标 verdict 通过 |
| 浏览器呈现 | 可见状态等价于已验证隐藏结果，或已单独重新验证 |
| 文档最终化 | 声明覆盖完整，或回答被强制包含明确限制 |

如果某阶段谓词要求工具或语义 verdict，模型就不能输出最终回答。阶段输出 schema 应只暴露合法分支，而不是始终提供通用 `final` 分支、等模型生成后再拒绝。这样可消除复测中浪费的可见浏览器模型调用。

## 8. 不包含测试特例的格式 Profile

公共投影引擎处理类型化单元，格式策略只提供结构与不变量：

- **DOCX：**块、标题、段落、表格、story part 和相邻关系；Runtime 拥有段落定位和旧文本保护。
- **PPTX：**幻灯片、可编辑 shape、布局角色和操作范围；Runtime 拥有 slide/shape 定位，并拒绝空更新和 no-op。
- **XLSX：**Sheet、结构化边界、行、列、单元格和样式；候选契约表达单元格/行及位置语义，Runtime 绑定 Sheet 哈希和行/单元格地址。
- **PDF：**页/窗口、提取来源和页覆盖；最终化的声明覆盖决定是否允许整篇与否定性结论。
- **浏览器：**控件、语义标签、渲染区域、动作、转移和呈现等价；Runtime 拥有完整 ref、路由身份、摘要、generation 和新鲜度。

测试必须覆盖不同段落位置、幻灯片页码、工作簿 schema、PDF 长度、站点、语言和控件标签。仅通过这五条字面提示明确不够。

## 9. 可观察性

每次语义调用都应产生一条 `workflow.evidence_projection.created` 审计事件，包含：

- 投影 ID 和 schema 版本；
- 源事件与派生断言 ID；
- 消费者 Workflow/node/stage 和语义变量；
- 归档字节、投影字节和比例；
- 各覆盖维度、遗漏项和 `complete_for_consumer`；
- 候选数和选中项数；
- 修复次数和验证错误码；
- 是否复用了源事件或派生记录。

审计还必须记录为什么跳过模型调用，例如只有一个确定性可用操作、呈现等价以及结构谓词已满足。这样才能区分“减少模型调用”和“静默漏做步骤”。

最终化现已使用同一审计表面，并记录实际 finalizer payload bytes、source lineage、coverage、
omission 和 binding 引用。

## 10. 迁移计划

阶段 0 至阶段 2 以及阶段 3 的 claim-aware finalization 契约已实现，并由通用 contract test
覆盖。额外持久化窗口扩展和阶段 4 只会在未来迁移测得明确问题时继续；当前实现会在 claim
coverage 不完整时明确说明限制并 fail closed，不会夸大结论，也没有增加第二套 evidence store
或机械替换全部 `CompletionEvidence`。

### 阶段 0：仅测量

- 在现有选择、生成、评估和最终化调用外增加投影记录与统一审计事件。
- 记录全部覆盖维度，不改变行为。
- 建立不依赖具体 fixture 的字节、调用、重复读取、修复和失败基线。

### 阶段 1：文档契约

- 把文档操作候选规范化为投影内候选契约。
- 增加类型化选择输出和一次受限修复。
- 强化变更 schema 与确定性 no-op 过滤。
- 保留现有 Runtime 绑定、审批和副本输出行为。

### 阶段 2：浏览器转移证据

- 引入动作/转移投影和重复语义动作保护。
- 分离初始动作选择与动作后目标验证。
- 在不存在实质差异时，用确定性呈现等价替代可见状态的再次语义评估。
- 从可执行目标证据中移除自由文本理由。

### 阶段 3：声明感知的最终化

- 为最终化增加显式投影和审计记录。
- 为否定性结论与整篇结论增加声明覆盖。
- 声明覆盖不足时读取更多已持久化窗口；仅当归档源本身不完整或已过期时才重新获取源。

### 阶段 4：清理

- 退役与投影记录重复的通用观察表示。
- 用类型化谓词替换语义过载的 `CompletionEvidence`。
- 完整观察 Artifact 继续服务于来源追踪和重放，而不是直接复用为提示内容。

## 11. 验收标准

只有五个真实场景及其通用变体同时满足以下不变量，重设计才算完成：

1. 未变化的文档或浏览器事件只获取一次；后续消费者复用该事件或显式派生断言。
2. 每个语义模型调用都有一个声明变量、一条投影记录、完整消费者覆盖和类型化输出 schema。
3. 模型输出不提供可执行路径、完整浏览器 ref、哈希、generation、输出路径或源版本。
4. DOCX 编辑保持当前单次读取、写入副本、审批和完整性行为。
5. PPTX 不能提交空的非删除替换，也不能提交过滤后只有 no-op 的变更；第一次可恢复失败只修复一次。
6. XLSX 对明确的末尾新建行请求无需额外工作簿证据即可完成选择，同时含糊请求继续 fail closed。
7. PDF 整篇和否定性结论必须具有完整声明覆盖；覆盖不可用时明确报告限制。
8. 浏览器在已验证状态发生变化后不得重复同一语义动作，除非新投影证明存在不同的重试条件。
9. 浏览器 verdict 与类型化证据矛盾时不得完成；可见呈现必须复用已证明等价的隐藏结果，或独立重新验证。
10. 缩小投影不得把任何必需覆盖维度从“完整”变成隐式或未知。

初始真实目标为：每次文档变更只读一次文档；修复时不重复读取源；每个语义阶段最多修复一次；不存在矛盾完成证据；每次模型调用都有完整投影遥测。字节比例只监控，不设为硬性验收阈值。

## 12. 实现边界

当前实现归属如下：

- 统一投影记录与审计生命周期：[`workflow_evidence_projection.go`](../../services/gateway/internal/agent/workflow_evidence_projection.go)；
- source provisioning 与 coverage 派生：[`workflow_evidence.go`](../../services/gateway/internal/agent/workflow_evidence.go)；
- 操作选择契约：[`workflow_decision.go`](../../services/gateway/internal/agent/workflow_decision.go)；
- Runtime/模型参数边界：[`workflow_model_projection.go`](../../services/gateway/internal/agent/workflow_model_projection.go)；
- XLSX 与 PPTX 源投影：[`tool_result_xlsx.go`](../../services/gateway/internal/agent/tool_result_xlsx.go) 和 [`tool_result_pptx.go`](../../services/gateway/internal/agent/tool_result_pptx.go)；
- 类型化格式策略与绑定：[`workflow_document_format_policy.go`](../../services/gateway/internal/agent/workflow_document_format_policy.go)；
- 有界语义修复：[`workflow_semantic_repair.go`](../../services/gateway/internal/agent/workflow_semantic_repair.go)；
- 浏览器状态机与 transition projection：[`browser_workflow_r2.go`](../../services/gateway/internal/agent/browser_workflow_r2.go) 和 [`workflow_browser_evidence_projection.go`](../../services/gateway/internal/agent/workflow_browser_evidence_projection.go)；
- 最终化投影：[`workflow_final_evidence_projection.go`](../../services/gateway/internal/agent/workflow_final_evidence_projection.go)。

当前实现没有为任何真实请求增加字面关键词；全部行为都通过 consumer、format、operation、
coverage、candidate、transition、validation 和 binding 契约表达。
