# 外部 MCP 普通对话能力收敛设计

> 语言：[English](../../docs/external-mcp-conversation-design.md) | 简体中文

| 字段 | 值 |
|---|---|
| 状态 | 已在当前工作树实现；生产 ISCP 上线仍待完成 |
| 决策日期 | 2026-08-13 |
| 范围 | 外部 MCP 入站业务能力 |
| 业务工具 | `sparkclaw.conversation.send` |
| 内容边界 | 普通文本，以及关联 SparkClaw workspace 下经过治理的多媒体文件 |
| 协议 | 现有认证传输之上的 MCP `2025-06-18` |
| 替代内容 | 入站 MCP 业务面中的路由叶子投影与逐叶子授权 |

## 决策

SparkClaw 将把外部 MCP 的业务面收敛为普通对话，不再把大量 Catalog 路由叶子投影成
MCP tool。一个有效的外部 MCP Binding 只暴露一个业务 tool：
`sparkclaw.conversation.send`。

该 tool 向 Binding 关联的 SparkClaw 会话提交一条普通消息。消息可以包含 owner 编写的文本，
以及零个或多个 workspace 多媒体 locator。SparkClaw 执行与其他消息来源相同的自然语言路由。
当路由选择普通对话后，`conversation.answer` revision 3 先判断响应是否需要多媒体，并解析所需
workspace 文件，再根据冻结的决策回答。结果通过共享 Delivery 链路返回同一个 MCP 来源。

这不是缩小后的 Catalog 投影。外部 client 不选择、不命名，也不会获得某个路由叶子、operation、
Workflow Profile 或 tool effect 的授权。消息进入系统后，由 SparkClaw 执行自然语言路由，
这与普通 Web、微信、Telegram 或 Timer 消息一致。

MCP 初始化、Access Ticket 兑换、Binding 撤销、持久 operation 状态查询与取消继续保留，
但它们属于协议和生命周期控制，不是额外业务能力。

SparkClaw 自身负责的 contract、Runtime、Store、Delivery 与 owner UI 变更已经实现。生产启用仍
需要可部署的 external Access Gateway 与真实 ISCP Relay 验证。

## 产品语义

“外部 MCP 可以发送 workspace 下的多媒体”同时表示：

1. 外部 client 可以提交普通对话请求。
2. 请求可以通过相对路径、完整文件名，或者在不知道完整名称时提供有界的 owner 搜索短语，
   指定已存在于关联 SparkClaw workspace 下的文件。
3. 路径由 SparkClaw 解析和校验，而不是由 client 解释。
4. 有效文件以普通 image、audio 或 file part 进入 Message Runtime。
5. 包含多媒体的结果在同一个 MCP operation 中作为真实 MCP content 返回，而不是只返回
   本地路径或不透明 artifact ID。
6. client 不获得可独立调用的 workspace 列表、搜索、读取或修改 API。当一个响应媒体 locator
   匹配多个 eligible 文件时，SparkClaw 只选择并发送 server 排序的 Top-1 文件。
7. 用户在 WebChat 直接选择的文件，以经过治理的 workspace 相对资源引用进入 Workflow
   Runtime，绝不会以 client 提供的绝对 host 路径进入。

本文的“多媒体”沿用 SparkClaw 现有消息语义：image、audio 和 file part。视频及其他二进制
格式在共享消息 contract 新增对应 typed kind 之前都属于 file part。MCP adapter 不维护独立的
媒体分类体系。

## 目标

- 对外提供一个稳定、provider-neutral 的 MCP 业务能力：携带 workspace 多媒体的普通对话。
- 复用现有 `MessageEnvelope`、自然语言 router、Workflow、Policy、approval、Store 和
  Delivery contract。
- 把普通对话约束为两个有序 Workflow 节点：先检测并冻结响应多媒体，再根据该决策回答。
- 支持纯文本、纯媒体、文本加媒体消息，不新增 MCP 媒体专用路由通道。
- workspace 所有权和路径校验完全由 SparkClaw 负责。
- 渐进式解析文件：先复用直接附件/path，再尝试精确 basename 索引；用户不知道完整文件名或
  精确索引未命中时，再使用有界 `files.search`。
- 协议存在原生表示时，通过标准 MCP tool result content type 返回经过治理的媒体字节。
- 保留持久幂等、有界执行、取消、恢复、connector 启用状态、Binding 撤销与脱敏 audit。
- 从外部 MCP onboarding 和普通调用中删除 Catalog revision、路由叶子、operation、effect
  与 approval grant 选择。

## 非目标

