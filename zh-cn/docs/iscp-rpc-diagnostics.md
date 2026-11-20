# ISCP 反向 RPC 排障

[English](../../docs/iscp-rpc-diagnostics.md)

> **2026-11-20** — 工作台 helper、其反向 RPC 跟踪阶段及下述事故分析脚本已退役。本文保留以供解读历史 trace，浏览器健康和私有耗时记录仍由相应组件产生。[连接协议](link-contract.md)。

排查 SparkX 浏览器健康检查时，从任务请求 ID 或
`browser.controller.status` 的 RPC ID 入手。RPC ID 在经鉴权的加密请求中，
Relay 无法读取。发送端分别记录请求和回包的 RPC ID 与信封 ID 对应关系，
接收端与 Relay 日志使用完全相同的外层信封 ID。不要仅凭时间戳配对。

| 环节 | 证据 |
| --- | --- |
| 网关 | `browser.health_result` 和 `browser.status_check` 区分无对端会话、反向调用失败、状态回包无效及运行时不可用。`rpc.call` 标明失败阶段、超时或取消；传输成功也可能收到非 200 状态。 |
| 发送端 | `rpc.send_authorization`、`rpc.envelope_created`、`rpc.relay_enqueue`、`rpc.relay_write`、`rpc.relay_receipt` 区分本地排队、套接字写入和 Relay 签名回执。套接字写入成功不能证明 Relay 已接收。 |
| Relay | `relay.submit_received`、`relay.submit_admission`、`relay.receipt_write`、`relay.delivery_claimed`、`relay.delivery_write`、`relay.delivery_commit` 区分入库与下游写入。Relay 投递成功不能证明对端已解密并处理。 |
| 接收端与 SparkX | `rpc.relay_receive`、`rpc.envelope_received` / `rpc.envelope_rejected`、请求鉴权和 `ipc.reverse_*` 标明 helper 收包、进程间交接及返回的 HTTP 状态。`browser.controller_status` 记录本地运行时或权限判定，不保存回包正文。 |
| 回程 | 按回包信封 ID 查看同样的 Relay 阶段。`rpc.response_received`、`rpc.response_authorization`、`rpc.delivered`、`rpc.response_dropped` 区分校验通过、拒绝和迟到。迟到回包可查，但不会完成已结束的调用。 |

启动网关前，将 `SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR` 设为服务用户拥有的
`0700` 绝对路径，生产环境沿用现有私有日志挂载。SparkX 默认在本客户端
配置目录内创建 `iscp-diagnostics` 并将路径传给 helper。显式环境设置会
覆盖默认值；空值关闭。主进程默认只记录反向 IPC 和浏览器状态，helper
记录传输阶段。启动时清理默认目录的旧日志，保留最近 7 天且之前
进程文件累计不超过 128 MiB。

独立构建的 ISCP Relay 需要为 `iscp-relay.service` 设置
`ISCP_WORKBENCH_TRACE=1`，并保留系统日志。可使用
`journalctl -u iscp-relay.service --since ... --until ... -o cat`
导出事故窗口。无需修改协议、身份或数据库结构。按站点策略清理网关重启
遗留的旧文件。每个 Go 进程最多保留 3 个 64 MiB 文件；SparkX 主进程
最多保留 3 个各 10 万条记录的文件。

```sh
node scripts/iscp-rpc-trace.mjs --request-id "$REQUEST_ID" \
  /private/gateway/go-*.ndjson /private/sparkx/go-*.ndjson \
  /private/sparkx/desktop-*.jsonl /private/relay-window.log
```

也可使用 `--rpc-id` 查看单次探针。脚本只在本地读取文件，按 ID 关联
并展示已有时间戳。跨机时钟可能有偏差，判断先后时应以阶段顺序和同进程
耗时为准。将缺失记录作为证据前，先核对日志健康、覆盖范围、保留窗口和
服务重启情况。Go 和 Relay 将日志写入有上限的队列，不等待磁盘；桌面
主进程默认只写短记录。磁盘或积压异常可能丢记录。日志仅保留关联 ID、
操作名、阶段、固定错误分类和状态，不保存任务正文、网址、凭据、原始
错误文本或密文。
