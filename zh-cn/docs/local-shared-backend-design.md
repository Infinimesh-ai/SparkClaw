# Web 与桌面客户端共享本机后端设计

> 语言：简体中文 | [English](../../docs/local-shared-backend-design.md)
>
> 日期：2026-09-22（Asia/Shanghai）。状态：实现完成，隔离资格验证通过。生产部署、真实设备麦克风／HTTPS 验收与发布切换仍须单独授权；本文不表示已完成生产部署。

## 1. 目标与范围

一台主机运行一套 SparkClaw Gateway、PostgreSQL 和业务文件存储。普通浏览器中的 Web 工作台与 Electron 桌面工作台访问这套后端，看到同一 Owner 的会话、消息、任务、邮件、记忆、审批和配置。桌面端启动时自动连接本机服务，无需填写服务器地址或进行配对。

本期同时验收本机桌面与局域网 Web。桌面固定访问 `http://127.0.0.1:18790`，局域网浏览器访问 `http://<后端主机局域网 IP>:18790`，实际端口沿用部署配置。两者进入同一监听服务。桌面端只接受回环地址，不提供远程服务器列表、跨设备发现、扫码配对、离线写入队列或多机同步。部署中的 local/remote 模型来源选项与客户端所在位置是不同概念；两种模型部署都可使用本设计。

桌面通过安装时配置的本机专用凭据自动认证，Web 沿用独立客户端 token 的登录方式。共用端口上的业务请求统一需要认证。此修订替代早期稿中的“Web 仅本机可用”和匿名 `/api/local/session` 自动授权方案；不再根据访问来源自动授予 Owner 权限。当前其他集成的凭据、审批和授权规则保持各自语义。

## 2. 已实现结果

| 范围 | 已实现结果 |
|---|---|
| 共用 UI／API | `apps/webchat` 继续同时服务浏览器与桌面端，环境差异收敛在同一类型化 API 下的 transport |
| 单一入口 | Web 与 desktop 的 HTTP／SSE／文件和语音都使用配置后的工作台 origin；宿主 `18789` 保持私有，`18795` 不存在 |
| 持久化 | 两端共用同一 Gateway repository、PostgreSQL、workspace、artifact 与 trace store；desktop 不创建业务数据库 |
| 凭据 | 部署生成权限受限的 Desktop Client；已认证 Owner 在设置中签发独立、一次性展示的 Web Client token |
| 身份／边界 | 两端验证 deployment／Owner／client；Gateway 从 bearer 推导 Owner，并在服务端隔离 session、trace、memory、approval、feedback、tool、file 与 client |
| 刷新 | 带认证且有界的 workbench SSE 加五秒／前台恢复 polling 刷新列表和可见数据；初连、重连、溢出都会 resync |
| 并发／生命周期 | Gateway 中央拒绝同会话并发提交，已接纳工作脱离 request 生命周期；desktop 在 resume／retry 时重校验 |
| 启动 | 空数据保持空视图，只有显式操作才创建会话；generation guard 丢弃旧响应，删除 active session 后选择有效或空视图 |

实现已有仓库测试、隔离 PostgreSQL／LAN 双客户端资格和重新构建的桌面成品证据；目标主机发布验收与实现证据分开管理。

## 3. 唯一入口与部署归属

| 调用方 / 协议 | 对外入口 | 实际目标 |
|---|---|---|
| 本机 Web 页面 | `http://127.0.0.1:<port>/` | WebChat 静态资源 |
| 局域网 Web 页面 | `http://<后端主机局域网 IP>:<port>/` | 同一 WebChat 静态资源 |
| Web JSON / SSE / 文件 | 同源 `/api/...` | Nginx → 同一 Gateway |
| Electron JSON / SSE / 文件 | `sparkclaw-app://workbench/desktop-gateway/...` | 主进程 → `http://127.0.0.1:<port>/...` → 同一 Gateway |
| 两端语音 WebSocket | Web 使用页面 origin，桌面使用回环 origin，路径均为 `/api/speech/realtime` | 同一 Nginx WebSocket proxy → 同一 Gateway |
| Gateway 业务数据 | 现有 Store repositories | 现有 PostgreSQL、workspace、artifact、trace 目录 |

