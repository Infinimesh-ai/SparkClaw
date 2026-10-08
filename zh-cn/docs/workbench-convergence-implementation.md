# 工作台收敛实施记录

> 语言：简体中文 | [English](../../docs/workbench-convergence-implementation.md)

日期：2026-10-06。实施分支：`codex/workbench-runtime-convergence`，起点为
`9b92968d`。本文记录源码和隔离验证，不代表运行服务已升级。
已确认的产品行为见[方案](workbench-runtime-convergence-design.md)。

## 阶段 0：基线与边界

修改前，八份方案／架构／索引文档与来源工作区逐字一致，包含第 6.1 节。
来源工作区未修改。通过已知 Linux 主机找到 `/home/infinimesh/InfiniCenter`，
已读注册表、空收件箱、中枢 C0001、proposed 0031 及 JingSi、App-CLI、IMMS
已接受契约。实施评审已追加到 0031；其状态仍为 proposed。
内部方案不代表外部协议或留存变更已获接受。

| 范围 | 初始行为 | 处理方向 |
|---|---|---|
| 历史 | 主机 Store 与桌面 SQLite／文件 | 保留本地归属及认证范围，统一选择规则 |
| 运行时 | 桌面另建 ToolHub 和策略 | 共用有效提供方、策略和注册表，隔离仓储及资源适配器 |
| 上下文 | 主机类型化历史与桌面 32 条／96 KiB 信封 | 分离传输限额与公共模型上下文选择 |
| 宿主资源 | 主机工作区／采集浏览器与明确绑定的桌面 Host | 保留资源授权，不隐式访问其他工作台 |
| 交付 | 主机结果持久化与桌面文件验证／ACK | 保留持久成功及仅查询的恢复语义，不重放不确定写入 |
| 调度 | 主机扫描过期任务；桌面后端续租 | 明确行为变更：本地轮次认领、离线轮次永久跳过 |
| 邮件 | 后端权威，桌面缓存 | 保留单一采集和服务权威 |
| 外部调用 | JingSi 持久结果／事件、App-CLI journal、IMMS evidence | 保持冻结契约和留存，不重置或导入 |

基线环境：macOS ARM64、Go 1.25.12、Node 26.2.0/npm 11；隔离 Linux ARM64
使用 Go 1.25.5／Node 26.2.0。两机测试前均执行 `npm run setup:document-tools`
安装已声明的文档依赖。

| 基线检查 | 结果 |
|---|---|
| Mac Go 构建 | 通过 |
| Mac Go 全量测试 | Gateway、cmd/sparkclaw、browser-host 存在原有失败：临时路径规范化保护和 Linux `/dev/shm` 要求；其他包通过 |
| Linux Go build／vet／全量测试 | 全新隔离快照通过，使用中枢 JingSi conformance manifest |
| Desktop | 102 通过，无跳过 |
| WebChat | 196 通过；TypeScript／Vite 构建通过 |
| 双语文档／本地链接 | 阶段 1 指南更新后检查 103 份英文文档 |

Mac 日志在忽略的 `.cache/convergence/`；Linux 基线日志和源码快照在
`/home/infinimesh/.cache/sparkclaw-convergence-20261006-DIrlhW`。
测试使用隔离文件，不连接生产业务服务。

## 阶段 1：现行指南

README、架构／索引、Store 和 WebChat 指南明确主机工作台持久化与执行内容的区别。
Mac 指南使用 `main`，链接有日期的原生证据，不再把已交付能力描述为未实现。
既有硬件验收缺口继续明确保留。

## 公共上下文准入

`configs/workbench-limits.json` 是限额来源，生成器将常量投影到 Go 和桌面安装包，并检查
是否过期。两种工作台均支持最多 64 KiB UTF-8 用户输入，提交信封最多 1 MiB／32 条消息。
主机及提交仓储使用同一最后八条对话选择和 UTF-8 消息边界。有效模型的 token 准入仍是
最终依据；字节上限不保证每个 64 KiB 输入都能放进配置的 embedding／guard 模型。
较大的传输额度移除了桌面独有的 16 KiB 限制，并为所选历史留出空间。
结果／文件限额和 24 小时绝对交付到期不变。

定向测试比较主机 FileStore 与提交 MemoryStore 的选择结果、跨会话隔离、UTF-8 边界、
相同 64 KiB HTTP／信封准入及生成契约新鲜度。Go build／vet 与 agent、workbench、
execution 包通过；合入限额和调度后 Desktop 116 项通过。

## 公共组装与工作台行为

