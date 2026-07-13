# 隐藏 Chromium 浏览器访问计划

> 语言：[English](../../docs/browser-hidden-chromium-access.md) | 简体中文

本文档定义隐藏浏览器执行。Profile 所有权和可见登录转换见
[托管共享 Chromium Profile 方案](managed-persistent-browser-profile.md)。

## 方案决策

SparkClaw 的所有托管浏览器 surface 统一使用 Chromium：

- 普通浏览使用 headless Chromium；
- 登录和人工验证临时使用可见 Chromium；
- 两种形态为选中的逻辑 browser profile 使用同一个持久 Profile；
- 不支持个人 Chrome 附着；
- 不使用 Cookie/storage 导出。

## 目标

- 不显示窗口地渲染 JavaScript 页面。
- 在可见和隐藏切换之间保留 Chromium 原生登录态。
- 普通搜索、读取、snapshot、导航和安全交互保持隐藏。
- 只有需要人工介入或用户明确要求时才显示 Chromium。
- 浏览器生命周期和 Profile 状态由 Gateway 管理。

## Provider 契约

自动化传输仍使用 Chrome DevTools MCP package，但实际启动的是配置的 Chromium executable。
Provider 名称和诊断应说明 Chromium，不能让人误以为 SparkClaw 连接了用户个人 Chrome。

隐藏启动参数：

```text
--executablePath=<Chromium executable>
--userDataDir=<SparkClaw shared profile>
--headless
--viewport=1365x768
--no-usage-statistics
```

隐藏共享 Profile provider 必须拒绝 `--isolated`、用户提供的 Profile 路径、浏览器端点和
自动 Chrome 附着参数。

## 读取路径

```text
browser.read
  -> 为选中的 Profile 启动或复用 headless Chromium
  -> new_page 目标 URL
  -> 等待渲染状态
  -> evaluate_script 获取 DOM/HTML/text 诊断
  -> 在 Chromium 外运行 Readability
  -> 返回内容和结构诊断
```

`browser.snapshot` 和安全后续工具使用同一个 active headless session，不再启动第二个隔离浏览器。

## 认证转换

隐藏 Chromium 检测到登录或验证墙时：

1. 创建 `BrowserLoginBlock`。
2. 停止 headless Chromium 并释放 Profile。
3. 使用同一个 executable 和 Profile 启动可见 Chromium。
4. 复用或打开交接页面。
5. 等待用户完成人工步骤。
6. 取得 selected 登录后 URL。
7. 停止可见 Chromium。
8. 使用同一个 Profile 重新启动 headless Chromium。
9. 读取并验证登录后 URL。

Runtime 不比较登录后 origin 与原始 origin。

## 可见性规则

以下任务默认隐藏：

- 公开和认证后读取；
- 搜索结果检查；
- snapshot 和 DOM 结构检查；
- 导航和安全 read-only 交互；
- 登录验证完成后继续原任务。

以下情况使用可见 Chromium：

- 输入密码；
- 验证码、短信、2FA、passkey、授权、同意和支付确认；
- 网站登录后仍拒绝 headless；
- 用户明确要求查看浏览器界面。

## 生命周期

- 一个 Profile 只有一个 active Chromium/MCP 进程。
- visible/hidden 切换前关闭当前进程。
- Gateway 关闭时由 `Close()` 停止当前进程。
- 启动和停止都有 timeout。
- 尊重 Chromium Profile 锁，不能强制删除。
- Profile 使用中不能复制。

## 输出字段

Browser output 应保留：

- `browser_mode`
- `presentation`
- `surface_visible`
- `browser_provider`
- `browser_profile_id`
- `browser_actions`
- `read_mode`
- `auth_challenge_detected`
- `needs_structure_snapshot`
- `final_url`

Profile 文件路径和认证材料不能对模型可见。

## 失败语义

- Chromium executable 缺失；
- 共享 Profile 路径非法或 busy；
- hidden Chromium 启动失败；
- visible 到 hidden 切换失败；
- 页面读取失败；
- 用户报告完成后认证墙仍存在；
- 网站拒绝 headless。

这些情况必须在工具输出和 audit event 中保持可区分。

## 验收测试

- 隐藏和可见 session 使用同一个 Chromium executable 和 Profile。
- 隐藏启动为 headless 且不使用 isolated。
- 同一个 Profile 的隐藏和可见 session 不会重叠。
- 隐藏读取可以复用可见 Chromium 创建的登录态。
- 跨 origin SSO 从登录后 URL 恢复。
- 不发生 Cookie/storage 导出或注入。
- 普通读取不显示浏览器窗口。
- 人工验证可以可靠切换到可见 Chromium。
