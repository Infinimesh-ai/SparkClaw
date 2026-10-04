# SparkClaw 客户端 R3 实施与验收

> 语言：简体中文 | [English](../../docs/client-r3-implementation.md)

后续 Mac 本机会话已完成 ARM64 开发包构建、启动和部分隔离检查，见[Mac 实际验收记录](macos-r3-acceptance.md)。下文保留 Linux／共享交付时的历史范围；不表示完整 M01–M12 已通过。

日期：2026-10-03。交付分支：`codex/sparkclaw-r3`。P0–P5 Linux／共享源码与适用隔离检查已完成。本记录表示源码交付，生产切换和 Mac 验收仍待完成；用户在代码完成后自行编译 Mac。本轮未进行 Mac 编译／交叉构建／签名、旧测试数据迁移或生产数据操作。

设计依据：[客户端／后端 R3](client-backend-architecture-design.md)、[Mac LAN 设计](macos-lan-desktop-design.md)、[Mac 连接指南](macos-connection-guide.md)。

## 本轮范围与历史基线

用户 10 月 3 日明确排除 InfiniCenter 协调及等待 0031 评审。9 月 30 日的中枢检查、提案属于历史记录，既不是本轮前置条件，也不表示其通过评审。既有公开集成契约与兼容性检查保留；本轮新增安装客户端 R3 路径，不改变原有保留期或线协议。

9 月 30 日已完成本地 SQLite／文件、安全登录、凭据领取／设备管理及 Mac 打包源码，执行、邮箱同步和 Host Broker 尚未完成。本轮基线完整 Go build／vet／tests、Desktop 36、WebChat 185、凭据 14 项通过。旧桌面读取后端历史并使用本地 relay；这些证据不能证明 R3。

## 阶段记录

| 阶段 | 已交付 Linux／共享范围 | 剩余门禁 |
|---|---|---|
| P0 | 冻结字节／期限／控制字段白名单；安装客户端身份；持久准入／防重记录；内存 Workflow 仓储、私有 tmpfs 工作区和加密交付 spool | 物理断电／磁盘满、Mac 实机证据；生产切换不在本轮范围 |
| P1 | 主进程 ClientStore schema 4；明确提交不可变上下文、输入文件、ExecutionClient、本地任务／消息／文件／审批保存、明确审批继续与取消／状态核对 | 交付 SHA 的 Mac GUI、真实模型／服务商使用 |
| P2 | 一步式初始／恢复连接凭据、可达设备设置、固定证书 LAN 登录及安装绑定；持久结果接收／ACK；邮箱目录／快照／增量／墓碑和离线缓存 | 真实 LAN／Mac Keychain、两台实机客户端 |
| P3 | 鉴权出站 WSS Broker／Host、统一采集／内嵌适配角色、资源端租约、真实会话 Electron 页面、封闭原生操作及未知写入核对 | Mac 内嵌页面、支持站点／弹窗及实机休眠／退出矩阵 |
| P4 | 校验输入／输出／附件副本、私有原子保存、落盘后才 ACK；过期／重启／故障／清理与哈希／路径检查 | 物理磁盘满／断电、Mac 下载／权限 |
| P5 | 桌面 main／preload／UI 集成、隔离 Linux／原生验收、Mac 仅客户端打包源码和用户构建命令，通过 R3 分支交付 | 用户 Mac 编译、M01–M12、架构／签名／公证／升级；生产切换 |

普通 Web 继续原有路径，不宣称 R3 本地持久化。R3 桌面挂载本地工作台、邮箱缓存、设备设置和内嵌 BrowserPanel。保存的请求在明确提交前保持 `awaiting_runtime`，升级和后台轮询均不提交该队列。交付轮询只查询原请求 ID，重试已持久 ACK；未知执行／写入不自动重发。

## 冻结的存储、身份与容量

ClientStore 位于 `userData/client-r3`，由 main 管理 SQLite WAL／FULL，schema 4，持久安装 UUID，并按 deployment／Owner／签发 client 分区。schema 1／2／3 升级保留记录与安装身份且不重放；新版本 schema 拒绝打开。退出保留本地数据库／文件。后端将每个签发客户端绑定到一个安装，另一安装不能接管。renderer 只获得有界类型化 IPC 和不透明文件／页面 ID，不获得凭据、Host grant 或任意路径。

