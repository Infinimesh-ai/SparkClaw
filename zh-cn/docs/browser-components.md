# 受管浏览器组件

[English](../../docs/browser-components.md)

油猴、RevivalStack AI Chat Exporter 和 ChatGPT Exporter 是由 SparkClaw 长期管理的产品组件。所有 Local／Remote 安装使用相同组件清单与统一安装流程；用户无需手工安装这些组件或配置扩展路径。账号登录仍保存在各自专用 Chromium profile。

## 发布职责

`configs/browser-components.json` 固定油猴上游／产品版本、官方 CRX 校验和、导出脚本源码及依赖。`tools/browser-userscripts/` 保存源码与许可证声明。官方专有扩展在安装时下载，不复制入本仓；官方内容变化导致校验失败时必须更新经审阅的 pin，不能直接接受 latest。已校验下载缓存在用户 XDG cache。

产品固定目录 `/opt/sparkclaw/tampermonkey` 对应固定 ID `dlgaaenljmglaedeiniphopajnkeakkf`，升级保持身份。此前用户的解压扩展和数据库保留供回退，其私人路径不用于其他部署。安装备份 profile 偏好与旧油猴数据库；产品数据库只通过原生系统脚本导入初始化，不能复制普通脚本以免重复注入。旧管理器数据保留在原身份下。

## 原生受管下发

安装器在 `/etc/chromium/policies/managed/sparkclaw-userscripts.json` 写入仅针对产品扩展 ID 的第三方策略。`jsonImport` 使用内嵌公开脚本包和油猴的版本化结构摘要，无需另起 HTTP 服务或授予文件访问权限。每次显式部署生成新的导入代次，普通重启复用该代次。每份脚本在产品清单中有固定 UUID；精确匹配的下发补丁使用该 UUID，避免每次生成副本，确保重复部署更新同一脚本并保留其存储。产品下发路径还允许同一受管 UUID 的同版本重新同步；上游原本拒绝此情况，该例外仅对显式受管下发开启。油猴以启用的系统脚本安装／替换产品脚本，保留其他 profile 数据。关闭单脚本自动更新，确保所有部署使用同一产品版本；所需 JavaScript 库从固定的随仓字节导入。

Chromium148 可能延迟 managed storage 回调，实测约五秒；上游油猴5.5.0 一秒即放弃。产品使用精确匹配补丁，将有界等待延长到二十秒，并等待当前部署哈希，避免空对象或旧策略缓存被判为就绪。补丁后的 Service Worker 使用绑定产品版本及部署哈希的文件名，确保升级使 Chromium 缓存失效。匹配失败则中止构建；产品版本与上游版本区分，记录完整安装文件哈希。该补丁属于受版本控制的部署逻辑，不是机器本地 launcher 改动。

仅为专用 profile 中的产品扩展开启已有 userScripts 能力；停止浏览器后才写 profile。就绪检查读取扩展 LevelDB 的临时私有快照，不直接修改数据库；核对已消费代次、脚本唯一性、版本、enabled/system 标记、独立更新关闭、源码／依赖哈希及权限。快照竞争失败会有界重试。这证明配置下发，不代替每个平台真实对话导出验收。

## 操作

`npm run deploy:local` 和 `npm run deploy:remote` 都进入共享浏览器安装流程。在已有 Remote 部署中仅同步浏览器组件：

```bash
SPARKCLAW_BROWSER_ENV_FILE="$PWD/.env.remote" bash scripts/setup-browser.sh
npm run start:remote -- --check
```

start／预检验证已安装版本，deploy 安装并同步。升级必须同步清单、已审阅兼容补丁与源码，再执行组件测试、全新 profile 实机安装、重复安装与真实就绪检查。不要绕过脚本回执不匹配；旧 `browser-extensions.json` 个性化方案不再属于产品契约。

## 受管邮件读取脚本

组件清单还包含 QQ Mail、Gmail 和 Outlook Network Reader。可编辑源码位于 `scripts/email/userscripts/lib/`；运行 `node scripts/email/userscripts/build.mjs` 生成 `tools/browser-userscripts/` 中的三份固定脚本并更新清单哈希，`--check` 会拒绝过期产物。脚本在匹配的已登录邮箱页面向 Controller 提供只读操作，不自行调度同步。

会话请求头、邮箱分页游标和已学习的原件下载地址仅保留在受控页面内存。Gateway 持久化绑定账号的收件时间区间与原件回执。由于 Outlook 可能在油猴异步注入完成前绑定传输对象，Controller 在页面创建时安装小型 Worker 观察桥，由已安装 Reader 消费观察结果并执行合格读取。脚本缺失或接口证据不足时保留原生采集回退。安装就绪不等于所有文件夹覆盖或原件采集通过，详见[邮件链路设计中的实现状态](email-pipeline-optimization-design.md)。
