import { FormEvent, KeyboardEvent, useCallback, useEffect, useMemo, useState } from "react";
import {
  Activity,
  Bot,
  CalendarDays,
  Check,
  Clock3,
  Copy,
  Database,
  Download,
  FileSearch,
  Gauge,
  Globe2,
  Inbox,
  KeyRound,
  Languages,
  Library,
  ListChecks,
  Mail,
  MemoryStick,
  Pencil,
  Plus,
  RefreshCw,
  Save,
  ScrollText,
  Send,
  Settings,
  ShieldAlert,
  Terminal,
  ThumbsDown,
  ThumbsUp,
  Trash2,
  UserRound,
  X
} from "lucide-react";
import { api, apiToken, clearAPIToken, saveAPIToken, sessionEventsURL } from "./api/client";
import type {
  Approval,
  ArtifactObject,
  AuditEvent,
  Client,
  EpisodeSummary,
  EvalRun,
  Memory,
  MemoryCandidate,
  Message,
  ModelCall,
  OwnerProfile,
  PublicConfig,
  ReadyStatus,
  RunTrace,
  Session,
  Skill,
  ToolCall,
  TraceMetadata
} from "./api/types";

type Language = "en" | "zh";
type PanelTab = "timeline" | "approvals" | "memory" | "trace" | "status" | "settings";

const LANGUAGE_STORAGE_KEY = "sparkclaw.language";

const dictionaries = {
  en: {
    app: {
      name: "SparkClaw",
      tagline: "Local agent runtime",
      titleFallback: "Agent Workbench",
      sessionTitle: "Local Agent Workbench"
    },
    common: {
      accepted: "accepted",
      approve: "Approve",
      approved: "approved",
      cancel: "Cancel",
      delete: "Delete",
      disabled: "disabled",
      edit: "Edit",
      enabled: "enabled",
      export: "Export",
      failed: "failed",
      gatewayUnavailable: "Gateway unavailable.",
      loading: "Loading.",
      noToken: "no token",
      none: "none",
      notSet: "not set",
      pair: "Pair",
      pairing: "Pairing",
      pending: "pending",
      refresh: "Refresh",
      reject: "Reject",
      rejected: "rejected",
      resolved: "resolved",
      revoked: "revoked",
      run: "Run",
      running: "Running",
      save: "Save",
      saveToken: "Save token",
      tokenConfigured: "token configured",
      unknown: "unknown",
      yes: "yes",
      no: "no"
    },
    nav: {
      newSession: "New Session",
      renameSession: "Rename session",
      saveSessionName: "Save session name",
      deleteSession: "Delete session",
      confirmDeleteSession: "Delete this session?",
      language: "Language",
      sessions: "Sessions",
      gateway: "Gateway",
      mode: "Mode",
      approvals: "Approvals",
      memories: "Memories",
      ready: "Ready",
      offline: "Offline"
    },
    topbar: {
      connecting: "Connecting to Gateway",
      modelMode: "model mode",
      workspace: "Workspace"
    },
    tabs: {
      timeline: "Timeline",
      approvals: "Approvals",
      memory: "Memory",
      trace: "Trace",
      status: "Status",
      settings: "Settings"
    },
    chat: {
      emptyTitle: "Ready for bounded local work.",
      placeholder: "Ask SparkClaw to inspect files, use tools, or prepare a guarded change...",
      send: "Send message",
      you: "You",
      assistant: "SparkClaw",
      correction: "Correction",
      helpful: "Mark helpful",
      unhelpful: "Mark not helpful",
      saveCorrection: "Save correction"
    },
    starters: [
      "Search for SparkClaw in the workspace",
      "Read https://example.com with browser.read",
      "Search email for deployment",
      "Read calendar for today",
      "Remember that SparkClaw prefers approval-first workflows",
      "Run shell command `ls -la` in the sandbox"
    ],
    auth: {
      gatewayToken: "Gateway token",
      unauthorized: "Token authentication failed"
    },
    errors: {
      connect: "Failed to connect to SparkClaw Gateway",
      createSession: "Could not create session",
      renameSession: "Could not rename session",
      deleteSession: "Could not delete session",
      message: "Message failed",
      approval: "Approval update failed",
      approvalEdit: "Approval edit failed",
      memory: "Memory update failed",
      memoryEdit: "Memory edit failed",
      memoryDelete: "Memory delete failed",
      feedback: "Feedback save failed",
      memoryExport: "Memory export failed",
      clientRevoke: "Client revoke failed",
      policyUpdate: "Tool policy update failed",
      ownerUpdate: "Owner profile update failed",
      trace: "Trace unavailable",
      pairing: "Pairing failed",
      eval: "Eval failed"
    },
    timeline: {
      title: "Tool Timeline",
      empty: "No tool calls yet.",
      openTrace: "Open run trace",
      approval: "Approval"
    },
    approval: {
      title: "Approval Inbox",
      empty: "No approvals.",
      editArguments: "Edit arguments",
      invalidJson: "Invalid JSON"
    },
    memory: {
      title: "Memory Review",
      emptyCandidates: "No memory candidates.",
      accepted: "Accepted",
      pending: "Pending",
      acceptMemory: "Accept memory",
      rejectMemory: "Reject memory",
      archiveExport: "Archive memory export",
      saveMemory: "Save memory",
      cancelEdit: "Cancel edit",
      editMemory: "Edit memory",
      deleteMemory: "Delete memory",
      kind: "Memory kind",
      content: "Memory content"
    },
    trace: {
      title: "Trace",
      empty: "No trace selected.",
      loading: "Loading trace.",
      modelNote: "Model Note",
      lane: "Lane",
      model: "Model",
      calls: "Calls",
      tokens: "Tokens",
      latency: "Latency",
      risk: "Risk",
      tools: "Tools",
      approvals: "Approvals",
      feedback: "Feedback",
      audit: "Audit"
    },
    status: {
      runtime: "Runtime",
      gateway: "Gateway",
      model: "Model",
      rateLimit: "Rate Limit",
      workspace: "Workspace",
      trace: "Trace",
      state: "State",
      dsn: "DSN",
      modelCalls: "Model Calls",
      noModelCalls: "No model calls in this session.",
      audit: "Audit",
      noAudit: "No audit events in this session.",
      artifacts: "Artifacts",
      noArtifacts: "No artifacts archived.",
      episodes: "Episodes",
      noEpisodes: "No episodes yet.",
      smokeEval: "Smoke Eval",
      noEval: "No eval run in this view.",
      skills: "Skills",
      noSkills: "No skills registered."
    },
    settings: {
      title: "Settings",
      unavailable: "Configuration unavailable.",
      ownerProfile: "Owner Profile",
      name: "Name",
      email: "Email",
      preferences: "Preferences",
      editOwner: "Edit owner profile",
      saveOwner: "Save owner profile",
      cancelOwner: "Cancel owner edit",
      ownerUnavailable: "Owner unavailable.",
      preferenceFormat: "Preferences use key=value lines",
      preferenceKey: "Preference keys are required",
      toolPolicy: "Tool Policy",
      file: "File",
      external: "External",
      dangerous: "Dangerous",
      verifier: "Verifier",
      sandbox: "Sandbox",
      untrusted: "untrusted",
      trusted: "trusted",
      approvalRequired: "approval required",
      notForced: "not forced",
      deepCheck: "deep check",
      standard: "standard",
      mutationsRequireSandbox: "mutations require sandbox",
      pairedClients: "Paired Clients",
      noClients: "No paired clients.",
      revokeClient: "Revoke client",
      seen: "seen",
      notSeen: "not seen",
      definitionApprovalTools: "Definition Approval Tools",
      configApprovalAdditions: "Config Approval Additions",
      deniedTools: "Denied Tools",
      saveToolPolicy: "Save tool policy",
      cancelPolicy: "Cancel policy edit",
      editPolicy: "Edit tool policy",
      approval: "Approval",
      deny: "Deny",
      modelProfiles: "Model Profiles",
      mode: "Mode",
      fast: "Fast",
      deep: "Deep",
      embed: "Embed",
      rerank: "Rerank",
      guard: "Guard",
      runtimeBoundaries: "Runtime Boundaries",
      remote: "Remote",
      artifacts: "Artifacts",
      adapters: "Adapters",
      memory: "Memory",
      skills: "Skills",
      localFiles: "local files",
      noAutoPrune: "no auto prune",
      encrypted: "encrypted",
      mock: "mock",
      externalModel: "external"
    },
    risk: {
      read: "read",
      draft: "draft",
      reversible: "reversible",
      dangerous: "dangerous"
    },
    state: {
      approved: "approved",
      accepted: "accepted",
      completed: "completed",
      failed: "failed",
      pending: "pending",
      rejected: "rejected",
      running: "running",
      passed: "passed"
    },
    units: {
      bytes: "bytes",
      tools: "tools",
      tokens: "tokens",
      deps: "deps",
      evals: "evals",
      schema: "schema",
      ctx: "ctx",
      max: "max",
      avg: "avg",
      retentionDays: "d retention"
    }
  },
  zh: {
    app: {
      name: "SparkClaw",
      tagline: "本地 Agent Runtime",
      titleFallback: "Agent 工作台",
      sessionTitle: "本地 Agent 工作台"
    },
    common: {
      accepted: "已接受",
      approve: "批准",
      approved: "已批准",
      cancel: "取消",
      delete: "删除",
      disabled: "已关闭",
      edit: "编辑",
      enabled: "已开启",
      export: "导出",
      failed: "失败",
      gatewayUnavailable: "Gateway 不可用。",
      loading: "加载中。",
      noToken: "未配置 token",
      none: "无",
      notSet: "未设置",
      pair: "配对",
      pairing: "配对中",
      pending: "待处理",
      refresh: "刷新",
      reject: "拒绝",
      rejected: "已拒绝",
      resolved: "已处理",
      revoked: "已撤销",
      run: "运行",
      running: "运行中",
      save: "保存",
      saveToken: "保存 token",
      tokenConfigured: "已配置 token",
      unknown: "未知",
      yes: "是",
      no: "否"
    },
    nav: {
      newSession: "新会话",
      renameSession: "重命名会话",
      saveSessionName: "保存会话名称",
      deleteSession: "删除会话",
      confirmDeleteSession: "确定删除这个会话吗？",
      language: "语言",
      sessions: "会话",
      gateway: "Gateway",
      mode: "模式",
      approvals: "审批",
      memories: "记忆",
      ready: "就绪",
      offline: "离线"
    },
    topbar: {
      connecting: "正在连接 Gateway",
      modelMode: "模型模式",
      workspace: "工作区"
    },
    tabs: {
      timeline: "时间线",
      approvals: "审批",
      memory: "记忆",
      trace: "Trace",
      status: "状态",
      settings: "设置"
    },
    chat: {
      emptyTitle: "已准备好执行有边界的本地任务。",
      placeholder: "让 SparkClaw 检查文件、调用工具，或准备需要保护的变更...",
      send: "发送消息",
      you: "你",
      assistant: "SparkClaw",
      correction: "修正内容",
      helpful: "标记有用",
      unhelpful: "标记无用",
      saveCorrection: "保存修正"
    },
    starters: [
      "在工作区搜索 SparkClaw",
      "使用 browser.read 读取 https://example.com",
      "搜索和部署相关的邮件",
      "读取今天的日历安排",
      "记住 SparkClaw 偏好审批优先的工作流",
      "在沙箱中运行 `ls -la`"
    ],
    auth: {
      gatewayToken: "Gateway token",
      unauthorized: "Token 认证失败"
    },
    errors: {
      connect: "无法连接 SparkClaw Gateway",
      createSession: "无法创建会话",
      renameSession: "无法重命名会话",
      deleteSession: "无法删除会话",
      message: "消息发送失败",
      approval: "审批更新失败",
      approvalEdit: "审批参数修改失败",
      memory: "记忆更新失败",
      memoryEdit: "记忆编辑失败",
      memoryDelete: "记忆删除失败",
      feedback: "反馈保存失败",
      memoryExport: "记忆导出失败",
      clientRevoke: "客户端撤销失败",
      policyUpdate: "工具策略更新失败",
      ownerUpdate: "Owner 资料更新失败",
      trace: "Trace 不可用",
      pairing: "配对失败",
      eval: "Eval 失败"
    },
    timeline: {
      title: "工具时间线",
      empty: "还没有工具调用。",
      openTrace: "打开运行 Trace",
      approval: "审批"
    },
    approval: {
      title: "审批收件箱",
      empty: "没有审批。",
      editArguments: "编辑参数",
      invalidJson: "JSON 无效"
    },
    memory: {
      title: "记忆审查",
      emptyCandidates: "没有待确认记忆。",
      accepted: "已接受",
      pending: "待处理",
      acceptMemory: "接受记忆",
      rejectMemory: "拒绝记忆",
      archiveExport: "归档记忆导出",
      saveMemory: "保存记忆",
      cancelEdit: "取消编辑",
      editMemory: "编辑记忆",
      deleteMemory: "删除记忆",
      kind: "记忆类型",
      content: "记忆内容"
    },
    trace: {
      title: "Trace",
      empty: "尚未选择 Trace。",
      loading: "正在加载 Trace。",
      modelNote: "模型备注",
      lane: "通道",
      model: "模型",
      calls: "调用",
      tokens: "Token",
      latency: "延迟",
      risk: "风险",
      tools: "工具",
      approvals: "审批",
      feedback: "反馈",
      audit: "审计"
    },
    status: {
      runtime: "运行时",
      gateway: "Gateway",
      model: "模型",
      rateLimit: "速率限制",
      workspace: "工作区",
      trace: "Trace",
      state: "状态",
      dsn: "DSN",
      modelCalls: "模型调用",
      noModelCalls: "此会话还没有模型调用。",
      audit: "审计",
      noAudit: "此会话还没有审计事件。",
      artifacts: "产物",
      noArtifacts: "还没有归档产物。",
      episodes: "Episodes",
      noEpisodes: "还没有 episode。",
      smokeEval: "Smoke Eval",
      noEval: "当前视图没有 eval run。",
      skills: "Skills",
      noSkills: "没有注册技能。"
    },
    settings: {
      title: "设置",
      unavailable: "配置不可用。",
      ownerProfile: "Owner 资料",
      name: "名称",
      email: "邮箱",
      preferences: "偏好",
      editOwner: "编辑 Owner 资料",
      saveOwner: "保存 Owner 资料",
      cancelOwner: "取消 Owner 编辑",
      ownerUnavailable: "Owner 不可用。",
      preferenceFormat: "偏好使用 key=value 格式",
      preferenceKey: "偏好 key 不能为空",
      toolPolicy: "工具策略",
      file: "文件",
      external: "外部内容",
      dangerous: "危险工具",
      verifier: "验证器",
      sandbox: "沙箱",
      untrusted: "不可信",
      trusted: "可信",
      approvalRequired: "需要审批",
      notForced: "未强制",
      deepCheck: "深度检查",
      standard: "标准",
      mutationsRequireSandbox: "变更需要沙箱",
      pairedClients: "已配对客户端",
      noClients: "没有已配对客户端。",
      revokeClient: "撤销客户端",
      seen: "最后活跃",
      notSeen: "未活跃",
      definitionApprovalTools: "定义中需审批工具",
      configApprovalAdditions: "配置新增审批工具",
      deniedTools: "拒绝工具",
      saveToolPolicy: "保存工具策略",
      cancelPolicy: "取消策略编辑",
      editPolicy: "编辑工具策略",
      approval: "审批",
      deny: "拒绝",
      modelProfiles: "模型配置",
      mode: "模式",
      fast: "Fast",
      deep: "Deep",
      embed: "Embed",
      rerank: "Rerank",
      guard: "Guard",
      runtimeBoundaries: "运行边界",
      remote: "远程",
      artifacts: "产物",
      adapters: "适配器",
      memory: "记忆",
      skills: "技能",
      localFiles: "本地文件",
      noAutoPrune: "不自动清理",
      encrypted: "已加密",
      mock: "mock",
      externalModel: "external"
    },
    risk: {
      read: "读取",
      draft: "草稿",
      reversible: "可逆",
      dangerous: "危险"
    },
    state: {
      approved: "已批准",
      accepted: "已接受",
      completed: "已完成",
      failed: "失败",
      pending: "待处理",
      rejected: "已拒绝",
      running: "运行中",
      passed: "通过"
    },
    units: {
      bytes: "字节",
      tools: "个工具",
      tokens: "tokens",
      deps: "依赖",
      evals: "eval",
      schema: "schema",
      ctx: "上下文",
      max: "最大",
      avg: "平均",
      retentionDays: "天保留"
    }
  }
};

