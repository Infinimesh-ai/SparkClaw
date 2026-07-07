# SparkClaw 周期性重构任务委托书（Agent Playbook）

> 语言： [English](../../docs/refactor-playbook.md) | 简体中文

本文档是下达周期性架构重构任务时交给 Agent（如 Claude Code）的标准指令。发起任务时说明审查范围（如"最近 N 个 commit"或"某个包"），并附上这份文档；其余按本文执行，不必逐条重复。

---

## 使命与边界

- **目标**：在保持现有功能可用的前提下优化代码架构，使项目具备长期演进的健壮基础。
- **红线**：不改变对外行为，除非该"行为"本身是缺陷（如功能静默失效、声明了却不工作的特性）。任何行为变化必须在最终汇报中单独列出。
- **裁判**：测试是重构正确性的唯一裁判。测试不足的地方先补测试再动刀。
- 审查标准以 [工程底线规则](engineering-baseline.md) 为准——重构的本质是让存量代码回到底线之上。

## 第 0 步：建立基线（不可跳过）

1. 干净构建：`cd services/gateway && go build ./...`
2. 环境依赖：文档工具测试需要 `npm run setup:document-tools`（安装 exceljs 与 python-docx/pptx/pypdf 到 `.tools/`）。
3. 全量测试：`go test ./...`，前端 `npm install && npm run build`（在 `apps/webchat`，含 `tsc -b`）。
4. **记录基线**：哪些包过、哪些失败及原因。分不清"本来就坏"和"被我改坏"，后面所有验证都是空谈。

## 第 1 步：并行侦察

- 用只读探索代理按包分区并行审查（历史分区参考：`agent` / `toolhub` / `gateway`+`store`+`config` / `weixin`+`notification`+`reminder`+`binding`+`browserautomation` / 前端）。
- 每个发现必须带：`file:line`、一句话描述、严重度（high/medium/low）、具体重构建议。
- 自己跑一遍 `go list` 画包依赖图，验证代理的耦合结论，不要全信转述。

## 第 2 步：排序与选型

按"价值/风险比"排序，优先级从高到低：

1. **功能性静默失效**（类型断言降级、字段存了不生效）——这是用户可感知的 bug；
2. **资源泄漏与无界执行**（无超时、孤儿进程、无退避重试）；
3. **单一事实来源破坏**（多处同步的 switch/名单/常量、字符串复述枚举）；
4. **死代码删除**（先 grep 确认零生产调用方，再删；连带假测试一起删）；
5. **God file 拆分**（同包纯搬移，零 API 变化）；
6. 纯风格问题——通常不值得做，写进遗留清单即可。

大而独立的项（并发模型改造、功能性 bug 修复、巨型文件拆分）拆成**独立后台任务**（spawn_task / 独立 worktree），与主线互不阻塞；小而确定的项在主工作区顺序做。

## 第 3 步：实施纪律

- **小步快跑**：每完成一个主题立即 `go build ./...` + 相关包测试，绿了再进下一个。
- **机械搬移与行为修改分开提交**。搬移类改动（拆文件、抽常量）用脚本批量做时，靠"字节级一致 + 端到端测试"验证。
- **一个 commit 一个主题**，信息说清动机（为什么原样是问题），结尾加 `Co-Authored-By` 署名。
- 测试断言需要改动时，先搞清楚**为什么**变了：预期中的行为改进（如后端能力对齐导致返回类型变化）可以改断言，但要在 commit 里说明；意外变化就是回归，回去修代码。
- 遇到"修复它会改变行为"的项（缺陷类），归入功能修复而非重构，单独提交并在汇报中标注。

## 第 4 步：验证

- `go test ./...` 全绿（对照第 0 步基线，不允许出现新失败）；`go vet ./...` 无告警。
- 前端改动：`tsc -b` + `npm run build`；纯搬移类拆分用 bundle 体积对比佐证无行为变化。
- 有后台任务时：先在各自 worktree 里独立验证（build + 测试 + diff 抽查），确认质量后再合并；每合并一个分支跑一次相关测试。
- 合并他人/其他会话的改动时检查 `git status`，剔除混入的测试运行产物。

## 第 5 步：收尾

- 合并完成且测试全绿后：删除已合并的 worktree 与分支。
- 提交可自主进行；**推送 `origin main` 前在汇报中列出完整 commit 清单**（除非任务指令已明确授权直接推送）。
- 没做完的高价值项：用后台任务建议（spawn_task）挂出，每个任务的 prompt 必须自包含（含文件路径、问题描述、验收标准），不依赖当前会话上下文。

## 汇报格式

- **结论先行**：第一句话回答"改了什么、现在什么状态"。
- 三个清单：已完成（按主题，带文件链接）、行为变化（如有）、遗留建议（含不做的理由）。
- 写给两周后回来看的人，不要用只有本次会话才懂的代号。

## 项目速查（省去重复摸索）

- 包依赖方向：`app` 是纯类型叶子包（不 import 任何 internal 包）；`gateway` 是 HTTP 层；`agent` 是编排核心；`toolhub` 是工具中枢。不允许出现 `toolhub → agent`、`app → 任何包` 的边。
- 工具注册：[registry.go](../../services/gateway/internal/toolhub/registry.go)，schema 在 `defaultDefinitions()`，两者由 `registry_test.go` 强制一致。
- store 三实现：`memory.go`（真身）/ `file.go`（写穿透装饰器）/ `postgres.go`。加接口方法三处都要动，快照结构在 `file.go` 的 `Snapshot`。
- 文档工具脚本：`internal/toolhub/scripts/*.py|.js`，经 `//go:embed` 引入；子进程统一走 `runSubprocessAdapter`（自带超时）。
- 微信协议/加密：统一在 [weixinproto](../../services/gateway/internal/weixinproto/proto.go)。
- 手动工具调用编排：`agent.Runtime.InvokeToolManually`（manual.go），HTTP handler 只做解码/映射。
- 文档 CI：每个 `.md` 需要 `zh-cn/` 镜像和双向语言链接（见 `.github/workflows/ci.yml`）。
