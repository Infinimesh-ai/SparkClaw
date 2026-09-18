# 安全邮件结构化内容呈现设计

> 语言：[English](../../docs/email-safe-html-preview-design.md) | 简体中文

状态：2026-09-18 已将首版“净化 HTML + iframe”实现升级为 `structured-mail-v3` 结构化内容投影。
Gateway 从邮件 HTML 中提取有意义的标题、段落、列表、表格、引用、代码、已验证 CID 图片及仅由用户
点击触发的 HTTP(S) 操作，只把封闭语义树交给 WebChat；发件人 HTML 与 CSS 不进入前端。WebChat 用
自身 React 组件和主题样式排版，不再使用 iframe；失效／远程图片会被完整省略，不显示破图占位符。

## 决策摘要

| 问题 | 决定 |
|---|---|
| 何时生成结构化内容？ | 新邮件复用现有单次 MIME 解析；当前项目数据在切换前由运维命令串行迁移一次。 |
| 在哪里呈现？ | API 只返回封闭语义树，WebChat 将已知节点映射为 React 组件；不插入 HTML，也不使用 iframe。 |
| 是否加载远程图片、字体或 CSS？ | 不加载；展开内容不会发出发件人控制的请求，只有用户明确点击已校验 HTTP(S) 操作时才会访问外部地址。 |
| 内嵌图片怎么办？ | 严格数量和字节限制内，只允许已校验的 CID PNG／JPEG／WebP；其余节点直接省略，不显示占位。 |
| 每次重新部署是否重新生成？ | 不会；投影按原件哈希和净化器版本成为不可变持久数据，重启后复用。 |
| 旧预览如何替换？ | 最终只保留一个结构化内容入口；纯文本作为普通文本节点呈现，旧纯文本与 HTML iframe 均不再提供。 |
| 点击后多久看到？ | 所有预览必须在可打开前预生成；点击至首次绘制 p95 不超过 300 ms。 |

## 问题与目标

首版安全预览保留了过多发件方 HTML 布局并装入固定高度 iframe。远程图片被移除后，无 `src` 的图片
节点仍可能出现破图占位；固定的小窗口和发件方样式也让正文难以阅读。

目标是从不可信邮件中提取可读结构，再按 SparkClaw 的视觉体系协调排版，同时保持现有 owner 边界，
并保证打开内容不会执行脚本、提交表单、导航应用、联系发件人或加载追踪像素。首次投影后必须快速，
普通重新部署后继续复用，并随整个对话删除。由于项目目前没有用户，产品不保留通用历史迁移机制或
双预览兼容模式。

## 非目标

- 完全像素级还原 Outlook、Gmail 或 QQ 的所有渲染差异。
- 运行邮件中的脚本、表单、动画、嵌套页面、媒体、插件或交互组件。
- 呈现时加载远程图片、远程字体、外链样式表或 CSS import。
- 把结构化内容投影当成来源证据；不可变 `.eml` 仍是唯一原件。
- 使用模型重建布局或正文。
- 一次性项目迁移遇到来源不可用时重新从邮箱服务商抓取。
- 改变主对话摘要、回复框、发送链路或对话归属。

## 用户体验

最终每封邮件提供两个正文相关入口：

1. **查看完整邮件内容**——新的、唯一用户可见正文视图；在摘要下方单独展开，不再嵌套于技术详情。
2. **下载 `.eml`**——原件仍存在时下载不可变来源字节。

展开后在当前邮件卡片内直接显示结构化内容，不改变对话滚动位置，也不形成第二层纵向滚动窗口。
标题、段落、列表、引用、代码与表格统一使用 SparkClaw 的字号、间距、颜色和边界；只有过宽表格自身
允许横向滚动。窄屏占满可用宽度。无法安全显示的图片节点被省略，因此不会出现破图图标或替代占位。

界面称它为“完整邮件内容”，而不是“原始页面”：这是协调后的可读内容，不承诺还原发件人视觉。
合法的绝对 HTTP(S) 地址会成为仅由用户点击触发的操作，并在发件人标签旁明确显示实际域名；危险、
相对或带 URL 用户名／密码的地址会降级为普通文字。界面不再为每封邮件重复显示“已阻止远程内容”，
因为呈现本身不会加载远程资源。

最终运行时不会在用户点击后才生成预览。如果投影意外不可用、过大、损坏或被净化器拒绝，界面说明
安全预览不可用，并在可能时继续提供 `.eml` 下载；不会暴露已退役的纯文本预览或原始 HTML。

