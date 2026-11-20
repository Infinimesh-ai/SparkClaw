// Development-only fixtures for client-preview.html. They install the desktop
// bridge, the local conversation store and a gateway fetch stub in memory, so
// the real client entry renders in a plain browser without Electron or a backend.
// Nothing here is imported by the production entry points.
import type { DesktopConnectionStatus, DesktopState, SparkClawDesktop } from "../desktop/types";
import type { ClientStoreAPI, LocalConversation, LocalConversationContent, LocalDraft, LocalFile, LocalMessage, LocalSchedule, LocalTask } from "../desktop/clientStore";
import type { MailCapability } from "../desktop/mailCapability";
import type { MedicineBoxAPI, MedicineItem } from "../desktop/medicineBox";
import type { Schedule } from "../api/types";
import type { EmailConversation, EmailDraft, EmailMessage, EmailRenderNode } from "../api/email";

const GATEWAY = "http://preview.invalid";
const SCOPE = "preview-scope";
const now = Date.now();
const minute = 60_000, hour = 60 * minute, day = 24 * hour;
const ago = (ms: number) => new Date(now - ms).toISOString();
const later = (ms: number) => new Date(now + ms).toISOString();
// Local wall-clock time `days` from today, for schedule fixtures with round times.
const at = (days: number, hours: number, minutes = 0) => { const date = new Date(now); date.setDate(date.getDate() + days); date.setHours(hours, minutes, 0, 0); return date.toISOString(); };
const nextMonday = (8 - new Date(now).getDay()) % 7 || 7;
const wait = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));
const id = (prefix: string) => `${prefix}-${Math.random().toString(16).slice(2, 10)}`;
const clone = <T>(value: T): T => structuredClone(value);

if (!window.localStorage.getItem("sparkclaw.language")) window.localStorage.setItem("sparkclaw.language", "zh");

// ---------------------------------------------------------------------------
// Desktop bridge

const surfaceNames = ["medicine", "medicine_text", "files", "mail_popup", "mail_settings", "mail_accounts", "mail_send", "mail_send_attachments",
  "approvals", "notifications", "settings_owner", "settings_connectors", "settings_bindings", "settings_devices", "browser"];
const disabledSurfaces = ["speech_recording", "speech_realtime", "settings_devices_delete",
  "settings_credentials", "settings_mcp_access", "settings_browser_control"];
const connection: DesktopConnectionStatus = {
  schema_version: 1,
  state: "connected",
  client_id: "client-preview-mac",
  owner_id: "owner-preview",
  authorization_revision: 1,
  backend: {
    schema_version: 3, transport: "link", client_id: "client-preview-mac", domain_id: "domain-preview",
    domain_name: "Preview", host_name: "SparkClaw preview",
    origin: GATEWAY, deployment_id: "deployment-preview", owner_id: "owner-preview",
  },
  capabilities: {
    operations: [], files: true, mail: true, browser: true, speech: false, approvals: true, settings: true, notifications: true, events: true,
    surfaces: Object.fromEntries([
      ...surfaceNames.map(name => [name, { enabled: true, reason: "" }]),
      ...disabledSurfaces.map(name => [name, { enabled: false, reason: "preview_unavailable" }]),
    ]),
  },
};

const desktopState: DesktopState = {
  schema_version: 1, capability_version: 1, runtime_kind: "electron", runtime_generation: "preview", revision: 1,
  pages: [
    { page_ref: "page_" + "1".repeat(32), role: "personal", task_id: "", title: "百度一下，你就知道", url: "https://www.baidu.com/", presented: false, loading: false, crashed: false, can_go_back: false, can_go_forward: false },
    { page_ref: "page_" + "2".repeat(32), role: "personal", task_id: "", title: "杭州西湖景区官方网站 - 游玩攻略与开放时间", url: "https://www.hzwestlake.gov.cn/", presented: false, loading: false, crashed: false, can_go_back: true, can_go_forward: false },
  ], downloads: [], permissions: [], browser_host: { state: "connected", role: "client_embedded", unknown_writes: [] },
  presentation: { layout_revision: 0, panel_bounds: { x: 0, y: 0, width: 0, height: 0 }, insufficient_space: false, presented_page_ref: "" },
};
const completed = async () => ({ completed: true as const });
const stateListeners = new Set<(state: DesktopState) => void>();
function publishState() { desktopState.revision += 1; for (const listener of stateListeners) listener(clone(desktopState)); }
let pageCounter = 3;
const desktop: SparkClawDesktop = {
  runtimeKind: "electron",
  capabilityVersion: 1,
  gatewayBase: GATEWAY,
  speechBase: GATEWAY,
  windowChrome: "inset",
  localConnection: async () => clone(connection),
  retryLocalConnection: async () => clone(connection),
  configureBackend: async () => clone(connection),
  login: async () => clone(connection),
  enroll: async () => clone(connection),
  connectionCredential: async () => "preview-connection-credential",
  checkNewAuthorization: async () => clone(connection),
  deleteAuthorization: async () => clone(connection),
  logout: async () => { console.info("[client-preview] logout requested"); return clone(connection); },
  speechStream: async () => ({}),
  cancelRecording: async () => ({}),
  transcribeRecording: async (request) => ({ id: id("speech"), request_id: request.request_id, session_id: request.session_id, text: "", duration_ms: 0, inference_ms: 0, audio_retained: false }),
  pendingRecordings: async () => [],
  acknowledgeRecording: async () => ({}),
  loginStartup: async (enabled) => ({ supported: true, enabled: enabled ?? false }),
  onBackendEvents: () => () => {},
  onLocalConnection: () => () => {},
  state: async () => clone(desktopState),
  selectConversation: async () => ({ page_ref: "" }),
  reconcileBrowserHost: async () => ({ completed: true }),
  createPersonal: async (url) => {
    const page_ref = "page_" + String(pageCounter++).repeat(32).slice(0, 32);
    desktopState.pages.push({ page_ref, role: "personal", task_id: "", title: url ? new URL(url).hostname : "", url: url ?? "about:blank", presented: false, loading: false, crashed: false, can_go_back: false, can_go_forward: false });
    publishState(); return { page_ref };
  },
  navigatePersonal: async (pageRef, url) => {
    const page = desktopState.pages.find((candidate) => candidate.page_ref === pageRef);
    if (page) Object.assign(page, { url, title: new URL(url).hostname, can_go_back: true });
    publishState(); return { completed: true as const };
  },
  personalNavigation: completed,
  closePersonal: async (pageRef) => {
    desktopState.pages = desktopState.pages.filter((page) => page.page_ref !== pageRef);
    publishState(); return { completed: true as const };
  },
  presentPersonal: completed,
  observeTask: completed,
  hideBrowser: completed,
  setBounds: completed,
  respondPermission: completed,
  cancelDownload: completed,
  showDownload: completed,
  onState: (listener) => { stateListeners.add(listener); return () => stateListeners.delete(listener); },
};
window.sparkclawDesktop = desktop;

// ---------------------------------------------------------------------------
// Local conversation store

type Thread = { conversation: LocalConversation; content: LocalConversationContent };
const threads = new Map<string, Thread>();
const drafts = new Map<string, LocalDraft>();
const fileBytes = new Map<string, Uint8Array>();
const listeners = new Set<() => void>();
const emitChange = () => listeners.forEach(listener => listener());

function addThread(title: string, updatedAgo: number, turns: Array<[LocalMessage["role"], string]>, extra: Partial<LocalConversationContent> = {}) {
  const conversationID = `conv-${threads.size + 1}`;
  const start = now - updatedAgo - turns.length * 2 * minute;
  const messages = turns.map(([role, content], index): LocalMessage => ({
    id: `${conversationID}-m${index + 1}`,
    role, content,
    ...(role === "assistant" ? { request_id: `${conversationID}-r${Math.ceil(index / 2)}` } : {}),
    created_at: new Date(start + index * 2 * minute).toISOString(),
  }));
  threads.set(conversationID, {
    conversation: { id: conversationID, title, created_at: new Date(start).toISOString(), updated_at: ago(updatedAgo) },
    content: { messages, tasks: [], files: [], ...extra },
  });
  return conversationID;
}