type Copy = (typeof dictionaries)["en"];

export function App() {
  const [language, setLanguage] = useState<Language>(() => initialLanguage());
  const text = dictionaries[language];
  const [sessions, setSessions] = useState<Session[]>([]);
  const [activeSession, setActiveSession] = useState<string>("");
  const [messages, setMessages] = useState<Message[]>([]);
  const [toolCalls, setToolCalls] = useState<ToolCall[]>([]);
  const [modelCalls, setModelCalls] = useState<ModelCall[]>([]);
  const [auditEvents, setAuditEvents] = useState<AuditEvent[]>([]);
  const [episodes, setEpisodes] = useState<EpisodeSummary[]>([]);
  const [approvals, setApprovals] = useState<Approval[]>([]);
  const [candidates, setCandidates] = useState<MemoryCandidate[]>([]);
  const [memories, setMemories] = useState<Memory[]>([]);
  const [ready, setReady] = useState<ReadyStatus | null>(null);
  const [runtimeConfig, setRuntimeConfig] = useState<PublicConfig | null>(null);
  const [ownerProfile, setOwnerProfile] = useState<OwnerProfile | null>(null);
  const [clients, setClients] = useState<Client[]>([]);
  const [evalRun, setEvalRun] = useState<EvalRun | null>(null);
  const [evalRuns, setEvalRuns] = useState<EvalRun[]>([]);
  const [artifacts, setArtifacts] = useState<ArtifactObject[]>([]);
  const [traceRun, setTraceRun] = useState<RunTrace | null>(null);
  const [traceList, setTraceList] = useState<TraceMetadata[]>([]);
  const [traceLoading, setTraceLoading] = useState(false);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [pairing, setPairing] = useState(false);
  const [tokenInput, setTokenInput] = useState("");
  const [error, setError] = useState("");
  const [tab, setTab] = useState<PanelTab>("timeline");
  const [editingSession, setEditingSession] = useState("");
  const [sessionTitleDraft, setSessionTitleDraft] = useState("");
  const [sessionActionId, setSessionActionId] = useState("");

  useEffect(() => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, language);
    document.documentElement.lang = language === "zh" ? "zh-CN" : "en";
  }, [language]);

  const refreshGlobal = useCallback(async () => {
    const [readyStatus, configStatus, owner, clientList, approvalList, candidateList, memoryList, skillList, evalList, artifactList, traces] =
      await Promise.all([
        api.ready(),
        api.config(),
        api.owner(),
        api.clients(),
        api.approvals(),
        api.memoryCandidates(),
        api.memories(),
        api.skills(),
        api.evalRuns(),
        api.artifacts(),
        api.traces()
      ]);
    setReady(readyStatus);
    setRuntimeConfig(configStatus);
    setOwnerProfile(owner);
    setClients(clientList.clients ?? []);
    setApprovals(approvalList.approvals);
    setCandidates(candidateList.memory_candidates);
    setMemories(memoryList.memories);
    setSkills(skillList.skills);
    setEvalRuns(evalList.eval_runs ?? []);
    setArtifacts(artifactList.artifacts ?? []);
    setTraceList(traces.traces ?? []);
  }, []);

  const refreshSession = useCallback(async (sessionId: string) => {
    if (!sessionId) return;
    const [messageList, callList, modelCallList, auditList, episodeList] = await Promise.all([
      api.messages(sessionId),
      api.toolCalls(sessionId),
      api.modelCalls(sessionId),
      api.audit(sessionId),
      api.episodes(sessionId)
    ]);
    setMessages(messageList.messages ?? []);
    setToolCalls(callList.tool_calls ?? []);
    setModelCalls(modelCallList.model_calls ?? []);
    setAuditEvents(auditList.audit_events ?? []);
    setEpisodes(episodeList.episodes ?? []);
  }, []);

  useEffect(() => {
    let cancelled = false;
    async function boot() {
      try {
        setError("");
        const [sessionList] = await Promise.all([api.sessions(), refreshGlobal()]);
        if (cancelled) return;
        let next = sessionList.sessions[0];
        if (!next) {
          next = await api.createSession();
        }
        setSessions(next ? [next, ...sessionList.sessions.filter((session) => session.id !== next.id)] : sessionList.sessions);
        setActiveSession(next.id);
        await refreshSession(next.id);
      } catch (err) {
        setError(err instanceof Error ? err.message : dictionaries[initialLanguage()].errors.connect);
      }
    }
    void boot();
    return () => {
      cancelled = true;
    };
  }, [refreshGlobal, refreshSession]);

  useEffect(() => {
    if (!activeSession) return;
    let refreshQueued = false;
    const refreshFromEvent = () => {
      if (refreshQueued) return;
      refreshQueued = true;
      window.setTimeout(() => {
        refreshQueued = false;
        void refreshSession(activeSession);
        void refreshGlobal();
      }, 80);
    };
    let events: EventSource | null = null;
    if (!apiToken() && "EventSource" in window) {
      events = new EventSource(sessionEventsURL(activeSession));
      events.onmessage = refreshFromEvent;
      events.addEventListener("message.created", refreshFromEvent);
      events.addEventListener("tool_call.completed", refreshFromEvent);
      events.addEventListener("tool_call.approval_pending", refreshFromEvent);
      events.addEventListener("tool_call.completed_after_approval", refreshFromEvent);
      events.addEventListener("approval.pending", refreshFromEvent);
      events.addEventListener("approval.approved", refreshFromEvent);
      events.addEventListener("approval.rejected", refreshFromEvent);
      events.addEventListener("memory_candidate.created", refreshFromEvent);
      events.addEventListener("memory.updated", refreshFromEvent);
      events.addEventListener("memory.deleted", refreshFromEvent);
      events.addEventListener("episode_summary.saved", refreshFromEvent);
    }
    const id = window.setInterval(() => {
      void refreshSession(activeSession);
      void refreshGlobal();
    }, 5000);
    return () => {
      window.clearInterval(id);
      events?.close();
    };
  }, [activeSession, refreshGlobal, refreshSession]);

  const pendingApprovals = useMemo(() => approvals.filter((approval) => approval.status === "pending"), [approvals]);
  const pendingCandidates = useMemo(() => candidates.filter((candidate) => candidate.status === "pending"), [candidates]);
  const active = sessions.find((session) => session.id === activeSession);
  const languageLabel = language === "zh" ? "中" : "EN";
  const nextLanguage = language === "zh" ? "en" : "zh";

  async function createSession() {
    try {
      setError("");
      const session = await api.createSession();
      setSessions((current) => [session, ...current]);
      setActiveSession(session.id);
      setMessages([]);
      setToolCalls([]);
      setModelCalls([]);
      setAuditEvents([]);
      setEpisodes([]);
      setTab("timeline");
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.createSession);
    }
  }

  async function send(content = input) {
    if (!activeSession || !content.trim() || busy) return;
    try {
      setBusy(true);
      setError("");
      setInput("");
      await api.sendMessage(activeSession, content.trim());
      const [sessionList] = await Promise.all([api.sessions(), refreshSession(activeSession), refreshGlobal()]);
      setSessions(sessionList.sessions ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.message);
    } finally {
      setBusy(false);
    }
  }

  function onSubmit(event: FormEvent) {
    event.preventDefault();
    void send();
  }

  function startRenameSession(session: Session) {
    setEditingSession(session.id);
    setSessionTitleDraft(session.title);
  }

  function cancelRenameSession() {
    setEditingSession("");
    setSessionTitleDraft("");
  }

  async function renameSession(id: string) {
    const title = sessionTitleDraft.trim();
    if (!title || sessionActionId) return;
    try {
      setSessionActionId(id);
      setError("");
      const updated = await api.updateSession(id, title);
      setSessions((current) => current.map((session) => (session.id === id ? updated : session)));
      cancelRenameSession();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.renameSession);
    } finally {
      setSessionActionId("");
    }
  }

  async function deleteSession(id: string) {
    if (sessionActionId || !window.confirm(text.nav.confirmDeleteSession)) return;
    try {
      setSessionActionId(id);
      setError("");
      await api.deleteSession(id);
      const sessionList = await api.sessions();
      let next = id === activeSession ? sessionList.sessions[0] : sessionList.sessions.find((session) => session.id === activeSession);
      if (!next) next = await api.createSession();
      setSessions(next ? [next, ...sessionList.sessions.filter((session) => session.id !== next.id)] : sessionList.sessions);
      setActiveSession(next.id);
      cancelRenameSession();
      setTraceRun(null);
      setTab("timeline");
      await Promise.all([refreshSession(next.id), refreshGlobal()]);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.deleteSession);
    } finally {
      setSessionActionId("");
    }
  }

  function onComposerKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing) return;
    event.preventDefault();
    void send();
  }

  async function resolveApproval(id: string, accepted: boolean) {
    try {
      setError("");
      if (accepted) await api.approve(id);
      else await api.reject(id);
      await Promise.all([refreshGlobal(), refreshSession(activeSession)]);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.approval);
    }
  }

  async function modifyApproval(id: string, args: Record<string, unknown>) {
    try {
      setError("");
      await api.modifyApproval(id, args);
      await Promise.all([refreshGlobal(), refreshSession(activeSession)]);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.approvalEdit);
    }
  }

  async function resolveMemory(id: string, accepted: boolean) {
    try {
      setError("");
      if (accepted) await api.acceptMemory(id);
      else await api.rejectMemory(id);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.memory);
    }
  }

  async function updateMemory(id: string, kind: string, content: string) {
    try {
      setError("");
      await api.updateMemory(id, kind, content);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.memoryEdit);
      throw err;
    }
  }

  async function deleteMemory(id: string) {
    try {
      setError("");
      await api.deleteMemory(id);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.memoryDelete);
      throw err;
    }
  }

  async function saveFeedback(message: Message, rating: "up" | "down" | "corrected", correction = "") {
    if (!message.run_id) return;
    try {
      setError("");
      await api.saveRunFeedback(message.run_id, message.id, rating, "", correction);
      await Promise.all([refreshSession(activeSession), refreshGlobal()]);
      if (traceRun?.run.id === message.run_id) {
        await openTrace(message.run_id);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.feedback);
      throw err;
    }
  }

  async function archiveMemoryExport() {
    try {
      setError("");
      await api.archiveMemoryExport();
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.memoryExport);
      throw err;
    }
  }

  async function revokeClient(id: string) {
    try {
      setError("");
      await api.revokeClient(id);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.clientRevoke);
      throw err;
    }
  }

  async function updateToolPolicy(deny: string[], approvalRequired: string[]) {
    try {
      setError("");
      await api.updateToolPolicy(deny, approvalRequired);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.policyUpdate);
      throw err;
    }
  }

  async function updateOwner(displayName: string, email: string, preferences: Record<string, string>) {
    try {
      setError("");
      const updated = await api.updateOwner(displayName, email, preferences);
      setOwnerProfile(updated);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.ownerUpdate);
      throw err;
    }
  }

  async function openTrace(runId: string) {
    try {
      setTraceLoading(true);
      setError("");
      setTab("trace");
      const [trace, traces] = await Promise.all([api.trace(runId), api.traces()]);
      setTraceRun(trace);
      setTraceList(traces.traces ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.trace);
    } finally {
      setTraceLoading(false);
    }
  }

  async function pairClient() {
    try {
      setPairing(true);
      setError("");
      const started = await api.startPairing();
      const claimed = await api.claimPairing(started.pairing_id, started.code, "WebChat");
      saveAPIToken(claimed.token);
      await bootstrappedRefresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.pairing);
    } finally {
      setPairing(false);
    }
  }

  async function submitToken(event: FormEvent) {
    event.preventDefault();
    const token = tokenInput.trim();
    if (!token) return;
    try {
      setError("");
      saveAPIToken(token);
      await bootstrappedRefresh();
      setTokenInput("");
    } catch (err) {
      clearAPIToken();
      setError(err instanceof Error ? err.message : text.auth.unauthorized);
    }
  }

  async function bootstrappedRefresh() {
    const [sessionList] = await Promise.all([api.sessions(), refreshGlobal()]);
    let next = sessionList.sessions[0];
    if (!next) next = await api.createSession();
    setSessions(next ? [next, ...sessionList.sessions.filter((session) => session.id !== next.id)] : sessionList.sessions);
    setActiveSession(next.id);
    await refreshSession(next.id);
  }

  return (
    <main className={`shell ${ready?.ok ? "gateway-ready" : "gateway-offline"}`}>
      <div className="connectionBar" aria-hidden="true" />
      <aside className="sidebar">
        <div className="brandRow">
          <div className="brand">
            <div className="brandMark">
              <Terminal size={18} />
            </div>
            <div>
              <strong>{text.app.name}</strong>
              <span>{text.app.tagline}</span>
            </div>
          </div>
          <button className="iconButton subtle" onClick={() => setLanguage(nextLanguage)} title={text.nav.language}>
            <Languages size={17} />
            <span>{languageLabel}</span>
          </button>
        </div>

        <div className="navStatus">
          <div className={`statusDot ${ready?.ok ? "ready" : "offline"}`} />
          <div>
            <strong>{ready?.ok ? text.nav.ready : text.nav.offline}</strong>
            <span>{ready ? ready.model_mode : text.topbar.connecting}</span>
          </div>
        </div>

        <button className="primaryButton" onClick={() => void createSession()} title={text.nav.newSession}>
          <Plus size={17} />
          <span>{text.nav.newSession}</span>
        </button>

        <dl className="navMetrics">
          <dt>{text.nav.sessions}</dt>
          <dd>{sessions.length}</dd>
          <dt>{text.nav.approvals}</dt>
          <dd>{pendingApprovals.length}</dd>
          <dt>{text.nav.memories}</dt>
          <dd>{pendingCandidates.length}</dd>
        </dl>

        <div className="sessionList" aria-label={text.nav.sessions}>
          {sessions.map((session) => (
            <div className={`sessionItem ${session.id === activeSession ? "active" : ""}`} key={session.id}>
              {editingSession === session.id ? (
                <form
                  className="sessionRenameForm"
                  onSubmit={(event) => {
                    event.preventDefault();
                    void renameSession(session.id);
                  }}
                >
                  <input
                    aria-label={text.nav.renameSession}
                    value={sessionTitleDraft}
                    onChange={(event) => setSessionTitleDraft(event.target.value)}
                    disabled={sessionActionId === session.id}
                  />
                  <button className="miniIconButton" disabled={!sessionTitleDraft.trim() || sessionActionId === session.id} title={text.nav.saveSessionName}>
                    <Save size={13} />
                  </button>
                  <button className="miniIconButton" type="button" onClick={cancelRenameSession} disabled={sessionActionId === session.id} title={text.common.cancel}>
                    <X size={13} />
                  </button>
                </form>
              ) : (
                <>
                  <button
                    className="sessionSelect"
                    onClick={() => {
                      setActiveSession(session.id);
                      setTab("timeline");
                      void refreshSession(session.id);
                    }}
                  >
                    <span>{session.title}</span>
                    <small>{shortId(session.id)}</small>
                  </button>
                  <div className="sessionActions">
                    <button className="miniIconButton" onClick={() => startRenameSession(session)} disabled={sessionActionId === session.id} title={text.nav.renameSession}>
                      <Pencil size={13} />
                    </button>
                    <button className="miniIconButton dangerIcon" onClick={() => void deleteSession(session.id)} disabled={sessionActionId === session.id} title={text.nav.deleteSession}>
                      <Trash2 size={13} />
                    </button>
                  </div>
                </>
              )}
            </div>
          ))}
        </div>
      </aside>

      <section className={`workspace ${error ? "hasError" : ""}`}>
        <header className="topbar">
          <div>
            <h1>{active?.title ?? text.app.titleFallback}</h1>
            <p>{ready ? `${ready.model_mode} ${text.topbar.modelMode} · ${ready.workspace_root}` : text.topbar.connecting}</p>
          </div>
          <div className="topbarActions">
            <span className="statusChip">
              <Bot size={14} />
              {ready?.ok ? text.nav.ready : text.nav.offline}
            </span>
            <button className="iconButton" onClick={() => void Promise.all([refreshGlobal(), refreshSession(activeSession)])} title={text.common.refresh}>
              <RefreshCw size={18} />
            </button>
          </div>
        </header>

        {error && (
          <div className="errorBanner">
            <span>{error}</span>
            {error.toLowerCase().includes("token") || error.toLowerCase().includes("unauthorized") ? (
              <div className="authActions">
                <form className="tokenForm" onSubmit={(event) => void submitToken(event)}>
                  <input
                    aria-label={text.auth.gatewayToken}
                    value={tokenInput}
                    onChange={(event) => setTokenInput(event.target.value)}
                    placeholder={text.auth.gatewayToken}
                    type="password"
                  />
                  <button type="submit" disabled={!tokenInput.trim()} title={text.common.saveToken}>
                    <KeyRound size={15} />
                  </button>
                </form>
                <button className="dangerButton" onClick={() => void pairClient()} disabled={pairing}>
                  <KeyRound size={15} />
                  <span>{pairing ? text.common.pairing : text.common.pair}</span>
                </button>
              </div>
            ) : null}
          </div>
        )}

        <section className="chatColumn">
          <div className="messageList">
            {messages.length === 0 ? (
              <div className="emptyState">
                <Activity size={25} />
                <span>{text.chat.emptyTitle}</span>
              </div>
            ) : (
              messages.map((message) => (
                <MessageBubble
                  key={message.id}
                  message={message}
                  text={text}
                  language={language}
                  onFeedback={(rating, correction) => saveFeedback(message, rating, correction)}
                />
              ))
            )}
          </div>
          <div className="composerDock">
            <div className="starterRow">
              {text.starters.map((prompt) => (
                <button key={prompt} onClick={() => void send(prompt)} disabled={busy}>
                  {prompt}
                </button>
              ))}
            </div>
            <form className="composer" onSubmit={onSubmit}>
              <textarea
                value={input}
                onChange={(event) => setInput(event.target.value)}
                onKeyDown={onComposerKeyDown}
                placeholder={text.chat.placeholder}
                disabled={busy}
              />
              <button className="sendButton" disabled={busy || !input.trim()} title={text.chat.send}>
                <Send size={18} />
              </button>
            </form>
          </div>
        </section>
      </section>

      <aside className="inspectorColumn">
        <div className="inspectorTitle">INSPECTOR</div>
        <div className="tabs">
          <button className={tab === "timeline" ? "selected" : ""} onClick={() => setTab("timeline")} title={text.tabs.timeline}>
            <FileSearch size={16} />
            <span>{text.tabs.timeline}</span>
          </button>
          <button className={tab === "approvals" ? "selected" : ""} onClick={() => setTab("approvals")} title={text.tabs.approvals}>
            <ShieldAlert size={16} />
            <span>{pendingApprovals.length}</span>
          </button>
          <button className={tab === "memory" ? "selected" : ""} onClick={() => setTab("memory")} title={text.tabs.memory}>
            <MemoryStick size={16} />
            <span>{pendingCandidates.length}</span>
          </button>
          <button className={tab === "trace" ? "selected" : ""} onClick={() => setTab("trace")} title={text.tabs.trace}>
            <ScrollText size={16} />
            <span>{text.tabs.trace}</span>
          </button>
          <button className={tab === "status" ? "selected" : ""} onClick={() => setTab("status")} title={text.tabs.status}>
            <Gauge size={16} />
            <span>{text.tabs.status}</span>
          </button>
          <button className={tab === "settings" ? "selected" : ""} onClick={() => setTab("settings")} title={text.tabs.settings}>
            <Settings size={16} />
            <span>{text.tabs.settings}</span>
          </button>
        </div>

        {tab === "timeline" && <ToolTimelinePanel calls={toolCalls} text={text} onTrace={(runId) => void openTrace(runId)} />}
        {tab === "approvals" && (
          <ApprovalPanel
            approvals={approvals}
            text={text}
            onResolve={(id, accepted) => void resolveApproval(id, accepted)}
            onModify={(id, args) => void modifyApproval(id, args)}
          />
        )}
        {tab === "memory" && (
          <MemoryPanel
            candidates={candidates}
            memories={memories}
            text={text}
            onResolve={(id, accepted) => void resolveMemory(id, accepted)}
            onUpdate={(id, kind, content) => updateMemory(id, kind, content)}
            onDelete={(id) => deleteMemory(id)}
            onExport={() => archiveMemoryExport()}
          />
        )}
        {tab === "trace" && (
          <TracePanel trace={traceRun} traces={traceList} loading={traceLoading} text={text} language={language} onOpen={(runId) => void openTrace(runId)} />
        )}
        {tab === "status" && (
          <StatusStack
            ready={ready}
            modelCalls={modelCalls}
            auditEvents={auditEvents}
            artifacts={artifacts}
            episodes={episodes}
            evalRun={evalRun}
            evalRuns={evalRuns}
            skills={skills}
            text={text}
            language={language}
            onRunEval={async () => {
              setError("");
              const result = await api.runEval("smoke");
              setEvalRun(result);
              setEvalRuns([result, ...evalRuns.filter((run) => run.id !== result.id)]);
            }}
            onSelectEval={async (id) => {
              setError("");
              setEvalRun(await api.evalRun(id));
            }}
            onError={(message) => setError(message)}
          />
        )}
        {tab === "settings" && (
          <SettingsPanel
            runtimeConfig={runtimeConfig}
            ownerProfile={ownerProfile}
            clients={clients}
            text={text}
            language={language}
            onUpdateOwner={(displayName, email, preferences) => updateOwner(displayName, email, preferences)}
            onRevokeClient={(id) => revokeClient(id)}
            onUpdatePolicy={(deny, approvalRequired) => updateToolPolicy(deny, approvalRequired)}
          />
        )}
      </aside>
    </main>
  );
}

