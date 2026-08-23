# PPTX 超长文本 Phase 0 资格测试

> 语言：[English](../../benchmarks/pptx-overlength-phase0-qualification.md) | 简体中文

| 字段 | 结果 |
|---|---|
| 日期 | 2026-08-14 |
| 决策 | **NO-GO** |
| 提案 | Gotenberg、LibreOffice、PDF.js 几何和确定性 raster 可见性证据 |
| 目标环境 | Linux `aarch64` |
| 样本 digest | `75aa95d309b53d6300fa7cf5b2df23c3d7d638471f2b3efd6365114516c025bd` |
| 机器结果 | [`pptx-overlength-phase0-result.json`](../../benchmarks/pptx-overlength-phase0-result.json) |
| 生产影响 | 无；Gateway 和 ToolHub 保持不变 |

> 保留说明：资格测试 harness 保留用于候选策略变化时重跑 Phase 0；若提案被撤回，则与设计文档一并删除。

## 决策

当前提案不进入生产接入。固定的 ARM64 渲染栈通过了合成完整性、可见性、确定性、保真、digest、
outage 和取消探针，但没有通过全部强制门禁：

1. 未缩减的请求边界允许 64 个 shape、每个 16 个候选，即组合渲染前已有 1,024 次候选转换。
   实测最快引擎的 median 是每次 0.1703 秒，对应 174.3872 秒的下限。在尚未计入 proof render、
   组合 render、排队、修改、PDF.js 检查和最终验证前，这已经超过 90 秒准备期限。
2. 本次没有提供执行不可逆私密文字替换的 owner 文稿，因此 owner 样本没有运行。
3. Microsoft PowerPoint 参考查看器兼容性没有运行；本报告不声称 PowerPoint 中没有裁切、修复
   提示或显示差异。

转换成本失败本身已经足以否决当前提案。更小的可执行候选策略、通过资格测试的确定性剪枝或更小
的准入范围都需要重新运行 Phase 0；在授权任何生产代码前，PowerPoint 和 owner 样本也必须通过。

## 已实现 Harness

仅用于资格测试的实现位于 `benchmarks/pptx-overlength-phase0/`，通过
`scripts/qualify-pptx-overlength.sh` 运行。它不是根 workspace 依赖，也没有注册 tool、配置字段、
feature flag、worker 或运行时路径。

Harness 包含：

- 自动生成的 16:9 和 4:3 英文、简体中文、中英混排、项目符号、soft break、裁切、重复归属、
  不透明/局部/透明遮挡、同色隐藏、缺失字体和旋转目标 fixture；
- 使用隔离 LibreOffice profile、字节限制、超时和进程组清理的 native LibreOffice 与私有
  loopback Gotenberg 转换；
- PDF.js 5.4.394 归一化文字、transform 几何、字体和 operator-list digest；
- 对比候选、移除目标文字和目标文字置顶三种 render 的反事实 raster 检查，因此能拒绝
  “已提取但不可见”的文字；
- 不受 ZIP 顺序和时间戳影响的 canonical OOXML package digest；
- 确定性的语义组、部分成功和 `no_safe_change` 资格合同；以及
- 不包含文档文字、PDF 字节和 job-local 路径的结构化结果。

## 固定 Runtime

| 组件 | 资格测试 artifact |
|---|---|
| Host 架构 | Native Linux `aarch64` |
| Host LibreOffice | `24.2.7.2` |
| Gotenberg | `8.15.3`，ARM64 manifest `sha256:664f1851e03fc230f194c114efa3ad7694e29951ac9ba04991c7b6e47bc243a8` |
| Gotenberg LibreOffice | `24.8.4.2` |
| PDF.js | `pdfjs-dist` `5.4.394`，精确 npm lockfile |
| PPTX writer | 通过 `python-pptx` 1.0.2 使用现有 `pptx_slide.py` |
| 资格字体 | Liberation Sans 与 Noto Sans CJK SC；声明但不存在的字体 fail closed |

Gotenberg 容器使用随机 loopback 端口、唯一名称、2 CPU、4 GiB 内存上限、PID 上限和只读 host
Noto 字体 mount；它没有修改或重启任何现有 SparkClaw 容器。

## 可执行结果

两套引擎对全部 17 个合成 case 都产生预期决策。可容纳的英文、中文、中英混排、项目符号、
soft break 和 4:3 内容被接受；故意构造的英文/中文/混排裁切、4:3 裁切、重复字符串、不透明与
局部遮挡、透明覆盖、同色隐藏、缺失字体和不支持的旋转目标均被拒绝。

