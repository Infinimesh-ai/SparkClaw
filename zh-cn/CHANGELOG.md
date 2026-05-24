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

- 用当前可维护文档替换旧 planning、audit 和 handoff documents。
- 将 runtime skill packages 排除出 bilingual documentation mirror，因为 skills 独立演进。

### Validated

- WebChat production build。
- Gateway skill registry test。
- Docker Compose config validation。
- `scripts/doctor.sh`。
- Markdown link 和 language-switch checks。
