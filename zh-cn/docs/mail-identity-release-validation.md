# 邮箱身份与恢复：发布验收

[English](../../docs/mail-identity-release-validation.md) · [App-CLI 实施验收](app-cli-implementation-validation.md)

更新日期：2026-10-21。**按用户要求停止。`.31`已安装到Mac，work2健康；Mac原邮箱检查中三家均ready/healthy。`.30`六方向均送达，四封自动捕获，两封进入Gmail垃圾邮件被排除。新增`.31` Outlook回复派发前失败，未发出；Gmail普通收件箱自动同步和回复链路未验收。** 普通启动及最终保全已完成，源码推送和正式发布尚未完成。

## 配套版本

| 组件 | 冻结值／状态 |
| --- | --- |
| 最终 SparkClaw 发布源码 | `14d1c8ac5065c883fb27ec199ce91141cc9cb544` |
| 已测试 Gateway/runtime 源码 | `f219bbb6c628301094bf8c2a871ed46b54ef72c9` |
| 已测试 Controller fixture 修正 | `a1cde4fbe290ec24e85b6e1777c6d8f8808710b3`，合入为 `3b5fae80`；仅测试修改 |
| AppCLI 发布版 | `0.3.0-sparkclaw.31` |
| AppCLI 源码 | `a91396eb68845b2f85f6786f2677e35858d95de6` |
| Runtime digest | `697d649969a9c5b405cae6be933661c4f15995d64f8d4c5ba6f8373a498db205` |
| Browser Bridge | `1.0.29`；闭包 `152141b6cf731ea05232eee510a9ea6459406774b75ea4170a004e1828332596` |
| Linux 托管扩展 | Tampermonkey 上游 `5.5.0`，产品版 `5.5.0.14`／修订 `14`；第二次启动通过原生导入、实际安装门禁通过；work2 于 UTC 10:52:03.839267 恢复健康 |
| Runtime / Host / 账本协议 | `3.0` / `1.0` / `2`，保持不变 |
| Mac `.31` 安装包 | `14d1c8ac` 构建的 ARM64 DMG/ZIP；277 项一致性通过；UTC 10:37:04.921526 已安装 |

产物文件名与完整摘要固定在[发布清单](../../configs/app-cli-release.json)中。Reader 源码摘要与稳定 UUID 固定在[组件清单](../../configs/browser-components.json)中。仅比较 Reader 版本字符串不能证明实际安装字节已经更新。

## 设计边界修复

1. **账号身份与投递地址。** Outlook 已登录账号的内部标识可以作为 Reader 和存储身份，但不一定是可投递的收件地址。实现保留 `account_address`、既有 mailbox ID、抓取命名空间、原文和草稿引用，以成对可选的 `delivery_address`、`account_aliases` 单独承载同账号原生证据。Outlook 使用当前已登录账号的原生默认发件地址与别名选择器，并校验属于同一账号的代理地址集合；不能使用任意邮件 From 头证明归属。证据缺失或冲突只撤下通信地址投影，不阻断有效抓取页入库。回复全部排除已验证的本机别名，包括大小写变体。暂停收信保留事务内最新地址证据，不用先前读取的旧副本覆盖它。

   Gmail 还有独立的本地化缺陷：Reader 要求英文账号按钮标签，实机已登录页面却使用中文。`.30` 仅从唯一可见的页头账号按钮提取一个一致的地址，且要求按钮具有精确的 HTTPS Google Accounts `SignOutOptions` 目标。Reader 与托管发信身份守卫共享此提取逻辑；邮件正文、账号选择列表和全局地址回退均不能作为身份依据。原有预期账号与切号检查保留。登录就绪与可选账号提示分开判断。

2. **接收设置未知结果与业务提交证据。** 登录检查 ready 不会自动创建接收邮箱；原来已经尝试但回执 unknown 的接收设置也缺少安全产生新前台意图的闭合路径。新增 `mail.intake.reconcile`，将原操作、scope、digest 与持久业务结果绑定。已提交时恢复原结果；能够证明未提交时，通过同一事务的 provider version CAS 围栏闭合为 `not_applied`，阻止旧请求迟到提交。回执 GET 仍然只读。版本冲突或证据不足时继续保持 unknown。新的明确用户操作使用当前 provider version，即使尚无 mailbox 也如此。这一机制不会重发未知邮件或重放其他无关写操作。现有证据未确定历史接收意图最初因何中断而变成 unknown。

