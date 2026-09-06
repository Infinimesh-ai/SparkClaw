# Changelog

> 语言： [English](../CHANGELOG.md) | 简体中文

所有重要的项目级变更都应记录在这里。

项目目前处于 pre-1.0。Breaking changes 可能发生，但当它们影响 users、operators 或 contributors 时应记录。

## [Unreleased]

### Added

- 新增 ISCP v0.2 托管模式：`iscp-bridge enroll-ticket` 子命令把 Cloud 签发的 pairing
  ticket v3 兑换为 `mode: "managed"` 注册 bundle；新增托管 session 层，由 Bridge 持有
  Trust Grant 并主动向只作应答方的手机发起会话；并按 Relay descriptor 声明的能力，主动
  完成 Trust Grant 自动续期与 Relay 凭据恢复。旧版外部签发的双 grant 注册契约保持不变。
- 新增面向手机首页的只读投影 `agent.activity.list.v1` 与 `agent.snapshot.get.v1`
  （能力 `agent.snapshot` v1），完全基于已有的审批、run 与通知状态聚合，不新增 store 实体。
- 新增 WebChat 原生边录边转写：Qwen3-ASR 持续返回 revisioned partial，同一 session 返回
  authoritative final；增加 browser-local 静音结束模式（默认关闭），并在录音中途发生任意
  realtime failure 后自动使用一份完整 WAV 执行 batch recovery。
- 新增网站可流式提供的安装器与 GB10 DGX Spark 部署入口：安全 clone/update checkout，
  在 `curl | bash` 中保留交互式 secret 输入，准备本地配置、下载并预热常驻模型组，启动
  Gateway/Sandbox/WebChat，并验证 ready 状态。
- 新增 Streamable HTTP MCP 发现与 ToolHub 注册，独立连接 Happy Team 任务端点和个人
  bridge；同时新增持久化 Happy supervised-plan 审批收件箱，支持 live plan 重试、编辑和
  remote-first reconciliation。
- 文档 OCR：可选启用的 OvisOCR2 适配器（`internal/documentocr`、`sparkclaw-ocr`
  compose 服务与 `SPARKCLAW_OCR_*` 配置），有界恢复扫描版 PDF 页面、增强图片证据，
  未配置时自动降级为关闭。
- LocalMind scoped workspace MCP 集成：身份锁定的发现、有界目录选择、命名空间化的
  `localmind.*` 动态工具与脱敏限长的结果投影；通过环境变量解析的 URL/token 显式启用。
- 受管入站 MCP/ISCP 访问：单次使用的哈希绑定访问票据、持久化对端绑定与幂等会话操作，
  经加密 ISCP bridge 与可选启用的 LAN `/mcp` 端点暴露；WebChat 提供 owner 侧传输开关
  与访问记录删除。
- 被动 ISCP 协作通知：按 owner 的持久化收件箱与 WebChat 全局通知中心。
- 微信通知绑定的 QR 登录改在受管可见 Chromium 配置内打开，不再调用宿主默认浏览器。
- 当前态 architecture、deployment 和 development documentation。
- `zh-cn/` 下的中文项目文档镜像。
- DGX Spark model-serving guidance 和 benchmark evidence。
- 开源项目文件：license、contribution guide、security policy、support guide、code of conduct 和 GitHub templates。

### Changed

- JingSi Runtime v1 运行边界：新增 `jingsi_runtime_v1.retention_days`
  （`SPARKCLAW_JINGSI_RUNTIME_V1_RETENTION_DAYS`，默认 30，`0` 表示永久保留，加载时校验），
  每小时清理超过保留期的终态 execution 记录与 negative fence，非终态工作永不清理；已接受但
  尚未运行的 execution 超过 4 × `max_concurrent` 后，Submit 返回可重试的 `runtime_unavailable`
  Problem（`retry_after_ms=5000`、无副作用），挂起的 goroutine 因而有界；在 Gateway 绑定
  提供方生命周期之前所有操作返回 `runtime_unavailable`，不会有 execution 在不可取消的
  context 下运行；提供方通过 `slog` 记录 bearer 拒绝（仅计数）、幂等冲突、持久化失败、
  队列拒绝与终态结果，日志不含 goal、Memory Context、摘要或 bearer。
