# 第三方连接器注册层重构方案

> 语言：[English](../../docs/connector-registry-refactor-plan.md) | 简体中文

截至 2026-07-15：本文档所述方案已在独立 worktree
`codex/third-party-integration-architecture` 中完成实现与验证。

## 目标

让第三方消息软件接入可以扩展，同时避免 Gateway、提醒投递和进程启动依赖
某一个具体产品。现有 Telegram 和微信能力继续使用相同的 HTTP、Store、Policy 和
Agent Runtime 路径。

这是进程内重构。SparkClaw 仍然是单个 Gateway 二进制，保留现有 package、API、
配置文件、状态后端、审批模型和连接器实现。

## 修改前基线

独立 worktree 从 `1578785` 开始，以下检查全部通过：

- `npm run setup:document-tools`；
- 在 `services/gateway` 执行 `go build ./...`、`go vet ./...` 和
  `go test ./...`；
- 在 `apps/webchat` 执行 `npm run build`；
- 执行 `npm run test:voice`，共 6 个测试。

WebChat 构建基线为 gzip 前 JavaScript 298.91 kB、CSS 35.20 kB。

## 当前耦合

目前增加一个消息连接器，需要同步修改多个互不相关的位置：

- `binding.NewRouter` 选择 Telegram 和微信 binding adapter；
- `notification.NewRouter` 只选择微信 outbound delivery，而 `main.go` 另行添加
  Telegram；
- Gateway 包含 Telegram 专用的 capability 和 activation URL 判断；
- `main.go` 分别启动 Telegram、微信后台任务，并单独连接一个 connector cancellation；
- 提醒投递另行创建 notification router。

现有领域契约已经有效：`binding.Adapter`、`notification.Adapter`、
`websearch.Adapter` 和 `speech.Transcriber`。问题在于重复构造和注册，不在于缺少一个
覆盖所有第三方能力的万能接口。

## 选定设计

新增 `internal/connector.Registry`，作为消息连接器的 composition 边界。

```text
provider 实现
  binding + notification + polling runtime + protocol delivery
                              |
                              v
                   connector.Registry.Register
                              |
       +----------------------+----------------------+
       |                      |                      |
 binding/notification connectorruntime.Runtime connectorruntime.AgentBridge
       |                      |                      |
 Gateway/reminders       process context       shared Agent path
```

每个 registration 只包含能力契约：

- 规范化后的 channel 名称；
- 支持 owner binding 时提供 binding adapter；
- 支持提醒外发时提供 notification adapter；
- 需要消费 inbound event 时提供 polling runtime；
- 需要停止活跃工作时提供 binding cancellation 函数。

Registry 读取现有 channel 配置，决定是否启用外发和后台工作。即使 operator 已关闭，
binding adapter 仍保留注册，使 Gateway 能区分 `connector_unavailable` 和
`operator_disabled`。

Registration 按 channel 建索引，拒绝空 channel 和重复 channel。Registry 不 import
Telegram、微信、对应协议或其专用配置字段。具体实现只在可执行程序 composition root
中构造，该位置是唯一允许知道哪个实现满足哪项能力的地方。

## 领域边界

不同语义继续使用不同接口：

| 能力 | 现有契约 | Registry 职责 |
|---|---|---|
| Owner binding | `binding.Adapter` | 注册并生成 Gateway router |
| 消息外发 | `notification.Adapter` | 注册并共享一个 router |
| Inbound 后台工作 | `connectorruntime.Runtime` | 在 server context 下运行 |
| 规范化 Agent 调用 | `connectorruntime.AgentBridge` | 共享幂等 Agent 调用 |
| 提醒目标 | `remindertarget.Resolver` | 使用统一 binding/session 字段解析，不按 provider 分支 |
| 搜索 | `websearch.Adapter` | 不修改 |
| 语音转文字 | `speech.Transcriber` | 不修改 |

`connectorruntime.Runtime` 表达共同的长生命周期形态：接收 provider event、归一化、调用
Agent bridge，再通过 provider 协议发送结果。Telegram 和微信继续保留各自的 polling
算法：Telegram 使用 durable inbox/offset 和 long polling，微信使用 cursor batch 与 CDN
media。公共 Runtime 契约不会削弱这些交付保证。

`connectorruntime.AgentBridge` 负责公共 Agent 调用和幂等路径选择。Provider dispatcher 继续
负责鉴权、命令、审批呈现、session identity、媒体解码、本地化文案和协议发送。Search
和 Speech 的消费端已经不依赖 provider；它们不负责账号绑定、消息轮询、提醒外发或
binding cancellation，因此与聊天连接器生命周期合并会形成语义薄弱的抽象。

## 核心修改

