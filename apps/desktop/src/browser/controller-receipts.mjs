import { ID, HASH } from "./controller-wire.mjs";

// Shared content-free fence/reconciliation ownership survives transport cutover.
export async function refreshBrowserFences(owner, conflict) {
    if (!owner.scope || owner.auth.status.state !== "connected") throw new Error("Browser reconciliation identity is unavailable");
    const generation = owner.generation; const scope = owner.scope;
    const response = await fetchBrowserReceipt(owner, "browser.receipt", "/api/v1/browser/hosts/fences", { headers: { "X-SparkClaw-Installation": scope.installation_id } });
    if (!response.ok) throw new Error("Browser write fences are unavailable");
    const result = await response.json();
    if (generation !== owner.generation || scope !== owner.scope) throw new Error("Browser reconciliation identity changed");
    if (!result || Object.keys(result).join() !== "fences" || !Array.isArray(result.fences) || result.fences.length > 1024) throw new Error("Browser fence response is invalid");
    const fences = new Map();
    for (const row of result.fences) {
      const keys = ["command_id", "scope", "host_id", "runtime_generation", "connection_epoch", "lease_id", "page_id", "page_generation", "authorization_digest", "digest", "write", "state", "updated_at"];
      const scopedKeys = ["owner_id", "client_id", "installation_id", "local_conversation_id", "local_task_id"];
      if (!row || Object.keys(row).sort().join() !== keys.sort().join() || !row.scope || Object.keys(row.scope).sort().join() !== scopedKeys.sort().join() ||
          scopedKeys.some((key) => !ID.test(row.scope[key])) || ["owner_id", "client_id", "installation_id"].some((key) => row.scope[key] !== scope[key]) ||
          !ID.test(row.command_id) || !["host_id", "runtime_generation", "connection_epoch", "lease_id", "page_id"].every((key) => ID.test(row[key])) || !HASH.test(row.digest) || !HASH.test(row.authorization_digest) ||
          row.state !== "unknown" || row.write !== true || !Number.isSafeInteger(row.page_generation) || row.page_generation < 1 || !Number.isFinite(Date.parse(row.updated_at)) || fences.has(row.command_id)) throw new Error("Browser fence identity is invalid");
      const local = owner.journal.rows.get(row.command_id);
      if (local && (local.digest !== row.digest || scopedKeys.some((key) => local[key] !== row.scope[key]))) { conflict(); throw new Error("Browser fence digest conflicts with the local journal"); }
      fences.set(row.command_id, Object.freeze(row));
    }
    owner.remoteFences = fences; owner.onChange(); return owner.snapshot();
  }

export async function reconcileBrowserFence(owner, commandID, digest, outcome) {
    if (!ID.test(commandID) || !HASH.test(digest) || !["observed_completed", "observed_not_applied"].includes(outcome)) throw new Error("Browser write reconciliation is invalid");
    await owner.refreshFences();
    const generation = owner.generation; const scope = owner.scope;
    const local = owner.journal.rows.get(commandID);
    const remote = owner.remoteFences.get(commandID);
    const scopedKeys = ["owner_id", "client_id", "installation_id"];
    const localBound = local && local.digest === digest && scopedKeys.every((key) => local[key] === owner.scope?.[key]);
    if (!(remote?.digest === digest || localBound && local.state === "unknown") || local && !localBound) throw new Error("Browser write reconciliation is invalid");
    const response = await fetchBrowserReceipt(owner, "browser.reconcile", "/api/v1/browser/hosts/reconcile", { method: "POST", headers: { "content-type": "application/json", "X-SparkClaw-Installation": owner.scope.installation_id }, body: JSON.stringify({ command_id: commandID, digest, outcome }) });
    if (!response.ok) throw new Error("Backend write reconciliation failed");
    if (generation !== owner.generation || scope !== owner.scope) throw new Error("Browser reconciliation identity changed");
    if (local) await owner.journal.reconcile(commandID, digest, outcome, Boolean(remote));
    else await owner.journal.recordReconciled(remote, outcome);
    owner.remoteFences.delete(commandID); owner.onChange(); return owner.snapshot();
  }

async function fetchBrowserReceipt(owner, operation, route, init) {
    if(owner.auth.descriptor?.transport !== "iscp")return owner.auth.authorizedFetch(`${owner.auth.descriptor.origin}${route}`,init);
    const body=init?.body?JSON.parse(init.body):{};
    const result=await owner.auth.invokeISCP(operation,body,{installationID:owner.scope.installation_id, ...(owner.receiptTimeoutMS ? { timeoutMS: owner.receiptTimeoutMS } : {})});
    return new Response(JSON.stringify(result),{status:200,headers:{'content-type':'application/json'}});
  }
