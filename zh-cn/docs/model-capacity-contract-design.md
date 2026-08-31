# 模型输入输出容量契约设计

> 语言：[English](../../docs/model-capacity-contract-design.md) | 简体中文

状态：设计经接受与最小架构复审后，于 2026-08-31 完成实施。

本文让所选部署 profile 成为模型容量的可执行事实源：必经的前置模型无法解析 owner 问题时直接
拒绝；少量稳定的输出能力等级拥有明确额度；每个完成渲染的模型请求都经过一次统一的最终准入。

有界历史获取由[上下文历史读取设计](context-history-query-design.md)负责。语义 section 的选择与
降级仍由 Agent ContextBuilder 负责。[Tree 同 Session 上下文设计](tree-session-context-parity-design.md)
使用相同容量边界，但不改变固定历史选择数量。

## 1. 已接受决策

| 关注点 | 已接受决策 |
|---|---|
| 物理容量 | 每个所选物理模型声明一个必填正数 `context_tokens` |
| 输出规划 | 使用少量、profile-specific 的输出能力等级；既不使用全局统一上限，也不为每次调用单独调参 |
| Operation 归属 | 每个类型化模型 operation 在代码中显式映射到一个输出等级和允许的逻辑 lane 集合 |
| 有效输入 | `context_tokens - output_class_budget`；不再设置重复的 profile 输入上限或 profile 级输出上限 |
| 非法配置 | 容量缺失、为零、为负、未知、不完整或关系非法时，所选 profile 加载失败 |
| 容量兜底 | 禁止用 Go 默认、旧常量、环境默认、provider 默认或其他能力等级修复容量 |
| Owner 问题 | 原文分别对 Guard 和 Embedding 检查；任一无法接纳就在历史、路由、Workflow 或工具前拒绝 |
| 语义压缩 | Agent 决定 `full -> compact -> drop`；Router 不截断也不改写 prompt 内容 |
| 最终边界 | Router 对完整渲染请求计数，超限时在 provider dispatch 前拒绝 |
| 输出完成 | 保留 `finish_reason`；`length` 代表未完成，不能完成 run |
| Profile 校验 | Runtime 只校验所选可执行 profile；CI 校验完整 catalog 和晋级证据 |
| Fast/Deep 故障 | 不进行隐式跨 lane 重试；同 lane transport 重试必须显式且仅限可重试错误 |
| Memory | Memory repository 记录不进入任何 Agent 模型上下文 |

## 2. 范围与非目标

本文适用于 Fast/Deep chat、Tree 初次评分和 repair、Workflow 模型步骤、operation selection、
conversation/final answer、direct chat、Guard、Embedding 和 Fast 图片理解调用。不经过 Model Router
的模型适配器（例如 OCR）可以消费同一所选容量 catalog，但保留自己的 transport 与媒体限制。

本文不做以下事项：

- 不随窗口扩大而增加 8 条消息、6 条工具结果、4 条 episode 或 3 张图片的选择数量；
- 不分块、摘要、改写或截断超长 owner 问题；
- 不为每个 Workflow、step 编号或 repair attempt 创建独立预算；
- 不让 Runtime 读取 benchmark 或评测输出文件；
- 不新增 capacity ticket、持久化 admission 记录、全局缓存、profile 热更新或第二个 Router；
- 不把 `observation.read`、episode 生成或跨 lane 故障转移纳入容量正确性；
- 不激活 Memory，也不把它放进 Tree、Workflow 或最终回答上下文。

大型受治理文档仍作为 tool/evidence 输入，而不是 owner-question text。其现有字节、artifact、授权与
投影限制保持不变。

## 3. 最小容量模型

容量把物理模型事实与逻辑模型工作分开：

```text
physical model
  context_tokens
  endpoint/model identity

logical lane
  physical model reference
  output_budgets[class]

typed operation
  allowed logical lanes
  output capability class
```

