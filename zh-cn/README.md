# SparkClaw

> 语言： [English](../README.md) | 简体中文

**面向 DGX Spark 的可靠本地 Agent Runtime。**

SparkClaw 将本地模型变成一个有边界、可审计的个人工作流系统。它面向单个本地 AI 工作站 owner，强调本地优先的数据处理、明确的工具契约、危险动作审批、trace、artifact 和可重复评测。当前本地模型形态已经是完整的单机双 lane 栈：响应快的 `fast` MoE lane、用于更难或更高风险工作的稠密 `deep` lane，以及为 retrieval workflow 常驻的 embedding/reranker 端点。

项目已经过了早期规划阶段。本 README 是入口，当前有效的详细文档是：

- [架构](docs/architecture.md)：产品边界、运行循环、服务边界、工具、数据和安全模型。
- [部署](docs/deployment.md)：本地、Docker Compose 和 DGX Spark 模型服务部署。
- [开发](docs/development.md)：仓库结构、验证矩阵、扩展流程和当前完成状态。
- [模型加载方案](docs/model-loading.md)：单机、轻量双常驻和双 DGX Spark 加载策略。
- [模型基线](benchmarks/model_baseline.md)：DGX Spark 端点证据、延迟数据和运行限制。
- [Contributing](../CONTRIBUTING.md)、[Security](../SECURITY.md)、[Support](../SUPPORT.md)、[Changelog](../CHANGELOG.md) 和 [License](../LICENSE)：开源项目流程和条款。

## 当前状态

已实现并验证：

- Go Gateway API：健康检查、ready 检查、direct chat、sessions、messages、events、tools、approvals、memories、traces、artifacts、eval reports、feedback、client pairing、token auth 和 rate limiting。
- Agent Runtime：guard review、由 Gateway 控制的 fast/deep routing、有界 repair、schema repair、grounded final answer 和 trace snapshot。
- 单机 `dual-light-v1` 模型 profile 已在 NVIDIA GB10 上验证：`fast` 与 `deep` chat lanes 加上 embedding/reranker 可以同时常驻，并使用显式 context、KV cache 和 sequence caps。
- ToolHub：为文件、memory、browser access、sandbox shell、code patch、notification 和 approval 提供 JSON Schema 校验工具。
- approval-first policy：file deletion、shell execution、patch application 和 sensitive memory write 等 reversible/dangerous action 都需要审批。
- file、browser 和 external adapter observation 都被当作 untrusted data，并在进入回答前被摘要。
- 邮件、日历和 Workspace Knowledge/RAG 在具备完整产品设计前明确暂缓；见[暂缓能力说明](docs/deferred-email-calendar-knowledge.md)。
- 本地 file-backed state，PostgreSQL 18/pgvector 持久化 sessions、tool calls、approvals、evals、document chunks，以及 filesystem 或 S3-compatible artifact storage。
- React/Vite WebChat workbench：chat、tool timeline、approval inbox、memory editor、trace viewer、eval/status/settings panels 和 model telemetry。
- Docker Compose profiles：mock local operation、development、evaluation、external model compatibility 和 DGX Spark local-model serving。
- DGX Spark NVIDIA GB10 验证：PostgreSQL 18/pgvector、MinIO、sandbox-runner 与 vLLM fast/deep/embedding/reranker endpoints。历史运行使用 58 个 case；移除仅有原型的能力后，当前活动矩阵为 43 个 case。

已知运行边界：

- 在已验证的 GB10 机器上，full 128K context 且启用 MTP 时，fast 和 deep chat lanes 应视为不能同时常驻，除非降低 context、MTP 或 GPU memory utilization 后重新测量。
- 当前接受的单机双 lane profile 是 `dual-light-v1`：fast 使用 32K context + 8G KV cache，deep 使用 64K context + 12G KV cache，二者都关闭 MTP。Deep 是稠密模型，慢是预期内；验收标准是任务稳定性和产品综合体验，而不是单独追求 deep throughput。
- 决定调用哪个 chat lane 的是 Gateway，不是 `fast` 模型本身。代码、terminal、危险、repair 或显式 deep/review 请求会走 `deep`；常规有边界任务走 `fast`。只有 fast 调用失败时，才会把 deep 作为 fallback。
- `skills/` 下的 skill package 是运行时 workflow 描述，后续会持续升级；它们不是项目文档的事实来源。

## 快速开始

推荐先用 Docker 路径启动：

```bash
cp docker/env/sparkclaw.example.env .env
sudo -n env SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal \
  docker compose --env-file .env -f docker/compose.yaml --profile minimal up -d --build
```

