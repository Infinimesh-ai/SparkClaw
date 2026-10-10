# SparkX 与 SparkClaw ISCP 能力扩展设计

> 语言：简体中文 | [English](../../docs/desktop-iscp-capability-expansion-design.md)
>
> 更新：2026-10-10。状态：P1–P5 适配及按能力开放的桌面界面已实现，桌面安装及 work2 部署验收见 9.6–9.7 节；ASR/TTS 继续暂缓。
> 范围：SparkX 经本地 Docker ISCP Relay 连接 SparkClaw，按依赖顺序补齐业务。允许调整本地 Relay，但必须保持与在线 Relay 的协议兼容；本轮不切换在线部署。

## 1. 目标、基线与实施顺序

用户于 2026-10-09 再次确认[客户端与后端关系](architecture.md)：所有业务逻辑交由
SparkClaw 处理，SparkX 负责本机工作台数据与界面。局域网和 ISCP 是连接同一后端的两种
方式。本机文件校验、传输和浏览器宿主命令属于资源适配，不把业务裁决移到客户端。

现有文本链路和续签证据见[本地 ISCP 联调设计](desktop-iscp-connection-design.md)。当前应用 profile 为 `sparkclaw.workbench.transport.v1`，仅包含 identity、installation bind、三项 presentation 读取以及 execution submit／lookup／cancel／ack 共九个操作。文件、设置、通知、审批、邮件、浏览器和语音不能因 ISCP 已连接就视为可用。此前审计出的文本并发、授权到期恢复和超限结果问题已纳入首批修复，实施记录见 9.1 节。

业务仍使用同一套 Gateway domain service、execution、ToolHub 和 Policy；ISCP 只增加传输和明确的业务适配。桌面非邮件历史、草稿、文件和定时继续由桌面本地保存；邮件以后端为事实来源。不把 WebChat 历史同步到桌面，也不复制一套运行时。

2026-10-09 评审决定：连接／设备的长期授权永久有效，直至用户手动删除；本地 Relay 可以调整，但不得形成与在线 Relay 不兼容的协议分支；等待审批的任务在 Gateway 重启后终结，不恢复等待或自动续跑。这些决定指导实施。现有二十四小时授权及原始 reference Relay 的验收记录仍只描述已实现基线，不能据本文认定运行服务已切换。

| 顺序 | 交付范围 | 前置依赖 | 可开放的界面 |
|---|---|---|---|
| P1 | 能力协商、长期授权及删除、配置、设置、通知基础接口 | 现有身份、绑定、续签和文本链路 | 已验收的授权管理、设置项、通知列表及已读操作 |
| P2 | 文件、图片、附件、生成产物和大消息分块；Relay 容量及兼容性验证 | P1 的能力、权限和请求恢复约定 | 分别验收后的上传、选择附件、预览、下载 |
| P3 | 审批、执行进度、可靠事件同步 | P1 的资源版本；P2 的大快照／产物传输 | 审批卡片、进度与实时状态 |
| P4 | 邮件、浏览器和其他工具业务 | P1–P3，按工具声明完整依赖 | 按邮箱服务商、浏览器宿主、工具逐项开放 |
| P5 | 录音转写、实时 ASR，以及独立验收的实时音频能力 | P1–P4；额外的 ISCP 流式承载实测 | 先录音转写，再实时转写，最后播放／双向音频 |

实施和发布按 `P1 → P2 → P3 → P4 → P5` 推进。前一阶段的公共底座必须通过，后续阶段才能启用；同阶段子能力也各自验收。未接通服务商、只有 mock、只有单测或只有拒绝请求的证据，均不能使对应界面可用。

## 2. 五个阶段共用的约定

### 2.1 应用 profile、操作注册和能力门禁

本文操作名是 **SparkClaw 应用协议草案**，不是上游 ISCP 标准。实施 P1 时冻结 `sparkclaw.workbench.transport.v2` 的消息 schema、错误码和协商测试向量。当前 v1 使用固定操作清单和严格校验，不能直接向 v1 manifest 塞入新增字段或操作；旧版本保留文本模式，新版本显式协商双方支持的 profile，版本不兼容则报错。

v2 使用单一 typed operation registry，定义每个操作的方向、版本、权限、输入／输出 schema、限额、幂等性、恢复方式、依赖能力和事件类别。Gateway dispatch、helper 校验、客户端映射和契约测试由它派生。禁止任意 `method + URL + headers` 隧道，也不接受客户端自报的 Owner 或管理员身份。

认证会话内取得 capability manifest，至少包含 profile／schema 版本、deployment identity、capability revision、支持的操作、授权投影、有效限额、依赖就绪状态和有效期；它必须绑定当前会话与主体。凭据只由 Electron main／helper 持有，renderer 只接收脱敏能力投影。

```text
界面可用 = 客户端已实现 ∩ 服务端已实现 ∩ 当前主体有权限
           ∩ 全部依赖就绪 ∩ 当前版本已通过验收 ∩ 发布开关已开启
```

验收状态属于受控发布配置，不允许服务端自报 `supported=true` 代替验收。manifest 缺失、过期、scope 改变或依赖失败时关闭对应动作并解释原因。设置菜单、文件上传、文件下载、邮件读取、邮件发送、浏览器读取、浏览器写入和实时语音分别设门禁；禁止一次性把 `iscp` 限制或 `TextOnly` 全部移除。

### 2.2 权限与授权续签

每个请求在服务端检查 Grant 与 peer、deployment、Owner、Client、installation、operation 和目标资源；操作涉及 execution、mailbox、browser host 时继续检查其归属及当前授权版本。现有 `sparkclaw.workbench.v1` permission 只代表传输准入，不自动授予设置管理、全部文件或所有工具权限。新增权限必须显式授权；auto-renew 只能延续已获准范围，不能自行扩权。

连接／设备的长期授权没有绝对截止时间，持久有效直至用户手动删除。短期 Grant、Relay access／refresh 凭据和 capability manifest 仍保留协议要求的有限有效期并自动更新；凭据到期不是用户授权失效，也不要求用户按日重新批准。任务 deadline、单次审批有效期及浏览器任务 lease 仍是资源和动作边界，不因长期授权永久有效而取消。使用有明确版本的长期授权记录与续签能力描述，不用远未来日期冒充永久授权，也不删除上游 Grant 的有效期字段。升级须明确迁移原有授权记录和撤销状态，两端及 issuer 完成迁移验收后才能宣称永久授权已生效。

手动删除以服务端权威授权记录提交撤销为生效点，推进授权版本并保留最小防重／撤销记录。Gateway 阻止新请求、后续工具副作用、审批放行、订阅和对象交付，取消仍可取消的工作；issuer 同时停止为旧授权续签。不能仅停止续签、让已签发 Grant 在剩余 TTL 内继续访问。删除响应丢失时按原操作核对；离线删除显示待提交，未获服务端确认不显示已撤销。旧 Grant、缓存 manifest、延迟续签响应及进程重启均不得恢复已删除授权；重新授权须由用户显式发起并使用新授权版本。已发生的外部副作用保留核对记录，不声称已回滚。

删除自身授权后的回执核对不能依赖该授权继续有效。P1 定义独立受限的控制面入口，验证原设备身份、删除操作 ID 和 digest，仅返回该操作的撤销状态；它不得返回业务数据、签发 Grant 或恢复授权。

订阅、分块、断点恢复和下载不是授权豁免：创建时、恢复时和资源交付／变更前复核；长会话通过撤销栅栏阻止后续消息。Owner／Client／deployment 切换后不得沿用旧缓存动作、游标、审批或 transfer。普通 Owner 设置与部署级管理分离；当前主体没有管理员权限模型时，管理操作保持关闭。

授权控制面的临时故障进入有界退避；有效 Grant 可在授权未撤销的范围内继续使用。Grant 到期且尚未取得有效替代时暂停受其约束的访问／新副作用，显示暂时无法续签，不删除长期授权；服务恢复后自动续签、重新校验并核对原任务，不能自动重放未知副作用。手动删除授权则关闭相关能力且不自动恢复。续签、Relay access／refresh 和业务重试分别记录状态，不互相冒充成功。

