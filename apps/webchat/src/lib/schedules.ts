import type { Schedule } from "../api/types";
import type { Language } from "../i18n";

export function formatScheduleTime(schedule: Schedule, language: Language) {
  const date = new Date(schedule.due_time);
  if (Number.isNaN(date.getTime())) return schedule.due_time;
  try {
    return new Intl.DateTimeFormat(language === "zh" ? "zh-CN" : "en", {
      month: "short",
      day: "numeric",
      weekday: "short",
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
      timeZone: schedule.timezone || undefined
    }).format(date);
  } catch {
    return new Intl.DateTimeFormat(language === "zh" ? "zh-CN" : "en", {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      hour12: false
    }).format(date);
  }
}

export function schedulePattern(schedule: Schedule, labels: { oneTime: string; daily: string; weekly: string; monthly: string }) {
  const recurrence = schedule.recurrence?.trim();
  if (!recurrence) return labels.oneTime;
  const lower = recurrence.toLowerCase();
  if (lower === "daily" || recurrence.includes("每天")) return labels.daily;
  if (lower === "weekly" || recurrence.includes("每周")) return labels.weekly;
  if (lower === "monthly" || recurrence.includes("每月")) return labels.monthly;
  return recurrence;
}
