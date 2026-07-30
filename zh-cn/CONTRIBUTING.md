# 参与贡献 SparkClaw

> 语言： [English](../CONTRIBUTING.md) | 简体中文

感谢你帮助改进 SparkClaw。这个项目是 local-first agent runtime，因此贡献应保持核心边界：可见工具、显式审批、可审计 trace 和安全的本地默认设置。

## 开始之前

- 阅读 [README.md](README.md)、[docs/architecture.md](docs/architecture.md) 和 [docs/development.md](docs/development.md)。
- 大改动请先开 issue 讨论。
- 保持变更范围清晰，避免无关重构。
- 不要提交 `.env`、模型权重、traces、本地 state、密钥或下载数据。

## 开发设置

```bash
npm install
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
npm --workspace @sparkclaw/webchat run dev
```

如果 host 没有 Go，请使用 [docs/development.md](docs/development.md) 中记录的 Docker builder fallback。

## 验证

开发时运行最小相关测试。打开 pull request 前运行：

```bash
npm --workspace @sparkclaw/webchat run build
go test ./services/gateway/...
bash scripts/doctor.sh
docker compose --env-file .env -f docker/compose.yaml config --quiet
```

如果修改 runtime、tool、policy、trace、model routing 或 approval 行为，还要运行：

```bash
bash scripts/run-eval.sh
```

对 Dockerized Gateway 做 eval 时，启动 Gateway 需要设置 `SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal`，并设置 `BROWSER_FIXTURE_URL=http://host.docker.internal:18791`。

## Tool And Runtime Changes

新增或修改工具必须包含：

- typed input and output contracts
- risk level
- policy defaults
- audit events
- observation summaries
- 当 observation 很大或未来有用时做 artifact archive
- unit tests
- 至少一个 golden 或 smoke eval path 覆盖用户可见行为

file、browser 和 external adapter content 都必须视为 untrusted data。Reversible 和 dangerous actions 必须保持 approval-gated。

## 文档

如果变更影响 commands、configuration、environment variables、deployment、safety boundaries、APIs 或 user workflows，请更新 docs。

默认文档为英文。中文文档位于 `zh-cn/`。

## Pull Request Checklist

- 相关测试通过。
- UI/API type 变更通过 WebChat build。
- Go 变更通过 Gateway tests。
- Docker/config 变更通过 Compose config 校验。
- runtime behavior 变更通过 golden eval。
- operator 或 contributor 行为变化时更新文档。
- 不包含 secrets、traces、local state 或 model weights。
- PR 说明 risk、verification 和 known limitations。
