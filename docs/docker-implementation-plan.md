# SparkClaw Docker 项目实施计划

版本：v0.1
日期：2026-05-22
依据：`README.md`、`spark_claw_readme_v_0_1.md`、`SparkClaw_Project_Development_Document_v0.1.md`

## 1. 目标

本实施计划用于把 SparkClaw 从现有设计文档推进到可交付、可部署、可验证的工程项目。环境形态以 Docker 为主，优先满足产品侧统一部署、统一配置、统一升级、统一排障的需要。

核心目标：

1. 以 Docker Compose 作为 MVP 和单机交付主路径。
2. 将 Gateway、WebChat、Agent Runtime、ToolHub、Memory、Evaluator、模型服务拆分为可独立构建和替换的服务。
3. 所有服务默认绑定本机或内网安全范围，保持 local-first 和 approval-first 的产品原则。
4. 用 profiles 支持最小运行、完整本地模型运行、开发调试、评测压测等部署形态。
5. 建立从开发、测试、镜像构建、配置模板、数据卷、日志、健康检查到发布包的闭环。

## 2. 设计约束

### 2.1 产品约束

- 默认单用户、单 owner、单本地 Gateway。
- 默认不开放公网 Gateway。
- 高风险动作必须进入 approval queue。
- 文件、邮件、日程、浏览器、shell、memory 等工具必须声明风险等级、审计策略和 sandbox 要求。
- 外部内容必须按 untrusted content 处理，不能绕过 tool policy。

### 2.2 部署约束

- 主要部署方式为 Docker Compose。
- 模型服务采用 OpenAI-compatible API，对上层服务隐藏 vLLM/SGLang/Ollama 等实现差异。
- sandbox backend 默认使用 Docker。
- 模型权重、向量库、SQLite/Postgres 数据、trace、日志、配置文件必须通过 volume 管理。
- 默认端口沿用现有文档：

| 服务 | 端口 | 默认暴露 |
|---|---:|---|
| Gateway API / WebSocket | 18789 | 127.0.0.1 |
| WebChat | 18790 | 127.0.0.1 |
| sparkclaw-fast | 8001 | 127.0.0.1 |
| sparkclaw-deep | 8002 | 127.0.0.1 |
| Memory API | 18810 | 127.0.0.1 |
| Eval API / report | 18820 | 127.0.0.1 |

## 3. 目标部署形态

### 3.1 Compose profiles

| Profile | 用途 | 包含服务 |
|---|---|---|
| `minimal` | 产品最小可用验证 | gateway、agent-runtime、model-router、toolhub、webchat、memory-lite |
| `models-local` | DGX Spark 本地模型完整体验 | minimal + sparkclaw-fast + sparkclaw-deep + embedding + reranker |
| `dev` | 本地开发调试 | minimal + hot reload + mock model + debug logs |
| `eval` | golden tasks 和回归测试 | minimal/models-local + evaluator + trace store |
| `compat` | 兼容外部模型服务 | minimal + external model endpoint config |

### 3.2 服务分组

```text
docker/
  compose.yaml
  compose.dev.yaml
  compose.eval.yaml
  env/
    sparkclaw.example.env
  images/
    gateway.Dockerfile
    agent-runtime.Dockerfile
    webchat.Dockerfile
    model-vllm.Dockerfile

configs/
  sparkclaw.default.json
  model.profiles.json
  tools.policy.json
  sandbox.policy.json

data/
  models/
  memory/
  traces/
  logs/
  workspaces/
```

### 3.3 网络与 volume

| 类别 | 建议 |
|---|---|
| 网络 | `sparkclaw_internal`，默认不向公网暴露内部服务 |
| 配置 | `./configs:/app/configs:ro` |
| 模型 | `./data/models:/models` |
| 记忆 | `./data/memory:/var/lib/sparkclaw/memory` |
| trace | `./data/traces:/var/lib/sparkclaw/traces` |
| 日志 | `./data/logs:/var/log/sparkclaw` |
| 用户 workspace | 显式 allowlist 挂载，不默认挂载全盘 |

## 4. 推荐仓库落地结构

按照现有文档中的仓库结构推进，并补充 Docker 相关目录：

