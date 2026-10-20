# ISCP 认证恢复与全双工并行实现

[English](../../docs/iscp-parallel-implementation.md)

日期：2026-10-19。本文记录两个本地工作树的实现，不代表生产部署，也不表示基线二进制能读取新状态。本地实现与集成验收已完成。最终实现候选为 SparkClaw `62358707d7f11202df4df44ab9420a77f305ee15`、ISCP `22da89f329a88bde8e12219c359e82063064798b`。

## 范围与接口冻结

用户选择先共同评审和冻结接口，再并行实现[连接认证与业务操作恢复设计](iscp-authentication-operation-recovery-design.md)和[全双工传输设计](iscp-duplex-websocket-design.md)。两个工作树均以 SparkClaw `cf9b81dec39b8872efc43650080bb8facaa58511` 为基线，共用设计快照 `76ed11bd94ab4bf4b7f63697a96dbf36d4fce053`。独立 ISCP 服务的基线为 `10fe37b3bd02a56b4f9d6cb8dc0333d7eec5356a`。

分支分别为 `codex/iscp-auth-operation-recovery` 和 `codex/iscp-duplex-websocket`，前者也承载集成候选。原检出目录及设计草稿保留。

InfiniCenter 0038、0041 已接受自管 fork 的共同接口。契约修订 `2026-10-19.2` 纳入已接受的 0042 Base64URL key-ID 正则修正，原快照另存归档。JingSi 的永久 `not_started` 围栏及 IMMS 契约保持，0040 继续撤回。ISCP 服务与 SparkClaw 消费方独立构建，通过冻结的线上协议互通。

## 实现行为

凭据恢复使用原持久操作意图及新鲜、精确关联的 HTTPS 认证尝试。issuer 与 Relay 分别维护凭据代次及授权版本。父操作保留稳定子键；响应丢失、进程替换及安装 ACK 丢失均对账原记录。结果在安装前过期时，先显式按 expired 关闭，再建立关联后继。

消费方先持久安装 Grant 和凭据租约，再确认安装。自动与手动调用共用同一恢复轮次，并覆盖跨进程归属。缓存及新恢复的租约都在安装 ACK 回调之后再次核对，避免并发失效后返回旧凭据。已验证的 issuer 终态记录在自身权限域，不能与 Relay revision 混比或覆盖它。撤销与真实 HTTP Write/Flush 披露共用权威数据库围栏。

显式固定 pins 的本地 Relay profile 收到当前 Access 的认证拒绝后，通过原有效 Refresh 执行一次按原凭据比较的强制刷新。服务端在同一受保护事务轮换两个 token 及凭据代次。初次拒绝后重连；活动 socket 原位 reauth，不替换 owner。迟到失败提示保留首次写入线上时的凭据，不能使新租约失效。缺少 generation 或 authority 历史时拒绝。

双工客户端分离 reader、writer 和应用回调工作器。发送先取得 receipt future，不再持有加密锁等待回执往返。Relay READY 先于端到端 Hello/Ready；原 socket 内更新认证保留端到端会话，真实 socket 替换则先废止旧会话再重新握手。

写入可能成为不明结果之前，客户端持久记录原 message ID、密文、摘要、期限、history 和入队顺序。恢复查询原元组；同会话前序仍为 unknown 时，阻止后序越过它重发。原样密文重发还须满足原会话仍有效及原截止时间。传输受理不等于业务完成，Relay `absent` 不能授权业务重试。取消仅在 writer 还能原子跳过该写入时返回 `NotSent`，否则保留持久原元组并返回 `Unknown`。

Relay 在同一 PostgreSQL 事务保存受理和队列消息，保留不可变回执，并执行 owner、authority、credential 和 writer 代次围栏。同会话按受理顺序投递，避免后续回复越过前序。旧 envelope POST 和单向 connect 入口拒绝旧客户端。激活后数据库写保护拒绝实际基线程序；旧快照和不完整历史不能成为新的受理权限。

桌面恢复日志保留原业务 operation ID 和加密结果。浏览器控制、变更操作和语音恢复在重连后使用原身份。渲染层 action token 只有不透明 ID 和摘要，不含凭据或操作正文。原任务授权和业务截止时间继续生效。

录音文字插入及其应用标记在同一个本地 SQLite 事务提交。schema 12 新增不透明 recording ID、scope、conversation 和 draft revision，不保存音频或额外转写副本。草稿保存后、录音 ACK 前崩溃，恢复只能补 ACK；清空或发送草稿不会删除该标记。同进程磁盘失败只重试保存原草稿，不重复插入文字。

