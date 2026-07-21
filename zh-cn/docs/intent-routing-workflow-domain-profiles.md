# 意图路由工作流 Profile 目录

> 语言：[English](../../docs/intent-routing-workflow-domain-profiles.md) | 简体中文

截至 2026-07-20 的设计状态：当前阶段目标在两个 branch 下注册五个 leaf，分别为浏览器联网搜索、单地点天气卡片、浏览器自动化、文档读取和文档编辑。这只是注册快照，不是固定分类。后续可以增加 Profile 或整个 branch，而不修改 Router 控制流。下文原有 Web/workspace 条目只保留为未来或过渡示例，不属于当前目标树。意图、Policy 与持久化契约以主方案为准；逐阶段目录检索与 schema 物化以[工具暴露契约](intent-routing-tool-exposure-contract.md)为准。

## Profile 注册与组合

每个 Profile 注册一个支持的 objective pattern，或一个有意设计的多 objective 组合。两个 Profile 以相同优先级匹配同一归一化意图时，注册测试必须失败。不支持或存在歧义的组合返回类型化澄清，不做启发式合并。

Registry 对归一化后的 Fast 语义 envelope 执行路由，并在持久化前统一校验 Plan identity、node graph、scope、transition、dependency 与 argument binding。

组合必须闭合，不能在 Runtime 中做集合并集。Composite Profile 必须注册准确 objective pattern，把每个 objective 分配给一个节点，声明全部依赖，并生成一份冻结 transition closure。只有 risk、actor、data scope 和 completion contract 全部一致时才能在代码中复用通用 fragment；否则必须定义具名组合 Profile 或进入澄清。独立匹配 Profile 的工具 scope 永远不能直接合并。

Profile 不规定具体工具顺序。每个 Profile 条目声明：

- 归一化意图模式和节点目标；
- Tool Exposure 首次检索的 CapabilityScope；
- 由类型化 ToolOutcome 激活的有界 ScopeTransition；
- 允许的风险和禁止的效果；
- 语义完成规则与决策语料用例。

Tool Exposure 只在当前 scope 内检索逻辑目录。唯一明确目录项可以自动物化；多个合理条目只以精简描述供有界选择。完成选择后才出现完整 ToolDefinition。

## 当前阶段注册树

```text
capability
  browser
    internet_search -> browser.internet_search r1
    weather         -> browser.weather r1
    automation      -> browser.automation r1
  document
    read            -> document.read r1
    edit            -> document.edit r2
```

树由 node 和 Workflow 注册组装。测试校验 identity、parent-child edge、环和 leaf Workflow reference，不断言这五个 leaf 永远是唯一合法注册。新增 branch 修改注册 catalog 和 decision corpus，不修改 Router switch。

任一时刻只有一个 Workflow stage 处于 active。Tool Exposure 接收该 stage 的 scope，只物化匹配 definition。阶段迁移会清除上一 Exposure view、增加 `ScopeRevision` 并拒绝旧 view 的调用；stage scope 绝不累积。

### 浏览器联网搜索：`browser.internet_search` r1

意图：查询正确答案依赖当前互联网状态的只读事实，并返回搜索结果。这包括当前金价、汇率、股票或指数行情、即时新闻、当前比赛结果和当前发布的日程。这些只是同一语义 leaf 内的例子，不是独立的垂直能力。

```text
stage search_info
  只暴露：web.discovery
  物化：使用已配置 Infinimesh Info provider 的 web.search
  使用冻结 query 执行

results_available 或 no_results
  -> 返回类型化搜索结果
  -> complete

provider 不可用、超时、结果非法
  -> 使用类型化原因 blocked/failed
```

Revision 1 不迁移到 `browser.read`，不打开可见 tab，也不执行浏览器自动化；这些行为需要后续已注册 Workflow 或显式 composition。

不依赖当前外部状态的静态常识保持 `unmatched`，Fast 不能强制联网。天气预警、天气新闻、历史天气调研和多地点比较使用该搜索 leaf。搜索绑定 `fact_scope=current_internet_state`，并在 Workflow 派发前把当前 owner message 冻结为 query。

### 浏览器天气卡片：`browser.weather` r1

意图：把一个明确地点的当前天气或短期预报渲染为专用天气卡片。

```text
前置条件
  fact_scope = weather_snapshot
  从当前 owner message 复制并校验唯一地点

stage render_weather_card
  只暴露：weather.card.render
  物化：media.render_weather_card
  绑定冻结的精确地点

artifact_available
  -> 返回 PNG 天气卡片
  -> complete

地点缺失、查询失败或 artifact 缺失
  -> clarify，或使用类型化原因 blocked/failed
```

该 leaf 不覆盖天气预警、新闻、历史调研或多地点比较；这些请求依赖更广的当前互联网证据，路由到 `browser.internet_search`。它也不暴露 `web.search`、page read 或浏览器交互。

### 浏览器自动化：`browser.automation` r1

意图：在受管浏览器中打开或聚焦一个已明确的目标 URL。

