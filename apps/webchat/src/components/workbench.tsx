import { useEffect, useRef, useState } from "react";
import { ArrowRight, Clock3, FileText, Folder, GitBranch, Globe, Search, Sparkles, X } from "lucide-react";
import type { Language } from "../i18n";
import type { Session } from "../api/types";

export type WorkspacePage = "chat" | "schedules" | "channels" | "memory" | "approvals" | "settings";
export const workbenchCopy = {
  zh: {
    workspace: "个人工作区", home: "工作台", newTask: "新任务", search: "搜索任务", recent: "最近任务",
    schedules: "定时任务", channels: "通讯工具", memory: "记忆", approvals: "审批中心", settings: "工作区设置",
    greeting: "你的本地智能体，随时待命", headline: "从一个想法，", outcome: "到一件完成的事。",
    intro: "搜索、研究、整理与执行。交给 SparkClaw，进展尽在掌握。",
    steps: "每一步，都有迹可循", reliable: "本地理解，可靠执行", resume: "接着上次的思路", all: "查看全部",
    empty: "还没有任务，从一个想法开始。", noMatches: "没有匹配的任务，试试其他关键词。",
    suggestions: ["研究一个课题", "让浏览器帮忙", "整理本地文件", "安排定时任务"],
    prompts: ["帮我研究一个课题：", "请用浏览器帮我查找并对比：", "帮我整理本地文件，先给出计划并等待确认：", "请帮我创建定时任务："],
    stepTitles: ["本地理解", "网络搜索", "逻辑判断", "交付结果"],
    stepDescriptions: ["理解你的文件、偏好与目标", "连接公开信息，补全上下文", "拆解任务，形成执行计划", "留下结果，也留下完整过程"],
    pageTitles: { schedules: "让日常，自动发生。", channels: "在你常用的地方，找到助手。", memory: "越了解你，越得心应手。", approvals: "关键操作，由你决定。", settings: "工作区设置" },
    pageDescriptions: { schedules: "安排一次，按时执行。把重复的事留给 SparkClaw。", channels: "连接通讯工具，让任务与结果在对话间自然流转。", memory: "你决定 SparkClaw 记住什么。随时查看、编辑或移除。", approvals: "先查看影响范围，再允许 SparkClaw 继续。", settings: "按你的习惯，安排 SparkClaw 的工作方式。" },
    local: "本地工作区", inspector: "任务检查", toggleNav: "切换侧栏", toggleInspector: "切换检查面板", model: "模型", newSchedule: "新建定时任务", addMemory: "添加记忆", memoryPrompt: "请记住："
  },
  en: {
    workspace: "Personal workspace", home: "Workbench", newTask: "New task", search: "Search tasks", recent: "Recent tasks",
    schedules: "Schedules", channels: "Connections", memory: "Memory", approvals: "Approvals", settings: "Workspace settings",
    greeting: "Your local agent, ready when you are", headline: "From an idea,", outcome: "to a job well done.",
    intro: "Search, research, organize, and execute. Let SparkClaw help you move forward.",
    steps: "Every step, in the open", reliable: "Local context. Reliable execution.", resume: "Pick up where you left off", all: "View all",
    empty: "No tasks yet. Start with an idea.", noMatches: "No matching tasks. Try another keyword.",
    suggestions: ["Research a topic", "Browse the web", "Organize local files", "Schedule a task"],
    prompts: ["Help me research this topic: ", "Use the browser to find and compare: ", "Help organize my local files. Propose a plan and wait for approval: ", "Help me create a scheduled task: "],
    stepTitles: ["Local context", "Web search", "Reasoning", "Deliver results"],
    stepDescriptions: ["Understand your files, preferences, and goals", "Find public information and complete the context", "Break down tasks into a clear execution plan", "Keep the results and the full execution history"],
    pageTitles: { schedules: "Make everyday work automatic.", channels: "Meet your assistant where you work.", memory: "An assistant that gets to know you.", approvals: "Important actions are your decision.", settings: "Workspace settings" },
    pageDescriptions: { schedules: "Plan once. Let SparkClaw handle the repetition.", channels: "Connect messaging tools to exchange tasks and results.", memory: "Choose what SparkClaw remembers. Review, edit, or remove it anytime.", approvals: "Review the impact before allowing SparkClaw to continue.", settings: "Make SparkClaw work the way you do." },
    local: "Local workspace", inspector: "Task inspector", toggleNav: "Toggle sidebar", toggleInspector: "Toggle inspector", model: "Model", newSchedule: "New scheduled task", addMemory: "Add memory", memoryPrompt: "Please remember: "
  }
};

export function WorkbenchWelcome({ language }: { language: Language }) {
  const copy = workbenchCopy[language];
  return <div className="workbenchWelcome"><p className="homeGreeting"><Sparkles size={19} />{copy.greeting}</p><h1>{copy.headline}<br /><span>{copy.outcome}</span></h1><p className="homeIntro">{copy.intro}</p></div>;
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
  const matches = sessions.filter(session => session.title.toLocaleLowerCase().includes(query.toLocaleLowerCase()));
  return <dialog ref={dialogRef} className="taskSearchDialog" aria-label={copy.search} onCancel={event => { event.preventDefault(); onClose(); }} onClick={event => { if (event.target === event.currentTarget) { const box = event.currentTarget.getBoundingClientRect(); if (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom) onClose(); } }}>
    <div className="taskInspectorHeader">{copy.search}<button className="iconButton" onClick={onClose} aria-label={language === "zh" ? "关闭" : "Close"}><X size={18} /></button></div>
    <input autoFocus type="search" aria-label={copy.search} placeholder={copy.search} value={query} onChange={event => setQuery(event.target.value)} />
    <div className="taskSearchResults">{matches.map(session => <button key={session.id} onClick={() => onSelect(session)}>{session.title}<ArrowRight size={16} /></button>)}{matches.length === 0 && <p>{copy.noMatches}</p>}</div>
  </dialog>;
}

export function WorkbenchGuide({ language, sessions, onSelect, onSuggest, onSearch }: {
  language: Language; sessions: Session[]; onSelect: (session: Session) => void; onSuggest: (prompt: string) => void; onSearch: () => void;
}) {
  const copy = workbenchCopy[language];
  const suggestionIcons = [Search, Globe, Folder, Clock3];
  const stepIcons = [Folder, Globe, GitBranch, FileText];
  return <div className="workbenchGuide">
    <div className="homeSuggestions">{copy.suggestions.map((label, index) => { const Icon = suggestionIcons[index]; return <button key={label} onClick={() => onSuggest(copy.prompts[index])}><Icon size={14} />{label}</button>; })}</div>
    <section className="homeSteps"><div className="homeSectionHeading"><h2>{copy.steps}</h2><span>{copy.reliable}<ArrowRight size={14} /></span></div>
      <div className="homeFlow">{copy.stepTitles.map((title, index) => { const Icon = stepIcons[index]; return <div key={title}><div className="flowNumber"><span>0{index + 1}</span><Icon size={18} /></div><h3>{title}</h3><p>{copy.stepDescriptions[index]}</p></div>; })}</div>
    </section>
    <section className="homeRecent"><div className="homeSectionHeading"><h2>{copy.resume}</h2><button onClick={onSearch}>{copy.all}<ArrowRight size={14} /></button></div>
      {sessions.length ? sessions.slice(0, 3).map(session => <button className="homeRecentRow" key={session.id} onClick={() => onSelect(session)}><FileText size={16} /><span>{session.title}</span><ArrowRight size={14} /></button>) : <p className="muted">{copy.empty}</p>}
    </section>
  </div>;
}
