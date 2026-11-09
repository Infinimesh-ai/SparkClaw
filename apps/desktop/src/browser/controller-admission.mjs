// The active slot is held from acquire admission through release/cleanup. Queue
// deadlines are measured at receipt, not reset when a predecessor finishes.
export class ControllerAdmission {
  constructor({ maximum = 32 } = {}) { this.maximum = maximum; this.active = null; this.queue = []; this.closed = false; }
  acquire({ deadline, waitMS, signal }) {
    if (this.closed || signal?.aborted || performance.now() >= deadline) return Promise.reject(new Error("browser_busy"));
    if (!this.active && !this.queue.length) { this.active = Object.freeze({}); return Promise.resolve(this.active); }
    if (waitMS <= 0 || this.queue.length >= this.maximum) return Promise.reject(new Error("browser_busy"));
    return new Promise((resolve, reject) => {
      const entry = { resolve, reject, signal, deadline: Math.min(deadline, performance.now() + waitMS) };
      const remove = () => {
        const index = this.queue.indexOf(entry);
        if (index < 0) return;
        this.queue.splice(index, 1); entry.clear(); reject(new Error("browser_busy"));
      };
      const timer = setTimeout(remove, Math.max(1, entry.deadline - performance.now()));
      entry.clear = () => { clearTimeout(timer); signal?.removeEventListener("abort", remove); };
      signal?.addEventListener("abort", remove, { once: true });
      this.queue.push(entry);
    });
  }
  release(slot) {
    if (this.active !== slot) return;
    this.active = null;
    while (!this.closed && this.queue.length) {
      const entry = this.queue.shift(); entry.clear();
      if (entry.signal?.aborted || performance.now() >= entry.deadline) { entry.reject(new Error("browser_busy")); continue; }
      this.active = Object.freeze({}); entry.resolve(this.active); return;
    }
  }
  close() {
    this.closed = true;
    for (const entry of this.queue.splice(0)) { entry.clear(); entry.reject(new Error("browser_busy")); }
    // Active is deliberately not released here; its owner must prove cleanup.
  }
}
