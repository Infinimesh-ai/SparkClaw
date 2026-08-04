# SparkClaw 部署

> 语言： [English](../../docs/deployment.md) | 简体中文

本文档是当前的本地开发、Docker Compose 和 DGX Spark 模型服务部署指南，替代旧的 Docker implementation plan 和 DGX handoff notes。

## 前置条件

- Ubuntu 24.04 或其他带 Docker / Docker Compose 的 Linux host。
- DGX Spark 模型服务需要 NVIDIA container runtime。
- host-side build 使用 Node.js 26 和 npm 11；版本入口为仓库 `.nvmrc`。
- host-side Gateway 开发使用 Go 1.25。
- 模型下载需要把 Hugging Face token 放在本地 `.env` 中。不要提交 `.env`。

创建本地环境文件：

```bash
cp docker/env/sparkclaw.example.env .env
```

在 `.env` 中设置 `HF_TOKEN` 或 `HUGGING_FACE_HUB_TOKEN`。该 token 只传给 model-serving containers。

## Compose Profiles

| Profile | 用途 |
|---|---|
| `minimal` | Gateway + WebChat，mock model routing。推荐首次运行。 |
| `dev` | 开发运行形态。 |
| `eval` | Gateway 加 evaluator 和 data services。 |
| `compat` | Gateway 连接外部 OpenAI-compatible endpoints。 |
| `models-local` | PostgreSQL 18/pgvector、MinIO、sandbox-runner、Gateway、WebChat 和可选 vLLM lanes。 |

WebChat 的 host port `18790` 默认绑定 `0.0.0.0`，允许局域网访问。Gateway、模型、
状态服务和 sandbox runner 仍绑定 localhost。Containers 通过私有
`sparkclaw_internal` network 通信。

## Minimal Local Runtime

启动 mock-mode control plane：

```bash
sudo -n env SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal \
  docker compose --env-file .env -f docker/compose.yaml --profile minimal up -d --build
```

检查状态：

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile minimal ps
curl -fsS http://127.0.0.1:18789/readyz
bash scripts/doctor.sh
```

本机打开 WebChat：[http://127.0.0.1:18790](http://127.0.0.1:18790)；同一局域网的
其他设备使用 `http://<主机局域网-IP>:18790`。

对 Dockerized Gateway 运行 golden eval：

```bash
BROWSER_FIXTURE_URL=http://host.docker.internal:18791 \
BROWSER_FIXTURE_BIND=0.0.0.0 \
bash scripts/run-eval.sh
```

`SPARKCLAW_BROWSER_READ_ALLOW_HOSTS` 是刻意显式的设置。它允许 eval fixtures 工作，同时保持 `browser.read` 默认拒绝 private hosts。

## Host Development Runtime

已验证 DGX Spark 主机的标准开发运行态是容器化
external-model/PostgreSQL topology：

```bash
npm run dev
```

使用 `npm run dev:gateway` 或 `npm run dev:webchat` 可以只重建一个应用容器，
且不会切回 mock/file mode。

仅在隔离的宿主进程调试中，才在两个 terminal 分别运行 mock/file Gateway
和 Vite server：

```bash
npm run dev:gateway:host
npm run dev:webchat:host
```

Host WebChat dev server 监听 `0.0.0.0:18790`，并把 API 请求代理到仅监听
loopback 的 Gateway。受保护的宿主进程运行态应把 `SPARKCLAW_API_TOKEN`
和 `VITE_SPARKCLAW_API_TOKEN` 设为相同值。

## ISCP Bridge 进程

JingSi App 集成以独立 host 进程运行，从而使用 GB10 的操作系统 keyring，并且只访问 loopback
Gateway。启用 Gateway token 认证，安装设备身份和 Cloud 签发的 enrollment bundle 后运行：

```bash
cd services/gateway
mkdir -p ../../bin
go build -o ../../bin/sparkclaw-iscp-bridge ./cmd/iscp-bridge
../../bin/sparkclaw-iscp-bridge run -config ../../configs/iscp-bridge.example.json
```