- JingSi Runtime v1 授权投影：scope 前缀、adapter 身份与 approval policy 枚举统一放在一个叶子包
  （`internal/jingsiscope`），由工具暴露与 run 预算共用同一个 fail-closed 解析器；无法解析的投影
  会关闭该 run（无工具、无工具调用）。`data_scope` 与 `network_scope` 现在在工具暴露边界强制执行：
  把工具声明的每个 effect 映射到 JingSi 必须授予的 token（`external.*` 对应 `network_scope`，
  `workspace.*`/`local.read`/`local.write` 对应 `data_scope`）；无 effect 或 effect 未映射的工具对
  所有 JingSi execution 隐藏，映射表记录在 `docs/jingsi-runtime-v1.md`。`budget.max_output_bytes`
  不再投影进 run（只由提供方限制结果摘要）。execution 现在把交付的附件报告为不透明的版本化
  `artifact_refs`，并在终态事件前为每个引用发出一条 `artifact.available` 事件。
- 第六轮评审的裁决落地：打开邮件提供方登录浏览器前必须先启用该提供方（登录不再改动启用开关，
  WebChat 登录按钮跟随开关）；上一进程遗留为 `executing` 的 run 在 Gateway 启动时标记为
  `failed` 并记录 audit `run.interrupted_by_restart`，不再永久显示执行中，LocalMind 任务设计
  不再声称轮询可跨重启恢复；非 loopback 的 `gateway.bind` 在既无 pairing 又无 API token 时打印
  启动警告；移除失效的 `model.fast.mtp`/`model.deep.mtp` 字段、从未产生的
  `ModelCall.fallback`/`error_note` 字段（Postgres 迁移 `0011` 删除该列）及其 WebChat 类型；
  JingSi Runtime 的 request key 文档化为绑定到已认证 caller（契约禁止跨 space 复用），结果摘要
  在契约 131072 字节响应上限内钳制为 64 KiB；ASR 文档化为不在模型容量契约范围内。CI 在
  `INFINICENTER_TOKEN` secret 授予中枢只读权限时运行中央 JingSi 契约门禁。
- Store 与 Gateway 遥测：`GET /metrics` 的消息、运行、模型调用、工具调用与
  情节摘要总数现在来自有界的 Store 聚合读取（`CountVisibleMessages`、
  `CountVisibleRuns`、`ModelCallStats`、`CountToolCalls`、
  `CountEpisodeSummaries`，memory、file 与 PostgreSQL 三个后端均已实现），
  不再在每次抓取时列出全部记录。导出的指标名称与数值保持不变。
- Approval 携带 `arguments_immutable`，由新的 `ToolDefinition.ArgumentsImmutable`
  标志（`email.send` 置位）投影而来；执行器指纹校验、`POST /api/approvals/{id}/modify`
  的 `409`，以及 WebChat 的编辑入口都读取该标志而不再匹配工具名。Postgres 迁移
  `0010` 新增该列；升级前已持久化的 Approval 报告为 `false`，服务端仍拒绝修改。
- 邮件自动化：Gateway 的提供方注册表由 Controller 注册表生成（`provider_scripts.json`，
  用 `npm run sync:provider-contract --prefix tools/browser-controller` 重新生成）；
  Go 侧从未使用且已漂移的登录 URL 与允许 Origin 副本已删除。提供方脚本失败通过共享的
  `app.ToolErrorEmail*` 代码分类；QQ 邮箱的 `body_too_large` 现报告为 `email_invalid_input`
  而非 `email_provider_unavailable`。
- 集成设置：凭据检查 API 从未返回文档中的 `checking` 状态（在任何响应前即被覆盖），
  状态表已删除该行；WebChat 继续自行渲染进行中的标签。Browser control 在校验进行时
  仍上报 `checking`。