`18790` 是默认工作台端口；`18789` 保持 Gateway 内部端口。本机工作台部署移除 `18795` 的监听、映射和探针，不新增另一配对端口。桌面应用本身不监听业务 HTTP 端口。

保持现有局域网可达的工作台入口，默认沿用 `0.0.0.0:<port>` 的 IPv4 publication，使回环和局域网 IP 都能访问同一服务。不能为桌面自动连接把 Web 入口改成仅回环。已有自定义绑定应验证同时满足本机桌面和 LAN Web 可达；不匹配时报告配置问题。IPv6 根据既有部署配置处理，不成为本期必需项。

共享入口只转发调用方已有的认证凭据，不注入通用 Owner token，也不新增匿名授权入口。桌面自动连接依赖本机凭据文件；Web 从本机或局域网访问均执行相同 token 校验。本设计文档本身不会修改现有部署。

部署 / 服务管理器拥有 Gateway、数据库与后台任务生命周期。桌面启动只检查并连接现有服务；关闭窗口、退出桌面或关闭 Web 标签页不会停止 Gateway、创建数据库或切换 Store backend。

### 3.1 本机连接描述

建议部署脚本生成 `data/runtime/local-workbench.json`，由安装后的 Launcher 将绝对路径传给桌面主进程。它是本机部署的输出，不是第二份手动配置；只保存版本、入口、部署标识，例如：

```json
{
  "schema_version": 1,
  "origin": "http://127.0.0.1:18790",
  "deployment_id": "<persistent-installation-id>"
}
```

文件不包含 token、数据库口令或凭据密钥。端口直接来自现有 `SPARKCLAW_WEBCHAT_PORT`。稳定的 deployment ID 保存在部署数据中，普通重启不变；导入另一部署或整库恢复时必须核验身份，不能靠端口号判断是原后端。专用凭据另存为 `data/runtime/desktop-client.json`，由 Launcher 向主进程提供受控绝对路径，见下一节；两个文件均不属于 Nginx 静态资源或下载目录。

主进程只接受规范化的 HTTP 回环 origin，拒绝域名、非回环 IP、用户名密码、路径、查询参数、fragment 和重定向到其他地址。描述缺失或不匹配时显示连接配置错误，不扫描端口、不回退到 `18789`，也不自动生成另一份数据。

开发与隔离资格测试可以显式注入测试描述，不能让测试地址覆盖规则成为产品的远程连接入口。

## 4. 不使用 18795 的认证

采用“桌面读取本机预配置 Client 凭据、Web 使用独立 Client 凭据”的方式。Gateway 在同一业务入口校验 bearer；不新增匿名 `/api/local/session`，也不依据回环 IP、Docker 网段、Host、转发头或 Electron 标记签发凭据。

### 4.1 桌面首次配置与自动连接

1. 本机部署 / 安装流程为指定桌面用户准备一个随机专用 Client token、稳定 client ID、deployment ID 与 Owner 绑定，凭据文件使用 `0600`，父目录 `0700`，不放入安装包或共享目录。容器读取所需配置时使用受限只读挂载。
2. Gateway 本地启动装配通过 repository 的专用条件命令登记该 Client，只保存 token hash。该初始化路径不能通过 HTTP 触发，不启用任何匿名创建 Client 的路由。部署脚本不直接写数据库，也不另起常驻 Gateway。
3. 登记确认持久化且身份匹配后，Launcher 将连接描述与凭据文件的绝对路径交给 Electron 主进程；主进程检查文件类型、归属和权限，读取凭据并请求回环地址上的身份接口。
4. 已认证的 `GET /api/workbench/identity` 返回 `deployment_id`、`owner_id`、`client_id`，供两端校验身份；返回内容不包含 token。校验通过后加载业务数据并订阅事件。
5. 桌面主进程给限定工作台请求注入 bearer，preload 只返回连接状态。重启复用同一 Client，不重新签发、不调用配对接口。

