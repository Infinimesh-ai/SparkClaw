# 服务商图标来源

> 语言： [English](../../../../../../apps/webchat/src/assets/connections/provider-brand-sources.md) | 简体中文

各服务商的原始图片仅作为视觉参考，用于“连接”详情面板的 imagegen 生成。
生成的位图采用 SparkClaw 品牌蓝 `#086DE1` 和统一的细线风格。位图的 alpha
通道作为 CSS 遮罩使用，因此显示颜色由当前主题的品牌蓝令牌控制，渲染尺寸为
22 CSS 像素。原始参考保留了每个服务商的识别形状。

生成的无损 WebP 资源位于 `providers/`。生成提示词、原始输出文件名和交付细节记录在
[`providers/generation.json`](../../../../../../apps/webchat/src/assets/connections/providers/generation.json)。

参考图片于 2026-10-20 核对。下表中四条 App Store 记录来自 Apple 公开查询接口；
使用前已核实开发者身份、图片 MIME 类型、像素尺寸和外观。

| 服务商 | 官方原始参考 | 参考文件 |
| --- | --- | --- |
| QQ 邮箱 | [QQ邮箱](https://apps.apple.com/us/app/qq%E9%82%AE%E7%AE%B1/id473225145?uo=4) — Tencent Technology (Shenzhen) Company Limited | `qq_mail.jpg`，JPEG，512 × 512 |
| Microsoft Outlook | [Microsoft Outlook](https://apps.apple.com/us/app/microsoft-outlook/id951937596?uo=4) — Microsoft Corporation | `outlook.jpg`，JPEG，512 × 512 |
| ChatGPT | [ChatGPT](https://apps.apple.com/us/app/chatgpt/id6448311069?uo=4) — OpenAI OpCo, LLC | `chatgpt.jpg`，JPEG，512 × 512 |
| Grok | [Grok AI](https://apps.apple.com/us/app/grok-ai/id6670324846?uo=4) — X Corp. | `grok.jpg`，JPEG，512 × 512 |
| Gmail | [Google Workspace 官方产品图标](https://knowledge.workspace.google.com/static/images/icons/product/gmail.svg)，链接自 [Google Workspace 帮助](https://knowledge.workspace.google.com/admin/getting-started/brand-your-internal-communications-with-google-workspace) | `gmail.png`，官方 SVG 的原样栅格化，768 × 768 |
| Claude | [Anthropic 官方媒体资料](https://www.anthropic.com/press-kit)，`Anthropic media resources/Anthropic logos/Claude logos/3 Claude Spark/PNG/Claude Spark - Clay.png` | `claude.png`，原始 PNG，937 × 937 |
| Gemini | [Gemini 官方产品页](https://gemini.google.com/) — [Google 原始图片](https://www.gstatic.com/lamda/images/gemini_sparkle_4g_512_lt_f94943af3be039176192d.png) | `gemini.png`，原始 PNG，512 × 512 |
| 微信 | [微信官方产品页](https://weixin.qq.com/)，取自页头 `.logo` 背景 SVG | `weixin.png`，官方 SVG 的原样栅格化，510 × 416 |
| Telegram | [Telegram 官方标志资源](https://telegram.org/tour/screenshots)，标志包内的 `Logo.png` | `telegram.png`，原始 PNG，1000 × 1000 |

参考图片文件和查询元数据在生成时暂存于本地 `/tmp/sparkclaw-provider-references/`。
它们是原始来源参考，并非应用中实际渲染的资源。

QQ 邮箱官方应用图标使用信封；其网站 favicon 目前使用企鹅和分段圆环。本组图标
以应用图标为来源。Outlook 官方应用图标目前为折叠的蓝色信封。ChatGPT 保留其
六环结；Grok 保留圆形轨道和斜切。

品牌名称和原始标志归各自服务商所有。
