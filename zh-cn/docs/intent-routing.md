# 意图路由

> 语言： [English](../../docs/intent-routing.md) | 简体中文

本文定义当前源码路由契约，包含 2026-10-30 接入的[会话文件、请求拆分与澄清](conversation-file-context-and-clarification-design.md)。
源码现已连接经过校验的请求拆分、确定性发布与签名选择续接。最终校准、配套原生端验收和
发布状态分别记录在主设计中；源码实施不等同于已安装应用或已部署服务的版本。

## Runtime 契约

```text
用户原话 + 已准入会话／资源快照
  -> 输入 guard 与完整原话容量检查
  -> 可选的紧邻签名澄清选择校验
  -> 一次请求拆分，至多一次修复（不改写）
  -> 确定性外发与定时检查
      -> deferred：完整原话 -> 调度校验
      -> 纯交付：确定对象 -> Catalog publish 路由
      -> 有业务：原文 business_span
          -> 按来源筛选候选（排除 publish）
              -> embedding：仅 business_span
              -> Fast/Tree：同一 business_span + 有界 typed context
          -> 融合 -> Top-2 -> grounding
  -> 策略、审批和当前状态检查
  -> 最多一个叶子 Workflow，或终态澄清／阻断
```

无文字媒体同时跳过拆分和评分，直接构造 Catalog publish 路由；typed 调度操作保留原有
校验路径。自然语言业务打分时，embedding 只接收逐字的 `business_span`，不接收资源或
历史；Tree 接收同一片段和已准入快照的有界 typed context。拆分失败且无外发证据时退回
整句；存在外发证据时阻断。系统不生成或持久化改写后的 canonical request。

独立 Fast 拆分调用只看原话与已准入上下文，不看候选或分数，只能输出原文片段和类型标记。
Fast/Tree 负责评分；两者都不能选择具体资源、输出 `RouteDecision` 或授予 authority。
Guard、风险判断、LocalMind 显式指派和 Workflow 目标保留完整原话；查询槽及业务 grounding
使用业务片段，对象 grounding 使用对象片段。主设计 4.5 节是各消费者文本映射的唯一依据。

语义图仍是 `capability.Catalog` 与 Workflow 样例、描述、区分和来源范围的不可变投影，
不是第二套 taxonomy 或向量数据库。非法 calibration、非法注册或必须的 embedding
索引不可用时启动失败。签名选择的 graph、catalog 或 calibration revision 过期时拒绝续接。删除 `conversation.answer#publish` 的评分资格不删除 Catalog 的
publish operation、Workflow、内容治理或外部 MCP 审批。

## 候选与评分

一个叶子可以注册多个路由变体。候选 ID 在图内稳定，格式为
`<capability>#<variant>`，例如：

```text
conversation.answer#answer
browser.automation#open
browser.page_read#read
browser.interaction#interact
browser.form_draft#draft
schedule.manage#create
schedule.manage#read
schedule.manage#edit
schedule.manage#delete
```

变体只选择经 Catalog 校验的 operation 和 fact-scope 模板，不会创建额外 Workflow。
两个语义通道都会为所有按来源过滤后的候选评分，并且只返回候选 ID 和分数：

```text
Embedding: candidate_id + embedding_score
Fast/Tree: candidate_id + tree_score

fusion_score = alpha * embedding_score
             + (1 - alpha) * tree_score
```

内置 calibration artifact 当前使用 `alpha = 0.50`。Fusion 根据 `fusion_score`
对完整 eligible candidate 集合进行确定性排序，并保留最终 Top-2。该权重由
`internal/semanticrouting/default_calibration.json` 管理；修改前必须有 calibration
证据和针对性路由测试。排序分数不是概率，持久化 confidence 由分数、margin、
negative conflict 和通道状态单独计算。

Fast/Tree 保留统一的候选评分 JSON 信封：

```json
{
  "graph_revision": "...",
  "candidates": [
    {"candidate_id": "...", "tree_score": 0.0}
  ]
}
```

初次评分调用与可选的单次 repair 都强制关闭 thinking，并请求同一份动态 strict JSON Schema。
Schema 约束 graph revision、候选数量与 eligible ID、分数范围、必填字段和未知字段；Runtime
继续作为精确候选集合与唯一性的最终权威，只有通过校验的分数才能进入 fusion。初次响应无效且
唯一一次 repair 仍 malformed 时，Tree channel 在正好两次调用后失败。本加固只针对 JSON 结构：
`temperature=0.2`、分数方差、calibration、seed 行为和 Fast profile 输出 token allowance 均不变。

## 语义注册

Workflow 拥有的语义注册结构如下：

```go
type ClarifyLabel struct {
    ZH string `json:"zh-CN"`
    EN string `json:"en"`
}

type IntentVariant struct {
    Key             string
    Route           RouteTemplate
    EmbedTexts      []string
    TreeDescription string
    HardNegatives   []string
    SourceKinds     []app.MessageSourceKind
    ClarifyLabel    ClarifyLabel
}
```

