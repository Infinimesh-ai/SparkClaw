export type ConversationActivity = "running" | "completed";

// Both transports expose execution states. Inputs are ordered newest first;
// unfinished executions take precedence over a newer completed execution.
export function conversationActivity(states: readonly string[]): ConversationActivity | undefined {
  if (states.some(isTaskInProgress)) return "running";
  if (["completed", "delivered", "saved"].includes(states[0])) return "completed";
  return undefined;
}

// The completed indicator marks an unviewed result, so the open conversation never shows it.
export function unseenActivity(activity: ConversationActivity | undefined, viewing: boolean) {
  return viewing && activity === "completed" ? undefined : activity;
}

export function isTaskInProgress(state: string) {
  return ["submission_pending", "accepted", "running", "approval_pending", "browser_login_blocked", "cancel_pending"].includes(state);
}

export function localExecutionStates(tasks: readonly { status: string }[]) {
  return tasks.filter(task => !["scheduled_local", "schedule_missed", "schedule_canceled"].includes(task.status))
    .map(task => task.status).reverse();
}