UI 保留原组件、菜单、布局和样式；能力不可用时保留入口并禁用操作，产品页面不增加常驻传输诊断信息。

10月19日实现将手动与自动浏览器连接合并到同一活动owner。10月20日后续取消手动浏览器授权按钮及renderer授权API：已验证的ISCP与HTTPS局域网桌面连接均自动建立和恢复宿主。重复连接通知保留有效宿主；停止、休眠或替换scope后的迟到结果不能覆盖当前宿主，未知写入仍需核对。

## 验收证据

验收全部使用合成身份、私有 profile、临时 PostgreSQL 数据库和受控邮件接收端，不使用日常桌面安装、真实账户或用户数据。

| 固定候选 | 证据 |
| --- | --- |
| SparkClaw `41b24a38`；ISCP `22da89f` | 独立全量 Go test、build、vet 通过；后续本地刷新及迟到提示修复另有 focused race 和真实 PostgreSQL 检查 |
| SparkClaw `62358707`；WebChat `3b4a0be1` | 桌面 465 项、WebChat 70 个文件共 360 项及前端严格构建通过 |
| SparkClaw `a438c44b` | 独立 SQLite/renderer 重启证明录音先保存、ACK 丢失后仅插入一次；事务回滚及旧 schema 检查通过 |
| 最终 ISCP `22da89f` / SparkClaw `41b24a38` 传输 | 独立复核关闭原元组取消、顺序、首写持久化、issuer/Relay 版本、真实 HTTP 披露、缺 domain 及迟到提示竞态 |
| 原生流式，SparkClaw `3b4a0be1` / 独立 ISCP `058e81b` | 20 次、2,000 批：观测 p95 151.09 ms / p99 168.16 ms，无缺失、重复、空白或节点重建；6,000 字可见自动跟随及选区/滚动联合场景通过 |
| 同一原生流式候选 | helper 中断后 2,233.8 ms 恢复，通过原 3 秒门槛；单次执行、无缺失或重复，在注入中断期间观测到一次空白 |
| 原生邮件，SparkClaw `00b48196` / ISCP `30707ae` | 原邮件 UI 的文件变化失效、桌面附件字节一致、丢回执对账不重发通过；10 MiB 用时 103,655 ms，通过原 180 秒门槛 |
| 原生授权，SparkClaw `77fbfb21` | 原设置删除、已签名结果保留、issuer/原生进程重启及 Relay 离线不回落检查通过 |
| 原生浏览器，SparkClaw `62358707` / ISCP `30707ae` | 七种截图模式全部通过，包含从未显示过的任务页面首拍；原手动/自动授权合并，读取/填入/点击经 ISCP，丢点击回执时只产生一次效果 |
| 原桌面组件 | 视觉矩阵 432 组、88 张截图，无记录的布局错误 |
| ISCP 延迟夹具 | 100 样本：发送至回执 p95/p99 14.20/17.32 ms；完整受理事务 8.21/14.33 ms；数据库 COMMIT 0.71/1.36 ms |

原生邮件场景仅使两个原有效 Access token 失效。两个原 Refresh 均自动将凭据 generation 从 1 恢复至 2，身份文件不变、授权 revision 仍为 1。真实 HTTP/client 测试另覆盖初始 closing、同 socket reauth、并发刷新、撤销及 authority 缺失。移走实际私钥文件后，各 enrollment 消费入口均失败，且不改动身份及 pending 证据；放回原密钥后恢复同一身份。

首张原生截图暴露了 Chromium 建立显示表面时的临时 `UnknownVizError`。最终实现仅在同一个五秒截图预算内重试这一有界只读截图，并在每次读取前检查租约。实际冷启动截图约 411 ms 恢复；七种模式均保持 view/window 可见性和焦点、释放 debugger，并返回当前草稿。此路径不会重试浏览器写操作。

[脱敏机器回执](../../docs/evidence/iscp-parallel-2026-10-19.json)将认证设计 16 项与传输设计 18 项验收逐项映射到测试和限制，并记录准确源码及证据摘要。流式计时使用原 DOM 与下一动画帧作为合成器绘制代理；主动阅读时处于屏外的正文不计作可见延迟。这些是受控本地测量，不代表生产网络或真实模型基准。

此前失败仍保留：FIFO 修复前约八秒恢复、恢复日志 checkpoint 成本过高、过期邮件后置检查，以及一版 Refresh 路径生成缺少 generation 的凭据。最后一种实验库继续拒绝并保留，未引入缺 generation 兼容例外。合成夹具的时限和旧 schema 构造已修正，未改变生产期限或放宽业务断言。

