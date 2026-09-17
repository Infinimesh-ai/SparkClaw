import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";

// Keep export popups in the owned task; Chromium still performs the native download.
function routeExport({ key, origins }) {
  const originalOpen = window.open;
  const originalClick = HTMLAnchorElement.prototype.click;
  const allowed = value => {
    try {
      const url = new URL(value, location.href);
      return !url.username && !url.password && ["https:", "blob:"].includes(url.protocol) && origins.includes(url.origin);
    } catch { return false; }
  };
  window.open = function(value) {
    if (!allowed(value)) return null;
    location.assign(value);
    return { closed: false, focus() {}, close() {} };
  };
  HTMLAnchorElement.prototype.click = function() {
    const target = this.getAttribute("target");
    if (target && target !== "_self") {
      if (!allowed(this.href)) return;
      this.setAttribute("target", "_self");
    }
    try { return originalClick.call(this); }
    finally {
      if (target === null) this.removeAttribute("target");
      else this.setAttribute("target", target);
    }
  };
  globalThis[key] = () => { window.open = originalOpen; HTMLAnchorElement.prototype.click = originalClick; };
}

export async function downloadFromPage(task, selector, destination, maxBytes) {
  const key = `__sparkclaw_export_${crypto.randomUUID().replaceAll("-", "")}`;
  const temporary = path.join(task.state.outputDir, `download-${crypto.randomUUID()}`);
  const capture = `async page => {
    const origins = ${JSON.stringify(task.registration.origins)};
    if (!await page.evaluate(origins => origins.includes(location.origin), origins)) throw new Error("email_provider_origin_invalid");
    let download;
    const pending = page.waitForEvent("download", { timeout: 25000 });
    pending.catch(() => {});
    try {
      await page.evaluate(${routeExport.toString()}, {key:${JSON.stringify(key)}, origins:${JSON.stringify(task.registration.downloadOrigins ?? task.registration.origins)}});
      if (${JSON.stringify(selector === '#sparkclaw-mail-original')}) {
        // A trusted click supplies Chromium's download user activation even
        // after a slow native inspection. The one-pixel link is task-owned.
        await page.locator(${JSON.stringify(selector)}).evaluate(node => {
          node.hidden = false;
          node.style.cssText = 'position:fixed;left:0;top:0;width:1px;height:1px;overflow:hidden;opacity:0;z-index:2147483647';
        });
        await page.locator(${JSON.stringify(selector)}).click({ timeout: 10000 });
      } else await page.locator(${JSON.stringify(selector)}).evaluate(node => node.click(), undefined, { timeout: 10000 });
      download = await pending;
      const allowed = await page.evaluate(({ value, origins }) => {
        const url = new URL(value);
        return !url.username && !url.password && ["https:", "blob:"].includes(url.protocol) && origins.includes(url.origin);
      }, { value: download.url(), origins: ${JSON.stringify(task.registration.downloadOrigins ?? task.registration.origins)} });
      if (!allowed) {
        await download.cancel();
        return { status: "unavailable" };
      }
      // The SparkClaw download hook already writes the browser response into
      // the task-private output directory. Reuse that completed file directly
      // instead of asking Playwright to copy it a second time through saveAs.
      let nativePath = null;
      try { nativePath = await download.path(); } catch {}
      if (typeof nativePath === "string" && path.isAbsolute(nativePath)) {
        await fs.rename(nativePath, ${JSON.stringify(temporary)});
      } else {
        await download.saveAs(${JSON.stringify(temporary)});
      }
      return { status: "saved" };
    } finally {
      await page.evaluate(key => { globalThis[key]?.(); delete globalThis[key]; }, ${JSON.stringify(key)}).catch(() => {});
      if (download) await download.delete().catch(() => {});
      else void pending.then(async value => { await value.cancel(); await value.delete(); }).catch(() => {});
    }
  }`;
  try {
    const result = await task.runReadCode(capture, 45_000);
    if (result?.status !== "saved") throw Object.assign(new Error("email_capture_unavailable"), { code: "email_capture_unavailable" });
    const info = await fs.lstat(temporary);
    if (!info.isFile() || info.isSymbolicLink() || info.uid !== process.getuid()) throw new Error("Invalid download file");
    if (info.size > maxBytes) throw Object.assign(new Error("email_download_limit"), { code: "email_download_limit" });
    await fs.chmod(temporary, 0o600);
    await fs.copyFile(temporary, destination, fs.constants.COPYFILE_EXCL);
    return { bytes: info.size };
  } finally {
    await fs.rm(temporary, { force: true });
  }
}