```text
前置条件
  target URL 确定且已冻结，否则 clarify

stage scan_tabs
  只暴露：browser.list_tabs
  持久化类型化 page ID 和规范化 URL

存在精确 target URL
  -> stage focus_existing
  -> 只暴露绑定匹配 page ID 的 browser.focus

不存在精确 target URL
  -> stage open_new
  -> 只暴露绑定冻结 target URL 的 browser.open

focus_completed 或 open_completed
  -> 返回浏览器结果
  -> complete
```

Revision 1 不暴露 navigate、click、type、select、page read 或 Web search。后续浏览器 branch 或 Workflow revision 可以通过注册增加这些行为，无需修改当前 Router 算法。

### 文档读取：`document.read` r1

意图：检查一个受治理文档或图片的内容并返回结果。

```text
preflight inspect_type
  解析精确受治理 path
  检测 extension/MIME/signature 并拒绝不一致
  记录源文件大小，对没有可用策略的资源明确拒绝或 deferred
  不向 Agent 暴露工具（或只暴露未来注册的 type-inspector）

stage read_by_type
  只暴露与检测类型和精确 path 兼容的 reader
  纯文本/代码 -> 有界文件 reader
  DOCX/XLSX/PPTX -> 格式兼容的文档 reader backend
  PDF -> PDF 文本 reader
  PNG/JPEG/GIF/WebP -> Fast 多模态图片 reader
  文档 reader -> 完整解析 -> 生成带稳定位置的 structured_document_v1
  图片 reader -> 返回带尺寸和模型来源的有界语义摘要

content_available
  -> 返回类型化内容与 reference
  -> complete
```

Read stage 不可见搜索、编辑或无关格式工具。直接图片分析仍属于 `document.read` r1：图片签名预检后只暴露 `images.inspect`，继续采用 12 MiB 原图限制和 Fast-only 模型策略。8 MiB 源数据与 200,000 bytes 完整抽取限制继续适用于结构化文档 reader；Path 缺失或类型不支持时 clarify 或 block，不扩大 exposure。

### 文档编辑：`document.edit` r2

意图：编辑一个受治理文档并返回新结果。
所有对现有文档内容的已检测变更都进入该 Profile；明确读取和总结仍进入 `document.read`。路由不把自然语言修改词语映射为具体 operation。如果请求的格式专属 operation 没有已注册 editor，r2 仍先执行受治理的结构化读取，再明确 block，而不是返回只读成功或选择无关 editor。

```text
确定性 preflight
  解析唯一权威受治理附件 path 和 output-copy path
  检测 extension/MIME/signature 并拒绝不一致

stage read_for_edit
  只暴露与检测格式和冻结 input path 匹配的 reader
  把完整小文档解析为 structured_document_v1
  保留 document.p[25]、sheet/cell、slide 和 page 等稳定 locator

document_evidence_available
  用检测格式专属 edit scope 替换 read scope
  根据请求与结构化证据选择一个精确、带 operation qualifier 的 editor entry
  根据结构化 observation 形成有边界的修改参数

stage edit_by_type
  只物化与检测格式和请求 operation 匹配的已选 editor
  纯文本/Markdown -> 有边界文本替换 entry
  DOCX -> 兼容 DOCX editor entry
  XLSX -> 兼容 XLSX editor entry
  PPTX -> 兼容 PPTX editor entry
  PDF -> 兼容 PDF transform entry
  Policy -> 在 reversible execution 前持久化可恢复 approval
  editor 重新 inspect -> read -> structure -> locate -> constrain
  row/slide 结构目标 -> 一个稳定实体，而不是全部子 block
  只 apply 到不存在的新 output copy
  校验每个类型化输出 -> 重新计算冻结输入哈希 -> 失败时清理

edit_completed
  -> 返回全部类型化 output artifact 和 operation result
  -> complete
```

Revision 2 返回可审计 `change_summary`，写入一个或多个 output copy，并且只有在每个类型化输出都存在且输入哈希未变化后才结束。统一 `WorkflowResult` 同时携带 `change_summary` 和新文件。目标未找到、歧义或命中数不符时在 approval 或 mutation 前 block。后续 revision 可以注册独立验证阶段，但当前不能静默插入。其他格式或 operation 的工具保持不可见。

## 旧上下文组装边界

这些 Workflow 继续使用既有 context assembler 组装会话历史、owner context、当前用户文本、附件和压缩上下文格式。本阶段不引入新的 context graph、reducer 或逐 Workflow prompt assembly。Route identity、active stage、冻结资源和 Exposure binding 只包在旧组装上下文外围。

旧 context 可以提供 evidence，但其中的 candidate-tool 和 Skill 列表不是可见性权威；只有 active stage 的 Tool Exposure view 能进入 Agent。

## 未来扩展矩阵（非当前注册）

