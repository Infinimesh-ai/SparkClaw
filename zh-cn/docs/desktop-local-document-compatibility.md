# 本地文档兼容性验收

> 语言：简体中文 | [English](../../docs/desktop-local-document-compatibility.md)

这是一份有限、可复现的只读本地查看器发布矩阵，验证下表中的支持场景和受控拒绝行为；不承诺所有 Microsoft Office 分页、字体、动画、密码输入或任意嵌入内容。既有隔离与资源限制见[文件浏览设计](desktop-local-file-browser-design.md)。

## 验收矩阵

| 范围 | 合成输入与要求 |
|---|---|
| DOCX 与真正的旧版 DOC | 两页、纵向后横向；中英文、页眉页脚、编号段落、合并表格和图片；通过沙箱转换、页几何、文字／坐标及可见渲染检查 |
| PPTX 与真正的旧版 PPT | 三张宽屏幻灯片，含中英文、彩色图片和表格；三页及预期表格值均保留 |
| OOXML 缺失字体 | 刻意缺失的字体产生引擎反馈、可读后备 PDF 和局部可关闭提示；关闭提示后继续阅读 |
| 加密 DOCX／PPTX | 使用真正加密包，生成时独立解密逐字节比对原件；预览拒绝，不索取密码或尝试显示明文 |
| 加密 PDF | AES-256 样本及独立校验的 Standard Security R2 样本；明确要求解锁并另存副本，不调用原生密码对话框 |
| 损坏／签名不符的 Office 和 PDF | 截断 ZIP、非法 OLE 结构、文本伪装 DOC／PPT、畸形 PDF 均失败；随后普通文档仍可打开 |
| Office 嵌入内容 | 含嵌入对象条目的 OOXML 在转换前被拒绝 |
| 图片 | PNG、JPEG、WebP、静态 GIF、安全 SVG 可显示；损坏 PNG、超大尺寸、动态 GIF、含主动／外部资源的 SVG 明确失败 |
| 未支持的位图格式 | BMP 不在已验收的签名解码器范围内并被拒绝；同样不声明支持 AVIF |
| 文本 | UTF-8 中英文按字面显示；256 KiB 读取窗口边界上的多字节字符翻到下一段后保持；二进制／NUL 及非法 UTF-8 明确失败 |
| Markdown | script 标记不执行；远程图片和活动超链接不获准使用；不可用的相对图片无法越出查看器 |
| CSV／XLSX | 字面字符串和看似公式的 CSV 值；错误引号／UTF-8 拒绝；XLSX 保存的零／false、缺少缓存结果、合并、双表及只读选择／切换 |

[2026-10-29 机器可读记录](../../docs/evidence/desktop-local-format-matrix-2026-10-29.json) 包含 Mac 的 19 项 broker 场景和 25 项原生查看器场景。五份 Office 转换文档共 11 页，另通过独立 PDF 页几何、预期文字、按坐标排列的中文字形和非空白像素检查；PDFium 拼图目视确认表格、图片、混合方向及幻灯片版式符合样本，原生查看器检查每份文档的 canvas 以及字体提示关闭。这不是与 Microsoft Office 的像素对比。每份 broker 输入哈希保持不变。这些属于源码／隔离运行验证；精确安装包及原安装配置的结果由单独的发布验收记录。

生成器创建 37 份原始文件。broker 覆盖其中 18 份，加上一份现场生成的 XLSX，共 19 项；原生查看器覆盖另外 19 份原始文件、五份已接受 Office 原件衍生的 PDF，以及同一份 XLSX，共 25 项。合计是 38 份不同输入、44 次分阶段执行，机器记录逐文件列出对应阶段。既有原生回归还另行生成一份加密 R2 PDF，不计入这 37 份文件。

## 保真与支持限制

- 静态预览不编辑、重算文档，不执行宏、动画或嵌入对象。密码保护文件须在其他应用中解锁，另存未加密副本。
- 用户于 2026-10-29 明确免验加密的旧二进制 DOC／PPT，现不属于发布必验范围，也不构成发布阻塞；不将其记为测试通过。受保护的 OOXML 与 PDF 使用真实加密样本。这是一组可复现场景，不是所有格式与条件的笛卡尔积。
- 引擎能报告 OOXML 声明的缺失字体；旧二进制 DOC／PPT 不提供完整缺字体清单，空警告不证明字体精确匹配。不远程下载字体。
- 原字体与已安装系统字体可能影响分页或外观。仅对引擎有反馈的文档显示可关闭提示，阅读布局和原控件保留。
- 所测 DOC／DOCX 导出的中文字形位置正确，但 PDF 逻辑提取顺序可能按不同字体的文字段重组；复制／提取顺序不保证逐字复现原句。矩阵保留这一差异，不把视觉通过当成语义提取完全一致。
- 部分不支持的图片／文本当前显示解码器级错误；此处验证的是可见、有界拒绝，不代表所有错误均已本地化或这些格式已获支持。

免验前曾使用隔离配置探测现有制作工具 LibreOfficeDev 26.8.0.0.alpha0（修订 `2c87e51eeaa2b413ff4ae097b2705eea1995d8e5`）：CLI 的 `Password` 过滤参数产生真正但未加密的 DOC／PPT，由独立 `msoffcrypto` 检查确认；Basic UNO `storeAsURL` 探针返回时既没有初始执行标记，也没有输出文件，环境还缺少 Python UNO 绑定。这仅说明本环境所尝试的制作路径，不能推断所有 LibreOffice 都不支持加密导出。用户免验后已停止进一步探测。

## 复现

样本制作工具链与产品运行时分离。在一次性 Python 环境中安装样本依赖，并把 LibreOffice CLI 作为 `soffice` 加入路径，或传入 `--soffice`。该工具只负责导出真正的 DOC／PPT 测试文件，产品始终使用自身打包引擎。

```sh
python3 -m pip install -r apps/desktop/test/document-format-fixtures.requirements.txt
python3 apps/desktop/test/generate-document-format-fixtures.py /tmp/sparkx-format-fixtures
node apps/desktop/test/run-local-preview-format-matrix.mjs /tmp/sparkx-format-fixtures /tmp/sparkx-format-results
python3 apps/desktop/test/inspect-document-format-pdfs.py /tmp/sparkx-format-results
npm run build:desktop-ui
node apps/desktop/test/run-local-preview-format-native.mjs /tmp/sparkx-format-fixtures /tmp/sparkx-format-results
node apps/desktop/test/run-local-preview-native.mjs
```

生成器拒绝覆盖现有样本目录。每次保存其 manifest 与结果 JSON；加密包采用新盐值，重建的密文哈希不必等于前次。`SPARKCLAW_FORMAT_MODULES` 与 `SPARKCLAW_PREVIEW_RUNTIME` 可指定候选包精确 broker 模块和 worker 可执行文件，`SPARKCLAW_PREVIEW_TEST_ASSETS` 指定精确查看器资源，`SPARKCLAW_FORMAT_SCREENSHOTS` 可选保留原生截图。Linux 原生命令须在已验收的显示环境下执行，不能用 Mac 观察替代 Linux 引擎／隔离证据。