## 端到端流程

```text
不可变 message.eml
  → 现有有界 MIME 遍历（新邮件只读一遍）
  → 选择主 multipart/alternative HTML 正文
  → 只解析已校验且受限的 CID 位图资源
  → 提取封闭语义节点、丢弃 HTML/CSS，仅保留已校验的点击操作
  → 原子发布不可变 JSON 内容产物
  → Store 元数据绑定 owner/邮件/representation/来源哈希/版本
  → 鉴权 JSON 获取
  → WebChat 将已知节点映射为 React 组件
```

摘要和列表路径绝不加载该产物。工作只发生在新邮件正常解析，或切换前明确执行的一次性项目迁移；
不调用模型，也不访问邮箱服务商网络。

## MIME 选择与还原范围

新邮件必须继续遵守“一次完整解析”不变量。MIME 遍历同时返回现有 `EmailRepresentation` 与一份仅在
内存中存在的有界渲染候选：

- `multipart/alternative` 选择与主正文对应的最后一个受支持 `text/html`，不拼接纯文本与 HTML。
- `multipart/related` 中的 `cid:` 只能指向同一封已校验 MIME 树内的 part。
- 没有 HTML 时，把 `text/plain` 保存为预格式文本节点并保留换行。
- 嵌套 `message/rfc822` 用带明确边界的引用区展示。
- 普通附件不插入预览，继续留在现有附件入口。
- 拒绝发件人提供的 `data:`／`blob:` URL；只有 SparkClaw 为已校验内嵌位图生成的有界 data URL 可用。

提取器只保留可读语义：段落、标题、表格、列表、引用、强调、代码、换行和分隔线。发件方字体、颜色、
定位、宽高、class、id 与全部 CSS 都会丢弃；最终视觉完全由 SparkClaw 控制。必须使用真实 HTML5 parser，
不能靠正则截取正文。

## 安全边界

邮件始终是不可信输入。设计使用相互独立的服务端和浏览器防线，避免一条净化规则遗漏便影响 WebChat。

### 结构提取

Gateway 至少移除 `script`、`iframe`、`frame`、`frameset`、`object`、`embed`、`applet`、`form`、
`input`、`button`、`select`、`textarea`、`video`、`audio`、`canvas`、`portal`、`base`、主动 `meta`、
外链样式表及 SVG／MathML；同时移除事件属性、`srcdoc`、脚本 URL、刷新／导航指令和发件人控制的
`data:`／`blob:` 内容。链接目标只有在绝对 HTTP(S)、不含 URL 用户名／密码、不含控制字符且不超过
8 KiB 时才保留；其余目标失去导航能力，但可读子文字继续显示。

提取器不输出任何 CSS 或通用属性，因此无需尝试修补 `@import`、`url()`、fixed／sticky、z-index 等发件方
布局。隐藏节点（`hidden`、`aria-hidden=true`、`display:none`、`visibility:hidden`、`opacity:0`）不进入投影。
未知布局包装只降级为无属性 section。合法操作中的远程图片最多贡献有界 alt 文字作为操作名称，图片
本身仍被省略。

### 资源策略

- 呈现内容时不请求远程 HTTP(S) 图片或资源；只有用户明确点击后才访问已校验的操作地址。
- CID 仅接受声明类型与检测类型一致的 PNG、JPEG 或 WebP。
- SVG、HTML、PDF 及其他主动或文档格式绝不作为图片嵌入。
- 最多嵌入 20 个资源、解码后合计 5 MiB；超出或不可用资源直接省略，不生成占位节点。
- Cookie、Bearer token、服务商凭据和本地文件路径不得进入预览文档。
- 邮件提供的操作 URL 本身可能含一次性令牌；它只存在于 owner-scoped 产物和鉴权响应中，不写日志，
  HTML 操作也不会把完整地址展开为可见文字，只额外显示域名。纯文本邮件仍按来源原样显示其 URL。

### 浏览器呈现边界

WebChat 通过现有鉴权 API 获取封闭语义树，只按固定 `kind` switch 创建 React 元素；未知 kind 返回空，
图片会再次限定为 PNG／JPEG／WebP data URL，链接也会再次校验为不含 URL 凭据的绝对 HTTP(S)。没有
`dangerouslySetInnerHTML`、`srcdoc`、iframe 或任意标签创建。操作链接只有点击后才以新标签打开，并
带 `noopener noreferrer nofollow` 与 `referrerpolicy=no-referrer`；可见域名同时对普通用户和辅助技术
公开真实请求主机。邮件文字只能作为 React 文本节点被转义，表格等结构使用应用自身 CSS。

