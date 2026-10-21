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

## Controller 的 systemd 停止顺序：2026-10-10

生成的 Controller unit 改为 `KillMode=mixed`，显式保留 `SendSIGKILL=yes`，`TimeoutStopSec=60` 不变。首次 SIGTERM 只交给 Controller，由其关闭拥有的 CLI daemon；主进程退出后仍有子进程、或超过停止期限时，systemd 仍收割剩余 cgroup。Executor 与 Browser unit 策略不变。这样可避免 `control-group` 在 Controller 尚在排空、准备通过 daemon 显式清理页面时，提前独立终止该 daemon。

[可执行验证脚本](../../scripts/qualify-controller-stop-order.py) 使用纯 Python 父进程、detached 子进程和私有 Unix socket，运行四个隔离的 `systemd --user` transient unit，不连接 Browser、不读取应用数据。显式排空屏障让信号投递顺序可观察：`control-group` 先终止子进程，随后清理失败（exit 1）；`mixed` 允许父进程先关闭拥有的资源，父子正常退出。`mixed` 下主进程失败（exit 7）和夹具三秒停止超时，均会触发最终 SIGKILL 收割余留进程。四项在 systemd 259 上全部通过，cgroup 为空，夹具 PID/unit 无残留，现有服务未改变。生产停止期限仍为 60 秒。在具备用户管理器的 Linux 上运行 `python3 scripts/qualify-controller-stop-order.py`，匹配的 setup 脚本须位于同目录。安装脚本 **13** 项测试、Python 编译及 shell 语法检查也通过。

归档 QQ 的 `tab-list / process_exit_page_closed` 事件缺少旧 PID 信号轨迹和原始 CLI 错误；这里证明了配置风险，未证明该历史事件的唯一原因。此错误仍不能证明浏览器任务页已经不存在，清理围栏及操作员恢复规则不变。真实部署的优雅停止仍须独立验收。详见[脱敏 systemd 证据](../../docs/evidence/browser-controller-stop-order-2026-10-10.json)。

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

无法证明任务页清理成功时，Host 在私有 `cli-runtime` 写入 `cleanup-fence.json`。回收 daemon 不会清除此围栏；应用准入以 `browser_cleanup_pending` 拒绝（Gateway 为 `email_browser_cleanup_pending`，检查连接时显示可读提示），并最多每 10 分钟记录一次 `application_cleanup_fenced`。

围栏记录其范围。`page` 围栏表示自有进程均已回收、仅任务页关闭未获证明（例如页面已在 CLI 之下被关闭）。之后的准入仅在本 Host 不再持有其他资源、且 Bridge 报告所有窗口均无任务页或遗留任务页时清除它，并记录 `application_cleanup_fence_cleared`（最多每 15 秒询问一次，且仅在配置了 `SPARKCLAW_BROWSER_BRIDGE_NATIVE_SOCKET` 时询问）。`page` 围栏不会阻止 Host 启动。`process` 围栏（回收失败）或无范围的旧围栏在 Host 重启后仍禁用应用准入，且不会自动清除：操作员须停止 Executor/Controller、重启专用浏览器并确认旧任务页消失，再归档诊断并仅移除这个 **Host 清理围栏**，随后重新 setup/check。不得移除 `authority.json`、ledger、授权索引或发送 journal。

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


## 取消累计任务条数上限（2026-10-15）

发行 `0.3.0-sparkclaw.21` 取消默认 10,000 条任务准入上限。`maxTasks=0`
不统计历史条数；显式正整数仍可启用可选上限。独立的 512 MiB 已分配数据门、
事件留存、OS 进程锁、执行代次、请求键、授权索引与发送 journal 保持。
原 ledger v1 数据库无需迁移或删除。容量拒绝和 Host 清理围栏是独立机制：
删除清理围栏不能解决账本已满，修改容量也不能证明旧任务页面已清理。

成套发行以原 `.20` 归档应用 `ledger-unbounded-history.patch`，保留此前邮件和
Host 修复。在 Node 26 下执行 `python3 scripts/build-ledger-unbounded-release.py --check`
可复现相同制品。维护 fork 同步相同 Ledger 修改与独立回归用例。

