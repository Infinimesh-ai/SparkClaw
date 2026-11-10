# Browser Controller 候选验收指南 — b175325b

> 语言：简体中文 | [English](../../docs/desktop-browser-controller-candidate-acceptance.md)
>
> 准备日期：2026-11-10。当前为工程候选，所有者最终验收待完成。
> 产品源码：`b175325b9e4c2976398288089e310dc2ea28a012`。
> 本交接不关闭 P3，也不代表已授权切换生产。
> 2026-11-10 所有者范围更新：最终原生验收仅含 Mac ARM64 和 Linux ARM64。
> Intel Mac 明确排除，已有 x64 结果保留为历史证据。

候选将 Controller 和 Playwright MCP 放入 SparkX，经已认证 ISCP 承载通用浏览器命令。
验收继续使用原工作台和浏览器面板；邮件、后端采集及个人脚本保持现有路径。
[设计文档](desktop-embedded-browser-controller-iscp-design.md) 定义 D1–D3 和证据边界，
其中 §15 是固定的工程收口清单。

## 候选与证据入口

已安装的 Mac ARM64 候选：
`/Users/dev/Applications/SparkX Controller Candidate b175325b.app`。
其 `Contents/Resources/app.asar` 的 SHA-256 为
`f2deabcc9532e0f2ccc65a1f04903f06dd5e910f5bd0215418b78446041d1a8c`。
生产应用 `/Applications/SparkX.app` 未被替换。

八份归档均位于 `/Users/dev/.cache/sparkclaw-controller-20261109/` 下，目录代号对应：

- A：`assembly-enrollment-b175325b/apps/desktop/dist/`
- B：`assembly-mac-x64-b175325b/apps/desktop/dist/`
- C：`assembly-linux-arm64-b175325b/apps/desktop/dist/`

| 目录 | 制品 | SHA-256 |
|---|---|---|
| A | `SparkX-0.1.0-mac-arm64.dmg` | `416aca39a8566f61c1cf632cdeecdeb0db053f2240ba07bd773cae163069d19f` |
| A | `SparkX-0.1.0-mac-arm64.zip` | `286a01b7e182480d7676a1d8b7a5436dcfc30c5960eb2f7a3e1ed7897d1bafc0` |
| A | `SparkX-0.1.0-linux-amd64.deb` | `bec9fe88f7251b962472ff2c8168611dc0e97334c59f5244a87e6a7cc448abc4` |
| A | `SparkX-0.1.0-linux-x86_64.AppImage` | `6acc14114327e07f417bd29b807a5f5e72caa0c03d8c21ab8179934ef2b05219` |
| B | `SparkX-0.1.0-mac-x64.dmg` | `083090d7c1063e8948374a48046e53cc8b2d3d51563bf8cfb286c90a90e15ba7` |
| B | `SparkX-0.1.0-mac-x64.zip` | `821df2593be0be6f71b7f2b816a004e39cbeefb843b3a6a2b00fdbdec26a27c2` |
| C | `SparkX-0.1.0-linux-arm64.AppImage` | `06eab873edd5a744f2cc6698545ef14af23eba0b0e9e68ffc54a1b674a8380ba` |
| C | `SparkX-0.1.0-linux-arm64.deb` | `6febe43b75d284dbea86a1d51bd3411213e8c5c754d70b873667cec4fb77a6fa` |

同一缓存根目录下的冻结索引为 `candidate-b175325b-index-dpkg-native-v1.json`：
99,080 字节，SHA-256 为 `9028d24efe1f672a5b42e32e476cee52673e3b286af2b62b0a265d54f5c96b42`。
它记录 136 份证据、61 份冻结验证源文件和八份制品，验证源码/文档版本为 `eea088a8`。
索引路径指向本机私有证据，不是公开下载地址。
`node-lab-v1/candidate-owner-handoff-artifacts-b175325b-v1.json` 为本次交接独立复核八份归档
和已安装 Mac ASAR 的记录，不包含凭据。

| 平台 | 已验证范围 | 尚需完成 |
|---|---|---|
| Mac ARM64 | 独立安装候选；原生语义、路由及限定范围的加密 ISCP/生命周期证据 | TEST1、真实 Dock/原 UI、Mac 离线 GUI |
| Linux x64 | work2 原生证据；独立根目录真实 dpkg 安装/配置/离线 GUI/卸载，最终 7 项安装 + 8 项原生检查 | 历史覆盖，不再增加 x64 最终门槛 |
| Mac x64 | DMG/ZIP、架构/资源审计；ARM64 转译环境下运行随包 Node | 所有者已明确排除其最终验收 |
| Linux ARM64 | ARM64 独立根目录真实 dpkg 安装/离线公开工具/卸载，7 + 8 项通过；归档/资源审计 | TEST1 及宿主原 UI 集成 |

