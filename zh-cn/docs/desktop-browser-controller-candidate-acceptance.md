# Browser Controller 候选验收与接入手册 — 1f7f20ee

> 语言：简体中文 | [English](../../docs/desktop-browser-controller-candidate-acceptance.md)
>
> 更新日期：2026-11-10。产品源码：`1f7f20ee27f45efe5dbf15e43c612a4d0cff88d6`。
> 状态：工程候选。所有者已决定接受当前性能并接入新链路；P3 剩余项、D2 删除旧路径、
> 成对发布和最终签收尚未执行，生产未切换。
> 最终原生范围为 Mac ARM64、Linux ARM64，排除 Intel Mac。

本手册供所有者完成后续工作：先看状态和性能，再按“后续任务”顺序执行。
候选将 Controller 和 Playwright MCP 放入 SparkX，经已认证 ISCP 承载通用浏览器命令；
邮件、后端采集及个人脚本保持现有路径。设计依据、D1–D3 和完整证据边界见
[设计文档](desktop-embedded-browser-controller-iscp-design.md) §10–§15。

## 1. 当前状态

| 项目 | 状态 | 说明 |
|---|---|---|
| 源码接线 | 完成 | Gateway 无条件启用 `ClientControllerHost`；桌面主进程只装配 Controller 转发器 |
| 旧路径 | 运行时停用，代码未删 | `host-agent.mjs`、`native-page-commands.mjs`、`host-socket.mjs` 及 `browser.command` 注册仍在，待 D2 |
| R01 TEST1 原生链路 | 完成 | Mac ARM64 九组、Linux ARM64 八组通过，含断线恢复和零旁路 |
| R02 性能 | 已实测新链路，见 §2 | 所有者接受性能不足；旧路径未在公网实测，只有估算 |
| R03 工具语义 | 完成 | 13 个公开工具逐项有证据 |
| R04 Mac Dock | 鼠标关闭和 Dock 重开通过 | 所有者确认会话、标签和窗口尺寸保留，重开后仍是同一候选进程 |
| R05 宿主集成 | 候选验证在记录范围内通过 | Linux ARM64 实际 GNOME/X11 桌面和当前 deb 隔离安装、离线、卸载通过。Mac ARM64 独立候选已在宿主启动，另有仅限候选进程的离线证据 |
| R06 ISCP 偶发失败 | 已完成证据范围内的定位并补充源码诊断 | 新的提交失败定位到响应授权检查，早先的读取失败和底层原因仍未分类 |
| R07 D2 删除及成对切换 | 未开始 | 见 §6 |
| R08 所有者签收 | 待验收 | 见 §7 |

分支 `codex/client-browser-controller` 经所有者确认已推送到配置的仓库。§4 的产品制品
仍对应 `1f7f20ee`。

## 2. 性能实测

### 2.1 测量方式

2026-11-10 在本机用未改动的 Mac ARM64 候选包，经公网 TEST1 Relay 连接 work2 专用验收
Gateway 和真实模型，连续执行 12 个独立任务。每个任务新建会话，提交“在页面 Display name
字段填写 Alice”，脚本批准一次 `browser.type`，直到状态为已交付且页面确实填入。
12 个任务全部成功。计时从提交到已交付；脚本每 100 ms 轮询一次并附带一次核对调用。

### 2.2 结果

| 指标 | 数值 |
|---|---|
| 完整任务耗时，中位数 | 26.3 s |
| 最小 / 最大 | 25.5 s / 27.7 s |
| 首个任务（Controller 冷启动） | 27.7 s |
| 后续 11 个任务中位数 | 26.3 s |
| 应用启动到链路就绪 | 3.5 s |
| 每任务反向调用数 | 35（acquire 1、execute 31、release 1、status 2） |

冷启动只多约 1.4 s，任务间波动很小。

### 2.3 时间花在哪里

单个任务的中位数分解：