- 向外部 MCP client 暴露 SparkClaw Catalog、Workflow Profile、ToolHub tool 或语义路由图。
- 为每个路由、provider、媒体类型、扩展名或目标新增 MCP tool。
- 允许任意绝对路径、`file://` URI、artifact 路径、远程 URL 或其他 session workspace 的路径。
- 提供 `resources/list`、workspace 目录浏览、通配符展开、可独立调用的文件名搜索 tool、内容
  搜索或通用文件下载 API。检测节点可以为当前响应媒体请求把仅匹配文件名的有界
  `files.search` 作为内部 fallback，并为每个 locator 选择一个 server 排序结果。
- 允许外部 client 指定 capability ID、路由 operation、目标叶子、Workflow revision、risk、
  effect、approval 决策、delivery endpoint 或 model lane。
- 把外部设备的字节上传到 SparkClaw workspace。首版 contract 只选择已经存在于关联
  workspace 下的文件。
- 改变 SparkClaw 到 LocalMind 的出站 workspace MCP client。
- 改变 JingSi 独立的 bridge 与 binding 设计。
- 替代产品能力路由。浏览器操作、时效事实搜索、文档读取/编辑、调度和媒体生成仍由普通对话
  Workflow 之前选中的独立注册 Workflow 负责。

## 唯一业务 Tool

Binding 认证后，`tools/list` 返回 `sparkclaw.conversation.send`，以及延迟执行所需的现有
Binding-scoped operation 控制 tool。operation 控制 tool 属于基础设施，不创建新对话消息。

业务 tool 使用固定、由 server 所有的 schema revision。首版 schema 提案如下：

```json
{
  "name": "sparkclaw.conversation.send",
  "title": "Send a SparkClaw conversation message",
  "description": "Send ordinary text and existing workspace media to the linked SparkClaw conversation.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "text": {
        "type": "string",
        "maxLength": 65536
      },
      "media": {
        "type": "array",
        "minItems": 1,
        "maxItems": 8,
        "items": {
          "type": "object",
          "properties": {
            "path": {
              "type": "string",
              "maxLength": 4096
            },
            "name": {
              "type": "string",
              "maxLength": 255
            },
            "query": {
              "type": "string",
              "maxLength": 255
            },
            "caption": {
              "type": "string",
              "maxLength": 2000
            }
          },
          "oneOf": [
            { "required": ["path"] },
            { "required": ["name"] },
            { "required": ["query"] }
          ],
          "additionalProperties": false
        }
      }
    },
    "anyOf": [
      { "required": ["text"] },
      { "required": ["media"] }
    ],
    "additionalProperties": false
  },
  "annotations": {
    "readOnlyHint": false,
    "destructiveHint": false
  }
}
```

仅有空字符串或全空白 `text` 不能构成有效请求；仅有空 `media` 数组也不能构成有效请求。
请求至少包含一个非空文本 part 或一个有效媒体项。

`media` 有意设计成一个有序数组，而不是分别提供 image、audio、video 和 document 参数。
顺序是普通 multipart 消息语义的一部分，每个 part 的 kind 由共享消息 contract 根据经过治理的
content type 决定。

每个媒体项只能提供一种 locator：

- `path` 是精确的 workspace 相对路径，例如 `exports/annual-report.pdf`；
- `name` 是完整 basename，例如 `annual-report.pdf`，用于调用方不知道所在目录时走精确索引
  快速路径；
- `query` 是不完整名称或简短的 owner 描述，例如 `年度报告`，只供有界 `files.search` fallback
  使用。

例如，下面两个请求可以选中同一个文件：

```json
{
  "text": "Send this file.",
  "media": [{ "path": "exports/annual-report.pdf" }]
}
```

```json
{
  "text": "Send annual-report.pdf.",
  "media": [{ "name": "annual-report.pdf" }]
}
```

不要求调用方知道完整文件名。等价 fallback 形式如下：

```json
{
  "text": "把年度报告发给我。",
  "media": [{ "query": "年度报告" }]
}
```

### 直接选择的附件

用户在 WebChat 选择文件，并不等于把它的绝对路径加入对话 contract。上传实现内部可以使用
绝对 host 路径，但它同时把对象写入 session workspace，并返回安全的 `rel_path`。消息 ingress
会清理该值，Workflow Runtime 消费的是如下经过治理的资源：

```json
{
  "kind": "workspace_file",
  "ref": "uploads/20260813/report.pdf",
  "provenance": "current_turn_attachment"
}
```

公开消息 contract 还可以携带 server 签发的 artifact identity，但不能接受或持久化绝对路径。
因此，直接附件表示“当前 turn 已经具有一个有界的 workspace 资源”，而不是“client 可以读取
任意 host 路径”。检测节点无需目录扫描即可复用该资源，但它成为响应媒体前仍必须经过治理并
冻结 identity。

## 普通对话 Workflow Revision 3

两节点规则只在普通意图路由已经选择 `conversation.answer` 后适用，不替代能力路由。
`answer` 与 `publish` 两个语义 variant 都解析到 revision 3；只有当前 turn 附件的媒体消息可以
根据 typed content 确定性选择同一个 Workflow。

