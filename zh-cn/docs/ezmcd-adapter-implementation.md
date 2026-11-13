# EzMcDonalds 分端接入实现

> Language: 简体中文 | [English](../../docs/ezmcd-adapter-implementation.md)

日期：2026-11-13。**本 worktree 已实现 P0–P4 的代码，真实产品发布验收尚未完成，三个启用开关均默认关闭。** 本轮实现没有新增真实订单或付款。参见[设计文档](ezmcd-split-integration-design.md)和[实现证据](../../docs/evidence/ezmcd-product-implementation-2026-11-13.json)。

## 交接和真实证据

引用的上游交接对话完成了一笔明确授权的 ¥14.90 `ARCHCARD` 订单，该授权已经消耗。原账号、操作键和交易账本仍是事实依据；这笔订单不代表 SparkClaw 新工作流通过付款验收，也不授权再买一餐。公开交接修复以校验和固定的补丁叠加在上游 `2497334e4ff1b2331773a36890db74136b288537`（0.1.0a50，MIT）上，适配器版本为 `sparkclaw-mcd-handoff-v3`。打补丁后的上游测试 787 项通过、2 项跳过。

[11 月 13 日 G1](../../docs/evidence/ezmcd-handoff-g1-2026-11-13.json)核对已安装的 7.0.451 二进制，完成真实 v4 会员和 v5 订单列表只读请求，确认账号文件、原账本、观测时间均未变化。此前当前版本的 v5 阻塞已解除，历史失败报告保持原样；没有重启采样器或延长观测有效期。

[11 月 12 日指定取餐 G2](../../docs/evidence/ezmcd-g2-selected-handoff-2026-11-12.json)保留为真实 work2 → 全新 Mac Store 的历史证据：门店 `3330613`、`locker-in`、19:30，完整报价与取餐回执跨端保存，购买摘要一致，原始 120 秒有效期不变，重复导入及五类拒绝检查通过，Mac 持久化未发送前撤销。普通 `eat-in` 路径尚未验收。更早 18:30 的 UNKNOWN 切换记录仍保留在原账本，只追加独立的过期意图退休回执，没有重试或改写为成功。

[会话基础验证](../../docs/evidence/ezmcd-session-foundation-2026-11-12.json)通过认证 SSH 验证 work2 加密持久化、新进程读取、续租和撤销，与产品 ISCP 验收分别记录。[初始失败](../../docs/evidence/ezmcd-p0-qualification-2026-11-12.json)和[凭据可移植性证据](../../docs/evidence/ezmcd-credential-portability-2026-11-12.json)继续保留。

## 已实现职责

| 范围 | 实现与边界 |
|---|---|
| P0 helper | [Python 私有适配器](../../tools/ezmcd-adapter/src/sparkclaw_mcd)、固定官方端点、显式构建支持表、跨语言摘要向量、固定上游及依赖、MIT 声明；不启动公共 MCP／HTTP 服务。 |
| P1 本机会话 | [桌面会话模块](../../apps/desktop/src/main/mcd/session.mjs)导入已验证的原账号。系统加密存储保留完整本机账号，认证连接只传最小 v4 材料；发送前持久化 provision／revoke 原请求。续租不刷新官方账号或观测时间。 |
| P1 Gateway | [mcdintegration](../../services/gateway/internal/mcdintegration)使用 Vault CAS、独立加密材料／控制记录及永久锚点、撤销墓碑、过期清理、作用域校验和执行总预算；延迟响应不能恢复已撤销的访问。 |
| P1／P2 产品路由 | 3 个正向会话操作和 6 个反向本机操作完成 ISCP 注册、独立协商和显式资格控制。敏感消息绕开通用回执日志和对象体落盘；无 HTTP／MCP 回退。 |
| P2 购物车／报价 | [mcddining](../../services/gateway/internal/mcddining)管理有界临时 Store、模型可见投影、购物车串行操作、发送前持久化围栏、完整报价传输与 Mac prepare／abort 回执；临时内容丢失不允许重放。 |
| P3 钱包执行 | [purchase.py](../../tools/ezmcd-adapter/src/sparkclaw_mcd/purchase.py)在原 Mac 账本上复用上游服务及预算／预留／写入逻辑。[本机 host](../../apps/desktop/src/main/mcd/host.mjs)独立核对本机保存的不可修改审批；仅支持 `ARCHCARD`。 |
| 工作流 | 只读、购物车、报价、购买、原操作查询五个叶子复用能力发现、Policy 和审批界面。Runtime 将购买参数绑定到完整报价审阅内容；模型不能选账号、传凭据、换账本或自行授权付款。 |
| P4 界面／运行时 | 在原「连接」目录接入 native 适配器，保留导航和其他服务，能力不可用时禁用操作。独立 Python 运行时构建、源代码契约、本机 manifest 固定、包审计和运维配置已实现。 |