## 维护与发布边界

激活必须显式进入维护窗口，保留原身份、pins、pending 操作和数据。旧操作结果未知时阻止激活，不把它导入为新控制历史。helper 显式 `-upgrade-node-control-v2` 入口要求 `-config` 和 `-activation-evidence-sha256`，拒绝与 enroll、普通 check、control-only 模式混用。

发布清单须包含共享 Relay、五个 Gateway/issuer 节点、control peer、forwarder 及配套客户端。生产切换属于后续操作。新格式产生写入后，基线 HTTP/upsert 程序不是回退候选；没有已验证的兼容包时保持维护，保留当前数据库和恢复证据。

已有 Relay 域的切换分为两个显式阶段：先准备 incomplete 历史，再凭经审计的完整旧 key 清单及其摘要激活。旧数据库角色必须具备 `NOLOGIN`、没有写权限且没有活动连接。激活保留 `(domain_id, message_id)` key，不引入命名空间前缀。旧队列及回执保留在新投递/清理路径之外，历史已删除 key 保留永久 unknown 墓碑。缺少历史证据不能激活；全新、从未服务过的测试域不能证明任意已有生产数据库可以安全切换。

以下基线来自已有[部署记录](public-iscp-node-deployment.md)。后续部署前必须与实际服务及配套安装逐一核对；本地实现不宣称记录中的安装版本仍为当前版本。

| 组件 | 基线身份与必须覆盖的范围 |
| --- | --- |
| 共享 Relay | TEST1 / VM100；公网入口、持久 signer、PostgreSQL、五个白名单 authority 路由及独立 control forwarder |
| work1 / VM103 | `sparkclaw-vm103-aee06778-2010-470c-83e9-f83572831c1a`；Gateway、node issuer、issuer service peer、responder 及其 TEST1 control peer |
| work2 / VM104 | `sparkclaw-work2-431c00ce-b780-42e3-9d25-c25c87c8c011`；同样的完整组件组，保留现有 authority 目录 |
| work3 / VM105 | `sparkclaw-vm105-219ec0dc-4add-41b0-ab1e-7dd02997de59`；同样的完整组件组 |
| work4 / VM106 | `sparkclaw-vm106-6c3485b3-81ed-4414-a3c2-b6ce879e9561`；同样的完整组件组 |
| work5 / VM107 | `sparkclaw-vm107-c840a3e8-86d2-4fc4-8cb2-75341cd1e333`；同样的完整组件组 |
| 配套客户端 | 上述路由的全部 SparkX/helper profile 及其他已准入客户端，包括现有 Mac 安装；分别保留 installation、设备密钥、pins、原操作及数据。发现清单外客户端时阻止切换。 |

实际可执行入口如下：

1. 独立 ISCP `iscp-node-issuer`：启用 `control_v2`，设置 `control_writer_epoch`、`control_bind`、`control_tls_certificate_file`、`control_tls_key_file`。现有 `control_config_file` 标识已登记的 issuer 服务主体，HTTPS control handler 不等待 WebSocket READY。
2. 独立 ISCP `iscp-public-relay`：启用 `ISCP_CONTROL_V2`，设置 `ISCP_CONTROL_ROUTES_FILE`。每个固定路由包含 issuer `audience`、HTTPS `origin` 和 `tls_certificate_file`；Relay 自身 audience 仍在本地处理。路由器重启后废止未完成的 challenge 路由，不信任调用者指定的目的地。
3. 节点 authority：`control_issuer_actor` 必须是 `ServicePeers` 的既有成员。issuer agent、forwarder、responder 分别使用自身 operator 权限和持久归属恢复；恢复仅获得 `relay.transport`，不生成 workbench 业务 Grant。
4. 维护期间先显式执行 `-activate-control-writer`，再对原 invitation 历史执行 `-activate-control-history`。旧签发未终态时阻止激活。旧 initiating helper 使用上文升级命令；service/responder enrollment 须显式将同源 WebSocket path 从 `/v2/relay/connect` 改为 `/v2/relay/duplex`，保留身份、pins 和 binding。
5. 注册与既有管理撤销保留原职责；激活后的 renew、recover、lookup、ACK 使用独立 HTTPS。新 Relay 服务产物来自 ISCP 仓库；SparkClaw 消费方构建不引入新的 fork 模块，也不依赖相邻源码目录。

本实现记录不包含源码推送、安装包安装或服务部署。
