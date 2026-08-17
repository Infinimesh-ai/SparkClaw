# Changelog

> 语言： [English](../CHANGELOG.md) | 简体中文

所有重要的项目级变更都应记录在这里。

项目目前处于 pre-1.0。Breaking changes 可能发生，但当它们影响 users、operators 或 contributors 时应记录。

## [Unreleased]

### Added

- 新增网站可流式提供的安装器与 GB10 DGX Spark 部署入口：安全 clone/update checkout，
  在 `curl | bash` 中保留交互式 secret 输入，准备本地配置、下载并预热常驻模型组，启动
  Gateway/Sandbox/WebChat，并验证 ready 状态。
- 新增 Streamable HTTP MCP 发现与 ToolHub 注册，独立连接 Happy Team 任务端点和个人
  bridge；同时新增持久化 Happy supervised-plan 审批收件箱，支持 live plan 重试、编辑和
  remote-first reconciliation。
- 文档 OCR：可选启用的 OvisOCR2 适配器（`internal/documentocr`、`sparkclaw-ocr`
  compose 服务与 `SPARKCLAW_OCR_*` 配置），有界恢复扫描版 PDF 页面、增强图片证据，
  未配置时自动降级为关闭。
- LocalMind scoped workspace MCP 集成：身份锁定的发现、有界目录选择、命名空间化的
  `localmind.*` 动态工具与脱敏限长的结果投影；通过环境变量解析的 URL/token 显式启用。
- 受管入站 MCP/ISCP 访问：单次使用的哈希绑定访问票据、持久化对端绑定与幂等会话操作，
  经加密 ISCP bridge 与可选启用的 LAN `/mcp` 端点暴露；WebChat 提供 owner 侧传输开关
  与访问记录删除。
- 被动 ISCP 协作通知：按 owner 的持久化收件箱与 WebChat 全局通知中心。
- 微信通知绑定的 QR 登录改在受管可见 Chromium 配置内打开，不再调用宿主默认浏览器。
- 当前态 architecture、deployment 和 development documentation。
- `zh-cn/` 下的中文项目文档镜像。
- DGX Spark model-serving guidance 和 benchmark evidence。
- 开源项目文件：license、contribution guide、security policy、support guide、code of conduct 和 GitHub templates。

### Changed

- 部署启动现在把产品 template 对齐到 PostgreSQL，且不迁移旧 file snapshot；
  healthy/current 模型组会被保留，degraded 模型组会原子整组恢复，同时提供显式 force-refresh
  flag；WebChat host port 由一个经过校验的配置统一拥有；readiness 内置于 vLLM 镜像并使用
  best-effort tmpfs marker；boot reconciliation 由最长四小时的 oneshot systemd unit 约束。
- 受管微信 QR 登录 Chromium 窗口现在使用独立的 per-binding lock 与固定 10 分钟 sliding
  lease。30 秒 janitor 会重试失败的过期清理；graceful shutdown 会在 browser adapter 关闭前
  释放全部 tracked window；无关 owner 不再因另一窗口的 browser round trip 而串行等待。
- Connector 启用现已在一个家庭 Gateway 内按 owner 隔离：启动时把全部 owner 的持久化 setting
  恢复到 write-through cache；每 channel 一个共享 worker 使用 owner gate；一个 owner 关闭不会
  停止另一 owner 的 runtime；已接纳 reply 会排空，未 dispatch input 会暂停；预加载失败会阻止
  Gateway listen。`/api/config` 的 `operator_enabled` 现在返回真实静态启动默认值。
- 为文档决策、文档/浏览器模型阶段和最终化统一了消费者级证据投影与谱系/覆盖 audit；
  增加规范化文档操作候选、带 approval 前临时 layout/preservation 预检的一次受限 PPTX
  语义修复、PDF claim coverage、浏览器 transition 证据、重复 action 阻断和确定性 visible
  presentation equivalence。
- 默认 `npm start` 与安装器路径现在使用 PostgreSQL-backed 产品运行态，在 Gateway
  之前启动并等待 PostgreSQL，且不再应用原有的 file-backed `minimal` 覆盖。
- 模型服务 health check 与联合启动现在允许有界的数小时冷下载，不再因原有的短 ready
  窗口而过早失败。
- 将 `document.edit` 升级到 revision 6：XLSX 现在使用类型化有界 sheet 证据、绑定证据的
  workbook/cell/row/sheet 修改、只更新前缀的 `update_row`、六种明确 operation 选择边界和
  失败关闭的 OOXML package 校验来保护每个生成副本。
- 用当前可维护文档替换旧 planning、audit 和 handoff documents。
- 将 intent routing、messaging/scheduling、browser、document、integration 和 WebChat
  文档合并为六份当前专项手册和一份文档索引；删除 29 对已完成或被替代文档。
- 将 runtime skill packages 排除出 bilingual documentation mirror，因为 skills 独立演进。

### Validated

- 证据投影改动通过 Gateway build/test/vet、WebChat test/build、双语文档检查、doctor
  和 47 条隔离 mock/file golden eval。
- PostgreSQL 产品启动 Compose 选择与 readiness。
- WebChat production build。
- Gateway skill registry test。
- Docker Compose config validation。
- `scripts/doctor.sh`。
- Markdown link 和 language-switch checks。
