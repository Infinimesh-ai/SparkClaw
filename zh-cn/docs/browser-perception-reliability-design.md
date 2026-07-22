# 浏览器感知可靠性优化设计

> 语言：[English](../../docs/browser-perception-reliability-design.md) | 简体中文
>
> 状态：待实现设计。本文档只优化现有 `browser.interaction` revision 1
> 的内部实现，不新增能力叶、Workflow、工具、浏览器 Provider 或 Policy 路径。

## 设计结论

在当前 Node driver 内部，为 SparkClaw 现有 Playwright 快照实现增加一个
有预算、多来源的页面感知流水线。增强后的流水线先合并 DOM 语义、可访问性
证据、布局状态、frame 上下文和通用可点击信号，再输出同一个有界的
`browser_interaction_snapshot_v1` 投影。

保持所有已经投产的架构边界：

- Fast 仍然只能选择已注册的 `browser.interaction` 能力叶。
- 现有 `browser.interaction` revision 1 Profile 继续负责阶段顺序。
- ToolHub 继续作为模型可见性、风险、ref 绑定和验证的权威边界。
- Go 仍然只管理一个有超时、可取消的 Node driver 进程。
- Node driver 仍然只管理一个 Playwright 持久化 Chromium Context。
- `browser.snapshot`、`browser.click` 和 `browser.verify` 的公开名称与必填参数不变。
- 点击仍然要求当前页面、当前快照、精确元素 ref、匹配的 fingerprint 和
  Playwright actionability。
- 每次成功点击仍然使来源快照失效；下一次点击前仍然必须重新快照并验证。

上游 `browser-use` 只是实现参考，不成为新的运行时依赖，也不替代 SparkClaw
的 Agent 架构。

## 目标

1. 识别由 SPA 自定义组件实现的控件，而不只识别原生元素或
   `cursor: pointer` 节点。
2. 在有界范围内观察同源/跨域 frame 和开放 Shadow DOM 中的控件。
3. 减少隐藏、禁用、禁止指针事件或被遮挡元素造成的错误候选。
4. 在正常 SPA 重渲染后保留语义连续性，但绝不让旧快照 ref 授权新元素。
5. 复用同一份结构化页面证据，提升通用登录态识别。
6. 为 `browser.verify` 提供稳定、可观察的状态差异，避免把任意瞬态 DOM
   文本变化当作有效进展。
7. 保持确定性、有界延迟、显式降级和现有的不可信证据边界。

## 非目标

本设计不会：

- 用通用浏览器 Agent 替换能力树；
- 新增浏览器 Provider、MCP 进程、远程调试端点或 `connectOverCDP` 路径；
- 向模型暴露 CDP、CSS Selector、XPath、JavaScript、backend node ID 或坐标；
- 把输入、选择、表单提交、上传、下载、截图、代登录、凭据输入、验证码、
  支付或账户修改加入 `browser.interaction` revision 1；
- 增加网站、邮箱产品或域名专用的生产规则；
- 把坐标点击或视觉点击作为后备；
- 在一个工具调用内批量执行多个点击；
- 把 LLM Judge 作为完成状态的权威；
- 静默修复或复用过期 ref；
- 在首个实现切片改变当前 24 个控件的模型投影上限或加入候选分页。
  分页继续作为浏览器完善路线图中的独立事项。

## 当前实现与失效模式

当前 `services/gateway/internal/browserautomation/scripts/playwright_driver.cjs`
中的快照实现会：

1. 在选中页面的主文档 `body` 中查找原生控件和固定 ARIA role。
2. 补充 computed cursor 为 `pointer` 的可见元素，并优先保留同名最深节点。
3. 按 DOM 顺序写入 `data-sparkclaw-ref`。
4. 从 label、`aria-label`、文本、placeholder、title 或 name 构建可访问名称。
5. 按冻结目标进行确定性词法评分，返回前 24 个控件。
6. 点击前通过注入属性解析元素，重新计算语义 fingerprint，再交给
   `locator.click()` 执行 actionability 检查。

这是一条合理的有界基线，但仍有可复用的缺口：

- 没有已识别 role、也没有 pointer cursor 的 JavaScript listener 控件可能漏召回。
- 主文档查询不能完整覆盖 frame 和 Shadow DOM。
- 几何和 CSS 可见不等于可以命中，也不能证明控件没有被上层元素覆盖。
- 框架替换节点后，注入属性会消失，即使新节点的语义完全相同。
- 当前 digest 包含易变页面证据，时钟、计数器、轮播或无关 live region 可能
  被误判为有效进展。