## 持久化与版本

结构化内容是与语言无关的派生投影，不是新来源。不能向现有不可变 `EmailRepresentation` 加入可变正文；
新增独立记录，字段等价于：

```text
EmailRenderPreview
  id, owner_id, mail_id, capture_id, representation_id
  source_sha256, sanitizer_version, state
  artifact_path, artifact_sha256, artifact_bytes
  embedded_resource_count, embedded_resource_bytes
  failure_code, created_at
```

产物位于 owner-scoped normalized 树，例如：

```text
email/<owner-scope>/normalized/<representation-id>/render/<sanitizer-version>/
  content.json
  manifest.json
```

两个文件都有大小边界、仅服务账号可读写，并在 Store 接纳前以不可变方式发布；Store 记录只能在哈希与大小最终确定后提交。
身份由 owner、representation ID、来源 SHA-256 和净化器版本导出，因此重放幂等。

使用相同净化器版本重新部署时直接复用产物；版本升级产生新产物。某版本被安全退役后不再提供旧产物。
即使原件已清理，也不能为了可用性继续展示已退役 HTML 投影；无法重建时将结构化内容标记为不可用。

手动清理邮件原件会删除 `.eml` 来源字节，但可保留已净化投影，与现有解析文本保留语义一致。删除整个
对话时，预览记录和文件必须与其他邮件数据一并删除。孤儿恢复可以删除无引用派生产物，但不得扫描或
修改邮箱服务商状态。

## 一次性项目迁移与切换

新邮件在生成 `BodyText` 的同一次 MIME 解析中产生预览候选；净化或发布预览失败不能使纯文本
representation 失败，只记录独立预览状态，邮件摘要仍然可读。`BodyText` 继续作为解析、摘要、分类和
搜索的内部证据；删除旧预览不等于删除该内部数据。

产品不增加后台补齐 cursor、兼容 worker、点击后生成任务或双预览 feature flag。最终部署前，由仅运维可用
的一次性迁移命令按有界分页枚举当前项目 Store，并从本地已校验 `.eml` 一次处理一封保留邮件。该命令
不访问 QQ、Gmail 或 Outlook，不调用模型、不改变已读状态、也不发送邮件；按 owner、representation、
来源哈希和净化器版本保持幂等，并输出准确的 ready／unavailable／failed 覆盖报告。

已实现的命令为：

```bash
go run ./services/gateway/cmd/sparkclaw \
  -config configs/sparkclaw.default.json \
  -migrate-email-render-previews <owner-id>
```

命令输出 JSON 覆盖报告；只要有一封邮件校验或生成失败，就以非零状态退出。

切换要求当前所有保留邮件都有经过校验的 ready 投影。来源缺失、损坏、哈希不符、过大或被净化器拒绝
时，切换停止并报告准确邮件；不得静默删除，也不得向服务商重抓。迁移覆盖率、安全、视觉和性能检查
通过后，在同一实施变更中删除旧纯文本预览组件、标签、API client、
`GET /api/email/messages/{mail}/preview` 路由、无用 CSS 和旧界面测试，并用新预览测试替代。最终部署版本
不会同时暴露两种预览。

## API 契约

一个 owner 鉴权内部接口足够：

```text
GET  /api/email/messages/{mail}/render-preview
```

`GET` 只读，返回 `ready`、`unavailable` 或 `failed`。ready 响应包含封闭语义节点、投影版本、
representation revision 和资源计数；响应使用 `Cache-Control: no-store` 与
`X-Content-Type-Options: nosniff`。

接口重新校验邮件 owner、capture owner 及 representation 绑定；revision 过期时直接拒绝，避免显示已被
替换来源的内容。结构化正文不进入对话列表响应、
搜索索引、模型输入、日志、遥测或错误信息。

## 大小与资源上限

初始实现采用保守限制：

| 项目 | 上限 | 超出后的处理 |
|---|---:|---|
| 选中的输入 HTML part | 解码后 2 MiB | 预览不可用；邮件摘要和 `.eml` 保留 |
| DOM 节点 | 50,000 | 预览不可用；邮件摘要和 `.eml` 保留 |
| 结构化 JSON 产物 | 2 MiB | 内容不可用；邮件摘要和 `.eml` 保留 |
| 内嵌资源 | 20 个／解码后共 5 MiB | 超出资源直接省略 |
| 一次性项目迁移 | 一个串行进程 | 不增加产品运行时 worker 或用户可见争用 |

