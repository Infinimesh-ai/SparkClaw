# SparkClaw 开发

> 语言： [English](../../docs/development.md) | 简体中文

本文档是贡献者入口。修改行为前先阅读系统职责[架构](architecture.md)、已发布用户表面
[Workflow 能力矩阵](workflow-capabilities.md)，以及[文档索引](index.md)中的相关专项手册。

## 仓库结构

```text
apps/webchat/              React/Vite owner 工作台
services/gateway/          Go Gateway 和 runtime package
configs/                   Runtime 和 model 配置
docker/                    Compose、image 和环境变量模板
scripts/                   setup、doctor、eval 和 model helper
eval/golden/               Golden case 和 fixture
benchmarks/                模型端点证据
packages/                  可移植 protocol/policy/schema 说明
tools/document-runtime/    声明的文档 adapter dependency
docs/                      当前英文文档
zh-cn/                     简体中文文档镜像
```

`services/gateway/internal` 中大体依赖方向：

```text
app contracts
  <- capability / semanticrouting / messagecontrol / delivery / document
  <- modelrouter / toolhub / policy / store / adapters
  <- agent Workflow runtime
  <- gateway HTTP 和 cmd/sparkclaw assembly
```

Owner package 不得依赖 Gateway handler 或 WebChat concern。Adapter 实现 typed interface，
不重新定义 domain record。

## 安装

已验证的宿主基线是 Ubuntu ARM64、Go 1.25、Node.js 26、npm 11、Python 3.12
和 Docker。`.nvmrc`、CI 与 Docker build 都使用 Node 26，使宿主和容器验证保持
相同主版本。

Go、Node.js、npm、Python 和 pip 应作为宿主机工具安装并可从 `PATH` 访问。所有
Node 依赖由根 npm workspace 管理，`setup:document-tools` 将 Python 文档库安装到
宿主用户的标准 site-packages。仓库不再使用 `.tools` runtime 或绑定平台的工具链目录。

```bash
npm run setup:host
```

根据要验证的模型来源，使用明确的产品运行态：

```bash
npm run start:local
npm run start:remote
```

两条命令都先加载 `docker/env/sparkclaw.product.env`，再分别加载
`docker/env/sparkclaw.local.env` 加 `.env.local`，或
`docker/env/sparkclaw.remote.env` 加 `.env.remote`。不再存在托管 chat 加本地辅助模型的
混合模式，也不提供可绕过 profile 校验的局部产品重建命令。

仅在隔离的 mock/file 与 Vite 调试中直接运行宿主进程，对应命令为
`npm run dev:gateway:host` 和 `npm run dev:webchat:host`。Compose、auth、
state backend、DGX Spark model 和运行环境变量见[部署](deployment.md)。

## 标准验证

按改动范围运行检查。常规完整本地 gate：

```bash
cd services/gateway && go test ./...
cd services/gateway && go vet ./...
npm --workspace @sparkclaw/webchat test
npm --workspace @sparkclaw/webchat run build
bash scripts/doctor.sh
```

还需要按范围运行：

- 路由、Workflow、模型、tool、Policy、delivery 或用户流程改动：`bash scripts/run-eval.sh`；
- Compose/config 改动同时验证两套版本化展开：

  ```bash
  docker compose --env-file docker/env/sparkclaw.product.env \
    --env-file docker/env/sparkclaw.local.env \
    -f docker/compose.yaml -f docker/compose.models.local.yaml \
    --profile product --profile models-local config --quiet
  docker compose --env-file docker/env/sparkclaw.product.env \
    --env-file docker/env/sparkclaw.remote.env \
    -f docker/compose.yaml --profile product config --quiet
  ```
- browser transport/profile 改动：`npm run setup:browser` 加针对性测试/eval；
- Markdown 改动：本地或 CI 运行 `docs` job 规则，每份英文文档必须有 `zh-cn/` 镜像，
  所有本地链接必须有效。

credential-gated live service 只能作为附加证据。确定性测试仍需覆盖 unavailable、timeout、
malformed 和 authorization failure。

## 修改流程

1. 为触及范围建立 clean behavioral baseline。
2. 阅读 owner package、测试、配置和当前专项手册。
3. 为每个新增事实或 registry entry 确定唯一 source of truth。
4. 修改最小完整 owner boundary；除非 persisted data 明确要求，不保留平行 compatibility path。
5. 为成功、失败和契约边界增加 focused test。
6. 运行相应验证矩阵。
7. 同一改动更新英文和中文文档。
8. handoff 前检查 `git diff` 中的无关修改、generated churn、secret 和 stale name。

所有工作遵守[工程基线](engineering-baseline.md)，架构清理遵守[重构手册](refactor-playbook.md)。

## Capability 与 Workflow 修改

当前自然语言路径是 semantic graph -> 为所有 eligible candidate 生成 embedding/Fast Tree
score -> weighted fusion -> Top-2 decision -> deterministic route assembly。不要增加
keyword fallback、第二套 capability map 或 model-owned `RouteDecision`。
见[意图路由](intent-routing.md)。

新增用户可见 capability：

1. 在 `internal/capability` 注册 branch/leaf、route contract、operation、target 和 Workflow reference。
2. 为叶子实现且只实现一个 versioned Workflow Profile。
3. 注册带真实样例、Tree distinction、hard negative 和来源限制的 semantic variant。
4. 为必需 resource 增加 candidate-neutral deterministic grounding。
5. 注册带 schema、risk/effect 和 outcome adapter 的准确 ToolHub capability。
6. 定义 Workflow node、transition、argument binding、completion evidence、retry bound 和 final projection。
7. 验证 Catalog/profile/graph 一致性、tool exposure、Policy/Approval、终态失败、语义混淆和端到端 delivery。
8. 更新 [Workflow 能力矩阵](workflow-capabilities.md)。