const longHistory: Array<[string, string]> = [
  ["帮我做一份本季度经营分析报告的框架，面向管理层，篇幅控制在 10 页以内。",
    "好的，下面是一个面向管理层、10 页以内的季度经营分析框架，重点放在“结论先行”。\n\n## 建议结构\n1. 核心结论（1 页）：收入、利润、现金流三项关键指标与目标的偏差\n2. 收入结构（2 页）：按产品线、区域、客户类型拆分\n3. 成本与费用（2 页）：固定成本、变动成本和一次性支出\n4. 经营效率（1 页）：人效、库存周转、回款周期\n5. 风险与机会（2 页）\n6. 下季度预算建议（2 页）\n\n每页建议只保留一个主要结论，图表放在结论下方作为证据。"],
  ["收入部分我们有三条产品线：企业版、团队版和个人订阅，怎么展示比较清楚？",
    "三条产品线建议用“总量 + 结构 + 趋势”三层来展示。\n\n第一层用一个堆叠柱状图展示过去四个季度的总收入，以及每条产品线的贡献；第二层用一张小表格列出本季度各产品线的收入、同比、环比和占比；第三层只针对变化最大的产品线展开说明原因。\n\n- **企业版**：通常客单价高、波动大，建议同时标注新签与续约\n- **团队版**：关注席位数增长和单席位价格\n- **个人订阅**：关注活跃订阅数和流失率\n\n这样管理层可以在 30 秒内看到“谁在增长、谁在拖后腿”。"],
  ["企业版本季度环比下降了 12%，但同比增长 18%，该怎么解释？",
    "这种情况通常说明存在季节性或大单节奏的影响，而不是业务本身在恶化。\n\n建议在报告中这样表述：上季度有两笔年度大单集中签约，抬高了环比基数；剔除这两笔后，企业版环比实际增长约 4%。同比 18% 的增长更能反映业务的真实趋势。\n\n为了让结论更可信，可以补充三项数据：\n- 剔除大单后的环比数据\n- 新签客户数与续约率\n- 在手订单（已签未确认收入）的金额\n\n如果在手订单充足，可以明确告诉管理层下季度企业版收入有支撑。"],
  ["成本拆分有什么常见的口径？财务那边给的是按科目分的。",
    "按科目拆分适合记账，但管理层更关心“钱花在哪类活动上”。常见有三种口径：\n\n## 1. 按性质\n固定成本（房租、人员基本工资、折旧）与变动成本（云资源、渠道佣金、支付手续费）。适合回答“收入下降时成本能否同步下降”。\n\n## 2. 按职能\n研发、销售、市场、客户成功、行政。适合回答“各部门投入是否与产出匹配”。\n\n## 3. 按产品线\n把可直接归属的成本分配到产品线，共享成本按收入或使用量分摊。适合计算各产品线的贡献利润。\n\n建议正文用按性质和按职能两种口径，按产品线的贡献利润放到附录，避免分摊规则引发争论。"],
  ["云资源费用涨得很快，比收入增速还高，要不要单独拿出来讲？",
    "要单独讲，而且建议放在“成本与费用”的第一页。云资源增速超过收入增速，意味着毛利率在被侵蚀，这是管理层最关心的信号之一。\n\n可以这样组织：先给出云资源费用占收入比例的四个季度趋势，再拆分增长来源，例如存储增长、计算资源增长、新上线功能带来的额外开销。\n\n最后给出可执行的优化方向：\n- 预留实例或长期合约折扣，预计可节省 15%～25%\n- 清理闲置资源和过期快照\n- 对冷数据启用低频存储\n- 为高消耗功能设置用量告警\n\n每一项最好附上预计节省金额和负责人。"],
  ["回款周期从 45 天拉长到 62 天，这个怎么写才不会显得太严重？",
    "不建议刻意淡化，但可以把问题写得“具体、可控”。\n\n先说明拉长的主要原因：是少数大客户的付款节点延后，还是整体回款变慢？如果前五大客户贡献了大部分延迟，就把它们单独列出来，并说明每一家的预计回款日期。\n\n然后给出应对措施，比如调整合同付款条款、对逾期 30 天以上的账款启动催收流程、销售提成与回款挂钩等。\n\n这样写的效果是：管理层看到的是一个已经被识别、有人负责、有时间表的问题，而不是一个失控的风险。"],
  ["风险与机会部分，你觉得应该列几条？",
    "建议风险和机会各列 3 条，总共不超过 6 条。太多会稀释重点，太少又显得思考不充分。\n\n每一条用统一的格式：\n- **描述**：一句话说明是什么\n- **影响**：可能影响的收入或成本金额区间\n- **概率**：高、中、低\n- **应对**：已采取或计划采取的措施\n\n风险可以考虑：大客户续约不确定、云资源成本继续上升、核心岗位人员流失。机会可以考虑：新行业客户试点、海外渠道合作、个人订阅提价空间。"],
  ["下季度预算建议要给几个方案吗？",
    "建议给两到三个方案，并明确推荐其中一个。\n\n## 基准方案\n按当前增长趋势延续，费用增幅控制在收入增幅以内，适合作为保底。\n\n## 进取方案\n在销售和市场上增加投入，换取更高的新签增长，需要说明额外投入的回收周期。\n\n## 稳健方案\n在不确定性较高时采用，冻结非必要招聘，优先保障现金流。\n\n每个方案列出收入预测、费用预算、利润和现金流四个数字，再用一句话说明适用条件。推荐方案放在最前面，并解释推荐理由。"],
  ["管理层比较喜欢看图，图表方面有什么建议？",
    "图表遵循“一张图只回答一个问题”的原则。\n\n- 趋势用折线图，比较用柱状图，结构用堆叠柱状图\n- 尽量不用饼图，超过三个分类时很难比较\n- 标题直接写结论，例如“企业版同比增长 18%，环比下降受大单节奏影响”\n- 突出一条关键数据线，其余使用灰色\n- 坐标轴从零开始，避免夸大波动\n\n另外，所有图表使用统一的颜色代表同一条产品线，管理层翻页时不需要重新理解图例。"],
  ["核心结论那一页你帮我起草一下，用我们刚才讨论的数据。",
    "以下是核心结论页的草稿，你可以根据实际数据替换数字：\n\n## 本季度核心结论\n1. 总收入同比增长 16%，达成季度目标的 97%；企业版环比下降主要受上季度大单基数影响，在手订单充足。\n2. 毛利率较上季度下降 2.1 个百分点，主要原因是云资源费用增速（28%）高于收入增速，已启动成本优化，预计下季度节省约 60 万元。\n3. 回款周期由 45 天延长至 62 天，集中在两家大客户，已明确回款计划。\n4. 下季度推荐采用基准方案，在保持费用增幅低于收入增幅的前提下，为企业版新行业试点预留专项预算。\n\n这一页建议只放文字和四个关键数字，不放图表。"],
  ["第二条里“预计节省约 60 万元”是怎么估算的？我需要能解释清楚。",
    "这个数字需要你按实际账单核对，这里给出估算思路，方便你向管理层解释：\n\n假设本季度云资源费用为 300 万元，其中计算资源约 180 万元、存储约 90 万元、其他约 30 万元。\n\n- 计算资源中可转为预留实例的部分约 60%，按 20% 折扣计算，节省约 21.6 万元\n- 清理闲置资源和过期快照，按存储费用的 15% 计算，节省约 13.5 万元\n- 冷数据转低频存储，按剩余存储费用的 30% 计算，节省约 23 万元\n\n合计约 58 万元，取整写作“约 60 万元”。建议在附录中列出这张估算表，并注明假设条件。"],
  ["好的。最后帮我检查一下整份报告还缺什么。",
    "对照常见的管理层报告要求，目前还可以补充以下几项：\n\n- **数据口径说明**：收入确认口径、汇率、是否含税，放在附录第一页\n- **上季度行动项回顾**：上次会议定下的事项完成情况，体现执行闭环\n- **关键假设**：预算方案依赖的增长率、价格和人员假设\n- **需要管理层决策的事项**：单独列一页，明确请管理层拍板的问题\n\n其中“需要管理层决策的事项”最容易被忽略，但往往是会议真正的产出。建议放在核心结论之后、正文之前。"],
  ["需要决策的事项我想列三个：新行业试点预算、云资源合约、个人订阅提价。帮我写成一页。",
    "## 需要管理层决策的事项\n\n1. **新行业试点预算**：申请 80 万元专项预算，用于两个新行业的试点客户交付。预计 6 个月内形成可复制方案，若试点成功，明年可带来约 500 万元新签。请决定是否批准。\n2. **云资源三年期合约**：签订三年期预留合约可在当前价格基础上获得约 22% 折扣，但需要承诺最低用量。请决定承诺用量的上限。\n3. **个人订阅提价**：建议对新用户提价 10%，老用户保持原价 12 个月。预计年化收入增加约 120 万元，流失率可能上升 1～2 个百分点。请决定是否执行以及执行时间。\n\n每一项都附上“如果不决策的后果”，可以帮助会议更快达成结论。"],
];

const longID = addThread("季度经营分析报告：收入结构、成本拆分、回款周期与下季度预算建议（管理层版）", 3 * minute,
  longHistory.flatMap(([question, answer]) => [["user", question], ["assistant", answer]] as Array<[LocalMessage["role"], string]>));