| 领域 | 代表性目标 | 自适应状态 |
|---|---|---|
| Conversation | 直接回答 | 无工具目录 |
| Web | 发现、核验、回答 | 证据深度、来源 URL、引用 |
| Browser | 读取、打开、检查、交互 | 呈现方式、tab、结构、登录接管 |
| Files | 查找、读取、回答 | workspace root 与受治理路径 |
| Document | 读取、定位、编辑、验证 | 格式、anchor、输出副本、覆盖范围 |
| Image | 检查或转换 | 媒体来源、artifact、输出模态 |
| Memory | 搜索或提议 | 敏感度、candidate review |
| Reminder | 创建/列出/更新/取消 | 到期时间、时区、渠道、绑定 |
| Code/Command | 检查、补丁、执行 | 证据、sandbox、回滚、审批 |

## 未来或过渡 Profile 示例

### 公共 Web 调研

意图模式：`domain=web`、`operation=search`，没有明确 URL。

```text
节点目标：使用充分的公共证据回答
初始 scope：web.discovery

检索目录
  -> 物化 web.discovery 实现
  -> ToolOutcome

evidence_sufficient
  -> 完成

要求来源证据，且结果包含类型化 URL reference
  -> 把 scope 替换为 web.page.read 一次
  -> 重新检索目录
  -> 物化 page-read 实现

没有结果、缺少类型化 URL 或 capability 不可用
  -> 明确说明限制或 blocked
```

该 Profile 既不固定要求读取结果页，也不禁止读取页面。它从 discovery 开始；只有归一化意图要求 source depth 时才允许页面证据。页面 URL 必须准确匹配 discovery 后持久化的受治理 URL reference；任意模型 URL 会在 ToolHub 执行前被阻止。可见 tab 与交互效果始终禁止。

### 明确 URL 读取

意图模式：`domain=web`、`operation=read`，target kind 为 `explicit_url`。

```text
节点目标：根据用户提供的页面回答
初始 scope：web.page.read

content_available
  -> 完成

structure_required
  -> revision 1 中 blocked；后续 Profile revision 可以声明 inspect/wait

authentication_required
  -> blocked
```

URL 已知，因此初始 scope 不包含 `web.discovery`。执行 URL 必须等于冻结 intent 中的确定性明确目标。页面读取也不意味着打开可见 tab 或执行页面交互。

### Workspace 文件搜索

意图模式：`domain=workspace`、`operation=search`，没有明确 path。

```text
初始 scope：workspace.file.search
results_available 或 no_results -> 完成
mutation、image 或 code 专用请求 -> 不匹配
```

该 Profile 覆盖明确 Workspace 搜索，以及没有更强公共 Web 或专用领域信号的普通本地 find/search 请求。它通过 Tool Exposure 物化 `files.search`，不会预先暴露 `files.read`。

### 明确 Workspace 文件读取

意图模式：`domain=workspace`、`operation=read`，target kind 为 `workspace_path`。

```text
初始 scope：workspace.file.read
content_available -> 完成
内容缺失或读取失败 -> blocked
```

执行 path 必须等于 intent 中保存的确定性 target。图片检查与修改请求不匹配该 Profile。读取成功后也不能扩展到编辑，除非存在单独注册且明确授权的 mutation Profile。

### 浏览器打开

已知 URL：

```text
初始 scope：browser.tab.open
open_completed -> 完成
```

只有命名目标、没有 URL：

```text
初始 scope：web.discovery
target_url_resolved 且原始 objective 明确要求打开
  -> 添加 browser.tab.open
  -> 重新检索目录
open_completed -> 完成
```

打开成功后不启用冗余 navigate、click、type 或 select。浏览器状态和 tab listing 继续作为 Runtime preflight，除非 owner 明确要求检查它们。

### 浏览器交互

意图模式必须包含明确交互 objective。

```text
初始 scope：browser.page.inspect
target_ref_resolved
  -> 只添加用户请求的 browser.interact qualifier
page_changed 或 stale_ref
  -> 在重试预算内回到 browser.page.inspect
authentication_required
  -> waiting_human
```

页面读取或搜索 objective 不能仅因为 ToolOutcome 中出现可点击元素就迁移到交互。

### 文档编辑

```text
节点目标：修改副本并验证请求的变化
初始 scope：document.read

format_and_anchor_resolved
  -> 使用精确 format qualifier 添加 document.modify
  -> 目录选择一个带 operation qualifier 的编辑描述

edit_completed
  -> 为输出副本添加 document.read

output_verified
  -> 完成
```

XLSX 单元格编辑不能检索 DOCX、PPTX 或 PDF 修改条目。插入、删除、追加、行和单元格请求共用同一个 Profile，仅在读取后的目录选择阶段产生区别。上传的原文件保持不可变。


### 提醒

必需字段解析后，初始 scope 只包含一个语义提醒效果：create、list、update 或 cancel。Tool Directory 描述解析当前可用实现，不暴露其他 CRUD 操作。低层连接器投递继续留在内部。

### 代码与命令

代码检查从 workspace 证据能力开始。明确 patch objective 可以添加 `code.patch`；明确 test 或 command objective 可以添加 `command.sandbox.execute`。加载 `coding_helper` 不能增加其中任何目录项，所有已物化修改仍需审批。