| 引擎 | 每个 fixture deck 重复次数 | 归一化文字/几何 | Raster | Median | p95 | Worst |
|---|---:|---|---|---:|---:|---:|
| Host LibreOffice | 100 | 稳定 | 稳定 | 1.1924 s | 1.3027 s | 1.3471 s |
| Gotenberg | 100 | 稳定 | 稳定 | 0.1703 s | 0.8326 s | 0.9046 s |

每套引擎共执行 204 次转换：两个 deck 的六次初始候选/证据 render，加 198 次重复 render。每套
引擎的 100 个组合归一化 digest 和 100 个组合 raster digest 均完全一致。

现有 writer 重放五次，raw package diff 只有 `ppt/slides/slide1.xml`；raw PPTX SHA-256 与
canonical package digest 均可重复。Canonical 输出 digest 为
`7c9bc214a83dd392a0c50850777622e6350a13ef37c7b864f0a72471e7d0e764`。

不可连接的 renderer 返回资格测试等价的 `runtime_unavailable`，没有留下 PDF。故意挂起的父子
进程树在 0.0032 秒内停止，没有存活子进程，低于两秒清理边界。

## Gate 矩阵

| 强制门禁 | 结果 | 证据 |
|---|---|---|
| Native 部署 | PASS | 固定 Gotenberg 和两套 LibreOffice 在 ARM64 原生运行 |
| 文字完整性 | PASS | 可容纳和故意裁切的英文/中文/混排 case 都符合预期 |
| 归属与可见性 | PASS | 重复、遮挡、透明和同色 case 均 fail closed |
| 几何 | PASS | 有贡献的 PDF.js 文字 transform 位于通过资格测试的目标区域内 |
| 字体确定性 | PASS | 已清点资格字体；缺失字体 case 被拒绝 |
| Render 可重复性 | PASS | 每套引擎 100 个归一化与 raster digest 稳定 |
| Owner 样本 | **未运行** | 未提供 owner 私密文字样本 |
| PowerPoint 兼容性 | **未运行** | 未提供参考查看器证据 |
| Writer 保真 | PASS | 五次写入都只改变声明的 slide XML part |
| 部分应用 | PASS | 一个独立可行组在另一个组被拒绝时仍保留 |
| 语义原子性 | PASS | 一个不可行成员拒绝完整组；非法 metadata 退化为 operation-wide 原子性 |
| No-safe-change | PASS | 所有更新都不可行时不生成 artifact |
| Pipeline outcome | PASS | 资格合同区分 requested/effective plan 和零修改结果 |
| Renderer outage | PASS | 类型化 unavailable 结果，无未检查输出 |
| 取消 | PASS | 完整进程树在两秒内停止 |
| 转换成本 | **FAIL** | 1,024 次转换的 median 下限 174.3872 s，超过 90 s |
| Digest 确定性 | PASS | Canonical package 与归一化 render digest 均可重复 |
| 保密 | PASS | 结构化结果不包含 fixture 文字、PDF 字节或 job 路径 |

## 仓库影响

该结果继续禁止：

- 接入 Gateway 或 ToolHub adaptation worker；
- 向生产 setup、Compose、doctor、配置或部署添加 Gotenberg、PDF.js、renderer、字体或 policy；
- 修改 `pptx.update_slide`、`pptx.update_deck`、审批、prepared artifact、Workflow outcome 或
  semantic repair；以及
- 声称准入模式兼容 Microsoft PowerPoint 或可以代表 owner 文稿。

当前基于启发式的 PPTX 行为保持不变。仓库影响仅限双语设计/报告、独立资格 harness、固定的
benchmark 依赖和结构化机器结果。

## 复现

在目标 ARM64 host 的仓库根目录运行：

```bash
scripts/qualify-pptx-overlength.sh
```

脚本使用隔离容器名和随机 loopback 端口。只有在 owner 样本或 PowerPoint 后续测试需要保留生成的
fixture 与诊断 artifact 时，才设置 `PPTX_PHASE0_KEEP_WORK=1`。

## 参考资料

- [PPTX 超长文本韧性适配设计](../docs/pptx-overlength-resilience-design.md)
- [此前的 DocumentBuilder Phase 0 资格测试](pptx-documentbuilder-phase0-qualification.md)
