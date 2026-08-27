# SparkClaw

> 语言： [English](../README.md) | 简体中文

**面向 DGX Spark 的可靠本地 Agent Runtime。**

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
- NVIDIA GB10 当前使用单机 `single-fast-v1` 产品 profile：一个 `fast` chat 模型加 embedding、guard 与 OvisOCR2 文档适配器；逻辑 deep Workflow profile 也路由到同一个 Fast endpoint。
- ToolHub：为文件、memory、browser access、sandbox shell、code patch、notification 和 approval 提供 JSON Schema 校验工具。
- approval-first policy：file deletion、shell execution、patch application 和 sensitive memory write 等 reversible/dangerous action 都需要审批。
- file、browser 和 external adapter observation 都被当作 untrusted data，并在进入回答前被摘要。
- 邮件、日历和 Workspace Knowledge/RAG 在具备完整产品设计前明确暂缓；见[暂缓能力说明](docs/deferred-email-calendar-knowledge.md)。
- 本地 file-backed state，PostgreSQL 18/pgvector 持久化 runtime records，以及 filesystem 或 S3-compatible artifact storage。
- React/Vite WebChat workbench：chat、tool timeline、approval inbox、memory editor、trace viewer、eval/status/settings panels 和 model telemetry。
- Docker Compose profiles：mock local operation、development、evaluation、external model compatibility 和 DGX Spark local-model serving。
- DGX Spark NVIDIA GB10 验证：PostgreSQL 18/pgvector、MinIO、sandbox-runner 与 vLLM fast/deep/embedding endpoints。当前 Fast + Embedding 校准在 15 条标注意图上达到 15/15。2026-08-24，恢复后的 vLLM-managed NVFP4 路径通过全部 47 个 real-model golden cases，没有 mock call 或 model error。

已知运行边界：

- 在已验证的 GB10 机器上，full 128K context 且启用 MTP 时，fast 和 deep chat lanes 应视为不能同时常驻，除非降低 context、MTP 或 GPU memory utilization 后重新测量。
- 当前单机产品 profile 是 `single-fast-v1`：fast 使用 32K context + 8G KV cache 并关闭 MTP；embedding、guard 与 OvisOCR2 保持有界的辅助配置。历史 `dual-light-v1` 实测仍作为证据保留，但不再是默认启动项。
- Gateway 仍记录逻辑 fast/deep Workflow 选择，但当前部署配置把两个 chat profiles 都解析到 `sparkclaw-fast`，不会启动 `sparkclaw-deep` 模型进程。
- Workflow capability 是唯一执行路径；当前能力面以 [Workflow 能力矩阵](docs/workflow-capabilities.md)为准。

## 快速开始

Ubuntu 测试 VM 使用外部模型 endpoint 时，以具备 sudo 权限的普通用户运行 cloud 安装入口。
它会在需要时安装 Docker，把 endpoint 和可选 API Key 只保存在本机 `0600` 配置文件中，并
验证容器内 Chromium 自动化运行态：

```bash
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --connect-timeout 15 --max-time 300 \
  https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/refs/heads/main/install-cloud.sh | bash
```

模型 endpoint 不需要认证时，交互配置中的 API Key 直接回车留空。当前可信局域网 cloud
profile 不启用 Gateway owner-token 认证，部署完成后直接访问 `http://<VM-IP>:18790`。

在已安装 Docker、Compose 与 NVIDIA Container Toolkit 的 NVIDIA GB10 DGX Spark 上，
运行流式安装入口：

```bash
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 300 https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/main/install.sh | bash
```

网站可以原样镜像仓库根目录的 `install.sh`，并替换上述 URL。bootstrap 会在
`$HOME/SparkClaw` 安装仓库或执行安全 fast-forward，然后把 stdin 重新连接到终端，确保
Hugging Face token 可以隐藏输入。部署会硬性校验 Linux/ARM64 与 NVIDIA GB10，下载 Fast、
embedding、guard 和 OCR，再构建并启动 Gateway、Sandbox Runner 与 WebChat；后续运行复用
模型缓存。

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

重建并重启当前真机使用的 external-model/OCR/PostgreSQL 开发运行态：

```bash
npm run dev
```

只重建一个应用容器时，使用 `npm run dev:gateway` 或 `npm run dev:webchat`。
直接运行宿主 mock/file Gateway 或 Vite 仅用于隔离调试，对应
`npm run dev:gateway:host` 和 `npm run dev:webchat:host`。

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
docker compose --env-file .env -f docker/compose.yaml config --quiet
```

当前 golden eval 覆盖 47 个 case，验证 direct chat、config/tool visibility、auth/rate-limit surfaces、grounded file/browser answers、approval lifecycle、memory review、sensitive-memory handling、prompt-injection chaos、trace refresh、artifact catalog、model-call telemetry 和 eval history。

## 开源

SparkClaw 使用 [Apache License 2.0](../LICENSE)。欢迎通过 issues 和 pull requests 参与。

贡献前请阅读 [CONTRIBUTING.md](../CONTRIBUTING.md)。安全漏洞请按 [SECURITY.md](../SECURITY.md) 私下报告，不要在公开 issue 中发布利用细节。

npm workspace root 保持 `private`，用于避免误发布 package。仓库本身是开源的；runtime images 和未来 release artifacts 应该经过明确流程发布。

## DGX Spark 模型

当前本地模型路径先启动单 Fast 产品 profile：

```bash
scripts/serve_models_compose.sh single-fast
scripts/restart_runtime_compose.sh
```

`single-fast` 会停止遗留的 `sparkclaw-deep` 容器，并同时启动 Fast、embedding、guard 与 OCR。`scripts/restart_runtime_compose.sh` 随后使用单 Fast 与 OCR 环境重启 Gateway/WebChat，把两个逻辑 chat profiles 都映射到 `sparkclaw-fast`，启用文档 OCR adapter，并在 Gateway 未 ready 时失败退出。

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