- 登录态识别和交互候选识别各自扫描 DOM，可能对同一个可见应用界面得出
  不一致结论。
- 词法评分只能改善排序，无法找回采集阶段根本没有发现的控件。

因此首要问题是候选召回和证据质量，而不是缺少学习型 reranker。

## 保持不变的运行形态

```text
browser.interaction r1
  -> 现有 Workflow 阶段门禁
  -> 现有 browser.snapshot 注册
  -> PlaywrightAdapter.Call
  -> 同一套换行分隔 JSON driver 协议
  -> 同一个 Node driver 和持久化 Chromium Context
       -> 增强的有界感知采集器
       -> 同一份快照状态/ref 表
  -> 同一个 browserautomation.Result
  -> 现有 ToolOutcome adapter
  -> 现有 Workflow assessment
  -> 通过 Playwright Locator 执行 browser.click
  -> 点击后 browser.snapshot
  -> 现有 browser.verify 边界
```

允许 Node driver 通过 Playwright 创建页面级 CDP Session，但它只能作为本地
观察实现细节。它不得开放远程调试端口、公开 CDP 路由、第二条浏览器连接或
模型可见工具。

## 增强感知流水线

### 1. 有界 Frame 清单

从选中的 Playwright `Page` 出发，以稳定的父节点优先顺序枚举 frame 树。
内部记录不透明 frame 引用、深度、URL origin、视口交集和宿主 frame 元素边界。

初始硬限制：

| 预算 | 初始上限 | 超限行为 |
|---|---:|---|
| Frame 文档 | 20 | 优先处理可见浅层 frame，并将快照标记为 truncated。 |
| Frame 深度 | 4 | 跳过更深后代，并将快照标记为 truncated。 |
| 检查的 DOM 节点 | 20,000 | 确定性停止采集，并标记降级。 |
| 内部候选 | 1,000 | 停止加入候选，保留已采集顺序并标记 truncated。 |
| 模型可见控件 | 24 | 保持当前契约，并报告省略数量。 |
| 每个控件的附近文本 | 320 字符 | 保持当前限制。 |

不可见或很小的第三方 frame 不应排在大型可见应用 frame 之前。Frame 顺序
不得依赖网络请求完成顺序。

### 2. 多来源采集

从现有 Playwright Session 中采集有界证据：

- DOM 结构，包括开放 Shadow DOM；
- Chromium 能提供的 accessibility role、name、ignored、focusable、editable
  和交互状态；
- 布局边界、computed `display`、`visibility`、`opacity`、`pointer-events`
  和 cursor；
- 原生元素语义和 ARIA 属性；
- click、mouse、pointer 或键盘激活 listener 的存在性，但绝不采集源代码；
- frame 归属和视口位置；
- checked、selected、expanded、pressed、disabled 等布尔状态。

采集器可以通过 Playwright CDP Session 使用 Chromium 本地 `DOMSnapshot`、
DOM、Accessibility 和 DOMDebugger Domain。这些调用应在一个子超时内并行执行。
某个增强来源失败时，不得让健康的 Playwright Session 崩溃或重置。

不得根据模型文本生成页面脚本。所有 evaluate 片段都必须是仓库内固定的采集代码。

### 3. 内部节点合并

按内部 backend-node identity 与 frame identity 合并各来源，生成一个内部描述符：

- frame 引用和深度；
- 仅保留在 driver 内部的 backend node identity；
- tag、type、语义 role、accessible name 及 role/name 来源；
- 容器 landmark 和有界附近文本；
- 布局边界和视口关系；
- enabled 与交互状态；
- 通用可点击信号；
- 命中测试和遮挡状态；
- 稳定 DOM 顺序；
- 当前 fingerprint 和跨快照 continuity key；
- 由当前快照状态持有的精确 Playwright resolver。

缺少的证据必须保持缺失。采集器不得根据域名或产品名发明 accessible name、
role、登录态或 click listener。

### 4. 通用候选资格

节点至少具备一个正向信号时才成为交互候选：

