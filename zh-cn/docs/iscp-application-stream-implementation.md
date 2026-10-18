# ISCP 应用流实现

语言：[English](../../docs/iscp-application-stream-implementation.md) | 简体中文

日期：2026-10-18。本次从 `cd7d5ccc` 实现[内部 v3 设计](iscp-application-stream-design.md)，属于本地开发，不代表已安装发布或获得 ISCP 上游接受。开发及临时配置均属于独立的 `codex/iscp-application-stream-v3` worktree。InfiniCenter 0036 分别保留本地实施授权和仍为 proposed 的上游标准化状态；既有 JingSi、IMMS、App-CLI 契约不变。

## 实现

最终答案源只发布由受信工作流明确选择的公开纯文本/Markdown 增量。结构化 JSON 完整校验后才可见。进程内投影沿用结果的 8 MiB 上限，重放与恢复窗口为 30 秒。首尾批次立即刷新，中间批次按 50 毫秒或 4 KiB 刷新；不发布模型推理、工具参数或工作流中间文本。

通用外壳、控制及终止格式见 [application-stream.v1.schema.json](../../packages/protocol/application-stream.v1.schema.json)，SparkClaw 的不透明正文绑定见 [execution-stream.v1.schema.json](../../packages/protocol/execution-stream.v1.schema.json)。本地 `internal/appstream` 编解码器是候选扩展，不修改 ISCP Core；SDK 仍固定为 `v0.2.0-rc.1`。两层拒绝缺失/未知字段、非法 Unicode、重复键和非法整数；有状态校验进一步验证身份绑定、UTF-8 字节偏移和全文 SHA-256。首个终止水位不可覆盖，提前到达的终止响应不能吞掉缺失正文。

Endpoint 协商 `stream_schema_version=1` 及新操作 `execution.progress.stream`。每个流使用新的订阅 ID，同时保留原执行 request ID。新操作必须匹配 Gateway/helper/Electron 能力，不会静默退回进度轮询。取消只结束观察者；发送开流与等待首事件共用十秒截止时间，缺失尾部有两秒恢复窗口。流失败不重置无关普通 RPC。

授权快照从查询发起起最多有效五分钟，后台刷新，双端每次发布都做本地检查。会话、身份、修订、权限、Grant 和能力到期时间共同阻止旧回调发布。更换会话后，预览隐藏至新授权通过；已校验正文和游标可私下保留用于恢复。提交、审批、查询及 ACK 仍走原独立新鲜授权路径。私有 IPC 同时传递剩余有效期，以及相对 helper 收到调用时刻的截止时间。Electron 在发送调用前建立单调时钟起点，因此 stdout 排队不会延长授权；到期正文立即丢弃。

自管 Relay 在既有 HTTP/WebSocket 传输中持续接收，覆盖空队列和令牌轮换。该路径及 issuer forwarder 的人为速率、并发和累计请求门槛已移除；身份、授权、密文完整性、消息大小及普通连接/队列错误仍有效，没有引入资源调度器。

ClientStore schema 11 在同一交付事务中，将新交付的助手消息关联到原请求，保留已有消息 ID 和历史。预览及最终答案使用原 `MessageBubble`，共用 `execution:<request_id>` React key 和滚动锚点。共享组件新增可选 `scrollAnchorId`，`data-message-id` 保留真实消息 ID。未修改 CSS、菜单、布局或滚动策略。读取进行中合并通知，按发布版本拒绝迟到内容，同时避免持续增量导致显示饥饿。桌面体验分支负责滚动、焦点及集成验证。

## 验证

受影响 Go 包的 race 测试、构建和 vet 通过。隔离 Linux 容器中，Gateway 全部 73 个测试包通过，另有 11 个命令/支持包无测试。macOS 上四个既有顶层 Gateway 测试因缺少 `/dev/shm`，或旧断言将 `/private/tmp` 误判为私密信息泄漏而失败；已用共同基线的干净归档逐一复现。桌面测试 424/424 通过且无跳过，WebChat 326/326 通过，生产构建通过。

通用格式包含四个正例和十七个反例，执行正文包含六个正例和九个反例。独立 Draft 2020-12 JSON Schema 校验器验证结构；故意设置错误 UTF-8 偏移的用例由生产有状态校验器单独拒绝。异常测试覆盖先认证再推进重放序号、篡改密文、重复、尾部乱序、冲突终止响应、开流前及开流窗口之后取消、授权到期/撤销、旧会话迟到查询及签名授权修订变化。

原生验收使用新建 PostgreSQL 18 容器、生产持续 Relay 和加密 Endpoint、真实 helper 私有管道、生产 Electron 入口/ClientStore 以及原界面，并阻断桌面/Gateway 业务 HTTP。证据记录关联 ID、长度、摘要、时序和构建指纹，不包含答案正文或凭据。动画帧观察作为合成器绘制的近似，跨进程时钟误差单独测量，端到端分位数使用其保守上界。