3. **QQ 写入派发与已读证明。** 派发成功并不等于远端已读。原确认路径可能依赖旧缓存状态或不适用的列表请求。新路径执行有界的新鲜精确查询，验证账号、邮件身份、收信时间、文件夹及响应完整性，之后才报告已读状态。已经释放的 unknown 或 pending 写入只能进行只读确认，不再次点击或派发。跨账号、歧义、加锁、溢出和畸形结果不能确认成功。确认失败不会撤销已经持久保存的原文。

4. **业务完成与资源清理。** `.31` 在 schema 校验及当时授权、取消、截止检查通过后保留业务结果，再执行一次有界 Host release。清理失败记录 `host_cleanup_failed`，保留 Host 围栏和退役状态，但不把已经证明的发送改成未知。终态发布和 ACK 仍在清理尝试结束后，避免撤销授权与 release 竞争。结果无效或缺失、验证前取消/超时、持久终态前进程崩溃仍遵守原失败/未知规则。既有 uncertain 发信仅查询或对账原操作，绝不重发。原生 sent journal 存在与 Executor 已经接受结果是两种证据。

5. **受控标签页关闭与事件顺序。** Bridge `1.0.29` 修复独立离线复现的顺序：`chrome.tabs.remove` Promise 先完成，旧实现提前遗忘归属，后到的 `onRemoved`/`onDetach` 被过滤，Playwright 因缺少 detach 通知持续等待。成功删除后，仅对本次精确授权 ID 经过既有 ownership 检查的事件路径发出关闭通知，再遗忘归属；先到和后到事件去重，失败删除、已交回或外部标签页不能伪造关闭。真实 pinned Playwright consumer 用例证实缺陷和修复。生产未记录原生事件先后，因此不能断言两次 Outlook 实机超时均由此顺序导致；资源清理健康与业务送达仍分别说明。

6. **确定性托管组件 stage。** `.31` 从规范化公开 manifest、已校验的脚本及依赖包、原生导入选项、已停用的受管 UUID 和版本化生成算法标识派生 generation，排除时间、stage/cache 路径及私人 profile 状态。同一固定官方 CRX 和四份未变脚本重复 stage，修复前产生不同 worker 和策略；修复后全部 88 个暂存文件字节一致。重装复用原生已消费 generation 的键；显式修复/重新导入须使用不同的产品版本，通常随 patch revision 递增。既有门禁仍在任何写入前拒绝同版本更换 worker 字节，并允许明确执行的另一版本配套回退。TM `.14` 已整合，隔离 stage/安装测试通过，第二次受控启动已通过实机原生导入；随后 work2 实际安装门禁及健康切换通过。不会清除原生 consumed 记录或 profile 存储。

原菜单、页面结构、布局与控件保持。已验证的投递地址仅替换原发件选择、邮箱筛选、接收设置和详情中的文字位置；操作仍使用稳定 mailbox ID 和 version。未新增常驻技术状态面板。

## 部署门禁与首次导入边界

Runtime 安装、Controller 依赖、Linux 托管 Reader 安装/导入、Mac 生成 Reader 是四个独立部署面。`.29` 首次切换更新了 runtime 和依赖，却留下旧的系统 Reader 字节和策略。Mac 打包检查也曾在接受安装包前发现生成 Reader 文件过期。这些是部署不完整，不能用 socket 正常或版本字符串一致来证明新 Reader 可用。

Linux Reader 内容变化还会改变扩展生成的 service worker 文件名。安装器在修改系统前拒绝“托管 worker 已变化，但产品版本未变”的候选；明确执行的另一版本配套回退仍允许。`.30` 使用产品版 `5.5.0.13`；其 stage 含时钟生成的 provisioning 值，已复现相同输入重新 stage 产生不同 worker 身份。已整合的 `.31` 确定性 stage 修复消除这一漂移来源，不放松版本门禁。TM `.14` 的重复 stage 一致性、同产物隔离安装、原生 generation 键保留和明确变更版本后的重新导入检查均通过。这些产物/安装检查与下述实机原生导入分别取证，不能代表 work2 最终健康已经通过。