- 原生交互元素；
- 交互型 ARIA 或 accessibility role；
- focusable、editable 或 settable accessibility 状态；
- 显式 click、mouse、pointer 或键盘激活 listener；
- 包裹 input 等表单控件的可用 label；
- 正向 `tabindex` 或 content-editable 状态；
- pointer cursor，且只作为最低置信度后备。

以下条件会排除节点或降低其优先级：

- `display: none`、隐藏 visibility、零 opacity、`aria-hidden=true` 或没有
  可用布局边界；
- 原生 disabled 或 ARIA disabled；
- `pointer-events: none` 且没有可操作后代；
- 被更高 paint order 的不透明元素完全覆盖；
- 已存在具有相同名称、role、边界和动作面的交互祖先或后代；
- 没有其他交互信号的装饰性 SVG 或图标内容。

本设计明确不采用基于产品词、搜索 class 名或猜测图标尺寸的上游启发式。
候选识别必须保持领域无关。

### 5. 可见性与命中测试

快照可见性和点击时 actionability 是两个不同保证：

- 快照采集使用 computed style、边界、父 frame 可见性、paint order 和有界
  hit testing，降低或排除明显不可用控件。
- 点击前，driver 仍然必须精确解析唯一元素、检查当前 fingerprint，并依赖
  Playwright actionability 和自动等待。

部分可见的元素只要至少一个测试点可命中，仍可保留。遮挡状态不确定的元素
只有在存在其他强语义信号时才可以带 `hit_testable=false` 返回；它必须排在
可操作候选之后，也不能绕过点击时 actionability。

## 向后兼容的快照契约

保持 `schema_version=browser_interaction_snapshot_v1` 和 ToolHub、Workflow
已经消费的所有必填字段，只新增可选证据字段：

```json
{
  "schema_version": "browser_interaction_snapshot_v1",
  "snapshot_id": "snapshot_1_8",
  "page_id": "page_1",
  "digest": "legacy-compatible-digest",
  "verification_digest": "stable-semantic-state-digest",
  "perception": {
    "version": "hybrid_dom_ax_v1",
    "status": "complete",
    "degradation_reasons": [],
    "frames_seen": 3,
    "frames_processed": 3,
    "nodes_inspected": 8120,
    "candidates_total": 42
  },
  "controls": [
    {
      "ref": "snapshot_1_8:e7:0123456789abcdef",
      "short_ref": "e7",
      "role": "menuitem",
      "accessible_name": "Drafts",
      "frame_ref": "f0",
      "click_signals": ["ax_role", "js_listener"],
      "hit_testable": true,
      "occluded": false,
      "continuity_key": "bounded-semantic-value",
      "fingerprint": "bounded-current-snapshot-value"
    }
  ]
}
```

规则：

- `fingerprint` 继续作为当前快照的点击绑定值。
- `continuity_key` 只能用于比较，绝不能授权点击。
- `frame_ref` 是当前快照内的不透明引用，不是 CDP target ID 或 origin 凭据。
- `click_signals` 只能使用小型注册词表，不能包含任意页面字符串。
- `perception.status` 只能是 `complete` 或 `degraded`，降级必须显式且有界。
- 任何 frame、节点、候选或模型投影预算导致覆盖不完整时，`truncated=true`。
- 现有消费者可以忽略所有新增字段。

## 确定性候选排序

排序保持确定性并在本地执行。浏览器 adapter 不调用 embedding、reranker、
Fast 或 Deep 模型通道。

只对合格候选按以下优先级排序：

1. 规范化 accessible name 被冻结目标包含，或目标被 name 包含；
2. accessible name 的短语和 token 重叠；使用 Unicode 规范化，并为没有空格
   分词的语言提供有界字符后备；
3. 容器或附近文本匹配；
4. 原生语义、accessibility role 或事件 listener 等强交互信号；
5. 可命中并处于视口内；
6. role 适配度只作为平分项；
7. 稳定 frame 顺序和 DOM 顺序。

disabled、遮挡不确定、无名称、过于通用或只有 pointer 信号的候选会被降权。
多样性选择应避免同一容器内大量同名节点占满 24 个返回位置，而其他相关容器
完全没有代表项。

排序只影响当前快照中哪些候选展示给模型。它不能让不合格节点变得可点击，
不能绕过 ToolHub 风险检查，也不能解析过期 ref。

## Ref 有效性与重渲染连续性

现有严格 ref 元组继续作为权威：