验证：Node 26.2.0 runtime 301/301，Python 86/86；已安装 Controller 206 通过、
124 项平台/显式启用用例跳过，另行启用安装 Python 的原请求对账和任务历史验证。
新回归在旧代码上失败，重启后保留 10,000 条合成终态历史并接收第 10,001 条，
同时保留防重、冲突与原事件。显式条数门和字节门仍被覆盖。隔离成套安装、
篡改/版本混用拒绝和保留状态回退通过。测试未发送真实邮件。macOS 使用规范
路径 `/private/tmp`，默认临时目录别名会触发现有路径隐私检查。


work2 于北京时间 2026-10-15 16:50 完成激活，切换前完成冷态成套备份。
原有 10,000 条任务逐条哈希一致，执行代次 26→27；Gateway 重启前业务数据库
和 workspace 保持一致，现有配置、Gateway 容器和镜像保留。仅 Controller /
Executor 切回 `.21`；Gateway 源码差异为注释与测试，无需替换二进制。
其他四台服务器与已安装桌面客户端未改动。

随后实测账本达到 10,008 条，QQ 完成 3 次分页采集，Outlook 完成 2 次，
两者同步水位继续推进且无 last-sync error；原 10,000 条哈希仍匹配，清理围栏
没有重现。原有 observer 状态仍为 `degraded`，本次证明分页同步恢复，
不将其声称为推送监听恢复；未发送测试邮件。源码为 SparkClaw `13a2a4fe`、
维护 fork `c4ea157`，本次未发布这些提交到 GitHub。


## 过期监听准入恢复（2026-10-15）

成套 `.22` 修复账本容量恢复后监听仍为 `degraded` 的问题。被拒请求此前已经
持久化签名授权索引，后续重试一直复用未决原请求。必须先通过公共 lookup 确认
结果仍 unresolved，且原签名授权属于本应用已过期、尚未绑定 task 的可续期只读
watch，邮件客户端才创建新会话，并在签名资源中携带 `previous_request_key`。
原索引和全部历史保留，新监听明确产生 gap 并触发补查。未到期授权与已知任务
继续原恢复路径；通用 ProductClient 未决准入规则和发送防重规则不变。

`watch-admission-recovery.patch` 基于精确 `.21` 包；Node 26 下执行
`python3 scripts/build-watch-recovery-release.py --check` 可重现制品。
真实 Python/Unix socket/SQLite 测试覆盖容量拒绝、原凭据及轮换凭据冷启动、
授权到期、原任务/索引保留、关联 gap、通知传递、续期与已知任务复用，另有七类
授权拒绝测试。Controller 213 通过、126 个平台/按需用例跳过；显式启用的两个
已安装 Python 监听场景另外通过。成套安装、篡改/混用拒绝及保留状态回退通过。
全部为合成事件，无测试发信；生产结果另行记录。


2026-10-15 work2 生产验收：17:11（北京时间）激活 .22，epoch 27→28。冷态
备份保留升级前全部 10,040 条任务，后续逐条哈希仍一致；业务数据库和 workspace
在 Gateway 恢复前一致，Gateway 容器/镜像及配置保持。QQ 和 Outlook 均产生
watch_ready 与关联原未决请求的 gap，Gateway 状态恢复 watching，补查成功且
无同步错误。两者保持同一 task，授权 revision 1→2，并跨过原五分钟授权到期
时间仍运行，没有再降级或出现 cleanup fence。只读监听收到心跳并持续运行；
未通过测试发信验证新的真实入站通知，合成通知传递由隔离回归覆盖。

冷态回退点为 `/var/backups/sparkclaw-app-cli-watch-20261015`；不恢复旧数据库
或 authority。其他四台服务器和 Mac 安装客户端未改动。SparkClaw `faacf177`、
维护 fork `d56dc9a`；本轮未推送上游。


## 真实来信与 Outlook 原文下载授权（2026-10-15）

用户授权的 QQ/Outlook 真实互发揭示了 `.22` 的另一处故障。QQ 来信在 2.86 秒
内将通知交给 Gateway，8.54 秒发现邮件，11.41 秒内观察到原文采集完整、解析
就绪。Outlook 约一秒发出入站通知并列出准确的来信，但原文下载返回 HTTP 400
与 `email_network_original_unqualified`，不能算同步成功。首次 QQ 发信使用
Outlook 内部账号标识而退信；随后根据另一封测试来信的 From 头确认实际投递
地址，创建独立的更正发送。未重试任何结果未知的发送。