```text
普通消息 ingress
  -> 语义或 typed 能力选择
  -> conversation.answer r3
      -> detect_response_media
      -> answer
  -> WorkflowResult
```

### 节点一：`detect_response_media`

该节点判断本次请求的响应是否应包含多媒体。它只消费冻结的 owner 原始问题、已选择的 route
variant、当前 turn 已经过治理的资源，以及 ingress 携带的显式 `media[].path`、
`media[].name` 或 `media[].query` locator。

附件存在本身不代表发送指令。文件可能是读取、总结、检查或编辑所需的输入证据，而响应仍然是
文本，或者包含一个新生成的 artifact。因此，该节点为当前 turn 的每个资源赋予
`input_evidence` 或 `response_candidate` 等 typed role；只有明确要求返回/发布某个资源时，
才能把它选为响应媒体。

普通对话的所有文件解析和多媒体治理都属于该节点。确定性 resolver 可以绑定 owner 提供的
locator，但模型不能虚构、排序或替换路径。解析顺序固定为：当前 turn 已治理资源或精确相对
path、精确 basename 索引、最后一次有界 `files.search` fallback。fallback 由节点本身使用
Runtime 固定的 workspace scope 和预算调用，不是模型选择的 tool。节点校验 workspace 边界、
对象类型、content type、字节限制、稳定 identity 和 hash，然后为下一节点冻结有序
`workspace_file` 资源引用。

该节点持久化一个 typed decision：

| 决策 | 语义 |
|---|---|
| `none` | 响应仅包含文本；没有选中响应媒体资源 |
| `selected` | 每个请求 locator 冻结一个经过治理的引用；显式多个 locator 保持输入顺序 |
| `clarify` | 完整 lookup 得到零个 eligible 文件；没有选中资源 |
| `blocked` | 搜索或遍历不完整、对象不安全/不受支持/已变化，或超过限制 |

对于 `selected`，decision 只包含 server 派生的资源 identity、workspace 相对 ref、展示 metadata、
hash 和顺序，绝不包含绝对路径。其他 decision 的 selected-resource 列表都为空。

### 节点二：`answer`

`answer` 硬依赖 `detect_response_media`，并且只消费其冻结的 decision 和资源。它不能列目录、
解析另一个名称、增加或删除文件、重新打开自由 locator，也不能替换资源。

- `none` 根据 owner 问题和允许的会话上下文生成普通无 tool 文本回答。
- `selected` 严格使用已经冻结的资源生成普通有序 multipart 结果。纯 publish 场景仍是确定性的，
  不需要模型选择内容。
- `clarify` 只用于完整搜索的零结果，并请 owner 修改名称/描述或附加文件。
- `blocked` 生成有界失败投影，不发送媒体。

完整 multipart 结果在 Delivery 前仍需原子治理。若资源在检测和结果构造之间发生变化，则 fail
closed；回答节点不能静默刷新 binding。

### 决策示例

| Owner 请求 | 路由与检测 | 结果 |
|---|---|---|
| 无附件，`幂等是什么意思？` | `conversation.answer`；`none` | 普通文本回答 |
| 无附件，`发送 report.pdf` | 在 `detect_response_media` 尝试精确 basename 索引 | 冻结精确匹配；精确未命中时运行有界 fallback |
| 显式 `media[].name = report.pdf` | 在 `detect_response_media` 解析精确 basename | 与等价精确相对 locator 相同的冻结结果 |
| `把年度报告发给我` 或显式 `media[].query = 年度报告` | 精确阶段无法绑定后运行仅匹配文件名的 `files.search` | 冻结并返回 server 排序的 Top-1 结果 |
| 直接选择附件并说`发送它` | 复用当前 turn 的 `workspace_file`，不扫描目录 | `selected`，冻结该资源 |
| 直接选择附件并说`总结它` | 路由到 `document.read`；附件是输入证据 | 由文档 Workflow 返回文本总结，不重新发布输入附件 |
| 搜索返回多个 eligible 文件 | 按文件名相关性排序，仅用相对路径字典序作最终 tie-break | 只冻结并返回 Top-1 |
| 搜索没有 eligible 文件 | `clarify`，reason 为 `file_not_found` | 不发送媒体；请 owner 细化 query 或附加文件 |
| 附件是输入，而请求操作会生成不同媒体 | 路由到已注册的生成/编辑 Workflow | 只有该 Workflow 经过治理的输出 artifact 才能成为结果媒体 |

## 渐进式文件解析

`media[].name`、`media[].query`，以及普通发送请求中的文件名/描述 token，会在
`detect_response_media` 内调用由 server 所有的 resolver。MCP adapter 在 Workflow 执行前
只做 schema 与 locator 语法校验；它不解析文件，也不向 MCP client 暴露 `files.search`。