相同 Gateway bearer 值或专用 paired-client token 必须写入 mode `0600` 的 Bridge
`gateway.token` 文件。enrollment bundle 同样为 `0600`；生产 Ed25519 设备身份密钥保留在系统
keyring。应由 service manager 执行失败重启，但显式设备撤销错误发生后，在安装新 enrollment
bundle 前不要重启。

注册、schema、credential rotation、mock mode 和完整安全边界见
[ISCP Bridge](iscp-bridge.md)。

## State Backends

默认 file state：

```text
data/memory/gateway-state.json
```

常用选项：

```bash
SPARKCLAW_STATE_BACKEND=memory
SPARKCLAW_STATE_PATH=/path/to/state.json
SPARKCLAW_STATE_ENCRYPT_AT_REST=true
SPARKCLAW_STATE_ENCRYPTION_KEY_FILE=/path/to/key
```

Postgres-backed state：

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d postgres

SPARKCLAW_STATE_BACKEND=postgres \
SPARKCLAW_STATE_DSN='postgres://sparkclaw:sparkclaw@127.0.0.1:15432/sparkclaw?sslmode=disable' \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

Gateway 启动时会应用当前核心 schema。项目标准 data service image 仍保留 PostgreSQL 18 with pgvector，但 Workspace Knowledge/RAG 暂缓期间，Gateway 不再创建或查询 Document Chunk/Vector Schema。

PostgreSQL 18 会把 cluster 存放在带主版本号的子目录中，因此 Compose 将
带版本号的 `sparkclaw_pg18` volume 挂载到 `/var/lib/postgresql`。使用旧
`/var/lib/postgresql/data` 挂载创建的 PostgreSQL 17 `sparkclaw_pg`
volume，必须先备份，再通过 `pg_dump`/`pg_restore` 迁移。不要把旧 data
directory 直接挂到 PostgreSQL 18，也不要通过删除旧卷来强制重新初始化。

## Artifact Storage

默认 artifact backend 是 `data/artifacts/{bucket}/...` 下的 filesystem object storage。S3-compatible storage 设置：

```bash
SPARKCLAW_ARTIFACT_BACKEND=s3
SPARKCLAW_S3_ENDPOINT=http://127.0.0.1:9000
SPARKCLAW_S3_ACCESS_KEY=sparkclaw
SPARKCLAW_S3_SECRET_KEY=sparkclaw-local
```

Compose 提供 MinIO：

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d minio minio-init
```

Artifacts 包括 tool observations、browser snapshots、generated documents/media、
memory exports、patch rollback files 和 eval failure archives。

## Sandbox Runner

Host binary 运行时，Gateway 可使用 `SPARKCLAW_SANDBOX_BACKEND=local-docker`。

Compose 使用独立 sandbox runner：

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile minimal up -d sandbox-runner
```

Compose 外的 standalone runner：

```bash
sparkclaw-sandbox-runner
SPARKCLAW_SANDBOX_BACKEND=http \
SPARKCLAW_SANDBOX_RUNNER_URL=http://127.0.0.1:18889 \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

如果 runner 访问 host Docker socket，且 host 与 container 看到的 workspace path 不同，需要设置 `SPARKCLAW_SANDBOX_HOST_WORKSPACE_ROOT` 和 `SPARKCLAW_SANDBOX_CONTAINER_WORKSPACE_ROOT`。


## DGX Spark Data Services

启动 durable state、artifacts、sandbox、Gateway 和 WebChat：

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d \
  postgres minio minio-init sandbox-runner gateway webchat
```

模型端点 healthy 后，用 external mode 重建 Gateway 与 WebChat：

```bash
scripts/restart_runtime_compose.sh
```