第一批实现已经新增独立的内部 Client 登记命令，不复用配对 claim，也不假定存在 SaveClient API。初始化对同一 ID / hash / Owner 的重放保持幂等，对已撤销 Client 或身份冲突会拒绝，并按既有持久化与 unknown-outcome 规则对账。客户端记录与凭据文件分两步落盘，必须使用稳定候选身份恢复；中途失败不能悄悄生成新 token 覆盖已登记记录。

已安装环境缺少专用文件时，由本机配置流程补齐；桌面显示配置未完成，不通过网络自我授权。重新安装、普通服务重启或点击重试均不能重新激活已撤销凭据。桌面登记失败单独报告并阻止桌面连接，不能因此停止已能正常服务其他客户端的 Gateway。

### 4.2 Web 登录与新凭据签发

Web 保留现有 token 输入与浏览器持久化流程，本机浏览器和 LAN 浏览器规则一致；已有有效 token 可继续使用。首次从 LAN 打开时，页面可加载，受保护数据需要登录后读取。存储按服务 origin / deployment 隔离，不复制桌面专用 token 到浏览器。

新 Web 客户端通过已认证的 Owner 管理操作 `POST /api/clients` 签发，可从桌面设置或其他已授权管理入口调用。后端从认证上下文派生 Owner／actor，要求 idempotency key，以稳定 candidate 对账 unknown outcome，并只展示一次 credential，供目标浏览器 token 表单使用；不需要 pairing code 或 18795。

签发返回使用 `Cache-Control: no-store`，不记录明文 token，不把它放在 URL、自动复制到剪贴板或发送给其他设备。签发结果未知时按候选身份对账，不能盲目重试创建多个凭据。各 Client 可独立撤销，撤销桌面凭据不会撤销 Web 凭据。

两端必须校验同一 deployment / Owner；沿用现有默认 Owner 的安装继续使用该 Owner。旧 token 指向其他 Owner 时说明不匹配，不切换、合并数据或显示新的空工作区。会话接口当前默认 Owner / 请求参数与其他接口的 principal 取值应对齐；不能仅靠前端保证归属。

### 4.3 请求边界与失败行为

- Nginx 保留 LAN 与回环入口，转发客户端 Authorization；不向任何通用业务路由注入 Owner 凭据或来源证明。伪造 `X-Forwarded-For`、Host 或本地标记不能绕过认证，Docker 地址转换也不改变此规则。
- Desktop main 只向连接描述中的回环 origin 注入凭据，拒绝跨 origin 重定向，并校验可信工作台调用方。凭据文件不经 HTTP / workspace 下载暴露；个人浏览页不能访问工作台代理或主进程凭据。
- Web 使用同源业务请求；允许的 Host / Origin 包含配置的局域网地址和本机地址。Origin / CORS 校验用于浏览器来源约束，不用于授予 Owner 权限。其他集成的来源和认证规则继续独立生效。
- 文件、SSE 和语音 ticket 使用相同认证边界。Web 的 WS URL 由当前页面 origin 派生，不能硬编码到访问者自己的 `127.0.0.1`；桌面才固定使用后端主机回环地址。
- 桌面文件缺失、权限错误或 token 撤销时停止自动连接，提示修复本机配置；Web 无效 token 显示登录状态。修复 / 换发需要现有 Owner 授权或本机部署管理能力，普通网络请求和重试不能重建权限。
- 超时或 `503` 保留身份，恢复后重试读取和订阅，不重放消息、邮件发送或审批。

本期信任指定桌面 OS 用户及其主进程；权限文件不能隔离已控制同一用户的恶意进程。业务认证须与旧 `pairing_required` 开关解耦，不能通过关闭该开关并清空 API token 实现免配对；browser-control 等依赖和产品配置校验需同步适配。

## 5. 数据共享边界