| 参数 | 实际限制 |
|---|---|
| 本地输入／上下文 | 输入 UTF-8 16 KiB；32 条 user／assistant、96 KiB 上下文；无可信授权角色 |
| 临时执行内容 | 每任务 32 MiB、每 Owner 256 MiB；记录写入前保守计量 JSON／artifact；最多 8 个活动执行 |
| 执行／上传暂存 | 执行和上传 15 分钟期限；最多 32 个暂存上传；重试不延长期限 |
| 待审批 | 每执行最多 32 条不可变记录，每条 64 KiB；明确决定绑定原请求／审批／摘要及既有 15 分钟期限 |
| 结果 | payload／文件合计 8 MiB；生成后绝对 24 小时过期；ACK 提前清理 |
| 本地选择／附件副本 | 每文件 64 MiB；执行上传仍受任务限制，生成文件受结果限制 |
| 持久执行防重 | 每部署生命周期 100,000 条；满容量拒绝准入，不删除防重记录 |
| 浏览器授权／租约 | 明确 Host grant 15 分钟；租约 30 秒、心跳 10 秒；精确身份／runtime／epoch／page／generation |
| 邮箱同步 | 完整编码页最多 1 MiB；有界分页、revision、epoch／cursor 恢复和墓碑 |
| 单次定时任务 | 未来 24 小时内，无重复／文件；在线租约 30 秒、每 10 秒续租；每 Owner 8、全局 32 个注册 |

这些是内容准入的逻辑预算，不表示整个进程 RSS 被限制到上述字节数。未支持的操作或必需限制不可用时拒绝执行。

每次 R3 执行只接收明确提交的不可变客户端上下文。新建 MemoryStore／ToolHub／policy／artifact 组合共享编译 Workflow 和模型路由，但不使用旧历史、trace、常驻提醒／消息路由、MCP、持久审批或 memory。工具文件使用按部署隔离、归属受控的 tmpfs 目录，不进入普通工作区；Linux 实现要求 tmpfs。完成／取消／到期清理；启动只移除本部署归属正确的过期目录。输入上传仅暂存内存。临时输入保留唯一可见文件名，使按文件名引用直接可用；主进程排队和服务端准入均拒绝重名附件，不覆盖或猜选文件。生成交付包使用 AES-256-GCM、私有原子文件和 0600 密钥，不在普通 artifact 仓储写明文输出归档。

持久执行控制只包含 Owner／client／installation／request ID、输入 digest、枚举状态及创建／期限／生成／过期时间和结果 digest。不保存 prompt、标题、上下文、本地会话 ID、DOM、文档正文或生成输出。浏览器 fence／journal 只保留命令 digest、身份／page／lease 代际和枚举结果，不保留参数、DOM 或输出。邮箱 revision 是类型化后端邮件记录，与非邮箱控制分离。

准入先持久 request fence 再执行。同 ID／digest 查询原状态，内容漂移拒绝。被中断的 accepted／running 恢复为 `unknown`，不重执行。读取结果先检查绝对期限，GET 不写入。ACK 要求 sequence／digest 匹配及客户端持久 receipt，先持久 `delivered` 再删除 spool。重启、丢 ACK 或清理不删除防重记录。私有进程锁阻止两个执行 spool 写入者。

## 协议与客户端组合

全部 `/api/r3/*` 请求要求已签发且认证的设备；除安装注册外，还要求服务端绑定的 `X-SparkClaw-Installation`。HTTPS／WSS 在传凭据前验证证书链、主机名及指纹，不允许重定向、裸 CDP 或客户端入站监听。

桌面注册只使用一个不透明的 `sparkclaw-connect-v1...` 值，其中包含公开 v2 固定后端描述和一枚已签发设备 Token。主进程负责解析，将描述与加密 Token 分开保存，并在发送任何 bearer 前执行相同 TLS 校验。这个本地注册信封不改变 Gateway bearer 或 R3 线协议。

