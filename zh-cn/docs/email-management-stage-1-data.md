# 阶段 1：身份、存储与状态

> Language: [English](../../docs/email-management-stage-1-data.md) | 简体中文

状态：2026-09-08 实施契约。代码与工程检查已进入工作区；真实邮箱和语义质量发布门槛另行记录，见[实施报告](email-management-implementation.md)。上位设计：[邮件管理系统](email-management-design.md)。
目标是使后续收件、分析和 UI 共用事实来源，而不是让脚本目录成为隐式数据库。

## 身份与记录

沿用[三层文件结构](email-read-design.md#物理存储与权威边界)：原件、确定性标准化、
不可变分析结果分别保存。新增管理记录由现有 Store 持有，不创建另一套 SQLite 或队列数据库。
建议新增专用 EmailRepository，与现有 Agent ConversationRepository 分离；邮件对话不是模型聊天 Session。

| 记录 | 最小字段与唯一性 |
|---|---|
| Mailbox | owner_id、mailbox_id、实际地址、provider、绑定版本、active/paused 绑定、接收设置与已接纳发现边界 |
| Mail | mail_id、mailbox_id、provider_message_id、方向、首次发现时间、已提交来源/解析指针；唯一键 owner＋mailbox＋provider_message_id |
| ProviderThread | mailbox_id＋provider_thread_id、成员观察版本、同步游标/覆盖、最后检查时间 |
| ThreadMember | 线程、单封身份、文件夹/方向/草稿证据、发现原因、采集状态；允许多文件夹定位符映射同一已证明邮件 |
| Capture/Representation/Analysis | 沿用三层版本 ID、文件清单及摘要；模型结果绑定具体输入版本 |
| EmailConversation | email_conversation_id、owner_id、标题、参与者地址集合、来源 mailbox 集合、成员版本、摘要指针、更新时间 |
| Membership | mail_id、conversation_id、decision_id、依据、有效版本；每封邮件最多一个当前管理归属，历史决定保留 |
| AssignmentDecision | 新建/追加/待定、候选快照版本、模型/规则版本、理由、证据、输入引用、提交结果 |
| AssignmentConcern | owner、涉及的 mail/conversation ID、suspected_duplicate/pending_correction、证据及输入版本；独立于已提交归属 |
| AnalysisDependency / RefreshIntent | 摘要/归属/检查目标、已提交与尚未解析的输入引用、期望代次、输入指纹、持久展开进度 |
| Job | job_id、类型、目标、幂等键、状态、尝试、lease_token、lease_until、next_attempt_at、错误 |
| SyncRun | mailbox、触发原因、发现进度、线程补齐进度、缺口；不等于模型或用户处理状态 |
| ViewReceipt | 唯一键 owner＋mail_id、first_viewed_at；实际呈现的邮件逐封记录，独立于对话和远端已读 |
| OwnerAssignmentEpoch | owner 范围归属索引版本，拒绝过时并发建议；不保证语义归类必然正确 |

地址保留来源原文和检索形式。域名按规范统一；不自行删除加号标签、点号或按显示名合并联系人。
来源账号沿用当前已验证绑定规则，具体地址规范化策略集中定义。新对话 ID 由 Runtime 分配，
从不由模型、标题或地址哈希生成。跨收件邮箱允许同一对话，但不跨 owner。

服务商线程与管理对话不是一对一强约束：主题转向可能拆成不同事项；多个服务商线程可能讨论同一事项。
线程索引由成员归属导出，返回候选集合，不强迫统一合并。
RFC Message-ID 是关联证据，不能单独作为邮件去重键；不同邮箱副本保留各自原件。
只有明确副本证据才建立展示关联，冲突或不明时分别展示，不能删除其中一份。

## 账号绑定

首版每个 owner、每家 provider 只启用一个浏览器账号，与现有 default 账号设置一致。
不同服务商可同时接收；同一家服务商多个账号同时接收不在首版范围。已接纳的旧 mailbox
仍可查询并参与跨邮箱对话。扩展既有设置/登录流程，返回已验证 mailbox 与独立、默认关闭的
接收开关；启用发送不能顺带启用后台接收。

实际账号切换时，先暂停旧 mailbox 的浏览器任务并使旧绑定代次失效，再启用已验证的新绑定。
运行中浏览器任务按关停预算结束，新任务等待 Controller 锁；每次新浏览器操作及发布前检查
mailbox/绑定代次。已发出的操作可能已经影响旧账号，保留回执供对账，不能接纳到新绑定下。
非预期账号不匹配暂停接收，须显式核验重绑。旧任务、来源、分析和查看记录保留，本地分析可继续。
重新绑定同一已验证账号保留 mailbox/mail ID，以新代次恢复固定目标任务；新账号使用不同 mailbox ID
和独立发现边界。单纯凭据轮换不改变邮箱身份。

## 状态边界

| 状态 | 定义 |
|---|---|
| 远端 unread/read/unknown | 带观察时间与依据的邮箱状态，不是任务队列 |
| 任务 queued/running/retry_wait/succeeded/failed | 执行状态，running 必须持有有效租约 |
| 来源 complete/partial/failed | 文件与附件覆盖；下载成功不代表解析完成 |
| 解析 ready/partial/unsupported/failed | 包括附件提取覆盖，不支持附件仍保留原件 |
| 归属 pending/assigned | 模型失败或候选歧义时仍可查看已接收邮件 |
| 归属疑点 | suspected_duplicate/pending_correction 是附加可见记录，不搬移成员，也不撤销原归属 |
| 摘要 pending/current/stale/failed | 新成员到达使旧摘要 stale，保留旧版并标记更新中 |
| 本地未查看 | 已接纳 mail 缺少 owner＋mail_id 的 ViewReceipt；对话未查看数是其中未查看成员数 |
| 线程覆盖 pending/partial/complete_for_observation | 仅针对某次有界观察范围，不宣称永远完整 |

管理对话“待回复/已完成”等用户工作状态暂不引入，避免借用采集失败表示事项失败。
迟到的旧邮件也分配新的成员序号：时间线按邮件发生时间排序，但“新到达”不能按最大邮件时间判定。
到达序号仅用于刷新/事件排序，不作为查看水位。待归类邮件也可逐封记录查看，之后归属不重置记录；
重复发现或摘要更新不重新标成未查看。查看某个跨邮箱副本不能自动确认其他来源 mail_id。

## 必须原子完成的 Repository 命令

以下是待实现语义，具体 Go 类型在本阶段实现时按 Store 规范定义。所有命令带 context 和稳定幂等键。

| 命令 | 同次提交的不变量 |
|---|---|
| AdmitDiscoveryBatch | 候选身份＋线程成员观察＋去重采集任务＋已接纳扫描进度；不能先推进游标再入队 |
| BindMailboxForIntake | 经既有设置流程原子提交已验证账号、绑定代次、旧浏览器任务暂停及保留/新建的发现边界 |
| ClaimJob / RenewJob / FinishJob | 条件领取及过期保护；过期 worker 不能覆盖新尝试结果 |
| PublishCapture | 校验后来源指针＋解析意图；仅完整采集创建标已读意图；输入变化持久触发依赖分析失效 |
| PublishRepresentation / PublishContext | 不可变解析或关系/覆盖版本＋失效代次＋持久刷新意图，已归属邮件同样适用 |
| ExpandRefreshIntent | 有界查询依赖、接纳刷新任务并在同次提交保存继续位置；包括未解析回复引用及受影响对话 |
| CommitAssignment | 检查输入/owner epoch；新建或首次归属＋决定＋成员版本＋摘要刷新意图；不能搬移已归属成员 |
| PublishAssignmentConcern | 校验同 owner 证据并发布有版本的疑点关联，不修改成员归属 |
| PublishMessageSummary / PublishConversationSummary | 比较期望代次和完整输入指纹，含提取/上下文/覆盖及所用摘要版本；旧结果留历史，最新刷新保持待执行 |
| MarkMailsViewed | owner 校验后，为至多 100 个明确的已接纳 mail_id 幂等插入查看记录；不按序号扩展，不调用服务商 |
| ReconcileCommand | 超时结果未知时按幂等键及内容核对，不盲目重复创建 |

文件先在尝试专属暂存区完成与校验，再发布 Store 引用。文件系统与 Store 不构成分布式事务；
崩溃留下的未接纳文件不进入 UI，重试按同次尝试对账。文件丢失不伪装成“从未采集”。

Memory、File、PostgreSQL 使用相同契约测试。File 的一次聚合命令必须是一次受锁保护的持久替换；
PostgreSQL 在事务中提交；不通过多次独立写入模拟原子性。模型与浏览器操作均在事务外执行。

输入发布与刷新意图同次原子提交。大量依赖通过可恢复批次展开；展开完成前，读取和发布也要比较
已提交输入版本，旧摘要不能继续显示 current。任务按 owner/类型/目标/输入指纹去重，并跟踪最新期望
代次；旧尝试完成不能清除新代次工作。相同输入的手动请求与运行中/已完成结果合并；重试耗尽的失败
可显式重新开启一轮有限尝试。启动恢复未完成意图和任务，耗尽失败持续可见并可重试。
依赖失败不回退浏览器采集；各分析类型的触发规则见阶段 3。

## 阶段验收与迁移

验证重复发现、重启恢复、发布回执丢失、租约过期、两个“新地址”并发新建、跨邮箱同一事项、
迟到邮件与摘要过期，三 backend 的状态一致。两个并发 new 结果只有一个可按旧 epoch 提交，
另一个重新检索并判断，不简单重放 new。
这只保证并发提交正确，不保证资料不足时没有语义重复对话。补测迟到证据下保留归属并显示疑点、
分页缺口下逐封查看、账号切换，以及失效提交后、刷新任务展开前崩溃。

既有脚本目录不是已入库数据；提供显式校验接纳过程，按已验证身份导入或记录问题。
原有 mail_id 与新 Store 分配若不一致，建立导入映射，不能覆盖原路径。
私有资格验证目录默认不导入产品。无新的跨项目接口，既有邮箱发送不改动。