| 数据 | 权威位置 | 双端行为 |
|---|---|---|
| 会话、消息、运行结果、任务、审批、记忆、计划任务、通知 | Gateway / PostgreSQL | 相同 Owner 和查询条件下看到相同记录 |
| 邮件、已保存邮件草稿、分类、查看记录 | Gateway / PostgreSQL | 共享持久状态，保留现有版本和发送语义 |
| 集成配置、模型配置、服务端凭据 | 后端对应配置 / credential repository | 两端看到相同可公开投影；密钥留在后端 |
| 文档、上传、制品、Trace | 后端文件存储及数据库元数据 | 使用相同资源 API；客户端不直接挂载数据目录 |
| 未发送的聊天输入与临时附件选择 | 当前前端 | 保留本地输入，另一端刷新不会覆盖；不承诺未保存草稿跨端同步 |
| 当前选中会话、语言、麦克风、侧栏、窗口大小 | 各客户端 | 独立设备 / 视图偏好 |
| Web 登录存储、Electron 网站 Cookie、个人下载与权限 | 各浏览器 / Electron Session | 不复制到普通浏览器，不通过业务数据库同步 |

这次不迁移 PostgreSQL、不导入旧 `gateway-state.json`，也不新增桌面业务数据库。Web 与桌面连接后应看到现有产品数据；若数据不一致，首先核对 deployment、Owner、过滤条件与入口，不能以“导入空数据”修复连接问题。

普通浏览器没有 Electron capability，保留现有 Web 展示能力。浏览器任务仍由同机 Gateway → Controller → Electron Adapter 执行；原 Unix Socket 控制链足够，不需要网络化设备通道。Electron 完全退出时，依赖它的浏览器能力应报告不可用；其他后端数据与不依赖浏览器的任务继续服务。

## 6. 双端同步方式

持久化成功的 API 响应是写入结果；SSE 只通知客户端刷新。建议增加认证的 `GET /api/workbench/events/stream`，按后端确定的 Owner 发布资源失效通知，覆盖会话列表、会话内容、任务、审批、邮件、计划任务、通知与共享设置。

首期单 Gateway 使用有界进程内通知，不为 UI 刷新增加持久事件表或跨机消息总线。事件携带 `epoch`、进程内递增 `sequence`、资源类别及必要资源 ID；它们是通知标记，不能充当业务行版本或已提交事务证明。

- 只在 repository 确认持久化后发布；后台邮件、计划任务、审批及模型执行路径同样发布，不能只在 HTTP handler 发布。
- 首次连接 / 重连都发 `resync`，客户端重新获取会话列表及当前可见数据。Gateway 重启会更换 epoch；序号缺口、队列溢出或慢消费者同样触发重新读取。本期不承诺离线事件逐条补发。
- 先建立订阅，再加载快照；快照期间收到的失效记录为 dirty，并在请求完成后补一次读取，避免启动窗口丢更新。
- 两端共用基于 `fetch` 的带认证 SSE 客户端；Web 附带自己的 bearer，桌面由 main 注入。复用已有流读取基础，消除当前“有 token 时不建立活动会话 EventSource”的差异。
- 按资源合并通知、去抖刷新，避免每个 token delta 触发完整 `refreshGlobal()`。会话列表必须独立刷新；邮件列表、通知已读状态、设置等刷新其对应查询。
- 前台每 5 秒进行有界的当前视图校准；恢复可见、获得焦点和网络恢复时立即校准。轮询用于弥补进程通知丢失，合并现有定时器，避免叠加多套全量轮询。
- 请求携带前端 generation / 当前选中资源标识，忽略过期响应；保留本端未发送输入，处理另一端删除当前会话时切换到空状态或有效会话。

验收目标：健康本机服务下，持久化后的变化在另一可见客户端 2 秒内触发刷新；停用事件连接时，在一个 5 秒轮询周期加查询耗时内收敛。另一端可实时看到已入库的状态 / 消息，首期不要求逐 token 镜像其他客户端的模型输出。

## 7. 并发、重试与启动

两端可以同时在线，各自选择不同会话。业务写入继续使用现有条件写入、版本检查和审批状态机；实现前检查涉及的接口，不能假定所有写接口已有幂等键。已支持 expected version 的接口在冲突时返回冲突并重新读取，不能用过期缓存覆盖新值。

同会话并发发送由后端统一接纳 / 排队或明确拒绝，不在两个前端各自判定为空闲。消息发送、任务执行和外部发送在断线后使用既有 operation/run ID 查询结果；没有可对账身份时显示结果待确认，禁止自动重发。任务流的 HTTP 生命周期与已接纳任务生命周期需要实际验证，不能仅因数据共用就声称关闭页面不会影响运行。

