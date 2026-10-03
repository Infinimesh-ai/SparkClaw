import test from "node:test";
import assert from "node:assert/strict";
import { configureWorkbenchPermissions } from "../src/main/workbench-permissions.mjs";

test("clipboard writes belong only to the workbench main frame; reads and embedded pages remain denied", () => {
  let check, request;
  const contents = {};
  configureWorkbenchPermissions({ setPermissionCheckHandler: (fn) => { check = fn; }, setPermissionRequestHandler: (fn) => { request = fn; } }, { webContents: contents });
  const url = "sparkclaw-app://workbench/index.html";
  const details = { requestingUrl: url, isMainFrame: true };
  assert.equal(check(contents, "clipboard-sanitized-write", url, details), true);
  const requested = (sender, permission, value) => { let allowed; request(sender, permission, (result) => { allowed = result; }, value); return allowed; };
  assert.equal(requested(contents, "clipboard-sanitized-write", details), true);
  for (const permission of ["clipboard-read", "deprecated-sync-clipboard-read", "unknown", "geolocation", "display-capture"]) {
    assert.equal(check(contents, permission, url, details), false);
    assert.equal(requested(contents, permission, details), false);
  }
  for (const [sender, value] of [[{}, details], [contents, { ...details, isMainFrame: false }], [contents, { ...details, requestingUrl: "https://example.test" }], [contents, { requestingUrl: url }]]) {
    assert.equal(check(sender, "clipboard-sanitized-write", value.requestingUrl, value), false);
    assert.equal(requested(sender, "clipboard-sanitized-write", value), false);
  }
  assert.equal(check(contents, "media", url, { mediaType: "audio" }), true);
  assert.equal(check(contents, "media", url, { mediaType: "video" }), false);
  assert.equal(requested(contents, "media", { ...details, mediaTypes: ["audio"] }), true);
  assert.equal(requested(contents, "media", { ...details, mediaTypes: ["audio", "video"] }), false);
});
