import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import vm from "node:vm";
import crypto from "node:crypto";

import { BACKGROUND_FOCUS_FUNCTION, BATCH_READ_FUNCTION, EDITOR_LINES_FUNCTION } from "../src/dom-actions.mjs";
import { createInvocationState } from "../src/cli-runtime.mjs";

const text = value => ({ nodeType: 3, textContent: value });
const br = () => ({ nodeType: 1, tagName: "BR", textContent: "" });
const div = (...childNodes) => ({ nodeType: 1, tagName: "DIV", childNodes, firstChild: childNodes[0] });
const read = childNodes => vm.runInNewContext(EDITOR_LINES_FUNCTION, { Node: { TEXT_NODE: 3, ELEMENT_NODE: 1 } })({
  getAttribute: () => "true", childNodes, textContent: childNodes.map(n => n.textContent).join(""),
});

test("background focus releases a recipient editor trap before focusing Subject", () => {
  const document = { activeElement: {} };
  let trapped = true;
  const events = [];
  const subject = {
    isConnected: true,
    dispatchEvent: event => {
      events.push(event.type);
      if (event.type === "mousedown" && event.bubbles) trapped = false;
    },
    focus: () => { if (!trapped) document.activeElement = subject; },
  };
  const focus = vm.runInNewContext(BACKGROUND_FOCUS_FUNCTION, {
    document, MouseEvent: class { constructor(type, options) { this.type = type; Object.assign(this, options); } },
  });
  assert.equal(focus(subject), true);
  assert.equal(document.activeElement, subject);
  assert.deepEqual(events, ["mousedown", "mouseup"]);
});

test("a batch captures every field before asynchronous hashing starts", async () => {
  const fields = { first: { value: "one" }, second: { value: "two" } };
  const inspect = vm.runInNewContext(BATCH_READ_FUNCTION, {
    document: { querySelector: selector => fields[selector] }, TextEncoder, Uint8Array,
    crypto: { subtle: { digest: async (...args) => {
      fields.second.value = "changed during hashing";
      return crypto.webcrypto.subtle.digest(...args);
    } } },
  });
  const result = await inspect([["get", "value", "first"], ["get", "value", "second"]]);
  assert.deepEqual(Array.from(result, entry => entry.value), ["one", "two"]);
  assert.equal(result[1].digest, crypto.createHash("sha256").update("two").digest("hex"));
});

test("QQ editor lines preserve blank lines without layout-generated innerText breaks", () => {
  assert.equal(read([div(text("First")), div(br()), div(text("Second")), div(br())]), "First\n\nSecond\n");
  assert.equal(read([div(br()), div(text("  indented  "))]), "\n  indented  ");
  assert.equal(read([text("plain\ntext")]), "plain\ntext");
  assert.equal(read([div(text("one"), br(), text("two"))]), "one\ntwo");
});

test("QQ editor rejects unexpected structure instead of normalizing away content", () => {
  assert.throws(() => read([{ nodeType: 1, tagName: "P", childNodes: [text("text")] }]));
  assert.throws(() => read([div({ nodeType: 1, tagName: "IMG" })]));
});

test("private JSON config preserves quotes, literal escapes and multiline mail exactly", async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-mail-config-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  await fs.mkdir(path.join(root, "cli-runtime"), { mode: 0o700 });
  const message = { recipient: "one@example.test", subject: `Both ' and " and backtick \``, body: {
    content: 'literal \\n and \\r\nactual newline\n"quote" # hash\tend',
  } };
  const state = await createInvocationState(path.join(root, "cli-runtime"), `session_${"a".repeat(32)}`, { message }, "send");
  const config = JSON.parse(await fs.readFile(state.secretsPath, "utf8"));
  assert.deepEqual(Object.values(config.secrets), [message.recipient, message.subject, message.body.content]);
  assert.equal((await fs.stat(state.secretsPath)).mode & 0o777, 0o600);
  await state.remove();
  assert.deepEqual(await fs.readdir(path.join(root, "cli-runtime")), []);
});
