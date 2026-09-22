# 已退役的浏览器表面路线与隔离资格验证

> 语言：简体中文 | [English](../../../tools/browser-surface-host/README.md)

> 状态：2026-09-21 从产品架构退役。本目录现仅为历史 X11 Host 边界保留一套范围
> 严格、可丢弃的 Electron 锚点资格测试；它不是浏览器运行时、回退路径或 Electron
> 发布门槛。

Electron 自带 Chromium 与原生 `WebContentsView` 仍是唯一目标浏览器架构。本目录不得
连接正在运行的 Browser Bridge、现有 Chromium 进程、生产浏览器 Profile 或用户活动桌面。

## 验证范围

测试专用 Electron 夹具在 runner 自建的 Xvfb Display 上创建一个 640×480 无边框锚点，
只加载 `data:` URL，使用随机身份标记，并把全部 Electron 状态写入每轮临时目录。
浏览器表面由本地合成 X11 子窗口代替；不会加载扩展或 Browser Bridge。

自动检查只证明这个历史 Host 能够：

- 精确接受 Electron 锚点的 PID、可执行文件、UID、标记和角色，并拒绝不匹配身份；
- 挂接时保留锚点焦点，把任务子窗口取得的焦点重定向出去；
- 把越出锚点的个人表面及其输入裁剪在锚点范围内；
- 在正常 detach 和 Host `SIGKILL` 后恢复或保全子窗口；
- Electron 锚点被强杀后安全退出且不杀死子窗口。

Runner 自行启动禁用 TCP 的 Xvfb，登记其 PID 和 Display，并拒绝使用继承的活动 Display。
Electron 与合成套件都会验证登记 PID 确实是存活的 Xvfb；仅设置
`SPARKCLAW_SURFACE_TEST_ISOLATED=1` 已不足以放行。

安装并运行可丢弃验证：

```sh
npm ci --prefix tools/browser-surface-host/test/electron-anchor
make -C tools/browser-surface-host test
make -C tools/browser-surface-host test-electron-anchor
```

这些测试不能验收真实窗口管理器／合成器、弹窗或对话框、GPU、生产 Chromium、
Browser Bridge，也不能代替已确定 Electron Adapter／WebContentsView 方案的验收。

## 昨天活动桌面为什么故障

此前测试直接在用户活动 `DISPLAY=:1` 上反复执行 X11 reparent、unmap、焦点和堆叠操作。
这些运行期间，系统日志反复出现已 disposed 的 `MetaWindowActorX11`、stage／allocation 错误及
Clutter 断言；在 `2026-09-21T10:10:40.586013+08:00` 明确记录
`GNOME Shell crashed with signal 11`。

因此直接故障是活动 Display 的 X11 窗口操纵路径触发 Mutter／GNOME Shell 合成器崩溃，
不是编译错误。Xorg 日志之后以 `Server terminated successfully (0)` 结束；同一时段内核
日志没有 NVIDIA Xid、GPU reset 或 OOM 证据。日志没有分离出具体是哪一次
reparent／unmap／焦点／堆叠操作暴露缺陷，也没有指出 Mutter 内部具体哪一行出错。
之后的测试修订还曾直接操作窗口管理器装饰 Frame，证明另有不安全路径；但该修订发生在
上述崩溃记录之后，不把它说成这次崩溃的触发点。因此仍不能扩大为“所有 GPU／显示风险都已排除”。

Host 使用 root-level override-redirect Frame、精确窗口身份、任务 InputOnly Shield、焦点
重定向和 X11 save-set。保留它只是为了安全复现和守住这条历史边界；生产实施继续以
[桌面浏览器设计](../../docs/desktop-client-embedded-browser-design.md)为准。
