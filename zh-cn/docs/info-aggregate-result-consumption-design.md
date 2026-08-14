# Info 上游聚合结果消费设计

> 语言：[English](../../docs/info-aggregate-result-consumption-design.md) | 简体中文
>
> 状态：已实现

## 目的

Infinimesh Info 返回的是一个已经聚合的 `AgentContextResponse`：它汇入符合条件的
search backend，应用 Info 自有的 policy 与最终 source 顺序，合成 `answer_context`，
并把 fact/conflict viewpoint 绑定到响应内的 source ID。`agent_context` 是请求的
response mode，不是某个搜索 provider 的透传 payload。SparkClaw 应把返回的聚合体
作为语义权威，不能再对同一批 fact 运行第二套相关性排序或合成逻辑。

本设计用类型化、经校验、有界且确定性渲染的 aggregate，替换当前会损失信息的 Info
结果处理，同时保留既有“不可信证据”边界。

## 范围

本设计覆盖成功的 `POST /v1/info/query` 响应，消费者包括：

- `browser.internet_search` Workflow；
- 托管浏览器 Workflow 共用的 Info-backed 公网目标识别阶段；
- `web.search` 的 ToolHub 持久化、observation projection、trace 和 audit metadata。

本设计不改变 Info 的检索、provider 汇入、排序、校验或合成；不为联网搜索增加页面
打开；不修改浏览器 URL 安全检查；不替换类型化天气路径；不增加 deep research；
也不执行 Info 返回的后续动作。

## 当前行为与问题

当前链路为：

```text
Info agent_context
  -> infinimeshinfo.QueryResponse
  -> websearch.Result
  -> ToolHub web.search output
  -> 基于 query 词面重排的 InfoEvidenceProjection v3
  -> groundedWebSearchSummary
  -> 用户答案
```

该链路会在多个位置丢失或改变上游 aggregate 语义：

1. `infinimeshinfo.AnswerContext` 只解码 `summary`、`key_facts` 和一个旧 citation
   字段，JSON 解码时会静默丢弃上游的 `conflicts`、`freshness`、`uncertainty` 和
   `recommended_next_actions`。
2. `websearch.Result.Answer` 只是 `Summary` 的重复字段，无法区分 aggregate 摘要、
   provider 状态句或从 snippet 拼出的 fallback。
3. `ProjectInfoEvidence` 按 query 字面词重新排列 fact、source 和 snippet，最多只留
   四个 fact、三个 source，还可能从 claim 中截取子串。这是在已经排序并合成的
   aggregate 上再次做本地相关性处理。
4. 只有 summary、fact 和 source snippet 同时存在时，projection 才被视为
   `complete`；这三类并非全部都是强制回答组件，当前实现混淆了“上游没有可选字段”
   和“投影丢失”。
5. `groundedWebSearchSummary` 通常直接返回 `answer`。只有 summary 命中一个硬编码
   的英文展示字符串模式时才退回 fact，并且只渲染前三个 claim 和三个 URL。
6. conflict、uncertainty、freshness risk 和精确的 claim-to-source 边都不会到达用户。
   因而流畅答案可能表现得比上游 aggregate 所支持的更确定或更新。
7. 浏览器公网目标识别会正确消费有序结构化结果 URL。Info 的最终 source 顺序不能与
   面向回答的 projection 耦合，也不能被本地字面重排改变。

## 设计原则

1. **Info 负责聚合语义。** SparkClaw 保留上游的 fact、conflict、source 和顺序决策。
2. **SparkClaw 负责信任边界。** 使用前校验 transport identity、引用、公开 URL、
   大小限制和输出安全。
3. **校验不等于重新聚合。** 无效引用可以被拒绝或标记遗漏；有效 claim 不会被改写、
   按语义合并、去重或重新评分。
4. **语义单元保持完整。** fact 和 conflict viewpoint 要么作为整体进入 projection，
   要么整体省略；不会被裁成命中 query 的子串。
5. **覆盖度与质量分开。** SparkClaw projection 忠实度不同于 Info 返回的 uncertainty
   或 staleness risk，也不代表 Info 内部 provider 的覆盖度。