| 阶段 | 耗时 | 是否由新链路决定 |
|---|---|---|
| 提交到第一次浏览器调用（模型规划） | 8.9 s | 否 |
| 审批前的 23 次浏览器调用及模型间隔 | 约 8 s | 部分 |
| 等待审批出现并批准 | 约 3.9 s（批准调用本身 0.76 s） | 否 |
| 批准后的 12 次浏览器调用到已交付 | 5.4 s | 部分 |

35 次反向调用在客户端一侧的开销：

| 每次调用 | 中位数 | 每任务合计 |
|---|---|---|
| 请求到达后的 issuer 新鲜授权检查 | 66 ms | 两项合计 4.8 s |
| 回复发出前的 issuer 新鲜授权检查 | 70 ms | — |
| Controller/MCP/页面实际执行 | 14 ms | 1.0 s |
| 两次调用之间的间隔（公网往返 + Gateway 处理） | 170 ms | 约 4.3 s（不含三次长等待） |

结论：

- 浏览器真正执行只占约 1 s（4%）。
- 新链路的传输开销约 10 s（约 38%）：每次调用约 290 ms，乘以 35 次。
- 其余约 15 s 是模型规划、审批和交付，与走哪条链路无关。
- 最大的两个可优化点是往返次数（35 次）和每次调用两次的授权检查（4.8 s）。

### 2.4 与旧路径的差距

公网传输开销一行按新路径的单次调用成本估算旧路径。

| 对比 | 旧路径 | 新链路 | 依据 |
|---|---|---|---|
| 本地 mock 任务中位数 | 2.7 s | 4.3 s | 实测，各 10 次，`4615ad82` |
| 每任务反向调用 | 11 | 35–36 | 实测 |
| 公网传输开销 | 约 3 s | 约 10 s | 旧路径按相同单次开销估算 |

按此估算，同一表单任务在公网上新链路比旧路径慢约 7 s，即 26 s 对约 19 s。

### 2.5 未覆盖范围

- Linux ARM64 只有单次 26.1 s 的观测，没有多样本统计。
- 只测了表单填写一种任务；截图、大结果分块和多页任务未计时。
- 12 个样本的最大值不代表生产尾延迟。
- 旧路径的公网数值是估算。

### 2.6 重新测量

基准脚本和证据在私有缓存：
`/Users/dev/.cache/sparkclaw-controller-20261109/test1-native-v1/`。
脚本为 `mac-network-fixture-v1/bench-test1-workflow-v2.mjs`，证据归档为
`mac-bench-r02-evidence-v1.tar.gz`（SHA-256
`b631a6ef026fad7b8f8ba07b13f8209aaf81afa61067471a92e65a3724e1da60`）。

在脚本所在目录执行，证据名每次必须不同：

```bash
SPARKCLAW_CONTROLLER_ISCP_LAB=/private/tmp/sx-controller-iscp-test1-mac-arm64-20261110-v1 SPARKCLAW_CONTROLLER_CANDIDATE="/Users/dev/Applications/SparkX Controller Candidate 1f7f20ee.app/Contents/MacOS/SparkX" SPARKCLAW_CONTROLLER_NODE_MANAGED=1 SPARKCLAW_CONTROLLER_EVIDENCE_NAME=test1-bench-r02-v3 SPARKCLAW_BENCH_SAMPLES=12 node bench-test1-workflow-v2.mjs
```

结果写入 `<LAB>/evidence/<证据名>/bench.json`，逐调用延迟追踪在同目录 `traces/`。
每次运行会调用真实模型，并在验收 Gateway 留下对应数量的任务记录。

## 3. 已知问题

### 3.1 提交偶发失败：`Backend request is unavailable`

第一轮基准有三个任务交付，第四次提交发生此错误。第二轮 12 个任务全部交付。
第一轮追踪定位到了失败阶段：

1. `execution.submit` 请求正常发出，Gateway 受理并回复，加密响应已到达客户端。
2. 客户端 helper 对该响应做 issuer 新鲜授权检查，47 ms 后返回错误（正常为 62–73 ms）。
3. 客户端丢弃该响应，界面报 `Backend request is unavailable`。