function MessageBubble({
  message,
  text,
  language,
  onFeedback
}: {
  message: Message;
  text: Copy;
  language: Language;
  onFeedback: (rating: "up" | "down" | "corrected", correction?: string) => Promise<void>;
}) {
  const [correction, setCorrection] = useState("");
  const [saving, setSaving] = useState(false);

  async function submit(rating: "up" | "down" | "corrected") {
    if (saving || !message.run_id) return;
    setSaving(true);
    try {
      await onFeedback(rating, rating === "corrected" ? correction.trim() : "");
      if (rating === "corrected") setCorrection("");
    } catch {
      return;
    } finally {
      setSaving(false);
    }
  }

  return (
    <article className={`message ${message.role}`}>
      <div className="messageMeta">
        <span>{message.role === "user" ? text.chat.you : text.chat.assistant}</span>
        <time>{formatTime(message.created_at, language)}</time>
      </div>
      <p>{message.content}</p>
      {message.role === "assistant" && message.run_id && (
        <div className="feedbackBar">
          <button onClick={() => void submit("up")} disabled={saving} title={text.chat.helpful}>
            <ThumbsUp size={14} />
          </button>
          <button onClick={() => void submit("down")} disabled={saving} title={text.chat.unhelpful}>
            <ThumbsDown size={14} />
          </button>
          <input
            aria-label={text.chat.correction}
            value={correction}
            onChange={(event) => setCorrection(event.target.value)}
            disabled={saving}
            placeholder={text.chat.correction}
          />
          <button onClick={() => void submit("corrected")} disabled={saving || !correction.trim()} title={text.chat.saveCorrection}>
            <Check size={14} />
          </button>
        </div>
      )}
    </article>
  );
}

