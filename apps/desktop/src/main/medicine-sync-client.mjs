const scopeOf = (c) => ({ deployment_id: c.deploymentID, owner_id: c.ownerID });
const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
const fields = (item) =>
  Object.fromEntries(
    [
      "name",
      "category",
      "quantity",
      "unit",
      "expires_on",
      "no_expiry",
      "location",
      "usage",
      "notes",
    ].map((key) => [key, item[key]]),
  );
// The original box continues to read its local cache; only this adapter writes
// authoritative records. Unknown mutations are looked up, never resubmitted.
export class MedicineSyncClient {
  constructor({
    store,
    getConnection,
    getFetch,
    installationID,
    onChange = () => {},
    canWrite = () => true,
    intervalMS = 30000,
  }) {
    Object.assign(this, {
      store,
      getConnection,
      getFetch,
      installationID,
      onChange,
      canWrite,
      intervalMS,
    });
    this.generation = 0;
    this.active = false;
    this.controllers = new Set();
    this.inflight = null;
    this.writing = false;
  }
  start() {
    if (this.active) return this;
    this.active = true;
    this.timer = setInterval(
      () => void this.sync().catch(() => {}),
      this.intervalMS,
    );
    this.timer.unref?.();
    void this.sync().catch(() => {});
    return this;
  }
  close() {
    this.active = false;
    this.generation++;
    clearInterval(this.timer);
    for (const c of this.controllers) c.abort();
    this.controllers.clear();
    this.inflight = null;
    this.lastSync = 0;
  }
  #connection() {
    const c = this.getConnection();
    if (!c) throw new Error("Medicine box is locked");
    return c;
  }
  #check(c, g) {
    if (!this.active || g !== this.generation || this.getConnection() !== c)
      throw new Error("Medicine connection changed");
  }
  async #request(c, g, path, { method = "GET", body } = {}) {
    this.#check(c, g);
    const controller = new AbortController();
    this.controllers.add(controller);
    const signal = AbortSignal.any([
      controller.signal,
      AbortSignal.timeout(30000),
    ]);
    try {
      const response = await this.getFetch()(`${c.origin}${path}`, {
        method,
        headers: {
          Authorization: c.authorization,
          "Content-Type": "application/json",
          "X-SparkClaw-Installation": this.installationID,
        },
        ...(body ? { body: JSON.stringify(body) } : {}),
        signal,
        redirect: "manual",
      });
      this.#check(c, g);
      signal.throwIfAborted();
      if (!response.ok) {
        await response.body?.cancel();
        const error = new Error(
          response.status === 409
            ? "这条药品已在别处修改，请刷新后重新操作"
            : "Medicine service is unavailable",
        );
        error.status = response.status;
        throw error;
      }
      const reader = response.body?.getReader();
      if (!reader) throw new Error("Medicine response is empty");
      let size = 0;
      const chunks = [];
      try {
        for (;;) {
          const { done, value } = await reader.read();
          this.#check(c, g);
          signal.throwIfAborted();
          if (done) break;
          size += value.byteLength;
          if (size > 1024 * 1024)
            throw new Error("Medicine response exceeds capacity");
          chunks.push(value);
        }
      } finally {
        await reader.cancel().catch(() => {});
      }
      this.#check(c, g);
      return JSON.parse(Buffer.concat(chunks).toString("utf8"));
    } finally {
      this.controllers.delete(controller);
    }
  }
  refresh() {
    return this.lastSync && Date.now() - this.lastSync < 5000
      ? Promise.resolve()
      : this.sync();
  }
  sync() {
    if (!this.active) return Promise.resolve();
    if (this.inflight) return this.inflight;
    const c = this.#connection(),
      g = this.generation;
    const promise = this.#sync(c, g).finally(() => {
      if (this.inflight === promise) this.inflight = null;
    });
    this.inflight = promise;
    return promise;
  }
  async #sync(c, g) {
    this.#check(c, g);
    const scope = scopeOf(c);
    await this.#recover(c, g);
    for (let batch = 0; batch < 2000; batch++) {
      const pending = [];
      for (const item of this.store.pendingImport(scope)) {
        if (Buffer.byteLength(JSON.stringify([...pending, item])) > 48 * 1024)
          break;
        pending.push(item);
      }
      if (!pending.length) break;
      if (!this.canWrite())
        throw new Error("Medicine migration needs a writable connection");
      await this.#mutate(c, g, {
        path: "/api/medicine/import",
        method: "POST",
        body: { installation_id: this.installationID, items: pending },
        kind: "import",
      });
    }
    let resets = 0;
    for (let page = 0; page < 100; page++) {
      const cursor = this.store.cursor(scope);
      let response;
      try {
        response = await this.#request(c, g, "/api/medicine/sync", {
          method: "POST",
          body: { cursor, limit: 100 },
        });
      } catch (error) {
        if (error.status === 409 && resets++ < 3) {
          this.store.reset(scope);
          continue;
        }
        throw error;
      }
      this.store.apply(scope, response, cursor);
      if (!response.more) {
        this.lastSync = Date.now();
        this.onChange();
        return this.store.list(scope);
      }
    }
    throw new Error("Medicine sync page limit exceeded");
  }
  async #recover(c, g) {
    const scope = scopeOf(c),
      pending = this.store.mutation(scope);
    if (!pending) return;
    if (pending.state === "prepared") {
      this.store.clearMutation(scope, pending.operation_id);
      return;
    }
    if (["completed", "reconciled"].includes(pending.state)) {
      if (pending.request.kind === "import") {
        this.#applyResult(scope, pending.request, pending.result);
        this.store.clearMutation(scope, pending.operation_id);
      }
      return;
    }
    let result = pending.result;
    if (!["completed", "reconciled"].includes(pending.state)) {
      const receipt = await this.#request(
        c,
        g,
        `/api/medicine/operations/${encodeURIComponent(pending.operation_id)}`,
      );
      if (receipt.operation_id !== pending.operation_id)
        throw new Error("Medicine receipt mismatch");
      result =
        pending.request.kind === "import" ? receipt.import : receipt.batch;
      if (!result || result.operation_id !== pending.operation_id)
        throw new Error("Medicine receipt is incomplete");
    }
    this.#applyResult(scope, pending.request, result);
    this.store.finishMutation(scope, pending.operation_id, result, true);
    if (pending.request.kind === "import")
      this.store.clearMutation(scope, pending.operation_id);
    this.onChange();
  }
  #applyResult(scope, request, result) {
    if (request.kind === "import") {
      if (
        !Array.isArray(result.items) ||
        result.items.length !== request.body.items.length ||
        !same(
          [...result.items.map((x) => x.id)].sort(),
          [...request.body.items.map((x) => x.id)].sort(),
        )
      )
        throw new Error("Medicine import receipt mismatch");
      this.store.imported(scope, result.items);
      return;
    }
    // Direct CRUD responses and recovered batch receipts share the same cache update.
    if (result.items) {
      const change = result.items[0];
      if (result.items.length !== 1 || !change)
        throw new Error("Medicine change receipt mismatch");
      if (change.deleted) {
        if (change.before?.id !== request.itemID)
          throw new Error("Medicine deletion receipt mismatch");
        this.store.remove(scope, request.itemID);
        return;
      }
      if (!change.after) throw new Error("Medicine result is absent");
      this.store.cache(scope, change.after);
      return change.after;
    }
    if (request.kind === "delete") {
      if (result.deleted !== true) throw new Error("Medicine deletion failed");
      this.store.remove(scope, request.itemID);
      return result;
    }
    if (request.itemID && result.id !== request.itemID)
      throw new Error("Medicine update identity differs");
    return this.store.cache(scope, result);
  }
  async #mutate(c, g, request) {
    const scope = scopeOf(c);
    const previous = this.store.mutation(scope);
    if (previous) {
      const { actionID: oldAction, ...oldRequest } = previous.request;
      const { actionID, ...newRequest } = request;
      if (
        ["completed", "reconciled"].includes(previous.state) &&
        same(oldRequest, newRequest) &&
        (previous.state === "reconciled" || oldAction === actionID)
      ) {
        this.store.finishMutation(
          scope,
          previous.operation_id,
          previous.result,
        );
        return previous.result.items?.[0]?.after ?? previous.result;
      }
      if (previous.state === "reconciled") {
        this.store.finishMutation(
          scope,
          previous.operation_id,
          previous.result,
        );
        throw new Error(
          "Previous medicine change was recovered; review the box before another change",
        );
      }
      if (previous.state !== "completed")
        throw new Error("Previous medicine change is awaiting reconciliation");
      this.store.clearMutation(scope, previous.operation_id);
    }
    const pending = this.store.beginMutation(scope, request);
    this.store.attempted(scope, pending.operation_id);
    try {
      const result = await this.#request(c, g, request.path, {
        method: request.method,
        body: { ...request.body, operation_id: pending.operation_id },
      });
      this.store.finishMutation(scope, pending.operation_id, result);
      const saved = this.#applyResult(scope, request, result);
      if (request.kind === "import")
        this.store.clearMutation(scope, pending.operation_id);
      this.onChange();
      return saved;
    } catch (error) {
      // Explicit pre-dispatch rejection proves no write; timeouts and 5xx do not.
      if (
        (error.status >= 400 &&
          error.status < 500 &&
          error.status !== 408 &&
          error.status !== 429) ||
        error.name === "ISCPRequestNotSentError"
      ) {
        this.store.finishMutation(scope, pending.operation_id, {
          rejected: true,
        });
        this.store.clearMutation(scope, pending.operation_id);
      }
      throw error;
    }
  }
  async save(item, actionID) {
    return this.#write(async (c, g, scope) => {
      const version = item.version;
      if (item.id && !(version > 0))
        throw new Error("Medicine record is not synchronized");
      return this.#mutate(c, g, {
        path: `/api/medicine/items${item.id ? "/" + encodeURIComponent(item.id) : ""}`,
        method: item.id ? "PUT" : "POST",
        body: {
          item: fields(item),
          ...(item.id ? { base_version: version } : {}),
        },
        kind: "save",
        itemID: item.id,
        actionID,
      });
    });
  }
  async remove(id, baseVersion, actionID) {
    return this.#write(async (c, g, scope) => {
      const version = baseVersion;
      if (!(version > 0))
        throw new Error("Medicine record is not synchronized");
      return this.#mutate(c, g, {
        path: `/api/medicine/items/${encodeURIComponent(id)}`,
        method: "DELETE",
        body: { base_version: version },
        kind: "delete",
        itemID: id,
        actionID,
      });
    });
  }
  async #write(action) {
    if (this.writing) throw new Error("Medicine change already in progress");
    if (!this.active || !this.canWrite()) throw new Error("连接后可修改");
    const c = this.#connection(),
      g = this.generation,
      scope = scopeOf(c);
    this.writing = true;
    try {
      await this.sync();
      this.#check(c, g);
      return await action(c, g, scope);
    } catch (error) {
      if (error.status === 409) await this.sync().catch(() => {});
      throw error;
    } finally {
      this.writing = false;
    }
  }
}