`.12` 首次启动观察必须保留因果边界：正式 `wait-profile` 失败，当前导入 hash 尚未消费，三家 Reader 源码摘要仍旧；但新 worker 已注册，原生脚本 `enabled=true`、`system=true`，并不存在已经证实名为“unknown”的原生状态。第二次受控启动后，当前 hash 被消费，四个托管脚本摘要全部一致。第一次失败没有捕获异常，因此不能宣称某个初始化竞态或单独版本递增就是它的根因或修复。历史邮件任务的 `unknown`/`uncertain` 是另一条效果安全边界。

保留的激活脚本始终关闭 Gateway 和 Executor 准入，要求无活动任务、待释放任务或清理围栏，冷备既有 profile 和组件，并通过正式安装器安装固定候选。仅 `wait-profile` 失败可以在再次证明准入静默、历史 unknown 完整行摘要未变之后，多执行一次有序 Controller/Browser 重启；激活最多允许两次 Browser 启动。安装、配置、账本、Preferences 或最终门禁失败均不自动重试。失败时保持准入关闭并保留诊断，不清原生存储、不删除围栏、不回滚运行中的 profile。已禁用仅回退 runtime 的自动回滚；恢复必须保持 runtime、Bridge 和托管组件配套，并保留当前业务及执行账本。流程请求原生会话恢复，但未检查前不能声称所有未保存网页状态都保留。

最终 `.31` 激活于 UTC 10:45:42.250894 开始。第一次原生导入检查因当前 hash 缺失而失败（10:45:46.079244–10:46:47.129516）；获准的第二次启动于 **UTC 10:46:56.044456** 通过原生导入。此现象再次出现，首次导入失败的根因仍未确定；不能用确定性 stage 修复声称初始化问题已经解决。已安装组件闭包及原生 Bridge `1.0.29` 握手此前已通过，但新增加的私有最终门禁错误要求解压扩展在 `Preferences` 内持久化 `manifest.version`。冷备的 `.30` 和当前 `.31` 注册均没有此字段。门禁于 UTC 10:46:56.842233 停止，准入持续关闭，**本次切换基线的全部 81 条 unknown 完整行保持不变**。

只读诊断将该门禁修正为校验实际持久化的精确路径、location `8` 和启用注册状态，组件版本由已安装 manifest、闭包摘要及原生握手证明。23 处 Preferences 变化均核对为浏览器维护记录，Secure Preferences 未变。修正后的私有激活、单次恢复和父执行器测试共 **17 + 5 + 18 = 40** 项通过。经审阅的单次组件恢复于 UTC 10:50:59.844167 完成，复用已导入组件，不重新安装、不进行第三次 Browser 启动；Controller 启动用于证明实际加载的握手，临时 runtime override 已移除。父切换流程随后验证实际 Bridge `1.0.29`、原生 Reader 导入和配套 runtime 门禁，再恢复准入，并于 UTC 10:52:03.839267 达到健康状态。这是部署门禁修正，不是新的产品/runtime 补丁。

激活中断和门禁修正证据保存在 `activation-evidence/activation.json`、`script-review/resume-review-result.json` 及激活/恢复/父流程测试日志中，保留第一次失败和准入停止区间，不将本次部署改写成一次启动成功。

恢复准入前，必须通过 runtime 文件校验、系统 receipt/policy 校验、当前 provisioning hash 的原生消费证明、已启用 Reader 源码/依赖精确摘要，以及只读安装版资格检查。之后还需由新的受控任务页证明 Reader 能力可用，结果实际进入 Gateway 邮箱投影。浏览器重启本身不等于成功。

## 已验证测试与构建范围

