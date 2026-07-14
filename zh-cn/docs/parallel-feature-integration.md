# 并行功能集成计划

> 语言：[English](../../docs/parallel-feature-integration.md) | 简体中文

本文档是 `codex/integration-voice-telegram-info` worktree 的合并契约。它负责协调
Infinimesh 信息、speech 与 Telegram 三个分支，但不接管这些分支的核心实现。

## 基线与当前状态

固定集成基点是 2026-07-14 的本地 `main` commit
`081c67a8c77ab0b838b547cd7b244d2eabc34c21`。本地 `main` 比 `origin/main`
领先 9 个 commit；本工作不会 push，也不会合并回 `main`。

创建本文档前已完成基线 setup 与验证：

| 检查 | 结果 |
|---|---|
| `npm run setup:document-tools` | 通过；Node 与 Python 文档运行时安装到被忽略的 `.tools/` 路径 |
| 在 `services/gateway` 运行 `go build ./...` | 通过 |
| 在 `services/gateway` 运行 `go vet ./...` | 通过 |
| 在 `services/gateway` 运行 `go test ./...` | 通过；setup 后包含 `internal/toolhub` |
| `npm --workspace @sparkclaw/webchat run build` | 通过；完成 `tsc -b` 与 Vite production build |
| 环境注记 | Host Go 为 `go1.25.12`；Host Node `v22.23.0` 低于声明的 Node 24 下限，但实际 build 通过 |

基线时的分支就绪状态：

| 分支 | 基点后 commits | Worktree 状态 | 合并状态 |
|---|---:|---|---|
| `codex/infinimesh-info` | 0 | Clean | 等待完成报告与 commits |
| `codex/voice-complete` | 0 | Clean | 等待完成报告与 commits |
| `codex/telegram-hardening` | 0 | 尚无已完成分支工作可用 | 等待完成报告、commits 与 clean worktree 证据 |

在三个分支依次具备完成报告、可审查 commit range 和 clean worktree 前，集成分支
必须保持 **等待合并** 状态。不得把未提交代码或 snapshot 代码复制到本分支。

## 兼容矩阵

| 场景 | 期望行为 | 必需证据 |
|---|---|---|
| Infinimesh info、speech、Telegram 全部关闭 | 现有本地 chat、WebChat、默认 tools 与 file state backend 正常；不新增必需 credential 或启动错误 | Config tests、Gateway tests、mock golden eval、默认 Compose config |
| Telegram 开启、speech 关闭 | Telegram text 与 attachments 仍可用；voice message 返回明确的 speech 不可用响应，且不产生伪造 transcript | 使用 disabled transcriber 的 Telegram unit/integration test |
| Speech 开启、Telegram 关闭 | Speech service 能独立初始化并服务其支持的路径；不启动 Telegram poller、webhook，也不要求 Telegram credential | Speech package tests 与 startup test |
| Infinimesh token 耗尽 | Infinimesh request 以有界、脱敏方式失败；本地 chat 与 Telegram 继续工作 | quota/auth exhaustion fault-injection test，加本地与 Telegram control requests |
| Infinimesh cloud 返回 5xx | Timeout/retry 保持有界，不污染本地 runtime 或 Telegram state | 5xx stub test，加本地与 Telegram control requests |
| 三者全开且使用默认 `file` backend | 三个功能通过唯一 assembly path 初始化；Telegram voice 使用真实 `speech.Transcriber`；不存在仅 PostgreSQL 可用的状态假设 | 使用 file state 的端到端 startup/API test |
| 敏感输入经过失败路径 | Token、query、transcript 不以明文进入 logs、traces、status JSON、artifacts 或 tracked diffs | 基于 canary 的 redaction tests 与最终 secret scan |
| WebChat desktop/mobile 尺寸 | 新 controls 与 status surfaces 可读，且不与现有 navigation、chat、action controls 重叠 | 代表性 desktop/mobile viewport 的 build 与截图 |

配置必须保留三个功能的独立开关。开启一个功能不得静默开启另外两个；关闭的功能
不得使其 credential 变为必填。

## 共享文件所有权

核心功能 package 继续由各功能分支负责。集成分支只处理共享装配与跨功能兼容。

| 共享表面 | 集成职责 | 必须保留 |
|---|---|---|
| `services/gateway/internal/config/config.go` | 协调 config fields、defaults、environment overrides、validation 与脱敏 public status | 每个功能一个开关；status 不带 secret；默认/最小 config 有效 |
| `services/gateway/cmd/sparkclaw/main.go` | 组合 lifecycle 与 dependencies；增加唯一 Telegram-to-speech 接线点 | 有界 shutdown；disabled feature 不初始化；不重复构造 transcriber |
| `services/gateway/internal/gateway/server.go` | 协调 API/status 暴露与 shared handlers | Gateway 保持权威；status endpoint 不暴露 secret、raw query 或 transcript |
| `apps/webchat/src/App.tsx` 与 `apps/webchat/src/api/types.ts` | 协调 shared types、status rendering 与 control placement | 现有 workbench workflow、严格 TypeScript build、desktop/mobile layout |
| `docker/compose.yaml` 与环境示例 | 协调 optional services 与 environment propagation | 默认 file backend、mock model mode、minimal profile 不要求新 secret |
| `scripts/doctor.sh` | 新增能区分 disabled、configured、reachable、degraded 的 capability checks | Doctor 输出脱敏；optional service 关闭时不作为必需项 |
| `docs/architecture.md`、`docs/development.md` 与 `zh-cn/` mirrors | 记录最终 boundary、configuration、operations、failure isolation 与 verification | 双向语言链接与 docs mirror CI |