这些只是预览限制，不改变现有 110 MiB 原件 envelope 或附件保留契约。

## 性能契约

在 Remote 部署参考主机上测量从点击到首次预览绘制，同时用浏览器网络记录确认点击操作前没有自动的
发件人控制请求：

| 场景 | 验收目标 |
|---|---|
| 界面响应点击 | p95 <= 100 ms |
| 当前版本缓存投影 | p95 <= 300 ms，p99 <= 750 ms |
| 新邮件额外解析开销，HTML <= 2 MiB | p95 <= 150 ms；与来源下载分开测量 |
| 相同版本重启／部署后再次打开 | 沿用缓存目标，不重新生成 |
| 一次性迁移 | 离线测量并在切换前完成，不增加用户点击路径工作 |

以上是未来发布门槛，不是对当前版本的实测结论。远程图片被拦截，因此其不可控延迟不计入；对话列表
和主摘要不得出现可测量退化。

## 失败与删除语义

- 净化失败：记录有界稳定错误码、显示预览不可用，绝不暴露被拒 HTML，也不恢复旧纯文本界面。
- 预览文件丢失或哈希不符：停止提供；原件仍在时标记为可修复，但只能由精确运维修复重建，用户点击或
  后台兼容 worker 都不会生成。
- 首次生成前原件丢失：结构化内容终态不可用，不向服务商重抓。
- 新邮件解析中服务重启：遵循现有持久 parse job 规则；一次性迁移被中断后由运维明确重跑，完成产物仅在
  哈希和 owner 校验后复用。
- 删除整个对话：继续遵循发送中／结果未知的 fail-closed 规则，再于同一删除边界清理预览记录和文件。
- 回滚部署：`BodyText` 仍保留在 representation 内供内部处理，因此可协调回滚旧镜像；最终正向版本
  不再暴露旧预览路由。

## 验证与发布门槛

完成以下全部验证前不得部署实现：

1. 恶意语料覆盖脚本／事件属性、表单、meta refresh、CSS import／URL、导航、SVG、嵌套 frame、畸形
   HTML、MIME 混淆和超大输入；不得产生执行、自动导航或网络请求，危险操作地址必须降级为文字。
2. Chromium 请求日志证明打开 QQ、Gmail、Outlook 和合成营销邮件但不点击操作时没有远程请求；用户
   点击测试则要确认目标经过校验、域名可见、新标签隔离且不发送 referrer。
3. 视觉金样覆盖表格 Newsletter、交易回执、验证码、引用回复、在新安全容器中显示的纯文本邮件、
   CID 图片、深色邮件和窄屏布局。
4. owner scope、过期 revision、来源哈希、直接导航和跨 owner API 测试全部 fail closed。
5. 重启／重新部署证明相同版本投影被复用而非重新生成。
6. 一次性运维迁移幂等、串行、有界，不触发服务商同步或模型调用，并输出准确覆盖率；任何保留邮件
   没有 ready 投影时都必须停止切换。
7. 原件清理后当前净化投影仍可用；整段对话删除会清除投影，不留孤儿文件。
8. 性能表必须实测而非推断，并分别记录普通和大型邮件结果。
9. Gateway 全量测试／build／vet、重点 race、WebChat 测试／build／i18n 及双语文档检查通过。
10. 先用非敏感测试邮件做现场验收；生产验收只打开 owner 选中的邮件，并确认不会附带发送、标记、
    同步，且明确点击操作前不会获取远程资源。

### 可点击操作部署证据（structured-mail-v3，2026-09-18）

- 当前 owner 的 8／8 封保留邮件均已生成 `structured-mail-v3` 记录与 `content.json`；首次迁移为
  `migrated=8,failed=0`，部署镜像复跑为 `reused=8,failed=0`。
- 8 份产物共 1,086 个语义节点、52 个 HTTP(S) 操作节点和 26 个可见域名；结构扫描发现 0 个非法
  操作 URL 和 0 个远程图片 source。当前样本没有 CID 图片节点。
- 线上打开历史工作区邀请邮件，确认“加入工作区”和实际域名共同组成协调后的操作项；DOM 属性为
  `target=_blank`、`rel="noopener noreferrer nofollow"`、`referrerpolicy=no-referrer`。验收只检查链接，
  没有点击或访问认证地址。