模型运行态应使用该脚本，而不是直接执行
`docker compose up --force-recreate gateway webchat`。脚本在 `.env` 后加载
`docker/env/sparkclaw.single-fast.env`，避免 Compose 退回
`docker/env/sparkclaw.example.env` 的 `mock/file` 默认值；同一个文件还会把逻辑
Deep profile 映射到 Fast endpoint。重启后脚本检查 `/readyz`，只有 Gateway 报告
`model_mode=external` 且 `state_backend=postgres` 时才成功退出。需要其他 runtime
profile 时应显式设置 `SPARKCLAW_RUNTIME_ENV`。

当主机存在可解析的 X11/XWayland display 时，脚本还会叠加
`docker/compose.visible-browser.yaml` overlay，使登录 handoff 可以在 owner 桌面
打开 visible Chromium。headless 主机上则以不带 overlay 的相同 stack 启动；hidden
浏览器自动化仍然可用，基础 compose 文件不授予 Gateway 任何 host display 访问权限。

## DGX Spark Model Services

Host-side vLLM scripts：

```bash
scripts/serve_fast.sh
scripts/serve_deep.sh
```

Compose vLLM services：

```bash
scripts/serve_models_compose.sh single-fast
scripts/serve_models_compose.sh fast
scripts/serve_models_compose.sh deep
scripts/serve_models_compose.sh dual-light
scripts/serve_models_compose.sh dual-light-asr
scripts/serve_models_compose.sh dual-light-chat
scripts/serve_models_compose.sh embedding
scripts/serve_models_compose.sh guard
scripts/serve_models_compose.sh asr
scripts/serve_models_compose.sh all
scripts/serve_models_compose.sh all-with-asr
```

不传参数时，`serve_models_compose.sh` 也会选择 `single-fast`。这是当前产品启动路径：
它会停止此前运行的 Deep 容器，并使用 `docker/env/sparkclaw.single-fast.env`
启动 Fast、embedding 和 guard。Deep 与 dual-light 命令仅作为显式测试/benchmark
入口。命令会等待所有选中服务进入 healthy。Guard 必须先成功完成一次有界的真实
`/chat/completions` 请求才会变为 healthy；一次性预热完成后，周期健康检查改用轻量的
模型列表 endpoint。

默认 endpoints：

| Lane | Served name | Endpoint |
|---|---|---|
| fast | `sparkclaw-fast` | `http://127.0.0.1:8001/v1` |
| deep | `sparkclaw-deep` | `http://127.0.0.1:8002/v1` |
| embedding | `sparkclaw-embedding` | `http://127.0.0.1:8003/v1` |
| guard | `Qwen/Qwen3Guard-Gen-0.6B` | `http://127.0.0.1:8005/v1` |
| asr | `sparkclaw-asr` | `http://127.0.0.1:8006` |
| 可选 OCR | `sparkclaw-ocr` | `http://127.0.0.1:8007/v1` |

检查 endpoints：

```bash
curl -fsS http://127.0.0.1:8001/v1/models
curl -fsS http://127.0.0.1:8003/v1/models
curl -fsS http://127.0.0.1:8005/v1/models
curl -fsS http://127.0.0.1:8006/v1/models
curl -fsS http://127.0.0.1:8007/v1/models
```

只有显式执行 `deep`、`dual-light` 或 `all` 启动后才会使用 `8002`；当前单 Fast
ready 检查不包含该端口。

重要环境变量：