| 传输 | 已实现操作 |
|---|---|
| `POST /api/r3/installations` | 封闭 schema／version／installation 注册，确认 deployment／Owner／client |
| `PUT /api/r3/inputs/{request}/files/{file}` | 明确有界不可变上传，digest／大小／哈希校验 |
| `POST /api/r3/executions` | 封闭 schema／context 信封和 `X-R3-Digest`，沿用持久 request ID |
| `GET /api/r3/executions/{request}` 和 `/files/{file}` | 分区状态／结果／文件查询，过期正文不可取 |
| `POST /api/r3/executions/{request}/approvals/{approval}` | 闭合 digest／approve-or-reject 决定；原 running 请求、不可变参数，不从内容取得权限 |
| `POST /api/r3/executions/{request}/ack` 和 `/cancel` | 持久接收或保守取消，不虚构完成 |
| `GET /api/r3/mail/mailboxes`／`POST /api/r3/mail/{mailbox}/sync` | 授权目录及 cursor／limit 同步；会修改 journal 的工作使用 POST |
| `GET /api/r3/mail/{mailbox}/messages/{mail}/attachments/{part}` | 后端权威附件，校验大小／哈希，明确保存本地副本 |
| `POST /api/r3/hosts/grants`、`GET /api/r3/hosts/connect` | 独立明确 Host grant 和客户端鉴权出站 WSS |
| `GET /api/r3/hosts/fences`、`POST /reconcile`、`POST /{host}/revoke` | 无正文未知记录、明确观测结果与独立 Host 撤销 |
| `POST /api/r3/schedules/lease`、`POST /{request}/renew` 或 `/cancel` | 内存未来上下文，绑定可续租的安装客户端在线租约 |

Gateway 可选原生 TLS 使用成对绝对路径 `gateway.tls_cert_file`／`gateway.tls_key_file` 或 `SPARKCLAW_GATEWAY_TLS_CERT_FILE`／`SPARKCLAW_GATEWAY_TLS_KEY_FILE`。无效证书对启动失败；私钥必须为 Gateway 用户拥有的普通私有文件，不能为 symlink。最低 TLS 1.2，Host 接入要求 TLS 实际到达 Gateway；代理需 HTTPS upstream，转发头不能授予访问。未配置时保留既有 HTTP 监听，源码交付不配置证书或生产入口。

renderer 不能代理这些 R3 传输。Desktop main 管理 ExecutionClient、ScheduleClient、MailSyncClient 和 BrowserHostAgent，退出／休眠／不可用时中止，恢复连接后继续状态核对。浏览器失去通道后需再次明确授权。认证代际阻止同身份的迟到响应落盘／ACK。

策略要求的审批保留在原临时 workflow 内，状态公开有界不可变参数及执行期限。明确批准通过正常 Runtime 校验执行原已保存工具调用并继续原 run，拒绝终止临时 workflow。批准后工具失败保守终止为 `unknown`，不虚报完成，不重放可能已经部分生效的外部写入。客户端上下文不携带审批权限，后端永久账本不保存审批正文。主进程先以相同身份／代际新查询确认 running 才开放动作，恢复／离线快照只读。决定响应丢失时核对原请求并保留决定不确定状态，不新提交任务。活动执行内同摘要同决定重复幂等，冲突拒绝；重启／取消／到期不重放审批。

ExecutionClient 验证原始 payload 和每个文件，统一持久保存结果正文／文件／receipt 后才 ACK。数据库、原子写入、损坏或哈希失败不确认接收。重启后丢 ACK 沿用原 receipt，不创建第二次执行，重试前再次校验 receipt 文件。

MailSyncStore 为独立、main 管理、分区 SQLite 缓存。重新快照暂存期间保留最后完整缓存；revision 缺口／epoch 变化重置暂存，不修改后端邮件。后端收信在没有客户端时继续，缓存／同步不启动重复收集器。暂停中止在途请求；恢复后的旧响应／409 不推进 cursor 或清空已提交缓存，晚附件不保存。附件副本是校验后的本地会话文件，原件仍归后端邮箱。本轮未读／发／删真实邮件。

