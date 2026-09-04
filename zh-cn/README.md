# SparkClaw

> 语言： [English](../README.md) | 简体中文

**提供明确本地与远端模型部署的可靠个人 Agent Runtime。**

SparkClaw 将本地模型变成一个有边界、可审计的个人工作流系统。它面向单个本地 AI 工作站 owner，强调本地优先的数据处理、明确的工具契约、危险动作审批、trace、artifact 和可重复评测。当前本地模型形态使用一个响应快的 `fast` MoE chat 模型，并常驻 embedding 与 guard 端点；`deep` 模型暂不接入默认产品 runtime。

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
- Local 与 Remote 统一使用 `sparkclaw-product-v1` 容量契约；Local 运行一个 `fast` chat 进程，加 embedding、guard、Qwen3-ASR 与 OvisOCR2，逻辑 Deep 路由到同一个 Fast endpoint。本地 262K 物理模型资格验证仍是后续工作。
- ToolHub：为文件、memory、browser access、sandbox shell、code patch、notification 和 approval 提供 JSON Schema 校验工具。
- approval-first policy：file deletion、shell execution、patch application 和 sensitive memory write 等 reversible/dangerous action 都需要审批。
- file、browser 和 external adapter observation 都被当作 untrusted data，并在进入回答前被摘要。
- 已通过专用 Host-CDP Chromium Profile 实现 QQ 邮箱、Outlook 和 Gmail 的仅发送浏览器邮箱能力；见[浏览器邮箱 Workflow](docs/browser-email-workflow-design.md)。邮件读取、日历和 Workspace Knowledge/RAG 仍保持暂缓；见[暂缓能力说明](docs/deferred-email-calendar-knowledge.md)。
- 本地 file-backed state，PostgreSQL 18/pgvector 持久化 runtime records，以及 filesystem 或 S3-compatible artifact storage。
- React/Vite WebChat workbench：chat、tool timeline、approval inbox、memory editor、trace viewer、eval/status/settings panels 和 model telemetry。
- 两套明确产品部署：NVIDIA GB10 全本地模型与公网模型全远端；两者都包含 PostgreSQL、Sandbox Runner、Gotenberg、Gateway 和 WebChat。
- JingSi→SparkClaw Runtime JSON/HTTP v1 provider 已在显式 loopback-only、独立 bearer 配置后实现。它在执行前持久化 request-key binding 与不可逆 negative fence，提供 submit/lookup/status/cancel/event-page actions，恢复未完成记录，并在 request-scoped tool/budget 收窄后进入既有 Agent Runtime。`return_nowhere` 不查询 endpoint；有界 IMMS Memory Context 只在 intent/capability admission 后作为 data 持久化并进入 workflow。仓库继续直接读取中央决策 0007 的 Schema/binding/fixtures。
- DGX Spark NVIDIA GB10 验证：PostgreSQL 18/pgvector、MinIO、sandbox-runner 与 vLLM fast/deep/embedding endpoints。当前 Fast + Embedding 校准在 15 条标注意图上达到 15/15。2026-08-24，恢复后的 vLLM-managed NVFP4 路径通过全部 47 个 real-model golden cases，没有 mock call 或 model error。

已知运行边界：

- 在已验证的 GB10 机器上，full 128K context 且启用 MTP 时，fast 和 deep chat lanes 应视为不能同时常驻，除非降低 context、MTP 或 GPU memory utilization 后重新测量。
- 两种产品模式统一选择 `sparkclaw-product-v1`：Fast 与逻辑 Deep 共享 262K context 契约和 Remote output budget；embedding、guard、OCR 分别使用 8K、8K、32K 契约。Local serving 必须满足该契约，不能静默选择更小 profile。历史 `dual-light-v1` 实测只保留为 benchmark 证据，不构成产品模式。
- Gateway 仍记录逻辑 fast/deep Workflow 选择，但当前部署配置把两个 chat profiles 都解析到 `sparkclaw-fast`，不会启动 `sparkclaw-deep` 模型进程。
- Workflow capability 是唯一执行路径；当前能力面以 [Workflow 能力矩阵](docs/workflow-capabilities.md)为准。
- 开发机门禁现以彼此独立的 PostgreSQL 18、真实 IMMS/SparkClaw/JingSi 服务与真实 JingSi-Node 进程贯通 Task Intake、Memory Context、Runtime 成功结果、IMMS Observation 及 origin notification/ACK。生产 service credential、断电/备份恢复、真实网络与 GB10 物理验收仍待完成。

## 快速开始

Ubuntu 服务器或 VM 使用版本化公网模型端点时，以具备 sudo 权限的普通用户运行 remote
安装入口。它会在需要时安装 Docker，把凭据和机器覆盖项只保存在本机权限为 `0600` 的
`.env.remote` 中，并
在宿主机安装固定版本的 SparkClaw Chromium。部署会验证 `agent-browser` 能通过受保护的
Host-CDP endpoint attach，并确认 MCP smoke process 停止后宿主 Chromium 仍在运行：

```bash
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --connect-timeout 15 --max-time 300 \
  https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/refs/heads/main/install-remote.sh | bash
```

Fast、Embedding、Guard、ASR、OCR 五个公网端点写在
`docker/env/sparkclaw.remote.env`；共享容量与业务契约写在
`docker/env/sparkclaw.product.env`，Fast 同时承担逻辑 Deep lane。模型 endpoint 不需要认证时，
交互配置中的 API Key 直接回车留空。

在已安装 Docker、Compose 与 NVIDIA Container Toolkit 的 NVIDIA GB10 DGX Spark 上，
运行流式安装入口：