P1 冻结权威授权记录、逐操作权限及其版本的落点。Gateway 从当前授权和已验收能力生成执行允许清单，传入共享 runtime／Policy；工具发现、实际调用、定时触发和审批续跑使用同一约束，并在副作用前复核撤销。客户端自报能力或仅隐藏按钮均不能代替此检查，不能简单移除 `TextOnly` 总开关。

### 2.3 幂等、持久化与恢复

所有写操作先生成稳定的 `operation_id`，携带资源版本及原始输入 digest；同 ID、同输入返回同一持久结果，同 ID、异输入返回冲突。保留期至少覆盖公开的重试／恢复窗口；过期返回明确终态，不能作为新写入重放。传输 message ID 与业务 operation ID 分开，重连不改变后者。

| 确认层级 | 含义 | 不能替代的证据 |
|---|---|---|
| ISCP 收包确认 | 某加密消息已被接收 | 服务端业务已提交 |
| chunk durable receipt | 对应分块已校验并持久保存 | 完整对象可用、任务已完成 |
| event applied cursor | 事件已与本地投影一起提交 | 用户已读、审批已处理 |
| execution delivery ACK | 最终结果及必需文件已在桌面可靠落盘 | 外部邮件／浏览器写入成功；后者须独立业务回执 |

业务状态与幂等回执原子提交；无法同事务的文件操作使用暂存、日志和恢复整理。事件发布在 P3 接入 durable outbox。桌面 SQLite 的投影与游标在同事务落盘，文件校验和原子移动成功后才能确认交付。新增 repository 接口必须覆盖 memory、file 和 PostgreSQL；memory 只用于进程内语义验证，重启验收必须使用持久后端。

统一可判定的错误至少包括权限拒绝／授权已删除、Grant 到期／暂时无法续签、版本冲突、能力不可用、限流、资源超限、对象过期、游标缺口和未知业务结果。只有明确可重试的错误进入有界退避；未知外部写结果进入核对状态。取消、续签和控制消息保留容量，大文件、轮询和恢复任务共享有界调度，不各自独占当前四个 RPC 槽位。

### 2.4 “没有偷偷回退到 HTTP”的定义与证明

限制的是 **SparkX 到 Gateway 的业务旁路**：选中 ISCP 时，配置、执行、文件、邮件、浏览器控制、事件和语音不得直连 Gateway HTTP／HTTPS／WS／SSE，也不得借预签名下载链接或对象存储 URL 绕开分块链路。主进程、renderer、helper、预览、下载器和子窗口均纳入检查；未知操作必须失败关闭。

Relay 自己的 HTTP／WebSocket 承载、限定的 enrollment／issuer 续签控制面，以及后端调用模型／邮件／ASR 服务或浏览器访问用户指定网站，是分别受控的合法网络用途。测试按进程、目标、路径和用途区分，不能把所有 TCP／HTTP 流量都算成回退，也不能用一个宽泛域名 allowlist 放过业务旁路。同源控制面只能放行其精确路由。

每一阶段都运行两种拓扑：正常 ISCP；从桌面侧阻断全部 Gateway 业务地址及下载地址，仅保留 Relay／必要控制面和受控工具访问的 ISCP。两者必须取得相同业务结果。再中断 Relay，确认没有出现 HTTP“救援”。统一记录各进程的出站尝试与接入端观测；`direct_gateway_business_attempts=0`，即使被拦截的尝试也判失败。HTTP 适配器注入计数只能作为一层证据，不能代替原生应用的网络边界观测。

手动另选 HTTPS 连接属于另一种连接模式，应清楚显示并重新校验身份与数据归属；本设计不在失败时自动切换。所有新增操作都走 ISCP transport；Gateway 内部复用 handler／domain service 不等于发起一次隐藏 HTTP 请求。

### 2.5 本地 Relay 调整与在线兼容

允许调整本地 Docker Relay 的轮询／持续收发、限流、队列、背压、公平调度及状态持久化。必须保留在线 Relay 支持的 ISCP 登记、PoP、加密 envelope、路由和凭据语义；不得要求在线 Relay 识别 SparkClaw 的业务操作，也不得增加客户端必须依赖的本地私有路由或字段。新增优化只能通过兼容的能力协商启用，不支持时沿用标准 ISCP 承载及其有效限额；对应业务若达不到验收要求则保持关闭，不能回退到 Gateway HTTP。

P2 前记录客户端／helper／SDK／本地 Relay 版本、修改差异，以及在线 Relay 对应的可核验版本／协议契约。用同一客户端和应用 profile 对调整后的本地 Relay 与在线兼容基线按阶段验证：P1 验证登记、握手、续签相关访问、重连和错误语义，P2 加入分块，P3 加入事件，P5 再加入音频；不让前序阶段依赖尚未实现的后序操作。在线基线可用匹配实现与契约向量隔离验证；只有实际在线测试才能记为在线部署验收。没有可核验的在线兼容证据时不能宣称兼容。保持本地联调部署，任何涉及其他项目或共享上游契约的实现变更仍须按仓库协调流程处理。

## 3. P1：配置、设置、通知等基础接口

### 3.1 范围与操作

先盘点实际设置组件的全部读取、写入和状态依赖，形成“界面项 → operation → 权限 → 实际生效证据”的清单；未映射的设置项单独保持关闭。

| 操作族草案 | 业务行为 | 权限和结果约束 |
|---|---|---|
| `capabilities.get`、既有 `presentation.*` | 基础配置、Owner、服务就绪与能力读取 | 返回当前主体投影，敏感值不随配置下发 |
| `settings.get/update`、`owner.update` | 语言、Owner 偏好、已支持的运行参数和连接器设置 | 按设置资源授权；写入带 `expected_revision`，返回 stored／effective revision |
| `credentials.status/set/delete` | 凭据状态、写入和移除 | 密钥只写不回显；部署级凭据必须有明确管理权限 |
| `authorizations.list/delete` | 长期授权查看及用户手动删除 | 当前主体权限范围内操作；删除带授权 revision 和幂等 ID，返回权威撤销结果，不能用清除本地凭据代替 |
| `notifications.list/read/read_all` | 分页通知、已读及未读计数 | Owner 隔离；`read_all` 固定到提交时的水位，不吞掉后来通知 |

本地主题、草稿和桌面本地设置继续写本地库，不伪装成 Gateway 远程配置。影响当前连接的设置要先持久保存可恢复状态，再重建连接；不因设置变更丢失原 execution 的跟踪。

P1 通知采用有界分页及低频轮询，明确资源 revision／snapshot token；没有事件流依赖。P3 再以事件唤醒同一读取模型。通知内容的接收、桌面通知是否获 OS 许可、用户是否已读是三个独立状态；审批提醒中的动作在 P3 前禁用。

配置写入后检查真实 runtime 使用新值；如果需要重启，返回 `restart_required` 和尚未生效的 revision，不显示“已生效”。凭据可用性用受控的真实服务请求验证，日志只记录凭据标识和结果。设置了工具策略不等于已开放该工具。

### 3.2 开放前验收

- **权限：**普通 Owner 与管理员边界、跨 Owner 读取、伪造 Client、只读凭据回显、撤销后继续写入均有反例；合法主体必须成功完成对应设置。
- **长期授权：**跨原二十四小时边界、多个 Grant 周期及双方／issuer 重启后仍保留授权；短周期测试与可控时钟证据须分别标注。手动删除后旧 Grant、延迟续签响应、旧缓存和重启均不能恢复访问；删除并发／丢响应、离线待提交及显式重新授权均可核对，原有已撤销记录不能被迁移复活。
- **业务：**设置保存、重启后回读及实际运行行为一致；至少一条真实业务产生的通知可显示、标已读，未读计数和后端一致；新通知不被旧 `read_all` 清掉。
- **恢复：**写入提交前／后丢响应、重复 operation、并发 revision 冲突、客户端和 Gateway 重启，最终只有一次变更；通知分页中断没有遗漏或重复已读副作用。
- **传输：**阻断直连 Gateway 仍能读写设置和通知，Relay 断开只呈现不可用；零 HTTP 旁路尝试后，逐项开启设置和通知 UI。

## 4. P2：文件、图片、附件、生成产物与分块

### 4.1 统一对象与分块协议

增加 `transfer.open/status/chunk/commit/abort`、`object.describe/read/release`；大 context、execution 最终 JSON、普通文件、图片、邮件附件和后续事件快照共享同一有界对象传输层。小控制消息继续 RPC，大内容引用已提交对象。对象用途必须显式声明，禁止任意服务器路径读取。