本机打开 [http://127.0.0.1:18790](http://127.0.0.1:18790)；同一局域网的其他设备
使用 `http://<主机局域网-IP>:18790`。

运行健康检查和 golden eval：

```bash
bash scripts/doctor.sh
BROWSER_FIXTURE_URL=http://host.docker.internal:18791 \
BROWSER_FIXTURE_BIND=0.0.0.0 \
bash scripts/run-eval.sh
```

browser allowlist 只用于确定性的本地 fixture。正常运行中，`browser.read` 默认拒绝 loopback/private hosts，除非显式 allowlist。

## 本地开发

安装 JavaScript 依赖：

```bash
npm install
```

分别启动 Gateway 和 WebChat：

```bash
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
npm --workspace @sparkclaw/webchat run dev
```

如果 host 没有 Go，可以使用 `scripts/doctor.sh` 同样采用的 Docker Go builder 路径：

```bash
sudo -n docker run --rm -u "$(id -u):$(id -g)" \
  -v "$PWD":/workspace -w /workspace/services/gateway \
  -e HOME=/tmp -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomodcache \
  golang:1.25-alpine /usr/local/go/bin/go test ./...
```

direct model-router smoke test：

```bash
curl -fsS -X POST http://127.0.0.1:18789/chat \
  -H 'Content-Type: application/json' \
  -d '{"profile":"deep","message":"Say hello from the selected SparkClaw lane."}'
```

`profile` 可以是 `fast`、`deep` 或配置中的 chat profile/model name。启用 Gateway auth 时，`/chat` 和 `/api/*` 使用同一个 bearer token。

## 验证

交付变更前推荐运行：

```bash
npm --workspace @sparkclaw/webchat run build
go test ./services/gateway/...
bash scripts/doctor.sh
bash scripts/run-eval.sh
docker compose --env-file .env -f docker/compose.yaml config --quiet
```

当前 golden eval 覆盖 43 个 case，验证 direct chat、config/tool/skill visibility、auth/rate-limit surfaces、grounded file/browser answers、approval lifecycle、memory review、sensitive-memory handling、prompt-injection chaos、trace refresh、artifact catalog、model-call telemetry 和 eval history。

## 开源

SparkClaw 使用 [Apache License 2.0](../LICENSE)。欢迎通过 issues 和 pull requests 参与。

贡献前请阅读 [CONTRIBUTING.md](../CONTRIBUTING.md)。安全漏洞请按 [SECURITY.md](../SECURITY.md) 私下报告，不要在公开 issue 中发布利用细节。

npm workspace root 保持 `private`，用于避免误发布 package。仓库本身是开源的；runtime images 和未来 release artifacts 应该经过明确流程发布。

## DGX Spark 模型

当前完整本地模型路径先启动已接受的单机 profile：

```bash
scripts/serve_models_compose.sh dual-light
scripts/restart_runtime_compose.sh
```

`dual-light` 会启动完整产品模型常驻服务：`fast`、`deep`、embedding 和 reranker。`scripts/restart_runtime_compose.sh` 随后以 `external/postgres` mode 重启 Gateway/WebChat，如果 Gateway 未 ready 会失败退出。

其他服务入口用于定向测试和对照实验：

```bash
scripts/serve_fast.sh
scripts/serve_deep.sh
scripts/serve_models_compose.sh fast
scripts/serve_models_compose.sh deep
scripts/serve_models_compose.sh dual-light-chat
scripts/serve_models_compose.sh embedding,reranker
```

默认 served lanes：

| Lane | Served name | Port | Default checkpoint |
|---|---|---:|---|
| fast | `sparkclaw-fast` | 8001 | `Qwen/Qwen3.6-35B-A3B-FP8` |
| deep | `sparkclaw-deep` | 8002 | `Qwen/Qwen3.6-27B-FP8` |
| embedding | `sparkclaw-embedding` | 8003 | `Qwen/Qwen3-Embedding-0.6B` |
| reranker | `sparkclaw-reranker` | 8004 | `Qwen/Qwen3-Reranker-0.6B` |

已接受的单机 profile 是保守取舍：`fast` 是响应快的 MoE lane，`deep` 是稠密的稳定性/质量 lane，MTP 关闭，辅助模型使用小的显式 KV budget 以保证完整产品栈能放下。`dual-light-chat` 只用于不带辅助端点的 chat-lane 对照实验。

加载策略见 [docs/model-loading.md](docs/model-loading.md)。Benchmark 证据、endpoint 快照和运行说明见 [benchmarks/model_baseline.md](benchmarks/model_baseline.md)。

## 仓库结构

```text
apps/webchat/              Vite/React workbench
services/gateway/          Go Gateway, Agent Runtime, ToolHub, policy and traces
configs/                   Model, tool, sandbox, logging and eval configuration
docker/                    Compose file and image definitions
scripts/                   Doctor, eval, model serving and benchmark helpers
eval/golden/               Golden task definitions
benchmarks/                DGX Spark model baseline evidence
docs/                      Current project architecture, deployment and development docs
packages/                  Portable protocol, policy and tool-schema notes
skills/                    Runtime workflow skill packages
zh-cn/                     Chinese documentation mirror
```

## 设计边界

SparkClaw 不是无限制 autonomous agent。read-only 和 draft tools 可以在配置边界内执行。reversible 和 dangerous actions 必须经过审批。tool observations 都是 untrusted data。每个重要 run 都应该留下可检查的 audit events、trace metadata 和 artifact references。