6. **Citation 是边，不是 URL 列表。** 用户可见引用从每个 fact 或 viewpoint 的
   source ID 推导；被引用 source 能否提供链接是另一个独立属性。
7. **上游建议只是数据，绝不是控制。** `recommended_next_actions` 可以保留在 raw
   result 中，但不能成为 Workflow transition、`next_step_hint`、tool call 或模型指令。
8. **一个结果，多个消费者视图。** 回答渲染与浏览器目标选择消费同一持久化结果的不同
   只读 projection。

## 已核对的 Info 快照

本设计已按 Infinimesh Info `b70c08c`（2026-08-14）重新核对。相关实现具有以下可观测
语义：

- 同步 query 装配可以并发调用多个已启用、适合同步路径的 provider；只有部分 provider
  失败时，仍会用成功结果继续处理；
- Info 合并这些结果，应用自身 source policy 和 `WeightedRouter` 分数，稳定排序合并集，
  再按最终顺序分配 `src_NNN` ID；
- 规则 pipeline 生成 summary、fact、freshness、uncertainty 与 conflict；可选 LLM
  enhancer 只有在 source ID 通过 Info citation guard 后，才能替换
  summary/fact/conflict；
- `sources[].authority_score` 是 Info 在最终 source record 上暴露的路由分数，不是
  SparkClaw 分数，也不保证保留底层 provider 的原生分数；
- `AgentContextResponse` 不包含底层 provider 名称、provider 覆盖度、provider 失败状态，
  也没有独立 aggregate quality score。

因此，SparkClaw 的有序输入是返回的 `sources[]`，不是 Exa、Parallel、Doubao、Kimi、
Ark 或其他底层 backend 的原生顺序。Info 返回 `status=ok` 也不能证明所有配置的 backend
都参与了请求。SparkClaw 不得从 source 数量推断 provider 覆盖度，也不得把自身
projection status 转述成上游覆盖度结论。

## 上游契约

SparkClaw 应镜像当前 Info `response_mode=agent_context` 契约，而不是继续维护缩水版：

```go
type AgentContextResponse struct {
    RequestID     string
    Status        string
    AnswerContext AnswerContext
    Sources       []SourceRef
    Usage         Usage
}

type AnswerContext struct {
    Summary                string
    KeyFacts               []KeyFact
    Conflicts              []Conflict
    Freshness              FreshnessStatus
    RecommendedNextActions []string
    Uncertainty            []string
}

type KeyFact struct {
    Claim      string
    Confidence string
    Sources    []string
}

type Conflict struct {
    Topic      string
    Viewpoints []Viewpoint
}

type Viewpoint struct {
    Claim   string
    Sources []string
}

type FreshnessStatus struct {
    Status           string
    LatestSourceDate *string
    StalenessRisk    string
}

type SourceRef struct {
    ID             string
    Title          string
    URL            string
    SourceType     string
    PublishedAt    *string
    RetrievedAt    string
    AuthorityScore float64
    Snippets       []string
}

type Usage struct {
    CostCredits int
    TokenType   string
    CacheHit    bool
}
```

当前 wire presence 规则如下：

| 字段 | Wire 规则 |
|---|---|
| `request_id`、`status`、`answer_context`、`sources`、`usage` | 必需的 response envelope；成功 query 的 status 必须精确为 `ok`。 |
| `summary`、`key_facts`、`freshness` | `answer_context` 的非可选成员；空 collection 只规范化为空，不编造内容。 |
| `conflicts`、`recommended_next_actions`、`uncertainty` | 可选成员；字段缺失不属于 projection loss。 |
| `latest_source_date` | 可选 freshness 成员；当前输出可能是 RFC 3339 或 date-only 格式。 |
| source 的 `id`、`title`、`url`、`source_type`、`retrieved_at`、`authority_score` | 声明的 source 成员；`url` 的类型是 string，但 query-path 天气响应中可以为空。 |
| source 的 `published_at`、`snippets`；usage 的 `cache_hit` | 可选成员。 |