前端启动只读取会话列表，不自动创建默认会话。空库显示新建入口，用户主动创建才写入。桌面和 Web 同时首次打开因此不会制造两条无意创建的空会话。

## 8. 连接状态与生命周期

| 状态 / 事件 | 预期行为 |
|---|---|
| 桌面首次打开 | 读取本机连接描述 / 专用凭据 → 验证身份 → 读取现有数据 |
| 本机 / LAN Web 首次打开 | 有有效 token 则读取数据，否则显示现有登录表单；不匿名授权 |
| Gateway 未启动 | 桌面提示本机服务不可用，Web 提示服务连接失败；提供重试，不启动临时 Gateway |
| PostgreSQL / credential vault 未就绪 | 显示依赖未就绪，保留缓存及身份；不回退 memory/file |
| 连接描述 / deployment / Owner 不匹配 | 停止加载，说明连接身份冲突；不自动重绑 |
| Gateway 重启 / 休眠恢复 | 重新验证身份、订阅和读取快照，不重新提交写操作 |
| 关闭桌面窗口 | 延续现有隐藏行为，Gateway 独立运行 |
| 完全退出 Electron | Gateway 保持；内嵌浏览器任务按现有故障恢复语义中断，不伪装成功 |
| 卸载桌面 | 不删除后端 PostgreSQL、凭据密钥、workspace、artifact 或 trace |

桌面显示“已连接本机服务 / 正在重连 / 本机配置未完成 / 认证失效”等状态；Web 保留登录和服务连接提示。普通用户流程不出现配对码、服务器列表、数据库参数或 `18795`。

## 9. 实施落点与阶段

| 位置 | 已实现变更 |
|---|---|
| `docker/compose.yaml`、Nginx 模板、部署及 autostart 脚本 | 保留 LAN 入口、移除 18795、生成连接描述 / 权限受限专用凭据、配置与文件持久化对账 |
| Gateway `config`、`middleware.go`、Client/credential repository 装配 | 内部 Client 签发、本机安装登记、已认证身份 / Owner 签发接口、认证配置解耦与 Owner 对齐 |
| Gateway 业务服务 / workbench 事件 handler | 持久化后失效通知、有界订阅及 resync |
| Desktop `main.mjs`、preload、连接模块 | 本机描述读取、凭据保管、统一 HTTP/SSE/文件目标、语音 WS、移除 pairing proxy |
| WebChat API、`App.tsx`、共享 hooks | 保留 Web token 登录、Owner 签发 UI、带认证订阅、列表 / 视图刷新、启动不创建会话及连接状态 |
| 部署文档、桌面资格测试与制品测试 | 更新连接模型，重新验证实际打包后的四类传输 |

本次实现已按以下三步依次完成：

1. **打通同一实例：**连接描述、桌面本机凭据、Web 独立 token 登录、Owner 绑定、两端查询现有 PostgreSQL；资格测试明确关闭 `18795`，并包含 LAN Web。
2. **完成共享体验：**事件通知、轮询校准、并发 / 断线行为、会话列表刷新与空库启动。
3. **完成产品接入：**部署配置与文档同步，重建桌面制品，隔离环境双端验收；已有制品的旧配对测试结果不算通过新设计。

三个实现阶段均已在当前工作树完成。生产上线仍不在本次范围：重建候选版本还需要目标主机 GPU／DPI／IME／音频、LAN HTTPS 麦克风、Provider 登录、安装／更新／卸载、图标及明确切换验收。

### 9.1 资格证据

- `npm run qualify:local-shared-backend` 启动一次性 PostgreSQL，将测试 Gateway 绑定到全部 IPv4 interface，desktop Client 经回环访问，独立 Web Client 从 Docker 网络经宿主非回环地址访问；验证 identity、空启动、双向 CRUD、两秒内 SSE、文件字节一致和伪造来源拒绝。
- `npm run qualify:desktop-artifacts` 分别解包并启动重建的 ARM64 AppImage／DEB，使用一次性 user-data 与受限 descriptor／credential，验证打包工作台、认证 HTTP／SSE proxy、语音 WebSocket 路由和有界 desktop IPC。
- Gateway、Store、WebChat、Desktop、deployment profile、launcher 与 Compose 测试覆盖 provisioning 重放／撤销／unknown outcome、Owner 隔离、队列溢出／resync、request 脱离、custom port、Compose 无 `18795` listener 及凭据不泄漏。