| 检查 | 有证据支持的结果 |
| --- | --- |
| AppCLI `.31` runtime | 457 通过、2 项显式跳过、0 失败 |
| AppCLI Python | 86 通过 |
| 配套发布资格检查 | 全新安装、精确原生 runtime、篡改/混版/漂移/运行中切换拒绝、整套回退、持久状态保留与 epoch 单调均通过 |
| Linux Gateway `f219bbb6` | build、vet 和全部 75 个测试包通过，`go test -count=1`；最终 `14d1c8ac` 的 Gateway 构建输入完全相同 |
| Linux Controller `f219bbb6` 加仅测试修改 `a1cde4fb` | 306 通过、109 项既有平台/显式资格跳过、0 失败；离线全新安装 10 个锁定依赖，`.31` runtime 82 项文件校验、postinstall patch 与生成契约检查通过 |
| Browser Bridge | 70 项通过；真实 pinned Playwright consumer 的两种事件顺序用例在因果复现后修复通过 |
| Bridge 发布一致性 | Controller 原生协议 5 项、artifact/package 15 项通过，版本和源码闭包一致 |
| 接收/地址存储及并发 | 先前 memory/file/PostgreSQL 故障用例通过，包括迟到提交排除、scope 隔离、负向围栏和原邮件/原文/草稿身份保留；`.31` 未改这些实现 |
| WebChat | 先前整合 432 项通过且最终源码未变；最终 `.31` 包 TypeScript/Vite 构建通过 |
| Desktop / Mac `.31` | 最终源码 507 项通过、0 跳过/失败；新 ARM64 包的 66 份桌面源码、8 份 Bridge 源码、5 份 UI 文件、helper 和全部 277 个 manifest 条目一致；Mac 原邮箱检查通过；最终普通启动及保全通过 |
| 确定性 stage / TM `.14` | 已整合；使用固定官方 CRX 的组件/stage/上游摘要/安装资格测试 40 项通过、0 跳过；两次实际四脚本 stage 的 88 个文件完全一致；第二次启动通过实机导入；实际安装门禁及恢复后的 work2 健康通过 |
| 私有激活/恢复门禁 | 激活 17 项、恢复 5 项、父流程 18 项通过；实际 Preferences 只读证明覆盖 23 处维护记录变化；单次恢复及 work2 健康切换通过 |

首次全新 `.31` Linux Controller 保留 299 通过、109 跳过、4 个报告失败：两个旧叶子断言（另计父测试）仍要求“结果已验证、清理失败”保持 uncertain；另有一个短超时 A/B 用例与 Go 同时占用 CPU。测试修正用“原生 journal 已写、验证前丢失返回”保留真实未知路径；另行证明结果已验证后的清理失败、重启和 journal 后续丢失仍保留 completed，且仅一次发送效果、一次 release 和一条清理诊断。未修改的 A/B 文件单独 14 项全通过。随后不与 Go 并跑的完整 Controller 全部通过，首次失败日志保留。

Gateway 使用 Go `1.25.5`、`CGO_ENABLED=0 GOOS=linux GOARCH=amd64` 和 `-trimpath`。产物 44,036,276 字节，SHA-256 `92deb024f573b7d82088137f9b8a335c54e8bb76eba67ade1ee1ff90c88eceea`，与 `.30` 完全相同。`final-source-equivalence.json` 证明已验证的 `f219bbb6` 与最终 `14d1c8ac` 之间，Gateway 全部构建输入、WebChat 源码和 Controller 生产源码均未变；之后仅调整 staging/配置、文档及一份 Controller 测试。

最终 Mac 包从干净的 `14d1c8ac` 在 Node `26.2.0`、`darwin/arm64` 上于 UTC 10:35:40.355–10:36:06.089 构建，耗时 25.734 秒。包 allowlist 与源码/UI/helper 一致性均通过。DMG SHA-256 为 `9dfe6bb87d604476bfb8fa10de4349331f8b27d707d814aacd39bb670f24b6be`；ZIP SHA-256 为 `7be4215dd9195d7e8990adbb8665bf3afe6cfac409f4ddada2173f99c042f927`。包未使用 Developer ID 签名或公证。安装于 **UTC 10:37:04.921526／北京时间 18:37:04.921526** 完成，全部 277 个文件/链接一致，六个既有 profile 在启动前逐字节保持不变，数据备份校验通过。该安装结果不代表后续正常启动、连接、原界面或最终运行态保全已经验收。