解析顺序固定，只有安全选中或 typed terminal decision 后才停止：

1. 复用 owner 显式选择、当前 turn 已经过治理的资源。
2. 若存在精确 `media[].path`，则校验该路径。
3. 使用 `media[].name` 或从文本解析出的完整文件名查询区分大小写的精确 basename 索引。
4. 精确阶段返回零候选，或者 owner 只提供不完整名称/描述时，使用冻结的 owner query 调用一次
   `files.search`。

精确索引得到多个相同 basename 时不进入更宽的 search。由于它们的文件名相关性相同，resolver
只使用 workspace 相对路径字典序作最终确定性 tie-break，并只冻结第一个 eligible 文件。

### 精确 Basename 索引

Resolver 在关联 session 的有效 workspace 下递归比较请求值与文件 basename。首版只使用
区分大小写的精确文件名相等判断，不使用 substring、glob、正则、edit distance、embedding、
文件内容或模型匹配。`name` 必须是单一 basename，不能包含 `/`、`\\`、URI scheme、`.`/`..`
路径 component 或 NUL 字节。

解析只有以下结果：

| 匹配数 | 结果 |
|---|---|
| 一个 | 冻结唯一 workspace 相对路径，继续执行普通媒体治理 |
| 零个 | 继续执行有界 `files.search` fallback |
| 多于一个 | 按 workspace 相对路径字典序选择一个，治理并冻结为 `selected` |
| 遍历预算耗尽 | 返回 typed `file_lookup_incomplete`，绝不返回不完整匹配集 |

遍历复用一套共享 workspace traversal policy，并跳过不属于普通 workspace lookup 范围的内部或
依赖目录。它可被 context 取消，并由 entry count、depth、elapsed time 和 candidate count
共同限制。找到一个匹配不能提前停止扫描：必须完成 eligible traversal 才能应用稳定 tie-break。
候选顺序稳定，按字典序排列。

精确候选集不得暴露绝对 workspace root、host path、content preview 或无关文件 metadata。
胜出的 workspace 相对 ref 只作为内部治理证据；MCP 结果构造只返回冻结 Top-1 文件的实际字节。

作为普通对话便捷形式，只含文本、具有 send/publish 动词和文件短语的请求，例如
`Send annual-report.pdf`、`send the annual report` 或 `把年度报告发给我`，当 token 是完整
文件名时可解析成 `name`，否则解析成 `query`。解析过程确定且 Unicode-aware，不让模型虚构
路径或排序结果。

### 有界 `files.search` Fallback

`files.search` 是内部 ToolHub capability，在这个阶段只暴露给 `detect_response_media`。workspace
root、query、timeout、traversal budget 和最大候选数由 Runtime 绑定，而不是由模型或外部
client 提供。此模式只把冻结 query 与文件 basename 比较；计算相关性时不得检查文件内容、
content preview、提取文本或目录路径 component。Search observation 属于不可信证据，不能授权
发送文件。

当前 ToolHub 实现会返回绝对路径和可选 content preview，并且可能达到 `max_results` 后停止，
却不报告扫描是否完整。这种 raw result 不适合直接进入本 Workflow contract。实现必须修订共享
tool output，或者增加一个 Workflow-owned adapter，满足：

- 把每个 path 转换成校验后的 workspace 相对候选，并拒绝关联 session workspace 之外的候选；
- 每个候选在持久化前只返回经过校验的 `rel_path`、basename、server 所有的文件名相关性 score
  和 typed match reason；
- 持久化或向用户投影前删除 workspace root、host path、content preview、提取内容和无关文件
  metadata；
- 记录遍历是否完成、候选列表是否被截断；
- 去重并按 server 所有的文件名相关性 score 排序，只在最终并列时使用 workspace 相对路径字典序；
- 证明候选枚举在声明的遍历预算内完整，确保未观察到的文件不能取代所选 Top-1。

Fallback outcome 如下：

| Search 结果 | 决策 |
|---|---|
| 一个 eligible 候选且遍历完整 | 治理并冻结为 `selected` |
| 零个 eligible 候选且遍历完整 | `clarify` + `file_not_found`；请 owner 细化 query 或附加文件 |
| 多个 eligible 候选且遍历完整 | 只治理并冻结文件名排序的 Top-1，decision 为 `selected` |
| 遍历截断或不完整，包括已经观察到临时 Top-1 | `blocked` + `file_lookup_incomplete`；不发送文件 |
| Tool 失败或 timeout | `blocked` + `file_lookup_failed`；不发送媒体 |

Ranker 由 server 所有且结果确定；模型不能选择候选或虚构路径。匹配之后不再设置 confidence
threshold：只要完整搜索存在至少一个 eligible 的文件名正匹配，就选择其 Top-1。即使 score 较低，
也会把它作为可能的文件返回，而不会转换成多候选澄清。零个正匹配仍返回 `file_not_found`。