## 10. 验收矩阵

使用隔离 PostgreSQL、临时 Electron user-data、测试页面和两个独立客户端；Web 从第二个测试网络环境访问宿主非回环入口，以验证 LAN，而不只测试 localhost。不操作用户正在使用的桌面显示、真实邮件账号或生产数据。Web 语音采集沿用浏览器的安全上下文要求，LAN 语音验收使用满足该要求的 HTTPS 入口；HTTP LAN 的数据访问与麦克风权限分别记录。

| 场景 | 通过条件 |
|---|---|
| 18795 无监听且宿主 18789 不可达 | 桌面自动读取凭据连接，LAN Web 使用独立 token 登录，均经过 18790；无 pairing 请求 |
| LAN 未认证 / 伪造本机来源 | 可加载登录页面，受保护 API 和签发接口拒绝访问；伪造 Host / 转发头不授予权限 |
| 桌面安装登记中断 / 重放 / 撤销 | 同一候选可对账，无重复 Client、凭据覆盖或重启后撤销失效 |
| 新 Web 凭据签发 | 已认证 Owner 可签发，匿名请求不可签发；新 token 绑定同一 Owner，不复用桌面 token |
| 自定义工作台端口 | 部署、Web、桌面和语音读取同一配置；无硬编码回退 |
| 首次打开与重启 | deployment / Owner 相同、client ID 分别记录；普通重启复用会话；空库无自动新增会话 |
| 双向 CRUD | 两端新增、改名、删除会话与已支持的共享记录，另一端按目标时限更新 |
| 文件与语音 | 两端授权下载相同文件字节；HTTP、SSE、blob 和 WS 均命中同一后端 |
| 邮件 / 计划 / 通知后台更新 | 无前端写请求也能通知另一端；通知已读状态能够收敛 |
| 并发修改与响应乱序 | 已有版本控制生效；旧响应不覆盖新视图；不产生重复发送 |
| SSE 丢失、队列溢出、Gateway 重启 | resync / 轮询恢复到数据库事实；不把 epoch 序号当成持久游标 |
| 断线时提交结果未知 | 对账或明确待确认；无自动重发、自动批准、自动切库 |
| 桌面非回环目标 / 跨源重定向 / 凭据文件 HTTP 访问 | 被拒绝，凭据不泄漏；合法 LAN Web 目标仍可用 |
| 认证撤销、Owner / deployment 不一致 | 停止使用旧连接；不后台自动创建新身份掩盖问题 |
| 关闭 / 卸载客户端 | 后端数据保留；普通后台服务持续；浏览器依赖明确报告状态 |

## 11. 兼容与迁移

部署升级沿用现有 PostgreSQL volume、credential key、Owner、文件目录与 LAN Web 可达性。先验证这些身份和已有数据，再配置桌面专用凭据；没有 file-to-PostgreSQL 或两库合并步骤。签发保存客户端记录，按既有 repository 持久化 / unknown-outcome 约定实现，不能绕过可靠性边界。

已有客户端 token 可先验证并复用；无法匹配当前本地 Owner 的旧会话不得自动替换。旧 `18795`、pairing proxy、前端 pairing 字段和相应产品探针应在连接改造交付时一起移除 / 更新。现有其他集成如果仍使用配对代码，其业务接口先保留；本设计不重新分发其权限。

已检查 InfiniCenter 的 SparkClaw–JingSi Runtime v1 及 SparkClaw–IMMS evidence v1/v2 契约。本设计新增的本地工作台入口与 UI 通知属于 SparkClaw 内部产品面，不改变这些契约、其他项目发布节奏或中央 schema；其他项目无需跟进。若实施中需要修改这些边界，应先按簇协议立案。