| 范围 | 已实现结果 | 代表性提交 |
|---|---|---|
| Runtime | `agent.WithExecutionScope` 从活动 runtime 派生仓库／资源，提供方、策略与注册表保持共用 | `3c488fb5`、`59a65de9` |
| 上下文 | 同一历史选择规则和生成的准入限额；主机已接收请求保留固定快照 | `980d2801`、`5f15a34d` |
| 草稿 | 公共版本队列配主机 Store／桌面 SQLite 适配器；恢复文本与文件引用、显式冲突恢复、身份及 StrictMode 保护 | `7c1520a1`、`d5893e44`、`ae0338b0`、`6c253d0f`、`73547c98`、`c38e86f2` |
| 提交 | 稳定请求身份，草稿清空／确认前保存输入；原 ID 查询、列表、取消；未知结果及 lookup 404 均不重放 | `faf7551d`、`5f15a34d` |
| 计划 | 桌面本地 schema 6，主机 Memory／File／PostgreSQL 定义与轮次；原子领取、离线永久错过、保留周期未来轮次 | `282e1cab`、`46a94cd4`、`9d5c20e4` |
| 背压 | 有界生产分派在工作线程满时继续观察脉冲；105 任务回归区分健康积压与实际可用性间隙 | `ac2c70f9` |
| 只读恢复 | 安装端状态／文件、邮箱授权、普通浏览器 fence GET 读取私有快照，不初始化可写服务或清理文件 | `00203a23`、`1547c2ed` |

作用域 ToolHub 使用相同注册表和活动提供方，不是无边界的工具并集。Owner 持久记忆／计划、
沙箱工作区、采集浏览器与外部连接器 ledger 需要显式资源绑定；未绑定时返回
`resource_unavailable`。提交执行仍使用临时仓库和 Linux 内存文件系统。主机工作台历史保存在
自己的 Store 中（包括 PostgreSQL），不复制到执行控制记录。

主机草稿 CAS 清空发生在持久准入和输入保存之后，不是跨 Store 与执行控制文件的单事务。
步骤间崩溃会留下可按原请求查询的记录，不会静默丢弃用户文字或授权再次提交。桌面入队／清空
是一个本地 SQLite 事务。保存草稿和重连都不提交执行。

## 中性命名与配套发布

机械重命名 `014b0132` 与协议／存储行为切换 `9e1c3fbe` 分开提交。产品使用
`internal/execution`、`internal/browserhost`、`internal/mailsync`、`/api/v1` 资源与
`X-SparkClaw-Digest`。浏览器宿主传输为 `/api/v1/browser/hosts`；桌面存储为
`userData/workbench`；后端控制根为 `State.Path + ".execution"` 和
`State.Path + ".mailsync"`。旧路由返回 404，旧摘要头不能提交执行。回归预置损坏的历史
控制文件，验证新绑定／执行成功且未打开、复制或修改旧数据。

不提供路径别名、旧设备支持或 schema 导入。[发布指南](workbench-release.md)要求后端、
WebChat、桌面使用配套构建，并选择新存储、重新登记。在该版本内，正常重启恢复已保存的
历史、草稿、文件与计划。来源 main 和运行中的安装均未修改。

## 已复核保留的版本与历史引用

| 保留引用 | 理由 |
|---|---|
| `docs/client-r3-implementation.md`、`docs/macos-r3-acceptance.md`、`docs/macos-r3-dual-host-acceptance.md` 及中文镜像 | 有日期的源码／部署／实机证据，hash、旧命令和验收 ID 保留原历史意义 |
| 客户端／后端、Mac、桌面内嵌及共享后端设计文档；本方案的基线盘点与映射表 | 原始设计版本，明确链接当前架构／发布指南 |
| Workflow／profile revision 3、`workflow_registry_test.go`、`workflow-capabilities.md`、外部 MCP 及财经设计 | 真实协议／Workflow 版本，不是产品命名空间 |
| App-CLI 契约／投影／vendor 资产及 JingSi／IMMS ledger | 冻结的外部接口与留存义务，不受内部切换影响 |
| `gateway/workbench_cutover_test.go`、`gateway/retired_schedule_routes_test.go` | 证明旧路由／header／存储不能恢复执行的反向测试 |
| Store 测试 run ID `r3` | 无关 fixture 标识，不是存储或传输路径 |

审计针对活动生产命名，不追求破坏真实版本号和历史证据的零匹配搜索。

## 最终源码验证与交付

可执行源码版本：`bdcd5f6a081eb7f5761d325006efb96be154b981`。后续收尾提交仅更新本文档。`e84fb5be` 与
`bdcd5f6a` 的审批／登录清理只退役已确定关闭的原执行权限，保留新输入与会话互斥，并区分
明确投递失败和未知结果。独立复核未发现新增阻塞问题，并重新运行了新增回归。