function ToolTimelinePanel({ calls, text, onTrace }: { calls: ToolCall[]; text: Copy; onTrace: (runId: string) => void }) {
  return (
    <div className="panelStack">
      <SectionHeader icon={<FileSearch size={17} />} title={text.timeline.title} />
      {calls.length === 0 ? (
        <span className="muted">{text.timeline.empty}</span>
      ) : (
        calls.map((call) => <ToolCallItem key={call.id} call={call} text={text} onTrace={onTrace} />)
      )}
    </div>
  );
}

function ToolCallItem({ call, text, onTrace }: { call: ToolCall; text: Copy; onTrace: (runId: string) => void }) {
  const Icon = call.tool.includes("shell")
    ? Terminal
    : call.tool.includes("memory")
      ? Database
      : call.tool.includes("knowledge")
        ? Library
        : call.tool.includes("browser")
          ? Globe2
          : call.tool.includes("email")
            ? Mail
            : call.tool.includes("calendar")
              ? CalendarDays
              : FileSearch;
  return (
    <article className={`toolCall ${call.risk} ${cssToken(call.status)}`}>
      <div className="toolIcon">
        <Icon size={16} />
      </div>
      <div className="toolBody">
        <div className="toolTitle">
          <strong title={call.tool}>{call.tool}</strong>
          <span className="toolBadges">
            <button className="miniIconButton" onClick={() => onTrace(call.run_id)} title={text.timeline.openTrace}>
              <ScrollText size={14} />
            </button>
            <RiskPill risk={call.risk} text={text} />
          </span>
        </div>
        <span className="statusLine">{formatState(call.status, text)}</span>
        {call.observation_summary && <small>{call.observation_summary}</small>}
        {call.error && <p className="compactError">{call.error}</p>}
        {call.approval_id && <small>{text.timeline.approval} {shortId(call.approval_id)}</small>}
        {Object.keys(stripSystemArgs(call.arguments)).length > 0 && <JsonBlock value={stripSystemArgs(call.arguments)} />}
      </div>
    </article>
  );
}

