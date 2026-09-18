# SparkClaw 工作台对齐

> 语言：[English](../design-qa-workbench.md) | 简体中文

最终结果：通过

## 范围与视觉证据

- 来源：`/home/infinimesh/Documents/xwechat_files/wxid_e7wrktvq5xkj22_a22d/msg/file/2026-09/SparkClaw 工作台（独立版）.html`。
- 预览：`http://127.0.0.1:18890/`，连接隔离 Worktree 的默认 Mock-model Gateway；这不是生产部署。
- 证据目录：`/home/infinimesh/.codex/visualizations/2026/09/17/01a0ad7d-598c-70a0-a16a-ad98b8c6e379/`。
- 来源截图：证据目录中的 `reference-workbench.png`。
- 最终实现截图：证据目录中的 `implemented-workbench-final.png`。
- 移动端截图：证据目录中的 `mobile-workbench.png`、`mobile-conversation.png`。
- 桌面对比：两张图均为 1280 × 900 像素、1280 × 900 CSS Viewport、1× Density、中文、浅色主题和空任务首页；图片在同一次 Browser Tool 结果中一起输出并直接比较，较早期 Viewport 不一致的截图不纳入对比。
- 移动端验证：390 × 844 CSS 像素、1× Density；完成后已重置 Native Viewport Override。

## 必须对齐的界面

- 字体：沿用来源的系统字体栈，首页标题为桌面 40px／移动端 32px、550 Weight、1.42 Line Height 和两行换行，辅助文字为 12–14px；层级与换行匹配来源，既有技术面板继续用等宽字体呈现标识符和结构化诊断。
- 布局：244px 侧栏、74px 桌面 Header、居中的 870px 首页内容、16px Composer 圆角、四列工作流指引、页面导航和可折叠任务检查器；移动端导航以 Overlay 显示并在选择后关闭，工作流改为两列。来源与实现的 Hero、Composer 和指引位置仅相差数像素。
- 颜色：白色／#f6f7f9 Surface、#4285f4 Action、#1f5fbf Emphasis、#e8e9ec Border；既有面板原绿色强调色已全部迁移，错误与通知文字也已适配浅色背景。
- 素材：直接复用来源中嵌入的 PNG Wordmark，不重绘、不改色；保留既有 Lucide 线性图标，与来源的 Stroke Icon 风格一致；未生成替代品牌素材。
- 内容：保留来源标题、介绍、建议、工作流步骤和页面名称，并提供英文对应文案；任务列表、审批数量、Connector 可用状态和模型模式继续来自现有 API，而非样例数据。
- 聚焦对比：桌面截图在 1× 下可读，并在同一组图片中直接检查 Logo、侧栏、标题、Composer 和流程条区域，因此无需另做放大裁剪。

## 发现与迭代

1. 首次离线截图暴露浅色主题上的旧错误文案对比度过低（P2）；已把浅红／浅蓝 Banner 文字替换为深色语义色，并保留真实 Offline/Error 状态而非隐藏。
2. 首次 390px 截图暴露 Topbar Action 继承 `width: 100%`，导致标题与检查器控件被隐藏（P1）；已改为内容宽度，窄屏减少 Destination Picker 细节，并移除旧 Message List 高度上限。修复后的 `mobile-conversation.png` 在 Viewport 内显示标题、邮件、通知、目标与检查器控件。
3. Search Dialog 最初把焦点放在关闭按钮上（P2）；现于 `showModal()` 后显式聚焦搜索框。重新检查 Ctrl+K：无障碍焦点位于搜索框，过滤与选择结果可打开对应真实任务；Native Dialog 提供焦点约束与 Escape 处理。
4. 选择空任务时可能恢复此前打开的检查器（P2）；任务选择现会关闭检查器，最终首页截图具有目标中的全宽 Composer 与指引。
5. 最终同尺寸桌面对比在本次生产前端适配范围内没有剩余可操作的 P0/P1/P2 视觉问题。

## 有意保留的生产适配

- 既有上传、工作区文档选择器、语音、邮件、通知与投递目标控件保持可用。原型中的模拟思考／搜索开关不会作为伪设置展示；模型设置会打开真实设置页。
- 受支持的消息连接为 Telegram、微信和既有浏览器邮件链路；不把原型中模拟的飞书连接宣传为可用能力。
- 新建计划和记忆会通过既有 Agent Workflow 创建任务草稿；已有计划编辑／删除与记忆查看／编辑／删除／导出 API 保持不变；未引入仅浏览器存在的伪记录或新后端契约。
- 设置保留真实账户、连接、Agent 和系统配置界面；检查器保留 Timeline、Approvals、Memory、Trace、Status 与 Settings，不丢弃既有诊断能力。
- 启动时保留既有 Session 选择；空 Session／新 Session 显示参考首页设计，使用真实历史与空状态替代样例投资／研究任务。

## 验证

- 浏览器：任务建议可填入草稿；消息提交能到达隔离 Mock Gateway 并渲染响应；任务检查器可开关；搜索可过滤并打开真实任务；桌面 Settings/Connections/Memory/Approvals 正常渲染；移动端导航可开关；新建计划入口会以计划提示词创建新草稿。
- Console：最终实现标签页没有 Error-level Entry。
- 前端生产构建、708 个双语 Key 的使用／对等检查、41 个文件中的 146 项既有测试及 `git diff --check` 均通过。
- 本次视觉验收未执行真实外部模型推理、麦克风访问、账户绑定、生产计划执行或远端部署。

## 后续润色

- P3：来源与实现的 Scrollbar Metric 使居中首页内容偏移约 4px；控件职责与生产 Runtime 内容构成其余 Toolbar／History 差异。
- 构建报告 >500kB Chunk 提示；原始附件中嵌入的 Wordmark 增加了 Bundle 体积，但不构成构建失败。
