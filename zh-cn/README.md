# SparkClaw

> 语言： [English](../README.md) | 简体中文

**面向 DGX Spark 的可靠本地 Agent Runtime。**

SparkClaw 将本地模型变成一个有边界、可审计的个人工作流系统。它面向单个本地 AI 工作站 owner，强调本地优先的数据处理、明确的工具契约、危险动作审批、trace、artifact 和可重复评测。当前本地模型形态已经是完整的单机双 lane 栈：响应快的 `fast` MoE lane、用于更难或更高风险工作的稠密 `deep` lane，以及为 semantic routing 常驻的 embedding 端点。

项目已经过了早期规划阶段。本 README 是入口；完整当前文档集合见
[文档索引](docs/index.md)。建议从以下文档开始：

- [架构](docs/architecture.md)：产品边界、运行循环、服务边界、工具、数据和安全模型。
- [部署](docs/deployment.md)：本地、Docker Compose 和 DGX Spark 模型服务部署。
- [开发](docs/development.md)：仓库结构、验证矩阵、扩展流程和当前完成状态。
- [Workflow 能力矩阵](docs/workflow-capabilities.md)：当前可实际执行的准确叶子 Workflow。
- [意图路由](docs/intent-routing.md)、[消息与定时任务](docs/messaging-and-scheduling.md)、[浏览器 Runtime](docs/browser-runtime.md)、[文档 Workflow](docs/document-workflows.md)、[外部集成](docs/integrations.md)和 [WebChat](docs/webchat.md)：当前专项契约。
- [模型加载方案](docs/model-loading.md)：单机、轻量双常驻和双 DGX Spark 加载策略。
- [模型基线](benchmarks/model_baseline.md)：DGX Spark 端点证据、延迟数据和运行限制。
- [Contributing](../CONTRIBUTING.md)、[Security](../SECURITY.md)、[Support](../SUPPORT.md)、[Changelog](../CHANGELOG.md) 和 [License](../LICENSE)：开源项目流程和条款。

## 当前状态

已实现并验证：

- Go Gateway API：健康检查、ready 检查、direct chat、sessions、messages、events、tools、approvals、memories、traces、artifacts、eval reports、feedback、client pairing、token auth 和 rate limiting。
- Agent Runtime：Catalog 派生语义图、全候选 embedding 与 Fast/Tree 分数融合、确定性 Top-2 和单叶子 Workflow 分发、grounded execution、repair 和 trace snapshot。
- 单机 `dual-light-v1` 模型 profile 已在 NVIDIA GB10 上验证：`fast` 与 `deep` chat lanes 加上 embedding 可以同时常驻，并使用显式 context、KV cache 和 sequence caps。
- ToolHub：为文件、memory、browser access、sandbox shell、code patch、notification 和 approval 提供 JSON Schema 校验工具。
- approval-first policy：file deletion、shell execution、patch application 和 sensitive memory write 等 reversible/dangerous action 都需要审批。
- file、browser 和 external adapter observation 都被当作 untrusted data，并在进入回答前被摘要。
- 邮件、日历和 Workspace Knowledge/RAG 在具备完整产品设计前明确暂缓；见[暂缓能力说明](docs/deferred-email-calendar-knowledge.md)。
- 本地 file-backed state，PostgreSQL 18/pgvector 持久化 runtime records，以及 filesystem 或 S3-compatible artifact storage。
- React/Vite WebChat workbench：chat、tool timeline、approval inbox、memory editor、trace viewer、eval/status/settings panels 和 model telemetry。
- Docker Compose profiles：mock local operation、development、evaluation、external model compatibility 和 DGX Spark local-model serving。
- DGX Spark NVIDIA GB10 验证：PostgreSQL 18/pgvector、MinIO、sandbox-runner 与 vLLM fast/deep/embedding endpoints。当前 Fast + Embedding 校准在 15 条标注意图上达到 15/15。43-case runner 仍包含已退役 code/shell 原型 Workflow 的断言，在与当前能力矩阵对齐前不能作为完整的当前验收结果。

已知运行边界：