私有证据位于 `20261021-mail-r31/`（`final-go/`、`controller-linux/result.json`、`evidence/{server-build,final-source-equivalence}.json`、`evidence/{desktop-final,mac-build}.log`、`mac-upgrade/evidence/{build-source,package-parity,installation}.json`、`staging/{baseline-reproducer,r14-reproducer}.json`、`staging/component-tests-r14.log`、`paired-qualification.log`）及 `.30`（`outlook-close/root-cause-and-fix.json`、`mac-upgrade/evidence/{six-mail-results,native-originals-four}.json`、`mac-upgrade/gmail-receipt/`）。`.29` 提供接收/地址故障测试和首次导入诊断。公开文档不能包含私有账号、profile、凭据、邮件原文或原始回执日志。

## `.31` 升级及剩余实机验收

已禁用仅回退 runtime 的自动回滚。任何经审阅的恢复都必须保持 runtime、Bridge 和托管组件配套，并保留当前业务和执行账本。既有配置、mailbox ID、原文、草稿、回执、登录/profile 和原界面均须保留。历史未知发信，包括更早的 Outlook 任务，不作为测试发送，绝不重发；接收负向证明和 QQ 只读确认均不放宽这一边界。

最终 `.31` work2 切换于 **UTC 10:44:51.615242–10:52:03.839267／北京时间 18:44:51.615242–18:52:03.839267** 完成，包含已保留的门禁中断和经审阅恢复。当前配套为源码 `14d1c8ac`、AppCLI `.31`、Bridge `1.0.29` 和 Tampermonkey `.14`。`evidence/server-cutover.json` 证明 **41 张业务表、2,051 个工作区文件及 70,935 份原有回执文件** 全部保留。`activation-evidence/activation-resume.json` 记录恢复时没有重装组件或重启 Browser，历史 unknown 完整行未变，临时 runtime override 已移除。23 处 Preferences 变化均核对为浏览器维护记录、窗口位置大小或当前扩展路径，选定的既有用户偏好保持不变。

切换后的首份快照为 **UTC 10:52:28.721921**：健康检查 HTTP 200，耗时 **0.917 毫秒**；无清理围栏，三家 watch 任务均活动，81 条基线 unknown 完整行未变，无新增未知任务，三份历史草稿记录完全一致。这是初始观察，不能代替长期稳定性测试。Mac 首次连接检查碰上维护期 HTTP 503，显示暂不可用；现有重试已清除此状态。随后已安装 Mac 的 ISCP 链路和原邮箱检查控件于 **UTC 10:53:03.252–10:53:37.558／北京时间 18:53:03.252–18:53:37.558** 对 Gmail、Outlook、QQ 均返回 HTTP 200、`ready` 和 `sync_health=healthy`，各耗时 7.875、11.236、15.104 秒。证据为 `mac-upgrade/evidence/final-mail-checks-recovered.private.json`。这证明连接/检查行为，不等于新增邮件捕获或回复发送通过。最终普通启动和保全证据见下文。

先前 `.30` work2 于 UTC 09:56:38.137490 完成切换，保留 41 张业务表、1,680 个工作区文件、70,560 份原回执及全部 80 条历史 unknown 行。Mac 于 UTC 09:51:34.153769 安装，277 个包条目及启动前六个 profile 完全一致。Gmail 通过原 UI 于 UTC 09:57:04.096–09:57:41.365 开启（HTTP 200；37.269 秒）。这些是上一版本事实，与上面的 `.31` 保全证据分别记录。