```text
sparkclaw/
  apps/
    webchat/
    desktop/
    cli/
  services/
    gateway/
    agent-runtime/
    model-router/
    toolhub/
    memory/
    safety/
    evaluator/
  packages/
    protocol/
    tool-schema/
    policy-engine/
    logger/
    common/
  tools/
    filesystem/
    email/
    calendar/
    browser/
    shell/
    code/
    notification/
  skills/
  configs/
  docker/
  scripts/
  benchmarks/
  eval/
  docs/
```

## 5. 阶段实施计划

### Phase 0：工程骨架与 Docker 基线

目标：建立可运行、可构建、可配置的工程基础。

交付物：

- 仓库目录结构。
- `docker/compose.yaml`、`docker/compose.dev.yaml`。
- `.env.example` 和配置模板。
- Gateway、Agent Runtime、WebChat、Memory 的占位服务。
- 健康检查接口：`/healthz`、`/readyz`。
- 本地 mock model endpoint，用于无 GPU 开发。

验收标准：

- `docker compose --profile minimal up` 可以启动最小系统。
- WebChat 可访问 Gateway。
- Gateway 可调用 mock model 并返回响应。
- 所有服务具备健康检查和结构化日志。

### Phase 1：模型服务与路由

目标：跑通 fast/deep 双模型接口，并让上层只依赖统一 model router。

交付物：

- `model.profiles.json`。
- `sparkclaw-fast` 和 `sparkclaw-deep` 的 vLLM/SGLang Docker 启动模板。
- `models-local` profile。
- model router 手动选择模型和自动 `choose_model(task)`。
- 64K / 128K context 基线报告。

验收标准：

- `docker compose --profile models-local up` 可启动模型服务。
- 上层通过 OpenAI-compatible API 调用 fast/deep。
- 高风险、代码、失败修复任务能路由到 deep。
- 记录 TTFT、tokens/s、总延迟、内存占用。

### Phase 2：Core Runtime 与 Approval

目标：完成 SparkClaw 最小 agent loop。

交付物：

- Gateway session、event stream、audit log。
- Agent Runtime 工具循环：plan、tool call、observation、repair、final。
- ToolDefinition、JSON Schema validator。
- Policy Engine 和 approval queue。
- WebChat 中的任务时间线和 approval inbox。

验收标准：

- read-only 工具可自动执行。
- dangerous 工具不会自动执行，必须进入 approval queue。
- 工具 JSON 校验失败可进入 repair loop。
- 每次模型调用、工具调用、approval 都可追踪。

### Phase 3：MVP 工具与 Docker Sandbox

目标：实现第一批可靠工具，并用 Docker 隔离可变更动作。

交付物：

- `files.search`、`files.read`、`files.write_draft`。
- `memory.search`、`memory.write_candidate`。
- `browser.read`。
- `shell.exec_sandboxed`。
- `code.apply_patch`。
- `notify.ask_approval`。
- `sandbox.policy.json`。
- sandbox runner 镜像和 workspace allowlist。

验收标准：

- 文件读写限制在允许 workspace 内。
- shell 默认在 Docker sandbox 内运行，网络默认关闭。
- 任何删除、外发、表单提交、host shell 不可自动执行。
- prompt injection golden tasks 不出现 critical failure。

### Phase 4：Memory、RAG 与本地数据持久化

目标：完成个人记忆和本地知识库的可用闭环。

交付物：

- Memory Service。
- SQLite/Postgres + 向量库 profile。
- embedding 和 reranker 服务配置。
- profile、episodic、semantic、procedural memory 存储模型。
- memory editor 和 candidate_then_confirm 流程。

验收标准：

- 本地文档可索引、检索、rerank、引用。
- 敏感记忆默认不直接写入。
- 用户可查看、编辑、删除记忆。
- 数据 volume 可备份、迁移、恢复。

### Phase 5：Eval、Trace 与发布包

目标：建立产品侧可回归、可排障、可发行的交付体系。

交付物：

- golden tasks。
- tool chaos tests。
- routing evaluation。
- MTP A/B 测试脚本。
- trace collection 和 failure archive。
- release compose bundle。
- 部署手册、升级手册、排障手册。

验收标准：

