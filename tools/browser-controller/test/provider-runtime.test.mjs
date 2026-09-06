import assert from "node:assert/strict";
import crypto from "node:crypto";
import test from "node:test";

import { GMAIL_SEND_SELECTORS } from "../../../scripts/email/gmail-send.mjs";
import { QQMAIL_LOGIN_PROBE_SELECTORS } from "../../../scripts/email/qqmail-login-probe.mjs";
import { QQMAIL_SELECTORS } from "../../../scripts/email/qqmail-send.mjs";
import { OUTLOOK_SEND_SELECTOR } from "../../../scripts/email/outlook-send.mjs";
import { createProviderRuntime } from "../src/cli-task.mjs";
import { ProviderScriptRegistry } from "../src/provider-scripts.mjs";

const CASES = Object.freeze([
  { provider: "qq_mail", adapter: "qq", probe: "qqmail.login_probe", send: "qqmail.send" },
  { provider: "outlook", adapter: "outlook", probe: "outlook.login_probe", send: "outlook.send" },
  { provider: "gmail", adapter: "gmail", probe: "gmail.login_probe", send: "gmail.send" },
]);

test("all provider probes execute through their injected Playwright runtime adapter", async () => {
  const registry = new ProviderScriptRegistry();
  await registry.prepare();

  for (const providerCase of CASES) {
    const registration = registry.resolve({
      provider: providerCase.provider,
      operation: "probe",
      scriptID: providerCase.probe,
      revision: 1,
    });
    const input = probeInput(providerCase.provider);
    const client = new ProviderRuntimeFixture(registration, input);

    registration.validate(input);
    const result = await registration.handler(
      input,
      createProviderRuntime(client, registration),
    );

    assert.equal(result.status, "ready", providerCase.provider);
    assert.equal(result.provider, providerCase.provider, providerCase.provider);
    assert.equal(client.adapter, providerCase.adapter, providerCase.provider);
    assert.equal(client.effectAttempted, false, providerCase.provider);
    client.assertAdapterLifecycle();
  }
});

test("QQ Mail probe owns a bounded signed-in CLI budget and preserves timeout classification", async () => {
  const registry = new ProviderScriptRegistry();
  await registry.prepare();
  const registration = registry.resolve({
    provider: "qq_mail",
    operation: "probe",
    scriptID: "qqmail.login_probe",
    revision: 1,
  });
  const client = {
    qqTask: () => ({
      onTab: async () => {
        throw Object.assign(new Error("private controller detail"), {
          code: "browser_script_timeout",
        });
      },
    }),
  };

  assert.equal(registration.timeoutMS, 90_000);
  await assert.rejects(
    registration.handler(
      probeInput("qq_mail"),
      createProviderRuntime(client, registration),
    ),
    (error) => error.code === "login_probe_timeout",
  );
});

test("all provider sends use the registered effect selector exactly once", async () => {
  const registry = new ProviderScriptRegistry();
  await registry.prepare();

  for (const providerCase of CASES) {
    const registration = registry.resolve({
      provider: providerCase.provider,
      operation: "send",
      scriptID: providerCase.send,
      revision: 1,
    });
    const input = sendInput(providerCase.provider);
    const client = new ProviderRuntimeFixture(registration, input);

    registration.validate(input);
    const result = await registration.handler(
      input,
      createProviderRuntime(client, registration),
    );

    assert.equal(result.status, "sent", providerCase.provider);
    assert.equal(result.provider, providerCase.provider, providerCase.provider);
    assert.equal(result.recipient_digest, recipientDigest(input.message.recipient));
    assert.equal(client.adapter, providerCase.adapter, providerCase.provider);
    assert.equal(client.effectAttempted, true, providerCase.provider);
    assert.deepEqual(client.effectSelectors, [registration.effectSelector], providerCase.provider);
    client.assertAdapterLifecycle();
  }
});

