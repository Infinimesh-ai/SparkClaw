import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";

const SUPPORTED_PERMISSIONS = new Set(["clipboard-read", "geolocation", "media", "notifications"]);
const MAX_DOWNLOAD_BYTES = 1 << 30;
const MAX_DOWNLOADS = 100;

export class OwnerBrowserServices {
  constructor({ browserSession, workbenchSession, registry, workbench, downloadsRoot, shell, onChange }) {
    this.browserSession = browserSession;
    this.workbenchSession = workbenchSession;
    this.registry = registry;
    this.workbench = workbench;
    this.downloadsRoot = downloadsRoot;
    this.shell = shell;
    this.onChange = onChange;
    this.downloads = new Map();
    this.permissions = new Map();
    this.grants = new Set();
  }

  async start() {
    await fs.mkdir(this.downloadsRoot, { recursive: true, mode: 0o700 });
    this.browserSession.on("will-download", (_event, item, webContents) => {
      const record = this.registry.recordForWebContents(webContents.id);
      if (record?.role === "personal") this.#download(item, "personal", record.pageRef);
    });
    this.workbenchSession.on("will-download", (_event, item, webContents) => {
      if (webContents === this.workbench.webContents) this.#download(item, "workbench", "");
    });
    this.browserSession.setPermissionCheckHandler((webContents, permission, origin, details) => {
      const record = webContents ? this.registry.recordForWebContents(webContents.id) : null;
      return record?.role === "personal" && this.grants.has(permissionKey(origin, permission, details?.mediaType));
    });
    this.browserSession.setPermissionRequestHandler((webContents, permission, callback, details) => {
      const record = this.registry.recordForWebContents(webContents.id);
      const origin = secureOrigin(details?.requestingUrl || webContents.getURL());
      const mediaTypes = Array.isArray(details?.mediaTypes) ? details.mediaTypes.filter((value) => ["audio", "video"].includes(value)) : [];
      if (record?.role !== "personal" || !origin || !SUPPORTED_PERMISSIONS.has(permission) ||
          permission === "media" && mediaTypes.length === 0) {
        callback(false);
        return;
      }
      const permissionRef = `permission_${crypto.randomUUID().replaceAll("-", "")}`;
      const timer = setTimeout(() => this.#resolvePermission(permissionRef, false), 30_000);
      timer.unref?.();
      this.permissions.set(permissionRef, {
        permissionRef, pageRef: record.pageRef, origin, permission, mediaTypes, callback, timer,
      });
      this.onChange();
    });
    return this;
  }

  snapshot() {
    return {
      downloads: [...this.downloads.values()].map((record) => ({
        download_ref: record.downloadRef,
        source: record.source,
        page_ref: record.pageRef,
        filename: record.filename,
        state: record.state,
        received_bytes: record.receivedBytes,
        total_bytes: record.totalBytes,
      })),
      permissions: [...this.permissions.values()].map((request) => ({
        permission_ref: request.permissionRef,
        page_ref: request.pageRef,
        origin: request.origin,
        permission: request.permission,
        media_types: request.mediaTypes,
      })),
    };
  }

  respondPermission(permissionRef, allow) {
    if (typeof allow !== "boolean") throw new Error("Permission decision is invalid");
    const request = this.permissions.get(permissionRef);
    if (!request) throw new Error("Permission request is unavailable");
    if (allow) {
      if (request.permission === "media") {
        for (const mediaType of request.mediaTypes) this.grants.add(permissionKey(request.origin, request.permission, mediaType));
      } else {
        this.grants.add(permissionKey(request.origin, request.permission));
      }
    }
    this.#resolvePermission(permissionRef, allow);
  }

  cancelDownload(downloadRef) {
    const record = this.#downloadRecord(downloadRef);
    if (record.state === "in_progress") record.item.cancel();
  }

  showDownload(downloadRef) {
    const record = this.#downloadRecord(downloadRef);
    if (record.state !== "complete" || !record.path) throw new Error("Download is unavailable");
    this.shell.showItemInFolder(record.path);
  }

  close() {
    for (const ref of [...this.permissions.keys()]) this.#resolvePermission(ref, false);
    for (const record of this.downloads.values()) {
      if (record.state === "in_progress") record.item.cancel();
    }
  }

  #download(item, source, pageRef) {
    if (this.downloads.size >= MAX_DOWNLOADS) {
      item.cancel();
      return;
    }
    const downloadRef = `download_${crypto.randomUUID().replaceAll("-", "")}`;
    const filename = safeFilename(item.getFilename());
    const target = path.join(this.downloadsRoot, `${Date.now()}-${downloadRef.slice(-8)}-${filename}`);
    item.setSavePath(target);
    const record = {
      downloadRef, source, pageRef, filename, path: target, item,
      state: "in_progress", receivedBytes: 0, totalBytes: Math.max(0, item.getTotalBytes()),
    };
    this.downloads.set(downloadRef, record);
    item.on("updated", () => {
      record.receivedBytes = item.getReceivedBytes();
      record.totalBytes = Math.max(record.totalBytes, item.getTotalBytes());
      if (record.receivedBytes > MAX_DOWNLOAD_BYTES || record.totalBytes > MAX_DOWNLOAD_BYTES) item.cancel();
      this.onChange();
    });
    item.once("done", (_event, state) => {
      record.receivedBytes = item.getReceivedBytes();
      record.state = state === "completed" ? "complete" : "interrupted";
      this.onChange();
    });
    this.onChange();
  }

  #resolvePermission(permissionRef, allow) {
    const request = this.permissions.get(permissionRef);
    if (!request) return;
    this.permissions.delete(permissionRef);
    clearTimeout(request.timer);
    request.callback(allow);
    this.onChange();
  }

  #downloadRecord(downloadRef) {
    if (typeof downloadRef !== "string" || !/^download_[a-f0-9]{32}$/u.test(downloadRef)) {
      throw new Error("Download reference is invalid");
    }
    const record = this.downloads.get(downloadRef);
    if (!record) throw new Error("Download is unavailable");
    return record;
  }
}

function safeFilename(value) {
  const basename = path.basename(typeof value === "string" ? value : "download");
  const cleaned = basename.replaceAll(/[^\p{L}\p{N}._ -]/gu, "_").slice(0, 160);
  return cleaned && cleaned !== "." && cleaned !== ".." ? cleaned : "download";
}

function secureOrigin(raw) {
  try {
    const url = new URL(raw);
    if (url.protocol !== "https:" || url.username || url.password) return "";
    return url.origin;
  } catch {
    return "";
  }
}

function permissionKey(origin, permission, mediaType = "") {
  return `${secureOrigin(origin)}\u0000${permission}\u0000${mediaType || ""}`;
}
