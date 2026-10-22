# Provider icon sources

Original provider images were collected as visual references for the imagegen
pass used by the Connections detail panels. The generated raster artwork is
styled for SparkClaw using brand blue `#086DE1` and a consistent fine-line
treatment. The generated raster alpha is used as a CSS mask so the active
brand-blue theme token controls its display color at 22 CSS pixels. The original
references retain each provider’s identifying shape.

The generated lossless WebP assets live in `providers/`. Their generation
prompts, original output filenames and delivery details are recorded in
[`providers/generation.json`](providers/generation.json).

Reference images were checked on 2026-10-20. The four App Store records below
were obtained from Apple's public lookup endpoint; developer identity, image
MIME type, pixel dimensions and appearance were verified before use.

| Provider | Official original reference | Reference file |
| --- | --- | --- |
| QQ Mail | [QQ邮箱](https://apps.apple.com/us/app/qq%E9%82%AE%E7%AE%B1/id473225145?uo=4) — Tencent Technology (Shenzhen) Company Limited | `qq_mail.jpg`, JPEG, 512 × 512 |
| Microsoft Outlook | [Microsoft Outlook](https://apps.apple.com/us/app/microsoft-outlook/id951937596?uo=4) — Microsoft Corporation | `outlook.jpg`, JPEG, 512 × 512 |
| ChatGPT | [ChatGPT](https://apps.apple.com/us/app/chatgpt/id6448311069?uo=4) — OpenAI OpCo, LLC | `chatgpt.jpg`, JPEG, 512 × 512 |
| Grok | [Grok AI](https://apps.apple.com/us/app/grok-ai/id6670324846?uo=4) — X Corp. | `grok.jpg`, JPEG, 512 × 512 |
| Gmail | [Official Google Workspace product icon](https://knowledge.workspace.google.com/static/images/icons/product/gmail.svg), linked from [Google Workspace Help](https://knowledge.workspace.google.com/admin/getting-started/brand-your-internal-communications-with-google-workspace) | `gmail.png`, unchanged raster of official SVG, 768 × 768 |
| Claude | [Official Anthropic press kit](https://www.anthropic.com/press-kit), `Anthropic media resources/Anthropic logos/Claude logos/3 Claude Spark/PNG/Claude Spark - Clay.png` | `claude.png`, original PNG, 937 × 937 |
| Gemini | [Official Gemini product page](https://gemini.google.com/) — [original Google image](https://www.gstatic.com/lamda/images/gemini_sparkle_4g_512_lt_f94943af3be039176192d.png) | `gemini.png`, original PNG, 512 × 512 |
| Weixin | [Official Weixin product page](https://weixin.qq.com/), header logo from its original `.logo` background SVG | `weixin.png`, unchanged raster of official SVG, 510 × 416 |
| Telegram | [Official Telegram logo resources](https://telegram.org/tour/screenshots), logo package member `Logo.png` | `telegram.png`, original PNG, 1000 × 1000 |

The reference image files and lookup metadata were staged locally under
`/tmp/sparkclaw-provider-references/` for generation. They are original-source
references rather than the assets rendered in the application.

QQ Mail's official app icon uses an envelope; the website favicon currently
uses a penguin and segmented circle. The app icon was selected as the source
for this set. Outlook's official app icon currently uses a folded blue
envelope. ChatGPT retains its six-loop knot; Grok retains its circular orbit
and diagonal cut.

Brand names and original marks belong to their respective providers.
