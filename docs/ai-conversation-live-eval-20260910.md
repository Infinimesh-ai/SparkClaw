# 2026-09-10 专用浏览器真实网站评测

本轮直接连接运行中的 SparkClaw 专用 Chromium，通过其 Browser Bridge 的独立任务页访问 ChatGPT、Claude、Gemini、Grok。使用已有登录状态及已有对话，没有发送测试消息、创建对话或改动邮箱标签页。

实际安装的是 RevivalStack 3.1.0。本轮把本 worktree 的派生脚本临时加载到测试页：批量和正文提取代码保持原样，初始化改为独立控件容器，GM 设置使用默认值，避免与既有油猴控件冲突。没有升级共享油猴安装、重启浏览器或部署 Gateway。

## 结果

| 平台 | 当前可见时间线 | 首次成功保存 | 导出消息数 | 重跑 |
|---|---:|---:|---:|---|
| ChatGPT | 2 条 | 2 条 | 2、4 | 两条均跳过，无重复文件 |
| Claude | 2 条 | 2 条 | 6、2 | 两条均跳过，无重复文件 |
| Gemini | 2 条 | 2 条 | 4、4 | 两条均跳过，无重复文件 |
| Grok | 1 条 | 1 条 | 2 | 一条跳过，无重复文件 |

共 7 条真实对话、24 条消息成功保存。逐条重新打开网页核对：7 份文件 SHA-256 均与 ledger 相符，网页与导出中的 user/ai 数量均一致。对包含相应内容的样例，检查了 14 个 `pre code` 文本节点和 90 个表格单元格，文字均可在导出结果中找到；节点数不等于独立代码块数。这不是逐字符或视觉排版无损认证。

脚本会重新访问没有更新时间的对话，再通过标题/消息内容指纹判断是否重复。导出时间变化不会单独导致重复保存。此次可见对话数均未超过评测上限（每个平台最多取两条），所以没有因评测抽样排除本次发现的对话。

## 实测发现并修正的问题

1. **后台页点击和等待超时**：普通 Playwright 鼠标点击需要页面稳定帧，默认 `waitForFunction` 使用 rAF；Bridge 后台任务页可能暂停这些帧。改为等待控件挂载后直接触发控件，并以定时轮询等待状态。
2. **Gemini 检索不能结束**：真实侧栏会保留已经隐藏的加载动画。原代码仍将它视为 busy。现在只认具有可见尺寸的加载/更多控件。
3. **Grok 时间线未发现**：真实侧栏使用 `[data-sidebar="sidebar"]`，不一定是 `nav`/`aside`；页面初始加载也较慢。新增该根节点支持，并在没有对话或明确空态时等待最多约 30 秒再判定未找到。
4. **专用浏览器连接方式**：该浏览器没有普通 CDP 调试端口。新增可复跑的 `scripts/ai-chat/qualify-bridge.mjs`，使用已配置 Bridge launcher 和调用方提供的本地浏览器控制凭据。它通过现有后台渲染标记保持任务页可运行，不激活用户标签页。

回归测试增加暂停 rAF、隐藏加载/更多控件、Grok 真实侧栏属性和延迟列表加载场景。批量测试 14/14、浏览器组件测试 9/9 通过；组合测试在隔离 Chromium 上完成四平台模拟页面的导出、重跑和已有标签页保留。

## 复跑与证据

```sh
node scripts/ai-chat/qualify-bridge.mjs \
  --workspace /absolute/private/session/workspace \
  --account account-workspace-label \
  --providers chatgpt,claude,gemini,grok
```

由现有凭据/运行时配置提供 `PLAYWRIGHT_MCP_EXTENSION_TOKEN`、`PLAYWRIGHT_MCP_EXECUTABLE_PATH`（Bridge launcher）和 `PLAYWRIGHT_MCP_USER_DATA_DIR`。不要把凭据写入命令参数、报告或版本库。可用 `SPARKCLAW_PLAYWRIGHT_CLI_ENTRY` 指定已安装的 CLI。

真实 JSON、ledger、逐次 manifest、源网页对照结果和资格评测汇总位于本 worktree 的忽略目录：`data/workspaces/ai-chat-live-eval/20260910/`。资格汇总记录候选脚本和临时注入内容的 SHA-256。正文、账号信息和对话标题不进入本报告或提交。

**通过的是当前登录环境下的短对话和可见时间线评测。** 未证明账号全部历史（归档、项目内、隐藏或平台未展示的对话）已完整检索，也未验证超长虚拟化正文、限流、大规模账号、手动文件夹选择流程、共享油猴升级和 Gateway 自然语言批量路由。文件保存状态仍使用 `saved_visible_history`，覆盖范围保持 unknown。