逻辑 `deep` lane 可以显式引用与 `fast` 相同的物理模型。该 alias 共享物理窗口和 endpoint identity，
但仍声明通过 `deep` 路由的 operation 所需输出等级。Alias 属于配置，不是故障 fallback。

对于生成调用：

```text
context_tokens > 0
output_budget = selected_lane.output_budgets[operation.output_class]
output_budget > 0
output_budget < context_tokens
input_budget = context_tokens - output_budget
actual_input_tokens <= input_budget
```

当前契约不要求 `max_input_tokens`：第二个应用输入上限会阻止物理窗口变更自动传递到可用输入预算。
当前契约也没有 profile 级 `max_output_tokens`：输出能力等级已经拥有该事实。如果以后确认某个 provider
存在独立的 completion 硬上限，可以把它作为已证实的物理 provider 事实加入，并要求所有等级不超过
它；本文不预先引入该字段。

因此，把 Fast 从一个物理窗口换为另一个窗口时，只需改变一个 `context_tokens` 声明。固定上下文
选择不会扩张，但更大的输入预算允许 ContextBuilder 在降级前保留更多 full variant。

## 4. 输出能力等级

初始设计按响应形状和当前模型能力使用以下少量稳定等级：

| 等级 | 代表性工作 |
|---|---|
| `guard` | 紧凑 Guard 分类 |
| `compact_structured` | Tree candidate score 与有界 selection JSON |
| `workflow_structured` | Workflow action/final envelope 与生成的工具参数 |
| `answer` | 对话、direct chat 与 Workflow 完成后的用户回答 |
| `vision_structured` | Fast 图片检查与文档图片语义提取 |
| `ocr_document` | OCR 适配器消费共享 catalog 时的输出 |

具体正数值是 profile-specific 的保守规划上限。评测覆盖每个等级的代表性最坏案例，确认该上限支持
当前模型能力；不为每个 operation 搜索各自唯一的最小值。

类型化 operation registry 的概念结构为：

```go
type OperationSpec struct {
    Operation    ModelOperation
    OutputClass  OutputBudgetClass
    AllowedLanes []ModelLane
    Generates    bool
}
```

每个 operation 必须指定一个等级，调用方不能传数值输出预算。初次调用与 repair 在响应 schema 相同
时复用同一等级；attempt number 和 audit operation 仍是独立元数据。只有评测证明某个响应族具有
显著不同的输出需求，才拆分能力等级。

新增适合已有等级的 operation，只需增加 registry 映射与覆盖测试，不新增 profile 字段。新增真正
不同的响应族，才需要新增等级、代表性评测，以及所有可执行它的所选 lane 中的正数值。

## 5. 所选 Profile 作为可执行输入

`configs/model.profiles.json` 是物理模型窗口和逻辑 lane 输出等级预算的唯一可执行来源。它可以同时
拥有描述相同物理部署的模型启动参数，但不吸收 observation bytes、Workflow step count 等无关的
Agent 限制。

Loader 在 Router、Agent 或模型适配器构造前完成：

1. 解析显式选择的可执行 profile；
2. 把逻辑 lane alias 解析为完整物理模型记录；
3. 校验物理窗口为正数；
4. 校验 binary 在每个所选 lane 所需的精确输出等级集合及其与物理窗口的关系；
5. 校验每个类型化 operation 的 lane 与等级映射；
6. 冻结一份不可变的进程内容量 catalog。

至少以下情况必须拒绝加载：

- 所选 profile、物理模型、lane、等级或 operation 映射缺失；
- 容量为零、负数、非整数、溢出或格式错误；
- 输出等级预算大于等于物理窗口；
- 未知等级、未知 operation、非法 lane 映射、alias 环或 alias 无物理目标；
- 可选择的 mock/external profile 没有显式容量；
- 迁移完成后 Go 默认、应用 JSON、Compose 默认或环境 override 仍含旧容量字段。

Runtime 只校验所选 profile。CI 校验 catalog 中每个可执行 profile，并确认晋级后的等级值有已接受
评测证据。没有具体容量的可复用 external template 必须标记为不可执行，不能被选择。