1. 新增 provider-neutral connector registry，并使用虚构 channel 名称编写定向测试。
2. 为 binding 和 notification package 新增基础 router constructor，使 Registry 可以
   在没有 provider switch 的情况下填充 adapter。
3. 把 binding 依赖就绪检查放到 binding adapter 契约之后；Gateway capability 计算不再
   判断 Telegram channel 名称。
4. 为 Gateway 增加 registry binding router 注入点。
5. 在 `cmd/sparkclaw` 中各注册一次 Telegram 和微信，并由 Registry 统一提供 Gateway
   binding、提醒投递、后台启动和 binding cancellation。
6. 新增共享 `connectorruntime.Runtime` lifecycle，让 Telegram Service 和 Weixin Syncer 实现
   同一契约，并删除 `main.go` 中按产品编写的启动 goroutine。
7. 让两个 dispatcher 都通过 `connectorruntime.AgentBridge` 执行普通和幂等 Agent 调用，同时
   把协议处理保留在 provider package。
8. 用状态和 capability 语义替换 Gateway 的 Telegram 特判：所有 channel 使用服务端
   计算的 startability，activation link 根据 binding 状态而不是产品名称隐藏。
9. 把提醒目标选择从 ToolHub 的产品分支移入 `remindertarget.Resolver`；delivery adapter
   继续执行协议专用的最终校验。

本轮可以保留 package 测试使用的兼容 constructor，但生产路径必须使用 Registry，不能
依赖其中的 provider switch。

## 行为不变量

本次重构必须保持：

- 可选连接器默认关闭；
- Telegram token 验证、加密凭据、激活、long polling、语音转写、提醒和撤销取消；
- 不同外部 Telegram 用户可以同时持有多个 Telegram Bot binding；每条 binding 分别保存
  加密 credential、activation challenge、cursor、inbox identity 和私聊鉴权边界；
- 微信 QR/manual binding、轮询、inbound dispatch、媒体、提醒和 notification 投递；
- WebChat 和 Telegram 共用同一个 Speech transcriber；
- Infinimesh Info 继续作为已配置但默认关闭的 `web.search` provider；
- 默认 file backend 及三种 Store 实现；
- HTTP route、审批规则、重试上限、worker 边界、轮询间隔和 shutdown cancellation；
- 对未就绪或关闭能力返回明确状态，不静默 fallback。

允许把 operator-enabled 检查一致地应用到所有已注册连接器，作为缺陷修正：operator 已
关闭的连接器不应启动新 binding 或后台 worker。任何可观察修正都会在最终报告中单列。

## 不在范围内

- 不实现 plugin loading、动态 Go module 或运行时代码下载。
- 不拆分服务或增加进程。
- 不替换 provider 专用协议 client。
- 不用无类型配置字典替换当前配置 schema。
- 除明确要求启用多个 Telegram binding 外，不新增 Telegram/微信协议、WebChat、搜索、
  Speech 或 TTS 功能。
- 不强行统一 cursor/offset 算法；在两个实现使用同一 durable inbox contract 之前，交付
  acknowledgement 仍由 provider runtime 负责。
- 不宣称已实现微信语音输入；该能力需要独立的 typed media 和 transcription 设计。

## 验证方式

定向检查：

- Registry duplicate/unknown/disabled/runtime/cancellation 测试；
- 两个 provider 共用的 Agent bridge 普通/幂等执行测试；
- 使用虚构 adapter readiness failure 的 binding capability 测试；
- Telegram 和微信通过注入 router 执行 Gateway binding 测试，其中 Telegram 在同一运行中
  创建两个不同 Bot credential；
- 通过共享 notification router 执行提醒投递测试；
- 使用虚构 channel 和多个 binding 执行提醒目标解析测试；
- `cmd/sparkclaw` 全功能及默认 file backend composition 测试。

最终检查：

- `gofmt` 和 `git diff --check`；
- Gateway `go build ./...`、`go vet ./...`、`go test ./...`；
- WebChat build 和语音测试，并比较 bundle 大小；
- `scripts/doctor.sh`；
- mock golden eval，既有验证漂移单独记录；
- 双语 Markdown mirror/link 检查；
- tracked diff 的敏感信息和生成物扫描。

## 完成标准

- 生产代码只注册每个消息连接器一次。
- Gateway、提醒和 lifecycle 消费 Registry 产物，不自行选择产品名称。
- Registry 核心测试不使用 Telegram 或微信标识。
- 默认 file backend 上的现有连接器能力保持全绿。
- 不在现有 Gateway 二进制之外增加新架构层。

## 回滚方式

本次只是代码装配重构。回滚时恢复原 router 构造和后台任务直接启动即可，不涉及持久化
schema、credential 格式、API migration 或数据重写。