只有 active Workflow node 暴露 tool。匹配 Workflow 不回退到任何通用循环；workflow 步骤循环（见 [Workflow 执行](workflow-execution.md)）是唯一的执行原语。矩阵外 tool
可以为未来迁移继续注册，但不能宣传为当前用户能力。

## 消息、定时与 Delivery 修改

复用 `internal/app` 共享 contract、`internal/messagecontrol` registry 和 `internal/delivery`
Provider/Gateway。Web 是注册 delivery port，不是独立 result path。Timer 通过 Message Runtime
重新发布到期内容，不直接发送。

Schedule edit/delete 必须 list pending owner-scoped record、唯一解析 target、绑定当前 version，
并用 compare-and-swap mutation。typed UI ID 只是 hint，不能跳过 fresh lookup。见
[消息与定时任务](messaging-and-scheduling.md)。

新 connector 注册 binding、delivery provider、可选 inbound runtime、capability 和 shutdown。
Gateway/Agent code 不得按 provider name 分支。

## 浏览器修改

唯一执行后端是 Checksum-pinned SparkClaw Browser Bridge 与 Owner-scoped Playwright
Controller，连接持久 SparkClaw Chromium Profile。ToolHub Contract 保持 Provider-neutral，
Controller/Session/Page Ownership 有界，Page Evidence 不可信，Action Ref 绑定 Fresh Snapshot。
不要恢复 Container Chromium、其他 Browser Profile、Cookie Export、CDP、任意 Playwright Code
或第二 DOM Collector。见[浏览器 Runtime](browser-runtime.md)。

## 文档修改

Format inspection、high-level parse、normalized evidence、context projection、准确 editor
registration、approval、output-copy write 和 post-edit preservation 必须保持在同一 staged pipeline。
新 editor 需要准确 format/operation schema 和 delta verification。见[文档 Workflow](document-workflows.md)。

每个格式或 operation 都在事实所属 package 中注册：

1. 把 ToolHub parser/editor 执行、调用校验、locator 构建、directory boundary 和输出投影加入
   对应 format provider。
2. 把 normalization、lifecycle、package verification 和 preservation delta 加入 `document`
   format policy。
3. 编排存在差异时，把 route grounding、decision evidence/rule、argument binding/validation、
   schema materialization 和结果 evidence 投影加入 Agent format policy。
4. signature inspection 保持集中，并以冻结 capability 的 `format`/`operation` qualifier 作为
   连接点。不要让 Agent import ToolHub 实现类型，也不要创建共享 mega-registry。

注册构造器必须拒绝重复 format/operation。扩展该抽象时增加 synthetic registration test 和
共享 dispatch source guard；prompt 搬移必须先用严格有序快照锁定，任何 calibration 改动另行提交。

## WebChat 修改

API transport 放在 `apps/webchat/src/api/client.ts`，共享 response/action type 放在
`apps/webchat/src/api/types.ts`。Gateway 负责 validation 和 public projection。为状态逻辑增加
focused component/library test 并 build 完整应用。在运行中的 Gateway 上检查 desktop/mobile。
见 [WebChat](webchat.md)。

## Store 修改

使用 `internal/store/store.go` 中的 typed interface；不得恢复 broad Store dependency。
Runtime 仅限于 `cmd/sparkclaw` assembly 与 lifecycle。消费者只接收所需 repository。

shared normalization、command、replay 与 reconciliation semantics 放在
`<repository>_contract.go`。存储实现分别放在 `<repository>_memory.go`、
`<repository>_file.go`、`<repository>_postgres.go`。新增 snapshot-backed record 时同步更新
File `Snapshot`。Backend construction 与 generic durability primitive 保持在精简的 root backend
文件中。

选择验证规格前，先把 operation 分为 P0、P1 或 P2。每项 change 都需要三 backend parity 和
显式 context/error behavior。只有 operation risk 或 aggregate invariant 要求时，才增加
transaction、idempotency、unknown-outcome reconciliation、确定性 failure injection、真实
PostgreSQL 与 race evidence。完整策略、lifecycle 与测试命令见 [Store](store.md)。

## 模型与 Prompt

- Gateway 选择 model lane，prompt 不能自选。
- semantic routing weight/threshold 是 embedded calibration artifact，只有在有 labeled
  calibration/holdout 证据时才能修改。
- Prompt 改动需要与契约对应的 malformed-output、repair、injection、多语言和 ambiguity coverage。
- smoke call 不能证明模型质量；可重复测量写入[模型基线](../benchmarks/model_baseline.md)。
- 加载/容量改动遵守[模型加载](model-loading.md)。

## 配置与数据卫生

- 每个配置都要加入 typed config、default、必要的 environment override、example env、public
  redacted projection 和测试。
- credential、native recipient ID、raw audio、敏感文件内容或未脱敏 provider payload 不得进入
  public config/log/trace。
- subprocess/outbound call 必须有 timeout、size/concurrency limit 和 owned cleanup。
- durable record 保持 file/PostgreSQL Store parity。
- 使用受治理 workspace/artifact ref，不接受任意 host path。

## 文档维护

编写 current-state 文档，不为每个功能永久保留计划。实现期间可以有 design record，完成后：

1. 把长期架构决策合并到 `architecture.md` 或 owner 专项手册；
2. 把命令合并到 `deployment.md`，贡献者步骤合并到本文；
3. 按需更新 capability matrix 和 changelog；
4. 删除已完成计划及其中文镜像；
5. 修复全部 inbound link 并运行 docs check。

这样当前文档集合才能保持足够小并真正权威。