function TracePanel({
  trace,
  traces,
  loading,
  text,
  language,
  onOpen
}: {
  trace: RunTrace | null;
  traces: TraceMetadata[];
  loading: boolean;
  text: Copy;
  language: Language;
  onOpen: (runId: string) => void;
}) {
  return (
    <div className="panelStack">
      <SectionHeader icon={<ScrollText size={17} />} title={text.trace.title} />
      {traces.length > 0 && (
        <div className="traceHistory">
          {traces.slice(0, 6).map((item) => (
            <button
              key={item.run_id}
              className={trace?.run.id === item.run_id ? "selected" : ""}
              onClick={() => onOpen(item.run_id)}
              title={item.artifact_uri || item.run_id}
            >
              <ScrollText size={14} />
              <span>{formatState(item.state, text)}</span>
              <strong>{item.tool_call_count} {text.units.tools}</strong>
              <small>{shortId(item.run_id)}</small>
            </button>
          ))}
        </div>
      )}
      {loading ? (
        <span className="muted">{text.trace.loading}</span>
      ) : !trace ? (
        <span className="muted">{text.trace.empty}</span>
      ) : (
        <>
          <article className="traceSummary">
            <div className="approvalTop">
              <strong>{formatState(trace.run.state, text)}</strong>
              <span className="pill">{shortId(trace.run.id)}</span>
            </div>
            <dl className="statusGrid compact">
              <dt>{text.trace.lane}</dt>
              <dd>{trace.model.lane}</dd>
              <dt>{text.trace.model}</dt>
              <dd>{trace.model.model}</dd>
              <dt>{text.trace.calls}</dt>
              <dd>{trace.model_calls?.length ?? 0}</dd>
              <dt>{text.trace.tokens}</dt>
              <dd>{(trace.model_calls ?? []).reduce((sum, call) => sum + call.total_tokens, 0)}</dd>
              <dt>{text.trace.latency}</dt>
              <dd>{formatLatency(trace.model_calls, text)}</dd>
              <dt>{text.trace.risk}</dt>
              <dd>{formatRisk(trace.run.risk, text)}</dd>
              <dt>{text.trace.tools}</dt>
              <dd>{trace.tool_calls.length}</dd>
              <dt>{text.trace.approvals}</dt>
              <dd>{trace.approvals.length}</dd>
              <dt>{text.trace.feedback}</dt>
              <dd>{trace.feedback?.length ?? 0}</dd>
              <dt>{text.trace.audit}</dt>
              <dd>{trace.audit.length}</dd>
            </dl>
          </article>
          <article className="traceSummary">
            <strong>{text.trace.modelNote}</strong>
            <p>{trace.model.content}</p>
          </article>
          {trace.model_calls && trace.model_calls.length > 0 && (
            <div className="traceList">
              {trace.model_calls.map((call) => (
                <article className="traceRow" key={call.id}>
                  <span>{call.operation} · {call.lane}</span>
                  <small>
                    {formatState(call.status, text)} · {call.total_tokens} {text.units.tokens} · {call.latency_ms} ms
                  </small>
                </article>
              ))}
            </div>
          )}
          {trace.episode && <EpisodeCard episode={trace.episode} compact text={text} />}
          {trace.feedback && trace.feedback.length > 0 && (
            <div className="traceList">
              {trace.feedback.map((feedback) => (
                <article className="traceRow" key={feedback.id}>
                  <span>{feedback.rating}</span>
                  <small>{feedback.correction || feedback.note || shortId(feedback.message_id || feedback.run_id)}</small>
                </article>
              ))}
            </div>
          )}
          <div className="traceList">
            {trace.tool_calls.map((call) => (
              <article className="traceRow" key={call.id}>
                <span>{call.tool}</span>
                <small>{call.observation_summary || formatState(call.status, text)}</small>
              </article>
            ))}
          </div>
          <span className="muted">{formatTime(trace.run.started_at, language)}</span>
        </>
      )}
    </div>
  );
}

function ApprovalPanel({
  approvals,
  text,
  onResolve,
  onModify
}: {
  approvals: Approval[];
  text: Copy;
  onResolve: (id: string, accepted: boolean) => void;
  onModify: (id: string, args: Record<string, unknown>) => void;
}) {
  const [editing, setEditing] = useState("");
  const [draft, setDraft] = useState("");
  const [parseError, setParseError] = useState("");

  function startEdit(approval: Approval) {
    setEditing(approval.id);
    setDraft(JSON.stringify(stripSystemArgs(approval.arguments), null, 2));
    setParseError("");
  }

  function saveEdit(id: string) {
    try {
      const parsed = JSON.parse(draft) as Record<string, unknown>;
      onModify(id, parsed);
      setEditing("");
      setParseError("");
    } catch {
      setParseError(text.approval.invalidJson);
    }
  }

  return (
    <div className="panelStack">
      <SectionHeader icon={<Inbox size={17} />} title={text.approval.title} />
      {approvals.length === 0 ? (
        <span className="muted">{text.approval.empty}</span>
      ) : (
        approvals.map((approval) => (
          <article className={`approvalItem ${approval.risk}`} key={approval.id}>
            <div className="approvalTop">
              <strong>{approval.summary}</strong>
              <RiskPill risk={approval.risk} text={text} />
            </div>
            <p>{approval.reason}</p>
            {approval.resources.length > 0 && (
              <div className="evalCases">
                {approval.resources.map((resource) => (
                  <span key={resource}>{resource}</span>
                ))}
              </div>
            )}
            <JsonBlock value={stripSystemArgs(approval.arguments)} />
            {approval.status === "pending" ? (
              <>
                {editing === approval.id && (
                  <div className="approvalEdit">
                    <textarea value={draft} onChange={(event) => setDraft(event.target.value)} />
                    {parseError && <span className="compactError">{parseError}</span>}
                  </div>
                )}
                <div className="buttonRow">
                  <button className="approve" onClick={() => onResolve(approval.id, true)} title={text.common.approve}>
                    <Check size={16} />
                  </button>
                  <button className="edit" onClick={() => (editing === approval.id ? saveEdit(approval.id) : startEdit(approval))} title={text.approval.editArguments}>
                    <Pencil size={15} />
                  </button>
                  <button className="reject" onClick={() => onResolve(approval.id, false)} title={text.common.reject}>
                    <X size={16} />
                  </button>
                </div>
              </>
            ) : (
              <span className="resolved">{formatState(approval.status, text)}</span>
            )}
          </article>
        ))
      )}
    </div>
  );
}

