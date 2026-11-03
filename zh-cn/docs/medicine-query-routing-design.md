# 药箱查询与编辑路由设计

> 语言：简体中文 | [English](../../docs/medicine-query-routing-design.md)
>
> 日期：2026-11-02。状态：设计稿第四版已在本地源码实现，多药品批量写入和分组字段补全审批已接通；验证与发布边界见第 13 节，尚未部署或安装桌面版本。范围：药箱数据改为 Gateway 存储、客户端同步；在 `capability.Catalog` 增加一级路由 `medicine`，下设药箱内容查询、药箱信息编辑、药品信息查询三个叶子。药箱模块的页面、菜单、布局和交互保持不变。

## 1. 背景与目标

药箱模块已上线（Gateway `97613e2a`，Mac `e6392b45`）。药品记录目前只存在桌面端本机 SQLite（`userData/workbench/medicine/medicine-box.sqlite`），对话中的药品问题只能落到 `conversation.answer` 或 `browser.internet_search`：模型看不到药箱，也没有药品场景的回答约束。

负责人要求（2026-11-02）：

1. 在一级路由中新增药箱查询。
2. 药箱内容查询：问药箱相关信息时，模型依据药箱内的记录回答。
3. 药品信息查询：单纯问药品时，判断用 Info 搜索还是直接调用模型，再返回答案。
4. 药箱数据和邮箱一样，在后端存储、客户端同步；第三方渠道和定时任务都能用。
5. 支持在聊天中修改药箱，新增叶子「药箱信息编辑」。
6. 「我发烧了吃什么药」这类没提药箱的问题，也要结合药箱内容给出一般建议：能用已有的药，还是需要购买。
7. 判断走 Info 还是模型失败时，默认走 Info。
8. 保留回答末尾的固定提示。
9. 字段明确且校验通过的药箱修改无需审批。
10. 支持多种药品批量写入；字段缺失或含义不明确时，弹出审批窗口，让用户补全或确认后继续。

设计原则：

- 路由只增加 Catalog 节点和 Workflow Profile，继续用现有拆分、融合打分、Top-2、澄清和单叶子派发，不加关键词路由。
- 药箱的权威数据在 Gateway；桌面端保存同步缓存，界面读缓存，修改经 Gateway 提交。
- 聊天里的单条或批量修改，字段明确且校验通过后直接写入；缺字段或有歧义时，整批暂停，在现有审批窗口中补全后再次校验。靠确定性校验、版本核对和回复里的逐条修改前后对比防止误改。
- 「Info 还是模型」是 Workflow 内的执行策略，不影响路由打分。

## 2. 现状

| 项目 | 现状 | 本设计的用法 |
|---|---|---|
| 一级路由 | `internal/capability/catalog.go` 中 root 下的 branch：`conversation`、`browser`、`ai_chat`、`document`、`localmind`、`schedule`、`coding`；Catalog 修订 `2026-10-30.v28` | 新增 branch `medicine` 和三个叶子，修订升为 v29 |
| 邮箱的存储与同步 | Gateway 保存权威邮件（PostgreSQL 类型化仓库）；`internal/mailsync` 维护带 epoch／sequence 的有界事件日志，`mail.sync` 按游标返回快照或增量；桌面 `MailSyncStore` 缓存，表为 `boxes`、`mail`、`snapshots`、`staged`；ISCP 事件类别 `email` 提醒客户端同步 | 药箱照此实现 |
| 桌面药箱存储 | `MedicineStore`：按 deployment+owner 分区，最多 2000 条；字段名称（≤80）、分类（8 类）、数量（0–100000，两位小数）+单位（9 种）、有效期／无有效期、位置（≤80）、用途（≤80）、备注（≤500，可多行）；渲染进程经 IPC `sparkclaw-medicine-box:invoke` 调 `list / save / remove` | 改为同步缓存，IPC 接口不变 |
| 客户端修改 | 带回执的转发操作（`recovery: operation_receipt`）加桌面 `ISCPMutationJournal`，例如 `mail.drafts.save` | 药箱新增、修改、删除用同样方式 |
| 免审批的写工具 | 定时任务的 `reminders.update` / `reminders.cancel`：`RiskReversible`、`RequiresApproval: false`、`Sandbox: forbidden` | 药箱写工具照此注册 |
| Info 搜索 | `browser.internet_search` 用 `web.discovery`（provider `info`），由 ToolHub `web.search` 执行，grounded 收尾 | 药品信息的 Info 路径复用 |
| 时区 | 消息信封带 `ClientTimezone`，定时任务自带时区；`currentSearchDateForTimezone` 计算当天日期 | 用来计算效期状态 |

## 3. 总体方案

```text
数据
  Gateway 药箱仓库（PostgreSQL，按 owner）── 事件日志 ── medicine.sync ──> 桌面缓存 ──> 药箱界面
       ^                                                                        |
       └──────── medicine.items.create / update / delete（带回执）<──────────────┘

路由
  owner 文本（桌面、WebChat、微信等第三方、定时任务）
    -> 现有拆分 / 嵌入 + Tree 打分 / Top-2 / 澄清
        -> medicine.box_query   #read   药箱里有什么、剩多少、在哪、是否过期
                                #advise 按症状用药：能用已有药还是需要购买
            -> read_box（Gateway 仓库）-> 依据快照回答
        -> medicine.box_edit    #create / #edit / #delete
            -> read_box -> plan_change（单条 / 批量）-> 确定目标与校验
                -> 字段明确：整批写入 -> 逐条回复
                -> 缺失 / 不明确：审批窗口补全 -> 重新校验 -> 整批写入 -> 逐条回复
        -> medicine.drug_info   #answer 药品知识，不涉及药箱
            -> choose_source -> Info 搜索 | 直接调用模型 -> 回答
```

## 4. 药箱数据：后端存储与客户端同步

