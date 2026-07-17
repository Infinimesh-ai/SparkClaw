# 意图路由工作流 Profile 目录

> 语言：[English](../../docs/intent-routing-workflow-domain-profiles.md) | 简体中文

截至 2026-07-16：`web.public_research`、`web.explicit_url_read`、`workspace.file_search` 与 `workspace.file_read` revision 1 已实现；其余条目是[意图路由与工作流工具暴露重构方案](intent-routing-workflow-refactor-plan.md)的有序迁移目录。随着决策语料与迁移证据落地，可以增加或修订 Profile。意图、Policy 与持久化契约以主方案为准；目录检索与 schema 物化以[工具暴露契约](intent-routing-tool-exposure-contract.md)为准。

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

## 领域矩阵

| 领域 | 代表性目标 | 自适应状态 |
|---|---|---|
| Conversation | 直接回答 | 无工具目录 |
| Web | 发现、核验、回答 | 证据深度、来源 URL、引用 |
| Browser | 读取、打开、检查、交互 | 呈现方式、tab、结构、登录接管 |
| Files | 查找、读取、回答 | workspace root 与受治理路径 |
| Document | 读取、定位、编辑、验证 | 格式、anchor、输出副本、覆盖范围 |
| Image/Weather | 检查或渲染 | 媒体来源、artifact、输出模态 |
| Memory | 搜索或提议 | 敏感度、candidate review |
| Reminder | 创建/列出/更新/取消 | 到期时间、时区、渠道、绑定 |
| Code/Command | 检查、补丁、执行 | 证据、sandbox、回滚、审批 |

## 代表性 Profile

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
  -> 使用精确 format 与 operation qualifier 添加 document.modify
  -> 目录选择只包含匹配的编辑描述

edit_completed
  -> 为输出副本添加 document.read

output_verified
  -> 完成
```

XLSX 单元格编辑不能检索 DOCX、PPTX 或 PDF 修改条目。上传的原文件保持不可变。


### 提醒

必需字段解析后，初始 scope 只包含一个语义提醒效果：create、list、update 或 cancel。Tool Directory 描述解析当前可用实现，不暴露其他 CRUD 操作。低层连接器投递继续留在内部。

### 代码与命令

代码检查从 workspace 证据能力开始。明确 patch objective 可以添加 `code.patch`；明确 test 或 command objective 可以添加 `command.sandbox.execute`。加载 `coding_helper` 不能增加其中任何目录项，所有已物化修改仍需审批。
