# OCR 深化使用与重构执行方案

> 语言： [English](../../docs/ocr-usage-refactor-plan.md) | 简体中文

状态：2026-08-04 完成规划，尚未实施。本方案依赖的 OvisOCR2 集成目前仅存在于
未提交的工作区改动中。

## 背景

项目已接入文档 OCR 模型（`ATH-MaaS/OvisOCR2`，由 vLLM 以 OpenAI 兼容接口提供，
别名 `sparkclaw-ocr`，端口 8007，compose 覆盖文件 `docker/compose.ocr.yaml`）。
适配器位于 `services/gateway/internal/documentocr/`，以 `adapters.documentOCR`
配置（默认关闭，`SPARKCLAW_OCR_*` 环境变量覆盖），在 toolhub/document 层
端到端接通三条链路：

1. 文档富化 —— `services/gateway/internal/toolhub/document_ocr.go` 中的
   `ovisDocumentOCREnricher`，在 `document_workflow.go` 中注册于 Fast
   图像语义富化器之前。
2. `images.inspect` 工具 —— 在 `services/gateway/internal/toolhub/images.go`
   中与 Fast 多模态模型并行执行 OCR。
3. 扫描版 PDF —— `toolhub/scripts/pdf.py` 对无文字层页面光栅化
   （pypdfium2 + Pillow），页级 OCR 成功后其 markdown 提升为页面块。

促成本方案的调研发现：

1. **OCR 初始化失败会让网关 panic。** `toolhub.go`（约 76 行）在
   `documentocr.New` 返回错误时直接 panic。一个*可选*适配器的配置错误
   不应拖垮整个网关；工程基线要求优雅降级（启动警告 + 调用期错误）。
2. **文档上下文重复输出图片文字。** 对同一张图，`document_pipeline.go`
   的 `smallDocumentContextSegments` 会同时产出 `ocr` 段（priority 90，
   完整 OCR markdown）和 `image_semantic` 段（priority 80，正文以
   `"Visible text: "` 再拼一遍文字）—— token 双倍浪费。
3. **`images.inspect` 无条件合并两路输出。** 期望策略：有文字的图 → OCR；
   无文字的图 → 仅视觉理解；图文混合 → 两者相加。
4. **Agent 层对 OCR 无认知。** 工具描述、能力目录、微信附件澄清话术
   均早于 OCR 接入；agent 不知道可以精确提取图中文字。
5. **两个不相关主题混在未提交工作区**（约 86 个文件）：OCR 集成与
   WebChat delivery 重构。

已确认的方向：

- 渠道图片（微信/Telegram）经 `images.inspect` **按需触发** OCR，不做
  ingest 自动 OCR。理由：OCR 单次超时上限 120 秒、服务并发配置为 2；
  图片是数据、随附文字才是指令，何时需要精读由 agent 决定。
- 重构 commit 落地前，先把现有未提交改动**按主题拆分为本地 commit**
  （不 push）。
- 本轮明确不做（记录为下方遗留项）：浏览器截图 OCR、WebChat 上传图
  OCR、ingest 自动 OCR、独立的 image-text-extraction workflow profile。

## Phase 0 —— 建基线并按主题提交现有改动

1. 动手前先建测试基线：
   - `npm run setup:document-tools`（不跑则 13 个 docx/xlsx/pptx 测试
     必挂，基线作废）
   - `cd services/gateway && go build ./... && go test ./... && go vet ./...`
   - `cd apps/webchat && npm run build`
   - 记录既有失败。
2. 全量核对 `git status`，剔除测试残留物（历史教训：一次 weixin 合并
   曾把观测 dump JSON 带进 main）。
3. 按主题本地提交，一题一 commit，动机写入 message（不 push）：
   - **WebChat delivery 重构** —— `App.tsx`、`composer.tsx`、
     `messages.tsx`、删除的 `delivery.tsx` / `deliveryDraft` /
     `useExternalDelivery`、`useDeliveryTarget`、`i18n.ts`、`app.css`、
     相关 `client.ts` / `messageStream` 改动。与 OCR 无关，先摘出。
   - **OCR 适配器 + 配置 + 部署** —— 新包 `internal/documentocr/`、
     `config.go` 的 `DocumentOCRAdapterConfig` / 归一化 / 环境变量、
     `configs/sparkclaw.default.json`、`configs/model.profiles.json`、
     `docker/compose.ocr.yaml`、`docker/compose.yaml`、`docker/env/*`、
     `scripts/doctor.sh`、`serve_models_compose.sh`、
     `setup-document-tools.sh`、`docs/model-loading.md`、
     `docs/deployment.md` 及 zh-cn 镜像。
   - **toolhub 接线 + 文档富化** —— `toolhub.go`、`document_ocr.go`、
     `document_workflow.go`、`document_pipeline.go`、
     `document/enrichment.go`、`docs/document-workflows.md` 及镜像。
   - **`images.inspect` OCR 合并** —— `images.go` 及测试、`registry.go`
     的描述部分。
   - **扫描版 PDF 路径** —— `toolhub/scripts/pdf.py`、
     `tools/document-runtime/requirements.txt`、相关统计/超时改动。
   - 其余文件按实际归属拆分（gateway ingress、message_control 及
     agent 层 diff 属于并行的 publish/delivery 主题）。