function MemoryPanel({
  candidates,
  memories,
  text,
  onResolve,
  onUpdate,
  onDelete,
  onExport
}: {
  candidates: MemoryCandidate[];
  memories: Memory[];
  text: Copy;
  onResolve: (id: string, accepted: boolean) => void;
  onUpdate: (id: string, kind: string, content: string) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
  onExport: () => Promise<void>;
}) {
  const [editingId, setEditingId] = useState("");
  const [editKind, setEditKind] = useState("");
  const [editContent, setEditContent] = useState("");
  const [savingId, setSavingId] = useState("");
  const [exporting, setExporting] = useState(false);

  function startEdit(memory: Memory) {
    setEditingId(memory.id);
    setEditKind(memory.kind);
    setEditContent(memory.content);
  }

  function cancelEdit() {
    setEditingId("");
    setEditKind("");
    setEditContent("");
  }

  async function saveEdit(memory: Memory) {
    if (!editKind.trim() || !editContent.trim() || savingId) return;
    setSavingId(memory.id);
    try {
      await onUpdate(memory.id, editKind.trim(), editContent.trim());
      cancelEdit();
    } catch {
      return;
    } finally {
      setSavingId("");
    }
  }

  async function removeMemory(memory: Memory) {
    if (savingId) return;
    setSavingId(memory.id);
    try {
      await onDelete(memory.id);
      if (editingId === memory.id) cancelEdit();
    } catch {
      return;
    } finally {
      setSavingId("");
    }
  }

  async function archiveExport() {
    if (exporting) return;
    setExporting(true);
    try {
      await onExport();
    } catch {
      return;
    } finally {
      setExporting(false);
    }
  }

  return (
    <div className="panelStack">
      <SectionHeader icon={<MemoryStick size={17} />} title={text.memory.title} />
      {candidates.length === 0 ? (
        <span className="muted">{text.memory.emptyCandidates}</span>
      ) : (
        candidates.map((candidate) => (
          <article className="approvalItem" key={candidate.id}>
            <div className="approvalTop">
              <strong>{candidate.kind}</strong>
              <span className="pill">{candidate.sensitivity}</span>
            </div>
            <p>{candidate.content}</p>
            {candidate.status === "pending" ? (
              <div className="buttonRow">
                <button className="approve" onClick={() => onResolve(candidate.id, true)} title={text.memory.acceptMemory}>
                  <Check size={16} />
                </button>
                <button className="reject" onClick={() => onResolve(candidate.id, false)} title={text.memory.rejectMemory}>
                  <X size={16} />
                </button>
              </div>
            ) : (
              <span className="resolved">{formatState(candidate.status, text)}</span>
            )}
          </article>
        ))
      )}
      <div className="sectionHeader smallHeader">
        <Database size={15} />
        <h2>{text.memory.accepted}</h2>
        <button className="miniIconButton headerAction" onClick={() => void archiveExport()} disabled={exporting} title={text.memory.archiveExport}>
          <Download size={14} />
        </button>
      </div>
      <dl className="statusGrid compact memoryCounts">
        <dt>{text.memory.accepted}</dt>
        <dd>{memories.length}</dd>
        <dt>{text.memory.pending}</dt>
        <dd>{candidates.filter((candidate) => candidate.status === "pending").length}</dd>
      </dl>
      {memories.map((memory) => (
        <article className="memoryItem" key={memory.id}>
          {editingId === memory.id ? (
            <div className="memoryEdit">
              <input
                aria-label={text.memory.kind}
                value={editKind}
                onChange={(event) => setEditKind(event.target.value)}
                disabled={savingId === memory.id}
              />
              <textarea
                aria-label={text.memory.content}
                value={editContent}
                onChange={(event) => setEditContent(event.target.value)}
                disabled={savingId === memory.id}
              />
              <div className="buttonRow">
                <button
                  className="approve"
                  onClick={() => void saveEdit(memory)}
                  disabled={!editKind.trim() || !editContent.trim() || savingId === memory.id}
                  title={text.memory.saveMemory}
                >
                  <Check size={16} />
                </button>
                <button className="edit" onClick={cancelEdit} disabled={savingId === memory.id} title={text.memory.cancelEdit}>
                  <X size={16} />
                </button>
              </div>
            </div>
          ) : (
            <>
              <div className="approvalTop">
                <strong>{memory.kind}</strong>
                <div className="buttonRow compactButtons">
                  <button className="edit" onClick={() => startEdit(memory)} disabled={savingId === memory.id} title={text.memory.editMemory}>
                    <Pencil size={15} />
                  </button>
                  <button className="reject" onClick={() => void removeMemory(memory)} disabled={savingId === memory.id} title={text.memory.deleteMemory}>
                    <Trash2 size={15} />
                  </button>
                </div>
              </div>
              <p>{memory.content}</p>
            </>
          )}
        </article>
      ))}
    </div>
  );
}

function StatusStack({
  ready,
  modelCalls,
  auditEvents,
  artifacts,
  episodes,
  evalRun,
  evalRuns,
  skills,
  text,
  language,
  onRunEval,
  onSelectEval,
  onError
}: {
  ready: ReadyStatus | null;
  modelCalls: ModelCall[];
  auditEvents: AuditEvent[];
  artifacts: ArtifactObject[];
  episodes: EpisodeSummary[];
  evalRun: EvalRun | null;
  evalRuns: EvalRun[];
  skills: Skill[];
  text: Copy;
  language: Language;
  onRunEval: () => Promise<void>;
  onSelectEval: (id: string) => Promise<void>;
  onError: (message: string) => void;
}) {
  return (
    <div className="panelStack">
      <StatusPanel ready={ready} modelCalls={modelCalls} auditEvents={auditEvents} text={text} />
      <ArtifactPanel artifacts={artifacts} text={text} />
      <EpisodePanel episodes={episodes} text={text} />
      <EvalPanel evalRun={evalRun} evalRuns={evalRuns} text={text} language={language} onRun={onRunEval} onSelect={onSelectEval} onError={onError} />
      <SkillsPanel skills={skills} text={text} />
    </div>
  );
}

function StatusPanel({ ready, modelCalls, auditEvents, text }: { ready: ReadyStatus | null; modelCalls: ModelCall[]; auditEvents: AuditEvent[]; text: Copy }) {
  const recentModelCalls = modelCalls.slice(-6).reverse();
  const recentAuditEvents = auditEvents.slice(-6).reverse();
  return (
    <div className="panelStack nestedPanel">
      <SectionHeader icon={<Clock3 size={17} />} title={text.status.runtime} />
      {!ready ? (
        <span className="muted">{text.common.gatewayUnavailable}</span>
      ) : (
        <dl className="statusGrid">
          <dt>{text.status.gateway}</dt>
          <dd>{ready.gateway_binding}</dd>
          <dt>{text.status.model}</dt>
          <dd>{ready.model_mode}</dd>
          <dt>{text.status.rateLimit}</dt>
          <dd>{rateLimitLabel(ready.rate_limit, text)}</dd>
          <dt>{text.status.workspace}</dt>
          <dd>{ready.workspace_root}</dd>
          <dt>{text.status.trace}</dt>
          <dd>{ready.trace_dir}</dd>
          <dt>{text.status.state}</dt>
          <dd>{ready.state_backend} · {ready.state_path}</dd>
          {ready.state_dsn && (
            <>
              <dt>{text.status.dsn}</dt>
              <dd>{ready.state_dsn}</dd>
            </>
          )}
        </dl>
      )}
      <div className="diagnosticList">
        <strong>{text.status.modelCalls}</strong>
        {recentModelCalls.length === 0 ? (
          <span className="muted">{text.status.noModelCalls}</span>
        ) : (
          recentModelCalls.map((call) => (
            <article className="diagnosticRow" key={call.id}>
              <div>
                <span>{call.operation} · {call.lane}</span>
                <small>{call.profile || call.model}</small>
              </div>
              <small>
                {formatState(call.status, text)} · {call.total_tokens} {text.units.tokens} · {call.latency_ms} ms
              </small>
            </article>
          ))
        )}
      </div>
      <div className="diagnosticList">
        <strong>{text.status.audit}</strong>
        {recentAuditEvents.length === 0 ? (
          <span className="muted">{text.status.noAudit}</span>
        ) : (
          recentAuditEvents.map((event) => (
            <article className="diagnosticRow" key={event.id}>
              <div>
                <span>{event.type}</span>
                <small>{event.actor}</small>
              </div>
              <small>{event.summary}</small>
            </article>
          ))
        )}
      </div>
    </div>
  );
}

function ArtifactPanel({ artifacts, text }: { artifacts: ArtifactObject[]; text: Copy }) {
  return (
    <div className="panelStack nestedPanel">
      <SectionHeader icon={<Database size={17} />} title={text.status.artifacts} />
      {artifacts.length === 0 ? (
        <span className="muted">{text.status.noArtifacts}</span>
      ) : (
        <div className="artifactList">
          {artifacts.slice(0, 5).map((artifact) => (
            <article className="artifactItem" key={artifact.id} title={artifact.path || artifact.uri}>
              <div className="approvalTop">
                <strong>{artifact.kind}</strong>
                <span className="pill">{artifact.backend}</span>
              </div>
              <span>{artifact.uri}</span>
              <small>{artifact.bytes} {text.units.bytes} · {artifact.content_type}</small>
            </article>
          ))}
        </div>
      )}
    </div>
  );
}

function EpisodePanel({ episodes, text }: { episodes: EpisodeSummary[]; text: Copy }) {
  return (
    <div className="panelStack nestedPanel">
      <SectionHeader icon={<ScrollText size={17} />} title={text.status.episodes} />
      {episodes.length === 0 ? (
        <span className="muted">{text.status.noEpisodes}</span>
      ) : (
        episodes.slice(0, 5).map((episode) => <EpisodeCard key={episode.id} episode={episode} text={text} />)
      )}
    </div>
  );
}

function EpisodeCard({ episode, text, compact = false }: { episode: EpisodeSummary; text: Copy; compact?: boolean }) {
  return (
    <article className={`episodeItem ${compact ? "compactEpisode" : ""}`}>
      <div className="approvalTop">
        <strong>{episode.outcome}</strong>
        <span className="pill">{shortId(episode.run_id)}</span>
      </div>
      <p>{episode.summary}</p>
      <dl className="statusGrid compact">
        <dt>{text.trace.lane}</dt>
        <dd>{episode.model_lane}</dd>
        <dt>{text.trace.risk}</dt>
        <dd>{formatRisk(episode.risk, text)}</dd>
        <dt>Repair</dt>
        <dd>{episode.repair_performed ? text.common.yes : text.common.no}</dd>
      </dl>
      {episode.tools.length > 0 && (
        <div className="evalCases">
          {episode.tools.slice(0, compact ? 4 : 8).map((tool) => (
            <span key={tool}>{tool}</span>
          ))}
        </div>
      )}
      {episode.failures && episode.failures.length > 0 && (
        <div className="evalCases">
          {episode.failures.slice(0, 3).map((failure) => (
            <span className="failed" key={failure}>
              {failure}
            </span>
          ))}
        </div>
      )}
    </article>
  );
}