class ProviderRuntimeFixture {
  constructor(registration, input) {
    this.registration = registration;
    this.input = input;
    this.adapter = "";
    this.effectAttempted = false;
    this.effectSelectors = [];
    this.fields = new Map();
    this.qqComposeOpen = false;
    this.qqSent = false;
    this.outlookInspectCalls = 0;
    this.outlookActCalls = 0;
    this.gmailOpened = 0;
    this.gmailClosed = 0;
    this.gmailDisposed = 0;
    this.gmailComposeOpen = false;
    this.gmailRecipientCommitted = false;
    this.gmailSent = false;
  }

  qqTask() {
    this.adapter = "qq";
    return {
      onTab: async (commands) => commands.map((command) => ({
        success: true,
        result: this.qqCommand(command),
      })),
    };
  }

  outlookTab() {
    this.adapter = "outlook";
    return {
      inspect: async () => {
        this.outlookInspectCalls += 1;
        return outlookProbeEvidence();
      },
      act: async (command) => {
        this.outlookActCalls += 1;
        return this.outlookCommand(command);
      },
    };
  }

  gmailTab() {
    this.adapter = "gmail";
    return {
      open: async () => { this.gmailOpened += 1; },
      getUrl: async () => "https://mail.google.com/mail/u/0/#inbox",
      getCount: async (selector) => this.gmailCount(selector),
      getAttribute: async (selector, attribute) => this.gmailAttribute(selector, attribute),
      getValue: async (selector) => this.fields.get(selector) ?? "",
      getText: async (selector) => this.gmailText(selector),
      waitFor: async () => {},
      click: async (selector) => this.gmailClick(selector),
      fill: async (selector, value) => { this.fields.set(selector, value); },
      focus: async () => {},
      press: async () => { this.gmailRecipientCommitted = true; },
      closeOwnedTab: async () => { this.gmailClosed += 1; },
      dispose: async () => { this.gmailDisposed += 1; },
    };
  }

  qqCommand(command) {
    const [name, subtype, selector] = command;
    if (name === "get" && subtype === "url") return { url: this.qqURL() };
    if (name === "get" && subtype === "count") {
      return { count: this.qqCount(selector), selector };
    }
    if (name === "get" && subtype === "text") {
      return { text: this.qqText(selector), origin: this.qqURL() };
    }
    if (name === "get" && subtype === "value") {
      return { value: this.fields.get(selector) ?? "", origin: this.qqURL() };
    }
    if (name === "is" && subtype === "visible") {
      return { visible: this.qqVisible(selector), origin: this.qqURL() };
    }
    if (name === "is" && subtype === "enabled") {
      return { enabled: true, origin: this.qqURL() };
    }
    if (name === "click") {
      if (subtype === QQMAIL_SELECTORS.composeButton) this.qqComposeOpen = true;
      if (subtype === this.registration.effectSelector) {
        this.markEffect(subtype);
        this.qqComposeOpen = false;
        this.qqSent = true;
      }
      return { clicked: subtype };
    }
    if (name === "fill") {
      this.fields.set(subtype, selector);
      return { filled: subtype };
    }
    if (name === "focus") return { focused: subtype };
    if (name === "press") return { pressed: subtype };
    if (name === "wait") return { waited: subtype };
    throw new Error(`unsupported QQ Mail command: ${JSON.stringify(command)}`);
  }

  qqURL() {
    if (this.qqSent) return "https://wx.mail.qq.com/home/index#/list/5";
    if (this.qqComposeOpen) return "https://wx.mail.qq.com/home/index#/compose/new";
    return "https://wx.mail.qq.com/home/index";
  }

  qqCount(selector) {
    if (selector === QQMAIL_SELECTORS.loginPage) return 0;
    if (Object.values(QQMAIL_LOGIN_PROBE_SELECTORS).includes(selector)) {
      return selector.includes("login-page") ? 0 : 1;
    }
    return 1;
  }

  qqVisible(selector) {
    if (selector.includes("login-page")) return false;
    if (selector === QQMAIL_SELECTORS.composePage) return this.qqComposeOpen;
    if (selector === QQMAIL_SELECTORS.sentPage) return this.qqSent;
    return true;
  }

  qqText(selector) {
    if (selector === QQMAIL_LOGIN_PROBE_SELECTORS.accountMarker) return "person@example.test";
    if (selector === QQMAIL_SELECTORS.recipientChip) return this.input.message.recipient;
    if (selector === QQMAIL_SELECTORS.body) return this.input.message.body.content;
    if (selector === QQMAIL_SELECTORS.sendButton) return "Send";
    return this.fields.get(selector) ?? "";
  }

