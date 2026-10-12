# 公共 ISCP 节点部署执行记录

> 语言：简体中文 | [English](../../docs/public-iscp-node-deployment.md)
>
> 日期：2026-10-12。状态：服务端发布已有记录，桌面继续验证。当前
> Mac 安装/迁移及 B/C 业务/UI 尚未完成验收；修复后的隔离原生重启通过，
> 不能将本文理解为当前安装成功。

本文记录[公共 Relay/节点签发设计](public-iscp-relay-node-issuer-design.md)的执行。
[部署指南](deployment.md)继续负责通用环境说明。用户已授权本会话 Codex 盘点、
备份、部署和验证完整连接流程，保留 UI、数据及 LAN。规定锚点缺少
InfiniCenter，不声称有中枢 `accepted` 决策。

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
隔离桌面重试候选为 `946c2345`：复用原 30 秒 ISCP 协议预算，限制为一个
进行中的重试；LAN 仍为五秒，修复不改变 UI。包审计与 57 份源码、五份 UI
资源一致，ASAR SHA-256 为
`cdddbc116adcb0e8285d455109b261322d29d7c419e1d34a43dd3580b05fca73`，
helper SHA-256 为
`b87cd59bc6578ce9b1a7828e08a2cb4a46c1e4c9d54584034d93dc16a8ba6264`。
桌面测试 275 通过。这些候选检查不能证明当前 Mac 安装或原业务数据迁移已验收。

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

当前 Mac 的原工作台必须在**精确原 deployment/Owner/Client 与 installation
绑定**下继续可见。私有迁移适配器仅为显式配置、固定一次操作接受
`existing_client_id`，并验证既有 Gateway Client 活跃且属于原 Owner。它复用
该 Client，不重新标记 SQLite 行、不复制会话、不另建 Owner、不扩张 scope；
此例外不向任意公网凭据调用方开放。全新安装使用分别批准的 Client/设备。
最终原生迁移、选择状态、安全存储和数据一致性仍待验收。

## 5. 证据与剩余门禁

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
| `native-B.json`、`native-B-fixed-trace.json` 与截图 | 隔离原生初连、原 UI、自动续签、修复后冷启动通过；40 秒连接/readiness/config/Owner/email-sync/notifications/identity 稳定检查 | 当前 Mac 安装/迁移及 B/C 模型、Owner 创建/复制、撤销门禁仍在进行 |
| `server-restart-checkpoint.json` | 新配对备份；Relay → 签发 → 控制组件六秒恢复，signer/TLS 稳定，Gateway 容器/镜像未变且未中断 | 仅覆盖这些新服务，不代替整机/PVE 重启或原生原 profile 迁移 |
| `linux-final-receipt.json` | 支持平台 Gateway 全套 70 包通过、10 包无测试 | 外部显式启用和中枢 fixture 门禁单独声明 |
| `iscp-official-conformance.json` | 官方本机 P0 共 37 case、31 pass、0 fail、6 skip；SDK/conformance 包测试通过 | Runner 使用本机密码向量，不连接远端 endpoint；六项 service/CLI 此处未验收 |

完成前须验收 B/C 模型 request/result/ACK、原生 Owner 邀请创建/复制、单设备
撤销和既有安装迁移，保留原 UI/数据/LAN。后续候选保持源码/包/helper 摘要
配对。尚未执行的崩溃窗口或官方 service 向量继续明确列出；本机协议测试
不能证明托管服务对比或中枢 accepted。

## 6. 配对备份与回滚

| 层 | 私有检查点 |
|---|---|
| PVE | `/root/sparkclaw-public-iscp-pre-20261012-0822`；VM100/VM104 snapshot `iscp_pre_20261012_0822` |
| TEST1 | `/var/backups/iscp-relay/checkpoint-zzq2wlFM`；激活备份 `/root/sparkclaw-public-iscp-activate-20261012`；保留原 signer/数据库检查点 `/var/backups/iscp-relay/checkpoint-zmIZ9Lf4` |
| work2 | `/var/backups/sparkclaw-iscp-pre-20261012-0822`；已停止回滚容器 `sparkclaw-gateway-pre-node-20261012` |
| Mac | `/Users/dev/.cache/sparkclaw-public-iscp-8kr3dw8g/backups/mac`；安装前仍须另外建立当前应用/profile/数据的配对检查点 |

回滚容器设为 `restart=no`，避免主机重启后两个 Gateway 同时启动；活跃替换
容器为 `unless-stopped`。执行/Client 数据和配置必须配对，不能让两个实例
同时使用同一可写状态。备份可能含授权、token 和个人数据，仅操作员可访问，
配对保存存储/签名密钥，不将内容复制到 Git 或桌面凭据中。

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

当前服务端部署和验证不代表桌面门禁完成。发布后配对服务检查点已有记录；最终当前安装迁移及原生业务/UI 证据落盘
后，本文才能记录整轮发布已验收。