### 4.1 归属

- 权威数据在 Gateway，按 `owner_id` 分区。同一负责人的所有桌面安装看到同一个药箱。
- 桌面端只保存同步缓存，用于界面显示和离线查看。
- 照片识别（`medicine.recognize`）、文字解析（`medicine.parse`）不变；识别结果仍由用户确认后才保存，只是保存目标改为 Gateway。

### 4.2 Gateway 存储

新增包 `internal/medicine`（领域类型、校验、服务），存储按现有 `*_contract.go` 加 memory／file／postgres 三种实现，新增迁移 `0021_medicine_box.sql`：

```sql
CREATE TABLE IF NOT EXISTS medicine_items (
    owner_id    text NOT NULL,
    id          text NOT NULL,              -- med_<32 hex>，沿用桌面端格式
    name        text NOT NULL,
    category    text NOT NULL,
    quantity    numeric(8,2) NOT NULL,
    unit        text NOT NULL,
    expires_on  date,                       -- 无有效期或旧数据为空
    no_expiry   boolean NOT NULL,
    location    text NOT NULL DEFAULT '',
    usage       text NOT NULL DEFAULT '',
    notes       text NOT NULL DEFAULT '',
    version     bigint NOT NULL CHECK (version > 0),
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    PRIMARY KEY (owner_id, id)
);
CREATE TABLE IF NOT EXISTS medicine_events (
    owner_id  text NOT NULL,
    sequence  bigint NOT NULL,
    item_id   text NOT NULL,
    deleted   boolean NOT NULL,
    item      jsonb,                         -- 删除事件保存被删除的整条记录
    PRIMARY KEY (owner_id, sequence)
);
CREATE TABLE IF NOT EXISTS medicine_box_state (
    owner_id  text PRIMARY KEY,
    epoch     text NOT NULL,
    sequence  bigint NOT NULL,
    floor     bigint NOT NULL                -- 早于它的事件已清理
);
```

- 每次新增、修改、删除在同一事务里更新 `medicine_items`、追加 `medicine_events`、推进 `medicine_box_state.sequence`，`version` 加 1。
- 批量新增或修改复用同一领域服务，以整批为事务边界；全部条目、逐条事件、序号推进和持久化批次回执共同提交或全部不变。memory／file／postgres 三种实现都要保证这一点，不得循环调用单条写接口造成部分成功。
- 事件保留最近 4096 条（同 `mailsync.MaxEvents`），更早的推进 `floor`；游标早于 `floor` 时返回重置，客户端重新拉快照。
- 每个负责人最多 2000 条（同桌面端上限）。

### 4.3 字段与校验

Go 端实现与 `medicine-store.mjs` 相同的规则，并共用一组测试用例，保证两端一致：名称必填 ≤80；分类为 8 个固定值；单位为 9 个固定值；数量 0–100000、最多两位小数；有效期为合法 `YYYY-MM-DD`，`no_expiry` 为真时日期必须为空；位置、用途 ≤80；备注 ≤500，可多行；不得含控制字符。

### 4.4 接口

新增 ISCP 转发操作（同步写入 `operations.json` 并运行 `node scripts/sync-iscp-operations.mjs`），HTTP 路径同时加入桌面 `workbench-proxy-policy` 白名单（上次发布漏加 `/api/medicine` 导致 501）：

| 操作 | HTTP | scope | 修改 | 恢复 | 用途 |
|---|---|---|---|---|---|
| `medicine.sync` | `POST /api/medicine/sync` | `medicine.read` | 否 | `read` | 游标增量同步 |
| `medicine.items.create` | `POST /api/medicine/items` | `medicine.write` | 是 | `operation_receipt` | 新增一条 |
| `medicine.items.update` | `PUT /api/medicine/items/{item}` | `medicine.write` | 是 | `operation_receipt` | 修改一条，带 `base_version` |
| `medicine.items.delete` | `DELETE /api/medicine/items/{item}` | `medicine.write` | 是 | `operation_receipt` | 删除一条，带 `base_version` |
| `medicine.items.batch` | `POST /api/medicine/items/batch` | `medicine.write` | 是 | `operation_receipt` | 同一操作的批量新增或修改，整批原子提交（第 7.6 节） |
| `medicine.import` | `POST /api/medicine/import` | `medicine.write` | 是 | `operation_receipt` | 本机旧数据一次性上传（第 4.7 节） |

ISCP 事件新增类别 `medicine`：药箱有变化时推送，桌面端收到后触发一次 `medicine.sync`。

`medicine.items.batch` 只接收已补全、校验通过的最终变更，使用一个批次操作 ID 与载荷摘要；修改项各带 `base_version`。聊天工具与此接口共用领域服务。字段补全通过版本化的审批输入合约提交，不能把未完成的草稿交给批量写接口，也不能借 `medicine.import` 完成聊天批量写入。

### 4.5 增量同步

请求与响应沿用 `mail.sync` 的形状：

```json
{"cursor": "<上次返回的游标，首次为空>", "limit": 100}
```

```json
{
  "schema_version": 1, "epoch": "…", "mode": "snapshot | delta",
  "base_sequence": 120, "sequence": 126,
  "events": [{"sequence": 121, "id": "med_…", "deleted": false, "item": {"id": "med_…", "version": 3, "name": "…"}}],
  "cursor": "…", "more": false
}
```

- 首次同步或收到重置时为 `snapshot`，分页返回全部条目；客户端先写 `staged`，最后一页到达后整体替换，避免显示半份药箱。
- 之后为 `delta`，只返回游标之后的事件。
- 每页最多 100 条（同 `MAIL_PAGE_LIMIT`），并保证不超过 ISCP 响应大小上限。

### 4.6 桌面端缓存与界面接入

