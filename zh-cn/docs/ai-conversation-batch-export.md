# RevivalStack 时间线批量导出

> 语言：[English](../../docs/ai-conversation-batch-export.md) | 简体中文

SparkClaw 派生版本 `3.1.0-sparkclaw.1` 为 ChatGPT、Claude、Gemini、Grok 增加批量导出。仓库和受管清单已移除 pionxzh ChatGPT Exporter，保留 RevivalStack 的 MIT 声明，并关闭上游自动更新，避免覆盖本地派生版本。

## 浏览器使用

安装随仓库提供的 `tools/browser-userscripts/revivalstack.user.js`，登录平台并展开历史列表：ChatGPT 侧栏、Claude `/recents`、Gemini `/app`、Grok `/history`。填写当前账号/工作区标识，点击 **选择导出文件夹**，再点击 **开始批量导出**，允许任务弹窗。文件夹选择与弹窗分别使用一次明确点击。保持源页面和弹窗打开，直到完成；**停止批量** 中止后续工作，已保存文件保留。

脚本扫描懒加载和虚拟化的可见列表，并继续访问页面实际提供的同平台项目、归档、历史集合链接，按对话 ID 去重。有准确更新时间的记录优先按时间从旧到新处理；没有时间戳时，各列表使用显示顺序的逆序，跨集合保留发现顺序，因此置顶或分组可能影响顺序。无法访问的集合会记录为检索失败，不能报告全部成功。源页面保持原地址，采集在独立弹窗内进行；手动消息过滤不会限制批量采集。

每份 JSON 保留脚本原始序列化结果。文件保存后回读校验 SHA-256，再更新账号范围内的 ledger。保存或记账失败会在下次重试；只有已有文件仍通过校验时才能跳过，丢失或损坏的文件会重新采集。没有更新时间的对话会重新访问，通过标题/消息内容指纹判断是否重复，单独变化的导出时间不会生成重复文件。文件名包含平台、对话 ID 和内容哈希，避免同名标题覆盖。报告列出已导出、跳过及失败项。

账号标识只区分**本地检查点**，不是已核验的平台身份。切换账号或平台工作区后应使用不同标识，不要在导出过程中切换登录。脚本不复制密码、令牌或 Cookie。手动模式使用 File System Access API 和同源弹窗，需要桌面 Chromium 及目录/弹窗授权；点击下载不等于成功保存。

## SparkClaw 自动化入口

向可调用接口提供已授权的 Playwright BrowserContext 和当前会话工作区：

```js
import { exportTimeline } from './scripts/ai-chat/batch-export.mjs';
const receipt = await exportTimeline({
  context, provider: 'claude', workspaceRoot: sessionWorkspace,
  accountScope: 'my-account-workspace', signal,
});
```

接口只创建、关闭自己的任务页。原始 JSON、ledger 和每次 manifest 写入 `ai-chat-exports/<provider>-<account-hash>/`。文件原子发布、回读校验后才记账；目录锁阻止同一范围并发导出，进程崩溃后的遗留锁须确认没有导出进程占用后再清理。取消或超时会关闭任务页，已保存记录保留，不修改其他标签页或邮件文件。

运行中的专用浏览器默认通过原生 Bridge 连接：

```sh
node scripts/ai-chat/export-batch.mjs --provider chatgpt \
  --workspace /absolute/session/workspace --account my-account-workspace \
  --bridge
```

通过已有本地运行时配置提供 `PLAYWRIGHT_MCP_EXTENSION_TOKEN`、`PLAYWRIGHT_MCP_EXECUTABLE_PATH` 和 `PLAYWRIGHT_MCP_USER_DATA_DIR`；可用 `SPARKCLAW_PLAYWRIGHT_CLI_ENTRY` 指定已安装的 CLI。凭据不进入参数、报告或仓库。其他明确授权的本地环境也可使用 `--cdp http://127.0.0.1:PORT`。

正式 Bridge 入口使用已安装控件，不注入候选脚本，也不设两条抽样上限。每个平台单独或顺序调用。命令不负责安装、启动浏览器、修改配置或重启服务，需要 browser-controller 的 Playwright 依赖。固定控件为 `sparkclaw-batch-scan`、`sparkclaw-batch-capture`、`sparkclaw-batch-status`、`sparkclaw-batch-output`，不提供任意代码执行接口。

`scripts/ai-chat/qualify-bridge.mjs` 仅供候选脚本评测，会在独立测试页临时注入脚本，每平台最多抽样两条，不更新共享安装。

现有 Gateway 的 `ai_chat.export` 仍为单对话原生下载流程；新增批量 callable/CLI **尚未接入自然语言批量路由**。后续接入必须保持会话工作区与任务所有权。

## 覆盖与评测

**当前实现可发现历史的批量导出，尚不能证明每个账号的所有对话均已检索。** 稳定滚动不代表隐藏归档、项目或未展示对话都已包含。报告保留 `complete:false`、`coverage:unknown` 和 `saved_visible_history`/`partial`。列表阻塞、没有明确空态及检索上限会显式失败；侧栏折叠、DOM 变化或登录重定向可能导致检索失败。正文稳定也不证明超长虚拟化消息已经完整加载。

行为测试覆盖四平台 URL 隔离、虚拟列表、时间排序、失败重试、内容变化、文件丢失和取消。隔离 Chromium 覆盖完整脚本初始化、导出/重跑、保留其他标签页、真实弹窗和 OPFS 文件写入；目录选择器使用替身，不能当作原生对话框验收。原生 Tampermonkey 隔离生命周期覆盖首次导入、升级淘汰、保留用户副本和再次部署，仅替换测试环境的 managed-policy 传输。

[2026-09-10 专用浏览器真实网站评测](ai-conversation-live-eval-20260910.md) 保存了四平台共七条已有对话，并核对文件哈希及网页角色数量。该轮使用临时注入；全账号、超长历史、共享安装升级及原生目录对话框仍未验收。

## 维护与集成

修改 `tools/browser-userscripts/src/timeline-batch.js` 后执行：

```sh
node tools/browser-userscripts/build-batch.mjs
node tools/browser-userscripts/build-batch.mjs --check
node --test tools/browser-userscripts/test/*.test.mjs
python3 -m unittest discover -s scripts -p test_browser_components.py
```

构建将源码嵌入 userscript、生成 `timeline-core.mjs` 并更新清单 SHA-256；`--check` 校验生成物。浏览器测试需要 `SPARKCLAW_TEST_PLAYWRIGHT` 和 `SPARKCLAW_TEST_CHROMIUM`，原生组件生命周期另设 `SPARKCLAW_TEST_COMPONENT_LIFECYCLE=1`；设置 `SPARKCLAW_TEST_NATIVE_BRIDGE=1` 和 `SPARKCLAW_TEST_CLI` 可验证完整安装脚本经真实 Bridge 执行，命令见评测报告。

由原整合任务合并此 worktree，注意与邮件任务共享的组件清单和安装测试。保留 RevivalStack UUID。迁移只通过 Tampermonkey 原生接口淘汰 UUID `c6378bd8-6136-4f1b-81be-2c0fed94baf3` 且 `system=true` 的旧脚本，保留用户副本，包括同名脚本。检查器验证当前受管 UUID 并拒绝残留旧受管身份。本次不修改邮件 provider/controller 注册。

组件 r8 在原生首次安装来源检查中识别清单固定的受管 UUID/源码哈希，避免刚导入的合法脚本被误判为来源不明；检查器拒绝仍有来源/黑名单阻断标记的脚本。隔离完整安装执行已通过真实 Bridge 短对话导出和重跑验证，未更新共享安装。
