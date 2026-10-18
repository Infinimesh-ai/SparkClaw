# 桌面体验与 ISCP 流式输出联合验收

语言：简体中文 | [English](../../docs/desktop-stream-integration-validation.md)

日期：2026-10-18。用户授权集成并在验收后合并本地 `main`，暂不推送或部署。本报告补齐[桌面验收](desktop-experience-validation.md)及[流式实施](iscp-application-stream-implementation.md)中待完成的集成门。新增运行均使用一次性身份、配置和 PostgreSQL 测试数据。已安装客户端与现网服务未变更。

## 集成源码

共同中英文设计基线为 `cd7d5ccc`。隔离集成分支先合并桌面 `2e5e96cb`，再合并流式 `071684d4`，形成 `63559c91`。共享消息冲突处理保留真实持久消息的 `data-message-id` 和稳定 `execution:<request_id>` 滚动锚点。沿用原工作台、浏览器、菜单和输入框；保存后的正文读取保留流式读取版本守卫。

交叉审查在验收前发现并修复三类行为：

- 文件已持久保存但随后读取失败时，不再提供重复上传；欢迎页首次上传失败绑定实际新会话，切换会话后仍保留原文件与草稿。桌面提交 `74103b2f` 集成为 `4c7c526f`，保存后读取使用 `readContent(id).catch(surfaceError)`。
- 选中的原生文本节点继续增长时，即使 article 和节点均未更换，选区仍会塌缩。`RenderedMessageText` 现在于 DOM 修改前记录原生选区，仅在所选已渲染文字前缀完全相同时恢复。保留选区方向；正文替换后不恢复旧文字，不干扰其他回答的选区。
- 行首或段首的选区端点须保留准确 DOM 边界，不能只恢复到文字偏移相等的上一节点末尾。补修优先复用仍指向相同文字的原端点。五项回归覆盖正反向、行内多节点、另一回答、正文替换、行首和段首。

最终产品及验收脚本提交为 `6608a8fe`。合并提交 `ef0dbfa5` 纳入已审查的桌面后续提交，Git 树逐字节一致。后续提交仅记录报告与证据。合并版本 Go 全套在 `63559c91` 采集后，Gateway 源码没有再修改。

## 联合验收

[脱敏回执](../../docs/evidence/desktop-stream-integration-2026-10-18.json)记录准确源码版本、构建和模块哈希、日志与原始回执指纹、计数及时间。最终原生运行均从干净的已跟踪源码开始，使用生产自动滚动，脚本辅助滚动次数为零。原 `--long-text` 辅助滚动模式仅保留作历史对照，不计入本次通过结论。

| 检查 | 结果 |
| --- | --- |
| WebChat / Desktop | 351 / 427 项测试通过；桌面无跳过 |
| Linux Gateway | 禁用测试结果缓存，73 个测试包通过；11 个包无测试；build 与 vet 通过 |
| 前端 | 严格 TypeScript 与生产构建通过 |
| 原生浏览器面板 | 48 组窗口/字号/侧栏/缩放，以及原生视图边界、弹窗遮挡、保留页面、焦点布局、键盘分隔条通过 |
| 原生视觉矩阵 | 432 组、保留 88 张截图，无布局断言失败；人工检查浅色大字及深色大字代表截图 |
| 原有滚动与输入交互 | 异步会话阅读位置恢复、选区、回到最新、31 行输入框通过；高度 168 px，输入至两帧反馈 12 ms |
| 20 次受控原生流式 | 2,000 批、20 次持久交付；保守上界 P95 109.18 ms、P99 116.39 ms；无空白、缺失、重复或重挂载消息行 |
| 6,000 字自动跟随 | 源、持久提交、渲染正文摘要一致；无脚本辅助滚动时末尾保持可见，最终交付仍为同一消息行 |
| 两次 6,000 字阅读 | 主动上滚与文字选区在增长期间保持稳定；其中一次保持到最终交付；原“回到最新”恢复跟随 |
| Helper 中断 | 原请求 1326.90 ms 恢复；一次 submit、两次订阅、一次持久最终 ACK；授权重新绑定时按要求隐藏预览一次 |

健康受控生产源每 100 ms 发一批，共 100 批，经过 PostgreSQL 18、连续加密 Relay/Endpoint、真实 helper 私有管道、Electron main/ClientStore 和原渲染器。不使用直接业务 HTTP 或进度轮询。连续样本和长回答都只执行一次，并对持久最终结果确认。计时使用 DOM 可见性加下一动画帧的合成绘制代理，包含跨进程时钟误差保守界限。阅读用例主动使末尾离开视口，其时间字段仅证明渲染，不能表述为可见延迟。

保留了原生选区失败记录，以及随后一次脚本失败：已排队的观察在主动上滚前记录了“跟随”阶段。脚本改为在实际测量帧读取阶段，没有放宽产品阈值。Linux 准备脚本的 `--user` 在镜像已有 Python 虚拟环境中被拒绝；核验同一镜像全部所需 Node/Python 文档模块后，全套通过。此前四项 macOS 全量 Go 失败仍为基线平台限制，不计作通过。

## 复跑与边界

使用 Node 26、npm 11、Go 1.25 和独立 worktree 依赖。原生命令会创建真实 Electron 窗口并重新构建 UI，应串行运行：

```sh
npm run test:webchat
npm run test:desktop
npm run build:desktop-ui
node apps/desktop/test/run-browser-panel-layout.mjs
node apps/desktop/test/run-experience-interaction.mjs
node apps/desktop/test/experience-visual-run.mjs
node apps/desktop/test/run-stream-native-qualification.mjs 20
node apps/desktop/test/run-stream-native-qualification.mjs 1 --auto-follow
node apps/desktop/test/run-stream-native-qualification.mjs 2 --joint-ui
node apps/desktop/test/run-stream-native-qualification.mjs 1 --recovery
```

Linux 使用现有测试镜像 `sparkclaw-public-iscp-linux-suite:s1lz19o5`（摘要 `865ab6631f4b8994769d43966141a539b9d1ca1a77bf60b9a639b4324bc946e0`），只在一次性容器内替换源码并保留其 Linux 依赖。完整命令为 `go test -count=1 ./services/gateway/...`，随后执行 build 与 vet。没有部署服务。

原生结果覆盖 macOS Apple Silicon。真实系统输入法、屏幕阅读器、其他平台字体及 x64 原生验证不在本次通过范围内。此前生产 Agent 的真实模型结果保留在流式报告中，本次集成没有重复请求该模型或修改模型环境。未来发布仍需 ClientStore schema 11 和匹配的 Gateway/Relay/helper/Electron 能力。ISCP 上游扩展接纳仍为 proposed；既有公共契约和 SDK pin 不变。