- 密封的 PPTX 候选不再在制品存储中无限堆积。审批通过的发布在完成状态落盘后立即
  删除候选字节与清单（审计事件 `document.pptx.candidate_discarded`）；每小时的
  保留协调器按有界分页清理 `pptx/sealed/` 下超过 24 小时审批 TTL 的对象（审计事件
  `document.pptx.candidate_expired`）。制品存储接口新增有界的
  `List(prefix, startAfter, limit)`：文件系统与 S3 后端已实现，未实现的后端返回
  显式错误，清理任务记录告警并跳过，而不是静默不做。
- Store：内存与 File 后端改用二分查找插入维护每个 session 的 tool call 与 episode 顺序，
  加载快照时每个 session 只排序一次，默认 File 后端的 Gateway 启动时间不再随 session 长度
  平方增长（4000 条 tool call 加 4000 条 episode 的 session 从 1.37 s 降到 7 ms）。
  recent-history 的排序契约保持不变。
- 配置：`SPARKCLAW_MODEL_MODE` 与 JSON 字段 `model.mock` 已退役；自模型容量契约落地以来，
  两者一直无效，因为所选容量 profile 的 `mock` 标志是 mock 路由的唯一来源。现在设置任一项都会
  在配置加载时报错，并提示改用 `model.capacity_profile` / `SPARKCLAW_MODEL_CAPACITY_PROFILE`。
  该变量已从 compose、env profile、部署校验器、CI、`run-eval.sh`（现根据容量 profile 推导
  `SPARKCLAW_EXPECT_REAL_MODELS`）和 `local-dev-env.sh` 中移除。
- 配置：`OPENAI_API_KEY` 成为正式配置项（`ModelConfig.APIKey`，仅限环境变量），由模型路由器
  在每次请求时附带，不再逐请求读取进程环境；非 mock lane 的 base URL 指向远程主机而密钥为空时，
  启动时记录一条警告。
- 配置：gateway 二进制不再内置构建机器上的 `configs/model.profiles.json` 路径；
  `model.capacity_catalog` 相对配置文件解析，内置默认值相对工作目录解析，
  `SPARKCLAW_MODEL_CAPACITY_CATALOG` 为显式覆盖。
- 配置：`docker/compose.yaml` 的 `SPARKCLAW_BROWSER_EXTENSION_CONNECT_TIMEOUT_MS` 回退值改为
  文档记载的 20000 ms（原为 15000）；新增契约测试将 Go 默认值与 `configs/sparkclaw.default.json`、
  `docker/env/sparkclaw.product.env` 和 compose 回退值逐项比对，包括产品 profile 有意设置的
  200000 字节阶段证据预算。
- 部署：`docker compose` 现在要求显式提供 `SPARKCLAW_MODEL_CAPACITY_PROFILE`
  （此前默认 `mock` 会在绕过部署脚本时把所有模型调用静默路由到 mock 路由器），
  vLLM 容量入口拒绝标记为 `mock` 的 profile，compose 中 `SPARKCLAW_PAIRING_REQUIRED`
  默认为 `true`，重复的 `dgx-spark-local` 容量 profile 已删除。
- 托管浏览器窗口（微信扫码登录）在打开后会交接给所有者；Playwright bridge 在后台打开
  标签页，此前页面已创建但从未显示。
- PPTX 最终渲染视觉 QA 在每次修复后重新审查整个授权页选择（此前没有修复项的页上的
  阻断问题可能在密封前消失），把缺失的 Python 运行时报告为渲染器不可用而不是证据完整性
  失败（shadow 阶段不再中止受治理的变更），并在修复预算耗尽时记录
  `pptx_render_repair_exhausted`。
- PPTX 最终渲染视觉 QA 会校验其所声明的渲染器栈：Gotenberg、LibreOffice 与 pypdfium2
  的 pin 成为配置项（`SPARKCLAW_PPTX_VISUAL_QA_{GOTENBERG,LIBREOFFICE,PDFIUM}_VERSION`，
  默认值不变，并由测试对照 Compose 与文档运行时的 pin），密封 manifest 记录配置值，
  运行中的 Gotenberg 或 pypdfium2 版本与 pin 不一致时在所有阶段都以
  `pptx_render_stack_mismatch` 终止准备。Fast 评估遗漏必需的 fact review 时报告为
  `pptx_render_model_invalid` 而不是 `pptx_render_model_unavailable`。