| 记录 | 最小字段与不变量 |
|---|---|
| 对象 manifest | `object_id`、资源归属、用途、名称、媒体类型、总字节数、完整 SHA-256、版本、保留期限 |
| transfer | 稳定 `transfer_id`、方向、对象版本、授权绑定、分块尺寸、credit window、到期时间；独立于会话 ID |
| chunk | `transfer_id`、index、offset、length、SHA-256、bytes；范围不能重叠或越界，同位置不同内容必须拒绝 |
| checkpoint | 已持久确认的范围／分块集合、已确认字节数、状态；查询分页有界，不回传无限 bitmap |
| commit receipt | 完整对象 hash、size、版本、持久化结果；重复 commit 返回同一结果 |

接收方通过 credit 限制在途数据，发送方不得超过窗口；落盘并校验后才补发 credit。双方限制单对象、单 Owner、总磁盘、并发 transfer 和空闲时间。断线后重新授权并查询 checkpoint，只补缺块；校验失败补传对应块，完整 hash 不一致不得 commit。取消及到期清理临时文件、配额和引用，进程重启后根据日志回收孤儿。

初始候选值为 8 KiB 原始 chunk、每 transfer 最多两个在途 chunk；这些是待实测的传输默认值，不是业务限额。上线前在 SDK 加密、JSON/base64、helper IPC 和真实 Relay envelope 每一层计算最终大小，再测试上界，必要时降低 chunk。不能用现有 64 KiB 明文上限推断 Relay 能接收同等净载荷；禁止把整个文件 base64 塞进单条 RPC。

Relay 容量验证前置到 P2：当前 reference 的每 IP 每分钟 120 次共享请求限额和约一秒轮询会同时消耗文件及控制预算，不能仅以“小文件成功”冻结分块参数。在目标网络测量各用途最大允许文件的吞吐、完成时间，以及传输同时进行的聊天、取消、续签和通知延迟；提前冻结通过阈值并记录限流／重试开销。按第 2.5 节调整本地 Relay 或协商更低有效限额，不把承载改进拖到 P5。

业务预算仍以 [workbench-limits.json](../../configs/workbench-limits.json) 及目标 domain service 为准：当前通用 `fileBytes` 为 64 MiB，而 execution 输入单文件和结果总预算受 8 MiB `ResultBytes` 约束，结果文件数最多 32；不能把通用文件限额误当 execution 附件限额。context 当前最多 1 MiB。协商返回各用途的有效最小限额，分块不会提高这些限额。超过限制返回可见终态，不无限 lookup。

### 4.2 接入业务与界面

上传必须先完成对象校验／提交，再提交引用该版本与 digest 的 execution；原 request ID 保持不变。大输入先传原始序列化内容，服务端按原字节验 digest，不能重新 JSON 编码改变请求身份。execution lookup 在 v2 返回状态与结果 manifest，大结果及文件分块下载，全部必需结果可靠落入 SQLite／文件目录后才发送原 execution ACK。

桌面持久区分上传中、对象已提交但执行尚未提交、执行提交结果未知和已受理。上传恢复不等于执行重试，不能在上传前就消耗执行提交标记。发送前先完成权限／容量准入，再持久记录可能发送的边界；日志能证明从未尝试执行提交时，可在恢复上传后按原用户意图与原 request ID 继续，定时任务仍遵守到期离线不补跑规则。跨过可能发送边界后只核对原请求，lookup 404 不能证明从未执行，也不能触发自动重发。覆盖对象 commit 后／submit 前和发送边界前后的崩溃用例。

以暂存文件、完整 hash 校验、原子 rename 和本地提交日志解决文件与 SQLite 的跨介质提交。桌面崩溃在 rename 后／ACK 前时能够核对并重发 ACK，不能重跑生成任务。对象在 execution 未完成交付前必须被引用保护；恢复窗口结束后按明确的结果到期策略处理，不静默删除仍承诺可恢复的数据。

预览从受控本地 scheme／缓存读取；校验文件类型、图片尺寸和解码预算，阻止路径穿越、任意 HTML 脚本及远程资源加载。下载选择本地位置不改变远端对象权限。文件接收、图片预览、图片模型理解和生成工具是独立能力；传输图片成功不等于模型已支持图片。

P2 验证真实 execution 产物提交、落盘、下载和 ACK 路径，可用可重复生成样例经过相同产物提交入口；不因传输通过开放全部生成工具。需要审批的文件操作等待 P3，通用文档／图片生成工具的真实调用及其入口等待 P4。已存在产物的交付能力可以独立验收和开放，避免传输底座反向依赖全部工具。

### 4.3 开放前验收

- **权限：**跨 Owner／installation／execution 的对象引用、越权下载、伪造路径、过期 transfer、撤销后 resume／commit 被拒绝；合法上传和下载必须成功。
- **业务：**空文件、多块文件、图片、多个附件及真实落盘产物逐字节／SHA-256 相等；正好等于限额与超限、损坏块、伪造媒体类型、磁盘满均覆盖。生成结果须核对 execution manifest 和本地实际文件，不能只检查文件名。
- **恢复：**缺块、乱序、重复块、提交响应丢失、双方重启、Grant 更新及会话替换后仅补缺块；commit／生成／交付各一次；取消后空间和配额释放，结果过期明确可见。
- **传输：**关闭所有 Gateway 文件／artifact 地址及远端图片 URL，仍可上传、渲染和下载；网络证据零旁路后，分别开放文件选择、图片预览、附件和产物下载。
- **容量与兼容：**各用途上限文件通过预先冻结的完成时间和控制响应阈值；同一客户端通过第 2.5 节的在线兼容基线。只在修改后的本地 Relay 成功不足以开放能力。

## 5. P3：审批、执行进度与事件同步

### 5.1 统一事件模型

新增 `events.subscribe/resume/ack/unsubscribe`、`state.snapshot`、`approvals.list/get/decide`；execution lookup／cancel／ack 保留原业务含义。事件包含 `event_id`、授权范围、stream generation、单流递增 sequence、资源 ID／revision、事件类型及有界内容或对象引用。只承诺同一范围内排序，不虚构跨 Owner 全局顺序。

业务状态提交与 outbox 记录原子持久化，事件至少投递一次；桌面按 event ID／资源 revision 去重。投影和 applied cursor 同事务提交，再确认游标。慢客户端使用 credit／分页与限额，不阻塞 execution；日志保留期和允许的最大断线时间作为公开配置，过期游标返回 `cursor_gap`。

事件订阅不能长期占住普通 RPC 槽位；使用有界的逻辑事件通道及独立控制预算。当前 reference Relay 可继续以队列轮询承载这些可靠事件，验收需记录实际通知延迟，不能据此承诺音频级实时性。即使暂时没有订阅消费者，业务状态仍须正常提交并可经 lookup／快照恢复。

恢复先重新授权，再从最后持久游标续读。出现 gap 时取得带一致水位的快照，原子替换对应远端投影，再消费水位之后的事件；不能用“先读状态再订阅”留下竞态窗口。大快照走 P2。重置只影响对应远端状态，不覆盖桌面本地草稿与历史。通知的 P1 轮询在此改为事件唤醒与周期核对，不能并行启动第二套同步状态机。

### 5.2 审批和执行语义

审批决定绑定原 execution、approval ID、动作／参数 digest、revision、到期时间和决定者身份。按钮显示不代表可提交：服务端复核当前审批仍 pending、内容未变、主体仍有权限，并原子提交一次决定。重复同一决定返回既有结果；不同决定或旧 revision 冲突。过期、撤销、执行取消后不得放行。

等待审批时 Gateway 重启，原任务必须终结，不恢复审批等待或 continuation，不自动重新提交。展示审批前持久记录最小等待阶段／审批身份／digest；重启恢复在接收新审批决定前，先将遗留等待任务持久标为终态，保留可查询的重启终结原因，并使旧审批失效。桌面经 lookup／事件更新卡片，说明任务因 Gateway 重启终结；普通桌面断线或重启而 Gateway 未重启时，仍可重新验证并展示原有效等待。