[工具清单](../../tools/ezmcd-adapter/src/sparkclaw_mcd/tool-inventory.json)覆盖固定上游的全部 38 个工具。helper 列出已实现操作不代表产品取得资格；selection 不具备下单、刷新账号或钱包 token 操作。本机 host 只能使用对应作用域的准备记录和原始持久化审批购买。

## 交易与恢复规则

私有协议每次通过 stdin 接收一个有界 JSON 请求，凭据不进入 argv、环境变量、模型结果或通用日志。单次 helper 最长 90 秒；执行保留原始最长 15 分钟截止时间，且不得超过最长 24 小时的会话租约。持久化总预算为只读 64 次、带购物车权限 128 次；每个请求还预留有界子预算。续租不能延长原执行或重置计数。

`mcd-json-v1` 使用排序 ASCII 键、UTF-8、安全整数、显式 null 和有序数组，拒绝重复键、浮点、非法编码及超限深度／大小。官方响应中的小数元数据保留为有界原始 JSON 字符串，完整纳入摘要。金额使用整数分。报价绑定 deployment、owner、client、installation、账号／会员、凭据版本、execution、purchase ID、epoch、审阅内容和完整取餐回执。传输最多 256 KiB、每块 8 KiB，不延长原始 120 秒有效期，保留上游报价指纹及取餐复核。

购物车发送前先持久化会员级控制记录。UNKNOWN 写入只能对账，不能以新操作键重试。过期取餐意图只能在原取餐时间过去后显式退休，保留原 UNKNOWN 结果并禁止重放旧键。若临时响应内容丢失，仍可能需要官方 App 核对或显式退休，不宣称所有购物车结果都能自动恢复。

准备操作只在明确为空的资格测试 Store 初始化，或在本机锚点落盘后向原 Mac 账本添加适配器控制表。已建立账本缺失、损坏、权限不符、锚点不一致或旧 schema 不完整时关闭能力，不自动创建空账本、修复或迁移原型。原 attempts、writes、日预算在接入和重连后继续保留。

购买审批绑定 purchase digest、金额、币种、钱包通道、门店编号／名称、取餐时间／方式、完整商品、优惠名称、凭据版本。Gateway Policy 与原 Mac ClientStore 都必须持有审批。本机在调用上游前预留命令与授权，首次上游写入认领与授权消费保持同一事务。原会员锁和消费限制继续生效。重复键返回原尝试，不再次调用 submit 或 `pay_existing`；响应丢失、进程终止和不确定结果只能查询。只有已核验的终态 `PAID`／`CANCELLED` 才解除后端购买冻结。

报价可在原执行时间及预算内最多刷新两次。Mac 先持久化未发送 epoch 的 `quote_refresh` 撤销；指定取餐需重新快照并获得新切换回执。复用审批要求包括 purchase digest 在内的全部审阅字段完全一致，降价也需要新审批；变化或过期的授权不会付款。

断开连接先停止正在运行的本机 helper，再移除本机秘密并记录撤销请求。离线撤销保持待处理，在重连之前发送。同账号重连保留账号身份和既有账本锚点，使用新连接 ID。登出、锁定和授权失效停止本机执行。撤销后可读取原回执和本机历史，但不恢复选餐／购买权限，本机历史也不能冒充最新官方核验。

## 构建和运维配置

使用 Node 26 和 Python **3.11.15**，显式开发构建，不在运行时安装依赖：