影响：Gateway 已受理任务，客户端却认为提交失败，留下一条仅存在于 Gateway 的任务。
不要重新提交原请求。

当前候选的 `authorizeResponse` 追踪只为耗时 46.9 ms 的授权检查记录了 `error`，
底层原因待查。更早的 Mac 审批前读取和 Linux 恢复读取属于其他事件，保留原证据及原结果。

源码提交 `b7551a9a` 为按需开启的私有追踪补充了与原请求关联的错误类别。可区分节点
issuer 的 HTTP 状态与传输失败，保留授权检查失败即拒绝结果和按原请求核对的行为。
相关竞态测试和 vet 通过。§4 的候选制品早于这项诊断改动。

复现方法：运行 §2.6 的基准；`bench-test1-workflow-v1.mjs` 在第一次失败处停止。

### 3.2 其他需要知道的事

- **硬切换**：此分支没有新旧路径选择器。新 Gateway 配旧客户端时浏览器自动化不可用，
  Gateway 与客户端必须成对发布。
- **验收设备 profile 在临时目录**：Mac 已登记的验收 profile 位于
  `/private/tmp/sx-controller-iscp-test1-mac-arm64-20261110-v1/controller-native-profile`。
  系统清理临时目录后该登记会丢失，届时需要重新登记设备。
- **证据存放在本机私有目录**。非缓存交付目录
  `~/Library/Application Support/SparkClaw Acceptance/controller-1f7f20ee-2026-11-10/`
  保留了索引、索引所列文件、制品和性能归档共 444 份校验副本。
  `evidence-manifest.json` 记录原路径、副本路径和 SHA-256。副本与缓存位于同一块 Mac 磁盘。
  凭据和已登记档案留在原私有位置。
- **验收服务仍在运行**：TEST1 新增验收节点 `sx-controller-test1-work2-20261110-v1`、
  work2 独立 issuer 和 Gateway 保持运行，验收结束后需要决定是否停用。
- **Linux ARM64 主机原有 Gateway** 在候选活动前已处于重启循环，本工作未处理。
- **本机跑测试**：默认 `TMPDIR` 经过 `/var` 符号链接，会让 `browserhost` 和部分
  Controller 测试误报路径不安全；先执行 `export TMPDIR=$(cd "$TMPDIR" && pwd -P)/`。
  依赖 `/dev/shm` 的 `gateway` 包测试需在 Linux 运行。

## 4. 候选与证据入口

独立 Mac ARM64 候选：`/Users/dev/Applications/SparkX Controller Candidate 1f7f20ee.app`。
ASAR SHA-256 为 `f2deabcc9532e0f2ccc65a1f04903f06dd5e910f5bd0215418b78446041d1a8c`。
ASAR、Controller、原 UI 和预览资源与 `b175325b` 逐字节一致，只有 ISCP helper 更新。
生产 `/Applications/SparkX.app` 未替换。

四份当前归档位于
`/Users/dev/.cache/sparkclaw-controller-20261109/assembly-test1-clock-arm64/apps/desktop/dist/`，
摘要已于 2026-11-10 重新计算并核对一致：

| 制品 | SHA-256 |
|---|---|
| `SparkX-0.1.0-mac-arm64.dmg` | `b502cf6e836034a1ea477ec36aa9e9f7757b47c437dff67415e791eec9265ccb` |
| `SparkX-0.1.0-mac-arm64.zip` | `0afe858d840d8f1bb90a25b6f347924d23e2fc096caf8e461e51462a90a9f9c1` |
| `SparkX-0.1.0-linux-arm64.deb` | `0f8849172d1f84f2b5224572aa9798c16121c834b47cfba6a8d6f6d507b1c2b8` |
| `SparkX-0.1.0-linux-arm64.AppImage` | `94ba0c0aef8b774d6fa94048842ecbd032a9c84723831eb54302305e836c9a37` |