## 工程准备

以下由工程方完成。所有者提供缺少的访问条件/宿主，并做最终体验验收，无须运行故障夹具。

1. 恢复 TEST1 访问，记录部署/配置基线。在要求的拓扑完成 R01，再完成 R02 同拓扑新旧链路
   计时和传输统计。SSH 端口可达或 work2 实验成功均不关闭这些门槛。
2. Mac 已解锁，继续完成 R04；CUA 读取 Dock 超时，该项保留待验。按当前范围补齐 R05。保持 Chromium 沙箱；静态归档审计
   不记作原生通过。
3. 用显式指定的验收 profile 启动准确候选，核对安装、设备及连接后再呈现窗口。保留已有
   profile 的原授权。TEST1 使用正确入网的 TEST1 验收设备：不得改写隔离 work2 身份、
   复用已撤权设备或使用默认生产用户目录启动。保留历史、草稿、文件、cookie 和 unknown 写入保护记录。
4. R06 保持“证据不足”分类：原查询错误缺少底层原因/状态/helper 阶段。复核已有结论，
   原运行没有通过。若独立新运行复现，使用现有有界读取观察器并保留前后记录。
5. 适用 P3 门槛通过后完成 R07：按 D2 删除活动旧路径，并演练 Gateway/issuer/helper/客户端
   成对升级及回滚。请求另行授权的生产发布前，提供最终候选和证据。

初次交接时，TEST1 已知 SSH 入口拒绝本机密钥，CUA 报告 Mac 锁屏。所有者现已提供 Linux
ARM64 宿主访问，限定范围的原生安装/离线验收见设计 §15.4。Mac 已解锁，实际 Dock 交互因
CUA 读取超时仍待验收。未将 work2 密码用于
TEST1。隔离实验活动已停止，单独打开应用不会恢复实验服务，也不代表已连接 TEST1。
远端保留 b175 AppImage 归档，其解压树已回收；再次解压必须满足已有 4 GiB 磁盘预留。
此前共享磁盘耗尽曾中断生产 PostgreSQL；恢复及变化后的运行基线继续保留于设计和证据索引。

## 所有者体验验收

工程方启动准备好的候选并说明可验收的平台/拓扑后，按下表检查。使用专门验收任务和不涉及
真实业务提交的夹具。每项结果关联平台、候选摘要、设备及证据位置。

| 步骤 | 所有者操作 | 预期结果 |
|---|---|---|
| 1 | 打开原工作台、设置、历史、文件及浏览器入口 | 原菜单/布局/交互保留，已有数据和草稿存在 |
| 2 | 打开个人标签，调整面板宽度、切换会话，再隐藏/恢复面板 | 选择、尺寸和个人页面保留，没有新增常驻技术状态信息 |
| 3 | 用独立新任务读取准备好的页面并生成截图 | 正确任务页面及截图通过原结果/文件界面呈现 |
| 4 | 在准备好的表单执行填写、焦点输入、选择和提交，只批准预期动作 | 页面实际值符合任务，提交一次，个人标签不受影响 |
| 5 | 取消一个新任务，再执行独立任务；观察另行准备的断连/恢复场景 | 取消生效，先前不确定的写入不重放，数据保留 |
| 6 | Mac 上用鼠标关闭窗口，点击候选的真实 Dock 图标，再正常退出/重开 | 原工作台恢复，几何、所选会话及数据正确 |
| 7 | 重新打开原邮件及个人脚本入口 | 原入口和预期功能在已授权账号范围内保持可用 |

失败时保留原 task/command 及证据，不清日志、不重提不确定写入以取得通过结果。
修复后的独立新尝试另行记录。

## 验收记录

记录验收人/日期、源码/制品摘要、系统/架构、设备/拓扑、各步骤结果及证据、接受的限制、
未解决缺陷、最终决定。目前最终决定为 **待验收**。限定范围的 UI 验收不自动覆盖未测试平台，
也不代表发布授权。R08 交接材料已准备，所有者最终签收仍未完成。