addThread("帮我整理周五产品评审会的会议纪要", 25 * minute, [
  ["user", "这是周五产品评审会的录音整理稿，帮我整理成会议纪要，重点是结论和待办。"],
  ["assistant", "## 会议纪要：产品评审会\n\n**结论**\n- 新版搜索功能按计划在 11 月 15 日灰度上线\n- 移动端首页改版推迟一个迭代，等待用户研究结果\n\n**待办**\n- 王磊：11 月 8 日前完成搜索灰度方案\n- 刘晓雨：整理用户访谈结论，下周三同步\n- 张伟：确认埋点需求并提交数据组"],
]);
addThread("上海到杭州周末两日游行程规划", 2 * hour, [
  ["user", "这周末从上海去杭州玩两天，两个大人一个 6 岁小孩，帮我规划一下行程。"],
  ["assistant", "## 第一天\n- 上午：高铁到杭州东站，约 1 小时\n- 中午：湖滨银泰附近午餐\n- 下午：西湖游船 + 断桥白堤散步，适合小朋友\n- 晚上：入住西湖附近酒店\n\n## 第二天\n- 上午：杭州动物园或浙江自然博物院\n- 下午：河坊街吃小吃，傍晚返程\n\n建议提前预约游船和博物院门票。"],
  ["user", "如果下雨的话有什么备选吗？"],
  ["assistant", "下雨可以考虑这些室内项目：\n- 浙江自然博物院（需预约，孩子会很喜欢恐龙厅）\n- 中国丝绸博物馆，免费\n- 杭州图书馆少儿馆\n\n西湖雨景其实也很美，带好雨具可以坐一段游船。"],
]);
addThread("合同条款审阅：第 7.3 条违约责任与第 9 条保密义务是否对我方不利", 5 * hour, [
  ["user", "附件是供应商发来的采购合同第三版，帮我看看第 7.3 条和第 9 条有没有对我方不利的地方。"],
  ["assistant", "我重点看了这两条：\n\n**第 7.3 条 违约责任**：违约金按合同总额的 30% 计算，且只约束我方逾期付款，没有约束供应商逾期交付，责任明显不对等。建议改为双方对等，并设置上限。\n\n**第 9 条 保密义务**：保密期限为“永久”，且未排除公开信息和独立开发的信息。建议改为合同终止后 3 年，并补充常见的除外条款。\n\n以上仅供参考，正式签署前建议请法务确认。"],
], { files: [{ id: "file-contract", name: "采购合同-v3.pdf", size: 482_133, sha256: "0".repeat(64), created_at: ago(5 * hour + 10 * minute), content_type: "application/pdf" }] });
addThread("Python 脚本批量重命名照片", day + 3 * hour, [
  ["user", "我有几千张照片，想按拍摄日期重命名成 2026-10-01_001.jpg 这种格式，用 Python 怎么写？"],
  ["assistant", "可以用 `Pillow` 读取 EXIF 中的拍摄时间：\n\n1. 安装依赖：`pip install pillow`\n2. 遍历目录，读取每张照片的 `DateTimeOriginal`\n3. 按日期分组并编号，再执行重命名\n\n建议先用“只打印不重命名”的模式运行一遍，确认结果没问题后再正式执行。"],
]);
const replyDraftID = addThread("给客户回复延期交付的邮件草稿", day + 7 * hour, [
  ["user", "客户的二期项目要延期两周交付，帮我写一封说明邮件，语气诚恳但不要太卑微。"],
  ["assistant", "以下是邮件草稿：\n\n尊敬的李总：\n\n感谢您一直以来对项目的支持。由于二期新增的数据迁移需求在测试中发现兼容性问题，为确保上线质量，我们计划将交付时间调整至 11 月 19 日。\n\n我们已增加两名工程师专项处理，并会每周向您同步进展。如有任何问题，欢迎随时联系我。\n\n此致\n张伟"],
]);
addThread("孩子英语启蒙绘本推荐", 3 * day, [
  ["user", "6 岁孩子英语启蒙，推荐一些适合亲子共读的绘本。"],
  ["assistant", "推荐几套经典的入门绘本：\n- 《Brown Bear, Brown Bear, What Do You See?》\n- 《Go Away, Big Green Monster!》\n- 《We're Going on a Bear Hunt》\n- 《Biscuit》系列\n\n建议每天固定 15 分钟共读，不必逐字翻译，让孩子先熟悉声音和节奏。"],
]);
addThread("家庭理财：每月定投基金组合怎么配置比较稳妥", 5 * day, [
  ["user", "每月能拿出 5000 元定投，风险偏好中等，怎么配置比较稳妥？"],
  ["assistant", "一个常见的中等风险配置思路是：\n- 宽基指数基金 50%\n- 债券基金 30%\n- 行业或海外指数 20%\n\n定投重在长期坚持，建议设定至少 3 年的投资期限，并预留 6 个月的生活费作为应急资金。以上不构成投资建议。"],
]);
addThread("新员工入职培训手册目录", 7 * day + 2 * hour, [
  ["user", "帮我列一份新员工入职培训手册的目录。"],
  ["assistant", "## 入职培训手册\n1. 公司介绍与文化\n2. 组织架构与联系人\n3. 办公工具与账号开通\n4. 考勤、请假与报销制度\n5. 信息安全规范\n6. 岗位入门任务清单\n7. 常见问题"],
]);
addThread("React 19 迁移注意事项", 9 * day, [
  ["user", "项目要从 React 18 升级到 19，有哪些需要注意的？"],
  ["assistant", "主要关注以下几点：\n- 移除已废弃的 API，例如 `ReactDOM.render`\n- `ref` 可以作为普通 prop 传递，`forwardRef` 逐步不再需要\n- 检查第三方库的兼容版本\n- 在 StrictMode 下回归测试副作用逻辑"],
]);
addThread("健身计划（减脂期，每周四练）", 14 * day, [
  ["user", "减脂期，每周能练四次，每次一小时，帮我排一个计划。"],
  ["assistant", "## 每周计划\n- 周一：上肢力量 + 20 分钟有氧\n- 周三：下肢力量 + 核心\n- 周五：全身循环训练\n- 周六：45 分钟中低强度有氧\n\n饮食上保持每天约 300～500 千卡的热量缺口，蛋白质按每公斤体重 1.6 克摄入。"],
]);
addThread("年会节目创意征集与串词", 21 * day, [
  ["user", "部门年会要出一个节目，十个人左右，有什么创意？"],
  ["assistant", "几个参与门槛低、效果好的方向：\n- 情景短剧：还原一年中的“名场面”\n- 歌曲串烧改编歌词\n- 默契大挑战互动游戏\n\n串词可以围绕“这一年我们一起解决的问题”展开，更有共鸣。"],
]);
addThread("读书笔记：《置身事内》第三章", 35 * day, [
  ["user", "帮我总结《置身事内》第三章的主要观点。"],
  ["assistant", "第三章主要讨论地方政府的财政收支和土地财政：\n- 分税制改革后，地方财权与事权不匹配\n- 土地出让收入成为地方重要的收入来源\n- 城投公司在基础设施融资中扮演关键角色\n\n作者认为理解这些机制，是理解中国经济发展模式的关键。"],
]);
addThread("装修预算表", 62 * day, [
  ["user", "90 平米两室一厅半包装修，预算大概多少？"],
  ["assistant", "以二线城市为例，半包装修大约 8～12 万元，主材另计。建议预留 10% 的预算应对增项。"],
]);

drafts.set(replyDraftID, { scope_key: SCOPE, content: "再帮我把语气改得更正式一些，并补充一句致歉。", local_file_ids: [], revision: 1 });

function draftOf(conversationID: string): LocalDraft {
  return clone(drafts.get(conversationID) ?? { scope_key: SCOPE, content: "", local_file_ids: [], revision: 0 });
}
function threadOf(conversationID: string) {
  const thread = threads.get(conversationID);
  if (!thread) throw new Error(`Unknown preview conversation ${conversationID}`);
  return thread;
}
function touch(thread: Thread) { thread.conversation.updated_at = new Date().toISOString(); }
function findTask(requestID: string) {
  for (const thread of threads.values()) {
    const task = thread.content.tasks.find(item => item.request_id === requestID);
    if (task) return { thread, task };
  }
  throw new Error(`Unknown preview request ${requestID}`);
}
function enqueueTask(conversationID: string, content: string, localFileIDs: string[] = []): LocalTask {
  const thread = threadOf(conversationID);
  const requestID = id("req");
  const task: LocalTask = { id: id("task"), request_id: requestID, status: "awaiting_runtime", created_at: new Date().toISOString(), revision: 1 };
  const attachments = localFileIDs.map(fileID => thread.content.files.find(file => file.id === fileID)).filter((file): file is LocalFile => Boolean(file))
    .map(file => ({ artifact_id: file.id, rel_path: `local:${file.id}/${file.name}`, name: file.name, bytes: file.size, content_type: file.content_type }));
  thread.content.messages.push({ id: id("msg"), request_id: requestID, role: "user", content, created_at: task.created_at, ...(attachments.length ? { attachments } : {}) });
  thread.content.tasks.push(task);
  if (thread.conversation.title === "新对话" || thread.conversation.title === "New conversation") thread.conversation.title = content.slice(0, 24) || thread.conversation.title;
  touch(thread);
  return clone(task);
}

