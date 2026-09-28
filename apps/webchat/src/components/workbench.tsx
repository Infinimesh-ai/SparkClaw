import { useEffect, useRef, useState } from "react";
import { ArrowRight, X } from "lucide-react";
import type { Language } from "../i18n";
import type { Session } from "../api/types";
import workbenchMark from "../../../desktop/src/assets/icon.png";

export type WorkspacePage = "chat" | "schedules" | "channels" | "memory" | "approvals" | "settings";
export const workbenchCopy = {
  zh: {
    newTask: "新任务", search: "搜索任务", recent: "最近任务",
    schedules: "日程", channels: "通讯工具", memory: "记忆", approvals: "审批中心", settings: "工作区设置",
    greeting: "你的本地智能体，随时待命", headline: "我们要做什么", outcome: "",
    steps: "每一步，都有迹可循", reliable: "本地理解，可靠执行", resume: "接着上次的思路", all: "查看全部",
    empty: "还没有任务，从一个想法开始。", noMatches: "没有匹配的任务，试试其他关键词。",
    stepTitles: ["本地理解", "网络搜索", "逻辑判断", "交付结果"],
    stepDescriptions: ["理解你的文件、偏好与目标", "连接公开信息，补全上下文", "拆解任务，形成执行计划", "留下结果，也留下完整过程"],
    pageTitles: { schedules: "让日常，自动发生。", channels: "所有连接，集中管理。", memory: "越了解你，越得心应手。", approvals: "关键操作，由你决定。", settings: "工作区设置" },
    pageDescriptions: { schedules: "安排一次，按时执行。把重复的事留给 SparkClaw。", channels: "集中管理消息、浏览器、服务与外部工具连接。", memory: "你决定 SparkClaw 记住什么。随时查看、编辑或移除。", approvals: "先查看影响范围，再允许 SparkClaw 继续。", settings: "按你的习惯，安排 SparkClaw 的工作方式。" },
    local: "本地工作区", connectGateway: "连接你的网关", connectGatewayDescription: "配对本地运行环境以启用模型和工具。", pairRuntime: "配对运行环境", inspector: "任务检查", toggleNav: "切换侧栏", toggleInspector: "切换检查面板", newSchedule: "新建定时任务", addMemory: "添加记忆", memoryPrompt: "请记住：",
    general: "常规", appearance: "外观", modelsTools: "模型与工具", permissions: "权限", connections: "连接", diagnostics: "诊断", backToApp: "返回应用", searchSettings: "搜索设置…", settingsNavigation: "设置导航", settingsGroup: "工作区", noSettingsMatches: "没有匹配的设置",
    settingsGroups: { workspace: "工作区", agent: "智能体", context: "上下文", activity: "活动" },
    settingsTitles: { settings: "常规", appearance: "外观", "models-tools": "模型与工具", permissions: "权限", connections: "连接", approvals: "审批", memory: "记忆", timeline: "时间线", trace: "追踪", status: "诊断" },
    settingsDescriptions: {
      settings: "管理当前工作区中已实际生效的通用设置。",
      appearance: "调整界面的主题和文字大小。",
      "models-tools": "管理任务可以使用的工具。",
      permissions: "查看并调整工具的审批与拒绝策略。",
      connections: "管理可用的消息渠道与服务。",
      approvals: "查看等待你决定的操作。",
      memory: "检查 SparkClaw 可以在任务之间保留的内容。",
      timeline: "查看此工作区最近的工具调用与决策。",
      trace: "检查所选任务的模型与工具执行细节。",
      status: "查看运行环境、服务、产物与评测状态。"
    }
  },
  en: {
    newTask: "New task", search: "Search tasks", recent: "Recent tasks",
    schedules: "Schedule", channels: "Connections", memory: "Memory", approvals: "Approvals", settings: "Workspace settings",
    greeting: "Your local agent, ready when you are", headline: "What should we do?", outcome: "",
    steps: "Every step, in the open", reliable: "Local context. Reliable execution.", resume: "Pick up where you left off", all: "View all",
    empty: "No tasks yet. Start with an idea.", noMatches: "No matching tasks. Try another keyword.",
    stepTitles: ["Local context", "Web search", "Reasoning", "Deliver results"],
    stepDescriptions: ["Understand your files, preferences, and goals", "Find public information and complete the context", "Break down tasks into a clear execution plan", "Keep the results and the full execution history"],
    pageTitles: { schedules: "Make everyday work automatic.", channels: "All connections, one place.", memory: "An assistant that gets to know you.", approvals: "Important actions are your decision.", settings: "Workspace settings" },
    pageDescriptions: { schedules: "Plan once. Let SparkClaw handle the repetition.", channels: "Manage messaging, browser, service, and external tool connections in one place.", memory: "Choose what SparkClaw remembers. Review, edit, or remove it anytime.", approvals: "Review the impact before allowing SparkClaw to continue.", settings: "Make SparkClaw work the way you do." },
    local: "Local workspace", connectGateway: "Connect your gateway", connectGatewayDescription: "Pair a local runtime to enable models and tools.", pairRuntime: "Pair runtime", inspector: "Task inspector", toggleNav: "Toggle sidebar", toggleInspector: "Toggle inspector", newSchedule: "New scheduled task", addMemory: "Add memory", memoryPrompt: "Please remember: ",
    general: "General", appearance: "Appearance", modelsTools: "Models & tools", permissions: "Permissions", connections: "Connections", diagnostics: "Diagnostics", backToApp: "Back to app", searchSettings: "Search settings…", settingsNavigation: "Settings navigation", settingsGroup: "Workspace", noSettingsMatches: "No matching settings",
    settingsGroups: { workspace: "Workspace", agent: "Agent", context: "Context", activity: "Activity" },
    settingsTitles: { settings: "General", appearance: "Appearance", "models-tools": "Models & tools", permissions: "Permissions", connections: "Connections", approvals: "Approvals", memory: "Memory", timeline: "Timeline", trace: "Trace", status: "Diagnostics" },
    settingsDescriptions: {
      settings: "Manage general settings that are supported by the current workspace.",
      appearance: "Adjust the interface theme and text size.",
      "models-tools": "Manage the tools available to tasks.",
      permissions: "Review and change tool approval and denial policy.",
      connections: "Manage available messaging channels and services.",
      approvals: "Review actions waiting for your decision.",
      memory: "Review what SparkClaw can carry across tasks.",
      timeline: "Inspect recent tool calls and decisions across this workspace.",
      trace: "Inspect model and tool execution details for the selected run.",
      status: "Review runtime health, services, artifacts, and evaluations."
    }
  }
};

