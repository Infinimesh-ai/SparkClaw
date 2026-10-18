# 桌面体验实现与验证

> 语言：简体中文 | [English](../../docs/desktop-experience-validation.md)

日期：2026-10-18。本次在 `codex/desktop-experience` 上以 `cd7d5ccca5e522b7b140cc2ac14b1c592e591be9` 为基线，本地实现[桌面体验设计](desktop-experience-design.md)。这不代表发布、打包、安装、真实后端验收或 ISCP 上游接受。

## 已实现范围

P0 统一系统字体、字号／行高、间距、表面和对比度变量。保留原有 `default`／`large` 存储值；大号文字不再整体缩放工作台。阅读与输入共用 760 CSS px 外框、16px 文字内边距。桌面导航保留 244px 宽度，顶栏采用 52px。通过按元素类型选择，修复消息滚动容器与 Markdown 列表共用类名造成的实际样式冲突。

P1 保留原输入组件及上传、文件、邮件、语音入口。文字增长到六行后内部滚动；上传失败保留草稿和成功附件，并在旁边提供重试。IME 组合态及组合结束保护避免误发送。搜索支持方向键／Enter，模态弹窗约束焦点，Escape 一次只关闭一层并恢复触发点焦点。Cmd/Ctrl+B 不拦截编辑区，Cmd/Ctrl+, 打开设置，原有新建／搜索快捷键保留。

桌面顶栏直接打开原浏览器并选择未使用的空白个人标签，仅在需要时新建。存在未提交地址文字的空白标签不会复用。收起后保留已挂载的标签和地址状态；关闭最后一个个人标签后保留可输入地址的空白界面。任务标签继续只读。`BrowserPanel` 新增可选 `open`、`visible`、`onCollapse`、`wide`、`onWideChange`，兼容原调用。

面板按实测 CSS 宽度和 24px 回差切换分栏／聚焦布局。缩窗保留键盘焦点所在区域；显式放大保持到用户恢复。原生 bounds 将 CSS 坐标转换一次为 Electron DIP，与显示器像素密度独立。收起面板和应用弹窗会暂停原生显示，后台 Host 活动不能自行显示另一标签。没有页面重载或业务数据迁移。

滚动观察原消息容器，仅在底部跟随；主动上滚或选中文字后暂停，并按会话恢复位置。锚点依次读取 `data-scroll-anchor`、`data-message-id`、DOM `id`。`messages.tsx` 仅增加 `data-message-id`；稳定执行身份、可选 `scrollAnchorId`、流式正文投影及预览／最终接续由 ISCP 分支负责。不增加模拟打字。

P2 将相同字号与表面规则用于邮件、文件、日程、通知及九项设置。原操作、权限检查、安全邮件 HTML、任务归属、草稿、日程及凭据继续使用原组件。

## 基线与检查

使用 Node 26.2.0 和 worktree 独立依赖。改动前 WebChat 59 个文件／319 项测试、Desktop 374 项、前端严格构建和隔离浏览器布局 24 组检查通过。

最终记录包括（117 篇项目 Markdown 源文档及其语言镜像／链接检查亦通过）：

- WebChat 64 个文件／333 项测试、Desktop 377 项、严格 TypeScript／Vite 构建通过；原本地工作台、邮件恢复、日程、语音、设置与消息仍有覆盖。
- 隔离 macOS Electron 视觉样例：18 类场景、两种字号、浅／深色、三种内容尺寸（1180 × 720、1440 × 900、1920 × 1080），以及最小尺寸下 125／150／200% 页面缩放。使用真实生产组件、脱敏固定数据并阻断网络。432 个组合全部通过，保留 88 张截图。100% 缩放时阅读／输入文字左缘偏差为 0px；所有缩放下最大为 0.2px。默认正文／输入为 16/28px，大号为 18/32px，CSS zoom 为 1。代表性文字／图标对比度最低 5.17:1；输入框边界浅色 3.57:1、深色 5.27:1。
- 真实 Electron 原生 view 的 48 组尺寸／字号／侧栏／缩放布局与可见性检查，以及地址栏就绪、空白标签复用、原生输入保留、弹窗遮挡、窄窗聚焦、键盘调整和拖拽收起。
- 滚动／输入测试覆盖锚点身份变化、异步会话恢复、选区、回到最新、IME 保护、30 行输入及上传失败重试。真实 Electron 布局运行通过异步会话位置恢复、选区保持、恢复跟随及 30 行输入；输入框保持 168px（六行、行高 28px），本地一次输入至两个动画帧反馈为 15.9ms。该测量不代表跨平台性能保证。
- `go build`、`go vet` 通过；未改 Gateway 源码。macOS 全量 Go 套件并非全绿：默认临时路径触发现有符号链接守卫，`TMPDIR=/private/tmp` 清除这些失败，Linux 内存工作区测试仍依赖 `/dev/shm`。另一个 readiness 测试的输出守卫拒绝 `private` 字样，因此也拒绝 `/private/tmp` 临时路径。单独记录这些结果，不混同前端验证或静默视为通过。

使用 Node 26 运行原生检查：

```sh
npm run test:webchat
npm run build:webchat
npm run test:desktop
npm --workspace @sparkclaw/desktop run test:browser-layout
node apps/desktop/test/experience-visual-run.mjs
node apps/desktop/test/run-experience-interaction.mjs
```

视觉运行器输出临时证据目录并保留截图及 `report.json`，删除独立 Electron profile。原生浏览器／交互运行器完成后删除独立 profile。不使用已安装客户端数据、真实数据库、邮箱或现网服务。

## 证据边界与集成

实际对齐、字号、溢出和代表性颜色对比检查补充截图走查，不等于完整无障碍审计。跟随系统主题沿用原偏好解析机制。真实系统中文输入法、屏幕阅读器，以及 Windows／Linux 字体和快捷键仍待目标环境验收。合成组件样例不能证明真实邮件投递、模型生成、权限恢复或后端性能；既有传输／恢复测试与真实服务验收分开记录。

协调聊天需将 App／LocalWorkbench 的布局 hook 与 ISCP 数据改动合并，使用集成后的稳定执行锚点重跑滚动／最终接续场景，同时保留 `data-message-id` 和 `data-scroll-anchor`。原生浏览器接口增加仅为内部显示控制；不改 Gateway、ISCP、JingSi 或 IMMS 公共契约。共享 InfiniCenter status 和协调记录由协调聊天更新。本分支未合并 main、推送、打包、安装或部署。