```text
(managed_profile_id, page_id, snapshot_id, element_ref, fingerprint)
```

点击时只要注入属性、frame、backend node、resolver 或 fingerprint 缺失或变化，
就返回 `snapshot_stale`。不得在当前 DOM 中搜索语义替代项并用旧 ref 点击。

稳定 identity 只能在新快照已经生成后使用：

- 关联点击前后控件，用于验证；
- 识别选中的菜单项变为 active 或消失；
- 在 stale-ref 转换后的新快照中提高语义等价目标的排序；
- 从等价页面状态中识别重复语义动作。

continuity key 应由规范化 role、accessible name、稳定属性、容器路径、frame
路径、目标 URL 类别和重复 ordinal 构成。动态 class、focus/hover/loading 状态、
快照生成属性、屏幕坐标和易变文本必须排除。

有歧义的 continuity 匹配必须保持歧义，不得自动选择点击目标。

## 复用登录态识别证据

把单独的主文档宽泛扫描替换为对增强感知结果执行的纯分类器。保持
《浏览器登录态操作文档》中已有的 `unknown`、`challenged`、`authenticated`
状态和证据优先级。

增强点：

- 检查已处理 frame 和开放 Shadow DOM 中的可见控件；
- 区分主应用界面与小型嵌入式组件；
- 在内部记录来源 frame 和证据类型；
- 主 frame 或大型活动 modal/frame 中的可见 challenge，比无关导航中的登录
  文本更强；
- 只有 sign-out、identity、account 和可用 application shell 证据可见且结构
  上有上下文时，才作为正向证据；
- 强信号冲突时仍然返回 `unknown`；
- 永不使用域名、邮箱名、cookie 名或隐藏文本作为规则。

登录态仍然只是证据，不是权限。它不会扩大 Workflow，也不会允许输入凭据。

## 稳定验证证据

保留现有 `browser.verify` 工具和有序的点击前-点击-点击后绑定。在归档快照中
新增规范化 `verification_digest` 和结构化状态差异。

verification digest 包含：

- 规范化 URL 和 title；
- 已处理 frame 拓扑；
- 控件 continuity key 和相关布尔状态；
- 可见 dialog、menu、tab、expanded region 结构；
- 存在时的稳定目标相关文本证据。

verification digest 排除：

- 与目标无关的时间戳、计数器、动画状态、轮播和 live-region 噪声；
- 动态 CSS class 和生成 ref；
- 绝对坐标，除非布局移动就是预期效果；
- 隐藏 script、style、template 或 metadata 文本。

当点击前后快照都提供 `verification_digest` 时，ToolHub 应优先使用它；旧快照
或降级快照继续回退到现有 `digest`。状态变化本身仍不足以证明成功。绑定的
选中元素、expected effect、规范化差异、风险规则、点击次数和模型 verdict
仍然通过现有 ToolHub 确定性检查。

有用的结构化差异包括：

- URL 或 title 变化；
- 选中控件消失，或 checked/selected/expanded/pressed 状态变化；
- 出现目标相关控件或区域；
- dialog、menu 或 tab-panel 状态变化；
- 出现明确认证 challenge；
- 没有稳定的可观察变化。

## 降级与错误语义

增强采集器是增量能力。CDP accessibility、snapshot、listener 或 frame 证据
不可用时，应回退到当前 DOM 采集器，并在 `perception` 中报告原因。

降级规则：

- 不能因为单个观察来源失败而重置健康的浏览器 Session。
- Frame、节点、候选或时间预算超限后，不能报告完整覆盖。
- 旧采集器仍有有效候选时，不能把增强失败转换成空的成功快照。
- 降级证据不能削弱点击时 fingerprint 和 actionability 检查。
- 增强与旧采集都失败，或 driver 无法生成有界结果时，必须明确返回快照失败。

采集器使用现有 adapter 请求 deadline 内的子超时，不为点击新增重试，也不新增
长期 worker。

## 现有模块职责

