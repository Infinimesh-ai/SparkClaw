# App-CLI 抽离：实现与验收

[English](../../docs/app-cli-implementation-validation.md) · [设计](app-cli-email-extraction-design.md)

实现日期：2026-09-29。SparkClaw 分支为 `codex/extract-email-app-cli`，维护 fork 分支为 [`codex/sparkclaw-email`](https://github.com/ZZZZJJJ0928/App-CLI/tree/codex/sparkclaw-email)。源 commit 与完整产物摘要记录在 [configs/app-cli-release.json](../../configs/app-cli-release.json)。

## 已交付行为

QQ Mail、Gmail、Outlook 的应用脚本、Reader 源码/构建、通知语义、发送 journal、Schema、Manifest 和 binding 已由 App-CLI 维护。SparkClaw 仅消费经校验的发行归档及生成的 Go/Reader/Desktop 投影，不再保留独立可编辑的邮件实现；旧 `scripts/email`、Controller 供应商执行分支和应用池已删除，上层工作流、存储与 UI 继续消费原业务回执。

Gateway → 已鉴权的 owner Controller → App-CLI Python 公共 Registry → RuntimeAdapter 2.0 → 常驻 Executor → 注册应用 → 签名 HostPort → 原任务专属浏览器页面。通用 Controller/Bridge/Desktop、页面所有权及个人页保护保留在 SparkClaw。新增使用已有 Host 能力的非邮件应用只需扩展 App-CLI 注册/binding，无需修改通用调度器。

Executor 将请求键与主体/owner/意图持久绑定；丢包查原 task，无法确认受理时不得重发。执行授权过期不撤销合法查询/取消权限。恢复依赖原 journal、效果围栏和递增 epoch。read/watch 各持活动租约，实际 daemon 到期、清理失败隔离和有界 park 在控制进程丢失后仍生效。应用发行校验失败只禁用应用准入，普通浏览器仍可启动。

## 已执行验证

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

实现与上述验证已完成；本次没有改变生产服务或真实邮箱。最终验收在目标环境激活匹配发行，确认正常浏览器任务页，再验 QQ/Gmail/Outlook 冷热读取、通知、原件、账户变化以及明确批准后的发送/对账。供应商模拟验证与实际本地浏览器验证分开记录，不代替三家真实站点验收。固定源提交的 App-CLI [云端 CI](https://github.com/ZZZZJJJ0928/App-CLI/actions/runs/36552474114) 共 7 个任务全部通过：Windows/macOS/Linux × Python 3.11/3.13，以及生命周期运行时任务。Windows 按平台跳过 4 项 POSIX 传输测试；非 POSIX Runtime v2 清理和 SparkClaw 云端 CI 不属于此次通过范围。

脱敏的机器可读证据：[app-cli-extraction.json](../../docs/evaluation/app-cli-extraction.json)。

无法证明任务页清理成功时，Host 在私有 `cli-runtime` 写入 `cleanup-fence.json`。回收 daemon 不会清除此围栏，Host 重启后仍禁用应用准入，普通浏览器启动不受影响。操作员须停止 Executor/Controller、重启专用浏览器并确认旧任务页消失，再归档诊断并仅移除这个 **Host 清理围栏**，随后重新 setup/check。不得移除 `authority.json`、ledger、授权索引或发送 journal。

首轮 Windows CI 检出 Unicode 夹具按系统编码读取的问题；显式 UTF-8 修复两项失败，最终矩阵全部通过。