集成分支不得重写 Infinimesh client、speech engine 或 Telegram transport/handler core。
如果 defect 只存在于某个 feature branch，应退回该分支处理；只有三功能共存确实需要时，
才做最小 shared-contract 修正。

## 合并顺序与门禁

顺序固定为：

1. `codex/infinimesh-info`
2. `codex/voice-complete`
3. `codex/telegram-hardening`

每次合并前：

1. 确认 branch owner 已报告完成并给出精确 commit set。
2. 用 `git status --short --branch` 确认其 worktree clean。
3. 从固定基点审查 `git log` 与完整 diff；拒绝无关 refactor、generated output、state、
   traces、transcripts、observation dumps、build products，以及无充分理由的 dependency drift。
4. 审查 test artifacts，并在其独立 worktree 复跑受影响测试。
5. 扫描 commit range，确认没有 secrets 及明文 token、query 或 transcript。
6. 记录 pre-merge head 作为 rollback point。

使用 non-fast-forward merge，使每个功能都有明确 rollback boundary。每次 merge 后立刻运行
受影响测试，并先更新下表，之后才能继续：

| 功能合并 | Feature commits | Merge commit | 立即验证 | 结果 |
|---|---|---|---|---|
| Infinimesh info | Pending | Pending | Config/model/provider tests；local-chat controls；token exhaustion 与 5xx isolation | Pending |
| Speech | Pending | Pending | Speech tests；disabled/enabled startup paths；local-chat controls | Pending |
| Telegram | Pending | Pending | Telegram text/attachment/voice tests；disabled-speech response；local-chat controls | Pending |

## 冲突解决原则

- 如果冲突只来自 shared imports、constructors、config layout 或 UI assembly，应保留功能
  分支已经测试的 core implementation。
- Shared file 必须基于 merge 后的 integration state 逐段解决，不能整文件选择一侧。
- Independent enablement 与 failure isolation 优先于便捷 default。
- 在 composition root 只构造一次真实 `speech.Transcriber`，并注入 Telegram 的
  `VoiceTranscriber` contract。Production wiring 不得增加第二个 adapter 或 mock。
- Speech disabled 时，注入明确的 unavailable implementation 或 absence state；Telegram
  text/attachment 保持工作，voice failure 必须向用户清晰表达。
- 使用 typed capability/status fields，不解析 display strings。Public status 只包含
  availability 与 reason code，绝不包含 credential、raw query 或 transcript content。
- 如果冲突要求改变 feature-core behavior，停止集成并退回 owner branch，不在集成阶段
  静默重新设计。

## 回归与隐私验证

每个集成 checkpoint 都运行最小受影响 suite。最终 checkpoint 从 repo root 运行以下命令
（标明目录的命令除外）：

```bash
npm run setup:document-tools
cd services/gateway && go build ./... && go vet ./... && go test ./...
cd ../.. && npm --workspace @sparkclaw/webchat run build
bash scripts/doctor.sh
bash scripts/run-eval.sh
docker compose --env-file .env -f docker/compose.yaml config --quiet
```

最终矩阵还必须包含：

- all-disabled、Telegram-only、speech-only、all-enabled/default-file-backend 的显式
  config/startup tests；
- Infinimesh quota exhaustion 与 5xx fault injection，同时 local chat 与 Telegram
  control requests 继续通过；
- Telegram voice coverage，证明 production object 是真实 `speech.Transcriber`，并覆盖
  speech disabled 的 unavailable response；
- Credential、query、transcript canary values，随后扫描 logs、traces、state/status output、
  artifacts 与 `git diff`；
- WebChat desktop/mobile visual checks，确认没有不合理 overlap；
- `.github/workflows/ci.yml` 中权威 docs mirror/link check；
- 扫描 tracked diff 中常见 credential prefix、authorization headers、private keys、raw
  transcripts、generated state 与 test artifacts。

Compose validation 使用从 `docker/env/sparkclaw.example.env` 复制的临时 `.env`；绝不加入 Git。

## 回滚点

| Checkpoint | 回滚动作 |
|---|---|
| 基线文档 commit | 将后续 integration work reset 到此 commit；保留 plan 与 baseline evidence |
| Infinimesh merge commit | Provider isolation 或 local controls 失败时 revert 此 merge commit |
| Speech merge commit | Disabled startup 或 independent speech behavior regression 时 revert 此 merge commit |
| Telegram merge commit | Text/attachment behavior、voice degradation 或 polling lifecycle regression 时 revert 此 merge commit |
| Shared wiring commit | Feature cores 独立通过但共存失败时，只 revert assembly commit |
| Docs/validation commit | Documentation 或 test-record changes 与 behavior commits 分开 revert |

任何回滚都不使用 destructive history rewriting，也不 push checkpoint。一个 gate 失败就停止
合并序列；不得在已知失败之上继续合并后续分支。

## Definition Of Done

只有满足以下条件，集成才算完成：

- 三个分支按固定顺序完成合并，并记录精确 commit sets；
- 每个 pre-merge worktree 都是 clean，且每个 commit range 均审查 scope、generated artifacts
  与 secrets；
- Conflict 与 behavior decisions 已记录在本文档和 final report；
- Telegram 使用唯一真实 `speech.Transcriber` 接线；speech disabled 时 voice 明确不可用，
  text/attachment 不受影响；
- 兼容矩阵所有场景通过，包括 file backend 与 failure isolation coverage；
- Setup、Go build/vet/test、WebChat build、doctor、mock golden eval、Compose config、docs
  mirror/link validation、secret scan 与 desktop/mobile visual checks 全部通过；
- Architecture/development 的 English 与 Chinese 文档都已更新；
- 每个主题独立 commit，没有 push 或 merge `main`，最终 integration worktree clean；
- Final report 列出 merged commits、conflict decisions、behavior changes、full validation evidence
  与 residual risks。
