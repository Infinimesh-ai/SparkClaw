# SparkClaw 部署

> 语言： [English](../../docs/deployment.md) | 简体中文

本文档定义 SparkClaw 唯一支持的两套产品部署。全本地部署在 NVIDIA GB10 上拥有五个
模型服务；全远端部署使用五个版本化公网模型端点。两者都运行 PostgreSQL、Sandbox Runner、
Gotenberg、Gateway 与 WebChat。

## 前置条件

- NVIDIA DGX Spark（GB10 GPU）、Linux/ARM64，以及至少 100 GiB 系统/统一内存；
  已验证的操作系统为 Ubuntu 24.04。
- Docker Engine、Docker Compose plugin、NVIDIA driver/container toolkit、`curl`、systemd，
  具备安装开机服务的 `sudo` 权限，并能访问 container registry 与 Hugging Face。
- 冷启动的模型与镜像缓存至少预留 125 GiB；已有部分缓存时，部署脚本会计算剩余需求。
- 用于本地模型下载的 Hugging Face token。不要提交 `.env.local` 或 `.env.remote`。

Node.js 26/npm 11 与 Go 1.25 只用于宿主开发，容器化部署不依赖它们。

## DGX Spark 一键部署

从已准备好的 DGX Spark 宿主开始：

```bash
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 300 https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/main/install.sh | bash
```

项目网站可以原样提供仓库根目录的 `install.sh`，并在上述命令中使用自己的 HTTPS URL；
不要通过明文 HTTP 发布安装器。

流式 bootstrap 与部署入口会：

1. 把指定 branch/tag 克隆到 `$HOME/SparkClaw`，或 fast-forward 已有的干净 checkout；
   存在本地改动或历史分叉时不改动目录并明确报错。由原官方仓库地址创建的干净 checkout
   会在成功 fast-forward 后迁移到当前 origin；其他 origin 不匹配仍会被拒绝。
2. 将 curl 管道的 stdin 重新连接到 `/dev/tty`，让 Hugging Face token 保持隐藏式交互输入。
3. 硬性校验 Linux/ARM64、NVIDIA GB10、至少 100 GiB 内存、Docker Compose、
   `nvidia-smi` 与磁盘空间。
4. 创建或保留权限为 `0600` 的 `.env.local`，接收不回显的 Hugging Face token，并把 bind mount
   数据目录对齐到当前用户。
5. 使用 vLLM 的 Hugging Face 集成，将 Fast、embedding、guard、Qwen3-ASR 与 OvisOCR2
   下载到共享的 `data/models` 缓存。
6. 等待模型 ready 以及 Fast/Guard 预热，构建 Gateway、Sandbox Runner 与 WebChat，
   最后验证 Gateway 和 WebChat。
7. 为部署用户安装并启用系统级 `sparkclaw-autostart.service`；安装过程不会重启当前运行实例。

首次运行约下载 70-85 GiB 模型数据以及容器镜像，可能持续数小时。模型 health check 与
联合启动共用默认三小时窗口；下载链路较慢时可把
`SPARKCLAW_MODEL_STARTUP_TIMEOUT_SECONDS` 设置为更大的正整数。后续运行会复用已下载的
checkpoint 与容器镜像。running、healthy 且配置一致的模型组会被保留；只有 degraded 或
drifted 的模型组才使用新的 GPU runtime 状态与进程本地 cache 进行重建。

非交互部署应在启动管道前导出 token；部署脚本只将其持久化到已忽略且权限为 `0600` 的
本地环境文件：

```bash
export HF_TOKEN=hf_example
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 300 https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/main/install.sh | bash
```

安装/更新仓库后只运行部署预检：

```bash
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 300 https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/main/install.sh | \
  bash -s -- --check
```

bootstrap 默认使用 `main` 与 `$HOME/SparkClaw`。在 `bash` 进程上设置
`SPARKCLAW_GIT_REF` 或 `SPARKCLAW_INSTALL_DIR`，可以固定 release 或更换安装目录。
已有仓库可直接运行：

```bash
npm run deploy:local
```

## 产品入口

| 部署 | 首次部署 | Reconcile/start | 配置 |
|---|---|---|---|
| 全本地 | `npm run deploy:local` | `npm run start:local` | product env、Local env，再加载 `.env.local` |
| 全远端 | `npm run deploy:remote` | `npm run start:remote` | product env、Remote env，再加载 `.env.remote` |

这四条命令是唯一产品入口。宿主机调试命令和定向模型 benchmark helper 不是部署模式。
已退役的 `online` 名称与托管 chat 加本地辅助模型的混合运行态不再受支持。

WebChat 是唯一应用入口，host port `18790` 默认绑定 `0.0.0.0`。设置
`SPARKCLAW_WEBCHAT_PORT` 可以发布另一个 host port；容器与 Nginx listener 仍使用内部
端口 `18790`。Gateway 不发布 host port；WebChat 通过私有 `sparkclaw_internal` network，
把选定路由代理到 `gateway:18789`。WebChat 必须只在本机可达时设置
`SPARKCLAW_WEBCHAT_BIND=127.0.0.1`。两个值都从所选私有覆盖文件读取，非法端口会在修改
容器前失败。
模型、状态服务和 sandbox runner 仍绑定 localhost 或私有 Docker network。

两种产品模式都要求 Gateway 配对，同时保持 `SPARKCLAW_API_TOKEN` 为空。部署入口会在所选
mode-`0600` 私有环境文件中生成随机 `SPARKCLAW_WEBCHAT_PROXY_TOKEN`，并且只注入 Gateway
与 WebChat reverse proxy。Nginx 只在 `http://127.0.0.1:18795` 暴露的精确配对 bootstrap
路由上使用该凭据；此 listener 不向局域网发布。首次进入产品 WebChat 时，应在 SparkClaw
宿主浏览器打开 `http://127.0.0.1:18790` 并选择“配对”。WebChat 会把返回的逐客户端 Gateway
token 保存在该浏览器中。局域网浏览器不能自行配对，必须在现有 token 表单中输入由 Owner
预先提供的 Gateway client token。Gateway client token、MCP Access Ticket 与 Playwright
Extension token 是三类彼此独立的凭据。

