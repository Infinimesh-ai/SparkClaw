# ISCP 认证恢复与全双工并行实现

[English](../../docs/iscp-parallel-implementation.md)

日期：2026-10-19。本文记录两个本地工作树的实现，不代表生产部署，也不表示基线二进制能读取新状态。最终集成验收仍在进行。

## 范围与接口冻结

用户选择先共同评审和冻结接口，再并行实现[连接认证与业务操作恢复设计](iscp-authentication-operation-recovery-design.md)和[全双工传输设计](iscp-duplex-websocket-design.md)。两个工作树均以 SparkClaw `cf9b81dec39b8872efc43650080bb8facaa58511` 为基线，共用设计快照 `76ed11bd94ab4bf4b7f63697a96dbf36d4fce053`。独立 ISCP 服务的基线为 `10fe37b3bd02a56b4f9d6cb8dc0333d7eec5356a`。

分支分别为 `codex/iscp-auth-operation-recovery` 和 `codex/iscp-duplex-websocket`，前者也承载集成候选。原检出目录及设计草稿保留。

InfiniCenter 0038、0041 已接受自管 fork 的共同接口。契约修订 `2026-10-19.2` 纳入已接受的 0042 Base64URL key-ID 正则修正，原快照另存归档。JingSi 的永久 `not_started` 围栏及 IMMS 契约保持，0040 继续撤回。ISCP 服务与 SparkClaw 消费方独立构建，通过冻结的线上协议互通。

## 实现行为

双工客户端分离 reader、writer 和应用回调工作器。发送先取得 receipt future，不再持有加密锁等待回执往返。Relay READY 先于端到端 Hello/Ready；原 socket 内更新认证保留端到端会话，真实 socket 替换则先废止旧会话再重新握手。

写入可能成为不明结果之前，客户端持久记录原 message ID、密文、摘要、期限、history 和入队顺序。恢复查询原元组；同会话前序仍为 unknown 时，阻止后序越过它重发。原样密文重发还须满足原会话仍有效及原截止时间。传输受理不等于业务完成，Relay `absent` 不能授权业务重试。取消仅在 writer 还能原子跳过该写入时返回 `NotSent`，否则保留持久原元组并返回 `Unknown`。

Relay 在同一 PostgreSQL 事务保存受理和队列消息，保留不可变回执，并执行 owner、authority、credential 和 writer 代次围栏。同会话按受理顺序投递，避免后续回复越过前序。旧 envelope POST 和单向 connect 入口拒绝旧客户端。激活后数据库写保护拒绝实际基线程序；旧快照和不完整历史不能成为新的受理权限。

桌面恢复日志保留原业务 operation ID 和加密结果。浏览器控制、变更操作和语音恢复在重连后使用原身份。渲染层 action token 只有不透明 ID 和摘要，不含凭据或操作正文。原任务授权和业务截止时间继续生效。

录音文字插入及其应用标记在同一个本地 SQLite 事务提交。schema 12 新增不透明 recording ID、scope、conversation 和 draft revision，不保存音频或额外转写副本。草稿保存后、录音 ACK 前崩溃，恢复只能补 ACK；清空或发送草稿不会删除该标记。同进程磁盘失败只重试保存原草稿，不重复插入文字。

UI 保留原组件、菜单、布局和样式；能力不可用时保留入口并禁用操作，产品页面不增加常驻传输诊断信息。

## 验收证据

验收全部使用合成身份、私有 profile、临时 PostgreSQL 数据库和受控邮件接收端，不使用日常桌面安装、真实账户或用户数据。

| 固定候选 | 证据 |
| --- | --- |
| SparkClaw `4e25b1ac` | 桌面 456 项测试通过；Linux 全量 Go 测试的 75 个有测试包通过 |
| SparkClaw `dfa0b651` | WebChat 70 个文件、360 项测试通过 |
| SparkClaw `a438c44b` | 独立 SQLite/renderer 重启证明草稿先保存、ACK 丢失后只插入一次；scope、事务回滚及旧 schema 测试通过 |
| ISCP `3cd6ea9`、`36a3117`、`058e81b` | 独立 race 检查覆盖原元组取消、unknown 前序顺序、已消费终态保留期和首写持久化 |
| ISCP `c0bb3fa` 与独立 SparkClaw 客户端 | 原生 helper 中断后 1,928.8 ms 恢复，通过原 3 秒门槛；单次执行，权威结果无缺失或重复 |
| 原桌面组件 | 原生交互检查通过；视觉矩阵 432 组、88 张截图无记录的布局错误 |
| 共用设计快照 | 新增本文前，精确 CI Markdown 链接及语言镜像检查通过，共 121 份项目文档 |

此前约八秒的原生恢复失败保留为证据；同会话 FIFO 修复通过原门槛。测试夹具失败也保留：邮箱 provider controller 漏接、过期的邮件窗口选择器、合成旧 schema 降级不完整，均已修正，未更改产品 UI 或放宽业务断言。

视觉矩阵本身不证明在线邮件、设置或授权功能。最终原生业务验收、实际 HTTPS 服务凭据恢复、数据库重启及历史边界、完整性能矩阵分别跟踪，直至通过。

## 维护与发布边界

激活必须显式进入维护窗口，保留原身份、pins、pending 操作和数据。旧操作结果未知时阻止激活，不把它导入为新控制历史。helper 显式 `-upgrade-node-control-v2` 入口要求 `-config` 和 `-activation-evidence-sha256`，拒绝与 enroll、普通 check、control-only 模式混用。

发布清单须包含共享 Relay、五个 Gateway/issuer 节点、control peer、forwarder 及配套客户端。生产切换属于后续操作。新格式产生写入后，基线 HTTP/upsert 程序不是回退候选；没有已验证的兼容包时保持维护，保留当前数据库和恢复证据。

本实现记录不包含源码推送、安装包安装或服务部署。