- Remote Gateway 镜像 `sha256:de17b2ba8b085aa4b22c760535ec4167ee3f20f072669b7c242f97ae280f69c4`
  与 WebChat 镜像 `sha256:ead37ba2ae0de2a7d12a1270b266bd5fa167c4bb5badef0c8f602cd4dbe52fc1`
  运行健康，WebChat HTTP 200。
- 迁移未访问邮箱服务商或调用模型；部署和验收没有发送、删除、手动同步邮件，也没有导航到提取出的
  操作地址。邮件库存和接收开关不变。

### 首版结构化内容证据（structured-mail-v2，已由 v3 取代）

- 当前 owner 的 8／8 封保留邮件均已生成 `structured-mail-v2` 记录与 `content.json`，首次迁移为
  `migrated=8,failed=0`，部署镜像复跑为 `reused=8,failed=0`。
- 8 份产物共 982 个语义节点；结构扫描没有发现未知 kind、远程 source 或非法图片 source。当前样本
  没有可用 CID 图片，因此最终内容中也没有图片节点。
- WebChat 不再包含 iframe、`srcdoc` 或任意 HTML 插入。真实历史邀请邮件现场验收中，主摘要不变，
  “查看完整邮件内容”作为独立一级 disclosure 出现在摘要下方；原单列表格布局被展平为普通段落，
  多列表格仍使用有界横向滚动表格，页面没有破图占位。
- Remote Gateway 镜像 `sha256:08056e1767eed103b049b2bbe85b6c0e8b4256a3d745751d0c6dbdfbf0dbad15`
  与 WebChat 镜像 `sha256:56ce211ebe7d5eaf525331d8c5d4c3b43186be1faba2dacddcc0348a7e248268`
  运行健康，WebChat HTTP 200。
- 迁移未访问邮箱服务商或调用模型；部署和验收没有发送、删除或手动同步邮件。库存仍为 8 封，QQ
  收件开启，Gmail／Outlook 关闭。

### 首版 HTML 预览历史证据（2026-09-17，已由 v2 取代）

- PostgreSQL 迁移 `0016_email_render_preview.sql` 已应用。当前 owner 有 8 条邮件记录和 8 条 `ready`
  预览记录；首次运维迁移报告 `migrated=8, failed=0`，部署镜像再次执行报告
  `reused=8, failed=0`。
- 工作区中有 8 份有界 `safe-mail-v1` HTML 产物，合计 97,859 字节；8 份都包含严格 CSP，结构扫描
  未发现主动元素、事件属性、链接目标或远程资源属性。
- 已部署且鉴权通过的 WebChat 打开了一封真实迁移后的验证码邮件；表格排版和可见验证码均保留。
  iframe 实际属性为 `sandbox=""`、`referrerpolicy="no-referrer"`，并有无障碍标题；对话主界面仍以
  本地化摘要为主。
- Remote Gateway 镜像 `sha256:a8bd27df487421fbddae1622b12cdf15919e8af6ccba50b1b2463cb3ef6ea651`
  健康；WebChat 镜像 `sha256:f55fde5a1ed921d8215ef8ab6e151b393e11334a419e33b567ab8b8db5f034a6`
  正在运行并返回 HTTP 200。
- 旧 `/api/email/messages/{mail}/preview` 路由、client 方法、组件和 CSS 已从部署版本移除。唯一用户可见
  正文入口是安全预览，`.eml` 下载保持独立。
- 迁移和验收没有执行发送操作，也没有改动收件开关。延迟分位数、多服务商视觉金样及浏览器级请求
  追踪仍是未声明完成的后续证据；安全边界不依赖这些测量。

## 交付顺序

1. 增加预览记录／Store 契约、schema migration、owner-scoped 产物路径和删除／恢复语义。
2. 扩展现有 MIME 遍历，使其输出有界候选；用 HTML5 parser 提取封闭语义树，并测试危险节点、远程／
   失效图片彻底省略及大小／深度上限。
3. 增加读取接口、仅运维可用的一次性迁移命令和准确覆盖报告。
4. 增加 WebChat 语义节点到 React 组件的固定映射及中英文状态；不使用 iframe 或任意 HTML 插入。
5. 对当前项目全部邮件执行串行迁移，再通过恶意语料、owner 权限、持久化、删除、视觉与性能门槛。
6. 删除旧纯文本预览组件、路由、client 方法、标签、CSS 和旧测试，重跑全部门槛，并确认最终构建只提供
   结构化邮件内容与 `.eml` 下载。
7. 部署时不改变收件开关、不触发邮箱同步；验收迁移后的项目数据并发布实测结果。