当前契约没有 `answer_context.citations` 字段。Citation 边只存在于
`key_facts[].sources` 和 `conflicts[].viewpoints[].sources`。也没有需要镜像的 provider
provenance、provider coverage 或 aggregate quality 字段。为了 forward compatibility，
未知新增 JSON 字段仍可忽略；但以上所有已记录的回答内容或限制字段都必须建模，并由
contract test 覆盖。

Info 的 OpenAPI 当前只把 `answer_context` 和每个 `sources[]` entry 声明为通用 object。
在上游 schema 完善前，SparkClaw contract fixture 必须锁定 Info domain type 与 API 文档
给出的具体 wire shape，并在该结构漂移时让 focused test 失败。

SparkClaw 继续请求 `citation_required=true`、仅公开 context、有界 source 数量和
`response_mode=agent_context`。

## 目标数据模型

### Raw Info Aggregate Result

解码后的精确 Info 响应只转换一次，得到版本化 ToolHub 结果：

```text
info_search_result_v2
  request_id
  status
  query
  provider                       # adapter identity: infinimesh-info
  retrieved_at
  took_ms
  aggregate
    summary
    facts[]
    conflicts[]
    freshness
    uncertainty[]
    recommended_next_actions[]   # 仅作为上游 advisory 保留
  sources[]                      # 保留 Info 最终顺序
  usage                          # 不含 secret 的有界 metadata
  untrusted=true
```

新 output 只在 `aggregate` 下保留一份回答事实来源；不再并行生成顶层 `summary`、
`answer`、`key_facts` 和 `citations` 副本。`sources[]` 继续位于顶层，因为浏览器目标
识别需要独立于回答渲染来消费 Info 的有序结构化 source 列表。

完整有界结果继续保存在 `ToolCall.Result` 和正常 observation artifact 链路中。
Model-visible 和 user-visible projection 不会替换该持久化 source record。

### 经校验的 Aggregate Directory

`websearch` 从 raw result 构造一个不可变、经校验的 directory，其中包含：

- 冻结 query 与 provider request ID；
- 按 Info 返回顺序排列的 aggregate 单元；
- 以 source ID 为 key 的唯一 source directory；
- claim/viewpoint 到 source ID 的边；
- 类型化 freshness 与上游 uncertainty；
- validation finding 与 projection coverage；
- 供浏览器消费者使用的有序、可链接公开 source URL 子集。

其中不存在本地 relevance score。

## 校验规则

两个消费者 projection 都必须先经过校验。

### Envelope

- 冻结的 ToolCall query 必须非空，并与持久化结果 query 完全相等。
- Provider 必须为 `infinimesh-info`，request ID 必须非空，status 必须精确为 `ok`，
  `untrusted` 必须为 true。
- 响应必须满足配置的 transport body 限制，映射后的结果必须满足 ToolHub
  observation/artifact 限制。

### Sources

- Source ID 必须非空且唯一。
- Source identity 与 linkability 分别校验。ID 有效但 URL 为空或无效的 source 可以保留为
  不可链接 citation record；SparkClaw 不得编造或替换 URL。
- 只有绝对公开 HTTP(S) URL 才能渲染为 hyperlink。浏览器目标选择继续执行更严格的
  HTTPS、DNS、IP 和 redirect 校验，并忽略不可链接 source。
- 标记无效 entry 后仍保留 source 顺序和原始 index；无效 URL 不会被静默替换。
- 存在日期时进行解析，接受当前 RFC 3339/RFC 3339 Nano 与 date-only 输出。无效日期
  产生类型化 validation finding，SparkClaw 不推断替代日期。
- Authority score 只作为 Info metadata 保留，不参与 SparkClaw 本地重排，也不描述为
  底层 backend 的原生分数。

### Facts And Conflicts

- 空 claim 无效。
- Fact 或 viewpoint 的每个 source ID 都必须能在 source directory 中解析。
- 在 citation-required 请求中，没有指向有效 source record 的边时，fact 不能作为回答
  证据。该 fact 仍保留在 raw result 中，并记录为被省略的无效单元。有效但不可链接的
  source record 仍满足 source-ID 解析，只生成没有 hyperlink 的 citation label。
