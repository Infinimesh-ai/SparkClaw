# 浏览器功能完善计划

> 语言： [English](../../docs/browser-automation-improvement.md) | 简体中文

本文档专门规划 SparkClaw 的 browser-backed web access 完善方向。整体架构仍以 [架构](architecture.md) 为准；浏览器读取和交互的路线图放在本文档维护。浏览器呈现模式的编码约束见 [浏览器模式代码编写指南](browser-modes-coding-guide.md)，隐藏真实浏览器里程碑见 [隐藏 Chromium 浏览器访问计划](browser-hidden-chromium-access.md)。

## 目标

把公开网页搜索、URL 读取和真实页面交互统一到一个浏览器能力下。浏览器层应该是真实浏览器身份下的访问系统，而不是独立的静态 fetch 分支。

目标流程是：

```text
需要发现来源时先 web.search
  -> browser.read 选择符合模式的读取路径
      - 协作/可见/强制 session 读取使用 ChromeDevTools MCP
      - 自主/隐藏读取在 hidden provider 可用前避免打开可见 Chrome tab
  -> 选择浏览器 session 时，evaluate_script 在渲染和懒加载后读取 DOM/HTML
  -> ToolHub 在浏览器外部用 @mozilla/readability 提取正文
  -> 如果正文不完整，再用 browser.snapshot 查看页面结构
  -> browser.click 或 browser.navigate 进入展开区域、分页或内部链接
  -> 页面状态变化后再次 browser.read
```

## 设计规则

- `browser.read` 对协作/可见读取、显式强制 session 读取，或已有 hidden/headless provider 支持的自主读取，优先使用真实浏览器会话。
- 当前 provider 不能隐藏页面时，自主 hidden 读取不能打开用户可见的 Chrome tab；在 hidden provider 可用前，应使用 direct HTTP 加 Readability。
- 下一个自主浏览器里程碑是 hidden Chromium/Chrome provider：真实渲染、执行 JavaScript、提取 DOM，但不展示窗口。
- `browser.read` 不应为了普通网页读取强制执行 `take_snapshot`。
- Readability 是 rendered HTML 的默认正文提取器。
- 结构快照是诊断和交互辅助，只在正文不足或页面结构重要时使用。
- Search 仍是发现工具，但 search、read、interaction 都归属于 browser access domain。
- 已有浏览器登录态可以被使用，但密码、验证码、2FA、支付确认等 human-only 步骤必须停下来交给用户。
- 所有浏览器观察结果都继续视为不可信外部内容。

## 标准读取路径

对于直接 URL 或搜索结果中的来源页，首轮读取只做获得正文所需的工作。当选择浏览器 session 路径时，使用：

```text
ChromeDevTools MCP new_page
  -> 等待页面加载和渲染状态
  -> evaluate_script
       - 适度滚动，触发常规懒加载
       - 收集 document.documentElement.outerHTML
       - 收集 document.body.innerText
       - 收集 title、lang、readyState、contentType 和登录拦截提示
  -> ToolHub 中用 jsdom + @mozilla/readability 解析
  -> 返回标题、正文、元数据和读取诊断信息
```

首轮读取的预期输出：

- 浏览器路径成功时返回 `read_mode=browser_session`。
- 自主 hidden 读取未使用可见浏览器 session 时返回 `read_mode=direct_http`。
- DOM/HTML 来自浏览器时返回 `rendered=true`。
- Readability 成功时返回 `extractor=readability` 和 `readability_status=applied`。
- `browser_actions` 通常只包含 `new_page` 和 `evaluate_script`，不包含 `take_snapshot`。
- 如果首轮正文完整，可以没有 `browser_snapshot_text`。

如果浏览器会话不可用，`browser.read` 可以退回 direct HTTP，并必须用 `read_mode=direct_http_fallback` 标明。

## Snapshot 触发规则

只有首轮读取出现以下信号时，才调用 `browser.snapshot`：

- Readability 没有正文，或对于明显应有内容的页面只返回很短文本。
- 页面像索引页、目录页、搜索结果页、登录墙、验证码页或付费墙。
- `auth_challenge_detected=true`。
- rendered HTML 或正文被截断。
- 用户询问控件、评论、表格、下载、菜单、标签页、分页或页面结构。
- 答案可能依赖非正文内容，例如侧栏、折叠区、附件、相关链接或评论区。

Snapshot 的作用是识别稳定 refs/uids、内部链接和可见标签。它不替代 Readability 作为常规正文提取路径。

## 交互闭环

当 snapshot 显示相关页面控件时，runtime 可以进入有界闭环：

```text
browser.snapshot
  -> 选择一个明确控件或内部链接
  -> browser.click 或 browser.navigate
  -> 必要时等待页面状态变化
  -> browser.read
  -> 重新评估 Readability 输出
```

示例：

