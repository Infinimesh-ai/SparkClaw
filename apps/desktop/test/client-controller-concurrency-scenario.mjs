import assert from "node:assert/strict";

// Invoked only by the opt-in native fixture. All work still enters the actual
// Gateway through ClientStore and reaches this observer over encrypted ISCP.
export function concurrentScenario({ evidence, until, expireWaiter = false }) {
  let firstAcquire;
  let heldRead = false;
  const arrivals = [], completions = [], refusals = [], active = new Set();
  const observationErrors = [];
  let maximum = 0;
  return {
    before(request) {
      if (request.operation === "browser.controller.acquire") {
        arrivals.push({command_id:request.body.command_id,conversation_id:request.body.binding.local_conversation_id});
        firstAcquire ??= request.body.command_id;
      }
    },
    async after(request, reply, forwarder) {
      const owner = request.body?.binding?.local_conversation_id;
      if (request.operation === "browser.controller.acquire" && reply.body?.outcome === "completed") {
        active.add(owner); maximum = Math.max(maximum, active.size);
        completions.push({command_id:request.body.command_id,conversation_id:owner});
        assert.equal(active.size, 1, "two native Controller leases overlapped");
        if (request.body.command_id === firstAcquire) {
          // Withhold only the first acquire reply until the second real task is
          // queued. This gives a deterministic overlap without changing bounds.
          try {
            await until(() => forwarder.admission.queue.length === 1, "second task in native FIFO", 8000);
            evidence.checks.second_conversation_waits_in_controller_fifo = true;
          } catch (error) {
            // A failed observation must not discard a successful acquire reply
            // and manufacture an unrelated unknown outcome in the application.
            observationErrors.push(String(error));
          }
        }
      }
      if(request.operation === "browser.controller.acquire" && reply.body?.outcome === "not_dispatched") {
        refusals.push({command_id:request.body.command_id,conversation_id:owner,error_code:reply.body.error_code});
        assert.equal(forwarder.journal.rows.has(request.body.command_id),false,"expired acquire reached the local journal");
      }
      if(expireWaiter&&!heldRead&&request.body?.operation==="page.read"&&reply.body?.outcome==="completed") {
        heldRead=true;
        // Hold a completed observation, not the acquire reply or a write. This
        // keeps the active task inside its read deadline while the other task's
        // unchanged 10-second admission budget expires, even on a fast client.
        try { await until(()=>refusals.length===1&&forwarder.admission.queue.length===0,"queued acquire expiry",12500); }
        catch(error) { observationErrors.push(String(error)); }
      }
      if (request.operation === "browser.controller.release" && reply.body?.outcome === "completed") active.delete(owner);
    },
    async run({ evaluate, forwarders }) {
      const conversations = [];
      for (const suffix of ["A", "B"]) conversations.push(await evaluate(`window.sparkclawClientStore.create('Controller concurrent ${suffix}')`));
      const personal = (await evaluate("window.sparkclawDesktop.createPersonal()")).page_ref;
      await evaluate(`window.sparkclawDesktop.selectConversation(${JSON.stringify(conversations[0].id)})`);
      const jobs = await Promise.all(conversations.map((conversation, index) => evaluate(`(async()=>{
        const store=window.sparkclawClientStore;
        const task=await store.enqueue(${JSON.stringify(conversation.id)},${JSON.stringify(`打开 https://controller.fixture.test/${index ? "b" : "a"} 网页`)},[]);
        await store.submit(task.request_id);return task;
      })()`)));
      let states;
      await until(async () => {
        await Promise.all(jobs.map(job => evaluate(`window.sparkclawClientStore.reconcile(${JSON.stringify(job.request_id)})`)));
        states = await Promise.all(conversations.map(conversation => evaluate(`window.sparkclawClientStore.read(${JSON.stringify(conversation.id)})`)));
        return states.every((state, index) => ["delivered", "failed", "canceled", "unknown"].includes(state.tasks.find(task => task.request_id === jobs[index].request_id)?.status));
      }, "two native conversation tasks", 90000);
      evidence.tasks.push(...states);
      for (let index = 0; index < jobs.length; index++) assert.equal(states[index].tasks.find(task => task.request_id === jobs[index].request_id).status, "delivered");
      assert.equal(maximum, 1); assert.equal(active.size, 0);
      const forwarder = [...forwarders][0];
      if(expireWaiter)assert.equal(refusals.length,1,"the forced waiter did not expire");
      if(refusals.length){
        // An expired waiter must never start later; a separate explicit task may.
        assert.equal(refusals.length,1);assert.equal(refusals[0].error_code,"browser_busy");
        const index=conversations.findIndex(conversation=>conversation.id===refusals[0].conversation_id);
        assert.ok(index>=0);assert.equal(forwarder.registry.conversationPages.has(conversations[index].id),false);
        const prompt = `打开 https://controller.fixture.test/${index ? "b" : "a"} 网页`;
        const fresh=await evaluate(`(async()=>{const s=window.sparkclawClientStore;const t=await s.enqueue(${JSON.stringify(conversations[index].id)},${JSON.stringify(prompt)},[]);await s.submit(t.request_id);return t;})()`);
        let state;
        await until(async()=>{
          await evaluate(`window.sparkclawClientStore.reconcile(${JSON.stringify(fresh.request_id)})`);
          state=await evaluate(`window.sparkclawClientStore.read(${JSON.stringify(conversations[index].id)})`);
          return ["delivered","failed","canceled","unknown"].includes(state.tasks.find(task=>task.request_id===fresh.request_id)?.status);
        },"fresh task after the queued request expired",60000);
        assert.equal(state.tasks.find(task=>task.request_id===fresh.request_id).status,"delivered");
        evidence.tasks.push(state);evidence.checks.expired_native_acquire_never_starts_later=true;
      }
      assert.deepEqual(completions, arrivals.filter(row=>!refusals.some(refusal=>refusal.command_id===row.command_id)));
      assert.equal(completions.length, 2);
      assert.equal(active.size,0);
      assert.deepEqual(observationErrors, []);
      evidence.concurrency = { acquire_arrivals: arrivals, acquire_completions: completions, refused_before_dispatch:refusals, maximum_active: maximum };
      evidence.checks.native_acquires_complete_in_fifo_order = true;
      assert.equal(forwarder.registry.selectedConversationID, conversations[0].id);
      const pages = conversations.map(conversation => forwarder.registry.conversationPages.get(conversation.id));
      assert.ok(pages.every(Boolean)); assert.notEqual(pages[0].webContents.id, pages[1].webContents.id);
      for (let index = 0; index < pages.length; index++) {
        assert.equal(pages[index].webContents.getURL(), `https://controller.fixture.test/${index ? "b" : "a"}`);
        assert.equal(pages[index].webContents.debugger.isAttached(), false);
        assert.equal(pages[index].webContents.getLastWebPreferences().sandbox, true);
      }
      const desktop = await evaluate("window.sparkclawDesktop.state()");
      assert.ok(desktop.pages.some(page => page.page_ref === personal && page.role === "personal"));
      const byCommand = (a,b) => a.command_id.localeCompare(b.command_id);
      assert.deepEqual([...desktop.browser_host.unknown_writes].sort(byCommand), [...evidence.unknown_fences_before].sort(byCommand));
      evidence.checks.concurrent_views_remain_owned_without_conversation_switch = true;
      evidence.checks.personal_page_and_prior_unknown_fences_preserved = true;
      assert.equal(evidence.direct_business_http_attempts, 0);
      evidence.passed = true;
    },
  };
}