| 现有模块 | 优化后的职责 |
|---|---|
| `browserautomation/scripts/playwright_driver.cjs` | Frame 清单、多来源采集、候选合并/排序、快照内 resolver 状态、点击时校验和页面登录态证据。 |
| `browserautomation/playwright_stdio.go` | 保持进程所有权、嵌入 driver 启动、deadline、协议 framing 和清理不变。 |
| `browserautomation/adapter.go` | 保持公开工具映射和 Provider 无关结果 envelope 不变，不加入网站规则。 |
| `toolhub/browser_interaction.go` | 现有 unsafe-target guard、当前 run ref 绑定、有序快照/点击验证，以及优先使用稳定 digest。 |
| `agent/workflow_outcome.go` | 把已注册的新增快照属性增量投影为 typed refs。 |
| `agent/browser_interaction_workflow.go` | 保持现有阶段、三次点击上限、循环处理和完成边界，不新增阶段拓扑。 |
| Artifact 与 trace 路径 | 在现有脱敏和 owner scope 下归档完整有界观察，只向模型暴露投影。 |

如果 driver 即将超过工程基线的文件软上限，应以纯机械方式拆分采集 helper，
但仍然嵌入并运行在同一个 Node 进程内。这只是代码组织调整，不是服务或运行时边界。

## 安全与隐私

- 所有 DOM、accessibility、frame、listener 和布局内容都是不可信外部证据。
- 绝不采集密码值、隐藏 input 值、cookie、storage、Authorization header、
  listener 源代码或页面 JavaScript body。
- 文本输入值不进入模型可见控件；验证所需的布尔状态可以保留。
- Password 和验证码字段只能以存在性、role、可见性和结构上下文参与登录态识别。
- 原始 backend node 和 frame target ID 只保留在 driver 内部。
- 跨域内容与主页面使用相同预算和不可信证据处理。
- 页面内容不能改变启动参数、允许域、Workflow scope、Policy、approval 或工具参数。
- Unsafe label 仍然在进入 adapter 前由 ToolHub 拒绝。

## 实施顺序

### 阶段 0：基线与 Fixture

1. 在确定性 fixture 上记录当前快照延迟、候选数量、target-unavailable 比例、
   stale-ref 比例和登录态识别结果。
2. 先提取当前采集、命名、fingerprint 和排序的纯 helper，不改变输出。
3. 行为改动前保持真实 Chromium smoke test 通过。

### 阶段 1：候选召回与 Actionability

1. 增加有界 frame 和开放 Shadow DOM 清单。
2. 增加 accessibility 与 listener 交互信号。
3. 增加 pointer-events、父 frame 可见性、遮挡和 hit-test 证据。
4. 保持当前 schema 和 24 控件投影，只新增可选 perception 字段。
5. 增强来源失败时明确回退旧采集器。

### 阶段 2：连续性、排序与登录态

1. 增加不能授权点击的 continuity key。
2. 用本文档的确定性优先级和重复项多样性替代零散数值排序。
3. 在现有页面登录态分类器中复用结构化感知证据。
4. 保持冲突处理和登录 handoff 不变。

### 阶段 3：验证强化

1. 增加 `verification_digest` 和结构化状态差异。
2. ToolHub 优先稳定 digest，并保留 legacy fallback。
3. 证明无关动态页面变化不算进展，而 selected/expanded/navigation 变化算进展。
4. 保持现有点击计数、循环和完成语义。

候选分页、截图、坐标动作、输入、选择和下载必须单独评审，不进入这些阶段。

## 验证计划

### 采集器 Fixture

- 原生 button、link、input、select 和 ARIA 控件；
- 没有 pointer cursor 的 listener-only `div`；
- 嵌套 wrapper/leaf 控件且不产生重复动作面；
- 开放 Shadow DOM 控件；
- 同源和跨域 frame 控件；
- 不可见、disabled、`pointer-events:none` 和被 overlay 遮挡的控件；
- 不同 landmark 或 dialog 中的同名控件；
- 中文和英文交互目标；
- 超过 24 个相关/无关候选时的确定性排序；
- Frame、节点、候选和时间预算降级。

### Ref 与验证 Fixture

- 未变化节点接受当前 ref；
- 重渲染节点使用旧 ref 返回 `snapshot_stale`；
- 新快照可以关联替代节点，但必须生成新 ref；
- continuity 有歧义时不选择元素；
- selected、checked、expanded、dialog、URL 和 title 变化产生稳定差异；
- 单独的时钟/live-region 变化不产生进展；
- 重复稳定状态仍返回 `interaction_loop_detected`；
- 第三次未完成点击仍返回 `interaction_attempt_limit`。

### 登录态 Fixture