Endpoint URL、credential 和所选 profile ID 仍可来自部署配置，但不能覆盖 `context_tokens` 或输出
预算。本地模型启动从同一物理模型记录派生 `--max-model-len`。Provider metadata 与 `/tokenize` 只
验证声明，绝不提供缺失的运行时默认值，也不改写 catalog。

## 6. Token 计算与最终准入

旧的每 token 四字节估算不再具有准入权威。Model Router 旁的一处 model-aware 计数边界，按最终
请求相同的 role、content、response schema、chat-template option 与模型 identity 计数。

最小实现为：

1. 当经过本地测试的保守上界能够证明请求可容纳时直接使用；
2. 如果该上界会拒绝一个可能合法的请求，调用所选 endpoint 的有界 `/tokenize` 或精确本地 tokenizer；
3. 需要精确计数但 tokenizer 不可用时失败关闭，不回退到旧估算器。

这不需要持久化 token decision 或 request ticket。Agent 选择语义变体时可以使用同一 counter；Router
在所有 call option 确定后始终重新检查最终请求。配置查找和预算算术来自不可变 catalog，成本很低；
每个不同 prompt 的实际输入 token 必须重新计数。

Embedding 对每个 input sequence 分别检查物理窗口，不能把 batch token 总数视为单 sequence 长度。
多模态适配器在现有图片预处理之后提供 model-specific 的保守图片 token 贡献；原始 Base64 字节数
不是图片 token 数。

Router 返回类型化 `model_input_too_long`，仅携带安全测量值。它不删除 message、不切字符串、不
切换 lane，也不换用更大的能力等级重试。

## 7. Owner 问题准入

Message Plane 完成 normalize 并持久化 inbound message/run 边界后，在历史获取、Guard、Embedding、
Tree、Workflow、resume 执行或工具前，Runtime 检查未经修改的 owner-authored text。

Guard 与 Embedding 使用不同 tokenizer，因此两者的 token count 不比较也不相加，而是独立证明：

```text
embedding_count(owner_question) <= embedding_context_tokens

guard_count(guard_system_prompt, owner_question)
  + output_budgets[guard]
  <= guard_context_tokens
```

Tree 的 graph、schema、resource 与历史不属于早期问题检查，后续由 whole-request admission 保护。

超长问题产生 `owner_question_too_long`，不发起任何历史、Guard、Embedding、Tree、Workflow model
或工具调用，并持久化一条稳定安全结果：

```text
问题过长，当前无法解析。请缩短问题后重试。
The question is too long to parse. Shorten it and try again.
```

公开结果不暴露 tokenizer、endpoint、count 或内部上限。Audit 可以记录安全的 count 与 profile
identity，但不记录问题正文。Attachment-only 输入不会仅因 owner text 为空而被拒绝。

## 8. Agent、Router 与 Provider 职责

最终职责链为：

```text
Agent / ContextBuilder
  管理语义 section 及 full/compact/drop variant
        |
        v
Model Router
  解析 lane + operation class
  预留该等级输出预算
  计算完整渲染请求
  超限时拒绝且不修改内容
        |
        v
Provider adapter
  发送显式输出预算
  约束 transport 并校验物理服务兼容性
```

ContextBuilder 必须原样保留 owner 问题。结构化 schema、resource 与 evidence section 使用预定义的
合法变体，而非任意字符截断。可选语义 section 全部降级后，fixed content 仍无法容纳时，在 dispatch
前失败。

Router API 接收类型化 operation 与 transport option；调用方 option 不含数值输出预算。Direct chat、
image chat、stream、Guard 与 Embedding 使用相同最终边界，不能有旁路入口。

删除隐式 Fast-to-Deep fallback，因为它会改变 lane 语义、容量、延迟与输出策略，而且现状会重试
不可重试错误。Provider adapter 只可对显式分类的瞬时 transport failure 做有界同 lane 重试。

## 9. 完成状态与响应边界

`ChatResult` 在流式与非流式路径保留 provider finish reason。至少满足：

