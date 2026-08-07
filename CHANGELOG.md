# Changelog

> Language: English | [简体中文](zh-cn/CHANGELOG.md)

All notable project-level changes should be recorded here.

The project is pre-1.0. Breaking changes may occur, but they should be documented when they affect users, operators or contributors.

## [Unreleased]

### Added

- A website-streamable installer and GB10 DGX Spark deployment entrypoint that
  safely clone/update the checkout, preserve an interactive secret prompt across
  `curl | bash`, prepare local configuration, download and warm the resident
  model group, start Gateway/Sandbox/WebChat, and verify readiness.
- Streamable HTTP MCP discovery and ToolHub registration for independent Happy
  Team task and personal bridge endpoints, plus a durable Happy supervised-plan
  approval inbox with live plan retry, editing, and remote-first reconciliation.
- Current-state architecture, deployment and development documentation.
- Chinese documentation mirror under `zh-cn/` for project docs.
- DGX Spark model-serving guidance and benchmark evidence.
- Open-source project files: license, contribution guide, security policy, support guide, code of conduct and GitHub templates.

### Changed

- Model-serving health checks and joint startup now allow bounded multi-hour
  cold downloads instead of failing after the previous short readiness window.
- Advanced `document.edit` to revision 6 for XLSX: typed bounded sheet evidence,
  evidence-bound workbook/cell/row/sheet edits, prefix-only `update_row`, six
  explicit operation-selection boundaries, and fail-closed OOXML package
  verification now protect every generated copy.
- Replaced old planning, audit and handoff documents with current maintainable docs.
- Consolidated intent routing, messaging/scheduling, browser, document,
  integration and WebChat documentation into six current component guides plus
  one documentation index; removed 29 completed or superseded document pairs.
- Excluded runtime skill packages from the bilingual documentation mirror because skills evolve independently.

### Validated

- WebChat production build.
- Gateway skill registry test.
- Docker Compose config validation.
- `scripts/doctor.sh`.
- Markdown link and language-switch checks.
