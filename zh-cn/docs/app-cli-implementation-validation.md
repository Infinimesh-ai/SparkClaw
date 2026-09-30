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

执行状态：**按用户要求，以下工作延后至 SparkClaw 本次改动合并主分支后进行。** 本次只整理交接，不执行合并、继续发信、切换服务或生产部署。合并代码不代表生产验收通过，也不自动激活新运行时。

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

抽离及本轮修复已提交到维护分支。本次实测临时切换了 owner 服务，并发送了用户授权的测试邮件；验证后恢复原部署，停用临时 Executor，保留其持久状态。这是结束临时资格验证，不是把 App-CLI 账本回退给旧版代码；旧 Gateway 不接管新 Executor 账本或工作目录。

最终验收应激活匹配的 Gateway/Controller/Desktop，确认正常浏览器任务页，再检查冷热读取、通知、账户变化及其余发送模式。原[七任务 App-CLI CI](https://github.com/ZZZZJJJ0928/App-CLI/actions/runs/36552474114)仅证明初始抽离版本；Windows 跳过了 4 项 POSIX 传输测试，非 POSIX Runtime v2 清理和 SparkClaw 云端 CI 不在其通过范围。

脱敏的机器可读证据：[app-cli-extraction.json](../../docs/evaluation/app-cli-extraction.json)。

无法证明任务页清理成功时，Host 在私有 `cli-runtime` 写入 `cleanup-fence.json`。回收 daemon 不会清除此围栏，Host 重启后仍禁用应用准入，普通浏览器启动不受影响。操作员须停止 Executor/Controller、重启专用浏览器并确认旧任务页消失，再归档诊断并仅移除这个 **Host 清理围栏**，随后重新 setup/check。不得移除 `authority.json`、ledger、授权索引或发送 journal。

首轮 Windows CI 检出 Unicode 夹具按系统编码读取的问题；显式 UTF-8 修复两项失败，最终矩阵全部通过。