- `SPARKCLAW_VLLM_IMAGE`
- `SPARKCLAW_FAST_MODEL_ID`, `SPARKCLAW_FAST_MODEL`, `SPARKCLAW_FAST_MAX_MODEL_LEN`, `SPARKCLAW_FAST_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_FAST_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_FAST_MAX_NUM_SEQS`, `SPARKCLAW_FAST_SPECULATIVE_CONFIG`
- `SPARKCLAW_DEEP_MODEL_ID`, `SPARKCLAW_DEEP_MODEL`, `SPARKCLAW_DEEP_MAX_MODEL_LEN`, `SPARKCLAW_DEEP_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_DEEP_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_DEEP_MAX_NUM_SEQS`, `SPARKCLAW_DEEP_SPECULATIVE_CONFIG`
- `SPARKCLAW_EMBEDDING_MODEL_ID`, `SPARKCLAW_EMBEDDING_MODEL`, `SPARKCLAW_EMBEDDING_MAX_MODEL_LEN`, `SPARKCLAW_EMBEDDING_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_EMBEDDING_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_EMBEDDING_MAX_NUM_SEQS`
- `SPARKCLAW_GUARD_MODEL_ID`, `SPARKCLAW_GUARD_MODEL`, `SPARKCLAW_GUARD_SERVED_NAME`, `SPARKCLAW_GUARD_MAX_TOKENS`, `SPARKCLAW_GUARD_CONTEXT_TOKENS`, `SPARKCLAW_GUARD_MAX_MODEL_LEN`, `SPARKCLAW_GUARD_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_GUARD_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_GUARD_MAX_NUM_SEQS`
- `SPARKCLAW_ASR_MODEL_ID`, `SPARKCLAW_ASR_SERVED_NAME`, `SPARKCLAW_ASR_MAX_MODEL_LEN`, `SPARKCLAW_ASR_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_ASR_MAX_NUM_SEQS`, `SPARKCLAW_ASR_DTYPE`
- `SPARKCLAW_SPEECH_ENABLED`, `SPARKCLAW_SPEECH_BASE_URL`, `SPARKCLAW_SPEECH_ALLOWED_HOSTS`, `SPARKCLAW_SPEECH_MODEL`, `SPARKCLAW_SPEECH_TIMEOUT_SECONDS`, `SPARKCLAW_SPEECH_MAX_AUDIO_SECONDS`, `SPARKCLAW_SPEECH_MAX_UPLOAD_BYTES`
- `SPARKCLAW_OCR_ENABLED`, `SPARKCLAW_OCR_BASE_URL`, `SPARKCLAW_OCR_ALLOWED_HOSTS`, `SPARKCLAW_OCR_MODEL`, `SPARKCLAW_OCR_TIMEOUT_SECONDS`, `SPARKCLAW_OCR_MAX_UPLOAD_BYTES`, `SPARKCLAW_OCR_MAX_OUTPUT_BYTES`, `SPARKCLAW_OCR_MAX_TOKENS`, `SPARKCLAW_OCR_MAX_CONCURRENCY`, `SPARKCLAW_OCR_MAX_PENDING`
- `SPARKCLAW_OCR_IMAGE`, `SPARKCLAW_OCR_MODEL_ID`, `SPARKCLAW_OCR_SERVED_NAME`, `SPARKCLAW_OCR_MAX_MODEL_LEN`, `SPARKCLAW_OCR_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_OCR_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_OCR_MAX_NUM_SEQS`
- `SPARKCLAW_MODEL_DISABLE_THINKING`
- `SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS`
- `HF_TOKEN` 或 `HUGGING_FACE_HUB_TOKEN`

`*_MODEL_ID` 是 serving container 加载的 Hugging Face checkpoint；`*_MODEL` 是 Gateway 发送的 OpenAI-compatible served name。

### 专用 Qwen3Guard

guard lane 使用公开的生成式 checkpoint `Qwen/Qwen3Guard-Gen-0.6B`；
`Qwen/Qwen3Guard-0.6B` 不是有效的公开 checkpoint ID。只启动 guard endpoint：

```bash
SPARKCLAW_MODEL_LOADING_PROFILE=single-fast scripts/serve_models_compose.sh guard
curl -fsS http://127.0.0.1:8005/v1/models
```

单台 GB10 的 `single-fast` profile 把 guard 限制为 16K context、2 GiB KV cache、
单序列和 eager execution。Qwen3Guard 返回原生
`Safety: Safe|Unsafe|Controversial` 与 `Categories:` 格式；Gateway 分别映射为
`allow`、`block` 和 `review`。SparkClaw 当前没有人工安全复核队列，因此 `review`
和 `block` 都会在 routing 或 tool execution 前终止 run。外部 endpoint 不可用时，
Gateway 会记录 `mock=true` 并使用本地 heuristic fallback。Compose 最多允许首次真实
推理 readiness 探针运行 110 秒，并且只有探针生成非空 completion 后才把 Guard
容器标记为 healthy。

