# SparkClaw 工程底线规则

> 语言： [English](../../docs/engineering-baseline.md) | 简体中文

本文档是提交代码前必须满足的**底线**，不是风格建议。每条规则都来自本仓库真实发生过、并在 2026-07 架构治理中修复的问题。违反任何一条的 PR 不予合并。

---

## 1. 依赖与环境：任何人 clone 下来必须能跑

- **运行时依赖必须声明在受版本控制的清单里**（`go.mod`、`package.json`、`requirements.txt`）。禁止依赖"我机器上恰好装了"的全局包。
  - *事故*：文档工具在运行时 `require("exceljs")`，但整个仓库没有任何清单声明它，13 个测试在干净环境下全部失败。现在的正确位置是 [document runtime npm workspace](../../tools/document-runtime/package.json)，配 `npm run setup:document-tools` 安装。
- **禁止写死开发机专属路径或平台**。宿主工具必须通过 `PATH` 解析，不得新增仓库私有 runtime/toolchain 目录。
- 新增外部依赖时，同步更新 setup/doctor 脚本，保证 `git clone && 安装脚本 && go test ./...` 一次通过。

## 2. 子进程与外部调用：必须有超时和归宿

- **每一次 `exec` 和出站 HTTP 调用都必须有超时**。调用方 context 没有 deadline 时，被调用处必须自己兜底（参考 `runSubprocessAdapter` 的 60 秒兜底）。
  - *事故*：所有文档子进程调用零超时，一个卡住的 node 进程可以永久占住请求。
- **长生命周期子进程必须挂在可取消的 context 上，并提供 `Close()`**，接入进程退出流程。
  - *事故*：浏览器自动化的 MCP 子进程挂在 `context.Background()` 上且无关闭入口，网关退出后 Chrome 成为孤儿进程。
- **进程失败与业务错误必须可区分**。脚本内部出错时禁止 `print(error json)` 然后 `exit(0)` 了事；要么非零退出，要么调用方对两条错误通道都做检查。

## 3. 单一事实来源：同一件事只允许在一个地方定义

- **新增一个工具/枚举/协议常量，只允许改一个注册点**。如果你发现自己要在 2 处以上同步添加同一个名字，先停下来建注册表或常量表，再继续。
  - *事故*：新增一个工具要同步改 schema 定义、90 行 dispatch switch、feature-gate 名单三处；漏一处只在运行时才炸。现在的注册点是 [registry.go](../../services/gateway/internal/toolhub/registry.go)，有一致性测试兜底。
- **禁止用字符串字面量复述已有枚举**。写 `"small_direct"` 之前先找 `app.DocumentStrategySmallDirect`。
- **禁止用面向用户的显示文案做程序分支**（如 `strings.HasPrefix(line, "兜底策略：")`）。语义要用类型化字段表达，文案随时会改。
- 微信协议常量、加解密、请求头一律使用 [weixinproto](../../services/gateway/internal/weixinproto/proto.go)，不要复制粘贴到自己的包里。

## 4. 接口实现必须完整，能力缺失必须显式

- **给接口新增方法时，所有实现必须同时补齐**（store 有 memory/file/postgres 三个后端）。
- **禁止用类型断言制造"可选能力"然后静默降级**。
  - *事故*：`DocumentStore` 只有 Postgres 实现，默认 file 后端下文档检索通过 `if ds, ok := store.(DocumentStore); ok` 静默返回空——功能"看起来在跑"，实际什么都没做。如果能力确实是可选的，必须在启动时打警告、在调用时返回明确错误。

## 5. 功能声明必须端到端可用

- **字段被持久化 ≠ 功能已实现**。声明支持的行为必须有一个端到端测试证明它真的发生。
  - *事故*：提醒的 `Recurrence` 字段被完整采集和存储，但调度器从不计算下次触发时间——"每天提醒"实际只触发一次。
- 失败路径同样要测：标记为 `retryable` 的失败必须真的会被重试。

## 6. 死代码不进主干

- **没有生产调用方的代码不许合并**。"以后会用到"的类型和函数放在你自己的分支里。
  - *事故*：269 行文档管线类型全仓库零引用；一个 110 行的 planner 只有测试在调用它，用 50+ 个测试伪装出了覆盖率。
- 删除比注释掉更好，git 历史就是回收站。

## 7. HTTP 与并发语义

- **GET 不得有副作用**（不得写库、不得改状态机）。轮询式状态推进用显式 POST 或后台 reconciler。
- **每请求禁止构造重资源**（policy engine、连接池、client）。构造一次，挂在服务对象上。
- **轮询/ticker 协程里禁止执行慢操作**（LLM 调用、大文件下载）。慢工作派发到有界工作池，保住轮询节奏。
  - *事故*：微信消息处理在轮询协程里同步跑 agent 回合，一个慢回复卡住全部用户。

## 8. 配置

- **一个开关一个字段**。禁止 `Enabled`/`Disabled` 这类需要人肉保持互斥的成对布尔。
- 新配置项必须有：默认值、加载期校验或回填（参考 `normalizeRuntimeLimits`——非法的 0 值不能静默瘫痪运行时）。

## 9. 提交纪律

- **一个 commit 一个主题**。禁止 3 万行的 "Upload project" 式提交——它让 review、bisect、回滚全部失效。
- **禁止提交测试运行产生的数据**（artifact、临时目录）。跑一次 `git status`，不认识的文件先查清楚。
- 提交前自查清单：`go build ./... && go vet ./... && go test ./...` 全绿；前端改动加 `npm run build:webchat`。

## 10. 文件与函数规模（软性红线，超过需在 PR 说明理由）

- 单文件 > 800 行、单函数 > 100 行、单包承担两个以上不相关职责——任何一条命中，PR 描述里必须解释为什么不拆。
  - *事故*：`agent.go` 曾达 4032 行、混杂六种职责；前端 `App.tsx` 曾达 3609 行。