- `medicine-box.sqlite` 升到 schema v3：`items` 增加 `version`，新增 `sync_state`（scope、epoch、sequence、cursor、synced_at）和 `staged`，做法同 `MailSyncStore`。
- 渲染进程的 `window.sparkclawMedicineBox.{list, save, remove}` 和返回格式不变，药箱界面代码不需要改：
  - `list`：读缓存，同时在后台触发一次同步。
  - `save` / `remove`：在主进程转成 `medicine.items.*` 转发操作，先写 `ISCPMutationJournal`，成功后用返回的条目更新缓存。
- 离线或未连接 Gateway 时，药箱照常显示缓存；添加、编辑、删除和数量加减按钮保留但禁用，提示「连接后可修改」。只有用户点了修改却无法完成时才显示错误。
- 结果未知（请求已发出、未收到回执）时，按现有回执机制核对，不重复提交。

### 4.7 本机数据迁移

- 升级后第一次连上 Gateway，桌面端把本机已有条目经 `medicine.import` 分批上传（每批不超过 ISCP 请求上限），保留原 `med_…` ID 和创建时间。
- Gateway 按 ID 幂等：ID 已存在则跳过，不覆盖 Gateway 上更新的版本。同一负责人的多台电脑各自上传，ID 不同所以直接合并；内容相同但 ID 不同的条目不自动去重。
- 上传全部成功后，桌面端在本机记录迁移完成，再拉一次快照，之后只用缓存。原表重命名为 `items_v2_backup` 保留一个版本周期，下一次发布再删除。
- 迁移失败不影响查看；下次连接时继续上传未完成的批次。

### 4.8 并发与冲突

- 修改和删除必须带 `base_version`。版本不符返回 409。
- 界面上遇到 409：刷新这条记录并提示「这条药品已在别处修改，已更新为最新内容」，请用户重新操作。
- 聊天修改在读取药箱时记下版本，写入时核对（第 7.6 节）。

## 5. 一级路由与语义注册

### 5.1 Catalog 节点

```go
branch("medicine", string(RootID), "Read, advise from, or edit the owner's medicine box, or answer general medicine questions."),
leaf("medicine.box_query", "medicine", "Answer from the owner's medicine box records: whether a medicine is stocked, remaining quantity, location, expiry status, and symptom-based advice on whether a stocked medicine fits or a new one is needed.", RouteContract{
    Operations: []app.RouteOperation{app.RouteOperationRead, app.RouteOperationAnswer}, FactScopes: []app.RouteFactScope{app.RouteFactScopeMedicineBox}, RequireQuery: true,
}),
leaf("medicine.box_edit", "medicine", "Add or change one or several medicines in the owner's medicine box, or remove one medicine; collect missing or ambiguous fields before writing.", RouteContract{
    Operations: []app.RouteOperation{app.RouteOperationCreate, app.RouteOperationEdit, app.RouteOperationDelete}, RequireQuery: true,
}),
leaf("medicine.drug_info", "medicine", "Answer general questions about a named medicine: ingredients, indications, usage, side effects, interactions, precautions, price, availability, or recalls. Does not read the owner's medicine box.", RouteContract{
    Operations: []app.RouteOperation{app.RouteOperationAnswer}, RequireQuery: true,
}),
```

- 新增 `app.CapabilityMedicineBoxQuery`、`CapabilityMedicineBoxEdit`、`CapabilityMedicineDrugInfo` 及同名 `WorkflowID`，fact scope `medicine_box`。
- `DefaultCatalogRevision` 升为 `2026-11-xx.v29`；图修订随之变化，旧修订的签名澄清选项按现有规则作废。
- 三个叶子都接受全部来源（桌面、WebChat、第三方、定时任务）。定时任务不能交互，编辑遇到需要澄清或缺字段时直接结束，把原因发到任务的投递目标（第 7.4、7.5 节）。

### 5.2 语义变体

| 候选 | 澄清标签 | EmbedTexts（独立语句） | HardNegatives |
|---|---|---|---|
| `medicine.box_query#read` | 查看我的药箱记录 / Check my medicine box | 药箱里还有退烧药吗；我的布洛芬还剩多少；有哪些药快过期了；感冒药放在哪里；家里还有没有创可贴；药箱里过期的药有哪些；What do I have for allergies in my medicine box? | 布洛芬有什么副作用；把布洛芬减一片；帮我添加一盒感冒灵；打开药箱；一小时后提醒我吃药 |
| `medicine.box_query#advise` | 根据药箱给用药建议 / Advise from my medicine box | 我发烧了吃什么药；头疼得厉害有什么药能吃；孩子咳嗽家里的药能用吗；拉肚子了要不要去买药；被划了个口子怎么处理；I have a sore throat, what should I take? | 布洛芬和对乙酰氨基酚有什么区别；发烧是怎么引起的；明天早上提醒我吃退烧药；添加一盒退烧药 |
| `medicine.box_edit#create` | 往药箱添加药品 / Add medicines to my box | 药箱里加一盒布洛芬，24 粒，2027 年 5 月过期；新买了一瓶碘伏放在卫生间；把这盒感冒灵记到药箱里；把布洛芬、感冒灵和碘伏一起记到药箱；Add loratadine and bandages to my medicine box | 药箱里有布洛芬吗；识别这张药盒照片；布洛芬多少钱 |
| `medicine.box_edit#edit` | 修改药箱里的药品 / Change medicines in my box | 布洛芬吃了两粒；把感冒灵的数量改成 5 袋；创可贴挪到客厅抽屉了；阿莫西林的有效期改成 2026 年 12 月；给碘伏加个备注开封于 11 月；布洛芬减两粒，感冒灵减一袋，帮我一起更新 | 布洛芬还剩多少；布洛芬一次吃几粒；提醒我每天吃两粒布洛芬 |
| `medicine.box_edit#delete` | 从药箱删除药品 / Remove a medicine from my box | 把过期的阿莫西林删掉；药箱里的感冒灵扔了，删除记录；Remove the expired ibuprofen from my box | 有哪些药过期了；删除这个定时任务；删除这个文件 |
| `medicine.drug_info#answer` | 查询药品信息 / Look up medicine information | 布洛芬和对乙酰氨基酚有什么区别；阿莫西林饭前还是饭后吃；连花清瘟的成分是什么；孕妇能吃氯雷他定吗；头孢和酒精一起会怎样；感康现在多少钱；这个药最近有没有召回；What are the side effects of ibuprofen? | 我药箱里有布洛芬吗；我发烧了吃什么药；解释一下免疫系统；今天有什么医疗新闻；提醒我八点吃药 |