当前 Outlook 消费者邮箱采用原生 Entra 请求头授权。旧 query 下载 token 已
过期，原生旧刷新函数在此模式下不执行操作。实测站点原生下载器，以及原生
请求头授权加现有有界 fetch，均返回 HTTP 200 和准确测试原文。成套 `.23`
因此在邮箱页面内取得原生 Authorization 或 X-Token 请求头。授权后的 URL
必须与核验过的 DownloadMessage 目标完全一致，只允许移除 token 参数。保留
账号、原消息 ID、域名/路径、请求头边界、取消、禁止重定向与原文完整性校验；
凭据不进入结果或诊断。旧式 token 继续使用站点原生的过期/刷新策略。

消费端另修正明确 blocked 的 read/capture/collect_page busy 结果分类，保留
`browser_busy`，让 Gateway 按既有短暂让步机制复用原 invocation，不再将争用
误判为邮箱服务不可用。uncertain 与发送结果语义保持。此问题在测试发信独占
共享浏览器时得到实测。

使用 `python3 scripts/build-outlook-authorization-release.py --check` 重现。
补丁应用到准确 `.22` 归档，保留已部署的账号识别行为；维护 fork 在重新生成
Reader 前也补齐该既有账号修复。源码：SparkClaw `cd3de78a`、fork `3376fdf`。
验证：Runtime 321 通过 / 2 按需跳过，Controller 258 通过 / 126 平台或按需
跳过，work2 暂存 78 通过（含已安装 Python 恢复测试）；Gateway busy 让步回归
通过。确定性成套构建、独立安装、混用/篡改拒绝与保留状态的整组回退通过。
公共 Lifecycle 2.0、Host 1.0、ledger 1 与 UI 不变。

前三次激活因 Controller 非零退出而中止并自动恢复 `.22`，没有替换代码或
回滚数据。Executor 与 Controller 存在 systemd PartOf/顺序关联，仅调整停机
顺序不足以结束在途监听注册。成功重试先将正式监听集合协调为空，等待全部
活跃应用任务结束，再要求服务正常退出、无所属进程、无清理围栏且 Executor
锁可取得。旧 Outlook 测试来信于北京时间 17:48:09 在 `.22` 页面重启后补采，
早于 `.23` 激活；此补采本身不能证明新授权路径或常驻页面行为。

运行时激活后，浏览器托管 Reader 起初仍是旧源码，验收检查发现实际导入摘要
不符。考虑到 Chromium 保留旧后台注册，将扩展提升到 product r11，并同步生成
桌面源码投影（`c9de358b`）。随后三次组件部署仍未通过实际导入检查，已恢复
公共组件，未回退数据。Chrome 原生扩展界面最终揭示原因：解包 CRX 含
`_metadata/verified_contents.json`，Chromium 将其视为保留名称而拒绝加载。
提交 `5eed92a5` 在归档路径安全检查后仅排除这份签名包元数据，仍核对原始
CRX 摘要并保留全部扩展代码，同时拒绝复用包含该目录的安装。20 项部署回归
在本机和 work2 均通过，覆盖元数据排除、路径穿越拒绝及拒绝复用异常安装。
原生策略页确认 CfT 策略路径正确且无策略错误，无需替换 Profile、删除扩展
数据库或修改邮箱授权范围。

修正后的包随后遇到 Chrome 因先前加载失败而保留的关闭状态。暂停 Gateway
准入、排空监听任务、停止 Controller／Executor，并证明无存活 CLI 和 cleanup
fence 后，操作者在原生扩展界面为这个可信的产品解包扩展开启开发者模式，恢复
启用并重新加载。四个托管 UUID、源码及依赖摘要终于全部通过。此前异常扩展
导致 Controller 停止时非零退出，该证据已保留，未绕过进程或 fence 检查；未
重置历史、authority 或邮件存储。该操作仅作用于产品专用 Profile。

北京时间 18:36 恢复服务，运行 `.23`、浏览器 product r11 及 SparkClaw 源码
`5eed92a5`。epoch 36→37；Gateway 重启前，暂停时的 10,233 条任务逐条摘要
保持。最初 10,000 条与监听升级前 10,040 条仍完全一致；授权修复前 10,141 条
任务身份和不可变意图字段全部保留，其中一条原 uncertain 的 QQ 采集合法对账
为 complete。Gateway 容器、镜像和配置保持。冷态检查点为
`/var/backups/sparkclaw-app-cli-managed-readers-20261015-r11-clean`，最终原生
重载回执与账本核对位于对应的 `-native-reload` 检查点。其他服务器和已安装
Mac 客户端未改动。