```bash
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 300 https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/main/install.sh | bash
```

网站可以原样镜像仓库根目录的 `install.sh`，并替换上述 URL。bootstrap 会在
`$HOME/SparkClaw` 安装仓库或执行安全 fast-forward，然后把 stdin 重新连接到终端，确保
Hugging Face token 可以隐藏输入。部署会硬性校验 Linux/ARM64 与 NVIDIA GB10，下载 Fast、
embedding、guard、ASR 和 OCR，再构建并启动 PostgreSQL、Sandbox Runner、Gotenberg、
Gateway 与 WebChat；后续运行复用模型缓存。

本机打开 [http://127.0.0.1:18790](http://127.0.0.1:18790)；同一局域网的其他设备
使用 `http://<主机局域网-IP>:18790`。首次自配对必须从 SparkClaw 宿主完成；局域网浏览器
需要使用已预先提供的 Gateway client token。

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
`agent-browser 0.32.3`，在宿主机安装当前架构获准的 SparkClaw Chromium 制品，并配置
owner-scoped `sparkclaw-browserd` service 与专用持久 Profile。Gateway 不包含 Chromium，
只通过 Host-CDP attach。

产品启动必须显式选择模式。先加载版本化 profile，再加载对应的 Git 忽略私有覆盖文件：

```bash
npm run start:local   # product env + local env + .env.local
npm run start:remote  # product env + remote env + .env.remote
```

产品不再提供 `online` 或托管 chat 加本地辅助模型的混合模式。remote 启动会校验全部模型
URL，并在 reconcile 应用服务前停止本地模型容器。直接运行宿主 mock/file Gateway 或 Vite
仅用于隔离调试，对应 `npm run dev:gateway:host` 和 `npm run dev:webchat:host`。

direct model-router smoke test：

```bash
docker compose -f docker/compose.yaml exec -T gateway node -e \
  "fetch('http://127.0.0.1:18789/chat',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({profile:'deep',message:'Say hello from the selected SparkClaw lane.'})}).then(async response=>{console.log(await response.text());process.exit(response.ok?0:1)})"
```

产品 Compose 不在 host 发布 `18789`，因此该命令在 Gateway container 内执行。`profile`
可以是 `fast`、`deep` 或配置中的 chat profile/model name。启用 Gateway auth 时，`/chat`
和 `/api/*` 使用同一个 bearer token。

## 验证

交付变更前推荐运行：

```bash
npm --workspace @sparkclaw/webchat run build
go test ./services/gateway/...
bash scripts/doctor.sh
bash scripts/run-eval.sh
docker compose --env-file docker/env/sparkclaw.product.env \
  --env-file docker/env/sparkclaw.local.env \
  -f docker/compose.yaml -f docker/compose.models.local.yaml \
  --profile product --profile models-local config --quiet
docker compose --env-file docker/env/sparkclaw.product.env \
  --env-file docker/env/sparkclaw.remote.env \
  -f docker/compose.yaml --profile product config --quiet
```

当前 golden eval 覆盖 47 个 case，验证 direct chat、config/tool visibility、auth/rate-limit surfaces、grounded file/browser answers、approval lifecycle、memory review、sensitive-memory handling、prompt-injection chaos、trace refresh、artifact catalog、model-call telemetry 和 eval history。

## 开源

SparkClaw 使用 [Apache License 2.0](../LICENSE)。欢迎通过 issues 和 pull requests 参与。

贡献前请阅读 [CONTRIBUTING.md](../CONTRIBUTING.md)。安全漏洞请按 [SECURITY.md](../SECURITY.md) 私下报告，不要在公开 issue 中发布利用细节。

npm workspace root 保持 `private`，用于避免误发布 package。仓库本身是开源的；runtime images 和未来 release artifacts 应该经过明确流程发布。

## DGX Spark 模型

当前全本地产品先完成部署，之后使用明确的 local 启动入口：

```bash
npm run deploy:local
npm run start:local
```

local 入口把 Fast、embedding、guard、ASR 与 OCR 作为一个模型组，两个逻辑 chat profile
都映射到 `sparkclaw-fast`，并启动包含 Gotenberg 的五个应用服务。以下 model helper 只用于
定向控制与 benchmark，不构成额外产品模式。

其他服务入口用于定向测试和对照实验：

```bash
scripts/serve_fast.sh
scripts/serve_deep.sh
scripts/serve_models_compose.sh fast
scripts/serve_models_compose.sh deep
scripts/serve_models_compose.sh dual-light
scripts/serve_models_compose.sh dual-light-chat
scripts/serve_models_compose.sh embedding
```

默认 served lanes：

| Lane | Served name | Port | Default checkpoint |
|---|---|---:|---|
| fast | `sparkclaw-fast` | 8001 | `nvidia/Qwen3.6-35B-A3B-NVFP4` |
| deep | `sparkclaw-deep` | 8002 | `nvidia/Qwen3.6-35B-A3B-NVFP4` |
| embedding | `sparkclaw-embedding` | 8003 | `Qwen/Qwen3-Embedding-0.6B` |

当前单机产品 profile 保持保守：chat 只由 `fast` 提供，两个逻辑 chat profile 都使用该 NVFP4 endpoint，MTP 关闭，embedding 与 guard 使用较小的显式 KV budget。独立 vLLM 0.24.0 chat image 负责解释 checkpoint quantization metadata，并自主选择 activation 与 kernel；SparkClaw 不提供 quantization override。Deep 和 dual-light 命令仍可用于定向对照，但不再加载 FP8 chat checkpoint。

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
