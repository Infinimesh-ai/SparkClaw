# Issue #15 部署启动可靠性设计

> Language: 简体中文 | [English](../../docs/issue-15-deployment-reliability-design.md)

> 状态：针对 [issue #15](https://github.com/Infinimesh-ai/SparkClaw/issues/15)
> 的实现与本地验证已完成。Owner 于 2026-08-17 接受了全部剩余建议；实际 DGX 重启验收仍需
> 在部署窗口执行。

> 当前入口说明（2026-09-03）：下述可靠性决策现在只通过两种产品模式生效：
> `deploy:local` / `start:local` 与 `deploy:remote` / `start:remote`。Local 和 Remote
> 共用应用 Compose 与产品契约，只有 Local 额外加载模型 Compose。下方故障分析只描述已退役
> 路径，不构成可运行的替代入口。

## 决策摘要

SparkClaw 的产品 Compose runtime 使用 PostgreSQL。项目仍处于研发阶段，没有存量用户迁移
义务，因此仓库不提供 file 到 PostgreSQL 的迁移工具，也不为旧 file snapshot 添加自动启动
guard。示例环境与部署文档必须表达实际生效的产品默认值，不能继续暗示 file backend 是产品
运行路径。

模型启动不再无条件冷重建，而是拆成三种判断：

- 已经 running、healthy 且配置一致的模型组保持原样；
- 任一成员缺失、停止、不健康或配置漂移时，恢复完整的目标 residency group；
- 显式设置 `SPARKCLAW_FORCE_MODEL_RECREATE=true` 时，即使健康也重建整个目标组。

WebChat host port 由一个经过校验的 `SPARKCLAW_WEBCHAT_PORT` 统一拥有，Compose、部署探针、
runtime readiness 与输出 URL 都使用它。Nginx 容器内部仍监听 `18790`。

Fast 与 Guard 的 readiness 程序被复制进 SparkClaw 构建的 vLLM 派生镜像。运行中的模型容器
不再依赖 checkout 的宿主路径。预热已经成功时，即使优化 marker 无法持久化，readiness 仍然
成功。

启动服务改用 `Type=oneshot`，以 `RemainAfterExit=yes` 保留成功后的 active 状态，并设置覆盖
Docker/NVIDIA 等待与正常模型启动预算的有限超时。

提交 `bbacb81` 已经让部署流程正确处理 `SPARKCLAW_AUTOSTART_ENABLED=false`；本 issue
接受该行为，不重复实现。

## 范围与非目标

范围内：

- 产品 state backend 默认值与升级说明；
- 模型组检查、恢复与显式强制重建；
- WebChat host port 在 Compose 和 shell 入口间的统一配置；
- 自包含模型 readiness 与 best-effort marker 持久化；
- 有限且可观测的 systemd 启动；
- 聚焦的脚本、Compose、文档与 DGX 启动验证。

不在范围内：

- 把 `data/memory/gateway-state.json` 导入 PostgreSQL；
- 删除、重命名或自动解释旧 file snapshot；
- 修改 PostgreSQL schema 或 Store contract；
- 修改模型 checkpoint、warmup payload、residency budget 或逻辑 model lane；
- 修改 WebChat 容器内部 listener port 或 Gateway 内部端口；
- 用 Docker restart policy 替代 Local 模型的开机恢复。

## 历史故障模型

本 issue 实施前，已退役路径单独看都有依据，但组合后并不安全：

1. 已退役的示例环境声明 `SPARKCLAW_STATE_BACKEND=file`，后加载的 runtime profile
   却静默把有效值改为 PostgreSQL。
2. `scripts/serve_models_compose.sh` 每次都停止并 force-recreate 所有目标模型。因此即使全部
   容器健康且配置一致，产品启动与开机启动也会承担完整模型加载。
3. Compose 允许配置 WebChat bind address，却把 host port 固定为 `18790`；
   已退役的 deployer 与 runtime readiness 路径又分别重复该端口。
4. Fast 与 Guard healthcheck 执行从 checkout bind-mount 的 Python 文件。模型进程仍然有效时，
   移动或删除仓库也会让 healthcheck 无法启动。
5. marker 写入抛出 `OSError` 时，已经成功的 warmup 会被改判为 healthcheck 失败。
6. 生成的 systemd unit 没有启动超时，卡住的启动可以永远保持 activating。

## 配置契约

| 设置 | 默认值 | 契约 |
|---|---|---|
| `SPARKCLAW_STATE_BACKEND` | example 与 product profile 中均为 `postgres` | 产品启动选择 PostgreSQL。`file` 只保留给显式的隔离 host/mock 使用。 |
| `SPARKCLAW_WEBCHAT_PORT` | `18790` | `1..65535` 范围内的十进制 host TCP port；显式空值或非法值必须在修改 Compose 状态前失败。 |
| `SPARKCLAW_WEBCHAT_BIND` | `0.0.0.0` | 只表示 host address，不包含端口。 |
| `SPARKCLAW_FORCE_MODEL_RECREATE` | `false` | 布尔值。已导出的进程环境优先于 `.env`；否则以不执行 shell 的方式读取 `.env`。非法值必须在修改模型前失败。 |
| `SPARKCLAW_VLLM_IMAGE` | 当前 pin/default 的上游镜像 | 继续表示本地 readiness 派生镜像的上游 base，保留现有 operator override。 |

Shell 入口不能 `source .env`。它们应共享一个不会执行内容的 dotenv reader，只读取少量由脚本
拥有的值。把已退役 deployer 与 `autostart_compose.sh` 中重复的 reader 提取出来，应作为与
行为变更分开的机械提交。

## State Backend 对齐

把示例环境改为 PostgreSQL，同时让 single-Fast 产品 profile 继续显式选择 PostgreSQL。部署
文档必须说明：

- `start:local`、`start:remote`、两个 deployer 与 Local boot autostart 都使用 PostgreSQL；
- 旧 file snapshot 既不迁移也不删除；
- 直接升级因此可能从空 PostgreSQL 数据库启动；
- file backend 仍可用于显式选择的隔离 host/mock 运行，但不是产品默认值。

产品 Compose CI 中对 `state_backend=postgres` 的断言继续作为可执行 guard。不增加 Store
代码或数据迁移。

## 模型启动状态机

### 检查

修改模型组之前，对每个目标 service 检查以下全部事实：

1. Compose container ID 存在；
2. `.State.Status` 为 `running`；
3. `.State.Health.Status` 为 `healthy`；
4. 容器的 `com.docker.compose.config-hash` 等于该 service 的 `docker compose config
   --hash` 结果。

主机重启后必须独立检查进程状态：Docker 可能在已停止的容器上保留旧 health 值。停止的容器
不是健康的 resident service。

对于 `single-fast`，Fast、embedding、Guard、ASR 与 OCR 仍是一个原子 residency group。任一
成员需要恢复时，五者一起恢复。评估产品组之前仍停止遗留 Deep 容器。显式 standalone 与
benchmark 命令只对其选择的 service 使用相同判断。

### 动作

| 条件 | 模型组动作 | 预期结果 |
|---|---|---|
| 全部成员 running、healthy、配置一致，force flag 为 false | 不停止、不重建，执行 `compose up --wait --build --no-recreate` | 使用构建缓存检查并确认 readiness；模型进程 identity 不变。 |
| 任一成员缺失、停止、不健康或漂移，force flag 为 false | 停止完整目标组，再执行 `compose up --wait --build --force-recreate` | 自动整组恢复会刷新陈旧 GPU runtime 状态。 |
| force flag 为 true | 停止完整目标组，再执行 `compose up --wait --build --force-recreate` | 刷新容器 runtime、NVIDIA attachment 与进程本地 cache。 |

`--build` 让小型 readiness 派生镜像与 Dockerfile/helper 保持一致；未变化启动依靠 Docker
layer cache，不重建上游 vLLM 镜像。现有 startup timeout 继续约束 `compose up --wait`。
任何检查错误都应成为显式脚本失败，不能静默当作健康模型组。

脚本测试使用有状态 fake Docker，覆盖 healthy/current、missing、stopped-with-stale-health、
unhealthy、drifted、forced、invalid-flag、inspection failure、single-Fast 原子性、
no-recreate 保留与 standalone lane。

## WebChat Port 所有权

Compose 发布：

```text
${SPARKCLAW_WEBCHAT_BIND:-0.0.0.0}:${SPARKCLAW_WEBCHAT_PORT:-18790}:18790
```

`docker/compose.yaml` 与 `docker/compose.dev.yaml` 使用相同映射。WebChat Dockerfile、Nginx
listener 与 service-to-service routing 仍使用内部端口 `18790`。

共享 dotenv reader 只解析和校验一次 host port。该值随后统一拥有：

- Local 与 Remote 入口中的 WebChat 与 `/readyz` probe URL；
- deployer 输出的 local、LAN 与 Remote URL；
- 两个 start script 共用的默认 readiness URL；
- `start:local`、`start:remote` 与 Local boot startup 使用的 Compose 插值。

显式 `SPARKCLAW_GATEWAY_READY_URL` 继续作为特殊 proxy layout 的最高优先级逃生口。测试必须
使用一个非默认端口，避免 operational path 中隐藏的 `18790` literal 蒙混过关。

## 自包含模型 Readiness

新增一个以 `SPARKCLAW_VLLM_IMAGE` 为 base 的小型派生 Dockerfile，并在构建时把
`scripts/model_readiness.py` 复制到 `/opt/sparkclaw/model_readiness.py`。通用 vLLM services
使用该镜像并删除 source-file bind mount。模型 cache 继续作为这些 services 唯一需要的 host
mount。OCR 保持其独立 pin 的镜像与轻量 healthcheck。

Fast 与 Guard marker 从通用 `/tmp` 移到专用的小型 container-local tmpfs，例如
`/run/sparkclaw-readiness`。marker 内容继续绑定 model、warmup shape 与 process start
identity，因此新模型进程仍然必须 warmup。

completion 通过 `require_completion` 后，marker 持久化只是优化：

1. 尝试原子写入 marker；
2. 出现 `OSError` 时输出一条有界且不含 secret 的 warning；
3. 返回 readiness success。

model listing 失败、completion 失败、malformed response 或 timeout 仍然导致 readiness 失败。
只有成功 warmup 证据之后的持久化是 best-effort。

marker 使用独立 tmpfs 后，常见的 read-only/full-`/tmp` 情形不再适用。如果该专用位置也不可
写，stateless Docker healthcheck process 无法持久记住已成功 warmup，后续 probe 可能重复
warmup。Owner 接受该残余行为；本 issue 不增加 long-lived supervisor 或 sidecar。

## Systemd 启动

生成的 service 保留现有 dependency、user/group、仓库 mount requirement、严格 umask 与安装
时不立即启动的行为。Service contract 改为：

```ini
[Service]
Type=oneshot
ExecStart=/absolute/path/to/bash /absolute/path/to/scripts/autostart_compose.sh
RemainAfterExit=yes
TimeoutStartSec=4h
```

接受固定四小时上限：它超过当前 10 分钟 Docker/NVIDIA readiness wait 与三小时 Compose 模型
启动预算，同时保持有限。超时会让 unit 进入 failed，日志保留在 journal，operator 可以显式
重试。本 issue 不增加另一个 timeout 配置项。

描述与日志不能再把每次 boot 都称为 cold recreate。它们应描述 reconciliation，并报告模型组
是 retained、recovered 还是显式 force-recreated。

## 失败语义

- 非法脚本配置在停止容器前失败。
- 模型检查错误 fail closed，并保留当前模型组。
- 健康且配置一致的模型组不会因为 `start:local` 或 boot 被停止。
- 模型组恢复绝不只启动 single-Fast residency set 的一个成员。
- 模型证据成功前 readiness 保持失败；只有该证据之后的 marker 持久化不致命。
- 自定义 WebChat port 在发布与本地 probe 中保持一致。
- systemd timeout 产生 failed unit，而不是永远 activating。
- 旧 file state 保留在磁盘上，但 PostgreSQL 产品 runtime 忽略它；这是文档化行为，不是自动
  error。

## 实现切片

1. 提取并测试共享的非执行型 dotenv reader，不改变行为。
2. 对齐 state backend template 与双语升级文档。
3. 增加 WebChat port 校验，并把值传递到 Compose、deployment、runtime readiness 与测试。
4. 增加 vLLM 派生镜像，删除 bind mount，把 marker 移到专用 tmpfs，并让 marker write 成为
   best-effort。
5. 实现模型组检查与最终确认的 recovery/force 状态机，增加聚焦 fake-Docker 测试。
6. 修改生成的 systemd unit 与测试，再更新双语 model loading 与 deployment 指南。

机械提取与行为变更应保持为不同提交。

## 验证矩阵

聚焦的确定性检查：

```bash
python3 -m unittest scripts/test_model_readiness.py
python3 -m unittest scripts/test_dotenv.py
python3 -m unittest scripts/test_serve_models_compose.py
python3 -m unittest scripts/test_local_compose.py scripts/test_remote_compose.py
python3 -m unittest scripts/test_autostart_compose.py
bash -n scripts/deploy_local.sh scripts/deploy_remote.sh \
  scripts/start_local_compose.sh scripts/start_remote_compose.sh \
  scripts/serve_models_compose.sh \
  scripts/autostart_compose.sh scripts/install_autostart_systemd.sh
```

Compose 检查必须同时 render 默认和非默认 WebChat port，断言产品环境使用 PostgreSQL，断言
vLLM services 不再 bind-mount `scripts/model_readiness.py`，并验证所有相关 overlay 组合。

仓库 gate：

```bash
cd services/gateway && go build ./... && go vet ./... && go test ./...
npm --workspace @sparkclaw/webchat test
npm --workspace @sparkclaw/webchat run build
bash scripts/doctor.sh
```

双语 Markdown mirror 与本地链接检查也作为 gate。

DGX 验收证据必须覆盖：

1. healthy/current 状态运行 `npm run start:local` 时，五个模型 container ID 保持不变，且不会再次 heavy
   warmup；
2. 每种 degraded predicate 都触发最终确认的 whole-group recovery；
3. 显式 force flag 替换全部目标 container ID；
4. 非默认 WebChat port 上 `/` 与 `/readyz` 都可用，且 deployment 输出该端口；
5. readiness 镜像构建后，临时重命名 host helper，运行中的模型 healthcheck 仍成功；
6. 模拟 marker-write failure 时，成功 warmup 仍为 healthy；
7. 生成的 unit 通过 `systemd-analyze`，以 oneshot 成功，并在故意阻塞启动时于有限时间内失败。

## 已解决决策

1. 产品 state backend 是 PostgreSQL。
2. 本 issue 不提供 file 到 PostgreSQL 迁移工具或 legacy-snapshot 启动 guard。
3. File state 保留为显式隔离开发选项。
4. 普通启动不冷重建 healthy/current 模型组。
5. Operator 只有一个显式 force-recreate flag。
6. WebChat host-port 所有权由环境端到端统一。
7. 模型 healthcheck 必须在运行容器中自包含。
8. Marker 持久化不能覆盖成功 warmup 证据。
9. 目标模型组 degraded 时自动整组 force-recreate；force flag 对原本健康的模型组执行同一
   动作。
10. systemd service 使用固定 `TimeoutStartSec=4h` 的 oneshot。
11. 专用 tmpfs marker 持久化是 best-effort；如果该 tmpfs 也不可写，readiness 仍成功，后续
    probe 可能重复 warmup，不增加 supervisor 或 sidecar。
12. `bbacb81` 完成的 autostart enable/disable 安装行为已经结束。

这些决定消除了剩余 recovery 与 failure-semantics 歧义。设计现在超过要求的 90% 实现把握
阈值。
