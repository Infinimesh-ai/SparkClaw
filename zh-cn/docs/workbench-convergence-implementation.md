# 工作台收敛实施记录

> 语言：简体中文 | [English](../../docs/workbench-convergence-implementation.md)

日期：2026-10-06。实施分支：`codex/workbench-runtime-convergence`，起点为
`9b92968d`。本文记录源码和隔离验证，不代表运行服务已升级。
已确认的产品行为见[方案](workbench-runtime-convergence-design.md)。

## 阶段 0：基线与边界

修改前，八份方案／架构／索引文档与来源工作区逐字一致，包含第 6.1 节。
来源工作区未修改。通过已知 Linux 主机找到 `/home/infinimesh/InfiniCenter`，
已读注册表、空收件箱、中枢 C0001、proposed 0031 及 JingSi、App-CLI、IMMS
已接受契约。实施评审已追加到 0031；其状态仍为 proposed。
内部方案不代表外部协议或留存变更已获接受。

| 范围 | 初始行为 | 处理方向 |
|---|---|---|
| 历史 | 主机 Store 与桌面 SQLite／文件 | 保留本地归属及认证范围，统一选择规则 |
| 运行时 | 桌面另建 ToolHub 和策略 | 共用有效提供方、策略和注册表，隔离仓储及资源适配器 |
| 上下文 | 主机类型化历史与桌面 32 条／96 KiB 信封 | 分离传输限额与公共模型上下文选择 |
| 宿主资源 | 主机工作区／采集浏览器与明确绑定的桌面 Host | 保留资源授权，不隐式访问其他工作台 |
| 交付 | 主机结果持久化与桌面文件验证／ACK | 保留持久成功及仅查询的恢复语义，不重放不确定写入 |
| 调度 | 主机扫描过期任务；桌面后端续租 | 明确行为变更：本地轮次认领、离线轮次永久跳过 |
| 邮件 | 后端权威，桌面缓存 | 保留单一采集和服务权威 |
| 外部调用 | JingSi 持久结果／事件、App-CLI journal、IMMS evidence | 保持冻结契约和留存，不重置或导入 |

基线环境：macOS ARM64、Go 1.25.12、Node 26.2.0/npm 11；隔离 Linux ARM64
使用 Go 1.25.5／Node 26.2.0。两机测试前均执行 `npm run setup:document-tools`
安装已声明的文档依赖。

| 基线检查 | 结果 |
|---|---|
| Mac Go 构建 | 通过 |
| Mac Go 全量测试 | Gateway、cmd/sparkclaw、browser-host 存在原有失败：临时路径规范化保护和 Linux `/dev/shm` 要求；其他包通过 |
| Linux Go build／vet／全量测试 | 全新隔离快照通过，使用中枢 JingSi conformance manifest |
| Desktop | 102 通过，无跳过 |
| WebChat | 196 通过；TypeScript／Vite 构建通过 |
| 双语文档／本地链接 | 阶段 1 指南更新后检查 103 份英文文档 |

Mac 日志在忽略的 `.cache/convergence/`；Linux 基线日志和源码快照在
`/home/infinimesh/.cache/sparkclaw-convergence-20261006-DIrlhW`。
测试使用隔离文件，不连接生产业务服务。

## 阶段 1：现行指南

README、架构／索引、Store 和 WebChat 指南明确主机工作台持久化与执行内容的区别。
Mac 指南使用 `main`，链接有日期的原生证据，不再把已交付能力描述为未实现。
既有硬件验收缺口继续明确保留。

## 后续阶段门

公共运行时组装、工作台行为、中性命名／API／存储切换和发布验收正在实施。
后续提交需在本文补充实际验证才能关闭阶段门。本文不授权推送、合并主分支、
部署运行服务或删除真实数据。

## 公共上下文准入

`configs/workbench-limits.json` 是限额来源，生成器将常量投影到 Go 和桌面安装包，并检查
是否过期。两种工作台均支持最多 64 KiB UTF-8 用户输入，提交信封最多 1 MiB／32 条消息。
主机及提交仓储使用同一最后八条对话选择和 UTF-8 消息边界。有效模型的 token 准入仍是
最终依据；字节上限不保证每个 64 KiB 输入都能放进配置的 embedding／guard 模型。
较大的传输额度移除了桌面独有的 16 KiB 限制，并为所选历史留出空间。
结果／文件限额和 24 小时绝对交付到期不变。

定向测试比较主机 FileStore 与提交 MemoryStore 的选择结果、跨会话隔离、UTF-8 边界、
相同 64 KiB HTTP／信封准入及生成契约新鲜度。Go build／vet 与 agent、workbench、
execution 包通过；合入限额和调度后 Desktop 116 项通过。
