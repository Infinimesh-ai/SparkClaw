import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { CONTROLLER_ERROR_CODES, ControllerError } from "../src/errors.mjs";

const testDir = path.dirname(fileURLToPath(import.meta.url));
const srcDir = path.resolve(testDir, "..", "src");
const gatewayErrorsPath = path.resolve(testDir, "..", "..", "..", "services", "gateway", "internal", "browsercontrol", "errors.go");

test("every ControllerError code thrown by the controller is in the shared code table", () => {
  const thrown = new Set();
  for (const entry of fs.readdirSync(srcDir)) {
    if (!entry.endsWith(".mjs")) continue;
    const source = fs.readFileSync(path.join(srcDir, entry), "utf8");
    for (const match of source.matchAll(/ControllerError\(\s*"([a-z_]+)"/gu)) thrown.add(match[1]);
  }
  assert.ok(thrown.size >= 10, "source scan found too few codes; the pattern may have drifted");
  for (const code of thrown) {
    assert.ok(Object.hasOwn(CONTROLLER_ERROR_CODES, code), `${code} is thrown but missing from controller-error-codes.json`);
  }
});

test("every gateway projection in the shared table is a browsercontrol code constant", () => {
  const gatewayErrors = fs.readFileSync(gatewayErrorsPath, "utf8");
  const gatewayCodes = new Set([...gatewayErrors.matchAll(/=\s*"(browser_[a-z_]+)"/gu)].map((match) => match[1]));
  assert.ok(gatewayCodes.size >= 10, "errors.go scan found too few codes");
  for (const [code, projection] of Object.entries(CONTROLLER_ERROR_CODES)) {
    assert.deepEqual(Object.keys(projection).sort(), ["gateway_code", "gateway_retryable"], code);
    assert.ok(gatewayCodes.has(projection.gateway_code), `${code} projects to unknown gateway code ${projection.gateway_code}`);
    assert.equal(typeof projection.gateway_retryable, "boolean", code);
  }
});

test("ControllerError refuses a code outside the shared table", () => {
  assert.throws(() => new ControllerError("browser_made_up", "nope"), TypeError);
  const known = new ControllerError("browser_busy", "busy", { status: 409, retryable: true });
  assert.equal(known.code, "browser_busy");
});
