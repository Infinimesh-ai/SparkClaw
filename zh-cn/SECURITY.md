# 安全策略

> 语言： [English](../SECURITY.md) | 简体中文

SparkClaw 是 local-first personal agent runtime。它不是 hostile multi-tenant service，也不是 public internet Gateway。

## 支持版本

在项目发布 tagged releases 之前，`main` branch 是支持的开发线。安全修复应优先进入 `main`。

## 报告漏洞

请不要为疑似漏洞打开公开 issue。请通过仓库 owner 首选的私下联系方式报告给维护者。如果还没有私下渠道，请打开一个不包含利用细节的最小公开 issue，请求安全联系方式。

请包含：

- affected commit or version
- setup details
- impact
- reproduction steps
- 已移除 secrets 的 logs 或 traces
- 如果已知，附 suggested fix

## Security Boundary

预期安全默认值：

- Gateway 默认绑定 localhost。
- 共享机器应设置 `SPARKCLAW_API_TOKEN`。
- `.env`、state encryption keys、traces、local state 和 model caches 必须留在 git 外。
- Browser reads 默认拒绝 loopback/private hosts，除非显式 allowlist。
- Shell execution 通过 sandbox runner 且 network-disabled。
- Reversible 和 dangerous actions 需要 approval。
- Tool observations 是 untrusted data。

破坏这些边界的问题属于安全敏感问题。

## 不在范围内

- attacker 已经控制 owner account 的 host compromise。
- 违反文档部署建议，将 Gateway 错误公开到公网。
- SparkClaw 控制外的恶意模型权重或第三方服务。
- 用户主动将 secrets 粘贴到 prompts、files 或 traces 后又手动分享。