- 在已验证的 GB10 机器上，full 128K context 且启用 MTP 时，fast 和 deep chat lanes 应视为不能同时常驻，除非降低 context、MTP 或 GPU memory utilization 后重新测量。
- 已验证的单机常驻 profile 是 `dual-light-v1`：fast 使用 32K context + 8G KV cache，deep 使用 64K context + 12G KV cache，二者都关闭 MTP。Deep 是稠密模型，慢是预期内；更广的产品验收仍需使用与当前能力对齐的端到端矩阵。
- 决定调用哪个 chat lane 的是 Gateway，不是 `fast` 模型本身。代码、terminal、危险、repair 或显式 deep/review 请求会走 `deep`；常规有边界任务走 `fast`。只有 fast 调用失败时，才会把 deep 作为 fallback。
- Workflow capability 是唯一执行路径；当前能力面以 [Workflow 能力矩阵](docs/workflow-capabilities.md)为准。

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

安装宿主开发依赖：

```bash
npm run setup:host
```

该命令安装根 workspace Node 包、用户 site-packages 中的 Python 文档库，校验固定版本的
agent-browser Runtime，并解析系统 Chromium；它不会下载 Chrome for Testing。当
Chromium 位于非标准路径时，设置
`adapters.browserAutomation.chromiumExecutable`。

重建并重启当前真机使用的 external-model/PostgreSQL 开发运行态：

```bash
npm run dev
```

只重建一个应用容器时，使用 `npm run dev:gateway` 或 `npm run dev:webchat`。
直接运行宿主 mock/file Gateway 或 Vite 仅用于隔离调试，对应
`npm run dev:gateway:host` 和 `npm run dev:webchat:host`。

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

当前 golden eval 覆盖 43 个 case，验证 direct chat、config/tool visibility、auth/rate-limit surfaces、grounded file/browser answers、approval lifecycle、memory review、sensitive-memory handling、prompt-injection chaos、trace refresh、artifact catalog、model-call telemetry 和 eval history。

## 开源

SparkClaw 使用 [Apache License 2.0](../LICENSE)。欢迎通过 issues 和 pull requests 参与。

贡献前请阅读 [CONTRIBUTING.md](../CONTRIBUTING.md)。安全漏洞请按 [SECURITY.md](../SECURITY.md) 私下报告，不要在公开 issue 中发布利用细节。

npm workspace root 保持 `private`，用于避免误发布 package。仓库本身是开源的；runtime images 和未来 release artifacts 应该经过明确流程发布。

## DGX Spark 模型

当前完整本地模型路径先启动已验证的单机常驻 profile：

```bash
scripts/serve_models_compose.sh dual-light
scripts/restart_runtime_compose.sh
```

`dual-light` 会启动完整产品模型常驻服务：`fast`、`deep` 和 embedding。`scripts/restart_runtime_compose.sh` 随后以 `external/postgres` mode 重启 Gateway/WebChat，如果 Gateway 未 ready 会失败退出。

其他服务入口用于定向测试和对照实验：

```bash
scripts/serve_fast.sh
scripts/serve_deep.sh
scripts/serve_models_compose.sh fast
scripts/serve_models_compose.sh deep
scripts/serve_models_compose.sh dual-light-chat
scripts/serve_models_compose.sh embedding
```

默认 served lanes：

| Lane | Served name | Port | Default checkpoint |
|---|---|---:|---|
| fast | `sparkclaw-fast` | 8001 | `Qwen/Qwen3.6-35B-A3B-FP8` |
| deep | `sparkclaw-deep` | 8002 | `Qwen/Qwen3.6-27B-FP8` |
| embedding | `sparkclaw-embedding` | 8003 | `Qwen/Qwen3-Embedding-0.6B` |

已验证的单机常驻 profile 是保守取舍：`fast` 是响应快的 MoE lane，`deep` 是稠密的稳定性/质量 lane，MTP 关闭，embedding 使用小的显式 KV budget 以保证当前模型栈能放下。`dual-light-chat` 只用于不带 embedding 端点的 chat-lane 对照实验。

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
zh-cn/                     Chinese documentation mirror
```

## 设计边界

SparkClaw 不是无限制 autonomous agent。read-only 和 draft tools 可以在配置边界内执行。reversible 和 dangerous actions 必须经过审批。tool observations 都是 untrusted data。每个重要 run 都应该留下可检查的 audit events、trace metadata 和 artifact references。
