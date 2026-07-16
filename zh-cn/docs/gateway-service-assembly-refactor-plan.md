# Gateway 服务装配重构方案

> 语言：[English](../../docs/gateway-service-assembly-refactor-plan.md) | 简体中文

截至 2026-07-15：本文档中的 `gatewayServices` composition wrapper 继续保留，
其中按具体连接器编写的装配已由 provider-neutral 的
[连接器注册层重构方案](connector-registry-refactor-plan.md) 取代。

## 目标

把 `cmd/sparkclaw` 中可选功能的装配逻辑提取为同包内的 composition
抽象，让语音、连接器凭据、Telegram、微信、提醒和 Gateway options 只经过一条
生产装配路径完成连接。

本次修改必须保持所有外部可见行为和现有架构不变。不新增服务、包依赖、协议、
配置字段或运行时能力。

## 修改前基线

实施前以下检查全部通过：

- `npm run setup:document-tools`
- 在 `services/gateway` 执行 `go build ./...`
- 在 `services/gateway` 执行 `go vet ./...`
- 在 `services/gateway` 执行 `go test ./...`
- 在 `apps/webchat` 执行 `npm run build`
- 在 `apps/webchat` 执行 `npm run test:voice`，共 6 个测试

WebChat 生产构建基线为：JavaScript 298.91 kB、CSS 35.20 kB，均为 gzip 前大小。

## 问题说明

当前 composition root 直接在
[`main.go`](../../services/gateway/cmd/sparkclaw/main.go) 中装配相关可选功能：

- 为 Telegram 语音消息适配 speech；
- 创建并检查连接器凭据库；
- 由 Telegram service 向 Gateway 提供 binding cancellation；
- 按条件向提醒路由注册 Telegram notification adapter；
- 分别启动微信 syncer 与 reminder scheduler。

“全部可选功能”集成测试没有调用生产装配路径，而是再次手工连接凭据库、Telegram
和 Gateway。这里存在单一事实来源问题：新增或修改连接器时，需要人工同步生产启动
代码和测试启动代码，容易出现功能只在其中一条路径完成装配的情况。

这个问题属于可执行程序的 composition root。把连接器逻辑移入 Gateway、Agent
Runtime、Store 或新建共享包会扩大架构边界，但不能改善当前功能边界。

## 选定设计

在 `services/gateway/cmd/sparkclaw` 中新增同包 `gatewayServices` 装配对象。

```text
main 持有的依赖
  config + store + toolhub + agent runtime + traces + speech
                              |
                              v
                    newGatewayServices(...)
                              |
          +-------------------+-------------------+
          |                   |                   |
       Gateway HTTP        后台任务             连接器装配
                         reminders/weixin       Telegram/vault
```

构造函数只负责目前散落在 `main.go` 中的装配决策：

1. 读取 Telegram channel 配置。
2. 按现有 auto-create 规则创建 credential vault。
3. Telegram 启用时，保留凭据库未就绪只记录 warning 的现有行为。
4. 创建 Telegram dispatcher/service 和 speech adapter。
5. 使用相同的 speech、vault、binding-cancellation options 创建 Gateway。
6. 只在提醒功能启用时创建 reminder router，并按现有条件挂载 Telegram
   notification adapter。
7. 使用现有 dispatcher 和配置创建微信 syncer。

`gatewayServices.Start(ctx)` 在现有 server context 下启动相同的后台工作。启用条件
和执行间隔保持不变：

- 微信同步：立即执行一次，之后每 15 秒执行；
- 提醒投递：只受 `Tools.Reminders.Enabled` 控制，立即执行一次，之后每 10 秒执行；
- Telegram service：只在 Telegram channel 启用时启动。

该抽象保留在 `main` 包，因为它组合多个具体 internal package，不是可复用的领域契约。

## 文件范围

实现只涉及可执行程序装配相关的功能文件：

- `services/gateway/cmd/sparkclaw/main.go`
- `services/gateway/cmd/sparkclaw/bootstrap.go`（新增）
- `services/gateway/cmd/sparkclaw/main_test.go`

文档只新增本方案及英文镜像。不修改配置、API、前端、Store backend、连接器包或架构文档。

## 行为不变量

本次重构必须保持：

- 默认 file backend 正常启动；
- 可选连接器默认关闭；
- speech 初始化失败仍是致命错误；
- Telegram credential vault 未就绪仍只记录 warning；
- 撤销 Telegram binding 时仍会取消活跃 Telegram 工作；
- Telegram 提醒仍走绑定凭据的加密路径；
- Telegram 关闭时微信轮询仍继续工作；
- HTTP 路由、公开配置、ready 状态、状态码和响应 payload 不变；
- shutdown 仍通过现有 server context 取消后台工作；
- 所有轮询间隔、重试行为和 worker 上限不变。

## 测试策略

1. 修改 `TestAllOptionalFeaturesComposeWithFileBackend`，通过
   `newGatewayServices` 创建 Gateway，证明集成测试覆盖生产功能装配路径。
2. 保留 recording speech backend 注入，测试不依赖外部 ASR 服务。
3. 完成机械移动后先运行 `cmd/sparkclaw` 定向测试。
4. 对 Gateway 运行 `gofmt`、`go build ./...`、`go vet ./...` 和
   `go test ./...`。
5. 重新运行 WebChat build 和语音测试，确认仓库级基线不变。
6. 运行双语文档镜像/链接检查和 `git diff --check`。

## 未采用方案

- **新增跨包 connector interface：** Telegram 和微信的轮询、持久化与生命周期语义
  不同，公共接口会隐藏真实差异并改变包边界。
- **把连接器装配移入 Gateway：** Gateway 是 HTTP 层，不应负责进程启动和后台服务
  生命周期。
- **引入通用依赖注入容器：** 当前依赖图仍然明确且规模有限，容器会降低类型清晰度，
  但不能进一步降低功能风险。
- **同一轮重构 Store 或连接器内部：** 为保持行为不变并支持独立回滚，本轮不扩大范围。

## 完成标准

- 生产启动和“全部可选功能”集成测试使用同一个可选功能装配构造函数。
- `main.go` 只保留进程级职责：配置、基础依赖、HTTP server、signal 和 shutdown。
- 不新增违反既有 package dependency direction 的 import。
- 外部行为和配置零变化。
- 所有基线检查通过，且没有新增被跟踪的生成物或运行时产物。

## 回滚方式

改动仅位于 `main` 包。回滚时把 constructor 和 `Start` 调用重新内联到 `main.go`，并
恢复集成测试原有装配代码即可；不涉及持久化数据迁移或 API 迁移。