- Conflict 只有在 topic 非空、并且至少两个非空 viewpoint 仍有有效 source 边时才可用。
- Fact、conflict、viewpoint 和 source 都保留上游顺序。
- Confidence 只作为 Info metadata 保留，绝不转换为本地排序权重。

### Freshness And Advice

- Freshness status 和 staleness risk 按字符串保留；当前已知词表为 `current` 以及
  `low`、`medium`、`high`。未知非空值原样保留并形成 contract finding，不做猜测。
- `uncertainty[]` 作为不可信 limitation evidence 原样保留。
- `recommended_next_actions[]` 只保留在 raw aggregate 中；model prompt、确定性答案、
  Workflow transition 和 control metadata 均不得包含它。
- 当前规则 pipeline 与 LLM draft 通常不会填充 `recommended_next_actions`，但为了
  forward compatibility，已记录的可选字段仍需保持隔离。
- SparkClaw 不自行合成 provider 降级或 provider coverage warning，只展示 Info 返回的
  uncertainty，以及 SparkClaw 自身的校验和 projection omission。

## 消费者 Projection

### 回答 Projection

`info_aggregate_projection_v4` 替换按字面重排的 v3 projection，结构为：

```text
schema_version=4
status=complete | partial | failed | no_results
request_id
query
summary?
facts[]
  ref
  claim
  confidence?
  source_ids[]
conflicts[]
  ref
  topic
  viewpoints[] { ref, claim, source_ids[] }
freshness
uncertainty[]
sources[] { id, title, url?, linkable, source_type, published_at, retrieved_at }
omissions[]
limitation_required
untrusted=true
```

Projection 顺序是确定的：

1. freshness 与 uncertainty metadata；
2. 能完整容纳、且至少存在一个可用 cited answer unit 时，把 aggregate summary 作为整体；
3. 按 Info 返回顺序加入 fact；
4. 按 Info 返回顺序加入 conflict；
5. 只加入已接纳单元引用的 source，过滤时不改变它们在 Info 最终 `sources[]` 中的相对顺序。

选择过程不使用 query term。按完整单元加入，直到 byte budget 用完；过大或剩余单元以
准确 reason 和 count 标记省略，不会把 claim 文本截成另一个 assertion。正常确定性答案
不需要 source snippet，因此回答 projection 不包含 snippet。Snippet 继续留在持久化结果中，
供 audit、debug 和未来经过明确设计的消费者使用。

Projection status 的含义为：

- `complete`：契约有效，所有可用上游回答单元和所需 source 边都已加入；
- `partial`：至少一个可用单元或边因无效引用或 projection capacity 被省略；
- `failed`：identity、trust 或 structural 校验导致 aggregate 不可用；
- `no_results`：有效 aggregate 中没有 citation-backed fact 或 conflict viewpoint；未绑定
  source 的 summary 或未被引用的 URL 不会变成回答证据。

没有可选 conflict 或 uncertainty 不会使 projection 变成 partial。上游 uncertainty
非空或 staleness risk 为 high 时，设置 `limitation_required=true`，但不会把忠实完整的
projection 改成 partial。`complete` 只表示 SparkClaw 忠实投影了返回的 aggregate，
绝不表示所有 Info backend 都成功或上游覆盖完整。

### 浏览器目标 Projection

浏览器消费者不使用回答 projection。它从 validated directory 中按 Info 最终 source
顺序读取 `sources[]`，跳过不可链接 entry，选择第一个通过现有公网目标安全门的 URL。

持久化 evidence 继续绑定：

- Info request ID；
- 原始 result index 与 source ref；
- canonical entry URL 与 normalized final URL；
- redirect chain 与 safety result。

回答预算、fact、conflict 和本地展示选择都不能重排该视图。

## 确定性用户渲染

`browser.internet_search` 继续使用 grounded Workflow，Info 返回后不增加第二个模型
finalizer。类型化 renderer 只消费 `info_aggregate_projection_v4`。