- 邮件自动化：提供方脚本的超时与输出无效代码映射为 `email_script_timeout` /
  `email_script_invalid_output` 而不是 `email_provider_unavailable`；`/api/email`
  错误携带 `code` 与 `retryable`；store 失败返回有界消息；控制器调用受脚本预算约束。
  失效的 `adapters.emailAutomation.scriptDir` 设置与 `SPARKCLAW_EMAIL_SCRIPT_DIR` 已移除。
- 集成设置：失败的 operator 或 household 激活可从设置面板重试，凭据写入失败不再
  破坏内存中的凭据列表。
- JingSi Runtime v1 路由共享网关限流器，运行状态无法持久化的执行会成为显式的 `failed`
  结果，`internal/contracttest` 的中央契约门禁在缺少同级检出时跳过（或读取
  `SPARKCLAW_JINGSI_CONTRACT_MANIFEST`）而不是让每个全新克隆失败。
- LocalMind grounding 只对显式 LocalMind 工作流的运行替换最终答案，且不再在每条
  路由消息上扫描完整会话工具调用历史；格式错误的 JingSi `max_tool_calls` 预算在
  step loop 中改为失败关闭，与工具暴露层一致。
- 浏览器控制器：显式映射 `browser_lane_unavailable` 与 `browser_controller_stopping`，
  无 deadline 的控制器调用获得 5 分钟兜底，CI 现在运行 browser bridge 测试套件。
  Bridge native host 与 Controller 的 Unix socket 现在从创建起即仅所有者可访问
  （在 `0077` umask 下绑定并在就绪前设为 `0600`），不再在监听器已接受连接后才收紧。
- 浏览器运行时（第七轮）：Playwright Tab List 与 Snapshot 解析以从固定版本 MCP/CLI
  包录制的 Golden 为准（此前 Fake 的形态是臆造的）；Bridge 在 `chrome.storage.session`
  中记录自己创建的标签组，Stale Cleanup 只关闭这些组（此前名为"SparkClaw task"的
  所有者标签组会被清空），因此新增 `storage` 权限并更新制品校验和；Relay 握手失败时
  关闭其 Socket；Gateway 关停与 Session 释放不再等待进行中的控制器调用（被挡在进行中
  操作之后的释放返回 `browser_busy` 并在后台完成）；`tools.browserAutomation.provider`
  必须为 `playwright-extension`，`adapters.browserAutomation.startupTimeoutMs`
  （现为获取 Session 的等待上限，500 到 30000 毫秒）会被校验；删除了无人读取的
  `require_visible_environment` 参数、从未发出的 `tabs.select` / `page.reload`
  控制器操作以及 `browser_click` 要求；浏览器控制状态上报 `cli` / `cli_version`；
  Controller 到 Gateway 的错误码只在一张共享表中定义。
- 实验性 JingSi LAN presentation route 统一移至 `/api/jingsi/v0/` 前缀
  （`POST /api/jingsi/v0/messages/stream`、`GET /api/jingsi/v0/client-events{,/head,/stream}`，
  面向手机的 readiness 探测改为 `GET /api/jingsi/v0/readyz`）。Gateway 自身现在会在这些
  route 上拒绝非私有地址 peer 与非私有 browser origin；LAN 端口可用
  `SPARKCLAW_JINGSI_LAN_PORT` 配置（默认 `18793`）。
- Qwen3-ASR image 现在由一个 SparkClaw-owned runtime 同时处理 batch/realtime，把全部 model
  call 串行固定在一个 owner thread，并在声明 ready 前完成首次 inference warm-up。Gateway
  只通过 authenticated single-use WebSocket ticket 暴露 realtime，并与 batch transcription
  共享 admission capacity。