- `stop` 表示完成；
- `length` 表示未完成，不能把 run 成功完成、持久化或投递；
- 空或未知 reason 由 provider contract 显式处理；
- reasoning-only 或空 assistant output 仍是错误。

结构化调用可以使用现有的一次 repair 契约，但响应契约未被证据拆分时仍使用同一显式等级。遇到
`length` 后不能借用更大等级。

Router 还对非流式 response bytes 和累计 stream bytes 设置 transport 安全边界。该限制不是另一个
语义任务预算，不属于 model-capacity profile。

## 10. 实施记录

### 已实施：Profile 与 Registry

- 清点生产模型调用，并按稳定响应契约分组；
- 增加类型化 operation、输出等级、允许 lane 与 registry 覆盖测试；
- 把混合 profile schema 替换为物理模型、逻辑 lane 映射和输出等级预算；
- 让 Gateway 与模型服务启动消费同一所选 profile，再删除重复容量默认与 override。

### 已实施：计数与 Owner Gate

- 增加中英文、代码、UUID、JSON、hash、类 Base64 和高熵文本的对抗性计数 fixture；
- 引入保守与精确计数路径；
- 在历史或执行工作前加入 Guard 与 Embedding 独立 owner-question 检查。

### 已实施：Router 边界与完成状态

- 每个模型调用点必须提供类型化 operation；
- 解析并发送所选 lane 的等级预算；
- 对 chat、stream、image、Guard 与 Embedding 应用最终准入；
- 保留 finish reason，并限制 transport response bytes；
- 删除隐式跨 lane fallback 与调用方选择的数值预算。

### 已实施：Agent 集成

- 替换 Workflow fallback 算术与四字节估算权威；
- 把 owner text 设为 fixed section，并与可降级 resource context 分离；
- 在有界历史获取后集成 Tree whole-prompt admission。

## 11. 验证与验收

实施只在以下条件全部满足时验收：

- 只改变所选物理模型的 `context_tokens`，计算出的输入预算随之变化，但固定历史选择数量不变；
- 每个生产模型调用都有类型化 operation、允许 lane 与显式输出等级；
- 一个等级可服务多个 operation，调用方不传数值；
- 任何容量路径都不能用默认、旧常量、provider omission、其他等级或其他 lane 修复非法配置；
- 非法所选 profile 在 Router 构造前失败；
- CI 校验全部可执行 profile 及其等级晋级证据；
- Guard 与 Embedding 的 owner-question 检查相互独立；
- 超长 owner 问题不产生历史或执行调用，且永不截断；
- 每个 Router 入口在 HTTP dispatch 前拒绝无法容纳的完整请求；
- `finish_reason=length` 不能产生已成功持久化或投递的回答；
- 结构化 fixed section 经语义降级后仍保持有效；
- 现有 Policy、Approval、external-MCP 隔离、Workflow evidence 与 claim coverage 测试保持通过。

运行聚焦的 config、Model Router、Agent 与 deployment 测试，以及完整 Gateway build/test/vet、
routing/model golden eval、默认 File 与可用时的 PostgreSQL coverage、Compose/profile validation、
必要时的 WebChat incomplete 终态测试和双语文档检查。

## 12. 所有权边界

- `internal/config`：所选 profile 加载与不可变容量 catalog；
- `internal/modelrouter`：类型化 operation/class registry、计数、最终准入、finish reason、transport
  response bound 与同 lane 重试分类；
- `internal/agent`：owner-question 失败投影、语义 section 降级和 Tree/Workflow 集成；
- 模型适配器：消费共享容量事实，同时拥有媒体 token 贡献与 transport bound；
- `configs/model.profiles.json`：物理模型窗口、逻辑 alias、输出等级预算与相关模型启动事实；
- 部署脚本：消费所选 profile 并验证 provider 声明；
- CI/评测：完整 catalog 校验与代表性能力等级晋级证据。

本文有意不新增第二份容量 registry、动态预算规划器或逐任务调参系统。