## 远端部署

远端部署拥有 SparkClaw 应用与持久化状态，但不运行模型容器。在 Ubuntu 上，以具备 sudo
权限的普通用户运行：

```bash
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --connect-timeout 15 --max-time 300 \
  https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/refs/heads/main/install-remote.sh | bash
```

bootstrap 会在需要时安装 Git，安全 clone 或 fast-forward 仓库，将 stdin 重新连接到终端，
并调用 `deploy:remote`。缺少 Docker Engine 与 Compose 时会自动安装；脏或分叉 checkout
绝不会被覆盖。

版本化 `docker/env/sparkclaw.product.env` 拥有共享业务行为、容量、凭据路径、ISCP 默认值与
应用生命周期输入。`docker/env/sparkclaw.remote.env` 只拥有 Remote 部署标记和以下五个公网
模型服务：

| 服务 | Base URL |
|---|---|
| Fast 与逻辑 Deep | `https://sparkclaw.infinimesh.cloud/fast/v1` |
| Embedding | `https://sparkclaw.infinimesh.cloud/embedding/v1` |
| Guard | `https://sparkclaw.infinimesh.cloud/guard/v1` |
| ASR | `https://sparkclaw.infinimesh.cloud/asr` |
| OCR | `https://sparkclaw.infinimesh.cloud/ocr/v1` |

`.env.remote` 只保存凭据和机器专属覆盖项。可用 `--configure` 输入可选共享
`OPENAI_API_KEY`；endpoint 不需要 Bearer 认证时直接留空。ASR 必须填写 service root，
因为 Gateway 会追加 `/v1/audio/transcriptions`；OCR 使用 OpenAI-compatible `/v1` base URL。

修改容器前，remote 启动会校验每个模型 URL，拒绝 `sparkclaw-*` Compose service name、
`localhost`、`*.localhost`、`127.0.0.1`、`::1`、Docker host/gateway alias、常见本地域名
后缀、单标签本地主机名，以及所有非公网 IP literal，包括 RFC1918、unique-local、loopback、
unspecified、link-local、reserved 与 multicast 地址。随后显式停止共享 Compose project 中的
本地模型容器，再准确启动 PostgreSQL、Sandbox Runner、Gotenberg、Gateway 与 WebChat。
五个应用服务都使用 `restart: unless-stopped`。

部署会安装或校验固定宿主 Chromium、Checksum-pinned SparkClaw Browser Bridge 与
Owner-scoped Playwright Controller。它要求有效 Owner X11/XWayland Session，启动持久 Headed
Browser，校验已加载 Bridge Version 与 Private Controller Socket，等待 Gateway Ready，执行
容器侧 Controller Smoke，并证明 Chromium 随后仍存活。不存在 Headless 或 Remote-debugging
Startup Path。

远端运行态的 reconcile、重新配置与只读检查：

```bash
npm run start:remote
bash scripts/deploy_remote.sh --configure
bash scripts/deploy_remote.sh --check
bash scripts/setup-browser.sh --check
```

两种部署模式使用相同的生产 Browser Setup。它创建 Owner-only Controller、MCP Output、CLI
Runtime、Profile 和 Native Messaging Host Directory，只把固定 Controller Path 写入所选
mode-`0600` 环境文件。这些目录不持久化 Extension Token，也不形成 Email Message Archive。
Bridge Token 通过 `设置 > 连接 > 浏览器控制` 登记，并在 Gateway Vault 中保持加密。

手动登录或 Human Verification 时使用 Desktop Launcher 或以下命令：

```bash
npm run open:browser
```

账户在持久 SparkClaw Profile 中完成登录后，在宿主机运行固定只读 Provider Probe：

```bash
npm run qualify:playwright-email -- --profile remote
```

可用 `--providers qq_mail`、`--providers outlook` 或 `--providers gmail` 隔离单个 Provider。
该 Helper 会校验并合并所选产品环境，把容器 PostgreSQL 与 Controller Path 映射到固定宿主机
Endpoint，通过 Owner-only Key 打开现有 Vault Credential，并且只调用
`TestPlaywrightExtensionLiveEmailProbes`。它不接受 URL、Selector、Script Path、邮件内容或 Send
Operation；任一所选账号未通过 Login Probe 时都会非零退出。

2026-09-04 的 Remote 资格验证基线通过上述精确命令依次完成 QQ 邮箱、Outlook 和 Gmail，
总耗时约 94 秒。QQ 邮箱的两轮已登录 Evidence 使用 Playwright CLI-only 的 90 秒 Probe
Deadline；其他 Probe 保持 45 秒上限。关闭官方扩展任务页可能在 `tab-close` 返回前终止 CLI
连接，因此 Controller 只识别这一精确的 Page-closed 终态，并随后回收 Metadata-bound Daemon。
资格 Chromium 保持运行，私有 Runtime Directory 恢复为空，且没有调用 Send Operation。

Gateway 仍只在 Docker 内部可达，WebChat 默认发布 `18790`，精确配对 bootstrap 则只在宿主
回环地址 `18795` 可达。该拓扑不安装 TLS 或防火墙规则，主 WebChat 端口应只放在 Owner
可信网络中。

## Product Runtime

产品模式只能通过明确的 local 或 remote 入口选择：

```bash
npm run start:local
npm run start:remote
```

local 启动把 Fast、embedding、guard、ASR 与 OCR 作为一个常驻模型组。健康且配置一致的
模型组会保留；任一成员缺失、停止、不健康或漂移时，完整模型组会 force-recreate。随后启动
PostgreSQL、Sandbox Runner、Gotenberg、Gateway 与 WebChat，并要求 Gateway 在 PostgreSQL
state 和 external model adapter 下 ready。

remote 启动按全远端 profile 校验六个逻辑 adapter URL，停止共享 Compose project 中的全部
本地模型容器，再启动同样的五个应用服务。它不会根据当前运行的容器推断 Fast 或 Deep。

