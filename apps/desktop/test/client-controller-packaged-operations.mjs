import assert from "node:assert/strict";

// Real workflow/model, packaged main/preload and Controller, encrypted ISCP.
// Only the visited fixture sites are synthetic. No direct action changes the DOM.
export async function qualifyPackagedOperations({ application, window, evidence, until }) {
  assert.equal(evidence.provider.mock, false, "Operation selection requires the real provider");
  const origin = "https://controller.fixture.test";
  const frameOrigin = "https://frame.controller-fixture.test";
  const pages = {
    "/select": '<h1>Draft option</h1><label>Choice<select aria-label="Choice"><option value="one">One</option><option value="two">Two</option></select></label><p id="result">Choice: One</p><script>document.querySelector("select").onchange=e=>document.querySelector("#result").textContent="Choice: "+e.target.selectedOptions[0].textContent</script>',
    "/dynamic": `<h1>Dynamic control</h1><button>Add dynamic field</button><p id="result">Waiting</p><script>document.querySelector('button').onclick=function(){this.disabled=true;setTimeout(()=>{document.querySelector('#result').textContent='Dynamic ready';this.insertAdjacentHTML('afterend','<label>Dynamic field<input aria-label="Dynamic field"></label>')},250)}</script>`,
    "/frame": `<h1>Cross-origin frame</h1><iframe title="Task frame" src="${frameOrigin}/action"></iframe>`,
    "/popup": `<h1>Owned popup</h1><button>Open task popup</button><p id="result">Waiting</p><script>document.querySelector('button').onclick=function(){window.open('/opened');this.disabled=true;document.querySelector('#result').textContent='Popup opened'}</script>`,
    "/opened": '<h1>Task popup ready</h1><p>Owned popup evidence</p>',
  };
  await application.evaluate(async ({ session }, { pages, origin, frameOrigin }) => {
    const protocol = session.fromPartition("persist:sparkclaw-browser-default").protocol;
    protocol.unhandle("https");
    await protocol.handle("https", request => {
      const url = new URL(request.url);
      let body;
      if (url.origin === origin) body = pages[url.pathname];
      else if (url.origin === frameOrigin && url.pathname === "/action") body = `<h1>Frame control</h1><button>Apply frame action</button><script>document.querySelector('button').onclick=function(){this.textContent='Frame applied';this.disabled=true}</script>`;
      if (!body) throw Error("Unexpected operation fixture destination");
      return new Response('<!doctype html><meta charset="utf-8"><title>Packaged operation fixture</title>' + body,
        { headers: { "content-type": "text/html" } });
    });
  }, { pages, origin, frameOrigin });
  // Optional diagnostic observer for these synthetic pages only. Preserve the
  // original method, arguments, result and rejection; never issue an operation.
  if (process.env.SPARKCLAW_CONTROLLER_OPERATION_TRACE === "1") {
    await application.evaluate(({ app }) => {
      const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
      const { ClientControllerRuntime } = require(app.getAppPath() + "/src/browser/controller-runtime.mjs");
      const request = ClientControllerRuntime.prototype.request;
      const rows = globalThis.__controllerOperationTrace = [];
      ClientControllerRuntime.prototype.request = async function(action, body, options) {
        const row = { action, operation: body?.operation, page_id: body?.arguments?.page_id, incarnation: this.incarnation };
        const started = performance.now();
        try {
          const result = await request.call(this, action, body, options);
          if (action === "execute") row.result = result.payload?.result;
          return result;
        } catch (error) { row.error = String(error); throw error; }
        finally { row.duration_ms = Math.round(performance.now() - started); if (rows.length < 512) rows.push(row); }
      };
    });
    evidence.instrumented_operation_observer = true;
  }
  const personal = (await window.evaluate(() => window.sparkclawDesktop.createPersonal())).page_ref;
  evidence.operation_cases = [];
  const cases = [
    { name: "select", tool: "browser.select", prompt: `在 ${origin}/select 页面的 Choice 下拉框选择 Two，保留为草稿，不提交。` },
    { name: "dynamic", tool: "browser.click", prompt: `在 ${origin}/dynamic 页面点击 Add dynamic field 按钮，等待显示 Dynamic ready。` },
    { name: "frame", tool: "browser.click", prompt: `在 ${origin}/frame 页面点击 iframe 内的 Apply frame action 按钮，确认显示 Frame applied。` },
    { name: "popup", tool: "browser.click", prompt: `在 ${origin}/popup 页面点击 Open task popup 按钮，打开任务弹窗。` },
  ];
  const selected = process.env.SPARKCLAW_CONTROLLER_OPERATION_CASES?.split(",") || cases.map(row => row.name);
  assert.ok(selected.length && selected.every(name => cases.some(row => row.name === name)));
  for (const scenario of cases.filter(row => selected.includes(row.name))) {
    const observation = { name: scenario.name, approvals: [], passed: false };
    evidence.operation_cases.push(observation);
    const conversation = await window.evaluate(title => window.sparkclawClientStore.create(title), `Packaged ${scenario.name} ${Date.now()}`);
    await window.evaluate(id => window.sparkclawDesktop.selectConversation(id), conversation.id);
    const task = await window.evaluate(async ({ id, prompt }) => {
      const store = window.sparkclawClientStore;
      const task = await store.enqueue(id, prompt, []);
      await store.submit(task.request_id); return task;
    }, { id: conversation.id, prompt: scenario.prompt });
    observation.task_id = task.id;
    let state;
    const started = performance.now();
    await until(async () => {
      await window.evaluate(id => window.sparkclawClientStore.reconcile(id), task.request_id);
      state = await window.evaluate(id => window.sparkclawClientStore.read(id), conversation.id);
      observation.state = state;
      if (scenario.name === "popup" && !observation.popup_during_task) {
        const popup = await application.evaluate(({ webContents }, url) => {
          const page = webContents.getAllWebContents().find(item => item.getURL() === url);
          return page && { id: page.id, sandbox: page.getLastWebPreferences().sandbox };
        }, origin + "/opened");
        if (popup) {
          const desktop = await window.evaluate(() => window.sparkclawDesktop.state());
          assert.ok(desktop.pages.some(page => page.role === "task" && page.local_conversation_id === conversation.id && page.url === origin + "/opened"));
          assert.equal(popup.sandbox, true);
          observation.popup_during_task = popup;
        }
      }
      const current = state.tasks.find(row => row.request_id === task.request_id);
      const approval = current?.approvals?.find(row => row.state === "pending" && row.actionable);
      if (approval) {
        assert.equal(approval.tool, scenario.tool);
        assert.equal(observation.approvals.length, 0, "fixture action must happen only once");
        observation.approvals.push(approval);
        await window.evaluate(({ request, approval }) => window.sparkclawClientStore.decideApproval(request, approval.approval_id, approval.digest, "approve"),
          { request: task.request_id, approval });
      }
      return ["delivered", "failed", "canceled", "unknown"].includes(current?.status);
    }, `packaged ${scenario.name} workflow`, 120000);
    observation.duration_ms = Math.round(performance.now() - started);
    evidence.tasks.push(state);
    observation.workflow_completed = !state.messages.some(message => message.role === "assistant" && /workflow (?:stopped|is blocked)/iu.test(message.content));
    assert.equal(state.tasks.find(row => row.request_id === task.request_id).status, "delivered");
    if (scenario.tool === "browser.select") assert.equal(observation.approvals.length, 1);
    observation.pages = await application.evaluate(async ({ webContents }, { origin, frameOrigin, name }) => {
      const page = webContents.getAllWebContents().find(item => item.getURL() === `${origin}/${name}`);
      if (!page) return null;
      const result = { id: page.id, sandbox: page.getLastWebPreferences().sandbox, debugger: page.debugger.isAttached(), pid: page.getOSProcessId(),
        dom: await page.executeJavaScript(`({value:document.querySelector('select')?.value,result:document.querySelector('#result')?.textContent,dynamic:!!document.querySelector('input[aria-label="Dynamic field"]')})`) };
      if (name === "frame") {
        const frame = page.mainFrame.framesInSubtree.find(item => item.url === frameOrigin + "/action");
        result.frame = frame && { pid: frame.osProcessId, text: await frame.executeJavaScript("document.body.innerText") };
      }
      if (name === "popup") result.popups = webContents.getAllWebContents().filter(item => item.getURL() === origin + "/opened")
        .map(item => ({ id: item.id, sandbox: item.getLastWebPreferences().sandbox, debugger: item.debugger.isAttached() }));
      return result;
    }, { origin, frameOrigin, name: scenario.name });
    assert.ok(observation.pages?.sandbox); assert.equal(observation.pages.debugger, false);
    if (scenario.name === "select") assert.equal(observation.pages.dom.value, "two");
    if (scenario.name === "dynamic") { assert.equal(observation.pages.dom.result, "Dynamic ready"); assert.equal(observation.pages.dom.dynamic, true); }
    if (scenario.name === "frame") { assert.match(observation.pages.frame?.text || "", /Frame applied/u); assert.notEqual(observation.pages.frame.pid, observation.pages.pid); }
    const desktop = await window.evaluate(() => window.sparkclawDesktop.state());
    if (scenario.name === "popup") {
      assert.ok(observation.popup_during_task, "owned popup must be observed during the task");
      // Normal release keeps the current conversation view and cleans other
      // owned tabs. The workflow returns to the parent to verify its effect.
      assert.equal(observation.pages.popups.length, 0);
      assert.equal(desktop.pages.filter(page => page.role === "task" && page.local_conversation_id === conversation.id).length, 1);
    }
    assert.ok(desktop.pages.some(page => page.page_ref === personal && page.role === "personal"));
    const byCommand = (a, b) => a.command_id.localeCompare(b.command_id);
    assert.deepEqual([...desktop.browser_host.unknown_writes].sort(byCommand), [...evidence.unknown_before].sort(byCommand));
    observation.passed = observation.workflow_completed;
    evidence.checks[`packaged_${scenario.name}_actual_effect_and_scope`] = true;
    console.log(JSON.stringify({ operation: scenario.name, actual_effect: true, workflow_completed: observation.workflow_completed }));
  }
  assert.ok(evidence.operation_cases.every(row => row.workflow_completed), "Every operation must satisfy the workflow completion rule");
}