审批决定与重启／终结竞争时按持久 revision 收敛，拒绝旧按钮与延迟决定。决定已提交但执行结果未知时保留原决定回执和副作用核对记录；终结不能伪装成此前没有副作用，也不能从头重跑。重启续跑不再是本阶段交付项，不为恢复等待而持久保存完整运行上下文。用户另行发起任务使用新 request ID 和新审批；修改参数仍须重新计算 digest。真实副作用继续由共享 Policy／execution service 执行。

进度可包含阶段、工具状态、partial 文本和文件准备状态，但“收到最后一条进度”不等于任务完成。终态以持久 execution 结果为准，最终交付仍按 P2 落盘后 ACK。取消与完成竞争按服务端 revision 收敛，旧事件不得把终态倒退成 running；进度丢失时 lookup 必须能够恢复真相。

### 5.3 开放前验收

- **权限：**跨 Owner 订阅和审批、旧主体游标、过期或内容变化的审批、权限撤销后的继续订阅全部拒绝。
- **业务：**受控的真实需审批操作批准后只执行一次；拒绝／到期后无副作用；执行进度、最终结果及后端状态一致，取消确实停止可取消工作。
- **恢复：**提交决定后丢响应、审批等待时 Gateway 重启及其与决定提交的竞争均收敛；重启终结可查询，旧审批不能执行，不自动续跑或创建替代任务。桌面独立重启不误终结仍有效的等待。事件重排／重复／丢失、ACK 前后崩溃、cursor gap 和快照期间新事件不导致重复审批／执行。
- **传输：**阻断 Gateway SSE／WebSocket 仍能取得审批、进度和补齐事件；零旁路后开放审批动作和实时状态 UI。

## 6. P4：邮件、浏览器及其他工具业务

### 6.1 邮件

操作族为 `mail.mailboxes/sync/message/attachment` 和独立受控的 `mail.send`。复用现有 mail sync projection、同步游标和绑定 generation；邮件以后端为事实来源，桌面只保存本主体的缓存。列表分页、正文按需获取，原始邮件／附件走 P2，新增／变更／删除和绑定撤销通过 P3 同步。同步缺口不能伪装成空收件箱。

沿用[邮件增量同步设计](email-timeline-incremental-sync-design.md)与[原文存储设计](email-local-download-storage-design.md)的服务商证据、重试和去重规则；不以更换传输为由重写邮件采集。富排版仍须屏蔽远程图片、追踪资源和脚本。

网络入口清单还要覆盖邮件采集器、浏览器扩展后台、宿主 broker 和下载回调，不能只替换工作台的 fetch。位于桌面侧的这些模块向 Gateway 提交采集结果、附件和回执时也必须经过同一 ISCP 适配；它们访问服务商或受控本地 IPC 的用途另行登记。

读邮件、写本地草稿、发送邮件分别授权。发送将收件人、正文及附件 manifest 绑定到审批；审批后改任一项均失效。业务 receipt 保存服务商可核查标识；请求已发但回执丢失时先核对，无法证明未发送则呈现未知结果，禁止自动再发。每个服务商单独验收读取、附件和发送，不共用一个“邮件支持”开关。

### 6.2 浏览器

操作族为 `browser.host.grant/revoke`、`browser.command/receipt/reconcile` 与浏览器状态事件。这是 Gateway 经同一认证 ISCP 会话向桌面宿主发送受限命令的反向链路，需要单独的方向／schema／权限白名单，不等于 responder 获得任意本机控制权。

复用现有 browser host broker、Owner Controller、任务 tab 所有权和 write fence；grant 绑定 host、installation、会话／任务、允许动作、tab 及有效期。截图／下载／导出文件走 P2，进度／授权／命令状态走 P3。浏览器读取与登录授权、点击外部写动作独立控制，撤销必须停止后续命令。桌面已有 URL、cookie、登录态不能作为任意任务的默认权限。

命令结果丢失先按原 command ID 核对 fence；不能重放发送、下单等未知写动作。单纯重载页面不构成核对写入结果。断线释放控制 lease；重新连接不能自动控制未重新确认所有权的 tab。受控页面访问外网是工具业务流量，桌面到 Gateway 的控制／截图上传仍只能走 ISCP。

### 6.3 其他工具

从现有 ToolHub typed registry 派生工具能力清单，逐个声明设置／凭据、对象传输、审批、事件和宿主依赖。适配层调用共享 runtime 与 Policy，不建第二套工具执行器，不单独放开任意 shell／URL／filesystem RPC。文本结果、大文件结果和外部副作用各自采用对应恢复规则。

至少分为纯读取工具、文件／文档工具、外部写工具、宿主工具四类验收。同类通过不代表全类开放：例如一个查询工具通过，不能证明邮件发送和文档生成可用。未配置的 provider 或没有可验证业务结果的工具持续关闭。

### 6.4 开放前验收

- **权限：**跨邮箱、旧 binding、未授权 browser host／tab、被禁止的工具，以及更换内容后的旧审批均拒绝；只读主体不能借工具执行获得写权限。
- **业务：**各声明支持的服务商真实同步一封邮件及附件；发送到受控测试邮箱并核对实际收件；浏览器在实际宿主页完成读取、截图、一次经批准的可逆写操作并核对页面结果；每个工具检查真实输出文件、数据或服务商回执。blocked／mock 不算正向通过。
- **恢复：**同步翻页、附件下载、browser command 回执和外部写响应丢失，客户端／Gateway 重启及授权撤销后均按原业务 ID 核对；明确展示无法判定的外部结果，不重复发送或执行。
- **传输：**在保留服务商／受控网站访问的前提下阻断 Gateway 业务入口；邮件、浏览器宿主连接、截图和工具结果无直连 HTTP／WS。按 provider／host／tool 开放对应入口。

## 7. P5：语音与实时音频

### 7.1 先录音转写，再实时流

P5a 增加 `speech.status/transcribe/cancel`：用户启动录音后，音频经 P2 上传至已授权转写任务，通过 P3 跟踪并返回实际 ASR 文本。麦克风授权同时检查 OS、可信工作台窗口和当前 connection capability；后台页面／普通浏览器 tab 不自动继承权限。转写进入草稿，不自动发送聊天。录音的最长时长、格式、体积和删除策略均明确配置。

P5b 增加 `audio.session.open/control/close`、带序号与时间戳的 audio frame，以及 `speech.partial/final` 事件。协商 codec、采样率、声道、frame duration、窗口、最大缓冲、空闲超时和 session deadline；控制、音频和大文件分别有容量预算。partial 使用 revision 替换，final 是同一 ASR 会话的权威结果，不追加出重复文本。

当前 pinned reference Relay 是队列 drain 后关闭、适配器约一秒后再轮询的实现。P2 的容量改进和既有文本验收均不能自动证明持续低延迟、双向流或公平调度可用。P5b 前必须按第 2.5 节的在线兼容约束，在本地 Docker 中验证或实现持续收发、背压和优先级，记录 SDK／Relay 版本及变更；验证失败则实时入口继续关闭，不能私接 Gateway WebSocket。P5a 不受实时流验收阻塞。

### 7.2 音频中断与恢复

文件传输追求可靠补齐；实时播放帧有时间有效性，迟到帧应丢弃。ASR 输入如果出现不可补齐的缺口，必须明确停止／降级该录音，不能把缺失音频当作完整语句。断线立即停止采集并释放设备，不自动重新打开麦克风；只核对已结束 session 的 final。新录音需要用户重新启动，旧 session ID 和音频帧不能混入新会话。

现有 WebChat 语音的 HTTP batch fallback 不能直接复用到 ISCP 模式。只有 P5a 已验收、完整录音确实保存且用户已知晓该恢复行为时，才可对同一次录音经 ISCP 做一次文件转写；它须标记为录音转写，不能称实时恢复。录音不完整则清楚说明，不生成冒充完整的 final。转写 operation 和草稿插入均幂等，恢复不能再次提交聊天。

P5c 的 TTS 播放／双向音频另设 capability，先确认实际 provider 支持，再验证首包、播放、打断和旧帧清理；不因 ASR 通过就宣称已支持语音对话。业务许可、OS 音频许可和可见的录音／播放状态保持一致。音频内容不进入 Relay 日志，临时音频按披露的保留期清理。

### 7.3 开放前验收

