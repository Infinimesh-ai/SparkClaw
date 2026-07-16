# SparkClaw 开发

> 语言： [English](../../docs/development.md) | 简体中文

本文档面向继续开发项目的 contributor。它替代旧的 initial roadmap 和 local implementation audit，记录当前实现状态、验证命令和扩展规则。

## 仓库地图

```text
apps/webchat/              React/Vite workbench
services/gateway/          Go API, Agent Runtime, Model Router, ToolHub, policy, state and traces
configs/                   Runtime, model, tool, sandbox, logging and eval configuration
docker/                    Compose file and service images
scripts/                   Doctor, eval, model serving and benchmark scripts
eval/golden/               Golden task definitions and fixtures
benchmarks/                DGX Spark model evidence
packages/                  Portable protocol, policy and tool-schema notes
skills/                    Runtime skill packages, intentionally evolving
docs/                      Current project documentation
zh-cn/                     Chinese documentation mirror
```

## 实现状态

MVP control plane 和 DGX Spark real-model closure 已完成。后续工作应作为 model optimization、product expansion 或 connector hardening 排期，而不是 MVP blocker。

| Area | Status | Main Evidence |
|---|---|---|
| Gateway control plane, sessions, messages, events, owner profile, client pairing and rate limits | Complete | Gateway tests, golden API checks |
| Agent Runtime, guard review, model routing, planning, repair and grounded answers | Complete | Agent tests, golden eval |
| Router-first 能力 Workflow | 浏览器搜索/自动化和文档信息/处理已迁移 | Catalog、精确 Registry/Dispatcher、固定工具暴露与四条端到端测试 |
| ToolHub contracts and MVP tools | Complete | ToolHub tests, `/api/tools`, golden checks |
| Approval-first reversible/dangerous actions | Complete | Approval tests, patch/delete/shell/memory golden cases |
| Audit log, traces, observation summaries and artifact catalog | Complete | Trace/artifact tests and golden checks |
| File、browser、memory、code 和 notify workflow | Complete | Unit tests plus 43-case eval |
| 邮件、日历和 Workspace Knowledge/RAG | 已暂缓；原型已移除 | [暂缓能力记录](deferred-email-calendar-knowledge.md) |
| Skills registry boundary | Complete | Registry tests and `/api/skills`; skills do not bypass policy |
| WebChat workbench | Complete | TypeScript/Vite build |
| Runtime config, model profiles, tool policy editor, secret redaction and metrics | Complete | Gateway tests and golden checks |
| Docker profiles and local deployment | Complete | Compose config, image builds, doctor script |
| DGX Spark fast/deep/embedding/reranker serving | Complete | `benchmarks/model_baseline.md` |
| Infinimesh Info `web.search` provider | Complete，opt-in | Contract/fault tests、redacted public config、credential-gated live smoke |
| WebChat 与 Gateway speech transcription | Complete，opt-in | Speech/Gateway tests、voice frontend tests、live ASR smoke evidence |
| 消息连接器 Registry 与 Telegram 多 Bot binding | Complete，opt-in | Provider-neutral registry、credential 隔离、binding、worker、media、reminder 与 WebChat tests |
| 消息控制、定时消息与结果投递 | Router-first 垂直切片已完成 | 持久化入口/返回上下文、Endpoint/Schedule Registry、有界 Timer Worker、唯一 WorkflowResult 投递链路、Provider 能力预检、[迁移指南](message-control-delivery-migration.md) |

## 标准验证

开发时运行最小相关测试，交付前运行完整矩阵。

Host checks：

```bash
npm --workspace @sparkclaw/webchat run build
npm --workspace @sparkclaw/webchat run test:voice
go test ./services/gateway/...
bash scripts/doctor.sh
bash scripts/run-eval.sh
```

如果 host Go 不可用：

```bash
sudo -n docker run --rm -u "$(id -u):$(id -g)" \
  -v "$PWD":/workspace -w /workspace/services/gateway \
  -e HOME=/tmp -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomodcache \
  golang:1.25-alpine /usr/local/go/bin/go test ./...
```

Compose checks：

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml config --quiet
sudo -n env SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal \
  docker compose --env-file .env -f docker/compose.yaml --profile minimal up -d --build