### 5.3 分界规则

TreeDescription 按以下规则写清各候选的区别：

- 问用户自己药箱里的东西（有没有、剩多少、在哪、是否过期）→ `box_query#read`。
- 描述症状或情况、问该吃什么或怎么处理，没有点名具体药品 → `box_query#advise`。是否提到「药箱」不影响判断。
- 问某个点名药品的一般信息、比较，或价格、在售、召回 → `drug_info`。点名药品且问「我药箱里的能不能吃」（例如「我药箱里的布洛芬能和感冒灵一起吃吗」）→ `box_query#advise`。
- 要求添加、记录用量、改字段、删除 → `box_edit` 对应变体。已完成的描述（「我刚吃了布洛芬」）本身不是修改请求；明确要求更新数量的（「布洛芬吃了两粒，帮我减掉」）才是。这类边界作为 HardNegatives 和测试用例覆盖。
- 吃药提醒属于 `schedule.manage`；定时触发的「检查药箱有没有快过期的药」在到期时进 `box_query#read`。

同一新增或修改意图下列举多种药品，应保留为一个业务片段、一个 `box_edit` Workflow 和一个批次；拆分器不按药名逐项派发。混合新增、修改、删除仍按现有复合请求规则拆分，各片段独立执行，不承诺跨片段原子性。

### 5.4 兄弟叶子与校准