渲染规则如下：

1. 只有 cited fact 或 conflict viewpoint 使回答可用时，才把上游 summary 作为开头。
   取消展示字符串嗅探，不做本地 summary 改写，也不为 summary 编造 citation 边。
2. 按 Info 返回顺序渲染已接纳 fact；每个 fact 携带从自身 source ID 解析出的 citation marker。
3. 明确渲染 conflict，并分别引用各 viewpoint 的 source。
4. 当 staleness risk 不是 low，或 latest source date 对当前信息答案构成实质边界时，
   渲染 freshness。
5. 在“限制”部分渲染每一条已接纳 uncertainty。
6. 使用相同 citation marker 渲染精简 source 列表。有效但不可链接的 source 以普通 label
   展示，绝不生成伪造链接。
7. Status 为 partial 时说明 projection omission，绝不把 partial 静默展示为 complete。
8. 对 `no_results` 或 `failed`，返回类型化的“不可用/没有可靠结果”，不能把 provider
   状态句当作答案暴露。

Info 返回的文本会规范化为普通展示文本。HTML 会被转义或移除，上游文本中的 Markdown
控制语法不会执行，hyperlink 只从经过校验的 source metadata 创建。上游内容绝不能发出
tool call 或改变 Workflow state。

## Observation 与 Audit 契约

继续使用正常的不可信 `toolResultMessage` envelope。Info structured fields 只应暴露：

- result 与 projection schema version；
- projection status 与 `limitation_required`；
- fact、conflict、source、linkable source、uncertainty 和 omission count；
- 当前 policy 已允许的 request/artifact ref；
- 描述 evidence boundary 的 SparkClaw 自有静态指令。

不得把 `recommended_next_actions` 复制到 `next_step_hint` 或任何类似控制的字段。

Projection 进入模型消费者时，复用现有 Agent-owned workflow evidence projection audit
抽象记录 model-visible telemetry。Grounded 确定性回答路径记录 result version、projection
digest/bytes、coverage status、omission reason code 和 source lineage；audit fields 不记录
raw query 或 answer 文本。

## 持久化兼容

当前未完成或可恢复的浏览器 run 可能已经保存现有无版本 ToolHub 结果结构，因此需要一个
只读兼容 decoder：

- 新 call 只生成 `info_search_result_v2`；
- 持久化 legacy result 可以解码为 validated directory；
- Legacy `summary` 与 `key_facts` 映射到 aggregate 单元；
- Legacy URL citation 根据持久化 result source 解析；
- 缺失的 typed conflict、freshness 和 uncertainty 记录为 legacy omission，不得编造；
- 新 producer 不再写 legacy 顶层 `answer` 副本。

兼容只存在于 persisted-result decoder，不能形成两套 live projection 或 renderer。超过最长
可恢复 run 生命周期和支持的升级窗口后，在独立 cleanup 变更中删除 decoder 及其 fixture。

## 实现边界

| Owner | 变更 |
|---|---|
| `internal/infinimeshinfo` | 镜像完整上游 `AgentContextResponse`，增加 decode/contract test。 |
| `internal/websearch` | 一次性 normalize，校验 aggregate/source 图，保留顺序，并在不做字面重排的前提下构建有界消费者 projection。 |
| `internal/toolhub` | 发布版本化 result schema，同时保留有序 `sources[]` 供浏览器目标识别使用。 |
| `internal/agent` | 投影类型化 observation、确定性渲染答案、报告 limitation，并解码持久化 legacy result。 |
| Workflow Profile | 保留冻结 query 与单次 `web.search` completion rule；不增加第二次搜索、page read 或 finalizer call。 |
| Docs/eval | 更新现行 integration contract，增加确定性 aggregate fixture 与用户流程 case。 |

不要在 Agent 或 ToolHub 创建平行 aggregate registry。响应类型和 normalize rule 的唯一 owner
是 `infinimeshinfo` 与 `websearch`，下游 package 只消费 projection。

## 实现记录

- `infinimeshinfo` 已锁定完整 aggregate wire shape、必需成员存在性、optional null 与
  additive-field tolerance。