两种模式都选择 `sparkclaw-product-v1`：Fast 与逻辑 Deep 共享 262,144-token Remote context
与 output budget；embedding、guard、OCR 分别使用 8K、8K、32K context 契约。本地模型 serving
接收同一 profile，私有文件或环境变量不能让它选择更小的容量契约。

### 开机自启动

local 部署从版本化 Local profile 默认启用宿主机开机自启动；`.env.local` 只能覆盖该生命周期设置：

```dotenv
SPARKCLAW_AUTOSTART_ENABLED=true
SPARKCLAW_AUTOSTART_READY_TIMEOUT_SECONDS=600
SPARKCLAW_AUTOSTART_PROBE_TIMEOUT_SECONDS=10
```

开机时，`sparkclaw-autostart.service` 以部署用户身份运行，在配置的总时限内等待 Docker 与
NVIDIA runtime 就绪，并用单次探针时限约束每条 readiness 命令；随后调用
`start:local`。它会保留健康模型组，或在模型
组 degraded 时自动 force-recreate，随后才启动应用服务。该 unit 是带
`RemainAfterExit=yes` 的 `Type=oneshot`；reconciliation 期间保持 activating，并在固定
`TimeoutStartSec=4h` 后失败，而不会永久等待。五个应用容器同时使用共享的
`restart: unless-stopped` 策略；systemd 仍负责宿主启动后的完整模型组与产品 reconcile。

设置 `SPARKCLAW_AUTOSTART_ENABLED=false` 后，下次开机会跳过启动。systemd 单元仍保持
enabled，因此把配置改回 `true` 就足够，下一次开机会重新读取。仓库移动后可执行以下命令
安装或刷新单元：

```bash
npm run autostart:install
systemctl status sparkclaw-autostart.service
journalctl -u sparkclaw-autostart.service -b
```

安装单元不会重启当前实例。若不重启主机而直接应用配置，可运行 `sudo systemctl restart
sparkclaw-autostart.service`。健康模型组会被保留。需要完整刷新时，先在 `.env.local` 设置
`SPARKCLAW_FORCE_MODEL_RECREATE=true`，重启 unit，再把该设置恢复为 `false`，供后续 boot
使用。

检查状态：

```bash
docker ps --filter name=sparkclaw
curl -fsS http://127.0.0.1:18790/readyz
bash scripts/doctor.sh
```