- `conversation.answer#answer` 的 HardNegatives 加入「布洛芬有什么副作用」「我发烧了吃什么药」。
- `browser.internet_search#search` 的 HardNegatives 加入「感康现在多少钱」「这个药最近有没有召回」，TreeDescription 注明具体药品的价格、在售、召回属于药品信息。
- 修改已发布 Profile 的 RoutingSemantics 时，按现有规则判断是否升 Profile 修订并保留 legacy 注册。
- `box_edit` 是修改类路由，按 [意图路由](intent-routing.md#决策状态) 使用更严格的分数和差距阈值；只有一个语义通道可用时，编辑直接 `blocked`，查询仍可在降级阈值下执行。
- 按 [意图路由](intent-routing.md#扩展路由) 第 5、6 步补齐用例并重建校准证据；融合权重 `alpha = 0.50` 不改，除非校准证据要求。

## 6. 叶子一：药箱内容查询 `medicine.box_query`

### 6.1 Workflow 计划

```text
read_box  （确定性，Runtime 调 medicine.box.read，MaxAttempts 1）
  -> answer（grounded 收尾，证据为药箱快照；#advise 另带用药建议约束）
```

- ToolHub 新增只读工具 `medicine.box.read`（能力 `medicine.box.read`，效果 `local.read`），直接读 Gateway 仓库，不经过桌面端。
- 风险 `RiskRead`，不暴露写工具。

### 6.2 快照投影

- 当天日期：消息来源用 `ClientTimezone`，定时任务用任务自身时区，都没有时用 Gateway 默认时区；与药箱界面一样以 30 天为临期阈值。
- 每条给模型的字段：名称、分类中文名、数量+单位、有效期、状态（已过期／30 天内到期／未临期／无有效期）和剩余天数、位置、用途、备注（截到 120 字）。日期由 Runtime 算好，模型不做日期计算。
- 按有效期升序，同名药品相邻。证据超过 48 KiB 时先去掉备注，再去掉用途，仍超出则截断条目并注明总数。

### 6.3 `#read` 回答约束

- 只依据快照回答。找不到就说「药箱里没有 X 的记录」，不猜测。
- 数量、单位、位置原样引用，不换算、不补全。
- 已过期的标明已过期；30 天内到期的给出到期日。
- 药箱为空：「药箱里还没有药品记录，可以在药箱中添加，也可以直接告诉我。」
- 截断时说明只看了部分记录。

### 6.4 `#advise` 回答约束

回答固定按三部分组织：

1. **一般建议**：针对所述症状的常规非处方用药方向和护理要点，不做诊断。
2. **药箱里能用的**：只列药箱中用途匹配、未过期、数量大于 0 的药，写出名称、剩余数量、位置；同时提醒按说明书用药。药箱里有但已过期或用完的，单独说明不能用。
3. **是否需要购买**：药箱里没有合适的药时，说明可以购买哪一类非处方药（写通用名或类别，不推荐品牌，不给购买链接）；已有合适的药时不建议购买。

另外：

- 出现急症信号（呼吸困难、胸痛、严重过敏、意识异常、大量误服、高热持续不退等）时，第一句先建议立即就医，再给其余内容。
- 儿童、孕期哺乳期、老人、慢性病和肝肾功能不全等情况，建议先问医生或药师；药箱里的成人药不直接推荐给儿童。
- 处方药（如抗生素）不建议自行使用，即使药箱里有。
- 末尾保留固定提示（第 8.4 节）。

### 6.5 来源

桌面端、WebChat、微信等第三方渠道和定时任务都可以查询，结果按现有 Delivery 规则回到来源或定时任务指定的目标。例如定时任务「每天早上 9 点检查药箱有没有快过期的药」由 `schedule.manage#create` 创建，到期时负载进入 `box_query#read`。

## 7. 叶子二：药箱信息编辑 `medicine.box_edit`

### 7.1 范围

- 一次请求支持一种或多种药品的新增、修改；一个批次使用同一种操作，单条是批次长度为 1 的情况。初始上限为 20 条，超过时提示分批，不能静默截断或自动分批写入。
- 新增：每项生成独立记录，同名不同有效期或位置的药品也可以分别录入。
- 修改：逐项修改数量（设值或增减）、单位、有效期／无有效期、位置、用途、备注（替换或追加）、分类、名称。
- 删除仍只支持一条记录，批量删除本次不做。药箱页面原有照片和文字录入交互保持不变，批量能力接在聊天路由中。

### 7.2 Workflow 计划

```text
read_box      （确定性，读 Gateway 仓库）
  -> plan_change （Fast 结构化调用，输出 medicine_edit_v2）
  -> resolve     （逐项确定目标、合并字段、收集缺失与歧义、整批校验）
      -> 全部明确：apply
      -> 需要补全：collect_fields（审批窗口，等待用户输入或选择）
          -> revalidate（确定性，校验输入、目标版本和整批最终值）-> apply
  -> apply       （medicine.box.write，整批事务提交，生成一个批次回执）
  -> answer      （确定性模板，逐条列出结果，不再调模型）
```

等待补全期间整批均不写入，包括字段已经完整的条目。用户补全属于原批次的结构化输入，绑定原请求和草稿，不作为脱离上下文的新聊天请求重新解析。

### 7.3 归一化变更结构 `medicine_edit_v2`

新增 modelcapacity 操作 `medicine.edit_plan`（fast structured，关闭思考，严格 JSON Schema，最多修复一次）。输入为业务片段原文、当天日期和药箱条目的精简列表（名称、有效期、位置、数量+单位，不含备注）。模型严格输出采用 `medicine_edit_v4`：每项自带 `action`（`create`、`update` 或 `delete`），`changes` 为 `{field, value, evidence}` 数组，值保留字符串／数字／布尔类型，可选目标字段携带原文证据。Runtime 从各项推出批次 `operation`，各项动作不一致时整批拒绝。原 `medicine_edit_v3` 让模型给出含 `mixed` 的整批 `operation`，真实快速通道把每个普通修改都标成 `mixed`，聊天修改从未写入。Runtime 随即归一化为下例中的字段映射并冻结草稿。这解决了真实受约束解码中有效期等可选键在后续键输出后被跳过的问题，不改变批次、审批或存储语义。归一化草稿示例：

```json
{
  "operation": "create",
  "items": [
    {
      "changes": {"name": "布洛芬", "category": "fever", "quantity": 24, "unit": "capsule", "expires_on": "2027-05-31", "no_expiry": false},
      "unresolved": []
    },
    {
      "changes": {"name": "感冒灵", "category": "cold", "quantity": 6, "unit": "sachet"},
      "unresolved": [{"field": "expiry", "reason": "missing"}]
    }
  ]
}
```

- `operation` 为 `create`、`update`、`delete`，须与路由变体一致；`items` 为 1–20 项，删除必须恰为 1 项。Runtime 按数组长度计数，不再使用模型给出的 `medicine_count`。
- 每个修改或删除项带 `target`，含用户给出的名称及可选的有效期、位置；Runtime 解析为记录 ID 和 `base_version`。模型不选记录 ID，也不决定写入。
- `changes` 在新增草稿中可以缺字段，直到补全才成为合法记录；修改只包含用户要改的字段，未提及的字段保留原值。`quantity` / `quantity_delta`、`notes` / `notes_append` 各自互斥。
- `unresolved` 标记缺失或歧义（`missing` / `ambiguous`），`field` 限于声明的字段及 `target`、`expiry` 两个组合输入。Runtime 另行检查必填字段、目标唯一性与来源依据，不因模型返回空数组就视为信息完整。
- 不明确的值保留未填写状态，不得猜数量、单位、日期或目标，也不能用 0、空字符串、「无有效期」冒充已补全。Runtime 为草稿各项分配稳定 `entry_id`，用于窗口行、输入和回执对应；它不是药箱记录 ID。

### 7.4 确定目标与澄清

Runtime 对每一项按名称完全匹配、再按规范化名称（去空格、全半角统一）匹配，两者都没有结果时，才匹配包含用户说法或被其包含的名称（「布洛芬」可找到「布洛芬缓释胶囊」）；并用用户明确给出的有效期、位置过滤，位置按包含关系匹配（「卧室」匹配「卧室柜子」）。模型只报告用户原话中的目标，不选记录。修改或删除时若模型标记名称不明确且并未改名，表示目标不明确，窗口让用户选择记录，而不是输入名称：

1. 只有一条：绑定记录 ID 和读取时的版本。
2. 多条：在补全审批窗口的对应药品下列出签名候选，包含名称、有效期、位置、数量，用户必须明确选一条；不能自动选第一条。
3. 没有：整批不写入，指出哪项「药箱里没有 X 的记录」，不能把修改悄悄变为新增。
4. 新增不需要已有目标；已有同名记录也照常新增，并在结果中提示同名药品分别记录。
5. 同一批次多个修改项指向同一记录时拒绝写入，提示合并该记录的修改，不能按顺序重复扣减或隐式覆盖。
6. 定时任务不能交互；需要选择目标或补字段时整批结束，将对应药品和待补信息发到投递目标，不挂起等待审批。

### 7.5 字段校验与审批窗口补全

- 新增必须明确名称、数量、单位，以及有效期或用户明确选择的「无有效期」。只给月份时，按现有规则取当月最后一天，并在窗口或写入结果中展示实际日期。
- 分类沿用 8 个固定值，不确定时用「其他」；未提及的位置、用途、备注可留空，无需为了可选字段弹窗。用户明确要求填写但含义不清的字段仍需补全；修改记录未涉及的旧字段不要求补填。
- 缺必填字段、数量／单位／日期表述有歧义或目标不唯一时，复用现有审批窗口容器，增加类型化字段表单。窗口按药品分组：展示已确定内容和整批变更摘要，只要求输入缺失或待确认字段；数量用数值输入，单位用固定选项，有效期用日期或「无有效期」，目标用候选选择。
- 所有需要处理的药品在同一窗口中集中补全；字段错误在对应行显示，校验未通过时禁用提交。提交文案明确「补全并写入 N 条」，用户一次提交补全信息并确认整批，无额外的第二轮通用写审批。
- 后端重新校验全部条目的最终值（第 4.3 节）。数量减成负数时拒绝并展示当前剩余；等于 0 时允许且在回复中注明。整批新增后超过 2000 条时全部不写入。
- 拒绝、取消、审批过期或任务终止时，整批零写入；窗口关闭按既有取消语义处理，不视为同意。Gateway 重启沿用现有审批等待任务终止规则，不恢复旧草稿继续写入。
- 桌面和 WebChat 使用同一表单合约。微信等不能显示表单的来源返回具体缺失项和补充完整请求的提示，本次整批不写入；不把任意后续消息当作审批，也不自动转发到别的渠道。

**审批输入合约是第三阶段的新能力。** 当前桌面执行审批只接收 `digest` 和 `decision`，`ComposerApproval` 只提供同意／拒绝，不能宣称现有接口已经能提交字段。需在原审批组件及其适配器下增加表单，配套扩展 Gateway、LAN／ISCP、桌面日志及 WebChat 输入合约，并协商版本：

- 待补全记录绑定 deployment／owner／client、原 request、草稿修订和摘要、每项 `entry_id`、候选记录及版本、可补字段 schema、期限；返回输入必须与这些绑定一致且只含允许的字段。
- 服务端先校验输入，再将用户提交的补充值与完整最终批次一起冻结，生成最终摘要；同意只对这个最终批次有效。不得拿旧草稿的同意授权不同值，重新选择目标时也不能自动更新已绑定的版本。
- 补全提交有持久化幂等键；重复提交同一内容返回同一结果，换内容或使用过期修订则拒绝。原请求正文和摘要保持不变，补充输入单独关联；提交结果未知时查询原审批／批次回执，不创建新批次。
- 老客户端不支持表单合约时保留入口，禁用该审批的提交并提示使用支持的客户端；不能退化成只有「同意」按钮的空值写入。

例如「把布洛芬 24 粒、2027 年 5 月到期，感冒灵 6 袋一起记到药箱」：窗口展示两条草稿，只要求补感冒灵的有效期或明确选择无有效期；提交并通过校验后两条一起写入，取消则两条都不写。

### 7.6 直接写入与批次提交

负责人 2026-11-02 的最新决定细化原免审批规则：字段明确、目标唯一且校验通过时直接写入；有缺失或歧义时先经第 7.5 节的补全审批。批量本身不额外触发审批。

- `medicine.box.write` 维持 `RiskReversible`、`RequiresApproval: false`、`Sandbox: forbidden`；补全等待由 Workflow 的类型化节点触发。写节点只接受已完整校验的批次，不能绕过补全节点写入缺失值。
- 路由仍使用修改类的严格分数和差距阈值，语义通道降级时 `blocked`。操作不一致、未解决的歧义、缺必填字段或非法值均不能进入写节点。
- 写入事务中再次核对所有目标的归属、存在性和 `base_version`，以及药箱容量。任意一项冲突则整批不写入，列出冲突药品并请用户重新发起；不能自动用新版本重放旧审批或数量增减。
- 批次操作 ID 由 Runtime 分配并绑定 owner、原请求、最终载荷摘要。整批记录、逐条事件、序号和回执一次提交，更新项各递增版本；file 后端也必须通过一次持久化提交保证崩溃恢复后全有或全无。
- 同一批次 ID、同一摘要重试只能得到原结果；同一 ID 不同摘要拒绝。已提交但响应丢失时从回执恢复，不能逐条补写或生成新 ID 重试。

### 7.7 写入后

- 整批提交成功后发出 `medicine` 事件，各桌面自动同步。逐条事件可分页返回，但批次全部变化提交后才能声称完成；同步完成后验收全部条目均出现。
- 模板回复先给总数，再按输入顺序逐条列出新增记录或修改前后值，例如「已更新 2 条：布洛芬 6 粒 → 4 粒；感冒灵 5 袋 → 4 袋」，并展示必要的有效期、位置和同名提示。删除仍列出整条记录，事件保留删除前的值。
- 校验失败或取消明确回复整批未写入；结果未知只说明正在核对，并按一个批次回执查询，不能声称成功或重复写入。

## 8. 叶子三：药品信息查询 `medicine.drug_info`

### 8.1 来源判断 `choose_source`

第一个节点为确定性节点，Runtime 发起一次 Fast 结构化调用（modelcapacity 操作 `medicine.info_source`，关闭思考，严格 JSON Schema，最多修复一次）：

```json
{"source": "info", "reason_code": "price_or_purchase"}
```

| source | reason_code | 含义 |
|---|---|---|
| `info` | `price_or_purchase` | 价格、在哪买、医保 |
| `info` | `availability_or_approval` | 是否上市、在售、获批、进口 |
| `info` | `safety_notice` | 召回、停产、警示、说明书修订 |
| `info` | `recent_evidence` | 最新研究、最新指南 |
| `info` | `unfamiliar_product` | 不熟悉的药名、品牌或复方 |
| `model` | `stable_knowledge` | 成分、作用机制、适应症、常见不良反应、一般用法、常见相互作用、储存 |

- 输入只有业务片段原文和当天日期，不带候选或路由分数。
- 判断结果写入节点 `OutcomeRefs`（kind `medicine_info_source`）和审计事件 `workflow.medicine_source_selected`，做法同 `detect_response_media`。
- **判断失败（初次和修复都无效）时走 Info**（负责人 2026-11-02 决定）。只有 Info 未配置时才走模型。

### 8.2 Info 路径

- 节点作用域为 `web.discovery`（provider `info`），参数绑定路由 query；查询词经 `materializeRoutedQuery` 处理，与 `browser.internet_search` 一致。
- grounded 收尾：依据 Info 来源回答，并列出来源。
- Info 调用失败或没有结果：改走模型路径，回答开头用一句话说明「未能联网核实，以下为一般知识」。

### 8.3 模型路径

- 与 `conversation.answer` 相同：不带工具，`workflowFinalizationModel`，但使用第 8.4 节的约束。
- 不编造来源、价格、批准文号或日期；涉及会变化的事实时，提示以说明书或最新信息为准。

### 8.4 共用的药品回答约束

适用于 `drug_info` 两条路径和 `box_query#advise`：

- 剂量只给说明书常规成人剂量，并提示以说明书为准；儿童、孕期哺乳期、老人、肝肾功能不全等特殊人群建议咨询医生或药师。
- 出现急症信号时，先建议立即就医。
- 不做诊断，不给处方药的自行用药方案。
- **末尾固定一句**（负责人 2026-11-02 决定保留）：「以上仅供参考，用药请以说明书或医生、药师意见为准。」`box_query#read` 和 `box_edit` 只讲记录，不加这句。
- 现有输入守卫和内容治理不变；过量、自伤类问题按现有守卫处理，列为验收用例。

### 8.5 调用次数

| 叶子 | 调用 |
|---|---|
| `box_query` | 仓库读取 1 次 + 回答 1 次 |
| `box_edit` | 仓库读取 1 次 + Fast 批次解析 1 次 + 整批写入 1 次；需要补全时增加用户输入等待和确定性复核，回复不调模型 |
| `drug_info` 模型路径 | Fast 判断 1 次 + 回答 1 次 |
| `drug_info` Info 路径 | Fast 判断 1 次 + Info 1 次 + 回答 1 次 |

## 9. 负责人决定（2026-11-02）

| # | 问题 | 决定 |
|---|---|---|
| 1 | 药箱数据放在哪里，哪些来源可用 | 和邮箱一样，后端存储、客户端同步；第三方渠道和定时任务都支持（第 4、6.5 节） |
| 2 | 聊天里能否修改药箱 | 支持，新增叶子 `medicine.box_edit`（第 7 节） |
| 3 | 没提药箱的症状问题 | 结合药箱内容给一般建议：能用已有药还是需要购买（`box_query#advise`，第 6.4 节） |
| 4 | 来源判断失败的默认值 | 走 Info（第 8.1 节） |
| 5 | 固定提示句 | 保留（第 8.4 节） |
| 6 | 修改药箱是否需要审批 | 字段明确且校验通过时无需审批；缺字段或有歧义时，在审批窗口补全并确认整批（第 7.5、7.6 节，细化此前免审批决定） |
| 7 | 能否一次写入多种药品 | 支持批量新增、修改，逐项解析与校验（第 7.1–7.3 节） |

本设计另外做了以下选择，负责人如有不同意见再调整：

- 字段明确时修改无需审批，定时任务也可以修改药箱，例如「每天晚上 9 点把维生素 C 减一片」；需要澄清或缺字段时直接结束并说明原因（第 5.1 节）。
- 初始每批最多 20 条，同一批次只做新增或修改，整批原子提交；不做批量删除（第 7.1、7.6 节）。
- 本机旧表保留一个版本周期作为备份（第 4.7 节）。

## 10. 界面影响

- 药箱模块的页面、菜单、布局、样式和交互不变；`window.sparkclawMedicineBox` 接口不变。
- 药箱页面的可见变化：离线时修改按钮禁用并提示「连接后可修改」；修改冲突时提示已刷新（第 4.6、4.8 节）。不增加常驻同步状态。
- 对话沿用现有回答、路由澄清和审批容器；仅在需要补全时，在原审批组件下接入按药品分组的字段表单和目标选择。保留已输入内容、键盘与焦点交互、取消和离线禁用行为，不另做药箱页面或常驻面板。

## 11. 实施与发布

分三个阶段，每个阶段都需要 Gateway 与桌面端配对发布（新增 ISCP 操作或 Catalog 修订）。推送、部署、安装需负责人另行确认。

### 11.1 第一阶段：后端存储与同步

- Gateway：`internal/medicine`；store 合约与三种实现；迁移 `0021_medicine_box.sql`；`medicine.sync / items.* / import` 接口；`medicine` 事件类别；校验与桌面端共用的测试用例。
- 桌面端：schema v3 缓存；同步客户端（参考 `mail-sync-client.mjs`）；`MedicineBoxCapability` 的 `save / remove` 改走转发操作；迁移上传；离线禁用；`workbench-proxy-policy` 白名单；`iscp-capabilities.mjs` 和超时。
- 验收：多台电脑同步、离线查看、冲突提示、迁移（空药箱、2000 条、中断后继续、重复上传）、原有药箱界面全部交互（照片录入、文字录入、详情加减、筛选排序）。

### 11.2 第二阶段：查询路由

- `box_query`（`#read`、`#advise`）和 `drug_info` 两个 Profile、`medicine.box.read` 工具、`medicine.info_source` 操作、兄弟 HardNegatives、Catalog v29、重建校准。
- 验收：第 5.2 节每个变体的直接说法、换种说法、互相混淆、与 `conversation.answer` / `browser.internet_search` 混淆、否定、引号、排障描述、复合请求、英文、非药品问题；桌面、WebChat、微信、定时任务四种来源；空药箱、已过期、临期、无有效期、同名多条、证据截断；`#advise` 的三种结果（有可用药、只有过期药、需要购买）和急症、儿童、处方药场景；来源判断每个 reason_code 至少两个用例、判断失败走 Info、Info 失败退回模型。

### 11.3 第三阶段：聊天编辑

- `box_edit` Profile、`medicine.edit_plan` 操作与 `medicine_edit_v2`、批次写工具和事务服务、目标选择、条件触发的补全审批、逐条回复模板。
- 扩展审批表单与版本化输入合约，配套 Gateway、LAN／ISCP、桌面审批日志和 WebChat；包含草稿与最终批次绑定、幂等补全和回执恢复，不能只改表单显示。
- 验收：单条及批量新增／修改（1、2、20 条，21 条拒绝且不截断）；同意图不按药名拆分；各药字段与顺序不串位；完整字段直接写入且不弹审批；部分或全部条目缺数量、单位、有效期，以及字段有歧义时只弹一个分组补全窗口；可选字段为空不弹窗；同名目标明确选择；未处理字段禁用提交；月末日期、无有效期显式选择、同名新增、容量跨上限。
- 保留设值、增减、减到 0、减成负数、改位置、追加备注和单条删除验收；新增同一目标重复修改、等待期间版本冲突、拒绝／取消／过期／重启均整批零写入；三种 store 的原子性、崩溃恢复和逐条事件；双击提交、回执丢失不重复新增或扣量；错 owner／client、篡改字段、过期草稿和不同载荷重放拒绝。
- 覆盖 LAN／ISCP 与 WebChat 表单、旧客户端提交禁用、微信完整请求成功与缺字段零写入提示、定时任务完整批次成功与需要交互时结束、语义通道降级时编辑被拦下。

### 11.4 真实模型与原生验收

- 用快速通道（`sparkclaw-fast`）抽查来源判断、变更解析和三类回答，参照 `TestMedicineRealModelSpotCheck`。
- 在打包后的应用中验收：原对话界面和药箱模块入口、照片／文字录入、详情加减、筛选排序保持；新增分组表单的键盘、焦点、字段校验、取消、重连和重复提交行为通过；批量聊天修改后药箱界面同步全部条目。

## 12. 明确不做

- 按条件批量删除，以及混合操作跨业务片段的原子提交。
- 药箱模块内的 AI 检索框（open-medkit 适配设计第 9 节「暂不实现」的决定不变）。
- 用药提醒（属于 `schedule.manage`）。
- 推荐具体品牌或给出购买链接。
- 关键词兜底路由，或按药名硬编码分支。


## 13. 本地实现记录（2026-11-02）

- Catalog `2026-11-02.v29` 已接入三个药箱叶子，沿用语义拆分和融合路由。多药品编辑保留为一个批次；明确字段直接写入，缺失或有歧义时在原审批容器中显示一个分组表单。不能交互的渠道返回缺失字段，整批零写入。
- 按 owner 隔离的持久药箱仓库贯穿临时对话执行。memory、file、PostgreSQL 均支持 1–20 条新增／修改的原子批次、单条删除、记录版本、有界同步和不可变回执。提交结果未知时查询绑定原 owner 与原载荷的回执，不重新发起变更。
- 审批表单 schema 1 与输入摘要绑定原冻结草稿、决定和补全值，LAN／ISCP、宿主 WebChat、桌面端共用同一合约。桌面对话库 schema 16、执行控制记录 version 5、药箱缓存 schema 3、PostgreSQL 迁移 `0021_medicine_box.sql`／`0022_approval_inputs.sql` 需要配对发布。旧药品保留在 `legacy_items`，分批上传迁移。
- 保留原药箱导航、分类侧栏、卡片／列表、搜索、效期筛选、详情加减、照片／文字录入和核对弹窗。离线时原地禁用写操作，冲突在原错误位置提示；未新增常驻同步状态面板。
- 验证：Linux Gateway 全量 5994 通过／214 跳过，build／vet 和相关 race 检查通过。实际执行器与 file 药箱测试覆盖补全、拒绝、持久回执、owner 隔离和重复请求。隔离 PostgreSQL 完整 store 测试、并发版本冲突、SQL 回执插入失败回滚、file 持久化故障测试通过。桌面 627 通过／1 跳过，WebChat 592 通过，桌面 UI 构建通过。隔离 Chrome 的 7 组交互与截图覆盖原药箱组件和分组补全表单。真实 Fast 验证通过：16 个药箱路由、完整／缺失字段的精确抽取、20／21 条边界、3 个来源判断、记录／建议／药品知识回答。更广的 63 例搜索回归发现 2 个私有数据误路由、1 个过时的药品期望和 4 个 Tree 通道失败；修正后，失败项、额外样例及对照组共 16 例均在双通道健康时通过。这不代表重新跑过且通过全部 63 例，早期失败日志已保留。
- 当前为源码验证，未打包发布。隔离 Electron 验证程序在渲染器启动前停滞，因此仍需打包应用的原生验收。未声称完成在线 Info、部署、安装或生产多设备迁移；第 11 节仍作为发布验收清单。
- 2026-11-03 编辑解析修复：真实模型下的 WebChat 检查发现，聊天修改全部被当作混合批次拒绝，删除解析失败；原因是模型把整批 `operation` 设为 `mixed`，并给删除附带了无关字段。线上格式改为每项自带动作的 `medicine_edit_v4`；删除忽略字段和缺失项；目标匹配增加上述包含层级。真实模型验收测试新增单条与批量修改、移动位置、补货、删除及两个混合请求。运行时需设置 `SPARKCLAW_MODEL_CAPACITY_PROFILE=sparkclaw-product-v1`：默认 mock 档位下 Tree 通道会因路由提示词超限而失败。