4. 每个 commit 前跑 gateway 全套；文档改动确认 zh-cn 镜像与双向语言
   链接。

## Phase 1 —— panic 改优雅降级 + 配置加载期校验

- `services/gateway/internal/config/config.go`
  （`normalizeDocumentOCRConfig`，约 651 行）：补齐构造期校验
  （provider 合法、baseUrl 可解析、host 白名单），enabled 但配置无效时
  **Load 即报错** —— 可扩展既有的
  `TestLoadRejectsUnsafeDocumentOCREndpoint`。
- `services/gateway/internal/toolhub/toolhub.go`（约 74–77 行）：去掉
  panic；`documentocr.New` 失败时记启动警告并落到 `disabledAdapter`
  （`documentocr/types.go` 已有）。参考同文件约 57–73 行 weatherInfo
  的处理模式。
- 测试：config 层"enabled + 非法配置 → Load 报错"；toolhub 层
  "构造失败 → New 不 panic 且 `images.inspect` 返回
  `ocr_status=disabled`"。
- 单独一个 commit（功能性修复）。

## Phase 2 —— `images.inspect` 智能合并（有字 OCR / 无字视觉 / 混合相加）

`images.go`（约 94–149 行）已并行执行 `h.ocr.Parse` 与 Fast
`ChatWithImage`。**OCR 本身就是文字检测器** —— 无文字图片经 OvisOCR2
产出为空/琐碎 markdown —— 因此不加前置检测调用、不增加延迟，只改
合并逻辑：

- OCR 成功且 markdown 非空 → 输出 `ocr_markdown` 加视觉描述（混合
  场景：两者相加；视觉部分侧重语义/布局而非重复文字）。
- OCR 成功但 markdown 为空/纯空白 → 判定无文字，省略全部 `ocr_*`
  字段噪音，仅视觉理解。
- OCR 关闭/失败 → 现有降级不变（仅视觉 + `ocr_status` /
  `ocr_warning`）。
- 新增输出字段（如 `text_detected: bool`），让 agent 明确看到判定
  结果。
- 新增"markdown 是否为琐碎空产出"的判断 helper（trim 后为空，或仅剩
  OCR 清洗残留），与 `cleanOvisOCR2Output` 同置于 `documentocr` 包或
  `images.go`。
- 测试：三个分支各一用例（httptest 模拟 OCR 返回空 / 非空 / 错误）。

## Phase 3 —— 文档富化去重

- `document_pipeline.go`（`smallDocumentContextSegments`，约 194 行）：
  当 image record 的 `ocr.status == "succeeded"` 且 markdown 非空时，
  `image_semantic` 段**跳过 `"Visible text:"` 拼接**，只保留描述与
  图文关系。OCR 关闭或失败时 `ocr_text` 仍是兜底 —— 不动 Fast
  富化器的 prompt/输出结构（单点裁决，保住降级路径）。
- 测试：(a) OCR 成功 → 有 `ocr` 段且 semantic 段不含 Visible text；
  (b) OCR 关闭/失败 → semantic 段保留 Visible text。
- commit message 标注行为变化（去重、省 token）。

## Phase 4 —— Agent/渠道层认知升级

- `services/gateway/internal/toolhub/registry.go`（`images.inspect`
  注册，约 258 行）：工具描述写明"启用 OCR 时精确提取图中文字
  （markdown），自动区分有字 / 无字 / 混合"。`registry_test.go`
  一致性测试自动覆盖。
- `services/gateway/internal/capability/catalog.go`：若改动
  DocumentRead 能力描述文案，同步 bump 对应 leafRevision 与
  `DefaultCatalogRevision`（当前 2026-08-04.v14）。
- `services/gateway/internal/weixin/chat.go`
  （`attachmentClarificationPrompt`，约 391 行）：图片类附件的澄清
  话术加入"可直接读出图中文字"提示（纯文案，不新增流程）。
- 文档：相应更新 `docs/workflow-capabilities.md`、
  `docs/intent-routing.md`、`docs/document-workflows.md` 及 zh-cn 镜像。

## 遗留项（本轮刻意不做）

1. 微信纯图消息 ingest 自动 OCR（需配置开关 + 短超时预算 + 异步
   worker）—— 观察按需路径的实际体验后再定。
2. 独立的 image-text-extraction 能力/profile —— 仅当"提取图中文字"
   类请求出现确凿误路由时。
3. 浏览器截图 OCR（做成 `inspect` 的显式参数）—— 仅当 canvas 型页面
   出现真实理解失败案例时。
4. OCR 质量测量 —— `docs/model-loading.md` 自述更广泛的质量测量
   仍待补齐。

## 验证（每个 Phase 收口执行）

1. `cd services/gateway && go build ./... && go test ./... && go vet ./...`
   对照 Phase 0 基线 —— 零新增失败。
2. 判定文档工具测试前，确认 `npm run setup:document-tools` 已执行。
3. 涉及前端的 Phase：`cd apps/webchat && npm run build`。
4. 所有改动的 `.md` 均有 zh-cn 镜像 + 双向链接（docs CI 要求）。
5. 用 DEFAULT 配置（file 状态后端）验证；OCR 保持默认关闭；启用态用
   `docker/env/sparkclaw.ocr.env` + `scripts/doctor.sh` 端点探活验证。
6. 收口时刷新 sparkclaw-sop skill 的 dated status line，并 append
   新教训。