const schedules: Schedule[] = [
  { id: "sched-daily-mail", title: "每天早上汇总未读邮件", text: "每天早上 8 点汇总昨晚到今早的未读邮件，按紧急程度排序发给我。", due_time: at(1, 8), timezone: "Asia/Shanghai", recurrence: "daily", status: "pending", updated_at: ago(3 * day), editable: true, cancelable: true, endpoint: { kind: "web", software_display_name: "SparkX", conversation_label: "本机", status: "active" } },
  { id: "sched-weekly-report", title: "提醒提交周报", text: "每周一上午 9:30 提醒我提交上周周报，并附上本周日程。", due_time: at(nextMonday, 9, 30), timezone: "Asia/Shanghai", recurrence: "weekly", status: "pending", updated_at: ago(10 * day), editable: true, cancelable: true, endpoint: { kind: "third_party_device", channel: "weixin", software_display_name: "微信", account_display_name: "张伟", recipient_display_name: "文件传输助手", status: "active" } },
  { id: "sched-review-meeting", title: "季度复盘会提醒", text: "下午 3 点提醒我参加季度复盘会，提前准备经营分析报告。", due_time: at(6, 15), timezone: "Asia/Shanghai", status: "pending", updated_at: ago(2 * hour), editable: true, cancelable: true, endpoint: { kind: "web", software_display_name: "SparkX", conversation_label: "本机", status: "active" } },
  { id: "sched-monthly-bill", title: "生成上月账单汇总", text: "每月 1 号整理上月信用卡和水电燃气账单，生成汇总表。", due_time: at(1 - new Date(now).getDate(), 9), timezone: "Asia/Shanghai", recurrence: "monthly", status: "missed", missed_count: 2, updated_at: ago(day), editable: true, cancelable: true, endpoint: { kind: "web", software_display_name: "SparkX", conversation_label: "本机", status: "active" } },
  { id: "sched-cake", title: "给妈妈订生日蛋糕", text: "提醒我给妈妈订生日蛋糕，周六中午前送到。", due_time: at(-2, 12), timezone: "Asia/Shanghai", status: "submitted", local_state: "delivered", updated_at: ago(2 * day), editable: false, cancelable: false, endpoint: { kind: "third_party_device", channel: "weixin", software_display_name: "微信", account_display_name: "张伟", status: "unavailable" } },
];
function localSchedule(schedule: Schedule): LocalSchedule {
  return { request_id: schedule.id, schedule_id: schedule.id, due_at: schedule.due_time, state: schedule.status, interval_ms: 0,
    definition_state: "active", missed_count: schedule.missed_count ?? 0 };
}
function scheduleOf(requestID: string) {
  const schedule = schedules.find(item => item.id === requestID);
  if (!schedule) throw new Error(`Unknown preview schedule ${requestID}`);
  return schedule;
}

const store: ClientStoreAPI = {
  schemaVersion: 1,
  list: async () => [...threads.values()].map(thread => clone(thread.conversation)).sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
  listFiles: async () => [...threads.values()].flatMap(thread => clone(thread.content.files)),
  create: async (title) => {
    const conversationID = id("conv");
    const created = new Date().toISOString();
    threads.set(conversationID, { conversation: { id: conversationID, title, created_at: created, updated_at: created }, content: { messages: [], tasks: [], files: [] } });
    emitChange();
    return clone(threadOf(conversationID).conversation);
  },
  remove: async (conversationID) => {
    const thread = threadOf(conversationID);
    if (thread.content.tasks.some(task => ["awaiting_runtime", "running", "accepted", "submission_pending"].includes(task.status))) return { deleted: false, reason: "pending_execution" };
    threads.delete(conversationID); drafts.delete(conversationID); emitChange();
    return { deleted: true };
  },
  rename: async (conversationID, title) => {
    const thread = threadOf(conversationID);
    thread.conversation.title = title; touch(thread); emitChange();
    return clone(thread.conversation);
  },
  read: async (conversationID) => clone(threadOf(conversationID).content),
  draft: async (conversationID) => draftOf(conversationID),
  saveDraft: async (conversationID, content, localFileIDs, revision) => {
    if (draftOf(conversationID).revision !== revision) throw new Error("Draft revision conflict");
    const value = { scope_key: SCOPE, content, local_file_ids: localFileIDs, revision: revision + 1 };
    drafts.set(conversationID, value);
    return clone(value);
  },
  recordingApplied: async () => true,
  moveWelcomeDraft: async (conversationID, revision) => {
    const welcome = draftOf("");
    if (welcome.revision !== revision) throw new Error("Draft revision conflict");
    const source = { scope_key: SCOPE, content: "", local_file_ids: [], revision: revision + 1 };
    const draft = { ...welcome, revision: 1 };
    drafts.set("", source); drafts.set(conversationID, draft);
    return { source: clone(source), draft: clone(draft) };
  },
  enqueueDraft: async (conversationID, draftID, revision) => {
    const value = draftOf(draftID);
    if (value.revision !== revision) throw new Error("Draft revision conflict");
    const task = enqueueTask(conversationID, value.content.trim(), value.local_file_ids);
    const draft = { scope_key: SCOPE, content: "", local_file_ids: [], revision: revision + 1 };
    drafts.set(draftID, draft); emitChange();
    return { task, draft: clone(draft) };
  },
  enqueue: async (conversationID, content, localFileIDs) => { const task = enqueueTask(conversationID, content, localFileIDs); emitChange(); return task; },
  // A submitted request runs briefly, then a canned answer is delivered so the
  // progress row and the completed reply can both be inspected.
  submit: async (requestID) => {
    const { thread, task } = findTask(requestID);
    task.status = "running"; task.progress_content = ""; emitChange();
    setTimeout(() => {
      task.status = "delivered"; delete task.progress_content;
      thread.content.messages.push({ id: id("msg"), request_id: requestID, role: "assistant", created_at: new Date().toISOString(),
        content: "这是预览环境的模拟回复，没有连接真实后端。\n\n- 消息已保存在本页面的内存中\n- 刷新页面后会恢复为初始示例数据" });
      touch(thread); emitChange();
    }, 2200);
    return clone(task);
  },
  reconcile: async (requestID) => clone(findTask(requestID).task),
  cancel: async (requestID) => { const { task } = findTask(requestID); task.status = "canceled"; emitChange(); return clone(task); },
  decideApproval: async () => ({ resolved: true }),
  scheduleCreate: async (conversationID, content, dueAt) => {
    const schedule: Schedule = { id: id("sched"), session_id: conversationID, title: content.slice(0, 20), text: content, due_time: dueAt, timezone: "Asia/Shanghai", status: "pending", updated_at: new Date().toISOString(), editable: true, cancelable: true, endpoint: { kind: "web", software_display_name: "SparkX", status: "active" } };
    schedules.unshift(schedule); emitChange();
    return localSchedule(schedule);
  },
  listSchedules: async () => clone(schedules),
  createScheduleRequest: async (content, timezone) => {
    await wait(400);
    const schedule: Schedule = { id: id("sched"), title: content.slice(0, 20), text: content, due_time: later(day), timezone, status: "pending", updated_at: new Date().toISOString(), editable: true, cancelable: true, endpoint: { kind: "web", software_display_name: "SparkX", conversation_label: "本机", status: "active" } };
    schedules.unshift(schedule); emitChange();
    return localSchedule(schedule);
  },
  editSchedule: async (requestID, _expectedVersion, draft) => {
    const schedule = scheduleOf(requestID);
    Object.assign(schedule, { text: draft.text, title: draft.text.slice(0, 20), timezone: draft.timezone,
      recurrence: draft.recurrence === "none" ? undefined : draft.recurrence, due_time: new Date(draft.dueTime).toISOString(), updated_at: new Date().toISOString() });
    emitChange();
    return localSchedule(schedule);
  },
  scheduleCheck: async (requestID) => localSchedule(scheduleOf(requestID)),
  scheduleCancel: async (requestID) => {
    const schedule = scheduleOf(requestID);
    schedules.splice(schedules.indexOf(schedule), 1); emitChange();
    return { ...localSchedule(schedule), definition_state: "canceled" };
  },
  scheduleRunNow: async (requestID) => {
    const schedule = scheduleOf(requestID);
    Object.assign(schedule, { status: "submitted", local_state: "running", missed_count: 0, updated_at: new Date().toISOString() });
    emitChange();
    return localSchedule(schedule);
  },
  saveFile: async (conversationID, name, bytes) => {
    const thread = threadOf(conversationID);
    const file: LocalFile = { id: id("file"), name, size: bytes.byteLength, sha256: "0".repeat(64), created_at: new Date().toISOString(), content_type: "application/octet-stream" };
    thread.content.files.push(file); fileBytes.set(file.id, bytes); emitChange();
    return clone(file);
  },
  exportFile: async (fileID) => { console.info("[client-preview] export file", fileID); return { saved: true }; },
  readFile: async (fileID) => {
    const file = [...threads.values()].flatMap(thread => thread.content.files).find(item => item.id === fileID);
    return { name: file?.name ?? fileID, content_type: file?.content_type ?? "application/octet-stream", bytes: fileBytes.get(fileID) ?? new Uint8Array() };
  },
  onChange: (listener) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
};
window.sparkclawClientStore = store;

// ---------------------------------------------------------------------------
// Mail fixtures (one connected QQ mailbox)

const MAILBOX = "mbx-qq-zhangwei";
const OWNER_ADDRESS = "zhangwei@qq.com";
const mailbox = {
  id: MAILBOX, version: 3, provider: "qq_mail" as const, account_id: "default", address: OWNER_ADDRESS,
  intake_enabled: true, active_binding: true, state: "active", provider_mode: "time_range" as const,
  last_sync_at: ago(4 * minute), coverage_start: ago(90 * day), coverage_end: ago(4 * minute), poll_through: ago(4 * minute),
  refresh_available: true, refresh_pending: false, refresh_request_id: "", backlog: 0, unacknowledged_warning_count: 0,
};
const provider = { provider: "qq_mail" as const, display_name: "QQ 邮箱", enabled: true, default: true, account: OWNER_ADDRESS, account_id: "default",
  multi_account_available: false, state: "ready" as const, last_checked_at: ago(4 * minute), version: 2, updated_at: ago(day) };