| 检查 | 结果 |
|---|---|
| macOS Go build／vet 与可移植恢复／切换定向测试 | PASS；安装端执行的 tmpfs 测试仍像基线一样要求 Linux |
| 新 Linux ARM64 快照 Go build／vet／全量套件 | PASS，共 63 条包结果；覆盖默认 File／Memory，并启用真实临时 PostgreSQL 17 |
| Linux race | PASS：cmd/sparkclaw、gateway、agent、toolhub、policy、execution、browserhost、mailsync、reminder、messagecontrol、store；启用 PostgreSQL |
| 默认 File／mock golden | PASS：独立新快照的 47 用例及扩展检查，合成端口 28889／28891 |
| Desktop 单元／契约测试 | PASS：122 项，无跳过 |
| WebChat 测试／构建 | PASS：52 文件、218 项；TypeScript／Vite 生产构建 |
| 生成契约／preload | PASS：公共工作台限额与受管脚本 |
| Mac／Linux 出站宿主原生 fixture | PASS：Electron 44.4.3 真实内嵌页、click／fill、20 次切换、页面作用域及丢响应写入 fence |
| Mac 重启 fixture | PASS：三个新 Electron 进程、真实 safeStorage、草稿／文件引用／周期计划恢复；离线到期轮次错过、未来保留，执行 POST／租约调用均为 0 |
| Mac ARM64 打包 | PASS：未签名 DMG／ZIP、公开客户端白名单审计（66 个应用条目、5 个 UI 文件）、DMG checksum 验证 |
| 冻结 Linux UDS 原生验收 | 已安装依赖并构建 UI，但被主机沙箱环境阻塞，不计为通过 |
| 双语文档／本地链接 | PASS：108 份英文项目 Markdown 及镜像 |
| Team-Skills 经验验证 | PASS：9 个 skill，0 错误；20 条既有日期／待归纳经验警告 |

桌面／WebChat 可执行源码树自 `9e1c3fbe` 全量测试后未变化；原生 fixture 与打包已在上述
可执行源码版本重新运行。Mac 安装包 SHA-256：

| 产物 | SHA-256 |
|---|---|
| `apps/desktop/dist/SparkX-0.1.0-mac-arm64.dmg` | `31a51f0bbd66a5a4d0b0dcc91c86439b5beb979d99c64e121426ab7ce492fb54` |
| `apps/desktop/dist/SparkX-0.1.0-mac-arm64.zip` | `a3bbf3fed97ee0601e450197e637b064b79879f8392a0de238b126761327c592` |

冻结 UDS runner 无法启动启用沙箱的 Electron：隔离 npm 的 `chrome-sandbox` 为普通用户所有、
权限 0755，AppArmor 同时禁止非特权用户命名空间。未禁用沙箱限制或修改主机安全配置。
独立出站宿主 Linux fixture 使用其既有 `--no-sandbox` 测试选项，PASS 证明传输与页面行为，
不代表沙箱验收。SIGTRAP 与环境诊断保存在 `host-review-native-uds-stderr.log`。

这些检查不代表实际 OS 睡眠／唤醒、签名／公证、已安装升级、真实模型／Info／邮箱提供方、
x64 Mac 或完整 M01–M12 实机验收。共享活动提供方由单元／集成测试覆盖，golden 使用 mock
模型。生产切换仍须按发布指南作为另行授权操作。

本机日志与产物保留在主工作树忽略目录 `.cache/convergence/`、`apps/desktop/dist/`。
Linux 证据及隔离源码快照位于 `/home/infinimesh/.cache/sparkclaw-convergence-20261006-DIrlhW`，
最终验证使用 `release-*` 日志。只停止了临时测试容器与 fixture；未修改生产服务、真实 profile
或来源 main，未推送、合并 main、部署、安装或删除真实数据。InfiniCenter 状态及 pending 经验
记录相同源码交付边界；外部消费者无须代码跟进。

## 本地 main 集成（2026-10-08）

按用户要求，将源码交付 `85d32c7b` 合入本地 `main`，合并前 HEAD 为 `dfdf4e72`。
main 一侧仅多出原始设计文档提交，保留其历史，并以实施后的现行版本解决八份文档重叠。
可执行源码与上文已完整验证的交付逐字一致；删除 worktree 前，在合并后的检出核对
build／vet、桌面／WebChat 和双语文档检查。

保留开发版 DMG／ZIP、hash、包审计及验证日志后移除已完成 worktree，保存位置为
`/Users/dev/.cache/sparkclaw-convergence-closeout-20261008-b3q0lbvd`。旧 worktree 下的产物链接属于历史引用。本次要求合并本地源码，不发布远端
Git 更新，也不执行运行服务／已安装应用的切换。
