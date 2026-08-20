# File Store 持久性设计

> 语言：[English](../../docs/store-file-durability-design.md) | 简体中文

> 状态：S2 设计审查草案，2026-08-19。只有 S0、S1 实现审查和本设计都
> 得到 `GO` 后才开始实现。S2 同时迁移 S0 接受的低风险 pilot repository。

## 问题

File 后端当前先修改 `MemoryStore`，再序列化，并丢弃大部分持久化错误。
它的 mutex 只保护 snapshot 写入，不保护此前的 mutation 或普通读取。因此
reader 可能观察到尚未持久、之后还需回滚的状态；后续成功 snapshot 还可能
把此前静默失败留下的脏状态一并持久化。

S2 先为所有 File 方法建立同一个事务边界，再通过已接受 pilot repository
在生产路径中证明完整持久化行为。其余 repository 在 S3 迁移。

## 提交可见性不变量

File 读取只能观察命令执行前的完整状态，或命令持久提交后的完整状态，绝不
观察暂态 Memory mutation。

所有 File 读写使用同一个支持 context 的事务 gate：

- read 获得共享 admission；
- command 在读取 pre-state 前获得独占 admission；
- 已迁移方法使用调用者 context，legacy 方法暂时使用有界内部 context；
- 禁止在已 admission 的 File 方法之外直接访问 `inner`；
- 等待 admission 必须遵守取消和 deadline。

gate 基础是机械 commit，与 pilot command 错误行为分开审查。pilot migration
commit 开始前，它必须覆盖所有现有 File 方法，包括较新的 MCP/ISCP 方法。

## 命令算法

持有独占 admission 时，已迁移 command：

1. 拒绝已取消 context；
2. 捕获完整的命令前 rollback state，其中包含持久 `Snapshot`，以及保存进程内
   revision 和其他易失派生状态的不序列化 sidecar；
3. 应用 Memory command 及其必需 event/audit 变更；
4. 捕获候选 snapshot；
5. 编码并按配置加密候选；
6. 在同一目录创建 mode `0600` 的唯一临时文件；
7. 写完所有字节、fsync 文件并关闭；
8. 在提交 replacement 前检查取消；
9. 原子 rename 临时文件覆盖 state path；
10. fsync 父目录；
11. 释放 admission 并报告成功。

配置为 File 后端时必须具有非空 path。需要非持久状态的测试使用
`MemoryStore`，而不是无路径 File Store。

## 失败状态机

| 阶段 | Memory 动作 | 结果 |
|---|---|---|
| Memory mutation 前 | 无 | 原始 domain、canceled 或 timeout 错误 |
| Memory command 拒绝 | 按命令契约保持不变 | 类型化 domain 错误 |
| rename 前 encode/encrypt/create/write/file-sync/close 失败 | 恢复完整 pre-snapshot | 带 cause 的 `durability_failed` |
| rename 前 context 结束 | 恢复完整 pre-snapshot | `canceled` 或 `timeout` |
| rename 报错 | replacement 确认未发生时恢复 pre-snapshot | `durability_failed`；否则 `unknown_outcome` |
| rename 成功后目录 sync 失败或完成结果不确定 | 保留候选状态，不声称回滚 | `unknown_outcome` |
| commit 成功 | 保留候选状态 | nil |

出现 `unknown_outcome` 后，在 repository reconciliation read 确定持久版本前
禁止普通重试。临时文件清理是 best effort，且不得隐藏主错误。

## 回滚正确性

回滚通过启动时相同的 normalization 路径加载持久 snapshot，确保持久派生
索引一致重建，再恢复易失 sidecar。sidecar 绝不改变 snapshot JSON。不接受
手写恢复单个 map entry，因为一个命令可能同时修改 event、index、revision
或关联记录。

回滚或 rename 后结果处理期间不得放行 read。

## 失败注入

File 持久化使用 package-local filesystem seam，支持确定性注入：

- encode 和 encryption；
- 临时文件创建和部分 write；
- file fsync 和 close；
- rename；
- directory open/fsync/close；
- cleanup。

测试不得依赖权限、磁盘写满、mount 行为或时间竞争。

## 验证

强制测试包括：

- reader 无法观察被阻塞的暂态 mutation；
- 排队的 read/write admission 遵守取消；
- 每个 rename 前失败恢复完整内存 snapshot；
- event、index 和 revision 状态也会回滚；
- rename 后目录 sync 失败返回 `unknown_outcome` 且不回滚内存；
- 成功 commit 后重启得到完全相同的提交状态；
- 并发 command 串行执行且不丢更新；
- 加密和明文使用相同提交阶段；
- race 测试覆盖 read、command、rollback 和 reload；
- source guard 拒绝丢弃持久化错误和不经过 gate 的 `inner` 访问。

## 过渡规则

S2 pilot 完成后到其余 repository 全部迁移前，legacy command 保留旧公共签名，但必须使用同一
gate 和有界持久化路径。过渡期间不宣称系统整体可靠。只有当一个 repository
的签名、调用者、全部后端、失败测试和 reconciliation 行为同步迁移后，它才
获得新契约。

## S2 审查门禁

设计 `GO` 要求接受 gate 实现策略、pilot、提交点、失败表和注入 seam。实现
分为两个分别审查的 commit：覆盖每个 File 方法的机械 gate，随后同步迁移
Memory、File、PostgreSQL 和调用者的 pilot repository。

S2 实现 `GO` 要求 pilot 确定性失败测试和 race 证据绿色、snapshot JSON
形态不变、默认后端无回归，并且没有未使用 transaction helper。只有 gate
脚手架时不得开始 S3。

## 审查记录

| 审查 | 修订/commit | 结论 | 证据和未解决风险 | 审查人/日期 |
|---|---|---|---|---|
| 设计 | pending | pending | pending | pending |
| 实现 | pending | pending | pending | pending |
