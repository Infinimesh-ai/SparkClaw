# Issue #20 巨型文件拆分设计

> 语言：[English](../../docs/issue-20-god-file-split-design.md) | 简体中文

> 状态：已在本地 `main` 实现并完成本地验证，面向
> [issue #20](https://github.com/Infinimesh-ai/SparkClaw/issues/20)，基于
> `70e30f0` 基线。Issue 当前没有评论。2026-08-18，owner 确认采用完整
> CSS 拆分、两批交付和默认测试内 i18n lint。

## 决策摘要

Issue #20 已拆成五项保持行为不变、可独立审查的重构。React 公共导入路径、
渲染 markup、CSS cascade、翻译查询 API、Go 测试名称、ToolHub adapter
请求/结果契约以及 Python 错误投影都保持不变。

WebChat panel 将在现有 `components/panels` 导入路径后拆成聚焦的功能模块。
Binding polling 和重复的异步 busy guard 进入有测试的 hook，但不会合并当前彼此
独立的并发域。CSS 按区域拆成有序文件，所有响应式 override 继续位于最后。
英文成为翻译 shape 的编译期权威，中文必须满足完全相同的 shape；基于
TypeScript AST 的 lint 会拒绝生产代码中未使用的 key。

Go schema 测试文件按功能和文档格式做机械拆分。PPTX slide adapter 变成真正的
内嵌 Python package。Go 在每次调用的独立临时目录中落地可信 package，通过现有
受限 subprocess 路径执行，并在调用结束后删除。Python 标准库单元测试覆盖拆出的
纯逻辑，现有 Go 集成测试继续覆盖可执行 adapter 契约。

## 实施前基线

Issue 创建后、实施开始前，目标文件行数已经继续增长：

| 文件 | Issue 行数 | 当前行数 | 主要问题 |
|---|---:|---:|---|
| `apps/webchat/src/components/panels.tsx` | 1,447 | 1,524 | 15 个导出组件；settings 同时持有 polling 和多个 action 域 |
| `apps/webchat/src/styles/app.css` | 3,532 | 3,623 | 全局 cascade、无职责段落、响应式 override 集中在末尾 |
| `apps/webchat/src/i18n.ts` | 878 | 900 | 两个手工镜像 dictionary，相隔约 440 行 |
| `services/gateway/internal/toolhub/schema_test.go` | 2,248 | 2,278 | schema、图片、天气、DOCX、XLSX、PPTX、PDF 测试混在一个文件 |
| `services/gateway/internal/toolhub/scripts/pptx_slide.py` | 905 | 905 | layout、文本改写、slide clone、dispatch 和 CLI 错误投影混在一个模块 |

现有回归锚点包括 `panels.test.tsx`、`panels.polling.test.tsx`、
`externalMCPSettings.test.tsx`、完整 WebChat 测试，以及当前位于
`schema_test.go` 的大量 PPTX mutation 测试。生产 PPTX adapter 目前以内嵌单个
字符串的方式通过 `python3 -c` 执行，因此拆成 package 必须新增多文件执行路径，
不能只移动 Python 函数。

## 目标

- 让每个生产源码文件明显低于 800 行软限制。
- 明确组件、样式、翻译、测试和 Python 模块的职责。
- 保持所有当前公共与 runtime 行为。
- 保持当前 UI action 的并发与错误展示语义。
- 在编译期强制英文/中文翻译完全同构。
- 让未使用翻译 key 在常规 WebChat 验证中失败。
- 通过 CI 已执行的命令运行聚焦 Python 单元测试。
- 保持 PPTX subprocess timeout、stdin/stdout JSON、错误码、output-copy 和清理保证。
- 让每项子重构可独立审查和回滚。

## 非目标

- 重设计 inspector、settings UI 或响应式布局。
- 重命名 CSS class、组件 export、翻译 key、测试函数或 ToolHub operation。
- 在机械拆分过程中合并重复 CSS selector。
- 改变 connector polling 间隔、取消、重试或错误语义。
- 引入新的前端状态管理、CSS module、CSS-in-JS、Python 测试或打包依赖。
- 改变 PPTX layout heuristic 或增加 PPTX 功能。
- 改变 Gateway API、Store contract、Workflow 行为或用户可见文案。

## 不变量

1. `import { SettingsPanel, ... } from "./panels"` 继续有效。
2. 组件 prop 和导出符号名称不变。
3. 现有 class 名和最终 CSS rule 顺序不变。
4. `dictionaries[language]`、`Copy`、`Language`、`initialLanguage` 和
   `LANGUAGE_STORAGE_KEY` 继续是公共 i18n API。
5. 每个 busy 域只阻塞它今天阻塞的 action；彼此独立的域仍能并发。
6. Binding polling 在 1 秒后启动，pending 时 2 秒后继续，失败后 4 秒退避，
   parent callback identity 改变时不重启，并在 cleanup 时 abort 活跃请求。
7. PPTX adapter 继续从 stdin 读取一个 JSON object，并向 stdout 输出一个 JSON
   object。业务错误继续通过 JSON 返回，使 Go 保留
   `pptx_layout_fit_conflict` 类型化映射。
8. 现有 60 秒 fallback timeout 和 caller cancellation 继续拥有每个 Python
   subprocess。

## WebChat Panel 模块

`components/panels.tsx` 变成小型兼容 barrel，实现文件位于
`components/panels/`：

```text
components/
  panels.ts
  panels/
    approvals.tsx
    memory.tsx
    primitives.tsx
    settings.tsx
    status.tsx
    timeline.tsx
    trace.tsx
```

Barrel 重新导出相同的 15 个公共组件。`primitives.tsx` 只负责
`SectionHeader`、`JsonBlock` 和 `RiskPill`。`status.tsx` 组合 Status、
Artifact、Episode 和 Eval，因为它们目前作为同一 status stack 装配。
Workspace approval helper 留在 `approvals.tsx` 内部；connector status rendering
留在 `settings.tsx` 内部。

第一步只做精确移动，并由 TypeScript 调整 import。不会在移动中混入 JSX、文案、
class、排序或条件变化。`inspector.tsx` 和测试中的 consumer import 不变。

### `useBindingPolling`

`hooks/useBindingPolling.ts` 负责完整 timer/ref/abort 生命周期。输入契约是 pending
binding key、refresh callback、本地化 fallback error 和 error sink。它继续使用
`pendingBindingPollKey` 生成的稳定序列化 ID 集；最新 refresh callback 保存在 ref
中，因此正常 parent rerender 不会重启 long poll。

现有 `panels.polling.test.tsx` 保留 Settings 集成层的 rerender/abort 用例；
`useBindingPolling.test.tsx` 直接覆盖成功后继续、terminal completion 和 refresh
拒绝后的退避。Hook 不自行获取 connector 或 binding list，因此不会形成第二个
状态权威。

### `useAsyncAction`

`hooks/useAsyncAction.ts` 提取 `externalMCPSettings.run` 契约：

- 一个 hook instance 拥有一个 single-flight 域；
- `run(action, task)` 记录当前 action token、清理配置的本地错误、通过配置的错误
  mapper/sink 捕获异常，并在 mounted 时总是清除 busy state；
- 返回的 action token 同时支持 boolean disabled 状态和精确行 spinner；
- unmount 阻止延迟 state write，但取消仍由 task owner 负责。

Settings 为 owner save、policy save、client revoke、binding action 和 connector
toggle 保留独立 hook instance。这会替换八个重复 handler，但不会把当前五个域变成
一个全局锁。`ExternalMCPSettings` 也采用同一 hook。需要在其他位置展示错误的 caller
可以使用 no-op 本地 error sink；错误不会被静默重新分类。

聚焦 hook 测试覆盖成功、拒绝映射、重复调用、action token identity 和 unmount。
现有组件测试继续作为 rendered disabled/spinner 和 error state 的契约测试。

## CSS 拆分

`styles/app.css` 变成有序 import manifest。机械移动时可调整具体文件名，但职责为：

```text
styles/
  app.css
  foundation.css
  shell.css
  notifications.css
  schedules.css
  conversation.css
  composer.css
  inspector.css
  settings.css
  responsive.css
```

Rule 按原始连续顺序移动。`responsive.css` 始终是最后一个 import，并保持现有
`1280px`、`900px`、`480px` block 的内部顺序。跨区域 grouped selector 在本轮
留在其首个/base declaration 所属文件。不在拆分中合并 selector、改变 specificity、
重写 shorthand 或移动 media rule。

验证会比较前后 Vite CSS bundle 的 rule 顺序和大小，并对主聊天、inspector、
settings、approval、connector binding 和 External MCP 状态生成桌面/移动截图。
Rule 顺序比较是行为不变的主要证据；仅比较源码行数不足以证明这一点。

## 翻译模块与 Lint

公共 facade 保持在 `src/i18n.ts`，底层为：

```text
src/i18n.ts
src/i18n/en.ts
src/i18n/zh.ts
```

`en.ts` 导出具有普通 widened string value 的英文 object。`zh.ts` 使用
`satisfies typeof en` 声明对象，因此 key 缺失、多余或 shape 错误都会成为
TypeScript error，但不会要求中文 string 与英文 literal 相等。Facade 导出
`Copy = typeof en`、构造类型化 `dictionaries`，并保留语言持久化 helper。

`scripts/check-i18n-usage.mjs` 使用项目已经声明的 TypeScript compiler API 和
WebChat tsconfig。它把 property symbol 解析回英文 dictionary leaf declaration，
而不是搜索源码文本。扫描生产 `.ts`/`.tsx` 文件，排除翻译声明与测试文件。
对于 `text.risk[key]` 这类类型化 subtree computed access，会把该 subtree 标为有意
消费。每个未使用英文 leaf 都以稳定 dotted path 输出，并导致非零退出。

`npm run lint:i18n` 暴露该检查；默认 WebChat `test` 命令在 Vitest 前运行它，使
现有 CI 无法绕过。首次运行发现 12 个英文 leaf 及对应的未使用中文 leaf；这些
dead pair 已删除，没有改变任何生产代码实际消费的文案。

## Go 测试拆分

`schema_test.go` 通过完整移动测试函数和 helper 进行拆分，不修改 assertion：

```text
schema_test.go
image_schema_test.go
weather_schema_test.go
files_read_schema_test.go
document_docx_schema_test.go
document_pptx_schema_test.go
document_xlsx_schema_test.go
document_pdf_schema_test.go
document_schema_helpers_test.go
```

如果某个 helper 只有一个格式 caller，可以减少 helper 文件。共享 fixture helper
仍处于同一个 `toolhub` test package，并使用格式名称，而不是形成新的通用垃圾场。
不会只为了减少文件数而把现有 `document_*_test.go` 扩大到软限制以上。

测试名称、package 名、fixture、subprocess call 和 assertion 保持不变，仅允许
`gofmt` 调整 import grouping。前后 `go test -list . ./internal/toolhub` 的排序后
测试名称集合必须完全一致；Go 按新文件名发现 declaration 后，原始输出顺序会变化。

## 内嵌 PPTX Package

单文件脚本变成：

```text
scripts/pptx_slide/
  __init__.py
  __main__.py
  clone.py
  constants.py
  errors.py
  layout.py
  slides.py
  text.py
  text_edit.py
  update.py
  tests/
    test_layout.py
    test_slides.py
    test_text.py
```

`text.py` 负责 measurement 与 normalized text，`text_edit.py` 负责 run property
copy、weighted replacement、exact-span replacement 和 shape rewrite。`layout.py`
负责 collision、band/card grouping、coordinated layout 和 post-layout check。
`slides.py` 负责基础 index 与 add/delete helper；`clone.py` 负责 move/clone/ref
helper 和 relationship copy。`update.py` 负责单 slide mutation orchestration。
`__main__.py` 保留原 operation dispatch，并负责 dependency failure projection、
stdin decode、保存、错误映射和 stdout encode。

生产文件通过 `embed.FS` 内嵌；测试文件不进入二进制。`runPythonPackageAdapter`
创建 invocation-scoped 临时根目录，以严格文件权限只写入可信 package，通过
`runSubprocessAdapter` 从该目录执行 `python3 -m pptx_slide`，并在成功、adapter
error、process error 或取消时删除根目录。并发调用不共享可写 package directory。

标准库 `unittest` 直接测试纯函数和小型 fake shape/slide object。一个 Go 测试调用
unittest discovery，使完成文档工具 setup 后的 `go test ./internal/toolhub` 继续是
唯一 CI gate。现有端到端 PPTX 测试从 `schema_test.go` 移出但不改变；它们证明
package 落地、import、JSON framing、python-pptx 集成、文件输出和类型化错误。

## 交付顺序

1. 记录 WebChat test/build artifact 和 ToolHub test name/package 基线。
2. 只拆 panel module；验证 WebChat test/build 和 bundle size。
3. 提取并测试 `useBindingPolling`。
4. 提取并测试 `useAsyncAction`，保留每个 busy 域。
5. 机械拆分 CSS，并验证编译后 rule 顺序与桌面/移动渲染。
6. 拆 i18n data、增加 parity typing，再增加并 gate unused-key lint。
7. 机械拆分 `schema_test.go`，证明测试列表不变。
8. 增加 package runner、拆 `pptx_slide.py`、增加 Python 单元测试，并运行所有聚焦
   PPTX/ToolHub 测试。
9. 运行完整 WebChat 和 Gateway 验证，检查 diff/worktree，报告 bundle/test-list
   比较结果。

每个编号的行为不变 topic 单独提交。机械移动不会与 hook 行为或 execution runner
变化混在一起。

## 验证

建立 ToolHub 基线或运行最终测试前，先执行 `npm run setup:document-tools`。
必需证据：

```bash
npm --workspace @sparkclaw/webchat test
npm --workspace @sparkclaw/webchat run build

cd services/gateway
go test ./internal/toolhub
go test ./...
go vet ./...
go build ./...
```

另外必须完成：

- 前后 WebChat 生产 CSS bundle 大小与有序 rule 比较；
- 聚焦 hook timing/cancellation 测试；
- 前后排序后的 `go test -list . ./internal/toolhub` 名称集合精确比较；
- 通过 ToolHub Go test 执行 Python 单元测试；
- 默认 file backend 验证，因为它是产品默认；
- 受影响 WebChat 状态的桌面/移动截图；
- 双语文档 mirror/link 检查；
- 最终 `git status` 检查 document fixture、临时 Python package、截图或其他测试
  artifact。

无需运行 eval suite，因为 routing、Workflow、model、tool schema、Policy 和用户可见
行为都不改变。任何意外行为差异都会停止本轮重构，并作为单独 defect change 处理。

## 验收标准

- 任何触及的生产源码文件都不超过 800 行，除非新增书面理由。
- 所有现有 component export/import 和 i18n 公共 export 继续有效。
- 现有 WebChat 测试通过，新 hook/lint 有覆盖并进入 gate。
- 英中 parity 与生产环境零未使用翻译 leaf 由工具强制执行。
- 编译 CSS 有等价的有序 rule，受影响桌面/移动状态除不确定渲染噪声外无视觉差异。
- ToolHub 拆分前后暴露完全相同的 Go 测试名称集合。
- PPTX adapter 的成功/失败 JSON、输出文件、layout check、错误码、取消、timeout
  和 cleanup 不变。
- Python 单元测试和所有现有 Go PPTX 集成测试通过。
- 完整 Go build/test/vet 与 WebChat test/build 为绿色。
- 没有无关改动或生成的 runtime artifact。

## Owner 决策

2026-08-18，owner 选择推荐方案：

1. CSS 按有序区域拆成多文件，而不是只增加 banner。
2. 在 issue #20 内把 WebChat 与 ToolHub/Python 作为两批分别验证。
3. `lint:i18n` 进入默认 WebChat test gate。