- `EmbedTexts` 是相互独立的真实表达和同义改写，不要把同义词拼成一条逗号列表。
- `TreeDescription` 说明意图和兄弟候选的区分方式，是推理上下文，不是关键词表。
- `HardNegatives` 覆盖引用意图词、故障描述、否定和未来时陈述等局部混淆样例。
- `ClarifyLabel` 必须包含非空 `zh-CN` 和 `en`；图编译拒绝非法 UTF-8、控制字符及超过 160 个 rune 的标签，平台特定变体在标签中写明 provider。标签仅解释候选，不授予权限。
- Catalog 注册 operation 级 `OperationSourceKinds`；移除评分候选后，确定性发布仍通过 `SupportsSource(operation, source)` 校验来源。
- `SourceKinds` 按 Web、第三方或 Timer 来源限制候选。
- tool 名、approval policy、model lane、delivery endpoint 和 Workflow step 不属于语义注册。

图编译会拒绝重复 ID 或样例、不可达候选、非法 operation/fact scope、缺少 Workflow
归属，以及未明确解析的多值 route 字段。

## Grounding 与 Route 组装

路由判断含义；确定性 grounding 判断资源。URL、workspace path、attachment、location、
schedule ID、endpoint ID 和任务栏 typed value 由受限、候选无关的 projector 提取，
模型不能编造。

文档 grounding 只使用一个最近文档 resolver。明确文件名先匹配已准入的本轮资源，再匹配
全部已准入会话 handle；同名不同 ID 保持独立。最新带文件或 `files_unavailable` 的消息构成
历史边界，不完整文件组不能回退更旧文件，选定文件本身也不授权执行 Workflow。
Handle ID、SHA-256 和大小保留在 Runtime binding 中，不预读字节。
`document.p[25]` 等结构定位属于 operation qualifier，不是文件路径。否则优先使用持久 `DocumentRecord` 活动，成功文档工具调用和附件消息仅
作为历史状态迁移 fallback。Record 只向 Fast 暴露有界元数据：document ID、受治理 path、
名称、content type、格式、来源/来源 ID 和最近活动。同一最新 activity 的全部输出保持
ambiguous，不会压缩成一个。唯一的最近文档可以在无需再次附加的情况下满足追问，但仍
必须通过 workspace、普通文件、符号链接、扩展名和文件签名 preflight。

“逐字提取这张图片里的文字”这类请求仍选择已绑定图片的 `document.read` candidate。OCR
是否可用不会创建 candidate、改变语义分数或选择模型 lane：冻结的图片格式只会选择
`images.inspect`，后者可以在已选 Workflow 内补充带分类的 OCR 证据。渠道图片仅在 owner
要求检查或转录时走同一路径；ingest 本身不会自动执行 OCR。

PDF 页级表达继续由同一组两个文档 candidate 处理。询问页面内容、提取页面文字或识别
扫描页选择 `document.read#read`；导出、删除、旋转或拆分页面选择
`document.edit#transform`。否定 transform、引用指令、已完成动作和故障排查描述不会授权
edit。无法区分页面文字与页面文件导出的表达保持 ambiguous，并要求澄清。由于当前没有
Workflow 负责有序多文档 grounding，merge 继续 unmatched/blocked。这些区分通过语义样例
和 hard negative 实现，不是关键词 fallback，也没有修改 fusion weight 或 threshold。

托管浏览器路由把语义选择与公网目标识别分开。明确 URL 或当前托管 tab 会直接 grounding，
不变的 destination registry 则在 candidate-independent grounding 阶段查询。registry miss 时，
Runtime 可以把 owner 的命名目标冻结成 `public_named_target`；只有允许在 Workflow 内解析公网
目标的 browser leaf 被选中后，该 Workflow 才会执行一次 Info-backed search。只有有序结构化
`results[].url` 可以成为绑定目标；Runtime 执行公网 HTTPS、DNS/IP、redirect 和最终 URL
安全检查，但不增加第二个相关性分类器。该资源解析 stage 不创建或重排 semantic candidate，
所以增加 registry entry 不会提高 Top-2 选择成本。

只有 Agent Runtime 能把一个 clear 候选转换成 `RouteDecision`：

1. 在冻结的图 revision 中解析候选。
2. 从 Catalog 推导完整 capability path。
3. 从注册变体复制 operation 和 fact scope。
4. 从逐字业务片段（允许回退时为整句）绑定 query，并从确定性 grounding 绑定资源。
5. 通过 Catalog 校验完整 route。
6. 解析并持久化唯一一个 Workflow Profile revision。

Tree 模型不输出 `RouteDecision`，任何路由阶段都不选 tool。叶子选定后，
Tool Exposure 才根据当前 Workflow node、注册 capability descriptor、
argument binding 和 Policy 派生。

系统不存在 post-fusion 浏览器关键词 veto。search、page read、open、click 和 form draft 的
含义通过 Profile 语义与 hard negative 区分；route clear 后，由选定 Profile 和具体 ToolHub
control 检查执行 effect boundary。超出范围或禁止的 effect 会在该 route 内明确 block，不会
被重新分类为 `unmatched`。

## 决策状态