本机打开 WebChat：[http://127.0.0.1:18790](http://127.0.0.1:18790)；同一局域网的
其他设备使用 `http://<主机局域网-IP>:18790`。首次自配对必须在 SparkClaw 宿主浏览器完成；
局域网浏览器需要输入已预先提供的 Gateway client token。

### JingSi LAN 呈现（实验性）

base stack 不发布 JingSi presentation port。要为一个现有 visible WebChat session 启用已实现的
SparkClaw 侧，由 operator 选择 session ID，再选择一个实际分配给本机的 RFC1918 address：

```bash
curl -fsS http://127.0.0.1:18790/api/sessions | jq -r \
  '.sessions[] | select(.hidden != true and .source == "webchat") | [.id, .title] | @tsv'
ip -4 -o addr show scope global

export SPARKCLAW_JINGSI_LAN_BIND=192.168.1.20
export SPARKCLAW_JINGSI_SESSION_ID=sess_replace_with_selected_id
bash scripts/restart_jingsi_lan_compose.sh remote
```

该操作只增加端口 `18793`（可用 `SPARKCLAW_JINGSI_LAN_PORT` 覆盖）上的精确 presentation
allowlist；WebChat 仍在 `18790`，Gateway 仍为
Docker-internal。helper 会拒绝 wildcard、public、hostname 和 malformed bind。它只应用于当前
runtime restart；之后执行普通 product restart 后若仍需此实验模式，必须再次运行。首期刻意没有
鉴权和 TLS，只能用于可信 LAN。route contract、Android 侧工作和实体验证见
[JingSi 局域网 Web 客户端设计](jingsi-lan-connection-design.md)。

golden eval script 会访问仅限内部的 `/chat` 与 `/metrics` route，因此有意使用隔离的 host
development Gateway，而不经过产品 WebChat 入口。先启动该 Gateway，再运行：

```bash
BROWSER_FIXTURE_URL=http://host.docker.internal:18791 \
BROWSER_FIXTURE_BIND=0.0.0.0 \
GATEWAY_URL=http://127.0.0.1:18789 \
bash scripts/run-eval.sh
```

`SPARKCLAW_BROWSER_READ_ALLOW_HOSTS` 是刻意显式的设置。它允许 eval fixtures 工作，同时保持 `browser.read` 默认拒绝 private hosts。

## Host Development Runtime

开发时使用与产品相同的明确启动命令：

```bash
npm run start:local
npm run start:remote
```

两条命令都会重建发生变化的应用 image。不再提供局部 Gateway/WebChat 产品入口，因为它会
绕过模式选择与 remote 停止本地模型的边界。

仅在隔离的宿主进程调试中，才在两个 terminal 分别运行 mock/file Gateway
和 Vite server：

```bash
npm run dev:gateway:host
npm run dev:webchat:host
```

Host WebChat dev server 监听 `0.0.0.0:18790`，并把 API 请求代理到仅监听
loopback 的 Gateway。受保护的宿主进程运行态应把 `SPARKCLAW_API_TOKEN`
和 `VITE_SPARKCLAW_API_TOKEN` 设为相同值。

## 外部 MCP 连接

Owner-facing External MCP 表面已安装但默认关闭。只有 SparkClaw 可以向真实 ISCP Domain
authority 请求标准 `iscp.pairing_ticket.v2` 对象时才会 ready：

```dotenv
SPARKCLAW_ISCP_PAIRING_ENABLED=true
SPARKCLAW_ISCP_DOMAIN_ID=<sparkclaw-domain-id>
SPARKCLAW_ISCP_AUTHORITY_URL=https://authority.example/v1/pairing-tickets
SPARKCLAW_ISCP_AUTHORITY_TOKEN_ENV=SPARKCLAW_ISCP_AUTHORITY_TOKEN
SPARKCLAW_ISCP_AUTHORITY_TOKEN=<authority-client-token>
```

若 secret 以 mode `0600` 文件挂载，则用 `SPARKCLAW_ISCP_AUTHORITY_TOKEN_FILE` 替代
`TOKEN_ENV`。必须且只能配置一种 token 来源。authority URL 除 loopback/private 开发服务外
必须为 HTTPS；默认请求边界为 15 秒和 64 KiB。

这是 SparkClaw authority adapter contract，不是 ISCP v0.1.0 当前定义的 HTTP endpoint。
SparkClaw 发送带认证的 `POST`，包含 `sparkclaw.iscp_pairing.request.v1` type、稳定
`request_ref`、配置的 `domain_id`、`max_uses: 1` 和 `ttl_seconds`。authority 响应只包含
`authority_ref` 与签名标准 `ticket` 对象。签名、消费、Device Proof、Provisioning、Trust
Grant 和 Relay credential 仍由 authority 负责；SparkClaw 不存储签名 ticket，也不暴露 claim
endpoint。

配置完成后，在 WebChat 设置中开启通用 MCP connector 与“通过 ISCP 连接”，签发并传递
单次展示的 ISCP Pairing Ticket，让 external Access Gateway 完成 enrollment，然后按固定
conversation scope 签发独立 MCP Access Ticket。关闭 ISCP 开关会立即拒绝 MCP Bridge ingress，
但不会删除已有 onboarding、ticket 或 binding record。生产端到端访问仍需要真实 authority
实现、external Access Gateway 和 Relay 实机链路。

### 局域网直连 MCP

直连 MCP 复用现有 WebChat 入口，不发布 Gateway，也不增加单独端口。在 WebChat 设置中开启
“允许局域网访问”，然后连接：

```text
URL: http://<sparkclaw-lan-ip>:18790/mcp
Initial Authorization: Bearer <SPARKCLAW_MCP_ACCESS_TICKET>
MCP-Protocol-Version: 2025-06-18
```

WebChat 始终监听 `18790`，但 Nginx 只把精确的 `/mcp` 路由代理到内部 Gateway。该开关是
应用授权门：关闭时 `/mcp` 返回 404。Gateway `18789` 没有 host port 映射。未配置 ISCP
Domain 时，直连使用 `SPARKCLAW_MCP_LOCAL_DOMAIN_ID`，默认值为
`sparkclaw-local`；已配置的 ISCP Domain 优先，因此两种 transport 共用同一个 access-ticket
domain。

MCP Access Ticket 有效期为 24 小时且只能使用一次。首次 `initialize` 会消费 ticket 并返回
`Mcp-Session-Id`；client 应在 `notifications/initialized`、`tools/list` 与 `tools/call` 中继续
携带该 header。session ID 是 bearer credential，不得写入日志或源码；SparkClaw 只存储其
SHA-256 派生身份。局域网直连使用明文 HTTP，不提供 ISCP encryption、Device Proof、Relay
或 Trust Grant。必须限制在可信局域网内，并撤销不再使用的 MCP Binding。MCP 调用使用独立
MCP Access Ticket，不需要 `SPARKCLAW_API_TOKEN`；后者启用时保护的是另一条 owner
WebChat/Gateway API。

`/mcp` 会校验浏览器 `Origin` header，作为 DNS-rebinding 防御。不携带 `Origin` 的请求
（curl、LocalMind 等原生 MCP client）不受影响。携带该 header 时，其值必须是 loopback
origin、Gateway 自身 bind 地址对应的 origin，或 `mcp_access.allowed_origins` 中的条目
（也可通过逗号分隔的 `SPARKCLAW_MCP_ALLOWED_ORIGINS` 设置）；否则返回 403。该列表默认为
空——仅当存在部署在其他 origin 的可信浏览器 MCP client 时，才加入形如
`https://panel.example.com` 的精确 origin。

## LocalMind MCP

LocalMind 连接默认不启用。按[外部集成](integrations.md)说明，在当前 SparkClaw JSON 配置中
加入 `mcp_servers.localmind` block，然后在部署环境中提供它引用的值：

```bash
LOCALMIND_MCP_URL=https://localmind.example/api/workspaces/<workspace-id>/mcp
LOCALMIND_MCP_TOKEN=<workspace-bound-token>
```

`docker/compose.yaml` 会把这两个变量转发给 Gateway。出厂 LocalMind entry 在任一值为空时
保持 inert。token 不得进入已提交配置。修改 JSON entry 后重启 Gateway；Gateway 会校验固定
server identity 与精确三工具 task contract，之后按配置间隔重复刷新。

若 containerized Gateway 访问 host 上的 LocalMind，请把 `localhost` 替换为
`host.docker.internal`。连接到 `sparkclaw_internal` 的 LocalMind service 可使用 Compose
service name。公网 endpoint 必须使用 HTTPS；private/container HTTP 还必须设置
`allow_private_http: true`。endpoint path 必须严格保持为
`/api/workspaces/<workspace-id>/mcp`。

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

两份版本化产品 profile、四条产品入口与 local boot service 都选择 PostgreSQL。升级时不会
迁移、导入或删除旧的 `data/memory/gateway-state.json`；PostgreSQL 产品 runtime 从数据库中
已有的 records 启动。项目仍为 pre-release，不提供 file 到 PostgreSQL 的迁移工具。

隔离 host/mock 运行使用的 file state：

```text
data/memory/gateway-state.json
```

常用选项：

```bash
SPARKCLAW_STATE_BACKEND=memory
SPARKCLAW_STATE_PATH=/path/to/state.json
SPARKCLAW_STATE_STARTUP_TIMEOUT_SECONDS=180
SPARKCLAW_STATE_READ_TIMEOUT_SECONDS=10
SPARKCLAW_STATE_WRITE_TIMEOUT_SECONDS=30
SPARKCLAW_STATE_TRANSACTION_TIMEOUT_SECONDS=60
SPARKCLAW_STATE_ENCRYPT_AT_REST=true
SPARKCLAW_STATE_ENCRYPTION_KEY_FILE=/path/to/key
```

Postgres-backed state：

```bash
sudo -n docker compose \
  --env-file docker/env/sparkclaw.product.env \
  --env-file docker/env/sparkclaw.local.env --env-file .env.local \
  -f docker/compose.yaml --profile product up -d postgres

SPARKCLAW_STATE_BACKEND=postgres \
SPARKCLAW_STATE_DSN='postgres://sparkclaw:sparkclaw@127.0.0.1:15432/sparkclaw?sslmode=disable' \
SPARKCLAW_STATE_STARTUP_TIMEOUT_SECONDS=180 \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

`state.backend` 会先 trim 并统一大小写，只允许 `memory`、`file` 或 `postgres`。File state
在 load 后必须得到 normalized absolute path。启用 file encryption 时，direct key 与可读且非空的
key file 必须且只能配置一个。PostgreSQL 需要非空 DSN；同时设置时，legacy
`SPARKCLAW_POSTGRES_DSN` 仍优先于 `SPARKCLAW_STATE_DSN`。Store startup 默认 180 秒，
允许范围为 1 到 900 秒。read、write、transaction operation budget 默认分别为 10、30、60
秒，每项允许范围均为 1 到 900 秒，并保留更短的 caller deadline。

所选 backend probe 成功后 Gateway 才开始 listen。Runtime supervision 将 backend 标为 unready
时，`/readyz` 返回 `503` 与有限 Store status；recovery probe 会周期 retry。`/metrics` 导出
`sparkclaw_store_ready`、active operation、operation total 与总 duration，只使用有限的
backend/repository/operation/mode/outcome label，不暴露 state path、DSN、owner ID、record ID
或 raw error。Shutdown 会拒绝新 Store operation，在 close deadline 内 drain 已获准工作，最后
关闭 backend。完整 state machine 与 failure contract 见 [Store](store.md)。

Gateway 会在 readiness 前应用内嵌的有序 schema。新 runner 由 advisory lock 串行化，
不可变 filename/checksum 记录到 `sparkclaw_schema_migrations`。没有 ledger 的数据库作为
unversioned adoption candidate：全部 migration、兼容 copy/normalization、精确 catalog 校验和
ledger row 原子提交。checksum 漂移、未知或有缺口的版本、不兼容 legacy natural key 或 catalog
漂移都会让 startup 失败，不接受 partial migration。PostgreSQL image 不再把 schema SQL 复制到
`docker-entrypoint-initdb.d`。

S1 是非滚动数据库升级。启动拥有这些 migration 的新 binary 前，必须停止所有旧 Gateway
process。migration 还会锁住四张 Weixin/external 兼容表直到 commit，以阻止旧 writer；这只是
backstop，不是 rolling-upgrade protocol。

项目标准 data service image 仍保留 PostgreSQL 18 with pgvector，但 Workspace Knowledge/RAG
暂缓期间，Gateway 不创建或查询 Document Chunk/Vector Schema。

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
sudo -n docker compose \
  --env-file docker/env/sparkclaw.product.env \
  --env-file docker/env/sparkclaw.local.env --env-file .env.local \
  -f docker/compose.yaml --profile eval up -d minio minio-init
```

Artifacts 包括 tool observations、browser snapshots、generated documents/media、
memory exports、patch rollback files 和 eval failure archives。

## Sandbox Runner

Host binary 运行时，Gateway 可使用 `SPARKCLAW_SANDBOX_BACKEND=local-docker`。

Compose 使用独立 sandbox runner：

```bash
sudo -n docker compose \
  --env-file docker/env/sparkclaw.product.env \
  --env-file docker/env/sparkclaw.local.env --env-file .env.local \
  -f docker/compose.yaml --profile product up -d sandbox-runner
```

Compose 外的 standalone runner：

```bash
sparkclaw-sandbox-runner
SPARKCLAW_SANDBOX_BACKEND=http \
SPARKCLAW_SANDBOX_RUNNER_URL=http://127.0.0.1:18889 \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

如果 runner 访问 host Docker socket，且 host 与 container 看到的 workspace path 不同，需要设置 `SPARKCLAW_SANDBOX_HOST_WORKSPACE_ROOT` 和 `SPARKCLAW_SANDBOX_CONTAINER_WORKSPACE_ROOT`。


## 产品 Reconcile

不要通过单个 Compose service 拼装产品运行态。使用拥有完整模式边界的入口：

```bash
npm run start:local
npm run start:remote
```

两条路径都会在 Gateway Startup 前验证 Browser Bridge 与 Controller，等待 PostgreSQL 与
Gotenberg，要求 Gateway 以 `model_mode=external` 和 `state_backend=postgres` Ready，执行
Controller Smoke，并证明 Chromium 在 Client Detach 后仍存活。Local 额外拥有五模型组；
Remote 拒绝本地模型 URL，并在应用启动前停止该模型组。

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

不传参数时，`serve_models_compose.sh` 也会选择 `single-fast`。这是当前 Local 模型启动路径：
它会停止此前运行的 Deep 容器，并从 `docker/compose.models.local.yaml` 同时启动 Fast、embedding、
guard、ASR 和 OCR。Deep 与 dual-light 命令仅作为显式测试/benchmark 入口。命令会等待
所有选中服务进入 healthy。
Fast 必须先成功完成一次贴近生产负载的有界 `/chat/completions` 请求才会变为 healthy。
当前合成输入在 Qwen3.6 tokenizer 上约为 3.4K token，并强制解码 480 token，让启动阶段承担
Tree routing 会遇到的长 prompt 与生成冷路径。Guard 另行执行较小的有界 completion。
readiness helper 会复制进以 `SPARKCLAW_VLLM_IMAGE` 为 base 的本地派生镜像，healthcheck 不再
bind-mount checkout 中的 source file。每个 marker 都存放在专用的容器本地 tmpfs，并包含
当前模型服务进程的启动时刻，因此新进程不能复用上一个进程的 readiness。成功 warmup 后即使
marker 无法写入，服务仍保持 healthy；如果连专用 tmpfs 也不可用，后续 probe 可能再次
warmup。marker 成功保存后，周期健康检查改用轻量模型列表 endpoint。如果五服务产品组中有
任一服务缺失、停止、不健康或配置漂移，快捷命令会 force-recreate 全部五个服务；
`SPARKCLAW_FORCE_MODEL_RECREATE=true` 会对健康模型组执行相同操作。脚本绝不会在常驻产品组
中单独增加或重建一个模型。单 Fast Embedding endpoint 在固定 2 GiB KV budget 下允许 128 条
短 sequence，使 110 项启动索引在 20 秒时限内完成。

默认 endpoints：

| Endpoint 角色 | Served name | Endpoint |
|---|---|---|
| fast | `sparkclaw-fast` | `http://127.0.0.1:8001/v1` |
| deep | `sparkclaw-deep` | `http://127.0.0.1:8002/v1` |
| embedding | `sparkclaw-embedding` | `http://127.0.0.1:8003/v1` |
| guard | `sparkclaw-guard` | `http://127.0.0.1:8005/v1` |
| asr | `sparkclaw-asr` | `http://127.0.0.1:8006` |
| OCR adapter | `sparkclaw-ocr` | `http://127.0.0.1:8007/v1` |

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

- `SPARKCLAW_MODEL_CAPACITY_PROFILE`（两个产品模式固定为 `sparkclaw-product-v1`；定向 benchmark helper 可选择另一个已测 profile）
- `SPARKCLAW_MODEL_CAPACITY_CATALOG`（高级 host script/catalog 路径 override；产品容器使用已挂载的版本化 catalog）
- `SPARKCLAW_VLLM_IMAGE`（embedding、guard 与 ASR 的基础 image）
- `SPARKCLAW_CHAT_VLLM_IMAGE`（Fast/Deep chat image；NVFP4 默认使用 vLLM 0.24.0）
- `SPARKCLAW_FORCE_MODEL_RECREATE`（默认 `false`；一次显式完整模型组刷新时设为 `true`）
- `SPARKCLAW_FAST_MODEL_ID`, `SPARKCLAW_FAST_MODEL`, `SPARKCLAW_FAST_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_FAST_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_FAST_MAX_NUM_SEQS`, `SPARKCLAW_FAST_SPECULATIVE_CONFIG`
- `SPARKCLAW_DEEP_MODEL_ID`, `SPARKCLAW_DEEP_MODEL`, `SPARKCLAW_DEEP_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_DEEP_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_DEEP_MAX_NUM_SEQS`, `SPARKCLAW_DEEP_SPECULATIVE_CONFIG`
- `SPARKCLAW_EMBEDDING_MODEL_ID`, `SPARKCLAW_EMBEDDING_MODEL`, `SPARKCLAW_EMBEDDING_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_EMBEDDING_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_EMBEDDING_MAX_NUM_SEQS`
- `SPARKCLAW_GUARD_MODEL_ID`, `SPARKCLAW_GUARD_MODEL`, `SPARKCLAW_GUARD_SERVED_NAME`, `SPARKCLAW_GUARD_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_GUARD_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_GUARD_MAX_NUM_SEQS`
- `SPARKCLAW_ASR_MODEL_ID`, `SPARKCLAW_ASR_SERVED_NAME`, `SPARKCLAW_ASR_MAX_MODEL_LEN`, `SPARKCLAW_ASR_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_ASR_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_ASR_MAX_NUM_SEQS`, `SPARKCLAW_ASR_DTYPE`
- `SPARKCLAW_SPEECH_ENABLED`, `SPARKCLAW_SPEECH_BASE_URL`, `SPARKCLAW_SPEECH_ALLOWED_HOSTS`, `SPARKCLAW_SPEECH_MODEL`, `SPARKCLAW_SPEECH_TIMEOUT_SECONDS`, `SPARKCLAW_SPEECH_MAX_AUDIO_SECONDS`, `SPARKCLAW_SPEECH_MAX_UPLOAD_BYTES`
- `SPARKCLAW_OCR_ENABLED`、`SPARKCLAW_OCR_PROVIDER`（`openai-http` 为默认且目前唯一的适配器；`disabled` 显式关闭适配器）、`SPARKCLAW_OCR_BASE_URL`, `SPARKCLAW_OCR_ALLOWED_HOSTS`, `SPARKCLAW_OCR_MODEL`, `SPARKCLAW_OCR_TIMEOUT_SECONDS`, `SPARKCLAW_OCR_MAX_UPLOAD_BYTES`, `SPARKCLAW_OCR_MAX_OUTPUT_BYTES`, `SPARKCLAW_OCR_MAX_CONCURRENCY`, `SPARKCLAW_OCR_MAX_PENDING`
- `SPARKCLAW_OCR_IMAGE`, `SPARKCLAW_OCR_MODEL_ID`, `SPARKCLAW_OCR_SERVED_NAME`, `SPARKCLAW_OCR_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_OCR_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_OCR_MAX_NUM_SEQS`
- `SPARKCLAW_MODEL_DISABLE_THINKING`
- `SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS`
- `HF_TOKEN` 或 `HUGGING_FACE_HUB_TOKEN`

`*_MODEL_ID` 是 serving container 加载的 Hugging Face checkpoint；`*_MODEL` 是 Gateway 发送的 OpenAI-compatible served name。
Fast、Deep、Embedding、Guard 与 OCR 的 context/output capacity 没有环境变量 override。设置已退役的
`*_CONTEXT_TOKENS`、`*_MAX_INPUT_TOKENS`、`*_MAX_TOKENS` 或 `*_MAX_MODEL_LEN` 容量变量时，
配置会直接失败，不会修改或修复所选 profile。

### 专用 Qwen3Guard

guard lane 使用公开的生成式 checkpoint `Qwen/Qwen3Guard-Gen-0.6B`；
`Qwen/Qwen3Guard-0.6B` 不是有效的公开 checkpoint ID。只启动 guard endpoint：

```bash
SPARKCLAW_MODEL_LOADING_PROFILE=single-fast scripts/serve_models_compose.sh guard
curl -fsS http://127.0.0.1:8005/v1/models
```

共享产品 profile 为 guard 提供 8K context；Local 基础设施使用 2 GiB KV cache、
单序列和 eager execution。Qwen3Guard 返回原生
`Safety: Safe|Unsafe|Controversial` 与 `Categories:` 格式；Gateway 分别映射为
`allow`、`block` 和 `review`。SparkClaw 当前没有人工安全复核队列，因此 `review`
和 `block` 都会在 routing 或 tool execution 前终止 run。外部 endpoint 不可用时，
Gateway 会记录 `mock=true` 并使用本地 heuristic fallback。Compose 最多允许首次真实
推理 readiness 探针运行 110 秒，并且只有探针生成非空 completion 后才把 Guard
容器标记为 healthy。

### OvisOCR2 文档 OCR

document OCR adapter 通过 OpenAI-compatible vLLM chat-completions endpoint 使用
[`ATH-MaaS/OvisOCR2`](https://huggingface.co/ATH-MaaS/OvisOCR2)，把 page image 按自然阅读
顺序解析为 Markdown，并保留公式和表格。Fast 仍负责 visual semantic 和 Workflow 推理；
OCR 输出是不可信文档证据，不能选择 model lane 或授权 edit。

Local 模型服务固定 vLLM `0.22.1`，只在 loopback 暴露 `8007`，使用显式 2 GiB KV cache budget，
并复用 Hugging Face cache。默认 `single-fast` 命令通过同一次 Compose 操作同时启动 OCR、
Fast、embedding、guard 与 ASR：

```bash
scripts/serve_models_compose.sh single-fast
curl -fsS http://127.0.0.1:8007/v1/models
```

使用匹配的 OCR adapter 配置启动完整 local 产品：

```bash
npm run start:local
```

host 侧 doctor 保留 Gateway 使用的 Compose service URL，只覆盖检查目标：

```bash
SPARKCLAW_DOCTOR_PROFILE=local \
SPARKCLAW_OCR_BASE_URL=http://127.0.0.1:8007/v1 \
  bash scripts/doctor.sh
```

当前单 Fast 产品运行态默认启用 OCR。选中的 Office/PDF 图片会得到有界 OCR Markdown；扫描 PDF
页自动调用 OCR。页面栅格化限制为八页、单页 4 MiB、每次 PDF 读取总计 16 MiB。adapter 关闭、busy、timeout、
返回 malformed 或 incomplete 时都会明确报告 partial evidence。GB10 上已经验证先停止全部
常驻模型、再执行统一启动的组合路径；向已常驻栈单独增加 OCR 会在 CUDA 初始化阶段失败。必须保留
显式 2 GiB KV cache，仅依赖 utilization 分配会得到负数的可用 cache 计算结果。一次并发图片
与扫描 PDF 冒烟调用已成功，但它不是 OCR 质量基线，仍需覆盖更多真实文档的质量测量。

### Qwen3-ASR 语音转写

SparkClaw speech 使用 OpenAI-compatible transcription endpoint。Qwen3-ASR 支持 vLLM serving 和 OpenAI transcription API。默认产品组使用 `Qwen/Qwen3-ASR-0.6B`，并通过其他常驻模型共用的 Hugging Face cache 下载。只有在完整常驻组下重新测过内存和延迟，才能切换到 `Qwen/Qwen3-ASR-1.7B`。

ASR 服务使用显式 2 GiB KV cache budget。五服务冷启动时，仅依赖 utilization
的分配方式在加载 1.53 GiB 模型后把音频 encoder profiling 峰值计入预算，算出
`-10.24 GiB` 可用 KV cache，因此固定 cache 是必需配置，不是可选调优。

[官方 Qwen3-ASR README](https://github.com/QwenLM/Qwen3-ASR) 建议中国大陆用户使用 ModelScope。若要使用预下载的 ModelScope 副本，可将其下载到共享 cache，并在调用启动命令的 process environment 中设置 `SPARKCLAW_ASR_MODEL_ID=/models/modelscope/Qwen3-ASR-0.6B`：

```bash
python3 -m pip install -U modelscope
mkdir -p data/models/modelscope/Qwen3-ASR-0.6B
modelscope download --model Qwen/Qwen3-ASR-0.6B --local_dir data/models/modelscope/Qwen3-ASR-0.6B
```

Local 模型 Compose 中的 ASR 服务会基于本地 vLLM image 构建一个轻量派生镜像，只补音频依赖，不改文本模型主镜像：

- Compose：`docker/compose.models.local.yaml`
- 环境变量：`docker/env/sparkclaw.local.env`
- 镜像配方：`docker/images/asr-vllm.Dockerfile`
- 默认 served model：`sparkclaw-asr`
- 默认模型 ID：`Qwen/Qwen3-ASR-0.6B`

只启动 ASR：

```bash
scripts/serve_models_compose.sh asr
```

默认产品启动已经包含 ASR。历史 dual-light 实验也可显式带 ASR 启动：

```bash
scripts/serve_models_compose.sh dual-light-asr
```

启动启用 speech 的完整 local 产品：

```bash
npm run start:local
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

host 侧运行 doctor 时，只把检查目标覆盖成 loopback：

```bash
SPARKCLAW_DOCTOR_PROFILE=local \
SPARKCLAW_SPEECH_BASE_URL=http://127.0.0.1:8006 \
  bash scripts/doctor.sh
```

2026-05-24 DGX Spark 验证说明：

- NVIDIA GB10 和 driver `580.159.03` 在 host 和 CUDA containers 中可见。
- `vllm/vllm-openai:cu130-nightly` 可在 arm64 上运行。
- `Qwen/Qwen3.6-27B-FP8`、`Qwen/Qwen3.6-35B-A3B-FP8`、`Qwen/Qwen3-Embedding-0.6B` 和 `Qwen/Qwen3Guard-Gen-0.6B` 已验证。
- full-context fast+deep dual residency 在两个 chat lanes 都为 128K context 且启用 MTP 时未能同时容纳。可一次运行一个 128K/MTP chat lane，把两个 Gateway profiles 都路由到已加载 lane，或降低 context/MTP 后重新测量。

当前单 Fast 产品启动：

```bash
npm run start:local
```

该命令把共享产品契约与 Local 模型连接应用到 `docker/compose.models.local.yaml` 中的
有界服务；Fast、embedding、guard、ASR 与 OCR 一起启动。Gateway 的两个逻辑 chat profiles 都发送到
`sparkclaw-fast`，语音转写使用 `sparkclaw-asr`，文档 OCR 使用 `sparkclaw-ocr`。chat endpoint
通过独立 vLLM 0.24.0 image 加载 `nvidia/Qwen3.6-35B-A3B-NVFP4`。SparkClaw 只提供
checkpoint ID 与容量预算；vLLM 读取 ModelOpt metadata，并负责 activation precision、
quantization dispatch 与 kernel/backend 选择。产品与定向 chat 加载默认值均不再保留
FP8 chat checkpoint。

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

历史已验证 real-model run 完成 58 个 golden cases。2026-08-24，恢复后的 vLLM-managed 路径通过当前 47-case matrix，包含 15 次 tool call、2 次 approval 和 1 个 memory candidate；Fast、Deep、Embedding 与 Guard 调用均为真实模型调用（`mock=0`），且没有 model error。强制 W4A4 结果因实验已回滚而只保留为历史记录。benchmark rows 和运行说明见 [model_baseline.md](../benchmarks/model_baseline.md)。

## Backup And Restore

需要备份的路径或 volumes：

- `.env.local` 与 `.env.remote` 中的凭据和覆盖项，存储在 git 外
- `data/memory`
- `data/traces`
- `data/artifacts`
- `data/workspaces`
- Postgres volume `sparkclaw_pg18`
- MinIO volume `sparkclaw_minio`
- 如果需要复用模型缓存，则备份 `data/models`

Postgres：

```bash
sudo -n docker compose \
  --env-file docker/env/sparkclaw.product.env \
  --env-file docker/env/sparkclaw.local.env --env-file .env.local \
  -f docker/compose.yaml exec postgres \
  pg_dump -U sparkclaw sparkclaw > sparkclaw.sql
```

filesystem state 最好在 Gateway 停止后复制。

## Upgrade Flow

1. 保存或导出重要 state。
2. 拉取或应用代码变更。
3. 运行 `npm run start:local` 或 `npm run start:remote`；所选入口会重建发生变化的 image，
   并 reconcile 完整模式。
4. 确认目标 profile ready。
5. 运行 `bash scripts/doctor.sh`。
6. 运行 mock golden eval。
7. DGX Spark 模型变更需要运行 endpoint checks，并追加新的 benchmark section。

### 2026-07-30 之后升级需要注意的行为变化

- 自 2026-09-05 起，SparkClaw Browser Bridge 与 Owner-scoped Playwright Controller 是唯一
  Browser Runtime。两种部署模式都会安装或校验持久宿主 Chromium 和两个 User Service。
  Gateway 不包含 Chromium/Xvfb 或 Browser Automation Engine，只读挂载 Controller Runtime，
  并拒绝已退役的 CDP/Transport 配置。
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
- Observation 压力现在有两条边界：
  `workflow_run_observation_compaction_bytes=36000` 开始滚动压缩，
  `workflow_run_max_observation_bytes=48000` 是优先检查的硬停止线。旧配置按已解析最大值
  的 75% 派生较低值；两值都显式配置时必须满足 `0 < compaction < maximum`。
- `workflow_stage_max_observation_reads=2` 独立限制已执行的 `observation.read` support
  call。环境变量覆盖为 `SPARKCLAW_WORKFLOW_RUN_OBSERVATION_COMPACTION_BYTES` 与
  `SPARKCLAW_WORKFLOW_STAGE_MAX_OBSERVATION_READS`。


## Secure Defaults

- 除非局域网 MCP client 确实需要直连，否则保持“允许局域网访问”关闭；出厂 Compose 中
  Gateway 仅在 Docker 私有网络可达。
- 把 WebChat `18790` 限制在 Owner 可信局域网。逐客户端 Gateway token 认证 Owner API，
  MCP Access Ticket 独立保护 `/mcp`；精确配对 bootstrap 必须保持在宿主回环端口 `18795`。
- 未实际测试时保持无鉴权的实验性 JingSi listener 不发布；启用时只把 `18793` 绑定到一个
  RFC1918 address，绝不能使用 wildcard 或 public interface。
- dangerous 和 reversible tools 保持 approval-gated。
- shell execution 保持 sandboxed 且 network-disabled。
- browser/email/file observations 视为 untrusted。
- 保持 Host Desktop 与 Browser Profile 对容器关闭。Gateway 只通过只读 Runtime Mount 获得
  Owner-only Controller Socket；Browser Bridge Token 在 Vault 中保持加密，绝不能进入 Compose
  Environment、Log、Trace、Artifact 或 Public Config Output。
- `.env.local`、`.env.remote`、model weights、state encryption keys 和下载数据不进入 git。
- 交付前扫描 diff 中的 token。

## Troubleshooting

| Symptom | Check |
|---|---|
| Docker permission denied | 使用 `sudo -n docker ...` 或将用户加入 Docker group。 |
| Browser Bridge unavailable | 运行 `bash scripts/setup-browser.sh --check`，检查 `systemctl --user status sparkclaw-browser.service` 与 `sparkclaw-browser-controller.service`，并确认 Controller Socket 由 Deployment UID 拥有且权限为 `0600`。已加载 Bridge 过期时需重启 SparkClaw Browser。 |
| Golden eval browser step fails | Docker eval 启动 Gateway 时设置 `SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal`；host eval 使用 `127.0.0.1`。 |
| 主机重启后 CUDA 或 Triton 报 `operation not permitted` | 运行 `scripts/serve_models_compose.sh single-fast`。任一成员停止或不健康时会自动整组重建，以新的 runtime cache 启动，同时保留 `data/models`；设置 `SPARKCLAW_FORCE_MODEL_RECREATE=true` 可手动强制相同恢复。 |
| Model returns reasoning but no answer | 设置 `SPARKCLAW_MODEL_DISABLE_THINKING=true`。 |
| Postgres vector extension unavailable | SparkClaw fallback 到 JSON vectors 和 Gateway-side hybrid scoring。 |
| 128K fast+deep does not fit | 一次运行一个 chat lane，或降低 context/MTP 后重新 benchmark。 |