type MailSeed = { from: string; to?: string[]; cc?: string[]; subject: string; body: string; arrived: number; outbound?: boolean; viewed?: boolean; summary: string; attachments?: EmailMessage["attachments"] };
type ConversationSeed = { id: string; entry: "interaction" | "notification"; title: string; participants: string[]; summary: string; mails: MailSeed[] };
const conversationSeeds: ConversationSeed[] = [
  { id: "ec-procurement", entry: "interaction", title: "办公设备采购报价确认（第二轮）", participants: ["li.na@huaxin-tech.com", OWNER_ADDRESS],
    summary: "华信科技针对 40 台笔记本和 12 台显示器给出第二轮报价，等待我方确认交付时间。", mails: [
      { from: "li.na@huaxin-tech.com", subject: "办公设备采购报价（第二轮）", arrived: 2 * day, viewed: true, summary: "供应商提交第二轮报价，总价下调 4.5%。",
        body: "张经理您好：\n\n根据上周的沟通，我们对报价做了调整，40 台笔记本和 12 台显示器的总价由 35.6 万元下调至 34.0 万元，含三年上门保修。\n\n附件是详细报价单，请您查阅。如无问题，我们可以在合同签署后 10 个工作日内完成交付。\n\n李娜\n华信科技 销售部", attachments: [{ id: "att-quote", name: "报价单-第二轮.xlsx", size: 48_120, available: true }] },
      { from: OWNER_ADDRESS, to: ["li.na@huaxin-tech.com"], outbound: true, subject: "Re: 办公设备采购报价（第二轮）", arrived: day + 2 * hour, viewed: true, summary: "我方确认价格，询问能否提前到 11 月 10 日前交付。",
        body: "李娜您好：\n\n报价已收到，价格方面我们可以接受。想确认一下，交付时间能否提前到 11 月 10 日之前？我们新办公室 11 月 12 日启用。\n\n张伟" },
      { from: "li.na@huaxin-tech.com", subject: "Re: Re: 办公设备采购报价（第二轮）", arrived: 3 * hour, viewed: false, summary: "供应商确认可在 11 月 8 日前分两批交付，需尽快签署合同。",
        body: "张经理您好：\n\n我们和仓库确认过了，可以在 11 月 8 日前分两批完成交付：第一批 25 台笔记本于 11 月 5 日送达，其余设备于 11 月 8 日送达。\n\n为保证排期，麻烦在本周五前完成合同签署。合同文本我稍后单独发给您。\n\n谢谢！\n李娜" },
    ] },
  { id: "ec-kickoff", entry: "interaction", title: "下周三项目启动会时间协调", participants: ["wang.lei@sparkcorp.cn", "liu.xiaoyu@sparkcorp.cn"],
    summary: "王磊提议下周三下午两点召开二期启动会，需要确认会议室和参会人。", mails: [
      { from: "wang.lei@sparkcorp.cn", cc: ["liu.xiaoyu@sparkcorp.cn"], subject: "二期项目启动会时间", arrived: 6 * hour, viewed: true, summary: "提议下周三 14:00 开启动会。",
        body: "张伟：\n\n二期项目准备得差不多了，我提议下周三下午 2 点开启动会，时长 1.5 小时，地点 5 楼大会议室。\n\n需要你帮忙确认客户那边的参会人员名单。\n\n王磊" },
      { from: OWNER_ADDRESS, to: ["wang.lei@sparkcorp.cn"], cc: ["liu.xiaoyu@sparkcorp.cn"], outbound: true, subject: "Re: 二期项目启动会时间", arrived: 5 * hour, viewed: true, summary: "确认时间，客户方 3 人参会。",
        body: "王磊：\n\n时间没问题。客户方参会的是李总、技术负责人陈工和项目经理周敏，共 3 人。会议室我来预订。\n\n张伟" },
    ] },
  { id: "ec-resume", entry: "interaction", title: "简历投递：高级前端工程师 - 陈思远", participants: ["chensiyuan@163.com"],
    summary: "候选人陈思远投递高级前端工程师岗位，6 年 React 经验。", mails: [
      { from: "chensiyuan@163.com", subject: "应聘高级前端工程师 - 陈思远", arrived: day + 5 * hour, viewed: false, summary: "候选人投递简历，期望下周面试。",
        body: "您好：\n\n我在招聘网站上看到贵公司高级前端工程师的职位，非常感兴趣。我有 6 年前端开发经验，主要使用 React 和 TypeScript，曾负责过日活百万级产品的性能优化。\n\n简历见附件，期待您的回复。\n\n陈思远\n138-0000-0000", attachments: [{ id: "att-resume", name: "陈思远-简历.pdf", size: 312_400, available: true }] },
    ] },
  { id: "ec-lease", entry: "interaction", title: "房屋租赁合同续签", participants: ["landlord.zhou@126.com"],
    summary: "房东周先生询问是否续租，租金拟上调 3%。", mails: [
      { from: "landlord.zhou@126.com", subject: "租房合同到期续签的事", arrived: 4 * day, viewed: true, summary: "房东询问续租意向。",
        body: "小张你好：\n\n房子的租赁合同 12 月底到期，想问问你是否打算续租。今年周边租金涨了一些，我这边打算上调 3%，其他条件不变。\n\n方便的话这周回复我一下。\n\n周先生" },
      { from: OWNER_ADDRESS, to: ["landlord.zhou@126.com"], outbound: true, subject: "Re: 租房合同到期续签的事", arrived: 3 * day, viewed: true, summary: "确认续租，希望签两年。",
        body: "周先生您好：\n\n我们打算续租，上调 3% 可以接受。如果可以的话，希望这次直接签两年，第二年租金保持不变。\n\n张伟" },
    ] },
  { id: "ec-bill", entry: "notification", title: "招商银行信用卡 10 月账单", participants: ["ccsvc@message.cmbchina.com"],
    summary: "10 月账单金额 8,326.50 元，到期还款日 11 月 18 日。", mails: [
      { from: "ccsvc@message.cmbchina.com", subject: "招商银行信用卡电子账单（2026 年 10 月）", arrived: 9 * hour, viewed: false, summary: "本期应还 8,326.50 元，最低还款 832.65 元。",
        body: "尊敬的张伟先生：\n\n您的 10 月信用卡账单已出，本期应还金额 8,326.50 元，最低还款额 832.65 元，到期还款日为 2026 年 11 月 18 日。\n\n请按时还款，以免影响您的信用记录。" },
    ] },
  { id: "ec-shipping", entry: "notification", title: "京东订单已发货", participants: ["jd@jd.com"],
    summary: "订单中的儿童绘本套装已发货，预计明天送达。", mails: [
      { from: "jd@jd.com", subject: "您的订单已发货", arrived: 20 * hour, viewed: true, summary: "绘本套装已发货。",
        body: "亲爱的用户：\n\n您购买的《英语启蒙绘本套装（20 册）》已发货，承运方为京东物流，预计明天 18:00 前送达。" },
    ] },
];

const mails = new Map<string, EmailMessage>();
const conversations = new Map<string, EmailConversation>();
let mailSequence = 0;
function buildMail(seed: MailSeed, conversationID: string | undefined, entry: "interaction" | "notification"): EmailMessage {
  const mailID = `mail-${++mailSequence}`;
  const sender = seed.outbound ? OWNER_ADDRESS : seed.from;
  return {
    id: mailID, version: 1, mailbox_id: MAILBOX, receiving_address: OWNER_ADDRESS, direction: seed.outbound ? "outbound" : "inbound",
    from: seed.from, to: seed.to ?? [OWNER_ADDRESS], cc: seed.cc ?? [], subject: seed.subject, sent_at: ago(seed.arrived), arrived_at: ago(seed.arrived),
    summary: seed.summary, summary_state: "ready", body_text: seed.body, body_available: true, body_revision: "1", viewed: seed.viewed ?? true,
    original_available: true, attachments: seed.attachments ?? [], conversation_id: conversationID, processing_state: "ready", history_state: "complete",
    current_sender_address: sender, current_sender_rule_revision: 0, classification_state: "ready", assignment_state: conversationID ? "assigned" : "unassigned",
    classification: { category: entry, effective_entry: entry, source: "model", state: "ready", revision: 1, reason_code: entry === "notification" ? "subject_evidence" : "body_evidence", sender_address: sender,
      ...(entry === "notification" ? { notification_subtype: "general" } : {}) },
  };
}
for (const seed of conversationSeeds) {
  const members = seed.mails.map(mail => buildMail(mail, seed.id, seed.entry));
  members.forEach(mail => mails.set(mail.id, mail));
  conversations.set(seed.id, {
    id: seed.id, version: 1, title: seed.title, title_state: "ready", participants: seed.participants, summary: seed.summary, summary_state: "ready",
    unseen_count: members.filter(mail => !mail.viewed).length, last_activity_at: members.at(-1)!.arrived_at, concerns: [],
    effective_entry: seed.entry, member_count: members.length, processing_state: "ready",
  });
}
const looseInteraction = buildMail({ from: "xiaoyu.liu@outlook.com", subject: "周末聚餐地点", arrived: 50 * minute, viewed: false, summary: "刘晓雨推荐了两家餐厅，请选择。",
  body: "张伟：\n\n周六部门聚餐我找了两家店：一家是静安寺附近的本帮菜，人均 150；另一家是徐汇的火锅，人均 120。你看哪家合适？\n\n晓雨" }, undefined, "interaction");