### Top-1 选择语义

一个 locator 最多产生一个冻结的响应文件。多个匹配不进入澄清状态，也绝不作为候选列表返回。
用户显式选择多个附件或提供多个 `media` locator 时仍可生成有序 multipart 响应，每个 locator
按输入顺序只产生一个 Top-1。若选中的任一对象在结果构造前发生变化、不可读或治理失败，整个
响应原子失败。

## Workspace 多媒体边界

每个 `media[].path`，以及从 `media[].name`/`media[].query` 解析出的每个候选，都相对于关联
session 的有效 workspace root 解释。请求本身不携带 workspace root，Binding 也不能选择或
覆盖 workspace root。

Ingress 在持久化消息前拒绝绝对路径、URI scheme、NUL 字节和路径穿越等 malformed locator。
文件系统解析与治理随后在 `detect_response_media` 中对所有媒体项原子执行。节点失败时冻结
`clarify` 或 `blocked`，不选择任何资源，也不产生部分 delivery。这两层检查至少包含以下规则：

- 拒绝空 locator、绝对路径、URI scheme、NUL 字节和路径穿越；
- 清理路径并将其拼接到有效 workspace root 下；
- 通过现有、可感知 symlink 的 workspace guard 解析 workspace root 和候选路径；
- 拒绝 workspace root 自身、目录、socket、设备、named pipe 和 symlink；
- 必须是解析后仍位于同一 root 下的普通文件；
- 路径解析后检查文件，并从治理证据推导 content type，不信任 client 提供的 MIME 或大小；
- 执行前应用 MCP provider 共享的 part 数量、单 part 字节数、总字节数和经过 qualification 的
  transport envelope 限制；
- 拒绝一次请求中解析到同一文件的重复项；
- 把已校验文件 identity 绑定到 operation，防止后续文件替换、symlink 交换或大小变化静默
  改变将要发送的对象。

外部 client 只能在 `path`、`name` 和 `query` 中选择一个，并可提供由 owner 编写的 `caption`。
它不能提供 `artifact_id`、selection identity、绝对 URI、content type、大小、尺寸、hash、
disposition、source 或 understanding summary。这些字段全部由 SparkClaw 派生。

Caption 是与选中 part 关联的 owner-authored 对话文本。文件元数据和提取内容仍属于不可信
resource evidence，不得提升为 owner instruction。

## 普通消息映射

一个 schema 有效的调用只创建一个持久 MCP operation、一条 user message、一个规范化
`MessageEnvelope` 和一个 Agent run。有效的 workspace 相对 locator 在
`detect_response_media` 解析并冻结前仍只是请求数据，并不授权读取或返回对象。

映射关系如下：

| MCP 值 | SparkClaw 值 |
|---|---|
| 有效 Binding 的 owner | `MessageEnvelope.OwnerID` |
| 有效 Binding 的本地 actor | `MessageAuthorization.PrincipalID` |
| 已认证外部设备 | typed MCP requester provenance |
| MCP request 与 idempotency key | 稳定 invocation、operation、message 与 run ID |
| `text` | 普通 text part |
| `media[].path`、`media[].name` 或 `media[].query` | 交给 `detect_response_media` 的 typed 未解析当前 turn media locator |
| Web 直接选择的附件 | 已清理的 `MessageAttachment.RelPath` 与 server 签发的 artifact identity |
| MCP source endpoint | `ReturnToSource` route |
| Binding 关联 session | session 与有效 workspace root |

Adapter 必须用 text、typed media locator 和 `MessageIngressContext` 调用普通消息入口。共享
ingress contract 必须区分未解析的 MCP locator 与已经过治理的 Web 附件；不能调用现有
bound-leaf 入口，不能合成 `RouteDecision`，也不能在 Workflow 执行前把 basename 转成
`MessageAttachment`。

规范化之后：

- 纯文本输入参加普通语义路由；
- 纯媒体输入沿用现有确定性的普通媒体发布行为；
- 文本加媒体输入使用与其他 provider 等价消息相同的 routing projection 与 resource 边界；
- 支持的 Workflow 可以生成文本或经过治理的媒体结果；
- Policy 与 approval 由本地选中的 Workflow 和 effect 推导，不读取 MCP grant bit。

MCP 来源不会让请求变得更安全或拥有更高权限，也不会强制所有请求进入
`conversation.answer`。“普通对话”描述的是 ingress contract；SparkClaw 仍可把 owner 请求
路由到本地支持的 Workflow。

## 结果映射

共享 Delivery Gateway 先按照 MCP provider 限制校验完整 `MessageContent`，随后 MCP sender
把有序 content 映射成 MCP `CallToolResult`。

