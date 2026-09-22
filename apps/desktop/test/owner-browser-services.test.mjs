import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { OwnerBrowserServices } from "../src/main/owner-browser-services.mjs";

test("owner browser services scope personal downloads and require an explicit permission decision", async () => {
  const temporary = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-owner-browser-services-"));
  const browserSession = sessionMock();
  const workbenchSession = sessionMock();
  const records = new Map([[21, { role: "personal", pageRef: "page_personal" }], [22, { role: "task", pageRef: "page_task" }]]);
  const changes = [];
  const services = await new OwnerBrowserServices({
    browserSession,
    workbenchSession,
    registry: { recordForWebContents: (id) => records.get(id) ?? null },
    workbench: { webContents: { id: 1 } },
    downloadsRoot: temporary,
    shell: { showItemInFolder() {} },
    onChange: () => changes.push(true),
  }).start();

  const personalDownload = downloadItem("report.txt");
  browserSession.emit("will-download", {}, personalDownload, { id: 21 });
  browserSession.emit("will-download", {}, downloadItem("task.txt"), { id: 22 });
  assert.match(personalDownload.savePath, /report\.txt$/u);
  assert.equal(services.snapshot().downloads.length, 1);
  personalDownload.emit("done", {}, "completed");
  assert.equal(services.snapshot().downloads[0].state, "complete");

  let permissionDecision;
  browserSession.permissionRequest({ id: 21, getURL: () => "https://example.test/page" }, "media",
    (allow) => { permissionDecision = allow; }, { requestingUrl: "https://example.test/page", mediaTypes: ["audio"] });
  const request = services.snapshot().permissions[0];
  assert.equal(request.origin, "https://example.test");
  services.respondPermission(request.permission_ref, true);
  assert.equal(permissionDecision, true);
  assert.equal(browserSession.permissionCheck({ id: 21 }, "media", "https://example.test", { mediaType: "audio" }), true);
  assert.equal(browserSession.permissionCheck({ id: 22 }, "media", "https://example.test", { mediaType: "audio" }), false);
  assert.ok(changes.length >= 3);
  services.close();
  await fs.rm(temporary, { recursive: true, force: true });
});

function sessionMock() {
  const target = new EventEmitter();
  target.setPermissionCheckHandler = (handler) => { target.permissionCheck = handler; };
  target.setPermissionRequestHandler = (handler) => { target.permissionRequest = handler; };
  return target;
}

function downloadItem(filename) {
  const item = new EventEmitter();
  item.getFilename = () => filename;
  item.getTotalBytes = () => 12;
  item.getReceivedBytes = () => 12;
  item.setSavePath = (value) => { item.savePath = value; };
  item.cancel = () => { item.canceled = true; };
  return item;
}
