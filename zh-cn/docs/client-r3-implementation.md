# SparkClaw 客户端 R3 实施与验收记录

> 语言：简体中文 | [English](../../docs/client-r3-implementation.md)

日期：2026-09-30。交付分支：`codex/sparkclaw-r3`。这是分阶段实施记录，不是生产切换。Mac 编译、实机、签名及公证由用户在交付提交上完成；不迁移旧测试历史。

实施依据：[客户端／后端 R3](client-backend-architecture-design.md)、[Mac LAN 设计](macos-lan-desktop-design.md)、[Mac 连接指南](macos-connection-guide.md)。

## 核对的基线

修改前完成 document-tools 环境准备。Linux ARM64、Node 26.2.0、npm 11.17.0、Go 1.25.5 上 Gateway build/vet/全量测试、桌面 17 项、WebChat 169 项及前端构建通过。原桌面自动载入本机私有 Token，消费后端 session/history，运行回环 adapter。设备签发／撤销存在，但设置导航未接入设备组件；这些均不能证明 R3。

InfiniCenter 确认 ProjectGroup-2；会话开始无未处理 SparkClaw 来信和影响本项目的 proposed 决策。已核对 accepted JingSi Runtime v1 与 App-CLI Lifecycle/Host 保证；新增 proposed 0031 及 JingSi／中枢 inbox 请求，记录留存与持久结果接收问题。现行契约与账本保留；本提案等待对端评审，不由本次实施单方接受。

## 阶段台账

| 阶段 | 本次交付范围 | 门槛与剩余工作 |
|---|---|---|
| P0 | 数据／身份拆分、全新本地 Schema、本地上下文限制、外部留存提案 | 部分完成：0031 为 proposed；后端临时内容／控制字段白名单、远程执行信封仍需实现与验收 |
| P1 | 主进程 SQLite ClientStore、本地对话／文件、输入＋不可变有界上下文＋请求 ID 同事务保存、受限 IPC 与本地桌面工作台 | 部分完成：网络 ExecutionClient／上下文提交及 Workflow 隔离未启用；本地保存明确不表示提交执行 |
| P2 | 独立首次／恢复领取工具、真实设备设置、桌面用户输入解锁、安全凭据复用、无密钥的版本化 LAN 描述符 | 部分完成：下文记录 Linux／共享验证；服务端安装身份绑定、执行事件／ACK、邮箱 revision／cursor／tombstone 同步待完成 |
| P3 | R3 正式路径关闭旧本地 adapter，不派发到未经授权的替代宿主 | 未交付：鉴权 WSS Broker、统一 BrowserHostAdapter、远程租约、按对话页面及写操作对账 |
| P4 | 用户选取文件的本地保存／另存、清单哈希与原子写入 | 部分完成：后端鉴权输出／文件交付、到期清理、磁盘／ACK／网络故障矩阵及内容残留审计 |
| P5 | Mac 客户端专用打包源码／配置及用户构建命令 | 仅源码；Mac 构建、GUI／硬件、签名公证、升级及生产切换待用户验收 |

普通 Web 保持既有路径，不宣称 R3 本地持久化。桌面 R3 不挂载旧共享历史 hooks，只开放本地保存与设备管理；任务执行、邮箱同步、浏览器自动化待后续阶段。已保存请求状态为 `awaiting_runtime`，不是后端接纳／完成。以后必须显式提交，升级不得自动重放本次队列。

## 本地已冻结边界与后端候选预算

ClientStore Schema 1 使用 SQLite WAL/FULL，生成新的安装 UUID；部署／Owner／已签发 client_id 隔离记录。位置为 `userData/client-r3`，不导入后端数据；退出保留数据库／文件，新版本地 Schema 不兼容时拒绝打开并保留数据。

本地输入最多 16 KiB UTF-8，上下文最多 32 条、总计 96 KiB、单条 16 KiB；仅 user/assistant 角色，不能携带可信授权。选取的本地文件最多 64 MiB，经私有暂存、fsync／原子改名及 SHA-256 清单验证。Renderer 不能指定后端或任意本地路径、越权身份、提交可信执行事件。

0031 中后端候选：执行 15 分钟、每任务内容 32 MiB、Owner 256 MiB、单结果 8 MiB、结果生成＋24 小时绝对到期、Host 租约 30 秒／心跳 10 秒。这些尚非已启用配置。无正文围栏仅保留请求／客户端 ID、规范摘要、状态、授权／租约代次及时间，部署生命周期保留；容量满拒绝新接纳，不删除旧围栏。控制记录不含 Prompt／标题／DOM／正文／输出，候选值不缩短外部契约。

## 验收记录

`PARTIAL` 是限定范围 Linux／共享证据，不是整项通过；`NOT_RUN` 表示缺少必需证据。Mac M01–M12 全部保持 `NOT_RUN`，等待用户同 SHA 实机证据。

| 用例 | 结果 | 证据及限制 |
|---|---|---|
| A01/A02/A14 | PARTIAL | 全新 ClientStore 与设备／Owner／后端隔离、重启持久保存、不读取旧 session；共享邮箱与真实 Mac/Linux 双端未验 |
| A03 | NOT_RUN | 新后端上下文／Workflow／临时存储及内容残留审计尚未实现 |
| A04/A13 | PARTIAL | 本地事务／文件失败、哈希、路径测试；真实磁盘满及后端输出／附件交付未验 |
| A05/A06 | NOT_RUN | 存储 receipt helper 重放／gap 测试不证明真实 submit／ACK／重启及 24 小时后端清理 |
| A07–A10 | NOT_RUN | Broker／Host／浏览器调用链未启用，R3 正式路径已关闭旧 adapter |
| A11/A12 | NOT_RUN | 未验证新邮箱同步／在线任务租约；保留原后端常驻邮箱服务 |
| A15/A17 | PARTIAL | Linux／共享代码验证版本化连接、main 登录及安全存储隔离；Mac Keychain／证书／实机 LAN 未验 |
| A16 | NOT_RUN | Mac 构建／硬件／签名／升级及普通 Web 本地存储未验 |
| A18–A21 | PARTIAL | 本机领取／恢复及设备导航／失败／重试／撤销夹具；没有真实生产凭据或 Mac 安全存储证据 |

集成验收完成后追加实际测试数、Git SHA 和可执行交接命令。源码交付、Mac 构建、实机验收、生产切换分别记录。