| SparkClaw part | MCP `content` block |
|---|---|
| Text | `TextContent` |
| Image | 带 base64 `data` 和治理后 `mimeType` 的 `ImageContent` |
| Audio | 带 base64 `data` 和治理后 `mimeType` 的 `AudioContent` |
| 其他文件 | 带 base64 `blob`、治理后 `mimeType` 和非本地 synthetic URI 的 embedded resource |

Synthetic URI 只标识返回的 operation object，不能暴露绝对 workspace 路径、`file://` URI、
host 布局、workspace root 或 bearer credential。文件字节直接嵌入 tool result；client 不需要
`resources/read`，SparkClaw 也不会为本设计声明 MCP `resources` capability。

结果还携带一个有界 `structuredContent` 投影，包含 operation ID、terminal/waiting state、
result status、part kind、display name、content type、byte count 和 SHA-256。它不包含本地路径，
也不重复已经位于 content block 中的原始文本。仅在 MCP 兼容性要求时，才在 text block 中包含
序列化后的结构化投影；它不得复制二进制 payload。

构造结果时，必须从为该次 Workflow result 治理的 artifact 或 workspace object 读取字节，
不能重新打开 client 提供的自由路径。对象缺失、变化、超限或不可读都会导致 delivery 原子失败；
禁止只发送 multipart 的一部分。

### Transport 大小 Qualification

当前 MCP Delivery Provider 声明的逻辑上限是 8 个 part、总 content 4 MiB。该数值本身不是
端到端二进制发送保证。Base64 会放大 image、audio 和 embedded file content，Gateway response
reader、SecureEnvelope、Relay 与外部 Access Gateway 还可能分别具有更小的编码消息上限。

实现必须定义一个经过测试的 MCP result envelope budget。实际媒体上限取共享 Delivery limit
与每个活动 transport 在计入 JSON、base64 和加密开销后的上限最小值，并且必须在发送第一个
字节前针对完整编码结果检查。生产 ISCP 链路在更大边界通过 qualification 之前，任何超过已
证明上限的结果都必须 fail closed，并返回 typed payload-too-large outcome。仅提高逻辑 Provider
limit 不能放宽该边界。

## 延迟 Operation

现有持久 operation 行为继续保留：

- `tools/call` 要求 Binding-scoped idempotency key；
- 立即完成时直接返回最终结果；
- 超过立即等待时间的执行返回持久 operation；
- `sparkclaw.operation.get`、`sparkclaw.operation.result` 和
  `sparkclaw.operation.cancel` 保持 Binding-scoped；
- 相同 key 和相同请求 fingerprint 的重放返回同一 operation；相同 key 的不同请求被拒绝；
- Binding 撤销会拒绝新调用，并按照现有 operation contract 终止或阻断可恢复工作。

此版本继续不声明标准 MCP Tasks。Owner UI 不得把控制 tool 描述成额外 SparkClaw 能力。

## 授权与 Binding 简化

MCP Access Ticket 只授权一件事：把外部设备激活为选定 owner 的普通对话来源。Ticket 不再携带
Catalog 叶子列表、路由 operation、effect、Workflow revision、Catalog revision 或
`allow_approval` 值。

持久 Binding 保留 device 和 owner identity、状态、关联 session、authorization revision、
时间戳与 transport session 证据，不再保存 leaf grant。

MCP onboarding 不能预授权 approval。若本地选中的 Workflow 需要审批，仍由普通 owner
approval record 和 UI 决定结果。外部 requester 可以观察 waiting operation，但不能通过
`sparkclaw.conversation.send` 或 operation 控制 tool 批准它。

因此 owner 管理 UI 将从 Catalog grant picker 改为固定 scope 说明：该设备可以发送普通对话
消息，并接收与此 Binding 关联的文本/媒体结果。Ticket 与 Binding 撤销功能保留。

## 安全不变量

- 通用 `mcp` connector 继续默认关闭，并对 ticket 签发、兑换、ingress、endpoint 可见性、
  execution 和 delivery 全链路设门。
- 外部设备由 ISCP 或显式启用的局域网直连传输认证；SparkClaw 不从 tool argument 接受
  requester identity。
- 外部 requester identity 与本地 executor/owner identity 在持久 invocation、audit、
  endpoint 和 delivery record 中保持分离。
- Binding 固定 owner 与关联 session。调用不能选择其他 owner、session、workspace、source
  endpoint 或 return endpoint。
- 精确索引和有界 fallback search 只属于该 workspace 与当前 invocation，不创建远端可查询的
  文件索引，也不放宽 Binding。
- 消息文本、caption 和外部 metadata 都是不可信输入；workspace 文件内容是不可信证据。
- 选择 workspace 文件不代表获得任意读取能力。只有被显式选择、通过校验且成为 result
  message part 的文件才能通过 MCP 离开本机。
