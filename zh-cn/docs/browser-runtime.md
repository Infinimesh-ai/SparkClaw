# 浏览器 Runtime

> 语言： [English](../../docs/browser-runtime.md) | 简体中文

本文档是当前浏览器实现与运行手册。SparkClaw 使用一个由宿主机拥有的 Chromium
进程、一个专用持久 Profile，以及通过受保护 Host-CDP endpoint 附着的固定版本
`agent-browser` `0.32.3`。旧的容器 Chromium、Xvfb、Profile mount、X11 overlay、
Profile lease 和启动 fallback 已全部删除。

迁移理由与完整设计记录保留在
[Host-CDP 浏览器设计](host-cdp-browser-design.md)。

## Runtime 拓扑

```text
Browser Workflow
  -> ToolHub browser capability
  -> internal/browserautomation
  -> private agent-browser MCP process
       AGENT_BROWSER_CDP=<capability WebSocket URL>
  -> browserd CDP proxy on a Docker bridge
  -> host sparkclaw-browserd
  -> one SparkClaw Chromium process
  -> dedicated owner-only persistent profile
```

不存在 Playwright fallback、direct CDP execution backend、日常浏览器 attach、cookie
导出或容器侧 Chromium launcher。

## 进程与 Profile 所有权

`sparkclaw-browserd` 是 systemd user service，也是 Chromium 的唯一进程 owner。它把
当前架构获准的固定制品安装到 `/opt/sparkclaw`，使用 owner 专属数据目录中的持久
Profile 启动，并通过 owner-only runtime directory 发布浏览器健康信息。

默认路径如下：

| 资源 | 宿主机路径 |
|---|---|
| Browser daemon | `/opt/sparkclaw/browserd/sparkclaw-browserd` |
| Browser config | `~/.config/sparkclaw/browserd.json` |
| Persistent profile | `~/.local/share/sparkclaw/browser/default/user-data` |
| Runtime directory | `${XDG_RUNTIME_DIR}/sparkclaw/browserd` |
| Capability endpoint | `${XDG_RUNTIME_DIR}/sparkclaw/browserd/cdp-endpoint` |
| Desktop launcher | `~/.local/share/applications/sparkclaw-browser.desktop` |

runtime directory 与 Profile 权限为 `0700`。endpoint 必须是 deployment user 拥有、
非 symlink 的普通文件，权限不得宽于 `0600`。Gateway 使用相同 numeric UID/GID，
只读挂载 browserd runtime directory；它不会获得 Profile 或 Chromium 原始调试端口。

当 browserd 能验证 owner 的 X11/XWayland display 时，宿主 Chromium 以 headed 方式
启动；否则以宿主机 headless 方式启动。点击桌面应用 **SparkClaw Browser** 时，可以让
browserd 使用有效桌面 display 和同一 Profile 按序重启该唯一进程；绝不会针对同一
Profile 启动第二个进程。

## Capability Endpoint

Browserd 的 capability proxy 只绑定 loopback，以及发现到的 `docker0` 或 `br-*`
Docker bridge 地址。endpoint 文件包含 browser identity、generation、presentation、
已验证版本，以及供容器和 direct-host 使用的 capability-bearing WebSocket URL。

capability 不会出现在公开 config/status API、日志、trace、artifact 或模型上下文中。
Chromium generation 每次变化时 browserd 都会轮换 capability。Gateway 创建或替换私有
MCP connection 时重新读取 endpoint。endpoint 过期、owner 不匹配、权限过宽、Profile
不匹配、URL boundary 非法或进程丢失都会 fail closed。

## 标签页所有权

连接同一个 Chromium 进程并不授权 SparkClaw 使用 owner 已打开的标签页。Gateway
维护按逻辑 browser scope 分隔的内存 target ID allowlist。

- `browser.open` 在调用 `agent_browser_tab_new` 前后分别列出 tab，只有差集恰好包含一个
  新 target 时才登记所有权。
- owner 既有 tab 和其他逻辑 scope 的 tab 不会出现在列表中，也不能被 focus、read、
  click、type、select 或 close。
- 所有隐式 active-tab 操作执行前都验证 active target 属于当前 scope。
- 多个新增 target 的歧义差集会 fail closed 且不登记任何所有权，而不是猜测哪个
  target 由 SparkClaw 创建。
- MCP transport 丢失、browser generation 变化或 reconnect 会清空全部内存授权；
  target ownership 不跨 reconnect 持久化。

owner 停止操作不会把标签页所有权转移给 SparkClaw。

## 登录 Handoff

认证状态只保留在专用 Chromium Profile 中。当 Workflow 检测到登录或人工验证 gate
时，会持久化 handoff 并要求 owner 打开 **SparkClaw Browser**。owner 在专用 Profile
中完成登录；SparkClaw 只会在重新验证 target、URL 和页面 evidence 后恢复同一个冻结
任务拥有的 target。

