import crypto from "node:crypto";

const SCOPE = ["owner_id", "client_id", "installation_id", "local_conversation_id", "local_task_id"];
export const sameBrowserTask = (a, b) => Boolean(a && b && SCOPE.every(key => a[key] === b[key]));
export const canInteract = record => Boolean(record && !record.destroyed &&
  (record.role === "personal" || record.interaction?.state === "human"));

// Execution ownership outlives a Controller lease. Only main-process terminal
// facts and proved cleanup advance this state; renderer requests cannot do so.
export class PageInteractions {
  constructor(registry) { this.registry = registry; this.groups = new Set(); }

  group(binding) {
    let group = [...this.groups].find(g => sameBrowserTask(g.binding, binding) &&
      g.binding.connection_epoch === binding.connection_epoch && g.state === "agent");
    if (!group) {
      group = { id: `group_${crypto.randomUUID()}`, binding: Object.freeze({ ...binding }), state: "agent", revision: 1, pages: new Set() };
      this.groups.add(group);
    }
    return group;
  }

  begin(binding, reason) {
    const groups = [...this.groups].filter(g => g.state !== "human" && (!binding || sameBrowserTask(g.binding, binding)) &&
      (!binding?.connection_epoch || g.binding.connection_epoch === binding.connection_epoch));
    for (const group of groups) {
      if (group.state === "agent") { group.state = "releasing"; group.revision++; group.reason = reason; }
      for (const page of group.pages) {
        const connection = this.registry.connections.get(page.connectionID);
        if (connection) connection.fenced = true;
      }
    }
    this.registry.changed();
    return groups;
  }

  async complete(groups) {
    for (const group of groups) {
      // A detached debugger/read may still have an outstanding native promise.
      // Failure or a stuck operation leaves the original group read-only.
      await Promise.allSettled([...group.pages].map(page => page.executionCleanup));
      if (group.state !== "releasing" || [...group.pages].some(page => !page.destroyed &&
          (page.connectionID || !page.executionClean || page.executionWork?.size))) continue;
      group.state = "human"; group.revision++;
      for (const page of group.pages) {
        if (page.destroyed) continue;
        try {
          const url = new URL(page.webContents.getURL());
          if (url.protocol !== "https:" || url.username || url.password) throw new Error();
        } catch { this.registry.destroy(page.tabID, "unsafe_handback_url"); continue; }
        this.registry.onHumanPage?.(page);
        if (this.registry.presentation.isPresented(page)) this.registry.presentation.showTask(page, true);
      }
    }
    this.registry.changed();
  }

  remove(page) {
    const group = page.interaction;
    if (!group) return;
    group.pages.delete(page);
    if (!group.pages.size) this.groups.delete(group);
  }
}

export function trackPageWork(record, work) {
  record.executionWork ??= new Set();
  let pending;
  try { pending = Promise.resolve(work()); } catch (error) { pending = Promise.reject(error); }
  record.executionWork.add(pending);
  void pending.finally(() => record.executionWork.delete(pending)).catch(() => {});
  return pending;
}

export async function drainPageWork(record) {
  let timer;
  try {
    await Promise.race([
      Promise.allSettled([...record.executionWork ?? []]),
      new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("Browser cleanup is pending")), 5000); }),
    ]);
    record.executionClean = true;
  } finally { clearTimeout(timer); }
}