```bash
npm run setup:ezmcd-adapter -- --python python3.11
npm run test:ezmcd-adapter
node --test apps/desktop/test/mcd-python-host.test.mjs
npm run check:ezmcd-contract
```

构建校验 [upstream.lock.json](../../tools/ezmcd-adapter/upstream.lock.json)、归档、公开补丁和精确依赖版本；`--archive` 可指定已下载且校验和一致的归档。不覆盖现有构建：保留旧目录并使用新输出，或移动旧开发构建后重建。开发 venv 不等于发布运行时。

macOS arm64 使用匹配的独立 Python 前缀构建：

```bash
SPARKCLAW_MCD_BUILD_PYTHON=/path/to/python3.11 npm run build:ezmcd-runtime
npm run check:ezmcd-runtime
```

构建器记录全部文件哈希、源码／依赖契约和 Python 基础元数据，移动目录后检查模块导入，再更新随客户端发布的 Mac manifest 固定值。运行时位于 ASAR 外，包审计核对固定值与完整文件集。已在 `apps/desktop/dist/` 成功构建未签名 macOS arm64 DMG／ZIP，哈希和包审计写入实现证据，尚未安装；生成物不进入 Git。未知架构／构建、文件变化、运行时缺失或契约不一致会禁用能力或令包校验失败。

Linux x86_64 在目标平台运行 `scripts/package-ezmcd-runtime.py --build … --runtime-prefix … --output …`。将完整 bundle 安装到稳定目录，在 Gateway 中固定 manifest：

```json
{
  "mcd_selection": {
    "enabled": false,
    "cart_enabled": false,
    "purchase_enabled": false,
    "helper_python": "/absolute/installed/ezmcd/bin/python3",
    "runtime_manifest_sha256": "<已安装 manifest.json 的 SHA-256>"
  }
}
```

只对分别完成验收、匹配且已认证的 Mac／work2 开启相应能力。会话要求 `mcd.session.provision`、`mcd.session.revoke`、`mcd.session.status`；本机操作另行协商 `mcd.account.ensure`、`mcd.checkout.prepare`、`mcd.checkout.commit`、`mcd.checkout.abort`、`mcd.operation.status`、`mcd.orders.query`。购买还要求现有审批决定能力；配置开关不绕过资格或用户审批。

在原连接页选择已验证的本机账号文件，不向聊天或 renderer 设置粘贴凭据。原账号源和购买账本留在 Mac，后端不接收完整账号／v5 runtime 或交易数据库副本。官方会话过期或变化时，先完成授权的官方 App 验证，再导入新的已验证材料；没有后台登录采样器或 refresh-token 流程。

回滚关闭新操作，但保留加密控制锚点、未解决写入认领、原账本、回执和撤销墓碑，不通过删除这些记录解除不可用状态。work2 验收时根盘低于 4 GiB 保留空间，构建均放在 `/tmp`；临时验证目录不代表生产安装。

## 验证与剩余发布门槛

[机器可读证据](../../docs/evidence/ezmcd-product-implementation-2026-11-13.json)记录命令、数量、manifest 和局限。覆盖 Python 账本／授权／崩溃／重复操作契约、跨语言规范化、本机到真实 Python 的准备与模拟付款／重启、加密 ISCP 夹具协商及恢复、Go race、桌面与前端回归、源码／运行时契约，以及原设置导航的可视检查。付款夹具替换外部 provider，无法访问官方服务。

macOS Go 全量运行保留三个既有 `/dev/shm` 相关 gateway 失败；对应 Linux gateway 套件在 work2 运行。Node 26 前端测试使用 `NODE_OPTIONS=--no-experimental-webstorage`，由 jsdom 管理浏览器存储。初次前端失败、桌面计时失败及修正后的通过结果均保留，不改写失败证据。

仍未取得资格：已安装产品 ISCP 的真实断连／重启／丢回执矩阵；普通报价及更广商品／自选套餐／优惠方法；新账号获取及跨天刷新；完整桌面安装包；以及单独授权、通过新工作流完成的真实付款。这些是发布门槛，不构成复用交接已付款订单的权限。具体新验收订单获明确授权前，付款保持禁用。本轮 worktree 实现无需验证码或用户额外操作。
