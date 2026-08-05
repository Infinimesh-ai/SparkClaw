# Changelog

> 语言： [English](../CHANGELOG.md) | 简体中文

所有重要的项目级变更都应记录在这里。

项目目前处于 pre-1.0。Breaking changes 可能发生，但当它们影响 users、operators 或 contributors 时应记录。

## [Unreleased]

### Added

- 当前态 architecture、deployment 和 development documentation。
- `zh-cn/` 下的中文项目文档镜像。
- DGX Spark model-serving guidance 和 benchmark evidence。
- 开源项目文件：license、contribution guide、security policy、support guide、code of conduct 和 GitHub templates。

### Changed

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
