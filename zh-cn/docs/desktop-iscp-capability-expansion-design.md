# SparkX 与 SparkClaw ISCP 能力扩展设计

> 语言：简体中文 | [English](../../docs/desktop-iscp-capability-expansion-design.md)
>
> 日期：2026-10-09。状态：首批实现已通过隔离验证；P1–P5 尚未发布。已实现子集见 9.1 节。
> 范围：SparkX 经本地 Docker ISCP Relay 连接 SparkClaw，按依赖顺序补齐业务。允许调整本地 Relay，但必须保持与在线 Relay 的协议兼容；本轮不切换在线部署。

## 1. 目标、基线与实施顺序

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

P1 能力协商／注册表、逐操作权限、设置、通知尚待实现。P2–P5 业务适配与原生发布门禁仍待完成。未升级已安装 SparkX 或运行中的部署。配置及祖先锚点均未找到 InfiniCenter，因此未修改跨项目契约或中枢状态。

## 10. 关联设计

- [本地 ISCP 联调及续签验收](desktop-iscp-connection-design.md)：已实现基线和真实测试范围。
- [架构](architecture.md)、[工作台发布](workbench-release.md)：数据归属与持久交付。
- [浏览器 Runtime](browser-runtime.md)：Controller、宿主、tab 和外部写约束。
- [邮件增量同步](email-timeline-incremental-sync-design.md)、[原文存储](email-local-download-storage-design.md)：复用的邮件业务语义。
- [WebChat 语音第二阶段](webchat-voice-phase2-design.md)：现有 ASR 的 partial／final 语义；其 HTTP fallback 不适用于 ISCP 模式。
