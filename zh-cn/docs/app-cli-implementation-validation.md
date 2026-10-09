# App-CLI 抽离：实现与验收

[English](../../docs/app-cli-implementation-validation.md) · [设计](app-cli-email-extraction-design.md)

实现日期：2026-09-29。SparkClaw 分支为 `codex/extract-email-app-cli`，维护 fork 分支为 [`codex/sparkclaw-email`](https://github.com/ZZZZJJJ0928/App-CLI/tree/codex/sparkclaw-email)。源 commit 与完整产物摘要记录在 [configs/app-cli-release.json](../../configs/app-cli-release.json)。

## 已交付行为

QQ Mail、Gmail、Outlook 的应用脚本、Reader 源码/构建、通知语义、发送 journal、Schema、Manifest 和 binding 已由 App-CLI 维护。SparkClaw 仅消费经校验的发行归档及生成的 Go/Reader/Desktop 投影，不再保留独立可编辑的邮件实现；旧 `scripts/email`、Controller 供应商执行分支和应用池已删除，上层工作流、存储与 UI 继续消费原业务回执。

Gateway → 已鉴权的 owner Controller → App-CLI Python 公共 Registry → RuntimeAdapter 2.0 → 常驻 Executor → 注册应用 → 签名 HostPort → 原任务专属浏览器页面。通用 Controller/Bridge/Desktop、页面所有权及个人页保护保留在 SparkClaw。新增使用已有 Host 能力的非邮件应用只需扩展 App-CLI 注册/binding，无需修改通用调度器。

Executor 将请求键与主体/owner/意图持久绑定；丢包查原 task，无法确认受理时不得重发。执行授权过期不撤销合法查询/取消权限。恢复依赖原 journal、效果围栏和递增 epoch。read/watch 各持活动租约，实际 daemon 到期、清理失败隔离和有界 park 在控制进程丢失后仍生效。应用发行校验失败只禁用应用准入，普通浏览器仍可启动。

## 初始抽离阶段验证

环境：Linux ARM64、Python 3.12、Node 26.2.0。实际隔离浏览器：Electron 44.4.3、Chromium 152.0.7977.130、内置 Node 24.21.0。

| 验证 | 结果 |
| --- | --- |
| App-CLI Python | 86 项；保留原核心/v1 和原生生命周期夹具；仓库外 wheel 安装复跑同一套测试 |
| App-CLI 运行时/邮件 | 275 通过，无跳过；真实常驻子进程、锁/重启/丢包、Schema、授权、恢复、Host 租约及迁入邮件用例 |
| SparkClaw Controller | 启用隔离 Chromium 下载后 122 通过，无跳过 |
| Desktop / Bridge | 17 / 67 通过 |
| Go Gateway | 全量 `go test ./services/gateway/...` 和 `go vet ./services/gateway/...` 通过 |
| WebChat | 43 文件 / 169 测试通过；生产构建通过，保留既有产物体积提示 |
| 浏览器组件脚本 | 14 通过，1 项依赖外部 Tampermonkey background 的可选测试跳过 |
| 成套发行 | 仓库外干净安装、实际文件篡改/混用 Python-runtime/消费者投影拒绝、运行中服务禁止切换 |
| 兼容整组回退 | 两个不同制品摘要；恢复原消费者+wheel+runtime，原状态保留，epoch 严格递增 |
| 实际 Electron | 安装后的 Python 公共准入 → 常驻 Executor → 签名 HostPort → 真实非邮件任务页；原 task 防重与页面/进程清理通过 |
| 普通浏览器回归 | MCP/CLI 导航、读写点击、截图下载、弹窗、worker/OOPIF、个人页及输入隔离、renderer/main 故障恢复、会话 cookie 持久化 |
| 网关容器 | 实际 Docker 镜像构建及隔离容器启动入口检查通过，未替换宿主运行服务 |
| 生成/公开产物 | Schema/投影一致、受管 preload 校验、源码卫生及双语 Markdown 链接检查 |

中间一次 Electron 下载验证失败，原因是重装依赖时禁用了生命周期脚本，遗漏既有 Playwright 下载补丁。执行 `install-playwright-downloads.mjs` 的正常安装步骤后，完整用例通过；安装器与 CI 显式执行该步骤，未放宽超时来通过检查。

## 三邮箱实测续验：2026-09-29

在现有专用浏览器持久 profile 上临时启用匹配的 `0.3.0-sparkclaw.2` Host/Executor，执行用户授权的测试邮件。六个方向均取得唯一标记邮件的接收端原件，校验了 manifest、文件完整性、解码后精确 Subject 和 From/To 路由。Outlook 的实际发信地址从已接收原件中取得，未直接将登录别名当作已验证收信地址。

| 方向 | 接收端原件 | 原生发送确认 |
| --- | --- | --- |
| Gmail → QQ Mail | 已验证 | 已确认 |
| Gmail → Outlook | 已验证 | 已确认 |
| QQ Mail → Gmail | 已验证 | 复验后仍未知，另有原件证据 |
| QQ Mail → Outlook | 已验证 | 未知，另有原件证据 |
| Outlook → QQ Mail | 已验证 | 未知，另有原件证据 |
| Outlook → Gmail | 已验证 | 已确认 |

这是多个候选构建的累计实测证据。最终 Outlook → Gmail 及 QQ Mail → Gmail 确认复验使用固定的最终发行，其他方向尚未全部在同一摘要下重跑。不重放结果未知的任务，也不将其账本静默改为完成；原 journal 与账本状态保留，接收端原件证据单独记录。

实测发现的问题已落实为以下修复：

- `setSecrets` 后重新加载任务专属浏览器 daemon 的私密表单值，确保后注入的引号、多行正文正确进入原生编辑器。
- 托管发送使用已鉴权 Reader 邮箱身份及必要的 snapshot 时间范围，避免混用账户菜单身份或登录别名。
- 保留 QQ 原生可访问的收件人输入框，区分 Bcc 开关与编辑器；Outlook 昵称收件人使用有界的原生已提交模型核验，拒绝矛盾、未解析和多余收件人。
- 旧发送与托管发送共用 QQ/Outlook 已发送文件夹证据，排除隐藏缓存行，证据不足继续返回不确定；watch 状态查询消费待处理事件，不隐式续期授权。

续验通过：App-CLI Python **86** 项、运行时/邮件 **283** 项（无跳过）、Controller **123** 项（含实际 Chromium 下载）、资格验证脚本 **8** 项、Go emailautomation/browsercontrol 包，以及生成投影、受管 preload、干净成套安装/篡改/混版拒绝/整组回退。完整隔离 Electron 验证还覆盖普通浏览器操作、非邮件公共 Registry 调用、textarea/contenteditable 的后注入私密值、页面隔离、清理及 renderer/main 进程恢复。

本轮 receipt-only 实测不代表生产通知延迟、多收件人、回复、附件及全部账户切换路径均已验收。[脱敏实测证据](../../docs/evaluation/app-cli-live-mail-20260929.json)记录各次尝试、标记、任务结果和私有日志摘要，不包含邮箱地址、凭据或原始邮件。下面的初始阶段证据保留为历史记录。[本次新七任务 App-CLI CI](https://github.com/ZZZZJJJ0928/App-CLI/actions/runs/36562915676)在 `6a46352`（发行源码 `99a59e9`）全部通过，覆盖 Windows/macOS/Linux 的 Python 3.11/3.13 和生命周期运行时。QQ 原生发送确认在最终复验后仍未通过；本轮实际发出的七封测试邮件均有独立校验的接收端原件。

## 合并主分支后的收尾清单：2026-09-30

交接时状态：以下清单原定合并后执行；用户现已授权执行。主分支合并完成，当前收尾实测结果见下节。合并代码不代表生产验收通过。

交接基线为 SparkClaw `cf28f87`、App-CLI fork `6a46352`，当前消费发行 `0.3.0-sparkclaw.2` 的源码为 `99a59e9`。已有六向累计送达、七封原件证据及四条历史不确定记录，均保留在上节链接的脱敏报告；不得将其改写成最终发行全量通过。App-CLI 继续在维护 fork 管理，无需以合并回上游作为本次收尾前提。

| 顺序 | 合并后工作 | 完成标准 |
| --- | --- | --- |
| 1 | 核对主分支合并结果与发布范围 | 记录主分支落点、App-CLI 源提交、成套制品摘要和准备开放的功能；检查合并冲突是否影响消费者、投影及宿主，运行对应回归 |
| 2 | 修复 QQ 发送确认，完善未知结果处理 | 在 App-CLI 定位原生已发送证据不足的原因；正常发送取得可靠确认；故障时 SparkClaw 正确展示未知和对账结果，不误报失败或自动重发；保留四条历史记录及独立送达证据 |
| 3 | 构建并冻结最终发行，验证完整产品链路 | 修复后生成新的匹配 wheel/runtime/消费者投影，记录摘要并通过发行检查；匹配 Gateway、Controller、Executor、Desktop 从产品界面走通；普通浏览器任务页、个人页面和登录态保持正常 |
| 4 | 同一最终制品完成六向互发复验 | QQ↔Gmail、QQ↔Outlook、Gmail↔Outlook 均核对发送状态、接收端原件、精确主题和地址；每个独立用例使用新标记，旧不确定任务不重放；不能用接收成功替代发送确认的验收 |
| 5 | 验证真实读取及准备开放的发送模式 | 三邮箱冷启动、连续复用、账户切换/登录失效正确；按实际开放范围验证多收件人、抄送、回复/回复全部与原件下载；未通过的模式不得标为已验收，附件发送不因本清单而扩充实现范围 |
| 6 | 收信通知与持续运行联验 | 真实新邮件经过监听、采集、去重、入库到 UI；覆盖 read/watch 两种启动顺序、多个授权续期周期、断线后的 gap 补查；记录通知延迟、页面/进程数量和资源占用趋势 |
| 7 | 目标部署恢复与兼容整组回退演练 | 验证 Gateway/Executor/Controller/浏览器重启、断连、取消、授权到期和凭据变化；不重复发送、不越权继续执行，清理失败正确隔离；兼容发行整组回退保留账本、journal、授权记录、原件和登录态 |
| 8 | 汇总最终验收证据，交用户验收 | 每项记录最终制品摘要、结果及剩余限制；双语文档与脱敏报告一致；用户完成最后验收后再确定生产启用范围 |

执行顺序为 1→2→3，再在同一最终制品上完成 4–7，最后执行 8。若修改了相关实现，重跑受影响用例并更新证据，不能沿用旧摘要的通过结论。供应商业务修复继续归 App-CLI；SparkClaw 仅处理通用 Host、上层调用和产品状态。若需改变已冻结跨项目契约，仍须先走 InfiniCenter 决策流程。

2026-09-29 结束临时验证时已恢复原运行服务、停用临时 Executor 并保留持久状态；这不是把新账本交给抽离前旧版的兼容回退。后续恢复工作从既有记录继续，不清空状态、不重放旧邮件。当前清单不安排后台监控或自动继续执行。

## 合并后收尾实测：2026-09-30

**有界验收仍有未通过门槛，尚未接受生产启用。** 工作区先提交再合并，主分支合并落点 `6a6f665`；原 SparkClaw worktree 及已合并分支已清理，私有状态另行备份。最终候选为 `0.3.0-sparkclaw.11`，App-CLI 源码 `9da7cfd`，冻结提交 `c08d396`，运行时摘要 `748eb24a2a9646e274d777680f9ff54ac0b77bf6b58a8e191fddd2efd0996000`；消费端复验提交 `41caf5a` 包含 Outlook 精度修复 `44e0dac`。[本次脱敏收尾证据](../../docs/evaluation/app-cli-closeout-20260930.json)保留各次尝试、请求摘要、结果、制品固定值及剩余门槛。只使用现有三个账户；用户要求账户切换待验，附件发送不扩充范围。

App-CLI 修复共享页并发初始化、watch/有限读取执行通道、空闲清理及失败的部分采集保留监听、兼容 binding 后访问原任务、仅 journal 对账、续期响应、Gmail 最小化编辑器恢复和首次活动租约窗口。SparkClaw 修复通用资源预约、产品/缓存订阅身份及草稿的嵌套 Nginx 代理。Outlook 重试时间按 PostgreSQL 微秒存储精度比较，其他身份字段和定位信息保持精确校验；只有显式原件恢复才绑定已保存目标，其余保留新发现的候选。独立登录探针使用新 invocation，避免与先前不可变授权冲突。冻结契约及授权时间窗均不变。

三邮箱冷启动/连续复用、read-first/watch-first 六种组合及显式取消通过（**196.06 秒，无发送**）。Python **86**、运行时 **299**、Controller **127**、Go build/vet/完整测试、投影/preload 一致性、成套安装和拒绝篡改/混版/运行中激活通过。[App-CLI CI](https://github.com/ZZZZJJJ0928/App-CLI/actions/runs/36683544137)全绿。隔离 Electron 覆盖普通 MCP/CLI、公共 App-CLI、个人页/输入隔离、Cookie、下载及 renderer/main 恢复；两次早期 MCP 超时失败保留。

下表在消费端修复后使用同一最终制品，每条独立路线只发送一次，标记各不相同。中间发行及超时尝试不因后来送达而改判通过。

| 路线 | 原生发送 | Gateway 原件及精确 From/To | 结果 |
| --- | --- | --- | --- |
| Gmail → Outlook | 已确认 | 未通过 | 失败 |
| Gmail → QQ Mail | 已确认 | 已核对 | 通过 |
| Outlook → Gmail | 已确认 | 已核对 | 通过 |
| Outlook → QQ Mail | 已确认 | 已核对 | 通过 |
| QQ Mail → Gmail | 已确认 | 已核对 | 通过 |
| QQ Mail → Outlook | 已确认 | 未通过 | 失败 |

新 Outlook 收件原件返回 `email_network_original_unqualified`，因此即使服务商确认发送，接收验收仍失败。此前两次 Gmail→Outlook 原件在微秒修复后已发布并解析，但原先超时结论保留，两条任务均未重发。恢复原 Gateway/Controller 并重启浏览器后，两条新超时路线和多To/Cc邮件的Outlook原件也已发布并通过精确头核对；这是旧服务恢复后的独立补采证据，不改变最终候选的失败结论。部分采集失败保留活动监听，不会将原件失败改成通过。发送开始至原件发布包含原生操作，不是纯通知延迟。

产品已用现有 token 配对。真实 UI 新建邮件含两名收件人和一名抄送者，获服务商确认；QQ 原件的 Subject/From/To/Cc 及文件摘要均准确，最终候选测试期间的 Outlook 接收未接受，后续补采另记在恢复证据中。真实回复草稿保存成功，发送在效果边界前被 `EMAIL_REPLY_TARGET_UNVERIFIED` 阻止，未重试原任务；Gmail 网络定位的目标尚未证实已在原生回复界面打开。回复全部会排除每个已知自有邮箱，因此现有三个自有账户没有有意义的正向收件对象；回复/回复全部仍不属于已接受模式。最终候选点击下载后，认证文件接口 HTTP 200 且与持久原件逐字节一致，但浏览器未暴露实际保存文件；此前实际保存的 `.8` 邮件原件仅保留为历史证据。

消费端修复后的最终整组运行 1823.94 秒，记录 181 个样本：任务页 3–4 个，残留页为 0，任务窗口未取得焦点。整个专用浏览器 Profile 与 CLI daemon 的 RSS 为 7519–8200 MiB，包含已有个人标签页，不能当作邮箱组件独占内存。任务身份与续期记录见脱敏证据；有界页面/续期观察通过，原件采集失败仍是验收门槛。

目标部署实际完成最终摘要→另一组仅 NOTICE 不同的兼容制品→最终摘要，成套切换 Gateway、消费者、wheel/runtime 及投影。epoch **56→90→91**，全部 **39** 条既有发送记录和 **2357** 份不可变授权/journal/原件保持不变；夹具与返回后的三账户真实登录探针均通过。初次切换的清理围栏正确拒绝应用准入；重启浏览器并确认旧任务页为零后，只归档并移除 Host 围栏。这证明代码与 ledger v1 相同的兼容夹具回退，不证明任意旧版或抽离前代码可消费新账本。

真实 watch 授权到期进入 `waiting_confirmation / AUTHORIZATION_EXPIRED` 后显式取消；错误浏览器凭据被拒绝。这不等于真实服务商登录到期或实际凭据轮换。实际 Gateway 在发送效果边界后被终止，只留下一个 uncertain 任务，UI 禁用发送并显示“发送结果待确认”；公共对账后原请求/任务仍不确定，新增发送准入为零。两接收端原件独立核对成功，也不能代替缺失的原生发送确认。四条历史不确定任务经公共 lookup/仅 journal 对账保持请求摘要，不使用当前浏览器凭据、不重放。

已恢复并检查原 Gateway/WebChat 容器与归档的抽离前 Controller 实现；原 WebChat 容器的模板和活动 Nginx 配置保留已验证的一行草稿代理修复，镜像/容器身份不变。配对与未知草稿在实际 UI 中仍可见，发送保持禁用；候选 Executor 已停止且 disabled，账本、授权、高水位、journal 和原件均保留。恢复原环境没有把新 Executor 账本交给旧代码消费。

清单 1–3 已覆盖；4–7 的失败/待验门槛保留如上。有界实测和恢复结果落盘后证据汇总完成，清单 8 仍等待用户最终验收并确定生产范围。真实登录失效、凭据轮换、账户切换、最终浏览器下载落盘及未通过发送模式不得标为已接受；历史 Gmail coverage gap 与当前处理警告保持可见。

## Host 并发退租修复：2026-10-09

本地成套发行 `0.3.0-sparkclaw.14` 修复 watch/read 同时退租。此前两个租约会在清理继续执行前都被标为 retired，但仍按集合条目计数，发布无活动、无空闲授权的空租约。daemon watchdog 因而可能先于 Host 显式清理关闭页面，随后正确留下无法证明清理成功的围栏。

现在退租仍立即 abort 对应租约，并串行处理同一资源的清理决策。只有未 retired 的活动可维持页面；最后一个活动直接清理，后续退租不能为已销毁资源重新发布租约。显式停放仍有期限。真实到期、epoch 撤销仍会关闭页面，未知清理失败仍保留围栏，包括 `page_closed`；本修复不将该错误视为已经证明清理成功。

确定性夹具组合真实 Host port、原子租约文件写入和 daemon watchdog，在每次发布后检查，不依赖 sleep。两个并发顺序均在 `.13` 复现失败、在 `.14` 通过。新增 **8** 项覆盖并发退租、活动 watch、停放到期、立即 abort、epoch 撤销及清理围栏，全部通过。隔离 Linux Controller **157** 项通过、**8** 项真实 Chromium 用例跳过；macOS **140** 项通过、**25** 项平台/浏览器用例跳过。临时状态内完成成套安装、篡改/混版拒绝、兼容整组回退、持久状态保留及 epoch 单调递增验证。这里不声明已部署服务或真实供应商业务通过验收。

使用 Node 26 及已安装 Controller 依赖，执行 `python3 scripts/build-lease-retirement-release.py --check`，可从保留的 `.13` 制品逐字节重建。runtime 仅修改 `src/host-port.mjs`、包版本及发行元数据；wheel 仅更新版本和绑定的 runtime 摘要。**14** 个 Reader 源码/资产文件逐字节相同，版本不变；浏览器组件 policy、Go 供应商投影及 Desktop 托管 Reader 投影无变化，投影检查通过。`.14` runtime 清单摘要为 `ae1786b8140cb456ed8183ba7b275a3611be67c5d4ba7c3c4214b6967f642fea`；制品摘要及可重建本地补丁链记录于消费发行清单，既有 `.13` 制品保持不变。独立的 watch 错误码规范化问题不在本补丁范围。

## Controller 停止失败后的清理：2026-10-09

work2 停止日志显示 Executor 于 UTC 09:14:26 退出，Controller 随后达到 systemd 60 秒停止期限并被 SIGKILL。日志没有关闭阶段跟踪，不能确定线上唯一阻塞位置。隔离复现确认了与现象相符的缺陷：Executor 退出后取消 watch 失败，使 `MailObserverFeed.close` 拒绝，跳过 factory/Host 清理，私有事件监听器仍保持引用。公共 HTTP 已关闭，停止调用在 1 毫秒内报错，但进程无法自然退出；60 秒是外部强杀期限，不是已成功完成的有界排空。

现在 feed 关闭失败后，Controller 仍尝试 factory 和普通 reservation 清理。Host 拥有的应用 reservation 由 factory 完成资源清理；失败时保持围栏，不虚假完成业务任务。driver 等待所有拥有的 handle 清理结束，并在 `finally` 关闭私有事件连接/监听器，保留原错误与持久清理围栏。App-CLI `.15`、Reader 资产、授权边界及服务停止期限均不变。

新增 **6** 项真实类组合回归包含两个 Unix 监听器、未完成请求体的真实 HTTP 请求、普通 MCP reservation、Executor 停止后的取消失败、正常关闭、页面/进程回收失败及多 handle 清理。macOS **152** 项通过、**25** 项平台/浏览器跳过；隔离 Linux **169** 项通过、**8** 项真实 Chromium 跳过。本次检查未修改运行服务，新代码在目标主机的优雅停止仍需协调验证。详见[脱敏证据与推断边界](../../docs/evidence/browser-controller-shutdown-2026-10-09.json)。

## 构建与消费发行

先在 App-CLI fork 提交审核后的源码，然后构建：

```bash
.venv/bin/python release/build.py
```

将 `dist/release` 的 **四个文件** 整组复制到 SparkClaw 的 `vendor/app-cli`：wheel、runtime tgz、`python-requirements.txt` 和 `release.json`。同一清单复制到 `configs/app-cli-release.json`。版本/文件名变化时更新 `tools/browser-controller/package.json` 的精确本地依赖并重新生成 npm 锁文件。当前版本同步投影及归档完整性：

```bash
npm run sync:app-cli-projections
npm ci --prefix tools/browser-controller
npm run build:desktop-managed-scripts
npm run check:app-cli-projections
npm run check:desktop-managed-scripts
npm run qualify:app-cli-release
```

制品 vendored 后不需要相邻 App-CLI checkout。wheel 绑定实际 runtime 清单摘要，服务和 Controller 实际安装的客户端包均在准入前校验，签名 Host 握手再次核对 binding/发行摘要。页内 `SparkClawMailReader` ABI 保持兼容，但实现归 App-CLI。迁入 Apache-2.0 源码保留出处，上游核心保留 MIT。归档不含凭据、邮件原文或私有验收数据。

## 安装、激活与回退

原安装入口现会准备匹配发行、按 Executor→Host 顺序排空、安装固定消费者，并启动 owner 服务：

```bash
npm run setup:browser-controller
npm run check:browser-controller
```

须由桌面 owner 在既有浏览器配置及 `.env.local` 环境执行。新增 `sparkclaw-app-cli-executor.service` 依附 Controller。私有发行位于 `$XDG_DATA_HOME/sparkclaw/app-cli`（默认 `~/.local/share/sparkclaw/app-cli`）；持久 ledger、Host epoch、签名授权和索引在独立 `state/`，不随 `releases/<digest>/` 替换。容器继续使用已鉴权 Controller socket，无需执行任意宿主路径或另装 Python。

只准备/检查、不改运行服务时，可显式给 `scripts/install-app-cli.py` 指定 `--root`、`--host-socket`、`--host-runtime-root`、`--workspace-root`。`--check` 校验安装文件；`--activate` 拒绝仍可连通的 Controller 或仍被占用的 Executor 锁。激活原子写入 `previous.json`、`current.json`。`qualify-app-cli-release.py` 在临时目录完成真实安装、故障探测与整组回退，不连接账号。

回退须恢复匹配的 SparkClaw checkout（消费者依赖、vendor 清单、Go 投影、Desktop preload 一起），在**相同 owner 状态及工作区路径**下重新 setup/check。Desktop/Gateway 单独部署时，也要恢复/重建匹配产物。只有兼容 ledger v1 的发行可接管；抽离前的旧部署不属于可兼容整组回退目标。未知格式或丢失/旧权威账本需恢复处理；禁止删除 ledger、授权索引、capture 或发送 journal 让回退启动。切换失败时保持不可用，直到恢复经过校验的匹配发行组。

## 用户最终验收

SparkClaw 抽离及后续修复已合并到 main；供应商实现仍在 App-CLI 维护 fork。2026-09-29 的原服务恢复是历史记录，本轮以本页 2026-09-30 实测及部署状态为准。恢复原服务时停止新 Executor 并保留其持久状态，不把账本交给旧代码消费；Gateway 继续使用邮箱权威存储和保留的原件文件。

最终验收应激活匹配的 Gateway/Controller/Desktop，确认正常浏览器任务页，再检查冷热读取、通知、账户变化及其余发送模式。原[七任务 App-CLI CI](https://github.com/ZZZZJJJ0928/App-CLI/actions/runs/36552474114)仅证明初始抽离版本；Windows 跳过了 4 项 POSIX 传输测试，非 POSIX Runtime v2 清理和 SparkClaw 云端 CI 不在其通过范围。

脱敏的机器可读证据：[app-cli-extraction.json](../../docs/evaluation/app-cli-extraction.json)。

无法证明任务页清理成功时，Host 在私有 `cli-runtime` 写入 `cleanup-fence.json`。回收 daemon 不会清除此围栏，Host 重启后仍禁用应用准入，普通浏览器启动不受影响。操作员须停止 Executor/Controller、重启专用浏览器并确认旧任务页消失，再归档诊断并仅移除这个 **Host 清理围栏**，随后重新 setup/check。不得移除 `authority.json`、ledger、授权索引或发送 journal。

首轮 Windows CI 检出 Unicode 夹具按系统编码读取的问题；显式 UTF-8 修复两项失败，最终矩阵全部通过。

## 2026-10-09 原生附件与发送恢复候选

配套 `.16` 运行时将 Outlook 共享功能区的文件操作与唯一受管编辑器、由原生文件
选择事件实际激活的输入框关联。多个编辑器或操作、仅支持图片的输入框、外来
选择事件及被替换的输入框均在上传前拒绝；上传内容仍为校验后的桌面来源字节。
QQ 附件就绪行契约仍需真实服务商验收。

回执核实比较已审核附件的不可变元数据，不重新打开已失效的临时路径。经鉴权且
不再执行的原任务可返回持久化 `not_sent` 证明，绑定 invocation、任务、意图、资源、
发行绑定和账本代际。`.15` QQ 迁移仅接受精确固定的绑定及已记录的发送前失败；
仅缺少发送日志不能证明未发送。Gateway 只解析同一草稿版本和发送尝试，保留
原证据后才允许重新审核。未知或已经提交的尝试继续受隔离，不会自动重发。

[候选证据](../../docs/evidence/mail-native-attachments-candidate-2026-10-09.json)
记录了 59 项组合测试、12 项真实 Chromium fixture、配套构建复现及安装回滚检查。
这些检查不等于真实服务商发送、送达或收件附件验收。