const looseNotification = buildMail({ from: "noreply@github.com", subject: "[GitHub] A new SSH key was added to your account", arrived: 2 * day + 3 * hour, viewed: true, summary: "账户新增了一个 SSH 公钥。",
  body: "The following SSH key was added to your account:\n\nMacBook Pro (work)\n\nIf you believe this key was added in error, you can remove it from your account settings." }, undefined, "notification");
const pendingMail = buildMail({ from: "invoice@fapiao-service.cn", subject: "发票开具申请待确认", arrived: 30 * minute, viewed: false, summary: "",
  body: "您好，您提交的发票开具申请（抬头：上海星火科技有限公司）待确认，请核对开票信息。" }, undefined, "interaction");
pendingMail.classification_state = "pending"; pendingMail.processing_state = "analyzing"; delete pendingMail.classification;
for (const mail of [looseInteraction, looseNotification, pendingMail]) mails.set(mail.id, mail);
let mailVersion = 1;

const emailDrafts: EmailDraft[] = [
  { id: "draft-lease", version: 1, mailbox_id: MAILBOX, mode: "compose", to: ["landlord.zhou@126.com"], cc: [], subject: "续租合同签署时间", body: "周先生您好：\n\n续租合同我们这周六上午签可以吗？", state: "draft" },
];

function renderNodes(body: string): EmailRenderNode[] {
  return body.split(/\n{2,}/).map(paragraph => ({ kind: "paragraph", children: paragraph.split("\n").flatMap((line, index) =>
    index ? [{ kind: "line_break" as const }, { kind: "text" as const, text: line }] : [{ kind: "text" as const, text: line }]) }));
}
function matches(values: Array<string | undefined>, query: string) {
  return !query || values.some(value => value?.toLocaleLowerCase().includes(query.toLocaleLowerCase()));
}
function mailPage(rows: EmailMessage[]) {
  return { version: mailVersion, messages: clone(rows), counts: { total: rows.length, unseen: rows.filter(mail => !mail.viewed).length }, server_now: new Date().toISOString() };
}
function presentation(kind: "mail" | "conversation", targetID: string, language: "en" | "zh") {
  const mail = mails.get(targetID), conversation = conversations.get(targetID);
  return { target_kind: kind, target_id: targetID, presentation_language: language, state: "ready" as const, analysis_revision: "1", revision: 1,
    title: conversation?.title ?? mail?.subject, summary: conversation?.summary ?? mail?.summary, purpose: "", concern_explanations: {} };
}
function saveDraft(body: Partial<EmailDraft> & { expected_version?: number }, draftID?: string): EmailDraft {
  const existing = emailDrafts.find(draft => draft.id === (draftID ?? body.id));
  const draft: EmailDraft = { ...(existing ?? { id: draftID ?? body.id ?? id("draft"), version: 0, mailbox_id: MAILBOX, mode: "compose", to: [], cc: [], subject: "", body: "", state: "draft" as const }),
    ...Object.fromEntries(Object.entries(body).filter(([key]) => ["mailbox_id", "mode", "reply_mail_id", "to", "cc", "subject", "body"].includes(key))) };
  draft.version = (existing?.version ?? 0) + 1;
  draft.mailbox_id ||= MAILBOX;
  if (!existing) emailDrafts.unshift(draft);
  else Object.assign(existing, draft);
  return clone(draft);
}

const mailSync: MailCapability = {
  catalog: async () => [{ id: MAILBOX, address: OWNER_ADDRESS, provider: "qq_mail", intake_enabled: true }],
  refreshCatalog: async () => [{ id: MAILBOX, address: OWNER_ADDRESS, provider: "qq_mail", intake_enabled: true }],
  read: async () => ({ mailbox_id: MAILBOX, sequence: 1, synced_at: ago(4 * minute), messages: [] }),
  sync: async () => ({ mailbox_id: MAILBOX, sequence: 2, synced_at: new Date().toISOString(), messages: [] }),
  saveAttachment: async (_mailbox, mailID, partID, conversationID) => {
    const attachment = mails.get(mailID)?.attachments.find(item => item.id === partID);
    return store.saveFile(conversationID, attachment?.name ?? "original.eml", new TextEncoder().encode("preview attachment"));
  },
};
window.sparkclawMailSync = mailSync;

// ---------------------------------------------------------------------------
// Medicine box: in-memory stand-in for the main-process store. Photo
// recognition is stubbed in the gateway routes below.

const expires = (days: number) => at(days, 12).slice(0, 10);
const medicine = (fields: Partial<MedicineItem> & Pick<MedicineItem, "name" | "category" | "quantity" | "unit">, created: number): MedicineItem => ({
  id: id("med"), expires_on: "", no_expiry: false, location: "", usage: "", notes: "", created_at: ago(created * day), updated_at: ago(created * day), ...fields });
// Every category and status: expired, expires today, expiring, in date, no
// expiry, no expiry date and used up; one same-name pair and one in millilitres.
const medicines: MedicineItem[] = [
  medicine({ name: "布洛芬缓释胶囊", category: "fever", quantity: 14, unit: "capsule", expires_on: expires(420), location: "客厅抽屉", usage: "退烧、头痛、牙痛",
    notes: "规格：0.3g×20粒\n生产企业：中美天津史克制药有限公司" }, 20),
  medicine({ name: "布洛芬缓释胶囊", category: "fever", quantity: 6, unit: "capsule", expires_on: expires(24), location: "卧室床头柜", usage: "退烧、头痛、牙痛" }, 3),
  medicine({ name: "999感冒灵颗粒", category: "cold", quantity: 4, unit: "sachet", expires_on: expires(0), location: "客厅抽屉", usage: "感冒引起的头痛发热、鼻塞流涕" }, 40),
  medicine({ name: "蒙脱石散", category: "digestive", quantity: 7, unit: "sachet", expires_on: expires(-12), location: "厨房药盒", usage: "腹泻" }, 200),
  medicine({ name: "复方甘草口服溶液", category: "cough", quantity: 85, unit: "ml", expires_on: expires(150), location: "冰箱侧门", usage: "镇咳祛痰" }, 15),
  medicine({ name: "氯雷他定片", category: "allergy", quantity: 0, unit: "tablet", expires_on: expires(300), location: "卧室床头柜" }, 60),
  medicine({ name: "云南白药创可贴", category: "wound", quantity: 30, unit: "patch", no_expiry: true, location: "玄关收纳盒", usage: "小伤口止血" }, 8),
  medicine({ name: "碘伏消毒液", category: "wound", quantity: 1, unit: "bottle", expires_on: expires(9) }, 90),
  medicine({ name: "双氯芬酸二乙胺乳胶剂", category: "pain", quantity: 1, unit: "tube", expires_on: expires(640), location: "客厅抽屉", usage: "关节肌肉疼痛" }, 30),
  medicine({ name: "维生素C片", category: "other", quantity: 48, unit: "tablet", notes: "旧记录，还没填有效期" }, 120),
  medicine({ name: "小儿氨酚黄那敏颗粒", category: "cold", quantity: 2.5, unit: "sachet", expires_on: expires(-40), usage: "儿童感冒" }, 300),
  medicine({ name: "藿香正气水", category: "digestive", quantity: 5, unit: "bottle", expires_on: expires(31), location: "厨房药盒" }, 1),
];
const medicineBoxMock: MedicineBoxAPI = {
  list: async () => ({ items: clone(medicines) }),
  save: async (fields) => {
    await wait(150);
    const existing = fields.id ? medicines.find(candidate => candidate.id === fields.id) : undefined;
    if (existing) { Object.assign(existing, fields, { updated_at: new Date().toISOString() }); return clone(existing); }
    const item: MedicineItem = { ...fields, id: id("med"), created_at: new Date().toISOString(), updated_at: new Date().toISOString() };
    medicines.push(item);
    return clone(item);
  },
  remove: async (medicineID) => {
    const index = medicines.findIndex(item => item.id === medicineID);
    if (index >= 0) medicines.splice(index, 1);
    return { deleted: index >= 0 };
  },
};
window.sparkclawMedicineBox = medicineBoxMock;

// ---------------------------------------------------------------------------
// Gateway fetch stub