- **权限：**OS 拒绝、非可信窗口、没有 speech scope、撤销中的录音、跨 session 音频注入均拒绝；停止或断线后物理麦克风指示关闭。
- **业务：**真实麦克风与真实 ASR 在停止前产生 partial，停止后得到同会话 final；固定语料记录准确性、长录音、噪声和语言覆盖。P5a 单独验证完整录音实际转写；P5c 单独检查实际扬声器播放与打断，不以收包计数代替听到声音。
- **恢复：**录音／finish／final 丢响应、Grant 续签、抖动、慢消费、网络中断及系统休眠均有明确收敛；不自动重新录音，不播放过期帧，不重复插入 final 或发送聊天。
- **传输：**禁用 Gateway speech HTTP／WS、远端音频 URL 后验证全链路，统计音频各方向字节；零旁路后才分别开启录音、实时 ASR 和播放／双向能力。

实时初始验收目标（待在实施前按目标设备冻结）：录音中首个 partial 的 p95 ≤ 2 秒、finish 到 final 的 p95 ≤ 3 秒、待确认音频目标 ≤ 2 秒且硬上限 5 秒、停止／取消到本地采集与播放停止 ≤ 300 毫秒。必须记录语料、设备、模型、网络和至少 30 次正常样本；超限按失败／显式降级处理，不能扩大无界缓冲。故障样本单独报告恢复时间，不混入正常延迟统计。TTS／双向语音还须单独冻结端到端播放与打断指标。

## 8. 代码落点与交付包

| 位置 | 计划变更 |
|---|---|
| `services/gateway/internal/iscpworkbench` | v2 operation registry、协商、typed 错误、分块／窗口、流复用和会话恢复；保持与业务解耦 |
| `services/gateway/internal/iscpbridge`、`internal/iscplocalissuer`、`cmd/iscp-workbench` | 复用 SDK 身份；实现长期授权、短期 Grant 续签与删除后的撤销一致性；扩展有界 helper IPC 及脱敏诊断 |
| `services/gateway/internal/gateway/workbench_iscp.go` | 显式 operation → 现有 domain service 的适配与逐请求权限；不得成为通用 HTTP proxy |
| `services/gateway/internal/execution` 及各 domain repository | 引用对象、业务幂等、最小审批等待记录及重启终结、outbox／快照和外部结果核对；同步三种 store 实现 |
| `docker/images/iscp-relay-local.Dockerfile`、`scripts/lib/iscp-docker-lab.mjs` 及兼容性夹具 | 固定本地 Relay 改动／版本，验证吞吐和在线协议兼容，不混淆隔离兼容证据与在线部署验收 |
| `apps/desktop/src/main/iscp-transport.mjs`、`desktop-auth.mjs` | 能力投影、调度、恢复状态和严格的 transport 选择 |
| `apps/desktop/src/main/client-store.mjs`、`execution-client.mjs` | transfer checkpoint、文件提交日志、事件投影／游标、原 request 结果恢复 |
| `apps/desktop/src/main/mail-sync-*.mjs`、浏览器宿主与权限模块 | 复用邮件缓存及宿主授权；替换其 Gateway 网络入口并补齐音频权限 |
| `apps/webchat/src/desktop/LocalWorkbench.tsx` 与共享组件 | 从统一 capability 投影逐项开放入口，展示失败／恢复／未知结果，删除已替代的硬编码限制 |

每阶段提交一套 operation／scope／错误清单、版本与存储迁移方案、客户端及服务端实现、正常和故障回放用例、原生 UI 验收记录。迁移需保留既有本地历史和原 request 追踪；回退前关闭新增能力，不让旧代码读取不兼容状态，更不能借回退把连接切成 HTTP。

## 9. 统一验收记录与发布规则

每个 capability 的记录必须列出：客户端源码／包 hash、helper／SDK／Relay／Gateway 版本、profile、脱敏主体与权限、业务 operation／request ID、预期结果与实际结果、持久状态／文件 hash／服务商回执、故障注入点、恢复后状态、业务执行次数、各进程旁路尝试数和 UI 门禁结果。凭据和正文不写入公开验收日志。

| 门禁 | 必须达到的证据 | 未通过时 |
|---|---|---|
| G1 权限 | 合法请求真实成功；长期授权不自动到期；无有效 Grant、授权已删除及越权恢复被拒绝 | 对应能力关闭 |
| G2 实际业务 | 原生 SparkX 经本地 Docker Relay 到真实 Gateway，持久结果／外部实际结果可核查 | mock 或隔离测试只能记为前置证据 |
| G3 断线恢复 | 提交前后、持久化前后、重启／续签／撤销的状态收敛；无重复副作用 | 不开放 UI，保留可诊断状态 |
| G4 无 HTTP 回退 | 业务地址阻断后正向成功；Relay 故障无旁路尝试；所有进程观测为零 | 整个对应能力判失败 |

先进行契约及故障注入测试，再运行独立 Docker Relay 集成，最后完成已安装原生 SparkX 的正向业务验收。default file backend 必测；新增 store 契约覆盖全部后端。测试候选能力可由受控测试配置开启，但面向用户的发布开关只有 G1–G4 全通过才能开启，不能把测试开关随包默认发布。

初始所有 P1–P5 新能力均为 `planned`。后续逐项记录 `implemented → isolated_verified → native_verified → enabled`，回归失败立即降为不可用并保留数据。停用／撤销后的任务按既有业务状态核对，禁止偷偷重发或切换传输。此前文本问题在相关发布门禁中若复现，仍属于未通过，不能被本文的路线规划豁免。

## 9.1 首批实施记录，2026-10-09

本批实现前置修复和部分 P1，不代表 P1 完成，也不开放新的业务界面。

| 范围 | 已实现及隔离证据 | 发布前仍需完成 |
|---|---|---|
| 长期授权 | Issuer 状态 v2、明确永久策略、helper profile 固定策略、签名且绑定设备证明的状态、有限 Grant 和撤销栅栏；控制时钟跨两年、重启及截短 Grant 迁移测试通过 | 产品授权管理、删除操作回执／离线核对、显式重新授权、已安装两端迁移 |
| 文本恢复 | 明确未发出的准入保留原草稿；上传暂存先于提交；短 Grant 到期重试；lookup 413 持久为 `delivery_too_large` 并显示说明 | 原生回归验收；未启用分块或新增文件操作 |
| 审批关闭 | 最小审批记录持久化、决定落盘后才继续、`gateway_restarted_awaiting_approval` 终态；先前已批准操作的副作用保留 `unknown` 和回执 | P3 传输操作、事件、审批界面继续关闭 |
| 撤销 | 分发／交付前验证最新 issuer 状态、取消执行中请求、同锁 Client 准入栅栏、持久保留原请求终态 | 通用工具副作用、订阅和对象交付随各阶段分别验收 |

新建 lab 使用 `-authorize-renewal -authorization-hours 0`，两端 helper profile 固定 `authorization_lifetime: until_revoked`。旧 v1 有限期授权仍可读取，服务启动不会静默迁移。显式迁移保留当前已签名 Grant 的 TTL，包括旧授权末期被截短的 Grant；已撤销记录拒绝迁移。续签回执保留到对应 Grant 到期后七天，清理后过期证明不能再次签发 Grant。Issuer CLI 的 `-revoke-renewal` 保留持久撤销标记，且只推进一次版本。

私有 issuer 路由 `/v1/authorization-status` 绑定新设备证明、请求摘要及固定 issuer 签名；包括撤销后也只返回授权状态，不是设计中的删除操作回执 API。未签名错误不能永久撤销授权。续签与状态请求分别退避。当前状态验证失败时暂停新业务分发／交付；后台长期授权检查的间隔至多十秒，另加请求耗时或状态端点退避。这是保守的本地文本实现，不声称已实现所有未来工具的原子撤销。

Execution 控制存储从 v2 迁移到 v3。**旧二进制不能读取 v3。** 回退不能丢弃账本或恢复旧请求栅栏，须使用兼容读取器／迁移方案。Issuer 状态 v2 同样要求新版 issuer。这两项存储变化不修改上游 Grant、Relay envelope 或严格的 v1 操作 manifest；本批未修改本地 Relay 源码或 SDK 依赖。

