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
| [意图路由](intent-routing.md) | 语义图、embedding 与 Fast/Tree 融合、Top-2 grounding 和单叶子分发 |
| [消息与定时任务](messaging-and-scheduling.md) | 消息进入、Endpoint/Schedule Registry、Delivery Gateway、Web 直接发送和 Timer 执行 |
| [浏览器 Runtime](browser-runtime.md) | agent-browser transport、托管 Chromium profile、浏览器 Workflow 边界和安全约束 |
| [文档 Workflow](document-workflows.md) | 结构化读取、受限编辑、enrichment、保真校验和格式覆盖 |
| [外部集成](integrations.md) | Telegram、微信、语音转写和 Infinimesh Info |
| [ISCP Bridge](iscp-bridge.md) | JingSi App 安全会话、注册边界、agent 协议与 GB10 运维 |
| [WebChat](webchat.md) | owner 工作台职责、API 权威、刷新模型和前端验证 |

## 运维与治理

| 文档 | 范围 |
|---|---|
| [模型加载](model-loading.md) | 单机和多机模型加载策略及验证状态 |
| [模型基线](../benchmarks/model_baseline.md) | 模型端点实测证据和运行边界 |
| [工程基线](engineering-baseline.md) | 不可违反的实现规则 |
| [重构手册](refactor-playbook.md) | 周期性架构检查流程 |
| [上下文组装方案](context-assembly-plan.md) | 拟议的阶段 0–1 prompt 组装与工具结果拼装优化 |
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
