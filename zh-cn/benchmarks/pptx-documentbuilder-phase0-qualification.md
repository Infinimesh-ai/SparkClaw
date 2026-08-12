# DocumentBuilder PPTX Phase 0 资格测试

> 语言： [English](../../benchmarks/pptx-documentbuilder-phase0-qualification.md) | 简体中文

| 字段 | 结果 |
|---|---|
| 日期 | 2026-08-11 |
| 决策 | **NO-GO** |
| 提案 | DocumentBuilder 9.4.0 + OR-Tools 确定性 PPTX 排版 runtime |
| 停止规则 | 第一个强制资格 gate 失败时立即停止 |
| 失败 gate | 免费版本的真实文字测量、稳定 package identity 和保真 |
| 仓库 revision | `8faa421c855969131bc99b272059231e4b79f1a9` |
| 主机 | Linux `aarch64`，kernel `6.17.0-1026-nvidia` |

## 结果

DocumentBuilder + OR-Tools 提案被否决。固定的 DocumentBuilder 9.4.0 ARM64 artifact
可以打开测试演示文稿，也能暴露 slide、shape、文字、外框几何和修改方法，但测试过的
Presentation SDK 没有暴露实际排版后的文字 bounds、文字已用高度或有效 overflow 结果。

现有 `RecalculateAutoFit()` 不是 overflow 测量。对于相同尺寸文本框中的短文字和故意放入的
超长中英文混合文字，它都返回 `true`。重新计算后，公开序列化状态仍然只有 `normAutoFit`
模式和声明的 24 pt run 字号，没有有效缩放值、已用 bounds、行布局或 overflow 状态。

因此，强制测量 gate 失败。根据 owner 关于区分项目适配和接入错误的要求，随后只执行了一次
根因保存探针，并分别通过两个官方接口复现。免费版本添加了 `Unregistered Version` 水印，
重写 non-visual shape ID，并改变无关 package part。这构成额外的强制 identity 和保真失败，
但该探针没有恢复迁移。

根据[设计中的 fail-fast 规则](../../docs/pptx-deterministic-layout-runtime-design.md)，没有继续执行
其余资格 gate，也没有开始任何生产集成。

## 根因分类

该结果属于 **DocumentBuilder 项目/免费版本不适合承担本设计职责**，不是 SparkClaw 接入错误。

- 两项失败都通过解压后的上游 Python SDK 和上游 `docbuilder` CLI 直接复现，没有 SparkClaw
  package、adapter、schema、process runner 或模型调用参与。
- 官方 Presentation API 缺少设计所需的测量结果。这是 API capability mismatch，不是参数绑定错误。
- 官方安装文档明确说明免费版本会给所有生成文档加水印；native 保存结果复现了这一版本行为。
- OR-Tools 不是失败组件。它可以优化有限候选，但无法创造文档引擎没有提供的可信文字测量。

DocumentBuilder 仍可以创建、编辑、转换和渲染演示文稿。No-Go 结论范围更窄：免费 AGPL
artifact 不能在已接受设计下充当 SparkClaw 的保真、确定性 PPTX 测量与写入后端。

## 基线

资格测试前已建立现有仓库基线：

| 检查 | 结果 |
|---|---|
| `npm run setup:document-tools` | 通过 |
| `services/gateway` 中的 `go build ./...` | 通过 |
| `services/gateway` 中的 `go vet ./...` | 通过 |
| `services/gateway` 中的 `go test ./...` | 通过 |
| `npm --workspace @sparkclaw/webchat test` | 通过，10 个文件、21 项测试 |
| `npm --workspace @sparkclaw/webchat run build` | 通过 |

这说明 No-Go 结果并非来自现有 Python PPTX adapter 或 Gateway 测试故障；现有基线保持绿色。

## 资格测试 Artifact

测试使用设计指定的准确 release：

```text
Release: ONLYOFFICE DocumentBuilder v9.4.0
Asset: onlyoffice-documentbuilder-linux-aarch64.tar.xz
Release size: 66971536 bytes
Expected SHA-256: 8756b0b57a4ab4b27608a6b1fbeb13f67670bde0245da166907277821afbeb8e
Observed SHA-256: 8756b0b57a4ab4b27608a6b1fbeb13f67670bde0245da166907277821afbeb8e
Runtime version: 9.4.0.130
```

`file` 将 `docbuilder`、`x2t`、`libdocbuilder.c.so` 和 `libdoctrenderer.so` 识别为 ARM
aarch64 ELF binary。`ldd` 在不使用模拟的情况下解析了随包 library 和 host glibc。Artifact
身份和 native 启动通过；这不代表完整 Linux ARM64 gate 已通过，因为完整 gate 要求其他全部
强制测试也通过。

## 测量探针

### Fixture

在临时目录中生成一个包含两个相同外框文本 shape 的 16:9 PPTX：

```text
width: 2011680 EMU（2.2 英寸）
height: 502920 EMU（0.55 英寸）
font: Carlito，24 pt
word wrap: enabled
margins: zero
```

一个 shape 包含 `Fits`，另一个包含较长英文句子和简体中文文字。第二个 fixture 为两个 shape
都启用 PowerPoint normal AutoFit。Fixture 和下载的 binary 都保存在仓库外，并在报告记录完成
后删除。

### 公开 API 清单

官方 9.4 时代 Presentation API 暴露以下相关方法：

- `ApiShape.GetPosX/GetPosY/GetWidth/GetHeight` 返回 drawing 外框；
- `ApiShape.GetContent` 返回文字内容；
- `ApiDocumentContent.GetText` 以及 paragraph/run 结构；
- 字体和 paragraph property getter；
- shape 位置、尺寸、padding 和文字对齐修改方法。