- Tool JSON validity >= 99%。
- Dangerous action auto-execution = 0。
- Prompt injection critical failure = 0。
- `docker compose --profile eval run evaluator` 可生成报告。
- 产品侧拿到 release 包后可按文档完成部署。

### Phase 6：DGX Spark 优化发行

目标：沉淀 DGX Spark 生产建议配置和兼容发行线。

交付物：

- DGX Spark optimized profile。
- fast/deep 双模型 serialized active generation 策略。
- daily/work/deep single 上下文 profiles。
- llama.cpp / Ollama / GGUF compatibility profile。
- 一键诊断脚本。

验收标准：

- 默认配置可在 DGX Spark 上稳定运行。
- 可切换 single-fast、single-deep、dual-resident、external-model。
- 发布包包含版本、配置迁移、数据迁移和回滚说明。

## 6. Docker Compose 交付原则

### 6.1 配置优先

所有环境差异通过配置和 env 控制：

- 模型 endpoint。
- 端口绑定。
- workspace allowlist。
- memory 后端。
- sandbox 网络策略。
- approval policy。
- 日志级别。

禁止把环境差异硬编码到业务服务中。

### 6.2 镜像分层

建议镜像分为：

| 镜像 | 内容 |
|---|---|
| `sparkclaw-base` | 基础 runtime、通用依赖、非业务工具 |
| `sparkclaw-gateway` | Gateway API、session、approval、audit |
| `sparkclaw-agent-runtime` | agent loop、model router client、tool orchestration |
| `sparkclaw-toolhub` | typed tools 和 policy binding |
| `sparkclaw-memory` | memory API、RAG、vector store client |
| `sparkclaw-webchat` | 前端静态资源或 Web 服务 |
| `sparkclaw-evaluator` | golden tasks、chaos tests、报告生成 |
| `sparkclaw-vllm` | 模型服务运行环境 |

### 6.3 健康检查

每个服务至少提供：

- `/healthz`：进程是否存活。
- `/readyz`：依赖是否可用。
- `/metrics`：后续可接入 Prometheus，MVP 可先留空或返回基础指标。

模型服务 ready 条件应包含：

- 模型 endpoint 可访问。
- `/v1/models` 返回目标 served model。
- 简短 completion/chat completion 可成功。

## 7. 产品侧部署流程

### 7.1 首次部署

```bash
cp docker/env/sparkclaw.example.env .env
docker compose -f docker/compose.yaml --profile minimal up -d
docker compose -f docker/compose.yaml ps
```

完整本地数据服务与本地模型 endpoint wiring：

```bash
docker compose -f docker/compose.yaml --profile models-local up -d
```

当前 `models-local` profile 启动 Postgres/pgvector、MinIO、Gateway、WebChat 和 sandbox-runner，并把 fast/deep/embedding/reranker endpoint 指向 Compose 网络内的预期服务名。实际 vLLM/SGLang 模型服务可先用 `scripts/serve_fast.sh`、`scripts/serve_deep.sh` 或外部 OpenAI-compatible endpoint 提供；后续发行包可把模型服务容器加入同一 profile。

### 7.2 升级

```bash
docker compose -f docker/compose.yaml pull
docker compose -f docker/compose.yaml up -d
docker compose -f docker/compose.yaml run --rm gateway sparkclaw migrate
```

升级要求：

- 配置文件版本化。
- 数据迁移可重复执行。
- 迁移前自动提示备份。
- 失败可回滚到上一镜像 tag。

### 7.3 备份与恢复

必须纳入备份：

- `configs/`
- `data/memory/`
- `data/traces/`
- `data/workspaces/` 中由 SparkClaw 创建的草稿和 staging 文件

不建议默认备份：

- 模型权重缓存。
- 临时 sandbox 容器。
- 可再生成的 eval report。

## 8. 配置清单

首批必须落地的配置文件：

| 文件 | 作用 |
|---|---|
| `configs/sparkclaw.default.json` | 总配置入口 |
| `configs/model.profiles.json` | fast/deep/embedding/reranker endpoint 与上下文策略 |
| `configs/tools.policy.json` | 工具风险等级、approval、deny list |
| `configs/sandbox.policy.json` | Docker sandbox、网络、workspace 挂载策略 |
| `configs/logging.json` | 日志级别、脱敏规则 |
| `configs/eval.profiles.json` | golden tasks 和模型评测配置 |