SparkClaw 不 attach owner 的日常浏览器 Profile，不复制 cookie，也不选择任意已登录的
既有 tab。browserd 和宿主 Profile 持续运行，因此登录状态可跨 Gateway 与 MCP restart
保留。

## Agent-Browser 生命周期

Gateway 校验精确的 `agent-browser 0.32.3` CLI 和 MCP server 版本，并使用 browserd
WebSocket URL 作为 `AGENT_BROWSER_CDP` 启动一个私有 MCP subprocess。该 subprocess
只拥有协议 transport；Chromium 由 browserd 拥有。

正常关闭时，Gateway 先针对唯一 session 调用 `agent_browser_close`，回收本次调用拥有的
agent-browser daemon 与 socket，再停止私有 MCP subprocess。该 close 只让 agent-browser
从外部 owner 的浏览器断开，不会终止 browserd 或 Chromium。MCP transport 已不健康时则
直接 abort，并依赖有界 daemon idle timeout 回收。无论哪条路径，宿主 Chromium PID 都必须
保持存活。browserd 或 Chromium 丢失会产生 typed unavailable/reconnect failure，且绝不
回退到容器浏览器。

## 配置

当前 adapter 配置如下：

```json
{
  "adapters": {
    "browserAutomation": {
      "command": "agent-browser",
      "timeoutMs": 30000,
      "startupTimeoutMs": 10000,
      "hostCDP": {
        "endpointFile": "/run/sparkclaw/browserd/cdp-endpoint",
        "profileID": "default",
        "connectTimeoutMs": 10000
      }
    }
  }
}
```

部署环境变量：

| 变量 | 用途 |
|---|---|
| `SPARKCLAW_BROWSER_CDP_RUNTIME_DIR_HOST` | 只读挂载到 Gateway 的宿主 browserd runtime directory |
| `SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE` | Gateway 内 endpoint 路径；默认 `/run/sparkclaw/browserd/cdp-endpoint` |
| `SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE_HOST` | setup、doctor 和 PID 检查使用的 direct-host endpoint 路径 |
| `SPARKCLAW_BROWSER_CDP_PROFILE_ID` | 预期 browserd Profile identity；默认 `default` |
| `SPARKCLAW_BROWSER_CDP_CONNECT_TIMEOUT_MS` | 有界 Host-CDP attach timeout |

旧 Chromium executable、Profile directory、daemon idle、display 与 Xauthority 环境字段
都会被拒绝。旧 JSON launch/Profile 字段同样会被拒绝，不存在静默 compatibility path。

## 安装与运行

两个正式部署入口调用同一个 installer：

```bash
npm run deploy:local
npm run deploy:remote
```

`scripts/install-host-browser.sh` 从 `configs/host-browser-artifacts.json` 解析获准
制品，验证 checksum 与架构，安装 browserd，创建 systemd user service 和 desktop
launcher，并把 Host-CDP 路径写入选定的 mode-`0600` env 文件。

只读检查：

```bash
bash scripts/deploy_local.sh --check
bash scripts/deploy_remote.sh --check
bash scripts/install-host-browser.sh --check --env-file .env.local
bash scripts/install-host-browser.sh --check --env-file .env.remote
```

本地 setup 与诊断：

```bash
npm run setup:browser
bash scripts/doctor.sh
systemctl --user status sparkclaw-browserd.service
/opt/sparkclaw/browserd/sparkclaw-browserd \
  --config "$HOME/.config/sparkclaw/browserd.json" status
```

Compose 包含 `agent-browser`，但不包含 Chromium 或 Xvfb。启动流程先检查 browserd，
再通过 `AGENT_BROWSER_CDP` 执行 MCP open/snapshot/close smoke，停止 MCP process，并验证
记录的宿主 Chromium PID 仍存活。

## 验证

浏览器 runtime 变更至少执行：

```bash
cd services/gateway && go test ./internal/browserautomation ./internal/config ./internal/gateway
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest scripts.test_host_browser \
  scripts.test_local_compose scripts.test_remote_compose scripts.test_deploy_remote
docker compose --env-file docker/env/sparkclaw.product.env \
  --env-file docker/env/sparkclaw.local.env \
  -f docker/compose.yaml -f docker/compose.models.local.yaml \
  --profile product --profile models-local config --quiet
docker compose --env-file docker/env/sparkclaw.product.env \
  --env-file docker/env/sparkclaw.remote.env \
  -f docker/compose.yaml --profile product config --quiet
```

最终 live acceptance 还会运行 browserd 与容器侧 MCP smoke，确认 owner 既有 tab 未被操作，
并确认 Gateway/MCP shutdown 后 Chromium 仍在运行。