- 点击 `展开`、`更多`、`阅读全文`、`显示全部` 后重新读取。
- 当 snapshot 暴露 `下一页`、`招生章程`、`下载`、`通知正文` 或来源文档链接时跳转读取。
- 遇到登录、验证码、短信码、2FA 或支付确认时停止并询问用户。

闭环必须有边界。如果一次重试仍不能得到有效内容，应返回已有证据并解释限制。

## 工具职责

| Tool | Responsibility |
|---|---|
| `web.search` | 发现候选公开 URL 和来源页。 |
| `browser.read` | 按 mode 安全读取页面；选择浏览器 session 时抓取 rendered HTML，并执行 Readability 正文提取。 |
| `browser.snapshot` | 查看页面结构、控件、refs/uids、内部链接和非正文页面元素。 |
| `browser.click` | 激活最新 snapshot 中一个明确 ref/uid。 |
| `browser.navigate` | 在保留浏览器上下文的前提下进入已知 URL。 |
| `browser.screenshot` | 用户要求截图或 snapshot 文本不足时做视觉确认。 |

## 实现状态

当前 browser-read 实现会在协作/可见或 forced-session 读取中通过 ChromeDevTools MCP 打开页面，用 `evaluate_script` 抓取 rendered DOM/HTML，并在浏览器外部运行 `@mozilla/readability`。当可用 ChromeDevTools provider 会打开可见 tab 时，自主 hidden 读取当前使用 direct HTTP 加 Readability。

默认 `browser.read` 路径已经不再把结构快照作为每一次浏览器会话读取的固定步骤。显式页面结构检查仍然使用 `browser.snapshot`，并映射到 ChromeDevTools MCP 的 `take_snapshot`。

剩余工作：

- 实现 [隐藏 Chromium 浏览器访问计划](browser-hidden-chromium-access.md) 中定义的 hidden Chromium provider，让自主读取既能真实浏览器渲染，又不展示窗口。
- 将自主 `browser.snapshot` 从“可见浏览器当前 tab”改为 hidden page 或 archived HTML 结构提取。
- 改进 snapshot 之后的后续选择质量，尤其是如何选择最安全的单个内部链接或展开控件。
- 登录和反爬处理保持为显式的后续扩展。

## 实施阶段

1. 文档和契约更新。
   - 以本文档作为浏览器完善计划的集中入口。
   - 更新工具描述和 skill 规则，明确 snapshot 是按需步骤。

2. 移除 `browser.read` 的强制 snapshot。已完成。
   - 选择浏览器 session 时，默认路径保持 `new_page -> evaluate_script -> Readability`。
   - 保留 direct HTTP fallback。
   - 只有真正收集 snapshot 时才返回 snapshot 字段。

3. 增加正文充分性诊断。已完成。
   - 添加或复用 `readability_status`、`readability_length`、`browser_html_truncated`、`auth_challenge_detected` 和 `needs_structure_snapshot`。
   - 让 runtime observation adapter 清楚暴露这些信号。

4. 增加有界后续行为。部分完成。
   - 由 ReAct 在诊断显示首轮正文不足时决定 `browser.snapshot -> browser.click/navigate -> browser.read`。
   - 暂不把多次点击隐藏在单个工具调用里，确保行为容易追踪。
   - 当前 runtime 行为：当 `browser.read` observation 包含 `needs_structure_snapshot=true` 时，下一步 ReAct 可以看到 `browser.snapshot`、`browser.navigate` 和 `browser.wait`；出现 snapshot observation 后，也可以看到需要 approval 的 `browser.click`。

5. 预留登录和反爬扩展。
   - 浏览器 profile/session 配置保持显式。
   - 可以复用已有登录态。
   - human-only 验证必须停下来交给用户。
   - 为未来反爬处理留接口，但不加入静默凭证或验证码自动化。

6. 增加 hidden Chromium access。
   - 按 [隐藏 Chromium 浏览器访问计划](browser-hidden-chromium-access.md) 实施。
   - hidden provider 可用时，自主 hidden 读取应使用真实浏览器渲染。
   - 自主 snapshot 应检查 hidden page 或 archived HTML，不能检查无关的可见 `about:blank` tab。

## 验证标准

本轮最小验证：

- 单测证明启用 browser automation 时，`browser.read` 使用浏览器会话 HTML 和 Readability。
- 单测证明自主 hidden `browser.read` 不调用会打开可见页面的 browser-session provider。
- 单测证明默认 `browser.read` 不调用 `take_snapshot`。
- 单测或 adapter 测试证明显式 snapshot 仍映射到 ChromeDevTools MCP `take_snapshot`。
- `go test ./services/gateway/internal/browserautomation -count=1`
- `go test ./services/gateway/internal/toolhub -count=1`
- `go test ./services/gateway/internal/agent -count=1`
- `go test ./services/gateway/...`
- `git diff --check`
