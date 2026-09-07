import assert from "node:assert/strict";
import test from "node:test";
import vm from "node:vm";

import { inspectGmailLogin } from "../../../scripts/email/gmail-browser.mjs";
import { probeOutlookLogin } from "../../../scripts/email/outlook-login-probe.mjs";
import { PlaywrightCLITask } from "../src/cli-task.mjs";

const providers = [
  {
    id: "gmail", url: "https://mail.google.com/mail/u/0/#inbox",
    login: "https://accounts.google.com/v3/signin/challenge/pwd",
    unknown: "email_page_contract_changed", invalid: "email_browser_output_invalid",
    label: "Google Account: Person (person@example.test)",
  },
  {
    id: "outlook", url: "https://outlook.live.com/mail/0/inbox",
    login: "https://login.microsoftonline.com/common/oauth2/authorize",
    unknown: "outlook_page_contract_changed", invalid: "browser_output_invalid",
    label: "Account manager for person@example.test",
  },
];

function harness(provider, {
  url = provider.url, compose = true, navigation = false, visible = true, account = false, delay = 0,
  redirectURL,
} = {}) {
  let elapsed = 0;
  let inspections = 0;
  let urlReads = 0;
  let domReads = 0;
  const task = new PlaywrightCLITask({
    state: { sessionID: "provider-login-test" },
    registration: {
      operation: "probe", timeoutMS: 45_000,
      origins: [new URL(provider.url).origin, new URL(provider.login).origin],
    },
  });
  task.currentURL = async () => { urlReads += 1; return url; };
  task.evaluate = async (expression) => {
    inspections += 1;
    const location = { href: url };
    const element = {
      isConnected: true,
      getBoundingClientRect: () => ({ width: visible ? 100 : 0, height: 20 }),
      getAttribute: () => provider.label,
    };
    const value = vm.runInNewContext(expression, {
      URL, Date: { now: () => elapsed },
      setTimeout: (callback, ms) => { elapsed += ms; callback(); },
      window: {
        location,
        getComputedStyle: () => ({ display: "block", visibility: "visible", opacity: "1" }),
      },
      document: {
        querySelectorAll: (selector) => {
          domReads += 1;
          if (redirectURL && domReads === 2) location.href = redirectURL;
          if (selector.includes("Google Account:") || selector.includes("#OwaTitleBar")) {
            return account ? [element] : [];
          }
          assert.ok(selector.includes('[gh="cm"]') || selector.includes('aria-label="New mail"'));
          return (compose || (navigation && selector.includes('[role="navigation"]'))) && elapsed >= delay ? [element] : [];
        },
      },
    });
    return await (typeof value === "function" ? value() : value);
  };
  const tab = provider.id === "gmail" ? task.gmailTab() : task.outlookTab();
  const probe = async () => provider.id === "gmail"
    ? await inspectGmailLogin(tab, { includeAccountHint: true })
    : await probeOutlookLogin({
      schema_version: 1, operation: "probe", invocation_id: "login-test",
      provider: "outlook", account: "default",
    }, { withTaskTab: async (_operation, callback) => callback(tab) });
  return {
    probe, task, inspections: () => inspections, elapsed: () => elapsed,
    urlReads: () => urlReads, domReads: () => domReads,
  };
}

for (const provider of providers) {
  test(`${provider.id} accepts compose alone in one DOM inspection without waiting`, async () => {
    const h = harness(provider);
    await h.probe();
    assert.equal(h.inspections(), 1);
    assert.equal(h.urlReads(), 0);
    assert.equal(h.elapsed(), 0);
  });

  test(`${provider.id} keeps account hints optional and masked`, async () => {
    const result = await harness(provider, { account: true }).probe();
    assert.equal(result.accountHint ?? result.account_hint, "pe***@example.test");
  });

  test(`${provider.id} recognizes a login URL without enumerating login controls`, async () => {
    const h = harness(provider, { url: provider.login, compose: false });
    await assert.rejects(h.probe(), { code: "email_login_required" });
    assert.equal(h.elapsed(), 0);
  });

  test(`${provider.id} waits only for a loading compose control within the same inspection`, async () => {
    const h = harness(provider, { delay: 300 });
    await h.probe();
    assert.equal(h.inspections(), 1);
    assert.equal(h.elapsed(), 300);
  });

  test(`${provider.id} rejects hidden controls and unrecognized mailbox pages`, async () => {
    for (const options of [
      { visible: false }, { compose: false }, { url: new URL("/other", provider.url).href },
    ]) {
      const h = harness(provider, options);
      await assert.rejects(h.probe(), { code: provider.unknown });
      assert.ok(h.elapsed() <= 8000);
    }
  });

  test(`${provider.id} rejects foreign origins, URL credentials, and malformed results`, async () => {
    for (const url of ["https://example.test/mail/", provider.url.replace("https://", "https://user:pass@")]) {
      const h = harness(provider, { url });
      await assert.rejects(h.probe());
      assert.equal(h.domReads(), 0);
    }
    const h = harness(provider);
    h.task.evaluate = async () => ({
      initial_url: provider.url, origin: provider.url,
      result: { url: provider.url, compose_visible: "true" },
    });
    await assert.rejects(h.probe(), { code: provider.invalid });
  });

  test(`${provider.id} rejects a changed URL after collecting the mailbox marker`, async () => {
    for (const redirectURL of ["https://example.test/mail/", provider.login]) {
      await assert.rejects(harness(provider, { redirectURL }).probe());
    }
  });
}

test("Outlook accepts mail navigation when the compose button is absent", async () => {
  const h = harness(providers[1], { compose: false, navigation: true });
  assert.equal((await h.probe()).status, "ready");
  assert.equal(h.elapsed(), 0);
});
