import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
const ID = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u;
const HASH = /^[a-f0-9]{64}$/u;
const KEYS = ["command_id", "digest", "installation_id", "local_conversation_id", "local_task_id", "owner_id", "client_id", "state", "updated_at"];

// Content-free local durability fence: no DOM, values, URL or website output.
// Incomplete writes remain unknown after crash and are never replayed.
export class HostJournal {
  constructor(root) { this.root = root; this.rows = new Map(); this.serial = Promise.resolve(); }
  async load() {
    await fs.mkdir(this.root, { recursive: true, mode: 0o700 });
    const directory = await fs.lstat(this.root);
    if (!directory.isDirectory() || directory.isSymbolicLink() || directory.mode & 0o077 || process.getuid && directory.uid !== process.getuid()) throw new Error("Browser journal root is not private");
    const entries = await fs.readdir(this.root);
    for (const filename of entries.filter((value) => value.endsWith(".json"))) {
      const filenamePath = path.join(this.root, filename);
      const stat = await fs.lstat(filenamePath);
      if (!stat.isFile() || stat.isSymbolicLink() || stat.size > 4096) throw new Error("Browser journal is invalid");
      const row = JSON.parse(await fs.readFile(filenamePath, "utf8"));
      validate(row);
      if (`${row.command_id}.json` !== filename) throw new Error("Browser journal is invalid");
      this.rows.set(row.command_id, row);
    }
    if (this.rows.size > 100000) throw new Error("Browser journal capacity is exhausted");
    for (const row of this.rows.values()) if (row.state === "dispatched") await this.#store({ ...row, state: "unknown", updated_at: new Date().toISOString() });
    return this;
  }
  async begin(command) {
    const prior = this.rows.get(command.command_id);
    if (prior) { if (prior.digest !== command.digest) throw new Error("Browser command digest conflicts"); return prior.state; }
    if (this.rows.size >= 100000) throw new Error("Browser journal capacity is exhausted");
    const b = command.binding;
    await this.#store({ command_id: command.command_id, digest: command.digest, installation_id: b.installation_id, local_conversation_id: b.local_conversation_id,
      local_task_id: b.local_task_id, owner_id: b.owner_id, client_id: b.client_id, state: "dispatched", updated_at: new Date().toISOString() });
    return "new";
  }
  async finish(id, state) {
    if (!["completed", "unknown", "failed"].includes(state) || !this.rows.has(id)) throw new Error("Browser journal transition is invalid");
    await this.#store({ ...this.rows.get(id), state, updated_at: new Date().toISOString() });
  }
  unknown(scope) { return [...this.rows.values()].filter((row) => row.state === "unknown" && row.installation_id === scope.installation_id && row.owner_id === scope.owner_id && row.client_id === scope.client_id); }
  async reconcile(id, digest, outcome, remoteUnknown = false) {
    const row = this.rows.get(id);
    if (!row || !(row.state === "unknown" || remoteUnknown && row.state === "completed" || row.state === outcome) || row.digest !== digest || !["observed_completed", "observed_not_applied"].includes(outcome)) throw new Error("Browser reconciliation is invalid");
    await this.#store({ ...row, state: outcome, updated_at: new Date().toISOString() });
  }
  async recordReconciled(fence, outcome) {
    if (!fence || this.rows.has(fence.command_id) || !["observed_completed", "observed_not_applied"].includes(outcome)) throw new Error("Browser reconciliation is invalid");
    await this.#store({ command_id: fence.command_id, digest: fence.digest, ...fence.scope, state: outcome, updated_at: new Date().toISOString() });
  }
  async #store(row) {
    validate(row);
    const pending = this.serial.then(async () => {
      const temporary = path.join(this.root, `.journal-${crypto.randomUUID()}`);
      try {
        const file = await fs.open(temporary, "wx", 0o600);
        try { await file.writeFile(JSON.stringify(row)); await file.sync(); } finally { await file.close(); }
        await fs.rename(temporary, path.join(this.root, `${row.command_id}.json`));
        const directory = await fs.open(this.root, "r");
        try { await directory.sync(); } finally { await directory.close(); }
        this.rows.set(row.command_id, Object.freeze(row));
      } finally { await fs.rm(temporary, { force: true }); }
    });
    this.serial = pending.catch(() => {}); return pending;
  }
}
function validate(row) {
  if (!row || Object.keys(row).sort().join() !== [...KEYS].sort().join() || !HASH.test(row.digest) ||
      KEYS.filter((key) => key.endsWith("_id")).some((key) => !ID.test(row[key])) ||
      !["dispatched", "unknown", "completed", "failed", "observed_completed", "observed_not_applied"].includes(row.state) || !Number.isFinite(Date.parse(row.updated_at))) throw new Error("Browser journal is invalid");
}