最终真实来信验收使用唯一标记 `SC-W2-LIVE-20261015T103657Z-18b319`，两方向
各完成一次原生浏览器发送，均有持久化 `sent` 回执。以下从提供方收件时间计算，
不使用发件方 MIME Date；完成列为有界监测首次看到原文采集 complete、解析
ready 的时间：

| 收件邮箱 | 观测到原生通知 | Gateway 收到通知 | 发现邮件 | 观测到完整同步 |
| --- | --- | --- | --- | --- |
| QQ | 2.27 秒 | 2.87 秒 | 9.34 秒 | 11.43 秒 |
| Outlook | 0.53 秒 | 3.25 秒 | 23.88 秒 | 31.51 秒 |

两封主题均唯一命中，原文／manifest SHA256、MIME 发件人／收件人、主题、解析
正文与入站身份全部匹配。Outlook 页面自 UTC 10:36:34.790 起保持未刷新；旧
query token 于 10:40:54 过期，早于 10:42:31.253 收件与 10:43:02.262 成功
采集。实际加载的 Reader 显示原生授权阶段 `authorized`，最后页面年龄为
455 秒，证明生效的是过期场景下的新授权路径。

北京时间 18:44，两邮箱均为 `watching`，同步 idle 且最后同步错误为空。
两者保持 epoch 37 的原监听 ID，授权 revision 已 1→2→3 续期。Gateway
healthy；Browser、Controller、Executor 均 active、零重启且无 cleanup fence；
固定配置与 authority 摘要一致。临时监测已停止，诊断标签已关闭，测试邮件及
历史保留。另有一条 QQ `mark_read` 返回 `INVALID_REQUEST` 并保留防重状态，
未重试，也未计入本轮入站同步验收；不声称已修复标记已读功能。

私有回执、监测样本与脱敏时序验收保存在
`/Users/dev/.cache/app-cli-live-inbound-20261015/managed/`，未将邮箱地址、令牌
或邮件原文提交入库。最终 113 份文档镜像／链接检查及桌面 Reader 生成检查
通过。未推送 GitHub，未部署其他节点或已安装桌面端。

## Lifecycle 3.0 与 ledger 2（2026-10-16）

成套发行 `0.3.0-sparkclaw.24`（runtime digest `bc177aa3…`，App-CLI 分支 `codex/lifecycle-v3` 的 `fc18087`）实现了修订后的[请求授权与存储生命周期设计](app-cli-storage-lifecycle-design.md)：请求代次中的 op3 操作身份及产品确认排空后的关闭、prepare/invoke 受理、经独立进度锚确认的 reserved → consume_pending → released 发送放行、自动回滚向前封闭、持久 ACK、未知发送的用户结案、三个固定授权槽位、关闭代次后的回收及按存储占用的准入。SparkClaw `78035c55` 增加 Gateway 发件箱身份、ACK 重试、排空关闭循环、`POST /api/email/drafts/{draft}/resolve` 与 ISCP `mail.drafts.resolve`、原有未知发送状态区内的结案选项，以及独立 `progress-anchor` 服务。

本地结果：App-CLI Node 348/348（含需 Python 的恢复测试）与 Python 86；Controller 265 通过、119 个平台或可选跳过；Desktop 373；WebChat 319；Go 包除 13 个在未改动基线上同样失败的 macOS TLS/`/dev/shm` 测试外均通过。发行验收、分阶段安装与部署验收通过；JavaScript 锚客户端已与 Go 锚服务互通验证。

work2 影子迁移复制了在线账本（12,352 个任务）并以 ledger 2 打开：除唯一一个 running 行像任何重启一样变为 `blocked/EXECUTOR_RESTARTED` 外，所有行一致。迁移耗时 58 ms，legacy 键仍可解析，不存在的 legacy 键返回 `retired(legacy)`，没有 legacy 行可被回收。

第一次激活在迁移检查已通过之后，因验证脚本的路径错误失败；自动回退无错误地恢复了排空后的状态、源码 `5eed92a5`、`.23` 发行与原 Gateway。第二次激活于 06:38 UTC 完成：12,368 个任务全部保留且零行变化，schema 2，执行代次 38 → 39，业务数据库与邮件工作区不变，锚以 generation 1 初始化。三分钟后新 Gateway 健康、无重启和错误，两个监听均以 `watch_ready` 运行，采集与探测以 op3 身份正常完成，Controller 报告 `lifecycle-3.0`，代次控制可用。未发送任何邮件；真实发送、其 ACK 及用户结案留待用户验收。容量与留存数值在影子测量前仍为设计占位值。锚与 runtime 同宿主，只能检测不包含其自身状态目录的回滚（设计中的 B 档需要另一台主机）。