- tool definition、result、日志、audit 或持久 operation 中都不能出现本地路径、workspace
  root、credential、原始 Access Ticket、原始 Pairing Ticket 或 key material。
- deadline、请求大小、并发边界、operation CAS 与 delivery 大小限制仍为强制要求。
- Multipart 校验和结果编码必须是原子的：所有治理后的 part 只发送一次，或者一个都不发送。

## 兼容与迁移

这是对尚未发布的外部 MCP 业务面的有意 breaking change，不应保留投影叶子 tool 的兼容
registry。

实现已按以下顺序落地：

1. 为唯一 conversation tool、直接 path/附件 binding、精确索引到 `files.search` fallback、
   complete/incomplete search outcome、确定性的文件名 Top-1 排序、稳定 tie-break、冻结资源交接、
   普通路由和 workspace 多媒体结果编码增加 contract test。
2. 增加一个 versioned conversation-tool schema，并从 Catalog eligibility 决策中删除 remote
   MCP metadata。
3. 用携带 typed media locator 和经过治理的直接附件的普通消息 runtime 入口替换
   `MCPBoundRouteRequest` 执行。
4. 用单一 conversation-access scope 替换 ticket 和 Binding leaf grant；已有预发布持久记录
   fail closed，或通过显式 audit event 撤销。
5. 删除 leaf tool 生成、grant filtering、stale-leaf 逻辑、Catalog revision 耦合与
   bound-leaf audit event。
6. 把 WebChat Catalog grant picker 和 leaf 数量展示替换成固定 conversation scope。
7. 把 text、image、audio 和其他 file 结果编码成 MCP content block，并验证不泄露本地路径。
8. 更新统一第三方接入设计、architecture、integrations、messaging、deployment、workflow
   matrix、协议 schema、eval 及中英文镜像。

由于旧实现尚未作为稳定 external API 发布，迁移方式是提升 schema version，并 fail closed 拒绝旧
leaf-grant ticket 与 Binding。禁止把旧的叶子级 Binding 静默放宽成通用 conversation access。

## 删除的概念

当前实现不再依赖：

- `sparkclaw.route.<leaf>` tool name；
- remote Catalog leaf eligibility 或逐叶子 projection revision；
- owner 选择的 MCP route operation 或 effect；
- MCP `allow_approval` grant；
- 确定性 bound-leaf Top-1 路由；
- MCP Access Ticket 与 Binding 中的 Catalog revision snapshot；
- stale-leaf filtering 与逐叶子重新授权；
- MCP 专用 `RouteDecision` reason 或 Agent 入口。
- 可由远端调用的 `files.search`、文件名搜索或内容搜索 MCP tool。

Catalog 和 Workflow 概念继续属于 SparkClaw 内部路由与执行架构；它们只从外部 MCP contract
中删除。

## 失败语义

对于 malformed schema、缺少 idempotency、Binding 无效、connector 已关闭、绝对/越界 locator
语法、URI scheme、NUL 字节或 request envelope 超限，adapter 必须在创建 message 或 run 前失败。

Operation 创建后：

- routing 或 Workflow setup 失败成为 typed operation failure；
- `detect_response_media` 把完整 lookup 的零结果冻结为 `clarify`，把完整非空 lookup 的文件名
  Top-1 冻结为 `selected`，把不完整/失败 lookup、不安全或不受支持的 filesystem object、重复项、
  对象变化和媒体 limit 违反冻结为 `blocked`；
- `answer` 对 `selected` 只发送已冻结资源，对 `clarify`/`blocked` 投影冻结 reason，且不执行
  第二次检索；
- owner approval 或 browser login wait 保持持久 waiting state；
- cancellation 是终态，但不承诺 rollback；
- result object 变化或消失成为 delivery failure；
- MCP 媒体编码不支持或 provider limit 失败会阻断完整结果；
- 使用相同 idempotency key 的重试观察同一持久 outcome。

用户可见错误必须有界，且不能泄露 filesystem 布局、route candidate、tool catalog 或
credential。Audit record 使用 typed reason code。

## Audit Contract

Audit 至少覆盖：

- Access Ticket 签发、兑换、过期和撤销；
- Binding 激活、重连、暂停和撤销；
- 业务 tool 列表与调用；
- workspace 多媒体校验结果，包括 item count、治理后的 content kind、total bytes 和 hash，
  但不记录本地路径或文件内容；
- 每次精确索引和 fallback search attempt，包括 query digest、stage、match count、traversal
  completion/truncation、ranker revision、选中 score 与 match reason、是否使用 tie-break 和 reason
  code；
- `detect_response_media` decision、当前 turn 资源 role 和冻结的选中资源 hash，不记录绝对路径；
- 通过现有 audit 记录普通 route selection 与 Workflow execution；
- operation 创建、重放、冲突、等待、取消和终态；
- 带 content kind 和 byte count 的结果 delivery；
- 每次拒绝及其稳定 reason code。