MVP 当前使用 `SPARKCLAW_STATE_BACKEND=file` 和 `SPARKCLAW_STATE_PATH=/var/lib/sparkclaw/memory/gateway-state.json` 持久化 Gateway 会话、消息、审批、工具调用、记忆候选和审计事件；也可以切换到 `SPARKCLAW_STATE_BACKEND=postgres`，由 Gateway 启动时应用 `migrations/0001_core.sql` 对应的核心 schema，并持久化 sessions、tool calls、approvals、documents、eval runs、artifact objects 等事实数据。

邮件和日程 MVP 当前使用 workspace 内 `.sparkclaw/mock/email_threads.json` 与 `.sparkclaw/mock/calendar_events.json` 作为本地 fixture 数据源，并把 approval-gated 的 mock 发送记录/创建日程记录写入 `.sparkclaw/mock/email_outbox.jsonl` 与 `.sparkclaw/mock/calendar_created_events.jsonl`。`search/read/draft/propose` 仍是低风险本地能力，`email.send` 与 `calendar.create` 必须进入 approval queue；真实账号连接应在后续 adapter 中保持同一工具契约，并继续沿用 approval-first 策略。

## 9. 安全默认值

默认策略：

- Gateway bind `127.0.0.1`。
- 内部服务只在 Docker internal network 通信。
- sandbox 网络默认 `none`。
- workspace 必须显式挂载。
- host shell 禁用。
- 邮件发送、日程创建、文件删除、表单提交必须 approval。
- 日志默认 redaction secrets。
- 外部网页、邮件、PDF、README、工具输出均标记为 untrusted。

## 10. 风险与缓解

| 风险 | 缓解 |
|---|---|
| DGX Spark 同时跑双模型资源紧张 | 默认 serialized active generation，提供 single-fast/single-deep profile |
| Docker GPU 配置差异 | 提供 NVIDIA Container Toolkit 检查脚本和诊断命令 |
| 模型权重过大导致部署慢 | 支持预下载模型 volume，文档明确缓存路径 |
| tool calling 不稳定 | schema validator、repair loop、golden eval、失败 trace |
| sandbox 权限过宽 | allowlist workspace、network none、只读配置挂载 |
| 产品侧升级破坏数据 | volume 备份、配置版本、迁移脚本、镜像 tag 回滚 |
| 端口冲突 | 通过 `.env` 配置端口，默认沿用文档端口 |

## 11. 第一周执行清单

```text
[x] 建立目录：apps、services、packages、configs、docker、scripts、eval、benchmarks
[x] 新增 docker/compose.yaml，支持 minimal profile
[x] 新增 docker/compose.dev.yaml，支持开发热更新或 mock model
[x] 新增 docker/env/sparkclaw.example.env
[x] 新增 configs/sparkclaw.default.json
[x] 新增 configs/model.profiles.json
[x] 新增 configs/tools.policy.json
[x] 新增 configs/sandbox.policy.json
[x] 实现 gateway /healthz、/readyz、/chat 接口
[x] 实现 agent-runtime mock agent loop
[x] 实现 webchat 工作台页面
[x] 实现 model-router 对 mock/OpenAI-compatible endpoint 的调用
[x] 实现 ToolDefinition 类型和 JSON Schema validator
[x] 实现 files.search、files.read 两个 read-only 工具
[x] 实现 notify.ask_approval 流程
[x] 添加 eval/golden/files.yaml（当前 58 个可执行 golden cases）
[x] 添加 scripts/doctor.sh，检查 Docker、端口、volume 与关键配置
```

## 12. MVP 完成定义

MVP 视为完成时，应满足：

1. 产品侧可用 Docker Compose 一条命令启动最小系统。
2. WebChat 能完成文件读取、文件摘要、记忆检索、approval 展示。
3. 本地模型服务可通过 profile 切换启用。
4. 所有危险工具均不会自动执行。
5. sandbox 以 Docker 运行，默认无网络，workspace allowlist 生效。
6. eval profile 可运行 golden tasks 并输出报告。
7. 配置、数据、日志、trace 均有明确 volume 和备份方式。
8. 部署文档覆盖首次部署、升级、回滚、排障。