export function WorkbenchWelcome({ language }: { language: Language }) {
  const copy = workbenchCopy[language];
  return <div className="workbenchWelcome">
    <img className="welcomeBrandMark" src={workbenchMark} alt="" aria-hidden="true" />
    <h1>{copy.headline}</h1>
  </div>;
}

export function TaskSearch({ language, sessions, onSelect, onClose }: {
  language: Language; sessions: Session[]; onSelect: (session: Session) => void; onClose: () => void;
}) {
  const copy = workbenchCopy[language];
  const dialogRef = useRef<HTMLDialogElement>(null);
  const [query, setQuery] = useState("");
  useEffect(() => {
    const dialog = dialogRef.current;
    dialog?.showModal();
    dialog?.querySelector("input")?.focus();
    return () => dialog?.close();
  }, []);
  const matches = sessions.filter(session => session.source !== "mcp" && session.title.toLocaleLowerCase().includes(query.toLocaleLowerCase()));
  return <dialog ref={dialogRef} className="taskSearchDialog" aria-label={copy.search} onCancel={event => { event.preventDefault(); onClose(); }} onClick={event => { if (event.target === event.currentTarget) { const box = event.currentTarget.getBoundingClientRect(); if (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom) onClose(); } }}>
    <div className="taskInspectorHeader">{copy.search}<button className="iconButton" onClick={onClose} aria-label={language === "zh" ? "关闭" : "Close"}><X size={18} /></button></div>
    <input autoFocus type="search" aria-label={copy.search} placeholder={copy.search} value={query} onChange={event => setQuery(event.target.value)} />
    <div className="taskSearchResults">{matches.map(session => <button key={session.id} onClick={() => onSelect(session)}>{session.title}<ArrowRight size={16} /></button>)}{matches.length === 0 && <p>{copy.noMatches}</p>}</div>
  </dialog>;
}