```json
{
  "release": "0.3.0-sparkclaw.31",
  "acceptance_status": "partial_stopped_at_user_request",
  "final_sparkclaw_source": "14d1c8ac5065c883fb27ec199ce91141cc9cb544",
  "work2_cutover_utc": "2026-10-21T10:52:03.839267Z",
  "work2_cutover_preservation": {
    "business_tables": 41,
    "workspace_files": 2051,
    "original_receipt_files": 70935
  },
  "work2_initial_snapshot_utc": "2026-10-21T10:52:28.721921Z",
  "baseline_unknown_rows_unchanged_at_initial_snapshot": 81,
  "mac_install_utc": "2026-10-21T10:37:04.921526Z",
  "mac_profiles_exact_before_launch": 6,
  "mac_normal_launch_utc": "2026-10-21T11:01:49.966932Z",
  "mac_debug_ports_closed": true,
  "final_package_parity_evidence": "mac-upgrade/evidence/package-parity.json",
  "pre_and_post_business_preservation_evidence": "mac-upgrade/evidence/preservation.json",
  "historical_unknown_rows_unchanged": 81,
  "original_ui_live_verification_evidence": "mac-upgrade/evidence/final-mail-checks-recovered.private.json",
  "three_provider_health_evidence": "mac-upgrade/evidence/final-mail-checks-recovered.private.json",
  "post_31_outlook_reply_result": {
    "state": "failed",
    "error_code": "email_send_not_dispatched",
    "effect": 0,
    "attempt": 0,
    "dispatch": null,
    "sent": false,
    "retried": false,
    "elapsed_seconds": 48.069
  },
  "post_31_gmail_normal_inbox_capture": "not_accepted",
  "final_observation_start_utc": "2026-10-21T10:52:28.721921Z",
  "final_observation_end_utc": "2026-10-21T11:01:51.616175Z",
  "source_publication_status": "not_pushed_at_user_stop"
}
```

## 最终收尾

Mac 于 **UTC 11:01:49.966932／北京时间 19:01:49.966932** 恢复普通启动：一个主进程，无 inspector 参数，调试端口关闭。原 Workbench、三个会话和邮件入口仍可见。最终保全验证277项安装资产、6个配置档、97张表、22条原消息、原会话/草稿、稳定身份配置和启动器；仅预期事件投影与3个可变传输journal更新。前述邮箱检查证明当时连接正常；最终重启后未再执行新的API检查。证据为 `mac-upgrade/evidence/{normal-launch,preservation}.json`。

最终work2快照于 **UTC 11:01:51.616175／北京时间 19:01:51.616175** 返回HTTP 200/ready，耗时 **1.268毫秒**；无清理围栏，81条基线unknown完整行未变，无新增unknown，三份历史草稿一致。有界观察不能证明长期稳定。临时诊断停止，私人句柄已释放，10个用户页面和3个常驻页保留。另有既存Mac browser-host重连/授权恢复问题未解决；成功的邮件检查走work2 ISCP链路。

两端维持当前安装，产品源码及制品保留本地；SparkClaw/AppCLI源码推送和正式发布尚未完成。本记录按用户要求停止时的部分验收结果收尾。

## `.30` 六方向真实结果

运行 `506172fb` 的服务商送达验证已经完成：六封均只发送一次，六封均在服务商侧收到；四封自动捕获并解析，两封 Gmail 目标邮件进入垃圾邮件，按现有 Reader 规则排除。因此**不能声称六方向自动同步全部通过**。未修改垃圾邮件策略、标签、已读状态或检查点来制造通过。Outlook → QQ 走原撰写/保存/重开/发送 UI；其他已安装客户端 native ISCP 调用不能写成 UI 点击测试。两次 Outlook unknown 均只对账原操作，重发为零。

| 方向 | 开始 UTC | 确定确认 UTC／耗时 | 收件证据 UTC | 原文／已读证明 |
| --- | --- | --- | --- | --- |
| QQ → Outlook | 09:58:40.713 | 10:00:09.453／88.740 秒 | 捕获 10:00:41.030439；解析 10:00:41.055297 | 原生原文 11,178 字节，hash 一致 |
| QQ → Gmail | 10:00:33.682 | 10:02:04.685／91.003 秒 | 服务商垃圾邮件 10:01:52.008；精确原生搜索；未自动捕获 | 仍未读，按策略排除 |
| Outlook → QQ | 10:05:42.688 | 原操作对账 10:09:07.128／204.440 秒 | 捕获 10:08:26.775885；解析 10:08:27.247516 | 原生原文 11,868 字节，hash 一致；QQ 已读确认 |
| Outlook → Gmail | 10:11:27.994 | 原操作对账 10:22:13.024／645.030 秒 | 服务商垃圾邮件 10:12:40.368；精确原生搜索；未自动捕获 | 仍未读，按策略排除 |
| Gmail → QQ | 10:15:08.287 | 10:16:33.190／84.903 秒 | 捕获 10:16:50.351232；解析 10:16:50.716321 | 原生原文 6,989 字节，hash 一致；QQ 已读确认 |
| Gmail → Outlook | 10:16:40.865 | 10:18:25.154／104.289 秒 | 捕获 10:20:15.120204；解析 10:20:15.745634 | 原生原文 14,512 字节，hash 一致 |