| 状态 | 含义 | 行为 |
|---|---|---|
| `clear` | Top-1 通过 calibration 分数和 margin 门槛 | 执行确定性资格检查，最多分发一个叶子 |
| `ambiguous` | 两个候选仍合理，或必需语义没有确定 | 返回 `clarify`，两个都不执行 |
| `low` | 候选尚不足以执行 | 返回终态澄清：达到 `clarify_floor` 或已有文件定位时提供可执行 Top-2；否则请用户完整描述 |
| 基础设施失败 | 必需通道、图或 calibration 不可用 | 返回 `blocked`，不能描述为用户歧义 |

mutation 和外部 delivery 使用更严格的分数与 margin。语义已明确但缺少资源时返回
`clarify` 或 `blocked`，而不是 `unmatched`。

`reason_code` 记录由哪条确定性规则产生终态，用于 audit、eval、metrics 和稳定 UI 处理；
它不是第三个意图信号，也不能改变排序。

最终 Top-2 证据会连同 graph/calibration revision、模型 fingerprint、通道状态、各通道
分数、fusion score、confidence、margin、verdict 和 reason code 一起持久化。Top-2 用于
澄清和诊断，永远不授权两个 Workflow。

只过滤融合后的 Top-2，不从更低排名补选；重新校验来源、策略和降级状态。桌面与主机 WebChat
保存签名问题，紧邻选择作为新请求处理；文件选择只确定资源，仍需正常评分。第三方要求重新描述，
外部 MCP 返回既有 clarify 结果，timer 终止而不交互等待。绑定、有效期与原子一次消费遵循主设计
5.4–5.6 节。

## 定时与 Delivery 语义

定时表示未来发布消息，不代表未来 payload 的业务 capability。例如
`半小时后查一下上海天气` 选择 `schedule.manage#create`；到期后保存的天气请求重新进入
同一个路由器。Timer payload 只有 `吃饭` 时可以选择 `conversation.answer`，不会成为
第二次提醒操作。

Delivery 目标选择与业务意图分离。明确的第三方目标通过 Message Control 投影和解析，
不改变语义候选分数。选中的 Workflow 始终返回一个 channel-neutral `WorkflowResult`，
之后才进入 Delivery Gateway。
例如，带唯一附件的 `发送文件` 是纯交付，不经评分而构造 Catalog publish 路由。目标只改变
交付：外部纯媒体结果仍只包含受治理的媒体，也不在来源 WebChat 投影助手结果。
`为什么文件发送失败` 是业务问题，不能被当成重发命令。拆分给出渠道和收件人，消息控制须解析到
唯一登记端点；文字要求的外发在投递前等待所有者审批确切结果和端点，交回当前会话不需要审批
（[会话文件上下文设计](conversation-file-context-and-clarification-design.md#46-外发审批)）。

Deferred 请求保留整句，须落地到支持的调度创建，不能立即执行业务。到期再拆分且不递归创建
同一日程；历史本机文件句柄不自动成为定时持久输入，见[桌面日程](desktop-scheduling.md)。

WebChat 的 typed schedule edit/delete 和持久化 resume 已携带校验过的 route identity，
可以绕过自然语言分类，但仍要经过 Catalog 校验、owner 检查、fresh target lookup 和
Workflow 执行。

只有附件的 Web 消息同样是 typed input，而不是自然语言。没有 owner 文本且全部 part 都是
image、audio 或 file 时，Runtime 直接构造 Catalog `conversation.answer` 的 publish 路由，不选择语义 candidate。
Catalog 只允许该 `publish` operation 的 query 为空；普通对话 `answer` 仍必须保留原始 owner query。

## 失败与降级

- 拆分与一次修复共享 `split_timeout_ms`（初始 15000 ms）；之后 Embedding 与 Tree 在路由 deadline 内独立执行，全部受执行总时限约束。
- 一个语义通道失败时，只读 route 可使用更严格降级门槛；mutation 或外部 effect 关闭失败。
- 两个语义通道都失败、索引不一致或候选未知时阻止路由。
- `clarify`、`blocked` 和 `unmatched` 都是终态，不回退到关键词路由或通用循环。签名选择是重新校验的新请求，不恢复旧执行。
- 纯交付不依赖 Tree，但仍检查资源可读、来源、治理、交付健康及权限；上传、选择或交付 unknown 不授权新 ID 或重放。

## 扩展路由

1. 在 `internal/capability` 增加或修改叶子和 route contract。
2. 为叶子注册且仅注册一个 versioned Workflow Profile。
3. 在 Profile 增加真实多语言样例、Tree 边界、hard negative 和来源限制。
4. 为新增资源类型提供确定性 grounding。
5. 增加直接表达、同义改写、兄弟混淆、否定、引用、故障陈述、复合请求、来源差异和 OOD 测试。
6. 修改权重或门槛前重新生成 calibration 证据。
7. 用户能力变化时更新 [Workflow 能力矩阵](workflow-capabilities.md)和[架构](architecture.md)。

实现位于 `internal/semanticrouting`、`internal/agent/intent_router.go`、Workflow Profile
注册和 Catalog。针对性测试覆盖图校验、分数融合、Top-2 决策、来源过滤、定时同义表达和
单叶子分发。