验证：授权／execution／Gateway 定向 race；桌面 158 项；WebChat 220 项及生产构建；本地 lab 六项，包含真实固定版本 Docker Relay、实际 Gateway 执行、helper 重连和零 Gateway 直连 HTTP 调用。Docker 模型明确为 mock。控制时钟的永久授权及注入网络的撤销测试不是真实两年持续运行、原生应用验收或在线 Relay 验收。隔离 Linux 全量 Go 检查及 Go build/vet 通过；macOS 基线中缺少 `/dev/shm` 和路径符号链接导致的失败由 Linux 环境验证覆盖。

以上是首批检查点，9.2 节取代其待实现清单。未升级已安装 SparkX 或运行中的部署。配置及祖先锚点均未找到 InfiniCenter，因此未修改跨项目契约或中枢状态。

## 9.2 全阶段实现与验收，2026-10-09

本节为首个全阶段实现检查点。浏览器、授权删除与附件发送的后续验收状态以 9.3 节为准。

P1–P5 复用既有领域服务，没有另造执行器，也没有任意 HTTP 隧道。唯一操作清单为 `services/gateway/internal/iscpworkbench/operations.json`，用 `node scripts/sync-iscp-operations.mjs --check` 检查桌面投影。两端私有 helper 配置必须明确选择 `["sparkclaw.workbench.transport.v2", "sparkclaw.workbench.transport.v1"]`。`qualified_capabilities` 填逐个操作名，默认空；签名授权 scopes 与部署验收资格相互独立。旧配置、原 Grant 接入新 responder 时仍严格保留 v1 九操作。

| 阶段 | 已实现 | 证据及仍需区分的验收 |
|---|---|---|
| P1 | 绑定会话且有到期时间的能力清单、签名精确权限、当前设备永久授权、设备证明绑定的删除与回执查询、显式重新授权、Owner／连接器／凭证 CAS、加密修改回执、通知水位 | 真实 issuer／加密链路／默认 file Gateway 测试；原生设置界面保存与读回、Keychain；未配置真实 provider 的凭证不声称可用性验收 |
| P2 | 共用私有对象存储、8 KiB 分块与两份额度、hash、持久检查点续传、配额与清理、执行输入输出、大型 typed JSON | 真实 Docker Relay 8／64 MiB 容量实测；半途签名 Grant 到期并续期后继续原传输；原生文件上传／执行结果／ACK；修改版与未修改参考 Relay 兼容 |
| P3 | 持久审批版本／摘要／决策回执、重启终结、持久有界事件窗口、加密游标与 outbox、SQLite 投影和游标提交后 ACK、缺口重建 | 审批／拒绝／重放／重启／丢响应测试；真实受控文档读写及产物；三后端事件窗口、epoch、丢 ACK 与保留期缺口 |
| P4 | 后端权威邮件缓存／附件读取／版本化草稿、明确发送确认与结果核对；既有 Browser Broker 的 ISCP poll；精确工具白名单传入共用 ToolHub／Policy | 真实文档工具、受控邮件接收端与持久回执、原生 Chromium 读取／填写／点击／截图与丢失写回执栅栏。真实邮件 provider 和浏览器展示配置分别验收。现有邮件 provider 不支持发送附件清单，因此附件发送明确保持不可用 |
| P5 | 录音 WAV 对象、持久请求回执与取消；有界 PCM 帧、provider ACK、partial／final／cancel 与撤销取消 | 受控 provider 协议测试；真实麦克风／语料／延迟依赖该部署配置 ASR。现有系统无回放／双向 provider，明确返回 `provider_unsupported` 并保持关闭 |

删除永久授权走 helper `authorization_delete`／`authorization_delete_receipt` 控制 IPC，用原设备 PoP 访问固定 issuer；Relay／Grant 不可用后仍可核对。`authorizations.list` 只返回当前设备的新鲜签名授权，Gateway 无权冒充设备删除；通用 `authorizations.delete` 返回 `device_authorization_control_required`。桌面先持久化原删除 ID 与预期 revision。重新授权需要 issuer 操作员明确执行 `-reauthorize-permanent -expected-revision … -operation-id … -authorization-scopes … -grant-file …`，两端导入新 Grant，再由用户点击“检查新安装的授权”。续期和重启均不能扩大 scopes 或恢复删除的授权。

本机 Docker Relay 实测（每个尺寸各上传／下载一次，控制请求全程采样）：

| 内容大小 | 上传 | 下载 | 控制请求 p95 |
|---|---:|---:|---:|
| 8 MiB | 38.63 秒 | 44.57 秒 | 两种尺寸全程 41.49 毫秒 |
| 64 MiB | 353.40 秒 | 403.58 秒 | 同一控制请求样本序列 |

两个下载文件的 SHA-256 均与输入相同。事先固定门槛为 8 MiB 每方向 90 秒、64 MiB 每方向 600 秒、控制 p95 2,000 毫秒。首轮 64 MiB 暴露五分钟后未派发能力刷新 worker 的缺陷；已补真实经过到期时间的回归测试并完整重跑通过，没有放宽门槛。原生设置／文件／事件／重连 fixture 的 Chromium NetLog 与 HTTP／WS 阻断记录中，直连业务尝试数均为 0。

验证：Linux Go 65 个有测试的包通过（另六个包无测试），Go build／vet、针对性 race、真实 PostgreSQL 18 CAS／事件窗口／重新连接测试、Desktop 186 项、WebChat 227 项与构建、两个生成契约检查、110 份双语文档镜像检查通过。原生应用使用 Electron 44.4.3／Chromium 152.0.7977.130。Mac arm64 未签名候选 ZIP 已构建，未覆盖现有安装，SHA-256 为 `f9de06ab7f5b8bfa4d272b27feb8ba795d407f61127dfdbdb046f337b974aff1`。原生业务测试仍受其明确标注的受控模型／provider 范围限制。

独立 Browser Host 原生 fixture 使用真实 `WebContentsView`、生产 Broker 和适配器，通过固定 issuer／本地 Relay 加密轮询宿主。受控页面读取、填写和截图成功，PNG 为 18,609 字节；实际点击生效后丢失回执，两端账本各保留一条 `unknown`，重复该命令被拒绝，点击计数仍为一。Gateway HTTP 直连和 Host WebSocket 尝试均为零。macOS fixture 明确激活应用／Dock，并关闭窗口遮挡和 renderer 后台节流，使合成器能产生帧。这只验证该前台 fixture 配置，不代表生产应用默认后台设置的截图行为已经验收。[机器可读验收摘要](../../docs/evidence/iscp-expansion-2026-10-09.json) 同时保留这些限制与源码／产物 hash。

存储与恢复：执行控制 v3、issuer 状态 v2、桌面 SQLite v8（v6／v7 迁移保留本机历史）。旧程序不得打开新版账本。对象和执行结果保留 24 小时，未完成上传一小时过期。领域回执使用加密有界账本（65,536 条／512 MiB），在产生副作用前预留结果空间。普通文件／邮件附件最大 64 MiB，执行输入／结果 8 MiB，执行上下文 1 MiB，单任务暂存 32 MiB。录音受实际 ASR 上传配置与 25 MiB 上限共同约束。原生截图保留 64 KiB capture、96 KiB reply JSON 限制。能力清单公布实际业务上限，不把普通文件容量当成所有领域容量。撤销后后续交付在授权检查处被拒绝；磁盘对象按有界清理器的 TTL 清除。

本地 Relay 优化通过 `prepare-expansion --capacity-relay` 明确启用。构建器复制 checksum 锁定的 ISCP `v0.2.0-rc.1` 到私有 lab，记录修改前后源码及二进制 hash，只调整 local-lab 调度与速率；不修改模块缓存、SDK、PoP、注册、envelope、路由或 message／drained 帧。流连接检测关闭的 peer，避免废弃连接吃掉后续消息。未修改的参考 Relay 已通过 v2 设置／分块／重连和严格 v1 检查。这是隔离协议兼容证据，未测试或改动在线部署。

复现命令（路径必须为独立的私有测试目录，不得指向现有部署）：

```sh
node scripts/build-iscp-helper.mjs
node scripts/iscp-local-lab.mjs prepare-expansion --input /private/path/input.json --directory /private/path/lab --capacity-relay
node scripts/iscp-local-lab.mjs up --directory /private/path/lab
node scripts/iscp-expansion-capacity.mjs /private/path/lab
node scripts/iscp-expansion-compatibility.mjs /private/path/reference-lab
SPARKCLAW_ISCP_NATIVE_LAB=/private/tmp/native-lab node_modules/.bin/electron scripts/iscp-native-expansion-fixture.mjs
node apps/desktop/test/run-host-iscp-native-qualification.mjs /private/tmp/browser-host-lab
```

