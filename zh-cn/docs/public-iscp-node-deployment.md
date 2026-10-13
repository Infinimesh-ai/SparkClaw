# 公共 ISCP 节点部署执行记录

> 语言：简体中文 | [English](../../docs/public-iscp-node-deployment.md)
>
> 初次发布：2026-10-12。该次部署及要求的验收已完成。服务端与原生
> `16b00f17` 已部署，精确 Client 迁移、原数据/UI、默认 Main 冷启动、E 注册/
> 模型/服务端本机 ACK/撤销/冷启动拒绝及测试通过。迁移例外已移除，最终
> 配对检查点保留 Main 活跃、六条撤销完成；D 未确认验证请求保持、不回放。

本文记录[公共 Relay/节点签发设计](public-iscp-relay-node-issuer-design.md)的执行。
[部署指南](deployment.md)继续负责通用环境说明。用户已授权本会话 Codex 盘点、
备份、部署和验证完整连接流程，保留 UI、数据及 LAN。规定锚点缺少
InfiniCenter，不声称有中枢 `accepted` 决策。

2026-10-13 的四节点扩展及独立原生验收记录在第 7 节，不能用初次
发布结果代替新增节点的验收。

## 1. 已记录拓扑

| 主机 | 实际职责 | 开放范围 |
|---|---|---|
| PVE | `pve-iscp-443.service`，脚本 `/usr/local/sbin/pve-iscp-443` | 唯一新增公网应用映射为 TCP `443` 到 TEST1 `10.20.0.20:443`；窄范围回程/reflection 支持 work2 出站 |
| TEST1，VM100 | IP TLS/Nginx、Relay、PostgreSQL 18、独立控制转发 | [HTTPS 入口](https://210.16.177.239)、`wss://210.16.177.239/v2/relay/connect`；Relay/数据库/控制内部和管理入口保持私有 |
| work2，VM104 | 原 Gateway，加独立节点签发/出站代理 | 无新增虚拟机网络或公网应用 listener；签发管理使用 Unix socket，控制链主动出站至 TEST1 HTTPS/WSS |
| SparkX | 原 UI，加凭据/main/helper 适配 | 本连接只访问 TEST1 IP 入口；设备私钥及本机工作台数据保留在桌面 |

不使用域名。保留原 PVE `8006`、虚拟机 SSH `22020`/`22023`、业务路由与 LAN
能力；不增加 work2 LAN 发布或物理 LAN 测试。公网 `/v2/relay/admin/*` 与
`/metrics` 返回 403，端口开放不等于公开签名密钥或允许匿名注册。

domain 为 `infinimesh-public`，Relay ID 为 `relay-210-16-177-239`，已配置签发
路由为 `sparkclaw-work2-431c00ce-b780-42e3-9d25-c25c87c8c011`。路由由
操作员批准并有界，调用方不能选择上游 URL。

## 2. 运行服务与私有路径

下列路径记录此次发布，不是让客户端复制其中凭据。不能公开原始 authority/
配置 JSON、environment 内容、key 文件、数据库连接 secret 或含 token 的回执。

| 服务 | 二进制/运行时 | 配置与状态 |
|---|---|---|
| TEST1 `iscp-relay.service` | `/opt/iscp-relay/bin/iscp-relayd` | 环境 `/etc/iscp-relay/relay.env`；已批准权威配置 `/etc/iscp-relay/node-authority.json`；私有适配 socket `/var/lib/iscp-relay/managed.sock`；PostgreSQL 18 数据库 `iscprelay` |
| TEST1 signer/TLS | 持久 descriptor signer、IP 证书/Nginx | Signer `/var/lib/iscp-relay/signer.json`；公有证书 `/etc/iscp-relay/tls/relay.crt`；签名/TLS 私有材料受保护地保留在 TEST1 |
| TEST1 `iscp-control-forwarder.service` | `/opt/iscp-relay/bin/iscp-control-forwarder` | 私有配置 `/var/lib/iscp-relay/node/authority/forwarder.json`；broker 身份、凭据、Grant 和回执位于私有 node 根目录 |
| work2 `iscp-node-issuer.service` | `/opt/sparkclaw-iscp/bin/iscp-node-issuer` | 状态根目录 `/home/ubuntu/.local/share/sparkclaw/public-iscp-node`；配置 `authority/issuer.json`；私有管理 `issuer-socket/issuer.sock`；私有投影 `registry/peers.json`；操作/回执由 PostgreSQL 保存 |
| work2 Gateway | Docker `sparkclaw-gateway-1`，镜像 `sparkclaw-gateway:public-iscp-3a9c2535` | 原 base 为 `work2-mail-eac29b37`；原 146 个 environment、13 个 mount 及端口均保留，增加六个受限 node mount 和两个 node environment |

原 Gateway 的 Owner/Client store 继续作为业务授权权威。签发进程经其受限
本地管理 socket 创建、检查或撤销 Client，不增设第二份 Owner 数据库。一个
稳定 responder 接收 Relay 队列，依据私有注册表与实时 Gateway Client 检查
分发隔离的 peer/session。

已记录服务端源码为 `3a9c2535`。两端归档分别位于 `/opt/iscp-relay/source` 与
`/opt/sparkclaw-iscp/source`，文件名 `source-3a9c2535.tar.gz`，SHA-256 为
`01ae02012c1cd0c7d74675c79974401752a1edbdc63354a9cd3cb44d985fa212`。
当前原生安装 `/Applications/SparkX.app` 在 `16b00f17` 包含新鲜授权检查合并
及同 Grant no-op 修复。包审计覆盖 58 份源码和五份未改变的 UI 资源，ASAR SHA-256 为
`66336000981bc7fb7e8ea3cc26925224f001d8a22de085a8f86eb91d58e7e3ba`，
helper SHA-256 为
`3348c007fd8c49edbe04e2a47beb7ff158d0717324f1244324328ec4c9fb27ce`。
Desktop/WebChat 分别为 289/299 项通过，nodeclient/issuer/workbench/helper
race 已用隔离 PostgreSQL 验证通过。`16b00f17` 支持平台全套为 70 包通过、
10 包无测试，真实 PostgreSQL opt-in 检查另行通过。服务端二进制保留
`3a9c2535`：此客户端只为 initiator/
helper 构造，work2 responder 使用本机实时 Gateway 授权。Main 正常默认
冷启动、E 原生业务生命周期、服务端/本机 ACK 一致性及最终六条撤销检查点
均通过。

此前修复沿用 30 秒 ISCP 协议预算、一个在途身份重试，保留 LAN 五秒预算，
复用原设备 UI。

冷启动仍保留四个业务请求 slot。七项固定安全 GET 在本机写入前因容量拒绝
时等待，以 25–250 毫秒退避共享调用方原有 30 秒期限；这些读取收到已认证
`throttled` 时也仅在同一期限内最多重试三次。格式错误/不合条件的 429、503、
mutation 及 LAN 不使用此重试。视图取消读取不会使其余健康已认证传输失效；
真实 helper 故障/撤销仍为权威，身份/profile 变化取消等待。ISCP 身份读取为
30 秒，LAN 保留五秒。原设备 UI 的
`client-<48hex>` 签发 key 在原生加密存储中持久映射为一个 wire UUID 和输入
指纹，重启或结果丢失时回放同一签发回执，映射不含凭据。设备组件复用桌面
已经验证的当前 Client ID；没有该 ID 的调用方仍先验证身份再列设备，从而
避免冗余启动身份请求，保留原组件/UI。

私有原生诊断定位到客户端 mutex 下重复串行的签发状态往返：并发业务读取
及入站消息分别再排一次新鲜检查，耗尽保持原值的 30 秒读取预算。节点客户端
`8a48daac` 中的修复（组件 `65ee9fb0`）仅合并同一 Client 的重叠检查，共享
一次新鲜签名往返；不设置
活跃结果缓存或时间 TTL，后续不重叠调用使用新 operation/proof。各等待者
保留自己的期限、独立取消，并收到独立 scope 切片。Grant/恢复/删除/接入
修改及新观察到的签名撤销推进 generation 屏障，旧活跃结果不能越过；未知
持久生命周期请求继续保留精确 proof/receipt 和 pending kind。默认加密文件
与隔离 PostgreSQL race 测试覆盖并发、两个独立 peer、重启、取消、失败分类、
scope 不符及延迟活跃结果，workbench/helper race 和 vet 通过。修复后的 Main
冷启动和 Main/D 身份隔离通过。后续运行时交互还需一次修复：十秒轮询即使
授权没有变化也再次接受同一 Grant。`16b00f17`（组件 `ded02fdf`）只对克隆后
完整验证、与当前值字节完全相同的 Grant 识别 no-op，在客户端锁下再次验证；
真实变化仍保留 mutation 屏障。私有摘要只用于识别候选，不能代替新鲜授权
检查。真实 TLS 检查保持在途超过十秒，同 Grant 轮询不阻断原 30 秒预算，
绑定/签名/期限变化、调用方别名及已知撤销仍被拒绝。

D 经 Main 原界面撤销，运行中关闭、冷启动拒绝，Main 继续可用。D 请求
`8c735df2-2ca3-4ce7-97df-941e992a4006` 保持 `submission_pending`、原输入
摘要及显式提交屏障。八次只读服务端观察未发现已接受执行屏障，但仅凭缺少
记录不能清除客户端屏障；私有验证 profile 保留，不回放此请求，不声称 D
模型结果/ACK 成功。独立 E 随后完成真实请求
`8962d263-8e43-44a5-b54d-f4acd50e345e`：模型标记存在，result sequence 1
已投递，输入摘要 `885f0499f2f90682319c2c23b7b43ba57ede03930588931cf29782dfdabcf885`、
结果摘要 `f1f8dce4e1726214d2ec839acdb9d718dc0a849b22174ae131dffa38e34daa3c`，
本机 ACK 为 true。Main/E 身份保持独立，Main 原 UI 随后撤销 E，运行中关闭、
冷启动拒绝，Main 身份仍为 200。最终 Main 默认启动以正确等待方式再次通过
四项启动读取、九个设置、原 Client 和两条会话；E 关闭后 Main 所有表/文件
摘要仍未改变。最终服务端检查点在服务重启前后，确认 E 的两项摘要及持久
ACK sequence 1 与本机记录一致。Main 是本节点签发/验证登记中的唯一活跃
Client，其他既有 Gateway Clients 保留；六条验证撤销 tombstone 的 Relay
阶段均完成；D 无已接受服务端执行屏障，其本机未知
提交保持原样，不回放。

## 3. 信任用途与期限

| 材料 | 权威/位置 | 用途 |
|---|---|---|
| Relay descriptor signer | TEST1 稳定私有 signer | 签名 discovery 与 Relay 身份连续性 |
| 邀请 signer | work2 私钥，TEST1 配置其公有 verifier | Owner 批准的单次 v3 邀请及连接约束 |
| Grant/bundle signer | 独立 work2 私钥、分别固定身份 | 工作台授权和密封节点结果；TEST1 不持私钥 |
| Responder、控制 peer、每台 SparkX | 各自设备密钥 | 身份证明、session 和各自 Access/Refresh |
| 原生 bootstrap secret | 单次 Owner 凭据、加密待完成状态 | Provisioning 认证，不是设备私钥或 Relay admin token |
| Relay actor/admin token | 受保护的 TEST1 操作员/适配器边界 | 已批准的注册/恢复管理，不能交给 SparkX 或 work2 签发进程 |

TLS 根据 Owner 分发的 IP 公有证书验证，证书时间与 IP/hostname 校验保持
启用。已记录证书到期为中国时间 2027-01-10 11:49:21。替换信任需要经过
验证的身份连续性或新 Owner 凭据；证书续期是运维事项，不能以此关闭验证。

当前默认值：单次邀请 15 分钟，工作台 Grant **900 秒**，Relay Access **15 分钟**，
Refresh **24 小时**。长期业务授权为 `until_revoked`，各类 token 均不变为永久
凭据。读取实际签发期限元数据，不修改时间戳伪造有效期。

固定、操作员批准的控制设备对只具有 `sparkclaw.issuer.control.v1`。work2
签发七天控制 Grant，每五分钟检查，在剩余不足 24 小时前轮换。TEST1 应用
控制 Grant 入口检查固定 signer、精确设备对/权限/Relay、新鲜持有证明、
持久 nonce 回执、当前服务 peer 状态及单调修订/时间后再保存。服务 peer 的
冷启动与运行中凭据恢复使用独立验收的 TEST1 密封适配器，不能新增 peer 或
授予工作台访问。这些应用适配器不修改标准 ISCP Hello/Ready、信封、票据或 Grant。

## 4. 持久生命周期与数据连续性

新应用凭据前缀为 `sparkclaw-iscp-connect-v1.`，原 HTTPS 格式继续支持。
SparkX 在本机生成设备密钥，在网络修改前持久化同一 installation/operation，
与 work2 完成真正跨进程 X25519/带外 secret 握手和区分角色的 finished MAC。
TEST1 经受限 ISCP 控制 session 转发不透明 provisioning 消息，不持有 work2
Grant 私钥、bootstrap secret 或业务 session 明文。

签发方预留精确权威 Client/设备绑定，准备签名 ticket/Grant。TEST1 在一笔
数据库事务中提交票据消费、设备、凭据轮换链和 wrap 公钥绑定密封回执。丢
响应及进程重启对账/回放同一操作。签发方的通道、secret 和回执材料加密落盘；
同一操作下更换设备、密钥或输入时冲突，另一设备不能消费已使用邀请。

Refresh 事务提交后继凭据及密封回执，先持久保存再使用。Grant 续签及双过期
恢复保留原设备、Client、scope 和撤销修订，不消费另一份邀请。普通客户端
恢复要求 work2 当前授权确认；服务 peer 使用不同的已存操作员授权。未知
结果保留原待完成 request/proof/wrap key。客户端持久保存后才取得签名
`applied` 确认；未确认结果与撤销 tombstone 不因 TTL 到期而直接清除。

撤销先提交业务修订，拒绝新业务和续签，仅使该 peer session 失效，并持久
重试 Relay 撤销直至取得回执。本机删行或缓存 Grant 不等于传输撤销完成，
延迟结果不能恢复已撤销授权。
原界面撤销 C 后，签发修订为 2，Relay 阶段已完成；B 继续活跃，直至在原
界面另行删除自身授权。两者原生冷启动均拒绝授权，撤销后的 ClientStore IPC
保持锁定。停机后离线检查 SQLite，确认已投递请求
`ceba5796-dafe-4b10-b0fa-f74acdee9199` 及恰好一次投递 ACK 仍保留；这是数据
保留证据，不表示已撤销工作台可解锁访问。

当前 Mac 的原工作台已在**精确原 deployment/Owner/Client 与 installation
绑定**下继续可见。私有迁移适配器仅为显式配置、固定一次操作接受
`existing_client_id`，并验证既有 Gateway Client 活跃且属于原 Owner。它复用
该 Client，不重新标记 SQLite 行、不复制会话、不另建 Owner、不扩张 scope；
此例外不向任意公网凭据调用方开放。全新安装使用分别批准的 Client/设备。

Main 迁移保留 deployment `431c00ce-b780-42e3-9d25-c25c87c8c011`、Owner
`owner`、Client `sparkx-iscp-remote-8143225e` 和 installation
`f0671ef3-354b-4ec4-b5a4-f581c7604359`，新设备为
`sparkx-5f2a903f-060e-483e-bc0f-54f770dfde2b`。正常默认启动无需资格验证
环境覆盖，仍能看见原两条会话和九个设置分区。全部 14 个 SQLite 表及所有
owned files 与安装前摘要一致；回执另行区分 13 个 protected tables。
数量仍为两条会话、两条消息、一个任务、两份草稿、零文件；config、readiness、
Owner 和 Clients 读取均为 200。

第一份迁移邀请未使用即过期。停签发服务后，操作员确认其为 `issued`、revision
1、无设备、无操作及绑定操作，把加密行备份为
`/work2state/expired-unused-migration-row.encrypted.json`，仅移除此无用签发
行，既有 Gateway Client 和用户数据库均未改动。随后通过新批准的固定操作
`6ffddd0f-58ba-47b7-8e5d-d47ffe332fb3` 完成迁移。这是有记录的单次对账，
不是通用过期行清理政策。已记录的服务端检查点确认临时 `MigrationClients` map 已移除，
其余配置字段均未改变，普通新设备签发继续可用。最终 qualified 检查点记录
Main 活跃和六条验证撤销 tombstone 持久保留，Relay 阶段全部完成。

## 5. 证据与验收边界

操作员私有证据根目录：
`/Users/dev/.cache/sparkclaw-public-iscp-8kr3dw8g/evidence`。
公共文档只记录脱敏回执和摘要。

| 证据 | 已记录结果 | 边界 |
|---|---|---|
| `public-ingress.json` | PVE 外部 TLS、health/discovery 通过，仅新增应用 443；admin/metrics 403；work2 无新增入站 | 健康响应不能代替桌面生命周期验收 |
| `real-peer-A.json`、`real-peer-B-helper.json` | 实际公网 production Relay 注册/加密连接及访问 work2 identity/config | Helper 与完整原生激活分别验收 |
| `real-peer-A-refresh.json` | 两类 token 轮换、设备不变、旧 Refresh 401；观察到三个 rotated 和一个 active 轮换记录 | 原操作回放与负例另外使用隔离测试 |
| `real-peer-A-recovery.json` | Grant、Refresh 同时过期后恢复原设备 | 不重新授权已撤销设备，不改变 scope |
| `real-peer-A-revocation.json` | A 撤销后关闭，冷启动被拒；B 仍可用 | 仅主动撤销验证设备，没有发送真实邮件 |
| `native-B.json`、`native-B-fixed-trace.json` 与截图 | 隔离原生初连、原 UI、自动续签、修复后冷启动通过；40 秒连接/readiness/config/Owner/email-sync/notifications/identity 稳定检查 | 续签/冷启动证据发生于主动撤销验证设备之前 |
| `native-main-migration.json`、`native-main-cold.json`、`original-data-preserved.json` | 精确 Client 迁移及 `16b00f17` 正确等待的正常默认冷启动通过；九个设置、两条会话、设备列表、四项启动读取 200；E 关闭后表/文件摘要仍不变 | 实际既有安装/数据验收，不声称整机重启或物理 LAN 测试 |
| `final-normal-running.json` | 已安装 App/helper 无测试 flags/env 正常打开，实际连接公网 `210.16.177.239:443`，应用保持运行 | Mac 随后锁定，不声称新增 GUI 截图；相同 `16b00f17` 默认冷启动/UI 和四项 API 读取另有通过证据 |
| `real-execution-ack.json` | 真实模型请求 `ceba5796-dafe-4b10-b0fa-f74acdee9199` 投递 `PUBLIC-ISCP-OK`；服务端/本机输入与结果摘要匹配，本机 ACK 已记录 | 另一 QA 请求已投递但被既有 `semantic_coverage_low` 政策阻断，不表示每个模型请求都成功 |
| `native-public-lifecycle.json` 与原设置截图 | 通过：B/C 独立身份、原邀请创建/复制、重命名/计划取消、界面撤销 C、B 删除自身授权及两者原生冷启动拒绝；离线数据/一次已投递 ACK 保留 | 撤销后 ClientStore IPC 正确锁定；夹具起初期望撤销后读取，已改为停机离线保留检查 |
| `server-restart-checkpoint.json` | 新配对备份；Relay → 签发 → 控制组件六秒恢复，signer/TLS 稳定，Gateway 容器/镜像未变且未中断 | 仅覆盖这些新服务，不代替整机/PVE 重启或原生原 profile 迁移 |
| `server-final-activation-checkpoint.json` | 历史首次 final 激活检查点：配对备份、迁移 map 移除完成，原 Main 活跃/四条验证撤销完成；Relay/签发/控制恢复，signer/TLS 稳定，原 Gateway 继续健康；PVE 只读规则/持久化检查通过 | 仅停止/恢复新服务，不声称整机/PVE 已重启 |
| `server-final-qualified-checkpoint.json`、`server-final-qualified-integrity.json` | 最终 13:37:52.760–13:38:07.225 UTC 配对检查点：Main 活跃/六条撤销完成；E 输入/结果/持久 ACK 与本机一致；权威/执行 ledger/Gateway/TLS/signer/Nginx 不变，服务 active/enabled、公网控制恢复；归档/dump 目录、SHA/size、0700/0600 权限验证 | 仅新服务；D 未确认提交保持屏障，无已接受服务端执行 |
| `linux-final-receipt.json` | `16b00f17` 支持平台 Gateway 全套 70 包通过、10 包无测试，真实 PostgreSQL opt-in 检查通过 | 外部显式启用和中枢 fixture 门禁单独声明 |
| `node-authority-coalescing.json` 及 Go 测试回执 | 客户端/签发/workbench/helper 真实 PG race、等待/修改/撤销/peer/scope、同 Grant 超十秒 no-op 测试及 vet 通过 | 原生验收另有 E/Main 回执 |
| `native-final-peer-revocation.json`、`disposable-D-preserved-pending.json` | Main 原 UI 签发/复制、D 注册、独立身份、运行中撤销/冷启动拒绝通过；Main 活跃；D 未确认任务/输入摘要/提交屏障保持，不回放 | 不声称 D 模型结果/ACK；B 与 E 的真实投递分别有证据 |
| `native-final-E.json`、`final-E-local-result-ack.json` | 最新 helper：Main 原 UI 签发/复制、E 注册、Main/E 独立身份、真实模型标记/结果/服务端本机 ACK 一致性、原 UI 撤销 E/运行中关闭/冷启动拒绝通过；Main 可用 | 不表示每个模型请求都成功；B 较早证据与 D 未知提交仍分别记录 |
| `iscp-official-conformance.json` | 官方本机 P0 共 37 case、31 pass、0 fail、6 skip；SDK/conformance 包测试通过 | Runner 使用本机密码向量，不连接远端 endpoint；六项 service/CLI 此处未验收 |

要求的链路已用实际安装包、服务端/本机 E 输入/结果/ACK 与最终配对备份
验收。保留原 UI/数据/LAN，临时迁移例外已移除。尚未执行的崩溃窗口及官方
service 向量仍明确列出；不由这些测试推断物理 LAN、整机重启、托管服务
对比或中枢 accepted。

## 6. 配对备份与回滚

| 层 | 私有检查点 |
|---|---|
| PVE | `/root/sparkclaw-public-iscp-pre-20261012-0822`；VM100/VM104 snapshot `iscp_pre_20261012_0822` |
| TEST1 | Qualified 配对检查点 `/var/backups/iscp-relay/public-node-final-qualified-20261012`；此前 final `/var/backups/iscp-relay/public-node-final-20261012`；发布后 `/var/backups/iscp-relay/public-node-post-20261012`；`/var/backups/iscp-relay/checkpoint-zzq2wlFM`；激活 `/root/sparkclaw-public-iscp-activate-20261012`；保留原 signer/数据库 `/var/backups/iscp-relay/checkpoint-zmIZ9Lf4` |
| work2 | Qualified 配对检查点 `/var/backups/sparkclaw-public-node-final-qualified-20261012`；此前 final `/var/backups/sparkclaw-public-node-final-20261012`；发布后 `/var/backups/sparkclaw-public-node-post-20261012`；`/var/backups/sparkclaw-iscp-pre-20261012-0822`；已停止回滚容器 `sparkclaw-gateway-pre-node-20261012` |
| Mac | Qualified 应用/userdata/八份默认配置检查点 `/Users/dev/.cache/sparkclaw-public-iscp-8kr3dw8g/backups/final-qualified-current`；此前 `/Users/dev/.cache/sparkclaw-public-iscp-8kr3dw8g/backups/pre-install-current`；原 `/Users/dev/.cache/sparkclaw-public-iscp-8kr3dw8g/backups/mac`；保留旧应用 `/Applications/.SparkX-pre-public-iscp-20261012.app` |

回滚容器设为 `restart=no`，避免主机重启后两个 Gateway 同时启动；活跃替换
容器为 `unless-stopped`。执行/Client 数据和配置必须配对，不能让两个实例
同时使用同一可写状态。备份可能含授权、token 和个人数据，仅操作员可访问，
配对保存存储/签名密钥，不将内容复制到 Git 或桌面凭据中。
最终检查点确认备份目录权限为 `0700`、归档/dump 为 `0600`，签名/存储材料
与数据库快照配对保留。
Mac 原生私钥未导出，恢复配对文件需要原 Keychain；文件系统备份不能代替
设备私钥。

回滚按顺序进行，范围仅限本次发布：

1. 保留待完成/未知业务操作，再取当前配对检查点。改变权威或数据库前停止
   新接入/控制准入。
2. work2 停止新签发服务及活跃 Gateway，保留当前恢复状态，使用匹配的授权/
   执行 ledger 恢复已命名回滚容器和配置。保留原 environment、端口、LAN 及
   mount；排除另一实例后才恢复选定 Gateway 的 restart 策略，不能盲目用旧
   ledger 覆盖更新的业务副作用。
3. TEST1 停止本次 Relay/控制组件，配对恢复二进制、数据库、descriptor signer、
   TLS 及权威配置；重开准入前复核稳定 discovery、原路由与管理入口。
4. PVE 只撤销已命名的本次规则/unit 或恢复其记录配置，保留 `8006`、两个 SSH
   映射及其他转发。
5. SparkX 配对恢复应用、启动选择、私有 profile、原生凭据状态、SQLite 与已有
   文件。新链路原生验收前保留原本机 Relay/issuer/profile。重连前检查既有
   后端撤销和未知结果；恢复文件不能复活撤销授权，不能授权重发不确定任务/邮件。

本次部署及要求的验收已完成，服务端/原生 manifest、既有安装数据/UI 证据和
qualified 最终备份均配对。D 未确认验证提交保持原样；完成不等于授权回放，
也不允许用旧 ledger 覆盖更新的副作用/撤销。

## 7. 新增四个独立节点，2026-10-13

用户选择 TEST1 公共 Relay、每个节点独立签发。发布前已提交并推送代码。
TEST1 Relay、五条控制链及五个签发服务现使用 `ae56b642`，原 work2 Gateway
镜像与 Mac `16b00f17` 安装包保持不变。四台新 Gateway 使用干净的
`git archive 7d5838c2` 发布镜像。宿主浏览器、四个独立原生注册、真实模型投递及
自然 Grant 续签均通过；四台最终原生界面与配对检查点验证通过。离线撤销后的
状态提示限制单独记录于下文。

| VM / 名称 | 内网地址 | 原 SSH 端口 | 权威路由后缀 / deployment |
|---|---|---|---|
| 103 / work1 | `10.20.0.22` | `22022` | `aee06778-2010-470c-83e9-f83572831c1a` |
| 105 / work3 | `10.20.0.24` | `22024` | `219ec0dc-4add-41b0-ab1e-7dd02997de59` |
| 106 / work4 | `10.20.0.25` | `22025` | `6c3485b3-81ed-4414-a3c2-b6ce879e9561` |
| 107 / work5 | `10.20.0.26` | `22026` | `c840a3e8-86d2-4fc4-8cb2-75341cd1e333` |

路由为 `sparkclaw-vm<VM>-<deployment>`。各 VM 本机生成独立的邀请、Grant、
responder、出站控制私钥，只分发公钥 pin 及该节点自己的 Relay 凭据。
TEST1 保留 operator token 和 `/var/lib/iscp-relay/nodes/vm<VM>` 下四份独立
broker 私钥，不接收签发私钥。原 work2 路由和密钥继续注册。新节点分别使用
`/home/ubuntu/.local/share/sparkclaw/public-iscp-node`、
`iscp-node-issuer.service`、独立 PostgreSQL 库/角色及私有 Unix IPC。

Relay schema-2 配置按完整 domain/Relay/signer/responder 绑定、带命名空间
的回执/操作及设备归属隔离权威。只接受五条批准路由，拒绝重复 JSON 字段及
调用者选择的上游 URL。每条路由独享 32 个请求槽和每分钟 600 次预算。五节点
原生测试发现共享来源的 Relay 请求预算返回 HTTP 429，故在原有有界配置范围内
将部署的每 IP 上限从每分钟 1200 提升到 3600；每路由预算和认证要求保持不变。
可选 `SPARKCLAW_WORKBENCH_ISCP_NODE_ROOT` 设置让普通 remote
重启保留六个受限 Gateway 挂载。Gateway 获得 responder 密钥、公钥 pin、registry、
私有 issuer socket 及其签发管理 token；邀请/Grant 签名私钥、issuer 存储/数据库
秘密及 Relay operator token 保留在 Gateway 外。

旧数据迁移已用 TEST1 实际数据库的隔离恢复副本演练。原九张表行数与内容
校验值保持一致，再次启动幂等，原 Access/Refresh 值保留。历史未知 operator
设备保持未归属，不分配给新租户。正式迁移及十二个服务身份注册通过；原 Main
恢复连接，两条对话和原界面保留。

控制端通过新鲜、验证签名的双向 ISCP Hello 恢复签发方或 broker 单独重启。
私有持久水位拒绝旧会话替换；待定操作保留原 ID，不把未知修改作为新请求
重放。带无效内层 frame 的公网请求在 broker 重启前后均到达五个签发方；
四个新 issuer 逐个重启均在三秒内恢复。这些拒绝探针只证明控制通道可达，
不代替客户端注册验收。真实 PostgreSQL race 测试覆盖权威隔离、迁移、续签、
重放、撤销及会话替换。公网 health/discovery 为 200，未知路由及公网 admin、
metrics 仍以 403 拒绝。

本次扩展不新增公网端口或 PVE 规则，原出站 NAT 和 443 reflection 已支持四台
访问 TEST1。原在线模型 URL、模型名称和凭据保存为私有 remote 覆盖项。
103/105/107 的 ASR 根地址修正了已有的 `/v1/v1/audio/transcriptions` 重复路径，
原环境文件保留备份。保留 LAN 配置及无关服务/端口映射。

四种发布镜像由已提交的跟踪源码构建为 Linux/AMD64，Gateway 内容 ID 为
`sha256:4350dd1c408e27253aef42091889ef0be37db7c9b4d999971c31d1d3548f006b`。
标准 `start:remote -- --no-build` 仍校验私有模型 profile、凭据、节点挂载、宿主
浏览器就绪及容器内 Controller smoke。旧 Gateway 镜像保留回退标签。其他三种
旧服务镜像虽有运行容器，父层内容已缺失；替换前已私有导出合并文件系统和完整
inspect，并导入回退镜像。原 PVE/数据库检查点仍是完整回滚的主要来源。

固定版本 Chrome for Testing 使用其真实 managed-policy 目录及精确、root 所有的
AppArmor 可执行路径例外。全局非特权 userns 限制保持开启，实际 renderer 保留
seccomp、NoNewPrivs 与独立 user namespace。四个 managed userscript 和 native
provisioning hash 均验证通过。VM106 重复 setup 复用逐字节校验的组件，worker URL/
policy hash 不变。VM103/105 浏览器迁移先冷备份、再隔离候选验证，原 history/cookie
行及旧 profile 均保留。

103/105 的 37 张原表共 664/525 条旧记录，其原 ID 和仍适用列的校验值全部保持。
既有迁移退役 `model_calls.fallback`，41/31 条旧值均为 false，保存在私有备份证据中；
不声称现库仍保留该列。106/107 保留原 Owner 及原本为空的消息/运行历史；新增
smoke 与测试记录单独计数。原环境文件及凭据加密 key 的校验值保持不变。

四个正式原生 profile 分别取得独立 deployment/Owner/Client 身份，完成真实模型
标记请求的持久投递，并在不改时钟或已签名 Grant 的情况下自然续签 900 秒 Grant。
最初部分合成提示被语义路由拒绝并已投递，该结果保留；另一个已完成测试证明真实
模型回复。未知提交只按原 request ID 查询，不盲目重发。一次性 QA profile 均通过
已安装 helper 注册及续签；仅将其本地 Access 到期元数据提前，两种真实 token 均轮换，
旧 Refresh 返回 401。四个 QA 随后达到 issuer revision 2、revocation-complete，最新
Refresh 也返回 401，正式身份保持 active。已离线撤销的 profile 冷启动暂显示“连接
不可用”，需获取权威撤销状态后才显示明确撤销提示。冷启动拒绝验收单独记录此 UI
限制、服务端撤销及业务访问拒绝，不声称立即进入明确的终态锁定提示。

独立启动器位于 `/Users/dev/Applications/SparkX Remote`：
`SparkX-work1-VM103.command`、`SparkX-work3-VM105.command`、
`SparkX-work4-VM106.command`、`SparkX-work5-VM107.command`。专用数据位于
`/Users/dev/Library/Application Support/SparkX Remote Profiles/VM<VM>/userdata`。
逐个实际启动器检查后，原侧栏、输入框、文件/邮件/浏览器入口及九项设置均保留。
各在线设备列表显示正式身份 active、一次性 QA 身份 revoked，原测试会话中均可见
已完成的模型标记回复。原 Main 默认选择不变，两条原对话、九项中文设置重新打开，
在线设备列表仍显示原身份有效；14 张业务表和已有文件的校验值不变，并留在原
工作台供继续使用。恢复私有 profile 的设备私钥仍依赖原 macOS Keychain。本次未
新增验证实际 LAN、整机重启、真实 ASR 转写及邮件发送。

私有证据根目录：
`/Users/dev/.cache/sparkclaw-four-remote-20261013-z7mvhnm8`。
PVE 快照：103/105/106/107 的 `sparkclaw_remote_pre_20261013`，TEST1 的
`sparkclaw_multi_node_pre_20261013`。四台文件、数据库及冷浏览器检查点位于
`/var/backups/sparkclaw-remote-pre-20261013`。TEST1 配对激活检查点为
`/var/backups/iscp-relay/multi-node-activation-pre-20261013`，work2 issuer
检查点为 `/var/backups/sparkclaw-iscp/multi-node-pre-20261013`。Mac 原 profile/
默认选择检查点为证据根下的 `backups/mac-before-four-node`。注册/续签/撤销后的最终
检查点位于各新 VM 的 `/var/backups/sparkclaw-iscp/four-node-final-20261013` 及 TEST1
的 `/var/backups/iscp-relay/four-node-final-20261013`。每节点六件、TEST1 三件归档/
dump 均有 hash 与成功恢复运行回执；TEST1 包含五个 broker 角色及 systemd 写路径
配置。Mac 正式 profile 冷备份为 `backups/mac-four-node-final`，设备密钥仍依赖原
Keychain。秘密归档不提交到 Git。
回滚 Relay 必须配对数据库、权威配置和二进制，不能仅降级二进制来撤销归属迁移。
考虑恢复前须保留更新的撤销记录及未知业务结果。