### OvisOCR2 文档 OCR

可选 document OCR adapter 通过 OpenAI-compatible vLLM chat-completions endpoint 使用
[`ATH-MaaS/OvisOCR2`](https://huggingface.co/ATH-MaaS/OvisOCR2)，把 page image 按自然阅读
顺序解析为 Markdown，并保留公式和表格。Fast 仍负责 visual semantic 和 Workflow 推理；
OCR 输出是不可信文档证据，不能选择 model lane 或授权 edit。

overlay 固定 vLLM `0.22.1`，只在 loopback 暴露 `8007`，使用显式 2 GiB KV cache budget，
并复用 Hugging Face cache。首次组合加载前先停止常驻模型服务以释放 unified memory，
再启动当前单 Fast profile 并带 OCR：

```bash
scripts/serve_models_compose.sh single-fast-with-ocr
curl -fsS http://127.0.0.1:8007/v1/models
```

启动启用 OCR adapter 的 Gateway 和 WebChat：

```bash
docker compose \
  --env-file docker/env/sparkclaw.single-fast.env \
  --env-file docker/env/sparkclaw.ocr.env \
  -f docker/compose.yaml \
  -f docker/compose.dual-light.yaml \
  -f docker/compose.ocr.yaml \
  --profile models-local up -d gateway webchat
```

host 侧 doctor 保留 Gateway 使用的 Compose service URL，只覆盖检查目标：

```bash
set -a
. docker/env/sparkclaw.ocr.env
set +a
SPARKCLAW_OCR_BASE_URL=http://127.0.0.1:8007/v1 scripts/doctor.sh
```

OCR 默认关闭。选中的 Office/PDF 图片会得到有界 OCR Markdown；扫描 PDF 页自动调用 OCR。
页面栅格化限制为八页、单页 4 MiB、每次 PDF 读取总计 16 MiB。adapter 关闭、busy、timeout、
返回 malformed 或 incomplete 时都会明确报告 partial evidence。GB10 上已经按上述“先停止、
再一起加载”的流程验证组合启动；直接向已常驻栈增加 OCR 会在 CUDA 初始化阶段失败。必须保留
显式 2 GiB KV cache，仅依赖 utilization 分配会得到负数的可用 cache 计算结果。一次并发图片
与扫描 PDF 冒烟调用已成功，但它不是 OCR 质量基线，仍需覆盖更多真实文档的质量测量。

### 从魔塔加载 Qwen3-ASR

SparkClaw speech 使用 OpenAI-compatible transcription endpoint。Qwen3-ASR 支持 vLLM serving 和 OpenAI transcription API，[官方 Qwen3-ASR README](https://github.com/QwenLM/Qwen3-ASR) 也建议中国大陆用户通过 ModelScope 下载。单台 GB10 同时运行已验证的 `dual-light` 常驻 profile 时，先用 `Qwen/Qwen3-ASR-0.6B`；只有在 fast、deep、embedding 都常驻后重新测过内存和延迟，再切到 `Qwen/Qwen3-ASR-1.7B`。

把 ASR checkpoint 下载到共享模型缓存：

```bash
python3 -m pip install -U modelscope
mkdir -p data/models/modelscope/Qwen3-ASR-0.6B
modelscope download --model Qwen/Qwen3-ASR-0.6B --local_dir data/models/modelscope/Qwen3-ASR-0.6B
```

ASR compose override 会基于本地 vLLM image 构建一个轻量派生镜像，只补音频依赖，不改文本模型主镜像：

- Compose：`docker/compose.asr.yaml`
- 环境变量：`docker/env/sparkclaw.asr.env`
- 镜像配方：`docker/images/asr-vllm.Dockerfile`
- 默认 served model：`sparkclaw-asr`
- 容器内默认模型路径：`/models/modelscope/Qwen3-ASR-0.6B`

只启动 ASR：

```bash
scripts/serve_models_compose.sh asr
```

启动已验证的常驻 profile 并带 ASR：

```bash
scripts/serve_models_compose.sh dual-light-asr
```

启动启用 speech 的 Gateway 和 WebChat：

```bash
docker compose \
  --env-file docker/env/sparkclaw.dual-light.env \
  --env-file docker/env/sparkclaw.asr.env \
  -f docker/compose.yaml \
  -f docker/compose.dual-light.yaml \
  -f docker/compose.asr.yaml \
  --profile models-local up -d gateway webchat
```

从 host 检查 ASR endpoint：

```bash
curl -fsS http://127.0.0.1:8006/health
curl -fsS http://127.0.0.1:8006/v1/models
curl -fsS http://127.0.0.1:8006/v1/audio/transcriptions \
  -F model=sparkclaw-asr \
  -F response_format=json \
  -F file=@/path/to/sample.wav
```

host 侧运行 doctor 时，`docker/env/sparkclaw.asr.env` 中的容器 URL 留给 Gateway 使用，检查命令里覆盖成 loopback：

```bash
set -a
. docker/env/sparkclaw.asr.env
set +a
SPARKCLAW_SPEECH_BASE_URL=http://127.0.0.1:8006 scripts/doctor.sh
```

2026-05-24 DGX Spark 验证说明：

- NVIDIA GB10 和 driver `580.159.03` 在 host 和 CUDA containers 中可见。
- `vllm/vllm-openai:cu130-nightly` 可在 arm64 上运行。
- `Qwen/Qwen3.6-27B-FP8`、`Qwen/Qwen3.6-35B-A3B-FP8`、`Qwen/Qwen3-Embedding-0.6B` 和 `Qwen/Qwen3Guard-Gen-0.6B` 已验证。
- full-context fast+deep dual residency 在两个 chat lanes 都为 128K context 且启用 MTP 时未能同时容纳。可一次运行一个 128K/MTP chat lane，把两个 Gateway profiles 都路由到已加载 lane，或降低 context/MTP 后重新测量。

当前单 Fast 产品启动：

```bash
scripts/serve_models_compose.sh single-fast
scripts/restart_runtime_compose.sh
```

该命令应用 `docker/env/sparkclaw.single-fast.env`，并复用
`docker/compose.dual-light.yaml` 中有界的 Fast 与辅助模型设置；只启动 Fast、
embedding 和 guard，Gateway 的两个逻辑 chat profiles 都发送到 `sparkclaw-fast`。

历史轻量双常驻实验：

```bash
scripts/serve_models_compose.sh dual-light
python3 scripts/record_model_loading.py --profile dual-light-v1
```

`dual-light` 快捷方式会应用 `docker/env/sparkclaw.dual-light.env` 和 `docker/compose.dual-light.yaml`：fast 32K + 8G KV cache，deep 64K + 12G KV cache，embedding 8K + 2G KV cache，guard 16K + 2G KV cache。MTP 关闭，并发序列数保持较低。运行 external mode Gateway 前先启动这个完整 profile。

只有在刻意测量不带辅助端点的 chat lanes 时才使用 `dual-light-chat`。

运行 repeatable endpoint benchmark：

```bash
SPARKCLAW_FAST_BASE_URL=http://127.0.0.1:8001/v1 \
SPARKCLAW_FAST_MODEL=sparkclaw-fast \
SPARKCLAW_DEEP_BASE_URL=http://127.0.0.1:8002/v1 \
SPARKCLAW_DEEP_MODEL=sparkclaw-deep \
SPARKCLAW_MODEL_DISABLE_THINKING=true \
python3 scripts/benchmark_models.py --append-markdown benchmarks/model_baseline.md
```

运行 real-model golden eval：

```bash
SPARKCLAW_EXPECT_REAL_MODELS=1 \
SPARKCLAW_MODEL_MODE=external \
BROWSER_FIXTURE_URL=http://host.docker.internal:18791 \
BROWSER_FIXTURE_BIND=0.0.0.0 \
bash scripts/run-eval.sh
```

历史已验证 real-model run 完成 58 个 golden cases。当前活动矩阵为 43 个 case，模型栈发生变化后应重新运行。benchmark rows 和运行说明见 [model_baseline.md](../benchmarks/model_baseline.md)。

## Backup And Restore

需要备份的路径或 volumes：

- `.env` secret template values，存储在 git 外
- `data/memory`
- `data/traces`
- `data/artifacts`
- `data/workspaces`
- Postgres volume `sparkclaw_pg18`
- MinIO volume `sparkclaw_minio`
- 如果需要复用模型缓存，则备份 `data/models`

Postgres：

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml exec postgres \
  pg_dump -U sparkclaw sparkclaw > sparkclaw.sql
```

filesystem state 最好在 Gateway 停止后复制。

## Upgrade Flow

1. 保存或导出重要 state。
2. 拉取或应用代码变更。
3. rebuild images：

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile minimal build
```

4. 启动目标 profile。
5. 运行 `bash scripts/doctor.sh`。
6. 运行 mock golden eval。
7. DGX Spark 模型变更需要运行 endpoint checks，并追加新的 benchmark section。

### 2026-07-30 之后升级需要注意的行为变化

- 可见浏览器登录接管现在必须叠加 `docker/compose.visible-browser.yaml`
  overlay；base compose 不再暴露宿主 X11 socket。
  `scripts/restart_runtime_compose.sh` 在解析到显示器时会自动叠加。
- Telegram 和微信现在在 typed config、Compose 与示例环境中都出厂默认关闭；账号设置前需从
  WebChat 显式开启渠道。`SPARKCLAW_TELEGRAM_ENABLED` 和
  `SPARKCLAW_WEIXIN_NOTIFICATION_ENABLED` 只在 owner 尚无持久化选择时提供初始值；binding
  和 credential 绝不会自动开启渠道。
- 过渡期 skills registry 已删除，包括 `GET /api/skills` 与 `skills`
  配置段；workflow 是唯一执行路径。
- guard 回复解析不出有效裁决时按不阻断的 `unknown` 处理，并记录
  `guard.verdict_unknown` audit event；显式 `review`/`block` 仍会阻断。
  无配置文件启动现在默认关闭模型思考，与出厂配置一致。
- Runtime 预算键拆分为 `workflow_stage_max_*` 与 `workflow_run_max_*`
  （旧的 `workflow_step_max_*`/`react_max_*` 仍可回退映射；见
  [Workflow execution](workflow-execution.md)）。


## Secure Defaults

- 只把 WebChat 暴露在 `0.0.0.0:18790`；Gateway 和其他服务端口仍绑定
  `127.0.0.1`。
- 在局域网或共享机器上开放 WebChat 前设置 `SPARKCLAW_API_TOKEN`。
- Gateway 仅在本地开发时允许无认证。
- dangerous 和 reversible tools 保持 approval-gated。
- shell execution 保持 sandboxed 且 network-disabled。
- browser/email/file observations 视为 untrusted。
- 保持 host 桌面对容器关闭：基础 compose 文件不挂载 X11 socket；
  `docker/compose.visible-browser.yaml` overlay 只应在需要 visible 登录 handoff
  的受信任单 owner 桌面 runtime 上使用。
- `.env`、model weights、state encryption keys 和下载数据不进入 git。
- 交付前扫描 diff 中的 token。

## Troubleshooting

| Symptom | Check |
|---|---|
| Docker permission denied | 使用 `sudo -n docker ...` 或将用户加入 Docker group。 |
| Golden eval browser step fails | Docker eval 启动 Gateway 时设置 `SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal`；host eval 使用 `127.0.0.1`。 |
| Model returns reasoning but no answer | 设置 `SPARKCLAW_MODEL_DISABLE_THINKING=true`。 |
| Postgres vector extension unavailable | SparkClaw fallback 到 JSON vectors 和 Gateway-side hybrid scoring。 |
| 128K fast+deep does not fit | 一次运行一个 chat lane，或降低 context/MTP 后重新 benchmark。 |
