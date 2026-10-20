import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import { loadPersonalPage } from "../src/browser/personal-page-load.mjs";

const url = "https://fixture.test/redirect";
const aborted = () => Object.assign(new Error("navigation replaced"), { code: "ERR_ABORTED", errno: -3 });
function fixture(load) {
  const contents = new EventEmitter();
  Object.assign(contents, {
    loading: true, destroyed: false,
    isDestroyed() { return this.destroyed; },
    isLoadingMainFrame() { return this.loading; },
    loadURL: (...args) => load(contents, ...args),
  });
  return contents;
}
const assertClean = (contents) => assert.deepEqual(contents.eventNames(), []);
const tick = () => new Promise((resolve) => setImmediate(resolve));

test("ordinary personal loads and real load failures retain their result", async () => {
  const contents = fixture(async (_contents, requested) => assert.equal(requested, url));
  await loadPersonalPage(contents, url);
  assertClean(contents);
  for (const error of [Object.assign(new Error("offline"), { code: "ERR_INTERNET_DISCONNECTED", errno: -106 }), new Error("text mentions ERR_ABORTED")]) {
    const failed = fixture(async () => { throw error; });
    await assert.rejects(loadPersonalPage(failed, url), (result) => result === error);
    assertClean(failed);
  }
});

test("aborted initial load waits for the replacement document to finish", async () => {
  const contents = fixture(async () => { throw aborted(); });
  let completed = false;
  const loading = loadPersonalPage(contents, url).then(() => { completed = true; });
  await tick();
  assert.equal(completed, false);
  contents.emit("did-fail-load", {}, -105, "ERR_NAME_NOT_RESOLVED", "https://frame.test", false);
  contents.loading = false;
  contents.emit("did-finish-load");
  contents.emit("did-stop-loading");
  await loading;
  assert.equal(completed, true);
  assertClean(contents);
});

test("replacement finishing before the original rejection is still recognized", async () => {
  const contents = fixture(async (page) => {
    page.loading = false;
    page.emit("did-finish-load");
    page.emit("did-stop-loading");
    throw aborted();
  });
  await loadPersonalPage(contents, url);
  assertClean(contents);
});

test("a main-frame failure after abort is reported even if the error page finishes", async () => {
  const contents = fixture(async () => { throw aborted(); });
  const loading = loadPersonalPage(contents, url);
  const rejected = assert.rejects(loading, { code: "ERR_CERT_AUTHORITY_INVALID", errno: -202 });
  await tick();
  contents.emit("did-fail-load", {}, -202, "ERR_CERT_AUTHORITY_INVALID", url, true);
  contents.emit("did-finish-load");
  await rejected;
  assertClean(contents);
});

for (const ending of ["already-stopped", "did-stop-loading", "destroyed", "render-process-gone", "timeout"]) {
  test(`aborted load without a successful replacement rejects on ${ending}`, async () => {
    const error = aborted();
    const contents = fixture(async () => { throw error; });
    if (ending === "already-stopped") contents.loading = false;
    const rejected = assert.rejects(loadPersonalPage(contents, url, { timeoutMS: 30 }), (result) => result === error);
    await tick();
    if (!["already-stopped", "timeout"].includes(ending)) {
      contents.loading = false;
      contents.destroyed = ending === "destroyed";
      contents.emit(ending);
    }
    await rejected;
    assertClean(contents);
  });
}