- ToolHub 只写 `info_search_result_v2`；一个只读 decoder 在共享校验前规范化持久化
  legacy result。
- Aggregate projection v4 已替代字面排序与 substring 提取，按 Info 顺序加入完整单元，
  并隔离上游 action。
- Grounded renderer 展示按单元绑定的 citation、conflict、freshness、uncertainty、
  不可链接 source 和 projection omission，不增加第二个模型 finalizer。
- 浏览器目标识别消费独立的有序 source 视图，保留原始 source identity 与现有 URL 安全门。
- Focused contract、projection、renderer、Workflow 和 browser-order test 已覆盖实现边界；
  当前行为也已写入 `integrations.md`、`workflow-capabilities.md` 和 `architecture.md`。

## 测试矩阵

| 层 | 必须覆盖的 case |
|---|---|
| Info client | 完整 aggregate decode、required/optional/null field 处理、新增未知字段、response mismatch、body bound、当前与 malformed date/value 形式。 |
| Websearch adapter | 精确 fact/conflict/Info 最终 source 顺序、有效 source 边、无效/缺失边、重复 ID、可链接/不可链接 source 分离、公开 URL 过滤、无本地评分。 |
| Projection | Complete、partial、failed、no-results、完整单元 byte admission、确定性输出、limitation 传播、无 snippet/upstream action。 |
| Renderer | Summary + cited facts、分别带 citation 的 conflict viewpoint、freshness warning、uncertainty section、partial omission notice、安全纯文本。 |
| Security | HTML/Markdown injection、每类上游字段中的指令文本、恶意 recommended action、不安全 citation URL、source-ID spoofing。 |
| Compatibility | File/PostgreSQL snapshot 中 legacy persisted result 的解码；无新 legacy producer。 |
| Browser | Info-final-order URL 选择、跳过不可链接及首个 unsafe result、稳定原始 index/ref、回答 projection 无法重排 target candidate。 |
| Workflow | 精确一次 `web.search`、冻结 query 不变、typed outcome、无 page read、无额外 finalizer、通过 Web/connector 共同 delivery 交付 grounded result。 |
| Eval | 当前价格/新闻/catalog、多来源比较、明确 conflict、stale evidence、上游 uncertainty、零结果、超大 aggregate。 |

## 验收标准

以下条件全部满足时，实现才算完成：

1. SparkClaw 对每个已记录的上游 aggregate 字段都有类型与测试。
2. 生产路径不再对 Info fact、conflict 或 source 做字面重排、语义合并或改写。
3. 每个渲染的 factual claim 或 conflict viewpoint 只解析到其自身的有效 source ID。
4. 上游 uncertainty、staleness risk 和 projection omission 在需要 limitation 时对用户可见。
5. 上游 recommended action 无法进入模型指令、Workflow control 或确定性用户输出。
6. 浏览器公网目标选择保留 Info 最终 source 顺序，跳过不可链接 entry，并保留全部现有
   HTTPS/DNS/IP/redirect 安全检查。
7. Raw result 保持有界并持久化；回答 projection 满足配置 envelope，且不会把 claim 裁成新 assertion。
8. Focused Go test、完整 Gateway test/vet gate、确定性 eval 和双语文档检查全部通过。

## 被否决的方案

- **在 Info fact 上运行第二个本地 ranker。** 会覆盖上游 aggregate，还可能把 claim 与预期
  evidence set 分离，因此否决。
- **把完整 aggregate 交给 SparkClaw finalizer model。** 正常搜索路径会增加延迟和另一轮
  synthesis，可能改变 conflict、uncertainty 与 citation，因此否决。
- **只使用上游 summary。** 会丢失结构化支持、conflict、freshness 和 limitation 语义，因此否决。
- **把上游 recommended action 暴露为 next-step hint。** 不可信上游数据不能控制
  Workflow 执行，因此否决。
- **回答与浏览器目标选择共用一个 projection。** 回答 byte budget 和 source admission 不能改变
  安全门控浏览器消费者使用的 Info-ranked URL 顺序，因此否决。