const owner = { id: "owner-preview", display_name: "张伟", email: OWNER_ADDRESS, preferences: { language: "zh" }, created_at: ago(400 * day), updated_at: ago(3 * day) };
const modelProfile = (name: string, model: string) => ({ name, base_url: "http://model.invalid/v1", model, capacity_physical_model: model, context_tokens: 131072, output_budgets: { default: 4096 } });
const config = {
  gateway: { bind: "127.0.0.1", port: 18789, pairing_required: true, remote_access: "iscp", rate_limit: { enabled: true, requests_per_minute: 120, burst: 30 } },
  model: { capacity_profile: "standard", mock: false, http_timeout_seconds: 120, fast: modelProfile("fast", "qwen3-8b"), deep: modelProfile("deep", "qwen3-32b"), embedding: modelProfile("embedding", "bge-m3"), guard: modelProfile("guard", "qwen3-guard") },
  speech: { enabled: false, backend: "none", model: "", default_language: "auto", max_audio_seconds: 120, max_upload_bytes: 10_485_760, retain_audio: false },
  iscp_pairing: { enabled: true, ready: true, state: "ready", domain_id: "domain-preview", authority_host: "authority.invalid", expected_ticket_type: "client" },
  workspaces: { default_root: "/srv/sparkclaw/workspace", allowlist: ["/srv/sparkclaw/workspace"] },
  security: { external_content_untrusted: true, approval_required_for_dangerous_tools: true, sandbox_required_for_mutating_tools: true, dangerous_tools_require_deep_verification: true, denied_tools: [], approval_required_tools: ["email.send"], tool_policy_path: "/etc/sparkclaw/tool-policy.yaml", browser_read_allow_hosts: [] },
  sandbox: { enabled: true, backend: "docker", runner_url: "", image: "sparkclaw/sandbox:latest", network: "none", workspace_access: "read_write", host_access: "none" },
  storage: { trace_dir: "/var/lib/sparkclaw/traces", log_dir: "/var/log/sparkclaw", artifact_backend: "file", artifact_dir: "/var/lib/sparkclaw/artifacts", artifact_bucket: "", s3_endpoint: "", s3_region: "", s3_access_key: "", s3_secret_key: "" },
  state: { backend: "postgres", path: "", dsn: "", encrypt_at_rest: true, encryption_key: "", encryption_key_file: "/etc/sparkclaw/state.key" },
  memory: { enabled: true, write_policy: "candidate", allow_sensitive_memory: false, retention_days: 365, redact_patterns: [] },
  runtime: { observation_summary_max_bytes: 16384 },
  tools: { notifications: { channels: { weixin: { enabled: true, provider: "ilink", base_url: "", token_configured: true, recipient_set: true, available: true, operator_enabled: true, binding_status: "active", startable: true } } }, reminders: { enabled: true, default_channel: "weixin" } },
  tool_policy: { operator_controls: { web_access: true, workspace_files: true, shell_commands: false, file_changes: "ask", external_actions: "ask" }, policy_path: "/etc/sparkclaw/tool-policy.yaml", external_content_untrusted: true, approval_required_for_dangerous_tools: true, sandbox_required_for_mutating_tools: true, dangerous_tools_deep_verification: true, definition_count: 42, risk_counts: { read: 20, draft: 10, reversible: 8, dangerous: 4 }, definition_approval_required_tools: [], configured_approval_required_tools: ["email.send"], denied_tools: [], browser_read_allow_hosts: [] },
};
const ready = {
  ok: true, workspace_root: "/srv/sparkclaw/workspace", trace_dir: "/var/lib/sparkclaw/traces", state_backend: "postgres", state_path: "", auth_required: true,
  rate_limit: { enabled: true, requests_per_minute: 120, burst: 30 }, model_mode: "local", gateway_binding: "iscp",
  speech: { enabled: false, ready: false, state: "disabled", backend: "none", model: "", supports_streaming: false, accepted_content_types: [], max_audio_seconds: 120, max_upload_bytes: 10_485_760 },
  resident_services: [{ lane: "fast", backend: "vllm", model: "qwen3-8b", readiness: "ready" }],
  credential_vault: { ready: true, state: "ready" },
  store: { backend: "postgres", state: "ready", ready: true, durable: true, active_operations: 0, started_at: ago(3 * day) },
};
const connectors = [
  { channel: "weixin", provider: "ilink", setup_kind: "qr", available: true, enabled: true, running: true, state: "active", binding_status: "active", binding_startable: true, supports_multiple_bindings: false, iscp_enabled: true, version: 3, updated_at: ago(10 * day) },
  { channel: "feishu", provider: "feishu", setup_kind: "secret", available: true, enabled: false, running: false, state: "disabled", binding_status: "none", binding_startable: false, supports_multiple_bindings: true, iscp_enabled: true, version: 1 },
];
const bindings = [
  { id: "binding-weixin", owner_id: owner.id, channel: "weixin", provider: "ilink", status: "active", display_name: "张伟", account_id: "wx-preview", default_for_channel: true, scopes: ["notify"], created_at: ago(30 * day), updated_at: ago(10 * day) },
];
const notifications = [
  { id: "notice-1", notification_id: "lm-1", source: "localmind", kind: "comment_mention", deep_link: "https://docs.invalid/d/quarterly#comment-12", occurred_at: ago(40 * minute), created_at: ago(40 * minute), updated_at: ago(40 * minute) },
  { id: "notice-2", notification_id: "lm-2", source: "localmind", kind: "document_mention", deep_link: "https://docs.invalid/d/onboarding", occurred_at: ago(day), read_at: ago(20 * hour), created_at: ago(day), updated_at: ago(20 * hour) },
];

