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
- 当前态 architecture、deployment 和 development documentation。
- `zh-cn/` 下的中文项目文档镜像。
- DGX Spark model-serving guidance 和 benchmark evidence。
- 开源项目文件：license、contribution guide、security policy、support guide、code of conduct 和 GitHub templates。

### Changed

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

- WebChat production build。
- Gateway skill registry test。
- Docker Compose config validation。
- `scripts/doctor.sh`。
- Markdown link 和 language-switch checks。