私有检查点索引为 `candidate-1f7f20ee-index-test1-v1.json`，父索引为
`candidate-b175325b-index-arm64-native-v1.json`，均在上述缓存根目录。此前实际 dpkg
安装/卸载证据严格属于 `b175325b`。

TEST1 验收结论：两类 ARM64 均通过真实模型审批/填表、原面板交互、仅限候选进程的 Relay
断线恢复和零旁路检查，每端覆盖全部 14 个必需 PID；Mac 另通过本地合成授权下的离线原生
操作。冻结检查点含 34 组匹配的完成回执，以及一条 Mac 审批前读取留下的
Gateway-only unknown。§3.1 的提交失败发生在此检查点之后。

Linux ARM64 当前候选从 deb 私有解包，在宿主现有 GNOME/X11 会话中使用独立 TEST1 档案
和私有密钥环，通过实际填表、原面板交互、Chromium 渲染隔离和正常退出等七项检查。
同一 deb 在私有根目录完成实际 `dpkg` 安装、离线运行和卸载，7,011 个条目核对一致，
关停档案保留。宿主安装、桌面入口及 AppArmor 基线均保留。交付目录内对应证据为
`linux-host-x11-v1/` 和 `linux-dpkg-1f7f20ee-v1/`。

## 5. 启动候选

候选使用自己的已登记 profile，经公网 Relay 连接专用验收 Gateway，不影响生产安装：

```bash
SPARKCLAW_DESKTOP_USER_DATA_DIR=/private/tmp/sx-controller-iscp-test1-mac-arm64-20261110-v1/controller-native-profile "/Users/dev/Applications/SparkX Controller Candidate 1f7f20ee.app/Contents/MacOS/SparkX"
```

基准和手动 Dock 验证使用同一个独立档案。自动化测试使用的
`https://controller.fixture.test/` 是脚本注入的页面，手动验收时不存在，需要自备一个
不涉及真实业务提交的测试页面。

## 6. 后续任务

前三项互不依赖。D2 在适用的 P3 检查和所有者体验验收通过后开始。成对升级和回滚先在
隔离环境演练，再进入发布。

1. **分支与证据。** 分支已推送，§3.2 记录了校验后的本机私有副本。
2. **体验验收。** 所有者已确认 Dock 检查（R04），在 §8 单独记录候选体验签收（R08）。
3. **偶发提交失败。** 保留授权失败即拒绝结果的行为和原请求。后续构建若复现，使用
   `b7551a9a` 增加的错误类别继续定位。
4. **D2 删除旧路径（R07）。** 范围以设计文档 §10 为准：
   - 删除：`browser.command` 注册及生成项（`operations.json` 两端）、`Broker` 反向分发、
     `apps/desktop/src/browser/{host-agent,native-page-commands,host-socket}.mjs`、
     `iscp-transport.mjs` 中对 `browser.command` 的接受、LAN host 的
     grant/connect/revoke/fences 路由及专属 grant/心跳。
   - 保留：broker scope/receipt/fence 服务、`HostJournal`、工作台 IPC 和核对 UI、
     后端 `BackendHost`、共享页面/隔离助手。
   - 先迁移仍被使用的助手，再删文件；活动 runtime 和构建引用清零即可，历史文档不必改。
5. **重新构建并演练成对升级。** 准备匹配的 Gateway、helper 和两端 ARM64 客户端。
   停止浏览器新准入，等活动任务结束，保留未决 fence 和原 ClientStore/profile/installation
   绑定，安装匹配的版本，验证能力后启用。
6. **验证回滚并记录发布授权。** 停止准入和子进程，恢复整套前版 Gateway、客户端、helper
   及兼容回执数据，再验证原连接和 UI。以已有安装身份及未决 fence 验证回滚。
7. **发布并移交证据后收尾。** 保存回滚记录和未决结果后，停用专用验收服务。
   核对独立副本后再回收可重建的缓存，保留索引和 unknown 回执。

## 7. 所有者体验验收