type Handler = (match: RegExpMatchArray, url: URL, body: Record<string, unknown>, method: string) => unknown;
const routes: Array<[string, RegExp, Handler]> = [
  ["GET", /^\/api\/config$/, () => config],
  ["GET", /^\/readyz$/, () => ready],
  ["GET", /^\/healthz$/, () => ({ ok: true })],
  ["GET", /^\/api\/owner$/, () => owner],
  ["POST", /^\/api\/owner\/language$/, (_m, _u, body) => { owner.preferences.language = String(body.language ?? "zh"); return owner; }],
  ["GET", /^\/api\/workbench\/identity$/, () => ({ deployment_id: "deployment-preview", owner_id: owner.id, client_id: "client-preview-mac" })],
  ["GET", /^\/api\/connectors$/, () => ({ connectors })],
  ["GET", /^\/api\/notification-bindings$/, () => ({ bindings })],
  ["GET", /^\/api\/notifications$/, () => ({ notifications, unread_count: notifications.filter(item => !item.read_at).length, watermark: notifications[0].id })],
  ["POST", /^\/api\/notifications\/read-all$/, () => { notifications.forEach(item => { item.read_at ||= new Date().toISOString(); }); return { updated: notifications.length, unread_count: 0 }; }],
  ["POST", /^\/api\/notifications\/([^/]+)\/read$/, (m) => {
    const notification = notifications.find(item => item.id === decodeURIComponent(m[1]))!;
    notification.read_at = new Date().toISOString();
    return { notification, unread_count: notifications.filter(item => !item.read_at).length };
  }],
  ["GET", /^\/api\/clients$/, () => ({ clients: [{ id: "client-preview-mac", name: "张伟的 MacBook Pro", created_at: ago(200 * day), last_seen_at: ago(minute) }] })],
  ["GET", /^\/api\/integrations$/, () => ({ integrations: [] })],
  // Mail
  ["GET", /^\/api\/email\/providers$/, () => ({ providers: [provider] })],
  ["GET", /^\/api\/email\/accounts$/, () => ({ accounts: [provider] })],
  ["POST", /^\/api\/email\/(?:providers\/[^/]+|accounts\/[^/]+)\/check$/, () => ({ ...provider, last_checked_at: new Date().toISOString(), sync_health: { state: "healthy", checked_at: new Date().toISOString(), poll_through: mailbox.poll_through } })],
  ["GET", /^\/api\/email\/sync-status$/, () => ({ version: mailVersion, provider_versions: { qq_mail: provider.version }, mailboxes: [mailbox], backlog: 0,
    pending_count: [...mails.values()].filter(mail => mail.classification_state === "pending").length, captured_count: mails.size,
    capacity: { state: "ok", total_bytes: 64 * 2 ** 30, free_bytes: 51 * 2 ** 30, used_percent: 20 } })],
  ["GET", /^\/api\/email\/sync-warnings$/, () => ({ items: [] })],
  ["POST", /^\/api\/email\/sync$/, () => { mailbox.last_sync_at = new Date().toISOString(); mailVersion++; return { scheduled: true, refresh_requests: [{ mailbox_id: MAILBOX, refresh_request_id: id("refresh") }] }; }],
  ["GET", /^\/api\/email\/conversations$/, (_m, url) => {
    const entry = url.searchParams.get("entry"), q = url.searchParams.get("q") ?? "";
    const rows = [...conversations.values()].filter(item => (!entry || item.effective_entry === entry) && matches([item.title, item.summary, ...item.participants], q))
      .sort((a, b) => (b.last_activity_at ?? "").localeCompare(a.last_activity_at ?? ""));
    return { version: mailVersion, conversations: clone(rows), counts: { total: rows.length, unseen: rows.reduce((sum, item) => sum + item.unseen_count, 0) } };
  }],
  ["GET", /^\/api\/email\/conversations\/([^/]+)\/messages$/, (m) => mailPage([...mails.values()].filter(mail => mail.conversation_id === decodeURIComponent(m[1])))],
  ["GET", /^\/api\/email\/conversations\/([^/]+)$/, (m) => {
    const conversation = conversations.get(decodeURIComponent(m[1]));
    if (!conversation) throw new GatewayError(404, "email_conversation_not_found");
    return { version: mailVersion, conversation: clone(conversation) };
  }],
  ["POST", /^\/api\/email\/conversations\/([^/]+)\/rename$/, (m, _u, body) => {
    const conversation = conversations.get(decodeURIComponent(m[1]))!;
    conversation.title = String(body.title ?? conversation.title); conversation.version++; mailVersion++;
    return { conversation: clone(conversation) };
  }],
  ["DELETE", /^\/api\/email\/conversations\/([^/]+)$/, (m) => {
    const conversationID = decodeURIComponent(m[1]);
    const members = [...mails.values()].filter(mail => mail.conversation_id === conversationID);
    members.forEach(mail => mails.delete(mail.id)); conversations.delete(conversationID); mailVersion++;
    return { conversation_id: conversationID, deleted_mails: members.length, freed_bytes: 0 };
  }],
  ["GET", /^\/api\/email\/interaction-mails$/, (_m, url) => mailPage([looseInteraction].filter(mail => mails.has(mail.id) && matches([mail.subject, mail.from], url.searchParams.get("q") ?? "")))],
  ["GET", /^\/api\/email\/notifications$/, (_m, url) => mailPage([looseNotification].filter(mail => mails.has(mail.id) && matches([mail.subject, mail.from], url.searchParams.get("q") ?? "")))],
  ["GET", /^\/api\/email\/pending$/, () => mailPage([...mails.values()].filter(mail => mail.classification_state === "pending"))],
  ["POST", /^\/api\/email\/messages\/viewed$/, (_m, _u, body) => {
    const ids = (body.mail_ids as string[] | undefined) ?? [];
    for (const mailID of ids) {
      const mail = mails.get(mailID);
      if (!mail || mail.viewed) continue;
      mail.viewed = true; mail.version++;
      const conversation = mail.conversation_id ? conversations.get(mail.conversation_id) : undefined;
      if (conversation) { conversation.unseen_count = Math.max(0, conversation.unseen_count - 1); conversation.version++; }
    }
    mailVersion++;
    return { version: mailVersion, mail_ids: ids };
  }],
  ["GET", /^\/api\/email\/messages\/([^/]+)\/render-preview$/, (m) => {
    const mail = mails.get(decodeURIComponent(m[1]));
    return { version: 1, id: mail?.id ?? m[1], state: mail ? "ready" : "unavailable", sanitizer_version: "preview", representation_id: `rep-${m[1]}`,
      content: mail ? renderNodes(mail.body_text ?? "") : [], embedded_resource_count: 0, embedded_resource_bytes: 0 };
  }],
  ["POST", /^\/api\/email\/messages\/([^/]+)\/reanalyze$/, () => ({ scheduled: true })],
  ["POST", /^\/api\/email\/messages\/([^/]+)\/classification$/, (m, _u, body) => {
    const mail = mails.get(decodeURIComponent(m[1]))!;
    const entry = body.entry === "notification" ? "notification" : "interaction";
    mail.classification = { ...(mail.classification ?? { category: entry, source: "manual", state: "ready", revision: 0 }), effective_entry: entry, category: entry, source: "manual", reason_code: "manual_choice", revision: (mail.classification?.revision ?? 0) + 1 };
    mail.version++; mailVersion++;
    return { classification: mail.classification };
  }],
  ["POST", /^\/api\/email\/messages\/([^/]+)\/assignment$/, (m, _u, body) => {
    const mail = mails.get(decodeURIComponent(m[1]))!;
    const conversationID = String(body.conversation_id || id("ec"));
    mail.conversation_id = conversationID; mail.version++; mailVersion++;
    return { conversation_id: conversationID };
  }],
  ["GET", /^\/api\/email\/messages\/([^/]+)\/verification$/, () => ({ purpose: "", state: "validity_unknown", server_now: new Date().toISOString(), can_reveal: false })],
  ["GET", /^\/api\/email\/messages\/([^/]+)$/, (m) => {
    const mail = mails.get(decodeURIComponent(m[1]));
    if (!mail) throw new GatewayError(404, "email_message_not_found");
    return clone(mail);
  }],
  ["GET", /^\/api\/email\/presentations$/, (_m, url) => {
    const kind = url.searchParams.get("target_kind") === "conversation" ? "conversation" : "mail";
    const language = url.searchParams.get("language") === "en" ? "en" : "zh";
    return { items: url.searchParams.getAll("target_id").map(targetID => presentation(kind, targetID, language)) };
  }],
  ["POST", /^\/api\/email\/presentations\/ensure$/, (_m, _u, body) => ({ items: ((body.target_ids as string[] | undefined) ?? []).map(targetID =>
    presentation(body.target_kind === "conversation" ? "conversation" : "mail", targetID, body.language === "en" ? "en" : "zh")) })],
  ["GET", /^\/api\/email\/sender-rules$/, () => ({ rules: [{ id: "rule-jd", address: "jd@jd.com", entry: "notification", enabled: true, revision: 1 }] })],
  ["GET", /^\/api\/email\/compose-capabilities$/, () => ({ compose: true, reply: true, reply_all: true, cc: true, max_to: 20, workspace_attachments: true, max_attachments: 5, max_attachment_bytes: 20 * 2 ** 20 })],
  ["GET", /^\/api\/email\/drafts$/, (_m, url) => {
    const draftID = url.searchParams.get("draft");
    if (draftID) return clone(emailDrafts.find(draft => draft.id === draftID) ?? {});
    return { items: clone(emailDrafts) };
  }],
  ["GET", /^\/api\/email\/drafts\/([^/]+)$/, (m) => {
    const draft = emailDrafts.find(item => item.id === decodeURIComponent(m[1]));
    if (!draft) throw new GatewayError(404, "email_draft_not_found");
    return clone(draft);
  }],
  ["POST", /^\/api\/email\/drafts$/, (_m, _u, body) => saveDraft(body as Partial<EmailDraft>)],
  ["PUT", /^\/api\/email\/drafts\/([^/]+)$/, (m, _u, body) => saveDraft(body as Partial<EmailDraft>, decodeURIComponent(m[1]))],
  ["POST", /^\/api\/email\/drafts\/([^/]+)\/send$/, (m) => {
    const draft = emailDrafts.find(item => item.id === decodeURIComponent(m[1]))!;
    Object.assign(draft, { state: "sent", version: draft.version + 1, sent_mail_id: id("mail"), confirmation_source: "provider_receipt" });
    return clone(draft);
  }],
  ["POST", /^\/api\/email\/drafts\/([^/]+)\/(?:reconcile|resolve)$/, (m) => clone(emailDrafts.find(item => item.id === decodeURIComponent(m[1]))!)],
  ["POST", /^\/api\/email\/replies\/polish$/, (_m, _u, body) => saveDraft({ id: String(body.id), body: "您好：\n\n感谢您的来信，已收到相关信息，我会尽快确认后回复您。\n\n张伟" })],
  ["GET", /^\/api\/email\/sent-sources$/, () => mailPage([...mails.values()].filter(mail => mail.direction === "outbound"))],
  // The preview has no vision model: a tiny image reads as unrecognized, anything else as one cold remedy.
  ["POST", /^\/api\/medicine\/recognize$/, (_m, _u, body) => String(body.image ?? "").length < 4000
    ? { recognized: false, name: "", category: "other", unit: "tablet", usage: "" }
    : { recognized: true, name: "复方氨酚烷胺胶囊", category: "cold", unit: "capsule", usage: "缓解感冒引起的发热、头痛、鼻塞" }],
  // Text reading: "和" names two medicines; anything without a known name is not a medicine.
  ["POST", /^\/api\/medicine\/parse$/, (_m, _u, body) => {
    const text = String(body.text ?? "");
    if (text.includes("和")) return { recognized: false, multiple: true };
    if (!text.includes("布洛芬")) return { recognized: false, name: "", category: "other", unit: "tablet", quantity: null, expires_on: "", location: "", usage: "" };
    const count = /(\d+(?:\.\d+)?)\s*粒/u.exec(text);
    return { recognized: true, name: "布洛芬缓释胶囊", category: "fever", unit: "capsule", quantity: count ? Number(count[1]) : null,
      expires_on: /2027\s*年\s*12\s*月/u.test(text) ? "2027-12-31" : "", location: text.includes("客厅抽屉") ? "客厅抽屉" : "", usage: text.includes("退烧") ? "退烧止痛" : "" };
  }],
];

class GatewayError extends Error {
  constructor(readonly status: number, readonly code: string) { super(code); }
}
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const originalFetch = window.fetch.bind(window);
window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  const target = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
  if (!target.startsWith(GATEWAY)) return originalFetch(input, init);
  const url = new URL(target);
  const method = (init?.method ?? (input instanceof Request ? input.method : "GET")).toUpperCase();
  let body: Record<string, unknown> = {};
  if (typeof init?.body === "string" && init.body) { try { body = JSON.parse(init.body) as Record<string, unknown>; } catch { body = {}; } }
  await wait(60 + Math.random() * 120);
  if (init?.signal?.aborted) throw new DOMException("The operation was aborted.", "AbortError");
  for (const [routeMethod, pattern, handler] of routes) {
    const match = url.pathname.match(pattern);
    if (!match || routeMethod !== method) continue;
    try { return json(200, handler(match, url, body, method)); }
    catch (error) {
      if (error instanceof GatewayError) return json(error.status, { error: error.code, code: error.code });
      console.error("[client-preview] mock handler failed", method, url.pathname, error);
      return json(500, { error: "preview_mock_failed" });
    }
  }
  console.warn(`[client-preview] unmocked gateway endpoint ${method} ${url.pathname}${url.search}`);
  return json(200, {});
};

console.info(`[client-preview] mocks installed: ${threads.size} conversations, ${schedules.length} schedules, ${conversations.size} mail conversations; long history in "${threadOf(longID).conversation.title}"`);