单次定时定义先本地持久再注册。未来上下文仅保留于在线租约，过期／撤销即丢弃，错过到期不补跑。重连先查原 request；未来定义只有未准入才用同 ID 重注册。明确“立即运行”创建一个新持久请求。常驻后端收信独立于客户端租约。

内嵌命令操作 PageRegistry 的真实会话 WebContents。选择只改变展示，不转移 controller；release／reacquire 复用同一 view，代际拒绝旧命令。采集走统一适配层的独立后端专用角色，保留现有 Controller 参数／结果，不作为交互客户端替代。原生操作为封闭 navigate／read／snapshot／click／fill／select／screenshot／wait 集合，使用 snapshot refs 和精确 fence。本地 journal 或后端 fence 的未知写入均要求明确“观测到已完成／未应用”核对，不自动重试或推断成功。

## 验收记录

下表 `PARTIAL` 表示 Linux／共享部分通过，完整跨平台／真实环境／生产用例仍缺证据；`NOT_RUN` 表示缺少必需证据。全部 Mac M01–M12 等待用户在交付 SHA 上验收。

| 用例 | 结果 | 证据与剩余限制 |
|---|---|---|
| A01/A02/A14 | PARTIAL | 全新存储、安装／设备分区、升级不重放、无旧历史回退通过；两台实机／生产切换未执行 |
| A03 | PASS（Linux／共享） | 真实 HTTP＋标准 mock Workflow 只用客户端上下文；合成模型端点驱动真实文档 Workflow，经不可变审批后实际编辑／文件交付或明确拒绝；临时预算、tmpfs 清理、明文／控制审计通过；不宣称外部模型／站点 |
| A04/A13 | PARTIAL | 输入／输出／附件哈希、原子保存、数据库／磁盘失败注入、真实安装客户端 HTTP 附件路径通过；物理磁盘满／断电待验 |
| A05/A06 | PASS（Linux／共享） | 持久准入、丢准入／ACK、重启、防重、sequence／digest 漂移、24 小时绝对时钟夹具、过期查询／清理、容量拒绝通过；未运行真实 24 小时 soak |
| A07/A08 | PARTIAL | 真实 Linux 内嵌 DOM 点击／填写／读取、采集适配语义及 TLS／WSS 通过；Mac／支持外部站点待验 |
| A09/A10 | PARTIAL | 两个真实 view、20 次切换、同 view 再获取、B 选中时 A 迟到响应、真实写入丢响应、未知 journal／fence、租约／撤销／休眠测试通过；实机睡眠／kill 矩阵待验 |
| A11 | PASS（Linux／共享） | 合成邮箱持久快照／增量／墓碑、缺口／epoch 恢复、离线缓存、畸形／超限／冲突／磁盘失败及 10×220 KiB 分页通过 |
| A12 | PASS（Linux／共享） | file 后端无客户端时收集继续；合成任务在线租约过期／准入／撤销、重启和不补跑通过 |
| A15/A17 | PARTIAL | 真实固定 TLS／WSS、错误身份／证书、可信 IPC、首次解锁／安装绑定、撤销和凭据代际通过；Mac Keychain／真实 LAN 待验 |
| A16 | NOT_RUN（Mac） | 客户端打包源码／审计及 Linux 拒绝 Mac 构建通过；Mac 编译／实机／签名／升级和 Web 本地存储未验 |
| A18–A21 | PARTIAL | 私有 UDS／PTY 领取／恢复、可达设备路由、签发重试和撤销夹具通过；无真实生产凭据／Mac 证据 |

## Linux／共享集成验证

环境：Linux ARM64、Node 26.2.0、npm 11.17.0、Go 1.25.5。已安装 Electron 44.4.3 内含 Chromium 152.0.7977.130 和 Node 24.21.0。新增数据全部合成且隔离，临时验收 profile／显示／TLS 服务检查后销毁。新 Host 原生验收已接入桌面 CI；本轮只声称本机执行通过，远端 CI 尚未运行。