Audit vocabulary 应使用 `mcp.conversation.*` 与共享 message event，不再使用
`mcp.bound_leaf.*`。

## 验收标准

- 一个有效 Binding 只列出一个业务 tool `sparkclaw.conversation.send`；任何
  `sparkclaw.route.*` tool 都不能被列出或调用。
- 纯文本 MCP 输入创建一条普通消息，并在没有 server 合成路由叶子的情况下使用普通语义路由。
- 普通对话被选中后，`conversation.answer` revision 3 必须先执行 `detect_response_media`，再执行
  `answer`；第一个节点未冻结 typed decision 前，第二个节点不能运行。
- 纯媒体 MCP 输入根据 typed content 选择普通对话 Workflow，不运行模型路由，也不合成
  command text；随后通过两节点 plan 冻结并发布经过治理的媒体。
- 文本加媒体 MCP 输入与等价 Web 消息产生相同的规范化 owner-text 和 resource projection。
- Web 直接附件以已清理的 workspace 相对 `workspace_file`/artifact reference 进入 Workflow
  Runtime，绝不接受或持久化 input 绝对路径。
- 附件存在本身不能选中响应媒体：`发送它`选择经过治理的附件，`总结它`则把同一附件作为输入
  证据并路由到文档 Workflow。
- 关联 session workspace 下的有效相对路径成为经过治理、有序的 message part；路径穿越、
  绝对路径、symlink、目录、特殊文件、重复项和跨 workspace 文件全部原子失败。
- 解析顺序必须是直接附件/path、精确 basename 索引，只有精确未命中或 owner 提供不完整文件名
  时才运行一次有界 `files.search` fallback。精确 basename 重复时按稳定 workspace 相对路径
  字典序选择一个，不做更宽 search，也不要求 owner 选择。
- Fallback 只比较文件名，绝不读取文件内容或提取 preview。完整非空 fallback 只冻结确定性的
  Top-1 正匹配。零候选返回 `file_not_found`，不完整/截断 lookup 返回
  `file_lookup_incomplete`；两者都不能发送临时结果。
- 一个 locator 绝不扩展成多文件结果。显式多个附件或 locator 仍可产生 multipart 输出，每个
  locator 按输入顺序只对应一个经过治理的文件。
- 所有 lookup 只发生在 `detect_response_media`。其 `selected` outcome 冻结有序资源 ref 和 hash；
  `answer` 不能搜索、增加、删除、刷新或替换这些资源。
- `把年度报告发给我` 这类文本即使没有完整文件名也可以触发有界 fallback；owner 描述绝不能
  转换成虚构路径。
- 外部 client 不能设置 MIME type、byte count、hash、artifact ID、owner、session、workspace
  root、endpoint、route、operation、effect、Workflow、approval 或 model lane。
- `tools/list` 不暴露 `files.search`、resource browser 或其他文档 lookup tool。只有精确阶段无法
  绑定文件时，Workflow 才能在冻结的 workspace/query/budget 下内部调用 `files.search`。
- Image 和 audio 结果使用 MCP 原生 image/audio content；其他 file 以内嵌 resource content
  返回，不暴露本地路径，也不需要后续 read API。
- 二进制结果字节与治理对象 hash 一致，且不超过 provider 与编码后 transport envelope limit；
  对象发生变化时不能返回部分结果。
- LAN-direct、Bridge/Gateway、SecureEnvelope、Relay 与外部 Access Gateway 都有精确覆盖“最大
  可接受编码结果”和“第一个被拒绝大小”的边界测试；生产启用必须提供 live ISCP 证据。
- Access Ticket 与 Binding projection 不包含 Catalog revision 或 leaf grant，owner UI 不包含
  Catalog grant picker。
- 现有 idempotency、deadline、cancellation、重启恢复、connector gate、Binding revocation、
  endpoint isolation、Delivery 和 audit test 保持通过。
- memory、file 和 PostgreSQL 对修订后的 ticket、Binding、invocation 与 operation contract
  表现一致。
- 中英文文档、协议 schema、WebChat test/build、Gateway build/test/vet、Compose validation、
  doctor 和聚焦的 external MCP live E2E 全部通过。

## 与现有文档的关系

本设计替代[统一第三方 ISCP MCP 接入](unified-third-party-access-design.md)中的 route-leaf
projection、leaf-grant、bound Top-1 与 Catalog-coupled 业务部分。该文档仍是 ISCP pairing、
认证传输、MCP Access Ticket 兑换、connector 管理、operation
持久性、外部 requester provenance 与 LocalMind 旧链路删除的权威说明。

[消息与调度](messaging-and-scheduling.md)定义的普通 multipart 行为与 workspace 治理继续有效。
外部 MCP adapter 必须直接消费这些 contract，不得重述或分叉实现。