它没有记录 text-bounds、used-height、line-layout 或 overflow 方法。对实时文件执行
`ApiShape.ToJSON()` 会返回外部 transform、text body property、paragraph、run 和声明字号，但
不包含渲染后文字范围或 overflow 值。

### 可执行结果

Native Python SDK 以状态 `0` 打开 fixture，并返回预期 shape 文字和外框几何。直接执行文字
bounds 调用时明确失败：

```text
TypeError: ...GetTextBounds is not a function
execute False
```

AutoFit 对比结果：

| Shape | 内容类型 | `RecalculateAutoFit()` | Width | Height |
|---|---|---:|---:|---:|
| `Fits` | 可容纳的短文字 | `true` | 2011680 EMU | 502920 EMU |
| `Overflows` | 超长中英文混合文字 | `true` | 2011680 EMU | 502920 EMU |

启用 normal AutoFit 后，两个 shape 的 `ToJSON()` 在重新计算前后都返回相同状态：

```json
{
  "bodyPr": {"textFit": {"type": "normAutoFit"}},
  "run": {"font": "Carlito", "sz": 2400}
}
```

`2400` 表示以百分之一 point 记录的声明 24 pt 字号。没有返回 effective font size、font scale、
text width、text height、line count、clipping 或 overflow 字段。Boolean 结果只表示接受了重新计算，
不能区分 fit 与 overflow。

通过渲染 shape 并从像素推断 bounds 会引入新的测量启发式，不能满足设计要求的合格文字排版
结果，因此没有用它绕过失败 gate。

### 独立 CLI 保存结果

为排除 Python binding 是问题原因，使用上游 executable 和其 native script 格式打开并保存相同
源文件：

```text
./docbuilder cli-save.docbuilder
docbuilder: license is invalid!
exit: 0
```

Python SDK 和 CLI 输出都添加了以下 slide 文字：

```xml
<a:t>Unregistered Version</a:t>
```

这与上游安装文档一致：免费版本会给所有生成文档添加水印，移除水印需要商业许可证。免费
AGPL license 是源码许可证，不是 DocumentBuilder registration key。

保存操作还改变了源 shape identity：

| Shape | 源 `p:cNvPr` ID | CLI 输出 `p:cNvPr` ID |
|---|---:|---:|
| `FitsAutoFit` | 2 | 1752396050 |
| `OverflowsAutoFit` | 3 | 989754690 |

CLI 保存新增 notes master、notes slide、relationship 和 theme part，同时删除源 thumbnail 和
printer-settings part。这些变化没有 SparkClaw mutation plan，超出设计的 package allowlist。
其中插入水印本身已经足以判定保真失败。

## Gate 矩阵

| 强制 gate | 结果 | 证据或原因 |
|---|---|---|
| 保存/重开后的稳定身份 | **FAIL** | 直接 CLI 保存把源 non-visual shape ID 2 和 3 改成不同的生成值 |
| 真实文字边界或有效 overflow | **FAIL** | 无公开结果；显式 bounds 调用不可用；AutoFit 结果不能区分 fit 与 overflow |
| 几何修改与取整 | NOT RUN | 强制失败后停止 |
| 富文本保真 | NOT RUN | 强制失败后停止 |
| OOXML package 保真 | **FAIL** | 免费 CLI/SDK 保存插入水印并改变无关 package part |
| 渲染确定性 | NOT RUN | 强制失败后停止 |
| Microsoft PowerPoint 查看器兼容性 | NOT RUN | 强制失败后停止 |
| 完整 Linux ARM64 样本 | NOT RUN | Artifact 身份/启动通过，但完整 gate 要求所有测试通过 |
| 取消和孤儿进程清理 | NOT RUN | 强制失败后停止 |
| 100 次可重复性 | NOT RUN | 强制失败后停止 |

Identity 和保真检查是初次停止后应 owner 要求进行的有限根因探针，不表示恢复完整 corpus。
`NOT RUN` 不表示其他能力失败，只记录决定性强制失败后的必要提前停止。

## 决策与仓库影响

本提案禁止继续以下工作：

- 向 SparkClaw 添加 DocumentBuilder worker 或 binary；
- 向生产依赖 manifest 添加 OR-Tools；
- 修改 `pptx.update_slide` 或 `pptx.update_deck` 执行；
- 增加排版引擎 config、schema、Store record 或 feature flag；
- 为该引擎修改 Fast context、提示词、重试或 semantic repair；
- 改接 LibreOffice、Aspose、其他 renderer，或像素/字符测量启发式；
- 继续 Phase 1 到 Phase 5。

SparkClaw 保持当前 PPTX 实现和行为。本次执行只修改双语设计状态，并增加这份双语资格记录。
重新考虑其他引擎或未来 DocumentBuilder release，必须建立新的显式设计决策，不能继续执行本次
已否决提案。

## 参考资料

- [已否决的确定性排版设计](../../docs/pptx-deterministic-layout-runtime-design.md)
- [DocumentBuilder v9.4.0 release](https://github.com/ONLYOFFICE/DocumentBuilder/releases/tag/v9.4.0)
- [DocumentBuilder 安装及免费版水印说明](https://api.onlyoffice.com/docs/document-builder/get-started/installing/)
- [ApiShape 方法清单](https://api.onlyoffice.com/docs/office-api/usage-api/presentation-api/ApiShape/)
- [ApiDocumentContent 方法清单](https://api.onlyoffice.com/docs/office-api/usage-api/presentation-api/ApiDocumentContent/)