  outlookCommand(command) {
    const [name, subtype, selector] = command;
    if (name === "fill") {
      this.fields.set(subtype, selector);
      return { filled: subtype };
    }
    if (name === "get" && subtype === "value") return { value: this.fields.get(selector) ?? "" };
    if (name === "get" && subtype === "text") return { text: this.fields.get(selector) ?? "" };
    if (name === "is" && subtype === "enabled") return { enabled: true };
    if (name === "click") {
      if (subtype === this.registration.effectSelector) this.markEffect(subtype);
      return { clicked: subtype };
    }
    if (name === "eval") {
      const url = "https://outlook.live.com/mail/0/inbox";
      return {
        result: {
          contract_version: 1,
          url,
          sent_evidence: true,
          compose_open: false,
        },
        origin: url,
      };
    }
    if (["wait", "focus", "press"].includes(name)) return {};
    throw new Error(`unsupported Outlook command: ${JSON.stringify(command)}`);
  }

  gmailCount(selector) {
    if (selector === GMAIL_SEND_SELECTORS.subject && this.gmailSent) return 0;
    if (selector === GMAIL_SEND_SELECTORS.recipientChip) {
      return this.gmailRecipientCommitted ? 1 : 0;
    }
    if ([
      "[data-identifier]",
      '[jsname="rwl3qc"]',
      "input#identifierId",
      "#identifierNext",
    ].includes(selector)) {
      return 0;
    }
    return 1;
  }

  gmailAttribute(selector, attribute) {
    if (selector === GMAIL_SEND_SELECTORS.recipientChip && attribute === "email") {
      return this.input.message.recipient;
    }
    if (attribute === "aria-label") return "Google Account: Person (person@example.test)";
    return "";
  }

  gmailText(selector) {
    if (selector === GMAIL_SEND_SELECTORS.sentStatus) return "Message sent";
    return this.fields.get(selector) ?? "";
  }

  gmailClick(selector) {
    if (selector === GMAIL_SEND_SELECTORS.compose) this.gmailComposeOpen = true;
    if (selector === this.registration.effectSelector) {
      this.markEffect(selector);
      this.gmailComposeOpen = false;
      this.gmailSent = true;
    }
  }

  markEffect(selector) {
    this.effectAttempted = true;
    this.effectSelectors.push(selector);
  }

  assertAdapterLifecycle() {
    if (this.registration.provider === "outlook") {
      assert.ok(this.outlookInspectCalls >= 1);
      if (this.registration.operation === "send") assert.ok(this.outlookActCalls >= 1);
    }
    if (this.registration.provider === "gmail") {
      assert.equal(this.gmailOpened, 1);
      assert.equal(this.gmailClosed, 1);
      assert.equal(this.gmailDisposed, 1);
    }
  }
}

function outlookProbeEvidence() {
  const url = "https://outlook.live.com/mail/0/inbox";
  return {
    result: {
      contract_version: 1,
      url,
      positive: {
        app_shell: true,
        compose_command: true,
        mail_navigation: true,
      },
      negative: {
        credential_entry: false,
        account_chooser: false,
        sign_in_action: false,
      },
      account_marker: null,
    },
    origin: url,
  };
}

function probeInput(provider) {
  return {
    schema_version: 1,
    operation: "probe",
    invocation_id: `${provider}-probe-1`,
    provider,
    account: "default",
  };
}

function sendInput(provider) {
  return {
    schema_version: 1,
    operation: "send",
    invocation_id: `${provider}-send-1`,
    provider,
    account: "default",
    message: {
      recipient: "person@example.test",
      subject: "Provider runtime contract",
      body: { format: "text", content: "Line one\nLine two" },
    },
  };
}

function recipientDigest(recipient) {
  return `sha256:${crypto.createHash("sha256").update(recipient).digest("hex")}`;
}

assert.equal(QQMAIL_SELECTORS.sendButton.length > 0, true);
assert.equal(OUTLOOK_SEND_SELECTOR.length > 0, true);