- 带 identity/account 控件的已认证 application shell；
- 已认证 SPA 中隐藏或无关的登录文本；
- 没有登录 form 的可见 folder-unlock/account-settings password input；
- 真正可见的登录 form 和明确登录动作；
- 可见 frame 或开放 Shadow DOM 内的 challenge/account 控件；
- 小型无关嵌入式 sign-in widget；
- challenge 与 authenticated 强信号冲突时返回 `unknown`。

### 必跑命令

```bash
go test ./services/gateway/internal/browserautomation -count=1
go test ./services/gateway/internal/toolhub -count=1
go test ./services/gateway/internal/agent -count=1
SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS=1 \
  go test ./services/gateway/internal/browserautomation \
  -run TestRealChromiumSnapshotAndLocatorInteractions -count=1
go test ./services/gateway/...
git diff --check
```

真实浏览器 fixture 必须覆盖 listener-only 控件、开放 Shadow DOM、frame、overlay、
重渲染后的 stale ref 和现有通用 authenticated-application 场景。真实 QQ 邮箱
可作为额外人工证据，但任何需要凭据的第三方网站都不能成为验收依赖。

## 可观测性

只向归档快照和现有浏览器工具 observation 增加有界诊断字段，不新建遥测系统：

- perception 版本与状态；
- DOM、accessibility、layout 和 merge 各阶段采集耗时；
- 看到、处理和按原因跳过的 frame 数；
- 检查的节点数；
- 按正向信号分类的候选数；
- 因可见性、禁用、去重或遮挡排除的候选数；
- 模型可见和省略的候选数；
- legacy fallback 原因；
- 点击解析结果：exact、stale、ambiguous 或 actionability failure；
- 登录态、confidence 和注册证据信号。

不得记录原始页面文本、表单值、可能包含 secret 的完整 URL、cookie、storage
或 listener body。

## 验收标准

1. Capability Catalog revision、注册能力叶集合、Workflow ID/revision、固定工具
   scope、公开工具名、风险级别和 approval 行为全部不变。
2. 同一个 Go adapter 与 Node driver 进程管理浏览器；不引入 browser-use、MCP、
   远程 CDP、云浏览器或第二进程依赖。
3. 新增可选字段存在或不存在时，现有快照消费者都能继续工作。
4. Listener-only、frame 和开放 Shadow DOM 控件能在确定性 fixture 中被发现。
5. 被覆盖、隐藏、禁用和禁止 pointer 的控件不能绕过 Playwright 点击时 actionability。
6. 动作或重渲染后旧 ref 保持无效；continuity metadata 永不授权点击。
7. 登录态判断保持领域无关，并在冲突时返回 `unknown`。
8. 稳定验证忽略无关瞬态变化，同时识别有意义的控件、区域和导航变化。
9. 所有快照工作都在现有请求 timeout 内完成，预算明确，覆盖不完整时绝不报告完整。
10. 聚焦测试、真实 Chromium smoke test、Gateway 全量测试和文档镜像检查全部通过。

## 上游参考

以下 `browser-use` 机制为感知设计提供参考，但不会作为 Agent Runtime 被复制：

- [DOM、Accessibility、Snapshot 与 device ratio 采集](https://github.com/browser-use/browser-use/blob/2be09b6c5eb702a9287684b42b27e7042a1aba29/browser_use/dom/service.py#L560-L668)
- [布局、可点击性、pointer-events、边界与 paint order 提取](https://github.com/browser-use/browser-use/blob/2be09b6c5eb702a9287684b42b27e7042a1aba29/browser_use/dom/enhanced_snapshot.py#L46-L176)
- [通用交互元素信号组合](https://github.com/browser-use/browser-use/blob/2be09b6c5eb702a9287684b42b27e7042a1aba29/browser_use/dom/serializer/clickable_elements.py#L39-L244)
- [稳定 hash 与多层历史匹配](https://github.com/browser-use/browser-use/blob/2be09b6c5eb702a9287684b42b27e7042a1aba29/browser_use/agent/service.py#L3529-L3668)
- [多动作执行的页面变化防护](https://github.com/browser-use/browser-use/blob/2be09b6c5eb702a9287684b42b27e7042a1aba29/browser_use/agent/service.py#L2719-L2818)

SparkClaw 只采用观察层经验，继续保留更严格的 ref 过期、单点击阶段、确定性验证、
本地进程所有权和注册驱动能力边界。