兼容测试单独准备不带优化的 `prepare-expansion` lab；原生界面另用全新 capacity lab。Browser Host runner 需要自己的已准备 capacity lab，且 lab Gateway 处于停止状态；它自行启动真实 Gateway 测试进程，固定证书的 HTTPS 监听只提供受控页面与测试驱动路由。一个 Client 绑定一个持久 installation，不得让互不相关的桌面 Store 并发共用 lab。原生工作台 fixture 导入真实应用入口，操作设置界面和沙盒 IPC，保存截图与 Chromium NetLog，在 Gateway 业务端口不发布的条件下检查执行交付；还会阻断原生直连 HTTP／WS，暂停 Relay 后验证重连不会重新提交。该 fixture 明确使用 mock 模型，不代表真实模型语义、邮件 provider 或麦克风识别验收。测试候选开关不是生产发布开关。

发布状态：实现代码与隔离候选可用于最后验收。真实邮件／ASR provider、部署实际使用的浏览器展示配置及逐个受控工具必须有各自正向业务证据才能在生产开放。不支持的邮件附件发送及 TTS／双向语音保持关闭，音频不会冒充 ASR 或回退 HTTP。现有安装、远端部署与在线 Relay 保持原状。配置锚点仍无 InfiniCenter，因此不声称中枢批准，也未改动共用上游协议。

## 9.3 非语音补充验收与仅限 workspace 的附件，2026-10-09

用户明确暂缓 ASR 与 TTS 验收，其既有 provider 开放条件保持不变；普通回归测试通过不代表麦克风识别、播放或双向语音验收。其余非语音实现与本地验收补充结果见[机器可读证据](../../docs/evidence/iscp-workspace-mail-2026-10-09.json)。

**数据边界纠正：**workspace 指发起请求的 SparkX 安装在桌面宿主机上的工作台数据，沿用[收敛设计](workbench-runtime-convergence-design.md)的既有定义，并非 Gateway Owner workspace。此前基于 Gateway 路径的实现及其附件验收证据已被替代；这是实现理解错误，不是用户新提出的数据边界。

邮件编辑器从 `<userData>/workbench/files` 已归属于当前作用域的本地文件中选择文件 ID。主进程经 ClientStore 解析 ID、校验登记的文件名／大小／哈希，再使用既有加密 ISCP 对象传输提交字节。拒绝任意路径、未归属文件、符号链接、硬链接和非常规文件。最多五个文件、合计 10 MiB。保存的草稿包含本地文件 ID、已校验的对象引用、名称、大小与 SHA-256。确认绑定该保存版本及清单，主进程发送前再次核验本机文件；文件变化、删除或不可用时必须显式重新选择、保存并审阅。存在结果未知的发送时，先核对原回执，不再次读取或上传本机文件。

Gateway 只接受当前 deployment、Owner、Client、installation 与授权 revision 绑定且已经完整提交的 `mail_send_attachment` 对象，并要求明确的 `files.read` 权限。元数据以对象存储实际记录为准，绝不按源文件路径读取。对象保留最多 24 小时的绝对到期限制；到期、释放或绑定变化都不能回退读取 Gateway 文件。永久授权不延长临时文件的保留期。

Gateway 将已校验的传输字节冻结到私有发送目录，仅供 provider 运行时使用；此临时副本不授予读取 Gateway workspace 源文件的权限。应用运行时校验冻结副本，使用内存字节、有界且脱敏的分块和浏览器 File／DataTransfer 文件输入路径上传，不把路径交给浏览器重新读取。派发前复核控件归属与哈希，点击发送前再次核对附件清单；缺失、额外、上传中或失败的附件均阻止发送。临时副本有数量限制，完成或重启后清理。结果未知时保留原 invocation 与持久防重发记录，核对不会再次上传或发送。回执身份排除一次性的暂存路径，但保留文件名、大小和哈希。旧的无附件草稿继续兼容。

应用运行时使用基于固定 `.11` bundle 的显式本地 App-CLI `.12` 增量。`vendor/app-cli/workspace-mail-attachments.patch` 可直接评审；`python3 scripts/build-workspace-mail-release.py --check` 可重复构建 runtime、Python wheel 和配套 release 元数据。元数据分别记录上游原始 commit 与本地补丁摘要。Runtime protocol 2.0、Host protocol 1.0 不变；安装器继续拒绝混用版本及文件篡改，整套回滚保留持久账本。这不是上游 App-CLI 发布，也未变更跨项目契约。

最终 10 MiB 上传使用已安装 binding、真实 Controller／CLI 和独立 Electron adapter：228 块、123.24 秒，低于未放宽的 180 秒门槛；接收端 SHA-256 一致，期间完成 12 次正常 lease 续期。每条代码封装小于 64 KiB，45 KiB 数据块执行前登记为需脱敏的内容。此容量 fixture 验证既有应用上传通道，不代表生产 ISCP 路由。macOS 的 legacy 进程回收器仍只支持 Linux；fixture 单独核实 CLI 已退出并清理自己创建的进程。Linux Controller 测试覆盖该平台的归属与进程清理路径。

真实 SparkX renderer／main／preload → 加密 ISCP → Linux Gateway → 受控邮件接收端已通过文件变化拒绝、保存后附件审阅、精确字节接收、丢回执核对和单 invocation 只生效一次。原生直连业务 HTTP／WS 尝试与 Chromium 业务 URL 事件均为零。真实 PostgreSQL 已通过快照持久化、版本冲突和未知发送防重放。独立真实 Chromium 测试覆盖 provider 形态控件、路径替换、哈希期间移动输入控件及发送前附件复核。这些受控页面与接收端**不代表 Gmail、Outlook 或 QQ 邮箱真实页面及投递验收**；最后一项需要已登录的测试账号、收件地址，以及对具体测试邮件的明确发送授权。依赖 provider 的功能在该项通过前仍各自关闭。

生产 Browser Host 入口已在不增加 compositor flags 的条件下通过七种截图状态：从未选中的页面、前台、窗口被遮挡、窗口隐藏、窗口恢复、切换其他会话及恢复原会话。从未呈现的视图使用固定视口、有界 Chromium capture，不改变焦点或会话选择，并保留撤销／期限检查和 debugger 清理。原生读取、填写、点击及点击回执丢失后的防重放通过，实际点击次数为一。

永久授权删除也已走真实设置界面：停止 Relay 并重启 issuer 与桌面应用后，仍恢复同一份签名删除回执，业务请求继续被拒绝。等待审批时重启终结沿用此前真实进程 SIGKILL 与持久账本重开的验收证据。本次未升级现有安装、远端服务或在线 Relay。配置锚点仍无 InfiniCenter，不声称中枢批准。供用户最后验收的候选包、准确检查数量与哈希记录于上述证据文件。

纠正前的历史检查：Go 65 个测试包、build／vet 和定向 race；Desktop 193 项；WebChat 237 项与生产构建；Linux Controller 142 项（一个无关的下载 opt-in fixture 跳过）；附件专项 23 项，其中七项使用真实 Chromium；配套 release 的安装／重复构建／回滚；110 份双语文档镜像。原邮件 fixture 将源文件放在 Gateway，不能据此认定桌面本机数据边界通过。独立容量重现命令为 `SPARKCLAW_MAIL_CAPACITY_TEST=1 node tools/browser-controller/test/qualify-workspace-mail-capacity.mjs`。

## 9.4 桌面本机附件边界纠正，2026-10-09

原生验收 fixture 现在将桌面用户数据建在所有 Gateway Docker 挂载范围之外，同时在 Gateway 放置内容不同的同名干扰文件。测试导入生产 SparkX main／preload／renderer，选择已归属的本地文件 ID，验证加密传输、接收字节一致、本机文件损坏后拒发，以及删除本机源文件后仍可核对原发送回执；另覆盖 10 MiB 对象传输上限。邮件保存／发送在桌面和 ISCP 各层统一使用有界 180 秒期限，其他 RPC 期限不变。此受控验收不包含真实 provider 投递及 ASR／TTS。结果见[纠正后的证据](../../docs/evidence/iscp-desktop-mail-boundary-2026-10-09.json)。