- 开机启动现在为每个 Docker/NVIDIA readiness probe 设置硬超时，doctor 会发现过期的
  systemd unit，并默认把 Qwen3-ASR 纳入单 Fast 原子常驻组与 Gateway runtime；
  固定 ASR KV cache budget 避免 utilization 估算得到负的可用 cache。
- 部署启动现在把产品 template 对齐到 PostgreSQL，且不迁移旧 file snapshot；
  healthy/current 模型组会被保留，degraded 模型组会原子整组恢复，同时提供显式 force-refresh
  flag；WebChat host port 由一个经过校验的配置统一拥有；readiness 内置于 vLLM 镜像并使用
  best-effort tmpfs marker；boot reconciliation 由最长四小时的 oneshot systemd unit 约束。
- 受管微信 QR 登录 Chromium 窗口现在使用独立的 per-binding lock 与固定 10 分钟 sliding
  lease。30 秒 janitor 会重试失败的过期清理；graceful shutdown 会在 browser adapter 关闭前
  释放全部 tracked window；无关 owner 不再因另一窗口的 browser round trip 而串行等待。
- Connector 启用现已在一个家庭 Gateway 内按 owner 隔离：启动时把全部 owner 的持久化 setting
  恢复到 write-through cache；每 channel 一个共享 worker 使用 owner gate；一个 owner 关闭不会
  停止另一 owner 的 runtime；已接纳 reply 会排空，未 dispatch input 会暂停；预加载失败会阻止
  Gateway listen。`/api/config` 的 `operator_enabled` 现在返回真实静态启动默认值。
- 为文档决策、文档/浏览器模型阶段和最终化统一了消费者级证据投影与谱系/覆盖 audit；
  增加规范化文档操作候选、带 approval 前临时 layout/preservation 预检的一次受限 PPTX
  语义修复、PDF claim coverage、浏览器 transition 证据、重复 action 阻断和确定性 visible
  presentation equivalence。
- 默认 `npm start` 与安装器路径现在使用 PostgreSQL-backed 产品运行态，在 Gateway
  之前启动并等待 PostgreSQL，且不再应用原有的 file-backed `minimal` 覆盖。
- 模型服务 health check 与联合启动现在允许有界的数小时冷下载，不再因原有的短 ready
  窗口而过早失败。
- 将 `document.edit` 升级到 revision 6：XLSX 现在使用类型化有界 sheet 证据、绑定证据的
  workbook/cell/row/sheet 修改、只更新前缀的 `update_row`、六种明确 operation 选择边界和
  失败关闭的 OOXML package 校验来保护每个生成副本。
- 用当前可维护文档替换旧 planning、audit 和 handoff documents。
- 将 intent routing、messaging/scheduling、browser、document、integration 和 WebChat
  文档合并为六份当前专项手册和一份文档索引；删除 29 对已完成或被替代文档。
- 将 runtime skill packages 排除出 bilingual documentation mirror，因为 skills 独立演进。

- `GET /readyz` 的常驻服务状态现在通过新的有界读取
  `RunRepository.LatestModelCallsByLane` 只读取每个 lane 最新一条 model call
  （Postgres 使用 `DISTINCT ON`，并由 schema migration `0009` 为 `model_calls`
  增加 `(lane, started_at DESC, id DESC)` 索引），不再在 WebChat 每 5 秒轮询时
  加载全部已持久化的 model call。

### Validated

- 已验证 Qwen3-ASR candidate 的冷启动 readiness 与首请求预热、batch output parity、真实
  4.439 秒 partial/final stream、按录音速度发送且未超过 5 秒 backpressure bound 的 60 秒
  stream，以及 realtime/batch capacity 的互斥与释放；desktop/mobile fake-microphone pass
  也验证了 AudioWorklet 到草稿的完整路径，健康路径未发起 batch request。
- 证据投影改动通过 Gateway build/test/vet、WebChat test/build、双语文档检查、doctor
  和 47 条隔离 mock/file golden eval。
- PostgreSQL 产品启动 Compose 选择与 readiness。
- WebChat production build。
- Gateway skill registry test。
- Docker Compose config validation。
- `scripts/doctor.sh`。
- Markdown link 和 language-switch checks。