BROWSER_FIXTURE_URL=http://host.docker.internal:18791 \
BROWSER_FIXTURE_BIND=0.0.0.0 \
bash scripts/run-eval.sh
```

Postgres integration check：

```bash
SPARKCLAW_TEST_POSTGRES_DSN='postgres://sparkclaw:sparkclaw@127.0.0.1:15432/sparkclaw?sslmode=disable' \
go test ./services/gateway/internal/store -run TestPostgresStoreRoundTrip -count=1
```

## Golden Eval 覆盖

`scripts/run-eval.sh` 当前期望 43 个 golden cases。覆盖：

- direct `/chat` profile selection
- config、tool、skill、owner、client、auth 和 rate-limit surfaces
- file search/read/write/delete 和 multi-file grounded answers
- browser read、multi-source comparison、raw snapshots 和 prompt-injection handling
- memory candidates、sensitive-memory rejection、approval-gated sensitive writes、editing 和 export
- approval modification、approval-pending run lifecycle 和 post-approval trace refresh
- shell 和 code patch approval、rollback artifacts 和 sandbox command queueing
- model-call telemetry 和 fast/deep/repair lane selection
- smoke/chaos eval persistence、failure archives 和 eval history

添加 user-visible behavior 时，优先添加聚焦单元测试，以及一个覆盖真实 API path 的 golden case。

## 使用 Tools

新增工具时：

1. 定义 input 和 output structures。
2. 在 ToolHub 同一 registration 中注册 execution、risk、语义 capability、可信目录元数据、effect 与 outcome adapter。
3. 校验成功 output 与类型化 `ToolOutcome` adaptation。
4. 如果它会 mutate、send、delete、execute 或暴露 sensitive data，添加 policy defaults。
5. 添加 audit events 和 trace observation summaries。
6. 如果 observation 可能很大或未来有用，存档完整 observation。
7. 添加 unit tests 和至少一个 golden/smoke eval path。
8. 如果改变产品边界，更新 [架构](architecture.md)。

注册 capability 不会自动让模型看到工具。已迁移 Workflow Profile 必须把 capability 放入冻结活动 scope，且 `ToolExposure.Search/Materialize` 必须接纳并物化对应 registration。不要在 TaskHint、Skill metadata 或 Agent Runtime 中增加平行工具名清单。

当前 risk expectations：

| Risk | Behavior |
|---|---|
| `read` | policy 允许时可运行；输出是 untrusted evidence。 |
| `draft` | 可产生本地 drafts/candidates，无外部副作用。 |
| `reversible` | 需要 approval；必须保存 recovery metadata。 |
| `dangerous` | 需要 approval；必须记录 reason、resources 和 execution result。 |

## 使用能力路由与 Workflow

完整合同见[意图路由重构方案](intent-routing-workflow-refactor-plan.md)。迁移每个能力叶子时：

1. 在版本化 Capability Catalog 中增加叶子与允许的类型化 Operation，并只引用一个精确 Workflow 协议。
2. Fast 输出保持工具中立，拒绝未知 JSON 字段，Normalizer 冻结确定性 URL/path Fact。
3. 注册一个精确版本化 Workflow Profile。Registry 只能使用已校验的叶子身份解析，不能重新解释消息。
4. 为所有允许工具增加固定 Capability Metadata 与 Outcome Adapter；Tool Exposure 只能物化该 Scope。
5. 在 Profile 中声明允许的 Transition、Risk 与受治理参数绑定，并持久化 Route 供审批/登录恢复使用。
6. 删除同一功能的 TaskHint Candidate 与旧 Workflow 分支。
7. 增加从生产入口执行真实 Tool Adapter 的端到端测试，断言 `WorkflowResult`，并证明没有 Legacy Routing Audit。

Core Runtime 必须保持 Profile-neutral。如果实现需要按 Workflow ID 或工具名 Switch 来选择 Scope、资源、Assessment 或下一步，应把行为移入 Profile、Plan Binding、ToolHub Registration 或 Outcome Adapter。只有 `RouteDecision.Status == unmatched` 可以进入 ReAct。

## 使用 Models

确定性开发和 eval 使用 mock mode。DGX Spark 或兼容 OpenAI-style endpoints 使用 external mode。

重要规则：

- 发送 `sparkclaw-fast` 等 served names，不一定是 Hugging Face checkpoint IDs。
- 对需要简洁 assistant content 的 Qwen3 chat-completions path 设置 `SPARKCLAW_MODEL_DISABLE_THINKING=true`。
- 长 golden eval runs 中保持 generation caps 实用。
- 修改 model、context、MTP、GPU memory utilization 或 serving image 后重新运行 benchmark。
- LoRA、distillation 和 GGUF compatibility 在有 before/after eval evidence 前都视为 post-MVP model-ops work。

## Frontend Development

WebChat 位于 `apps/webchat/src/App.tsx`，共享 API types 位于 `apps/webchat/src/api/types.ts`。

修改 UI 行为时：

- Gateway 保持 policy 和 execution 的 source of truth。
- 展示 approval、trace 和 tool-call state，而不是隐藏它们。
- 保留 review-before-send microphone flow 与 Telegram multi-binding lifecycle。
- 修改 composer 或 settings control 后检查 desktop 与 mobile layout。
- 运行 `npm --workspace @sparkclaw/webchat run build`。
- runtime status 和 error states 要足够可见，方便本地 operator 排障。

## Config And Environment

主要配置文件：

- `configs/sparkclaw.default.json`
- `configs/model.profiles.json`
- `configs/tools.policy.json`
- `configs/sandbox.policy.json`
- `configs/eval.profiles.json`
- `docker/env/sparkclaw.example.env`

常用环境变量：

- `SPARKCLAW_API_TOKEN`
- `SPARKCLAW_MODEL_MODE`
- `SPARKCLAW_FAST_BASE_URL`, `SPARKCLAW_FAST_MODEL`
- `SPARKCLAW_DEEP_BASE_URL`, `SPARKCLAW_DEEP_MODEL`
- `SPARKCLAW_EMBEDDING_BASE_URL`, `SPARKCLAW_EMBEDDING_MODEL`
- `SPARKCLAW_RERANKER_BASE_URL`, `SPARKCLAW_RERANKER_MODEL`
- `SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS`
- `SPARKCLAW_MODEL_DISABLE_THINKING`
- `SPARKCLAW_STATE_BACKEND`, `SPARKCLAW_STATE_DSN`
- `SPARKCLAW_ARTIFACT_BACKEND`
- `SPARKCLAW_BROWSER_READ_ALLOW_HOSTS`
- `SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE`
- `SPARKCLAW_BROWSER_PROFILE_DIR`
- `SPARKCLAW_WEB_SEARCH_ENABLED`, `SPARKCLAW_WEB_SEARCH_PROVIDER`
- `SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF_FILE`
- `SPARKCLAW_INFINIMESH_INFO_DEVICE_ATTESTATION_FILE`
- `SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF_FILE`
- `SPARKCLAW_SPEECH_ENABLED`, `SPARKCLAW_SPEECH_BACKEND`
- `SPARKCLAW_SPEECH_BASE_URL`, `SPARKCLAW_SPEECH_ALLOWED_HOSTS`, `SPARKCLAW_SPEECH_MODEL`
- `SPARKCLAW_TELEGRAM_ENABLED`, `SPARKCLAW_TELEGRAM_BASE_URL`
- `SPARKCLAW_CREDENTIAL_KEY`, `SPARKCLAW_CREDENTIAL_KEY_FILE`
- `HF_TOKEN`, `HUGGING_FACE_HUB_TOKEN`

不要提交 `.env`、state encryption keys 或 downloaded model weights。

Infinimesh search、speech 与 Telegram 相互独立且默认关闭。Minimal profile 继续使用 `file` state backend，不要求 cloud 或 connector credential。每项功能都必须显式启用；Telegram enabled 而 speech disabled 时，text 与 attachment 继续可用，voice 返回明确不可用响应。

## Data And Trace Hygiene

Traces 和 artifacts 是开发资产，但可能包含敏感运行上下文。分享前：

- 确认 redaction settings 已启用
- 避免提交 `data/`
- 扫描 diff 中的 `hf_`、`sk-` 和 `Authorization` 等 token
- 确认 Infinimesh query 与 speech transcript 不进入 log、trace、status payload 或 committed fixture
- 确认 Telegram file/PostgreSQL state 保存 credential envelope，而不是 bot token
- raw external observations 只有在明确清洗后才进入 training data

## Post-MVP Work

有价值但不属于当前 MVP 必需项的后续工作：

- 更长的 DGX Spark soak loops
- smaller-context 和 no-MTP residency matrix，用于 simultaneous fast/deep serving
- 在满足[暂缓能力](deferred-email-calendar-knowledge.md)门槛后，以设计优先方式重新引入相应能力
- connector hardening 和 user-facing account setup
- trace cleaning 后的 LoRA/QLoRA 或 distillation
- GGUF/Ollama/llama.cpp compatibility profile validation
- 从 `packages/protocol` 抽取 SDK
- custom model profiles 的 packaging 和 rollback documentation

Model training 只应在稳定 eval loop 之后开始。任何 custom model release 都需要：dataset manifest、redaction notes、exact base checkpoint hash、training config、before/after eval report 和 rollback path。

## Handoff Checklist

声明项目级变更完成前：

1. 如果 commands、environment variables、boundaries 或 user workflows 改变，更新 docs。
2. 运行变更区域的 targeted tests。
3. UI 或 API types 变更后运行 WebChat build。
4. Go code 变更后运行 Gateway tests。
5. runtime 变更后运行 `scripts/doctor.sh` 和 mock golden eval。
6. Docker 变更后验证 Compose config。
7. 扫描 tracked diffs 中的 secrets。
8. 新的 DGX Spark benchmark evidence 记录到 `benchmarks/model_baseline.md`。
