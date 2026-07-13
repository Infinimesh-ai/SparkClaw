# 个人 Chrome 附着方案（已废止）

> 语言：[English](../../docs/personal-chrome-attachment-plan.md) | 简体中文

截至 2026-07-10：SparkClaw 不会连接用户日常 Chrome Profile，也不会把 Chrome 作为独立的
可见浏览器 provider。

当前确定的方案是
[托管共享 Chromium Profile](managed-persistent-browser-profile.md)：

- 隐藏和可见形态统一使用 Chromium；
- 两种形态共享同一个 SparkClaw-owned 持久 Profile；
- 普通任务使用 headless；
- 只在人工验证或用户明确要求时显示 Chromium；
- 登录态始终保留在共享 Chromium Profile 中；
- 登录恢复使用登录后的实际 URL，不要求登录前后同源。

保留本文档仅为了防止旧链接继续把已放弃的个人 Chrome 附着方案描述成有效计划。