| 检查 | 结果 |
|---|---|
| `services/gateway` 中 `go build ./...`、`go vet ./...`、`go test ./...` | PASS，最终集成全量 |
| `go test -race ./cmd/sparkclaw ./internal/gateway ./internal/r3execution ./internal/r3browser ./internal/r3mail ./internal/emailmanagement` | PASS |
| `npm run test:desktop` | PASS，87 项 |
| 已安装 Electron Node 模式 store／capability／execution／approval／schedule／mail | PASS，真实 Electron Node／SQLite 49 项 |
| `npm run test:webchat`、`npm run build:desktop-ui` | PASS，193 项／47 文件、i18n 757 keys 和生产构建 |
| `npm run qualify:desktop-r3` | PASS，真实 Go TLS／WSS Broker＋实际 Electron 内嵌页面；点击／填写、两会话、20 切换、再获取和丢响应 fence |
| `npm run qualify:desktop` | PASS，隔离旧 adapter 回归，与 R3 Host 证据分开 |
| `npm run test:credentials`、`npm run check:desktop-managed-scripts` | PASS，凭据 14 项及生成资产检查 |
| 合成 ASAR／Resources 包审计及 Linux 拒绝 Mac 构建 | PASS，包含于 Desktop 测试；未编译 Mac |
| `npm audit --omit=dev` | PASS，运行依赖零漏洞 |
| `npm audit`（含开发依赖） | OPEN，electron-builder 开发工具链 8 项高危；未强制降级 |
| 精确双语 CI／链接检查、`git diff --check` | PASS，100 份双语项目 Markdown |

使用临时 headed Chromium 对本地工作台生产 bundle 在 1440×1000 和 390×844 实际截图检查，页面错误和横向溢出均为零。第一次窄屏检查发现继承的满屏高侧栏，修正后复验通过。设备／登录夹具保留 9 月 30 日证据；审批界面另以同样两种尺寸复验：不可变参数与明确按钮可见，缓存动作禁用，无页面异常或横向溢出。截图不算 Mac GUI 验收。Vite 既有 >500 KiB chunk 警告仍为非阻断。

旧 adapter 首次回归在 popup 后超时，未改变超时限制的隔离复验通过，保留该首次失败记录。开发依赖审计涉及 `electron-builder → app-builder-lib → @electron/get@3 → got → cacheable-request → http-cache-semantics@4.2.0`；[GHSA-ch52-4w7c-c8xp](https://github.com/advisories/GHSA-ch52-4w7c-c8xp) 于 10 月 2 日更新且无已发布补丁。此工具链审计仍为开放事项，运行依赖审计通过；本轮保留锁定打包版本，不通过强制降级宣称修复。

故障注入是有界合成证据，不表示物理磁盘满、突然断电、所有站点兼容或真实到期 soak。Host 原生夹具运行 Broker 和实际 WebContents，安装客户端 HTTP 路由验证 Gateway 安装／grant／reconcile／approval 形状；生产监听测试通过真实注册客户端 HTTPS／WSS、撤销、TLS1.1／不可信证书拒绝及伪造转发头拒绝；集成时发现形状不匹配，交付前已修复。审批加入后第一次全 Go 检查发现与 Store 接口守卫的方法名冲突；临时决定方法已改名，未放宽守卫，全量复验通过。未操作运行中的生产 Gateway、收信器、浏览器 profile 或客户端数据库。

## 源码交付与下一门禁

远端 `origin`（`https://github.com/Infinimesh-ai/SparkClaw.git`），分支 `codex/sparkclaw-r3`。最终交接给出推送 HEAD 的准确 SHA，Mac 构建／反馈使用该 SHA。[Mac 命令与连接前置条件](macos-connection-guide.md#3-mac-同步代码与构建) 包含 ARM64／x64 本机打包、独立设备凭据及通过原生 Gateway TLS 或 HTTPS upstream 代理配置并验证的 LAN HTTPS 入口。

P0–P5 源码与适用 Linux／共享检查完成。下一步由用户在交付 SHA 编译 Mac 并完成 M01–M12。签名／公证、CPU 架构、Keychain、升级／回退、实机睡眠／退出／磁盘故障、支持站点兼容、双客户端实机及生产切换仍未完成。不导入旧测试历史，不自动重放本地队列，不表示已部署生产。