function EvalPanel({
  evalRun,
  evalRuns,
  text,
  language,
  onRun,
  onSelect,
  onError
}: {
  evalRun: EvalRun | null;
  evalRuns: EvalRun[];
  text: Copy;
  language: Language;
  onRun: () => Promise<void>;
  onSelect: (id: string) => Promise<void>;
  onError: (message: string) => void;
}) {
  const [running, setRunning] = useState(false);
  const [loadingId, setLoadingId] = useState("");
  async function run() {
    try {
      setRunning(true);
      await onRun();
    } catch (err) {
      onError(err instanceof Error ? err.message : text.errors.eval);
    } finally {
      setRunning(false);
    }
  }
  async function select(id: string) {
    try {
      setLoadingId(id);
      await onSelect(id);
    } catch (err) {
      onError(err instanceof Error ? err.message : text.errors.eval);
    } finally {
      setLoadingId("");
    }
  }
  return (
    <div className="panelStack nestedPanel evalPanel">
      <SectionHeader icon={<ListChecks size={17} />} title={text.status.smokeEval} />
      <button className="secondaryButton" onClick={() => void run()} disabled={running} title={text.status.smokeEval}>
        <ListChecks size={16} />
        <span>{running ? text.common.running : text.common.run}</span>
      </button>
      {!evalRun ? (
        <span className="muted">{text.status.noEval}</span>
      ) : (
        <article className={`evalResult ${evalRun.status}`}>
          <div className="approvalTop">
            <strong>{formatState(evalRun.status, text)}</strong>
            <span className="pill">{shortId(evalRun.id)}</span>
          </div>
          <p>{evalRun.summary}</p>
          <div className="evalCases">
            {evalRun.cases.map((item) => (
              <span key={item.name} className={item.status}>
                {item.name}
              </span>
            ))}
          </div>
          {evalRun.failure_archives && evalRun.failure_archives.length > 0 && (
            <div className="archiveList">
              {evalRun.failure_archives.map((archive) => (
                <span key={`${archive.case_name}-${archive.uri}`} title={archive.path || archive.uri}>
                  {archive.case_name}: {archive.uri}
                </span>
              ))}
            </div>
          )}
        </article>
      )}
      {evalRuns.length > 0 && (
        <div className="evalHistory">
          {evalRuns.slice(0, 5).map((runItem) => (
            <button
              key={runItem.id}
              className={evalRun?.id === runItem.id ? "selected" : ""}
              onClick={() => void select(runItem.id)}
              disabled={loadingId === runItem.id}
              title={`${runItem.profile} ${shortId(runItem.id)}`}
            >
              <Clock3 size={14} />
              <span>{runItem.profile}</span>
              <strong className={runItem.status}>{formatState(runItem.status, text)}</strong>
              <small>{formatTime(runItem.started_at, language)}</small>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function SkillsPanel({ skills, text }: { skills: Skill[]; text: Copy }) {
  return (
    <div className="panelStack nestedPanel">
      <SectionHeader icon={<Library size={17} />} title={text.status.skills} />
      {skills.length === 0 ? (
        <span className="muted">{text.status.noSkills}</span>
      ) : (
        skills.map((skill) => (
          <article className="skillItem" key={skill.name}>
            <div className="approvalTop">
              <strong>{skill.name}</strong>
              <span className="pill">{skill.risk_level}</span>
            </div>
            <p>{skill.description}</p>
            <div className="evalCases">
              {skill.allowed_tools.slice(0, 4).map((tool) => (
                <span key={tool}>{tool}</span>
              ))}
            </div>
            {(skill.dependencies.length > 0 || skill.eval_cases.length > 0) && (
              <div className="skillMeta">
                {skill.dependencies.length > 0 && <small>{skill.dependencies.length} {text.units.deps}</small>}
                {skill.eval_cases.length > 0 && <small>{skill.eval_cases.length} {text.units.evals}</small>}
                {skill.input_schema && <small>{text.units.schema}</small>}
              </div>
            )}
          </article>
        ))
      )}
    </div>
  );
}

function SettingsPanel({
  runtimeConfig,
  ownerProfile,
  clients,
  text,
  language,
  onUpdateOwner,
  onRevokeClient,
  onUpdatePolicy
}: {
  runtimeConfig: PublicConfig | null;
  ownerProfile: OwnerProfile | null;
  clients: Client[];
  text: Copy;
  language: Language;
  onUpdateOwner: (displayName: string, email: string, preferences: Record<string, string>) => Promise<void>;
  onRevokeClient: (id: string) => Promise<void>;
  onUpdatePolicy: (deny: string[], approvalRequired: string[]) => Promise<void>;
}) {
  const [editingOwner, setEditingOwner] = useState(false);
  const [ownerName, setOwnerName] = useState("");
  const [ownerEmail, setOwnerEmail] = useState("");
  const [ownerPrefsText, setOwnerPrefsText] = useState("");
  const [ownerError, setOwnerError] = useState("");
  const [savingOwner, setSavingOwner] = useState(false);
  const [editingPolicy, setEditingPolicy] = useState(false);
  const [denyText, setDenyText] = useState("");
  const [approvalText, setApprovalText] = useState("");
  const [savingPolicy, setSavingPolicy] = useState(false);
  const [revokingClient, setRevokingClient] = useState("");

  if (!runtimeConfig) {
    return (
      <div className="panelStack">
        <SectionHeader icon={<Settings size={17} />} title={text.settings.title} />
        <span className="muted">{text.settings.unavailable}</span>
      </div>
    );
  }
  const policy = runtimeConfig.tool_policy;
  const riskCounts = Object.entries(policy.risk_counts).sort(([left], [right]) => left.localeCompare(right));
  const preferences = ownerProfile?.preferences ?? {};

  function startOwnerEdit() {
    setOwnerName(ownerProfile?.display_name ?? "");
    setOwnerEmail(ownerProfile?.email ?? "");
    setOwnerPrefsText(formatPreferences(preferences));
    setOwnerError("");
    setEditingOwner(true);
  }

  function cancelOwnerEdit() {
    setEditingOwner(false);
    setOwnerName("");
    setOwnerEmail("");
    setOwnerPrefsText("");
    setOwnerError("");
  }

  async function saveOwnerEdit() {
    if (savingOwner) return;
    setSavingOwner(true);
    setOwnerError("");
    try {
      await onUpdateOwner(ownerName, ownerEmail, parsePreferences(ownerPrefsText, text));
      cancelOwnerEdit();
    } catch (err) {
      setOwnerError(err instanceof Error ? err.message : text.errors.ownerUpdate);
    } finally {
      setSavingOwner(false);
    }
  }

  function startPolicyEdit() {
    setDenyText(policy.denied_tools.join("\n"));
    setApprovalText(policy.configured_approval_required_tools.join("\n"));
    setEditingPolicy(true);
  }

  function cancelPolicyEdit() {
    setEditingPolicy(false);
    setDenyText("");
    setApprovalText("");
  }

  async function savePolicyEdit() {
    if (savingPolicy) return;
    setSavingPolicy(true);
    try {
      await onUpdatePolicy(parseToolList(denyText), parseToolList(approvalText));
      cancelPolicyEdit();
    } catch {
      return;
    } finally {
      setSavingPolicy(false);
    }
  }

  async function revokeClient(id: string) {
    if (revokingClient) return;
    setRevokingClient(id);
    try {
      await onRevokeClient(id);
    } catch {
      return;
    } finally {
      setRevokingClient("");
    }
  }

  return (
    <div className="panelStack">
      <SectionHeader icon={<Settings size={17} />} title={text.settings.title} />
      <article className="settingsBlock">
        <div className="approvalTop">
          <span className="settingsTitle">
            <UserRound size={15} />
            <strong>{text.settings.ownerProfile}</strong>
          </span>
          <div className="buttonRow compactButtons">
            {editingOwner ? (
              <>
                <button className="approve" onClick={() => void saveOwnerEdit()} disabled={savingOwner} title={text.settings.saveOwner}>
                  <Check size={15} />
                </button>
                <button className="edit" onClick={cancelOwnerEdit} disabled={savingOwner} title={text.settings.cancelOwner}>
                  <X size={15} />
                </button>
              </>
            ) : (
              <button className="edit" onClick={startOwnerEdit} title={text.settings.editOwner}>
                <Pencil size={15} />
              </button>
            )}
          </div>
        </div>
        {editingOwner ? (
          <div className="ownerEditor">
            <label>
              <span>{text.settings.name}</span>
              <input value={ownerName} onChange={(event) => setOwnerName(event.target.value)} disabled={savingOwner} />
            </label>
            <label>
              <span>{text.settings.email}</span>
              <input value={ownerEmail} onChange={(event) => setOwnerEmail(event.target.value)} disabled={savingOwner} />
            </label>
            <label>
              <span>{text.settings.preferences}</span>
              <textarea value={ownerPrefsText} onChange={(event) => setOwnerPrefsText(event.target.value)} disabled={savingOwner} />
            </label>
            {ownerError && <span className="compactError">{ownerError}</span>}
          </div>
        ) : ownerProfile ? (
          <>
            <dl className="statusGrid compact">
              <dt>{text.settings.name}</dt>
              <dd>{ownerProfile.display_name}</dd>
              <dt>{text.settings.email}</dt>
              <dd>{ownerProfile.email || text.common.notSet}</dd>
            </dl>
            <div className="evalCases">
              {Object.entries(preferences).map(([key, value]) => (
                <span key={key}>{key}:{value}</span>
              ))}
              {Object.keys(preferences).length === 0 && <span>{text.common.none}</span>}
            </div>
          </>
        ) : (
          <span className="muted">{text.settings.ownerUnavailable}</span>
        )}
      </article>
      <article className="settingsBlock">
        <div className="approvalTop">
          <strong>{text.settings.toolPolicy}</strong>
          <span className="pill">{policy.definition_count} {text.trace.tools}</span>
        </div>
        <dl className="statusGrid compact">
          <dt>{text.settings.file}</dt>
          <dd>{policy.policy_path}</dd>
          <dt>{text.settings.external}</dt>
          <dd>{policy.external_content_untrusted ? text.settings.untrusted : text.settings.trusted}</dd>
          <dt>{text.settings.dangerous}</dt>
          <dd>{policy.approval_required_for_dangerous_tools ? text.settings.approvalRequired : text.settings.notForced}</dd>
          <dt>{text.settings.verifier}</dt>
          <dd>{policy.dangerous_tools_deep_verification ? text.settings.deepCheck : text.settings.standard}</dd>
          <dt>{text.settings.sandbox}</dt>
          <dd>{policy.sandbox_required_for_mutating_tools ? text.settings.mutationsRequireSandbox : text.settings.notForced}</dd>
        </dl>
        <div className="evalCases">
          {riskCounts.map(([risk, count]) => (
            <span key={risk}>{formatRisk(risk, text)}:{count}</span>
          ))}
        </div>
      </article>
      <article className="settingsBlock">
        <div className="approvalTop">
          <strong>{text.settings.pairedClients}</strong>
          <span className="pill">{clients.length}</span>
        </div>
        {clients.length === 0 ? (
          <span className="muted">{text.settings.noClients}</span>
        ) : (
          <div className="clientList">
            {clients.map((client) => (
              <div className="clientItem" key={client.id}>
                <div>
                  <strong>{client.name}</strong>
                  <small>
                    {client.revoked_at
                      ? text.common.revoked
                      : client.last_seen_at
                        ? `${text.settings.seen} ${formatTime(client.last_seen_at, language)}`
                        : text.settings.notSeen}
                  </small>
                </div>
                {!client.revoked_at && (
                  <button className="reject" onClick={() => void revokeClient(client.id)} disabled={revokingClient === client.id} title={text.settings.revokeClient}>
                    <Trash2 size={14} />
                  </button>
                )}
              </div>
            ))}
          </div>
        )}
      </article>
      <article className="settingsBlock">
        <strong>{text.settings.definitionApprovalTools}</strong>
        <div className="evalCases">
          {policy.definition_approval_required_tools.map((tool) => (
            <span key={tool}>{tool}</span>
          ))}
          {policy.definition_approval_required_tools.length === 0 && <span>{text.common.none}</span>}
        </div>
      </article>
      <article className="settingsBlock">
        <strong>{text.settings.configApprovalAdditions}</strong>
        {editingPolicy ? (
          <div className="policyEditor">
            <label>
              <span>{text.settings.approval}</span>
              <textarea value={approvalText} onChange={(event) => setApprovalText(event.target.value)} disabled={savingPolicy} />
            </label>
          </div>
        ) : (
          <div className="evalCases">
            {policy.configured_approval_required_tools.map((tool) => (
              <span key={`configured-${tool}`}>{tool}</span>
            ))}
            {policy.configured_approval_required_tools.length === 0 && <span>{text.common.none}</span>}
          </div>
        )}
      </article>
      <article className="settingsBlock">
        <div className="approvalTop">
          <strong>{text.settings.deniedTools}</strong>
          <div className="buttonRow compactButtons">
            {editingPolicy ? (
              <>
                <button className="approve" onClick={() => void savePolicyEdit()} disabled={savingPolicy} title={text.settings.saveToolPolicy}>
                  <Check size={15} />
                </button>
                <button className="edit" onClick={cancelPolicyEdit} disabled={savingPolicy} title={text.settings.cancelPolicy}>
                  <X size={15} />
                </button>
              </>
            ) : (
              <button className="edit" onClick={startPolicyEdit} title={text.settings.editPolicy}>
                <Pencil size={15} />
              </button>
            )}
          </div>
        </div>
        {editingPolicy ? (
          <div className="policyEditor">
            <label>
              <span>{text.settings.deny}</span>
              <textarea value={denyText} onChange={(event) => setDenyText(event.target.value)} disabled={savingPolicy} />
            </label>
          </div>
        ) : (
          <div className="evalCases">
            {policy.denied_tools.map((tool) => (
              <span className="failed" key={tool}>{tool}</span>
            ))}
            {policy.denied_tools.length === 0 && <span>{text.common.none}</span>}
          </div>
        )}
      </article>
      <article className="settingsBlock">
        <strong>{text.settings.modelProfiles}</strong>
        <dl className="statusGrid compact">
          <dt>{text.settings.mode}</dt>
          <dd>{runtimeConfig.model.mock ? text.settings.mock : text.settings.externalModel}</dd>
          <dt>{text.settings.fast}</dt>
          <dd>{profileLabel(runtimeConfig.model.fast, text)}</dd>
          <dt>{text.settings.deep}</dt>
          <dd>{profileLabel(runtimeConfig.model.deep, text)}</dd>
          <dt>{text.settings.embed}</dt>
          <dd>{profileLabel(runtimeConfig.model.embedding, text)}</dd>
          <dt>{text.settings.rerank}</dt>
          <dd>{profileLabel(runtimeConfig.model.reranker, text)}</dd>
          <dt>{text.settings.guard}</dt>
          <dd>{profileLabel(runtimeConfig.model.guard, text)}</dd>
        </dl>
      </article>
      <article className="settingsBlock">
        <strong>{text.settings.runtimeBoundaries}</strong>
        <dl className="statusGrid compact">
          <dt>{text.status.gateway}</dt>
          <dd>{runtimeConfig.gateway.bind}:{runtimeConfig.gateway.port}</dd>
          <dt>{text.settings.remote}</dt>
          <dd>{runtimeConfig.gateway.remote_access}</dd>
          <dt>{text.status.rateLimit}</dt>
          <dd>{rateLimitLabel(runtimeConfig.gateway.rate_limit, text)}</dd>
          <dt>{text.status.workspace}</dt>
          <dd>{runtimeConfig.workspaces.default_root}</dd>
          <dt>{text.settings.sandbox}</dt>
          <dd>{runtimeConfig.sandbox.enabled ? `${runtimeConfig.sandbox.backend} · ${runtimeConfig.sandbox.network}` : text.common.disabled}</dd>
          <dt>{text.status.state}</dt>
          <dd>
            {runtimeConfig.state.backend} · {runtimeConfig.state.path || runtimeConfig.state.dsn}
            {runtimeConfig.state.encrypt_at_rest ? ` · ${text.settings.encrypted}` : ""}
          </dd>
          <dt>{text.settings.artifacts}</dt>
          <dd>{runtimeConfig.storage.artifact_backend} · {runtimeConfig.storage.artifact_dir || runtimeConfig.storage.artifact_bucket}</dd>
        </dl>
      </article>
      <article className="settingsBlock">
        <strong>{text.settings.adapters}</strong>
        <dl className="statusGrid compact">
          <dt>{text.settings.email}</dt>
          <dd>{adapterLabel(runtimeConfig.adapters.email, text)}</dd>
          <dt>Calendar</dt>
          <dd>{adapterLabel(runtimeConfig.adapters.calendar, text)}</dd>
          <dt>{text.settings.memory}</dt>
          <dd>
            {runtimeConfig.memory.enabled
              ? `${runtimeConfig.memory.write_policy} · ${retentionLabel(runtimeConfig.memory.retention_days, text)}`
              : text.common.disabled}
          </dd>
          <dt>{text.settings.skills}</dt>
          <dd>{runtimeConfig.skills.dirs.join(", ")}</dd>
        </dl>
      </article>
    </div>
  );
}

function SectionHeader({ icon, title }: { icon: React.ReactNode; title: string }) {
  return (
    <div className="sectionHeader">
      {icon}
      <h2>{title}</h2>
    </div>
  );
}

function JsonBlock({ value }: { value: unknown }) {
  const raw = JSON.stringify(value, null, 2);
  async function copy() {
    await navigator.clipboard?.writeText(raw).catch(() => undefined);
  }
  return (
    <div className="jsonBlock">
      <button className="miniIconButton jsonCopy" onClick={() => void copy()} title="Copy JSON">
        <Copy size={13} />
      </button>
      <pre>{raw}</pre>
    </div>
  );
}

function RiskPill({ risk, text }: { risk: string; text: Copy }) {
  return <span className={`riskPill ${risk}`}>{formatRisk(risk, text)}</span>;
}

function initialLanguage(): Language {
  const stored = window.localStorage.getItem(LANGUAGE_STORAGE_KEY);
  if (stored === "en" || stored === "zh") return stored;
  return window.navigator.language.toLowerCase().startsWith("zh") ? "zh" : "en";
}

function stripSystemArgs(args: Record<string, unknown>) {
  return Object.fromEntries(Object.entries(args).filter(([key]) => !key.startsWith("_")));
}

function profileLabel(profile: PublicConfig["model"]["fast"], text: Copy) {
  const model = profile.model || profile.name;
  const maxTokens = profile.max_tokens ? ` · ${profile.max_tokens.toLocaleString()} ${text.units.max}` : "";
  return `${profile.name} · ${model} · ${profile.context_tokens.toLocaleString()} ${text.units.ctx}${maxTokens}${profile.mtp ? " · MTP" : ""}`;
}

function adapterLabel(adapter: { backend: string; base_url: string; token: string }, text: Copy) {
  const target = adapter.base_url || text.settings.localFiles;
  const token = adapter.token ? text.common.tokenConfigured : text.common.noToken;
  return `${adapter.backend} · ${target} · ${token}`;
}

function rateLimitLabel(limit: { enabled: boolean; requests_per_minute: number; burst: number } | undefined, text: Copy) {
  if (!limit?.enabled) return text.common.disabled;
  return `${limit.requests_per_minute}/min · burst ${limit.burst}`;
}

function retentionLabel(days: number, text: Copy) {
  if (!days || days <= 0) return text.settings.noAutoPrune;
  return `${days}${text.units.retentionDays}`;
}

function parseToolList(value: string) {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of value.split(/[\n,]/)) {
    const tool = raw.trim();
    if (!tool || seen.has(tool)) continue;
    seen.add(tool);
    out.push(tool);
  }
  return out;
}

function formatPreferences(preferences: Record<string, string>) {
  return Object.entries(preferences)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => `${key}=${value}`)
    .join("\n");
}

function parsePreferences(value: string, text: Copy) {
  const preferences: Record<string, string> = {};
  for (const line of value.split(/\n/)) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const separator = trimmed.indexOf("=");
    if (separator === -1) {
      throw new Error(text.settings.preferenceFormat);
    }
    const key = trimmed.slice(0, separator).trim();
    const itemValue = trimmed.slice(separator + 1).trim();
    if (!key) {
      throw new Error(text.settings.preferenceKey);
    }
    preferences[key] = itemValue;
  }
  return preferences;
}

function formatRisk(risk: string, text: Copy) {
  return text.risk[risk as keyof Copy["risk"]] ?? risk;
}

function formatState(state: string, text: Copy) {
  return text.state[state as keyof Copy["state"]] ?? state;
}

function shortId(id: string) {
  return id.slice(0, 10);
}

function cssToken(value: string) {
  return value.toLowerCase().replace(/[^a-z0-9_-]+/g, "-");
}

function formatLatency(calls: ModelCall[] | undefined, text: Copy) {
  if (!calls || calls.length === 0) return "0 ms";
  const total = calls.reduce((sum, call) => sum + call.latency_ms, 0);
  return `${Math.round(total / calls.length)} ms ${text.units.avg}`;
}

function formatTime(value: string, language: Language) {
  return new Intl.DateTimeFormat(language === "zh" ? "zh-CN" : "en-US", { hour: "2-digit", minute: "2-digit" }).format(new Date(value));
}