### Release 26 后续修复（2026-10-16）

成套发行 `0.3.0-sparkclaw.26`（App-CLI `90b0f8a`）与 SparkClaw `49875de6` 修复首次激活后发现的两个缺口。从未放行发送的任务在 runtime 中被取消，并报告为 `email_send_not_dispatched`，这是可重新审阅后再发送的确定失败；对账会关闭此类结果未知的草稿而不再卡住，已 retired 的远程写入仍保持未知。生命周期维护循环通过任一已绑定邮箱访问代次控制，没有草稿的部署也会关闭已排空代次。锚放行重试改为退避，并在短暂冷却期内快速失败。App-CLI 生命周期文档已更新为协议 3.0。

work2 于 07:14 UTC 从 `.24` 升级，无需迁移：12,433 个任务全部保留且零行变化，执行代次 39 → 40，业务数据库与工作区不变。随后轮转开放请求代次 2；Gateway 在一个维护周期内确认排空并以 `drained` 原因关闭代次 1。锚因一次真实的 `mark_read` 放行推进到序列 1。该 `mark_read` 仍为 uncertain，因为该命令在页面准备之前就放行（既有限制）。启动时的 QQ 监听失败已自动替换，与以往代次相同。

### Release 27：mark_read 放行位置（2026-10-16）

成套发行 `0.3.0-sparkclaw.27`（App-CLI `24c71cb`）与 SparkClaw `5e96498a` 把 `mark_read` 的发送放行从处理器开始处移到原生已读请求之前：此时已重新选中确切目标，且待执行的已读意图已持久化。账户身份不可用等准备阶段失败现在可证明未派发，不再留下结果未知的远程写入；放行失败不会到达服务商，也不会记为未知已读状态。work2 于 07:29 UTC 升级，Gateway 容器不变：12,458 个任务全部保留且零行变化，执行代次 40 → 41，两个监听正常运行，所有服务无重启。由于 `mark_read` 会标记真实邮件，验证期间未执行，仍属于用户验收内容。随后，先前结果未知的 `mark_read`（任务 `c490b6ce`）通过生命周期 `resolve` 操作结案为 `confirmed_not_sent`：其 `read-state.json` 从未写入，而 27 之前的流程会在发出任何原生已读请求之前先把它写为 pending。结案记录了证据摘要，没有派发任何请求，work2 上已无结果未知的 Lifecycle 3.0 任务。


### Release 27 推广到 work1、work3、work4、work5（2026-10-16）

按用户要求，其余四台现已运行源码 `2e7814db` 与成套发行 `0.3.0-sparkclaw.27`。Gateway 二进制与 work2 已部署的 `49875de6` 构建逐字节一致（SHA256 `0bd9ed2874875e1079a97f4e513e7ef183ac0a58adc13cc6abf2bd32f20ebf2e`）。work2 保留原运行容器、启动时间和源码 `5e96498a`，只核验状态，未升级或重启。

四台均没有已绑定邮箱、草稿或发送快照，停用的旧 App-CLI 账本均为零任务。先在副本上验证迁移，再将实际账本从 schema 1 升到 2 并保留 authority：execution epoch 分别为 work1 1 → 2、work3 1 → 2、work4 9 → 10、work5 6 → 7。旧 local-mail runtime 状态原样保留；Controller、Executor、Browser 和同主机进度锚均已启用并运行。

四台 Gateway 均 healthy、零重启。全部 41 张业务表的原有记录、工作区文件、模型/配置及授权身份保持；Docker 环境、HostConfig、命令和网络地址与升级前一致。各节点均使用现有固定证书通过 Relay HTTPS 健康请求。相同制品复用已有功能和 UI 验收，本次未另跑模型基准、浏览器 UI 全套或真实邮件操作。

各升级节点保留 `/var/backups/sparkclaw-r27-20261016`，含已验证的 PostgreSQL dump、成套状态归档、配置/逐行指纹及回执，并保留停机的旧容器。禁止用旧 `.20` Executor 启动 ledger 2；软件回退须保留新执行证据，不得把恢复旧 authority 或旧数据库快照当作常规降级。同主机进度锚沿用 work2 的保证范围，不能检测连同锚状态一起恢复的整机回滚。InfiniCenter status 已记录本次明确授权的部署，0031/0035 提案与公共契约未因部署而改为 accepted。