四份原文经已安装客户端的原生下载路径于 UTC 10:23:51.842–10:23:55.595 下载，全部摘要与存储 manifest 一致。Outlook 的持久原生 sent journal 早于清理失败：i2 为 **UTC 10:07:04.451**，i3 为 **UTC 10:12:39.804**。204.440 秒和 645.030 秒确认耗时包含人工等待和原请求对账，不能作为纯服务商发信性能。原生回执、服务商到达、Gateway 捕获/解析和前台最终确认是不同时间点；邮件 Date 头不是精确到达时间。

Gmail 使用独立受控只读页面搜索精确唯一测试标记，并包含垃圾邮件；没有打开或移动邮件。当前 Reader 排除 Spam/Trash/Drafts，且按收信时间窗口过滤。旧邮件时间越过当前 overlap 后再移入 Inbox，不能证明正常自动收信；本次未改 watermark 或扩大采集范围。未来可另行设计服务商原生修改提示或显式授权重扫，本发布不实施。

## 已停止的续验与未完成事项

新增 Outlook 回复草稿经原 UI 保存并重开，再通过原发送控件于 **UTC 10:55:07.599–10:55:55.668／北京时间 18:55:07.599–18:55:55.668** 执行发送，耗时 48.069 秒。请求 HTTP 200，但业务状态为 `failed`、错误码 `email_send_not_dispatched`；对应任务 `effect=0`、`attempt=0`、`dispatch=null`，没有原生 send journal，也没有清理围栏。**没有实际发出邮件，未重试该操作。** 失败草稿和原邮件保留；它与既有未知发信分别处理，历史未知任务未重放。

保留的受控只读诊断执行到 `verifySendAccount`，在 `inspect`/`eval` **10.425 秒**后超时，尚未选择回复目标或点击回复按钮。诊断释放了私人句柄、保留用户页面且未留下围栏；它缩小了失败前置步骤的范围，但尚未区分账号检查为何超时。另外，源码审查发现 `openNativeReplyTarget` 通过网络采集后，没有选择/打开实际 Outlook 目标，也没有提供回复编辑器仍依赖的旧 DOM 证明。该独立缺口未修复，**不能将它认定为前面的账号检查超时根因**。证据为 `reply-diagnostic/owned-prerequisites-observation.private.json`、`reply-diagnostic/first.private.json` 和 `mac-upgrade/evidence/result-reply-send-original.private.json`。

用户要求停止时，未完成事项为：

- 查明 Outlook 发件账号检查超时，并独立修复/验证原生回复目标选择。回复链路未验收，本任务不再安排发信尝试。
- 验证一封新邮件进入 Gmail 普通收件箱并自动完成 Gateway 捕获/解析。两封旧垃圾邮件仍按策略排除，未改垃圾邮件规则或检查点。六方向服务商送达通过，自动捕获仅四个方向有证据。
- 查明重复出现的首次原生导入失败原因。`.12`、`.14` 都在有界第二次启动后通过，原因仍未证实，独立的确定性 stage 修复不能替代这一结论。
- Outlook 通用附件上传仍未验收。纯文本发送和原文下载不代表附件发信通过；历史未知任务不得重发。
- 源码推送及正式发布未完成。Mac普通启动和保全已完成；停止要求后未继续修复、实机诊断或发信。
- 另有既存Mac browser-host重连/授权恢复问题未解决，work2 ISCP邮箱检查已通过。