[脱敏验收记录](../../docs/evidence/iscp-stream-2026-10-18.json)包含源码版本、二进制/模块摘要、原始收据及测试日志摘要，以及全部 23 次持久化交付。生产实现截至 `a43100bd`，附加的管道阻塞回归测试为 `c3a524d2`，显式启用的验收 fixture 为 `6962371b`。

| 原生用例 | 观测结果 | 范围 |
| --- | --- | --- |
| 20 个健康回答，每个 600 字，共 2,000 个源批次 | 首批最多 59.26 毫秒；P95 113.08 毫秒；P99 119.72 毫秒；最大增长间隔 166.40 毫秒；最终提交至交接最多 49.38 毫秒 | 单个活跃回答、本地加密链路、100 毫秒受控生产间隔；至少 98% 在完成前可见 |
| 6,000 字 / 18,000 字节长文 | 源、提交和界面正文摘要一致；99% 在完成前可见 | 不再受旧预览长度截断；测试脚本主动滚动 85 次保持尾部可见 |
| 显示 240 字后终止 helper | 1,328.20 毫秒内恢复原请求；一次提交、两次订阅、一次最终 ACK | 会话更换期间按要求隐藏一次；无重挂载、重复或丢失行 |
| work2 授权模型配置、生产 Agent | 730 个可见字符、63 次实时正文观测；工作流完成前出现正文；一次提交/订阅/最终 ACK | 真实模型、`mock=false`；提交至首显 6.98 秒，包含模型及工作流时间 |

所有健康用例均无空白、丢失、重复或重挂载回答行，全部非空观测的答案尾部均位于原视口。20 次及真实模型用例未由脚本滚动；长文用例不代表生产自动跟随通过。时序采用视口正文加下一动画帧的近似测量，没有独立分解每层延迟。真实 Agent 在隔离 Linux fixture 中运行以保留既有 `/dev/shm` 要求，调用 work2 配置的模型服务，使用真实 macOS helper/Electron 原界面。前后检查确认 work2 Gateway 容器、启动时间、配置、挂载及模型配置文件未变化。

20 次收据对应 `a43100bd` 的干净生产源码；随后真实模型、恢复及长文收据对应 `c3a524d2`，另记录 progress、projection、store、capability 和 auth 模块的独立摘要。测试 fixture 在测量时尚未纳入 Git，由驱动脚本及编译二进制摘要标识实际执行版本；最终 runner 增加显式 Linux 镜像参数和执行启动次数计数。更早的同会话 DOM 时序不用于视口结论。开发期第九样本重挂载、最初恢复 fixture 错误要求零隐藏、macOS 真实工作流缺少 `/dev/shm` 的失败均保留记录，健康用例阈值未放宽。

Linux 全包测试早于最后一次 helper 截止时间采样顺序调整；该调整由专项 helper race/vet 和管道阻塞回归检查覆盖。原生 fixture 必须显式启用，普通 `go test` 通过不代表已完成原生验收。

## 复现与集成

使用 Node 26、npm 11、Go 1.25.5 或更高兼容 Go 1.25；Gateway 验证前运行 `npm run setup:document-tools`。显式启用的原生 runner 需要 macOS、本地 Electron 依赖和 Docker PostgreSQL 18：

```sh
node apps/desktop/test/run-stream-native-qualification.mjs 20
node apps/desktop/test/run-stream-native-qualification.mjs 1 --long-text
node apps/desktop/test/run-stream-native-qualification.mjs 1 --recovery
SPARKCLAW_STREAM_FIXTURE_MODEL_CONFIG=/private/tmp/model-test.json \
  SPARKCLAW_STREAM_FIXTURE_LINUX_IMAGE=sparkclaw-linux-test-runtime:local \
  node apps/desktop/test/run-stream-native-qualification.mjs 1 --real-model
```

真实模型模式只将模型设置复制到临时配置，须使用明确授权的模型环境，不部署或修改该环境。需提供含仓库 Node/document-runtime 依赖的 Linux 测试镜像，私有模型配置及其引用的目录文件须位于同一私有目录。示例镜像标签是前置条件，runner 不会下载该镜像。生成的测试身份、私有配置及原始证据均在 Git 之外；runner 只清理带本次标签的临时 Gateway/数据库容器及其自身子进程。

集成桌面分支的共享组件时，同时保留 `data-scroll-anchor` 和 `data-message-id`，在合并后的源码运行该分支的滚动、文字选择与焦点矩阵。匹配发布须包含本次 Gateway、Relay/forwarder、helper 和 Electron 源码；schema 11 是真实存储迁移，不能假定旧二进制支持它。本次不包含合并 main、打包、安装、推送或现网部署。