集成代码通过 Linux Go 65 个测试包、build／vet、定向 race、真实 PostgreSQL 清单／CAS／重启验证、Desktop 216 项、WebChat 239 项与生产构建、Linux 本机文件边界 27 项及 110 份双语镜像检查。原生邮件六项边界与恢复检查通过，受控接收端三次发送各生效一次、一次原回执核对。10 MiB 本机文件传输并保存供审阅耗时 43.727 秒，接收字节与哈希一致。Gateway 未挂载桌面数据目录，原生直连业务 HTTP／WS 尝试和 Chromium 业务 URL 事件均为零。首轮原生测试发现的本机错误码映射缺陷已修正，并通过整轮重跑。

## 9.5 ISCP 邮箱服务商配置，2026-10-09

固定 v2 注册表新增 `mail.providers.list`、`update`、`check` 和 `login`，复用后端既有 provider 业务服务。列表要求 `mail.read`；更新配置和检查登录态要求 `mail.read` 与 `settings.write`；打开登录浏览器另需 `browser.login`。请求准入、能力报告和持久回执读取使用同一权限检查。更新仅接受启用／默认选项与预期版本，收取绑定属于独立领域。

独立的 `mail_settings` 能力仅在当前能力报告通过资格与权限检查时，在“连接”页挂载邮箱控件；无需先绑定邮箱，也不因此开放发送。登录明确打开后端专用浏览器，打开页面不代表登录探测通过。变更复用持久回执，丢失或不确定的登录结果即使重启也不能重复开页，独立的登录态检查仍可使用。桌面收到明确的未知结果响应时也保留原变更 fence。

新增操作要求桌面／helper／后端版本匹配，并逐项配置四个 operation 的资格；Relay 封装及 v1 保持不变。加密 issuer／transport、权限、回执／重启及界面测试属于隔离实现证据，不代表已部署、真实 provider 登录、邮件投递或 ASR／TTS 验收。

## 9.6 已安装桌面端与 work2 联调，2026-10-09

候选包已安装到 Mac，work2 的现有 Gateway、WebChat 和邮箱登录台已升级，
并保留可恢复备份。数据库、模型、TLS、部署、Owner、Client 及桌面安装标识
均保留。参见[已安装环境证据](../../docs/evidence/iscp-work2-native-2026-10-09.json)
及[邮箱读取证据](../../docs/evidence/mail-provider-live-read-2026-10-09.json)。
前文的部署状态属于历史检查点。

真实 SparkX 界面分别通过 ISCP 与直连 HTTPS 完成模型任务，并读取同一个桌面
导入文件。三个请求的输入、结果摘要及持久交付 ACK 在桌面与 work2 一致。
切换传输方式后，六个对话、九个已交付任务及本机文件保留。直连使用 work2
现有 HTTPS 入口；本 Mac 无法访问其私网地址，同网段 LAN 连通性仍未实测。

两种连接方式都能保存并审阅 QQ 和 Outlook 草稿，附件从 ClientStore 选择，158 字节大小
和界面指纹均与本机原文件一致。Gateway 没有挂载桌面源目录。邮件草稿和回执
仍以服务端为准。用户已批准四封准备好的合成自投测试邮件。此检查点仍在修复真实附件控件和核实原任务回执；后续真实发送结果见 9.7 节。

原生联调修复了浏览器面板取整越界 1 像素、能力刷新失败丢失未保存编辑，以及
页面导航销毁上下文被误判为租约撤销的问题。已安装浏览器通过默认宽度、加宽
及关闭重开检查。暂停本地 Relay 50 秒后，未保存邮件和附件选择保留，业务操作
禁用；恢复后没有自动保存或发送。撤销、锁定及身份变化仍清空编辑器。浏览器
测试另覆盖 24 种布局、渲染器重载及真实越界拒绝。能力刷新仅对本地明确未发出
的容量不足错误进行四次有界重试，并要求会话及报告仍有效；独立定时器负责到期
关闭能力。新增 12 项回归、桌面全量 236 项通过，原生跨刷新周期观察中邮箱和
草稿操作持续可用。ISCP 模式的 Chromium NetLog 无业务 HTTP/WS URL 事件，该范围不包含独立 Go helper 的套接字。

邮箱运行时已成套升级到 App-CLI `.15`。升级前停止 Gateway，完整备份并验证
PostgreSQL、邮件及执行状态、浏览器配置、版本指针和服务配置。QQ 历史原文
保留已验证的收件时间，在两秒窗口内再次核对精确邮件及账户身份。新增 6 项回归
及 Linux Controller 163 项通过（8 项真实 Chromium opt-in 测试跳过）；可复现
构建、成套安装、篡改及混版本拒绝、回滚、状态与 epoch 保留检查通过。Reader
及 Go 投影字节未变，故沿用 Gateway 镜像及 r10 Reader generation。后端重启
后桌面端自动恢复连接。

已安装的 `.15` 在限定历史日期内分别发现 QQ 11 封、Outlook 7 封候选，并各
下载一封原文（6,374 字节、10,423 字节），实际文件哈希一致，原有已读状态
保留。QQ 首次遇到导航上下文销毁且 effect=0，在常驻采集恢复后仅重试一次，
同 scope 和时间窗口读取通过。两封原文均无附件，收件附件处理仍单列验收。

最后的 Controller 清理修复也已部署。两个邮箱观察器确认就绪后，受控停机在
2.606 秒内退出，所有属主进程和 cgroup 清空，无超时或 SIGKILL；原取消错误
仍以退出码 1 保留，这证明了有界清理，不代表退出码为零。新增 6 项真实监听器
回归及 Linux Controller 169 项通过。修复前的 60 秒超时、旧日志无法证明唯一
执行路径的限制保留在[停机证据](../../docs/evidence/browser-controller-shutdown-2026-10-09.json)中。

长期授权为 revision 2，永久有效直至手动删除，开放 71 个已验证的非语音操作，
短期 Grant 自动续期。此验收环境需要 Docker Relay/Issuer 和通向 work2 的
SSH 反向桥接持续运行。固定 Issuer 映射端口修复了重启导致的路由失效，授权
未重建。在线 Relay 未修改，ASR/TTS 继续暂缓。配置锚点下未找到 InfiniCenter，
故不声明中枢验收完成。

## 9.7 真实邮件验收，2026-10-10

work2 已部署 `75418c75` 的 Gateway 与成套 App-CLI `.17`；SparkX 已安装
`1b22eddc`，另补齐直连 HTTPS 的发送结果核对时限。只有精确的 POST
draft/reconcile 请求放宽为 180 秒，普通读取仍为 30 秒，调用方提前取消仍有效，
真实 TLS 回归通过。浏览器 Reader 文件和策略未改变。详见
[真实发送证据](../../docs/evidence/mail-provider-send-2026-10-10.json)。

QQ 已通过直连 HTTPS 与 ISCP 两种方式的完整链路：每次成功尝试仅原生确认一次，
取得服务商发送回执，采集独立的真实收件原文，并通过 SparkX 将附件复制到所选
本机对话。两份收件附件均为 158 字节，批准的 ClientStore 源文件、MIME 解码附件
和实际本机副本的哈希全部一致。本地已发送快照本身不作为送达证据。

Outlook 尚未通过。本次 `.17` 直连尝试停在附件菜单的原生点击步骤，已有与精确
task、intent、resource 绑定的持久“未发送”回执，待显式核对。较早的 `.16` ISCP
尝试缺少充分的持久负向证明，继续保留为 unknown，不自动重试。当前用固定的
空白撰写窗诊断定位原生菜单问题，不再通过发邮件试错。ASR/TTS 与物理同子网
局域网验收仍不计入这些结果。

## 10. 关联设计

- [本地 ISCP 联调及续签验收](desktop-iscp-connection-design.md)：已实现基线和真实测试范围。
- [架构](architecture.md)、[工作台发布](workbench-release.md)：数据归属与持久交付。
- [浏览器 Runtime](browser-runtime.md)：Controller、宿主、tab 和外部写约束。
- [邮件增量同步](email-timeline-incremental-sync-design.md)、[原文存储](email-local-download-storage-design.md)：复用的邮件业务语义。
- [WebChat 语音第二阶段](webchat-voice-phase2-design.md)：现有 ASR 的 partial／final 语义；其 HTTP fallback 不适用于 ISCP 模式。
