# SparkClaw 文档索引

> 语言： [English](../../docs/index.md) | 简体中文

本索引列出当前有效的文档集合。文档描述现行 runtime 和受支持的扩展边界；
已经完成的迁移计划和被替代的实现方案只保留在 Git 历史中，不再混入当前文档树。

## 从这里开始

| 文档 | 用途 | 权威范围 |
|---|---|---|
| [README](../README.md) | 项目概览、快速启动和当前状态 | 项目入口 |
| [架构](architecture.md) | 产品边界、runtime 拓扑、职责和不变量 | 系统事实来源 |
| [部署](deployment.md) | 本地、Compose、DGX Spark、状态、备份和排障 | 运维手册 |
| [开发](development.md) | 仓库结构、实现规则、验证和扩展流程 | 贡献者手册 |
| [Workflow 能力矩阵](workflow-capabilities.md) | 当前 Workflow runtime 确切可执行的能力 | 用户可见能力清单 |

## Runtime 手册

| 文档 | 范围 |
|---|---|
| [Workflow 执行](workflow-execution.md) | Workflow 原生执行流水线、步骤循环及其协议、预算、恢复语义和扩展点 |
| [Workflow 证据所有权与复用](workflow-evidence-ownership.md) | 正在迁移的 Runtime/模型职责、单次采集多消费者复用、类型化 locator 绑定和 Profile 迁移门槛 |
| [意图路由](intent-routing.md) | 语义图、embedding 与 Fast/Tree 融合、Top-2 grounding 和单叶子分发 |
| [消息与定时任务](messaging-and-scheduling.md) | 消息进入、Endpoint/Schedule Registry、Delivery Gateway、Web 直接发送和 Timer 执行 |
| [浏览器 Runtime](browser-runtime.md) | agent-browser transport、托管 Chromium profile、浏览器 Workflow 边界和安全约束 |
| [文档 Workflow](document-workflows.md) | 结构化读取、受限编辑、enrichment、保真校验和格式覆盖 |
| [外部集成](integrations.md) | LocalMind task MCP、Telegram、微信、语音转写和 Infinimesh Info |
| [WebChat 语音输入闭环设计](webchat-voice-input-design.md) | Phase 1 稳定采集闭环，以及 Phase 2 native record-time ASR、partial/final reconciliation、silence stop 与 batch fallback 设计；LLM 润色推迟到 Phase 3 |
| [WebChat 语音 Phase 2 设计](webchat-voice-phase2-design.md) | 规范性的 native record-time Qwen ASR transport、revisioned partial/final output、silence auto-stop、完整 WAV fallback 与 acceptance gate |
| [JingSi 局域网 Web 客户端互联](jingsi-lan-connection-design.md) | SparkClaw 侧已实现：专用 allowlisted LAN port 上一个服务端绑定 WebChat session、文本发送和过滤后的实时/补拉消息投影；JingSi client 改造与实体验证仍待完成 |
| [ISCP Bridge](iscp-bridge.md) | 当前共享 Bridge；LocalMind 使用属于旧链路，JingSi 使用保留到 direct-LAN client 与实体验证完成 |
| [统一第三方 ISCP MCP 接入](unified-third-party-access-design.md) | 已实现本地 Route MCP runtime 与分离的 ISCP pairing、SparkClaw MCP access ticket；生产 provisioning、外部 gateway 验证和 LocalMind 旧链路删除仍待完成；不含 JingSi |
| [通用外部 MCP 安全防护](generic-mcp-safeguards-design.md) | 通用 catalog 过滤/分类，以及与固定 LocalMind task adapter 共享的有界脱敏结果和 approval 持久化防护 |
| [按 Owner 的 Connector 启用](connector-owner-runtime-design.md) | Issue #13 已接受设计：owner 隔离 setting、共享 channel worker、缓存一致性、排空语义与重启协调 |
| [WebChat](webchat.md) | owner 工作台职责、API 权威、刷新模型和前端验证 |

## 运维与治理

| 文档 | 范围 |
|---|---|
| [模型加载](model-loading.md) | 单机和多机模型加载策略及验证状态 |
| [Issue #15 部署启动可靠性](issue-15-deployment-reliability-design.md) | 已实现的 state backend、模型 reconciliation、可配置 WebChat port、自包含 readiness 与有限 systemd 启动契约 |
| [Issue #16 工具策略审批](issue-16-external-media-approval-design.md) | 已实现的 ToolDefinition/Policy 边界，治理 external-MCP-AI workspace access，且不把 local model 当作 external principal |
| [Issue #18 文档操作契约](issue-18-document-operation-contract-design.md) | 已实现的 canonical format-operation catalog、统一 source-hash contract 与 runtime-only provenance 边界 |
| [Issue #20 巨型文件拆分](issue-20-god-file-split-design.md) | 已实现的行为不变 panel、CSS、i18n、ToolHub 测试与内嵌 PPTX package 拆分 |
| [模型基线](../benchmarks/model_baseline.md) | 模型端点实测证据和运行边界 |
| [工程基线](engineering-baseline.md) | 不可违反的实现规则 |
| [重构手册](refactor-playbook.md) | 周期性架构检查流程 |
| [Store](store.md) | 类型化 repository、风险分级可靠性、三 backend、内嵌 PostgreSQL migration、Runtime 监管、source layout 与验证 |
| [上下文组装方案](context-assembly-plan.md) | 拟议的阶段 0–1 prompt 组装与工具结果拼装优化 |
| [Info 上游聚合结果消费](info-aggregate-result-consumption-design.md) | 已实现的 Info `answer_context` 类型化、无二次聚合消费方案，覆盖 citation、limitation 与 Info 最终浏览器顺序契约 |
| [PPTX 超长文本韧性适配](pptx-overlength-resilience-design.md) | Phase 0 No-Go 报告与受限渲染检查设计；生产行为保持不变 |
| [DOCX 编辑](docx-editing-optimization.md) | 当前 DOCX 样式验证、证据绑定、run 保真、coverage、目标感知 decision 投影与评测契约 |
| [观测压缩重构](observation-compression-redesign.md) | 已实施的统一工具结果信封、运行时证据供给与无损压缩 |
| [暂缓能力](deferred-email-calendar-knowledge.md) | 已移除的邮件、日历、workspace knowledge 原型及重新引入门槛 |

仓库协作流程见[贡献指南](../CONTRIBUTING.md)、[安全策略](../SECURITY.md)、
[支持说明](../SUPPORT.md)和[变更记录](../CHANGELOG.md)。

## 文档规则

- `architecture.md` 负责跨组件边界。专项手册可以解释实现，但不能重新定义这些边界。
- `workflow-capabilities.md` 只列出已注册且可执行的 Workflow Profile。仅注册
  ToolHub tool 不等于用户能力已经可用。
- `deployment.md` 负责命令和环境配置。专项手册通过链接引用，不重复整套部署步骤。
- 当前行为用现在时描述。计划完成或被替代后，先把长期有效决策合并进当前手册，再删除计划文档。
- 每份英文 Markdown 都在 `zh-cn/` 下有简体中文镜像，且双方互链。
- 代码、schema、生成的 API 类型和测试仍是可执行事实来源。公共契约变化时必须同步修改文档。