按 §5 启动候选后逐项检查。使用专门验收任务和不涉及真实业务提交的页面。

| 步骤 | 所有者操作 | 预期结果 |
|---|---|---|
| 1 | 打开原工作台、设置、历史、文件及浏览器入口 | 原菜单/布局/交互保留，已有数据和草稿存在 |
| 2 | 打开个人标签，调整面板宽度、切换会话，再隐藏/恢复面板 | 选择、尺寸和个人页面保留，没有新增常驻技术状态信息 |
| 3 | 用独立新任务读取准备好的页面并生成截图 | 正确任务页面及截图通过原结果/文件界面呈现 |
| 4 | 在准备好的表单执行填写、焦点输入、选择和提交，只批准预期动作 | 页面实际值符合任务，提交一次，个人标签不受影响 |
| 5 | 取消一个新任务，再执行独立任务 | 取消生效，先前不确定的写入不重放，数据保留 |
| 6 | Mac 上用鼠标关闭窗口，点击候选的真实 Dock 图标，再正常退出/重开 | 原工作台恢复，几何、所选会话及数据正确 |
| 7 | 重新打开原邮件及个人脚本入口 | 原入口和预期功能在已授权账号范围内保持可用 |

按 §2 的数据，一个表单任务约需 26 s，其中等待审批前约 20 s，属预期。
遇到 §3.1 的提交失败时记录时间，不重提原请求，另起新任务继续。
其他失败保留原 task/command 及证据，不清日志；修复后的新尝试另行记录。

## 8. 验收记录

检查日期为 2026-11-10。工程检查使用 §4 的制品摘要、随包 Electron 44.4.3 和
Node 24.21.0，在 Mac ARM64 独立候选及 Linux ARM64 GNOME/X11 宿主上执行。
联网检查经公网 Relay 和独立 TEST1 设备连接 work2 专用验收 Gateway 及真实模型。
离线检查使用本地合成授权。

| §7 步骤 | 实际记录 | 证据与范围 |
|---|---|---|
| 1 | 原工作台、历史、文件及浏览器入口可见；此前候选已核对原草稿和附件 | 当前 Mac Dock 会话、Linux 宿主工作台截图；早期缓存 UI 核对 `arm64-native-v1/mac-ui-cua-review-v1.json` |
| 2 | 原标签、收起重开、加宽恢复通过 | 两端当前 TEST1 fixture 和 `linux-host-x11-v1/` |
| 3 | 大 PNG 通过加密 ISCP 交付，原文件 IPC 可读，尺寸和摘要一致 | 当前 Linux GNOME/X11，`linux-host-experience-v1/screenshot/` |
| 4 | 真实模型审批和实际填入 Alice 通过。当前离线检查通过焦点输入及单次提交，选项选择有独立的工具证据 | §4 和设计 §15.2、§15.5，分别执行 |
| 5 | 取消撤销原审批，保留取消意图和既有 fence，后续独立任务成功 | 当前 Linux GNOME/X11，`linux-host-experience-v1/cancellation/` |
| 6 | 所有者鼠标关闭候选窗口，点击实际 Dock 图标，确认保留会话、标签和尺寸；重开后的同一进程已核对 | 所有者实际操作及原工作台状态。当前 fixture 另验证了正常退出和数据保留 |
| 7 | 原邮件入口可见。真实授权账号及个人脚本操作待所有者复核 | 邮件及个人脚本入口在通用 Controller 改动范围之外 |

| 项目 | 记录 |
|---|---|
| 已接受的性能 | Mac ARM64 表单任务中位数 26.3 s，公网差距约 7 s 为估算。所有者于 2026-11-10 接受 |
| 待查问题 | §3.1 响应授权失败。保留已有 unknown fence，源码已补充复现诊断 |
| 候选体验决定 | **待所有者签收（R08）** |
| 成对发布决定 | **待 D2 及成对升级与回滚证据（R07）** |

签收时记录所有者及接受的范围。生产发布单独决定。
