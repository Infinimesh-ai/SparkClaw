import assert from "node:assert/strict";
import test from "node:test";
import vm from "node:vm";

import { PlaywrightCLITask } from "../src/cli-task.mjs";
import { probeQQMailLogin, QQMAIL_LOGIN_PROBE_SELECTORS as selectors } from "../../../scripts/email/qqmail-login-probe.mjs";

function harness({
  accountPresent = true, accountVisible = true, loginVisible = false,
  accountText = "test@example.test", url = "https://wx.mail.qq.com/home/index",
  loadingEvaluations = 0,
} = {}) {
  let evaluations = 0;
  let reads = 0;
  const task = new PlaywrightCLITask({
    state: { sessionID: "probe-batch-test" },
    registration: { operation: "probe", timeoutMS: 90_000, origins: ["https://mail.qq.com", "https://wx.mail.qq.com"] },
  });
  const element = (visible, text = "") => ({
    isConnected: true, innerText: text,
    getBoundingClientRect: () => ({ width: visible ? 100 : 0, height: 20 }),
  });
  const elements = new Map([
    [selectors.accountMarker, accountPresent ? element(accountVisible, accountText) : null],
    [selectors.loginPage, element(loginVisible)],
  ]);
  task.evaluate = async (expression) => {
    evaluations += 1;
    if(loadingEvaluations && evaluations>loadingEvaluations) elements.set(selectors.accountMarker,element(true,accountText));
    return vm.runInNewContext(`(${expression})()`, {
      URL, location: { href: url },
      document: {
        querySelector: (selector) => {
          reads += 1;
          assert.ok(elements.has(selector), `unexpected selector: ${selector}`);
          return elements.get(selector);
        },
      },
      getComputedStyle: () => ({ display: "block", visibility: "visible", opacity: "1" }),
    });
  };
  return {
    task, evaluations: () => evaluations, reads: () => reads,
    probe: () => probeQQMailLogin({
      schema_version: 1, operation: "probe", provider: "qq_mail", account: "default", invocation_id: "batch-test",
    }, { withTaskTab: async (_operation, callback) => {
      const adapter=task.qqTask();
      return callback({onTab:commands=>commands[0]?.[0]==='wait'?Promise.resolve([{success:true,result:{}}]):adapter.onTab(commands)});
    } }),
  };
}

test("QQ probe accepts a visible account header with one evaluation of two markers", async () => {
  const h = harness();
  assert.equal((await h.probe()).account_hint, "te***@example.test");
  assert.equal(h.evaluations(), 1);
  assert.equal(h.reads(), 3);
});

test('QQ probe tolerates bounded account-header loading without accepting an unknown account',async()=>{
 const h=harness({accountPresent:false,loadingEvaluations:2});
 assert.equal((await h.probe()).account_hint,'te***@example.test');
 assert.equal(h.evaluations(),3);
});

test("a visible login page requires login even when a stale account header remains", async () => {
  for (const accountPresent of [false, true]) {
    await assert.rejects(harness({ accountPresent, loginVisible: true }).probe(), { code: "email_login_required" });
  }
});

test("missing, hidden, or invalid account markers and non-mail routes stay unrecognized", async () => {
  for (const options of [
    { accountPresent: false }, { accountVisible: false },
    { accountText: "" }, { accountText: "Sign in" },
    { url: "https://wx.mail.qq.com/login" },
  ]) {
    await assert.rejects(harness(options).probe(), { code: "page_contract_changed" });
  }
});

test("probe reads reject foreign origins and URL credentials before reading DOM", async () => {
  for (const url of ["https://example.test/", "https://user:pass@wx.mail.qq.com/home/index"]) {
    const h = harness({ url });
    await assert.rejects(h.probe());
    assert.equal(h.reads(), 0);
  }
});

test("batched probe rejects malformed URL, text, and visibility", async () => {
  for (const [command, result] of [
    [["get", "url"], { url: "https://example.test" }],
    [["is", "visible", "x"], { visible: "true" }],
    [["get", "text", "x"], { text: null }],
  ]) {
    const h = harness();
    h.task.evaluate = async () => ({ url: "https://wx.mail.qq.com/home/index", results: [result] });
    await assert.rejects(h.task.qqTask().onTab([command]));
  }
});
