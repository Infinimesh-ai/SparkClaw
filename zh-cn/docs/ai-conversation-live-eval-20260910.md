# 2026-09-10 专用浏览器真实网站评测

> 语言：简体中文 | [English](../../docs/ai-conversation-live-eval-20260910.md)

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

## 后续范围与阻塞

正式 Bridge 入口已改为使用已安装控件、不临时注入、不设两条抽样上限；集合链接遍历、目录与弹窗分步点击、精确受管 UUID 淘汰均为后续修改，不能把前述真实网站结果当作这些新修改的验收证据。

后续连接专用浏览器成功，但 Chromium 拒绝 Bridge 导航到 Tampermonkey 管理页与扩展管理页；电脑控制工具仅提供 Codex 内置浏览器，未提供专用浏览器或系统文件夹对话框。因此未修改或重启共享安装，已释放浏览器协调窗口供邮件任务继续验证。完整安装初始化、共享升级及文件夹选择成功/取消/弹窗失败尚未完成真实网站验收。

最终隔离组合测试 18 项通过，包括原生 Tampermonkey 首次导入、升级淘汰、用户副本保留及再次部署。原生生命周期只替换 managed-policy 测试传输，保留原生下载、哈希检查及导入行为。组件检查 10 项、权威文档检查 77 份 Markdown 通过；后续强化的 Bridge 所有权回归也通过，额外弹窗及目标页丢失均不会使导航或清理转移到其他页面。

手动路径模拟页使用完整脚本初始化、真实弹窗和真实 OPFS 文件写入，但文件夹选择及取消使用替身。另一次原生安装后控件探针在导入和 bootstrap 注册成功后仍超时，该失败随后按下文查明并修复；未以模拟初始化替代安装执行验收。

## 完整安装执行验收补充

根因是产品初始化时序：新安装 unpacked 油猴扩展时，原生 `onInstalled` 来源检查晚于受管导入，把刚导入的脚本标为 `evilness=12`（来源不明），因此数据库中虽然启用，却不进入实际执行列表。原生 userScripts 权限和注册均正常；拦截式测试页面和本地 HTTPS 页面都能复现。

组件 r8 只把 UUID、`system=true`、UTF-8 源码 SHA-256 三者同时匹配清单的脚本认作已知受管来源。其他脚本仍接受原生来源检查，其他黑名单检查保持有效。profile 检查现在拒绝非零 `evilness`，不再仅凭启用/版本/源码哈希判定就绪。

更新后的原生生命周期测试保留同一安装路径和浏览器 profile 来模拟升级，每次新导航都必须实际出现控件；还验证旧来源标记恢复、淘汰旧受管 UUID、保留用户副本及再次部署。

新增 `installed-bridge.test.mjs` 在独立 Chromium 内使用真实油猴、真实 SparkClaw Browser Bridge 扩展和真实 native-host/launcher，经正式 `connectBatchBridge()`、`exportTimeline()` 导出 ChatGPT 短对话测试页中的两条消息，核对文件哈希及消息文本，再次运行跳过未变内容，并保留原 owner 页面。没有注入 userscript、替换 `UIManager.init()` 或 GM API。测试还加入一个不在产品清单中的 system 脚本，要求原生来源保护仍标为 12。另一个原生负例保留已知受管 UUID 但改变源码字节，仍必须被拒绝。生命周期测试按当前 manifest 的完整受管 UUID 集合断言，可与邮件脚本清单组合。

仅 managed-policy 测试传输和网页内容使用 fixture；profile、凭据与 Unix socket 全部独立，不改共享浏览器或邮箱安装。这补齐了隔离安装执行证据，不增加此前真实登录网站的七条对话样本数。

```sh
SPARKCLAW_TEST_PLAYWRIGHT=/absolute/playwright/index.mjs \
SPARKCLAW_TEST_CHROMIUM=/absolute/chromium/chrome \
SPARKCLAW_TEST_CLI=/absolute/@playwright/cli/playwright-cli.js \
SPARKCLAW_TEST_COMPONENT_LIFECYCLE=1 \
SPARKCLAW_TEST_NATIVE_BRIDGE=1 \
node --test tools/browser-userscripts/test/component-lifecycle.test.mjs \
  tools/browser-userscripts/test/installed-bridge.test.mjs
```

测试仅在临时 profile 内注册原生消息宿主，无需修改用户的宿主注册、控制凭据或 Controller socket。共享安装升级、OS 原生目录对话框、隐藏历史、超长正文和自然语言批量路由的既有边界仍保留。

最终针对性验收：原生测试 3/3、组件检查 10/10、权威文档检查 77 份全部通过。
