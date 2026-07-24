# 意图路由

> 语言： [English](../../docs/intent-routing.md) | 简体中文

本文档是自然语言意图识别的当前契约，替代旧 Fast-only 分类器、关键词识别、
路由重构计划、语义融合方案、Profile 快照和单独的工具暴露说明。

## Runtime 契约

自然语言请求由两个独立通道在同一份注册语义图上分类：

```text
规范化 owner 请求
  -> 确定性 grounding
  -> 按来源过滤后的语义候选
      -> embedding 相似度评分
      -> Fast 模型沿 capability tree 推理
  -> 加权融合
  -> 有界 reranking
  -> 最终 Top-2 决策
  -> 确定性 Route 组装
  -> 仅一个叶子 Workflow，或 clarify / blocked / unmatched
```

语义图是不可变的内存投影，不是向量数据库，也不拥有第二套 capability taxonomy。
图拓扑、叶子契约、operation 和 Workflow 引用来自 `capability.Catalog`；每个
Workflow Profile 提供真实 embedding 样例、Tree 描述、兄弟候选区分、hard negative
和允许的消息来源。

Gateway 启动时通过配置的 embedding 模型构建索引。图或 calibration 不一致、注册非法、
或必须的 embedding 索引不可用都会让启动失败，不会静默恢复旧分类器。

## 候选与评分

一个叶子可以注册多个路由变体。候选 ID 在图内稳定，格式为
`<capability>#<variant>`，例如：

```text
schedule.manage#create
schedule.manage#read
schedule.manage#edit
schedule.manage#delete
```

变体只选择经 Catalog 校验的 operation 和 fact-scope 模板，不会创建额外 Workflow。
两个语义通道都只返回候选 ID 和分数：

```text
Embedding: candidate_id + embedding_score
Fast/Tree: candidate_id + tree_score

fusion_score = alpha * embedding_score
             + (1 - alpha) * tree_score
```

内置 calibration artifact 当前使用 `alpha = 0.50`。融合保留有界 Top-N，
reranker 只能给这些候选重新评分：

```text
final_score = (1 - reranker_weight) * fusion_score
            + reranker_weight * reranker_score
```

当前 `reranker_weight` 为 `0.28`。这些参数由
`internal/semanticrouting/default_calibration.json` 管理；修改前必须有 calibration
证据和针对性路由测试。排序分数不是概率，持久化 confidence 由分数、margin、
negative conflict 和通道状态单独计算。

## 语义注册

Workflow 拥有的语义注册结构如下：

```go
type RoutingIntentVariant struct {
    Key                 string
    Route               RoutingRouteTemplate
    EmbedTexts          []string
    TreeDescription     string
    HardNegatives       []string
    EligibleSourceKinds []app.MessageSourceKind
}
```

- `EmbedTexts` 是相互独立的真实表达和同义改写，不要把同义词拼成一条逗号列表。
- `TreeDescription` 说明意图和兄弟候选的区分方式，是推理上下文，不是关键词表。
- `HardNegatives` 覆盖引用意图词、故障描述、否定和未来时陈述等局部混淆样例。
- `EligibleSourceKinds` 按 Web、第三方或 Timer 来源限制候选。
- tool 名、approval policy、model lane、delivery endpoint 和 Workflow step 不属于语义注册。

图编译会拒绝重复 ID 或样例、不可达候选、非法 operation/fact scope、缺少 Workflow
归属，以及未明确解析的多值 route 字段。

## Grounding 与 Route 组装

路由判断含义；确定性 grounding 判断资源。URL、workspace path、attachment、location、
schedule ID、endpoint ID 和任务栏 typed value 由受限、候选无关的 projector 提取，
模型不能编造。

只有 Agent Runtime 能把一个 clear 候选转换成 `RouteDecision`：

1. 在冻结的图 revision 中解析候选。
2. 从 Catalog 推导完整 capability path。
3. 从注册变体复制 operation 和 fact scope。
4. 从持久化规范化请求和 grounding 绑定 query 与资源。
5. 通过 Catalog 校验完整 route。
6. 解析并持久化唯一一个 Workflow Profile revision。

Tree 模型不输出 `RouteDecision`，reranker 不能增加候选，任何路由阶段都不选 tool。
叶子选定后，Tool Exposure 才根据当前 Workflow node、注册 capability descriptor、
argument binding 和 Policy 派生。

## 决策状态

| 状态 | 含义 | 行为 |
|---|---|---|
| `clear` | Top-1 通过 calibration 分数和 margin 门槛 | 执行确定性资格检查，最多分发一个叶子 |
| `ambiguous` | 两个候选仍合理，或必需语义没有确定 | 返回 `clarify`，两个都不执行 |
| `low` | 没有候选获得足够语义覆盖 | 返回 `unmatched` |
| 基础设施失败 | 必需通道、图或 calibration 不可用 | 返回 `blocked`，不能描述为用户歧义 |

mutation 和外部 delivery 使用更严格的分数与 margin。语义已明确但缺少资源时返回
`clarify` 或 `blocked`，而不是 `unmatched`。

`reason_code` 记录由哪条确定性规则产生终态，用于 audit、eval、metrics 和稳定 UI 处理；
它不是第三个意图信号，也不能改变排序。

最终 Top-2 证据会连同 graph/calibration revision、模型 fingerprint、通道状态、各通道
分数、最终分数、confidence、margin、verdict 和 reason code 一起持久化。Top-2 用于
澄清和诊断，永远不授权两个 Workflow。

## 定时与 Delivery 语义

定时表示未来发布消息，不代表未来 payload 的业务 capability。例如
`半小时后查一下上海天气` 选择 `schedule.manage#create`；到期后保存的天气请求重新进入
同一个路由器。Timer payload 只有 `吃饭` 时可以选择 `conversation.answer`，不会成为
第二次提醒操作。

Delivery 目标选择与业务意图分离。明确的第三方目标通过 Message Control 投影和解析，
不改变语义候选分数。选中的 Workflow 始终返回一个 channel-neutral `WorkflowResult`，
之后才进入 Delivery Gateway。

WebChat 的 typed schedule edit/delete 和持久化 resume 已携带校验过的 route identity，
可以绕过自然语言分类，但仍要经过 Catalog 校验、owner 检查、fresh target lookup 和
Workflow 执行。

## 失败与降级

- Embedding 和 Tree 在总路由 deadline 内独立执行。
- 一个语义通道失败时，只读 route 可使用更严格降级门槛；mutation 或外部 effect 关闭失败。
- Reranker 失败时，只读工作可保留融合顺序；mutation 或外部 effect 被阻止。
- 两个语义通道都失败、索引不一致或候选未知时阻止路由。
- `clarify`、`blocked` 和 `unmatched` 都是终态，不回退到 TaskHint、旧关键词逻辑或 ReAct。

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
