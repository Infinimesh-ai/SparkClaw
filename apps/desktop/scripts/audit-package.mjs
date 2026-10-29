import fs from "node:fs/promises";
import path from "node:path";
import crypto from "node:crypto";

const privatePattern = /(?:^|\/)(?:\.git|\.env(?:\.[^/]*)?|data|runtime|qualifications?|private|credentials?|\.tools|services|configs|InfiniCenter)(?:\/|$)|(?:^|\/)(?:desktop-client|desktop-profile|iscp-launch-profile|local-workbench|desktop-connection|backend|credential)\.(?:json|bin)$|\.(?:pem|key|sqlite(?:-wal|-shm)?|db|dump)$/iu;
const desktopRoots = ["package.json", "src", "bin", "node_modules"];
// Follow only the desktop's locked production graph. Adding a renderer or
// development dependency does not silently widen the packaged main process.
const approvedDependencies = await lockedDesktopDependencies();

async function lockedDesktopDependencies() {
  const lock = JSON.parse(await fs.readFile(new URL("../../../package-lock.json", import.meta.url), "utf8"));
  const manifest = JSON.parse(await fs.readFile(new URL("../package.json", import.meta.url), "utf8"));
  const names = new Set(), visited = new Set();
  const resolve = (base, name) => {
    for (let current = base; ; current = current.includes("/") ? current.slice(0, current.lastIndexOf("/")) : "") {
      const candidate = `${current ? current + "/" : ""}node_modules/${name}`;
      if (lock.packages[candidate]) return candidate;
      if (!current) throw new Error("A desktop runtime dependency is absent from the lockfile");
    }
  };
  const visit = (entry) => {
    if (visited.has(entry)) return;
    visited.add(entry);
    const record = lock.packages[entry];
    if (!record) throw new Error("A desktop runtime dependency is absent from the lockfile");
    if (record.link) return visit(record.resolved);
    for (const name of Object.keys({ ...record.dependencies, ...record.optionalDependencies })) { names.add(name); visit(resolve(entry, name)); }
  };
  for (const name of Object.keys(manifest.dependencies)) { names.add(name); visit(resolve("apps/desktop", name)); }
  return names;
}

export function auditApplicationEntries(entries) {
  for (const raw of entries) {
    const entry = raw.replaceAll("\\", "/").replace(/^\//u, "");
    const parts = entry.split("/");
    if (!entry || parts.some((part) => part === ".." || part === ".") || !desktopRoots.includes(parts[0]) || parts[0] !== "node_modules" && privatePattern.test(entry)) {
      throw new Error("Installation package contains a file outside the public client allowlist");
    }
    if (parts[0] === "node_modules") {
      for (let index = 0; index < parts.length - 1; index++) {
        if (parts[index] !== "node_modules") continue;
        const name = parts[index + 1].startsWith("@") ? parts.slice(index + 1, index + 3).join("/") : parts[index + 1];
        if (!approvedDependencies.has(name) && !(name.startsWith("@") && !name.includes("/") && [...approvedDependencies].some(value => value.startsWith(name + "/")))) {
          throw new Error("Installation package contains an unapproved client dependency");
        }
      }
    }
  }
}

// electron-builder invokes this on the Mac after assembling its own runtime.
// It audits our ASAR and extra resources before generating distributables.
export default async function auditPackage(context) {
  const { listPackage } = await import("@electron/asar");
  const resources = context.electronPlatformName === "darwin"
    ? path.join(context.appOutDir, `${context.packager.appInfo.productFilename}.app`, "Contents", "Resources")
    : path.join(context.appOutDir, "resources");
  auditApplicationEntries(listPackage(path.join(resources, "app.asar")));
  if (context.electronPlatformName === "darwin") {
    const plist = await fs.readFile(path.join(resources, "..", "Info.plist"), "utf8");
    const icon = plist.match(/<key>CFBundleIconFile<\/key>\s*<string>([^<]+)<\/string>/u)?.[1];
    if (!icon || /electron/iu.test(icon) || path.basename(icon) !== icon) throw new Error("Mac package must use the SparkX application icon");
    await fs.access(path.join(resources, icon.endsWith(".icns") ? icon : `${icon}.icns`));
  }
  const webchat = path.join(resources, "webchat");
  const helper = path.join(resources, "iscp-workbench");
  const helperInfo = await fs.lstat(helper);
  if (!helperInfo.isFile() || helperInfo.isSymbolicLink() || !(helperInfo.mode & 0o111)) throw new Error("Installation package requires an executable ISCP helper");
  const helperSHA256 = crypto.createHash("sha256").update(await fs.readFile(helper)).digest("hex");
  const entries = await walk(webchat);
  if (!entries.includes("index.html") || entries.some((entry) => privatePattern.test(entry) || !/\.(?:html|js|css|png|svg|webp|woff2|json|ico)$/iu.test(entry))) {
    throw new Error("Installation package WebChat resources are outside the built UI allowlist");
  }
  const previewEntries = await walk(path.join(resources, "local-preview"));
  if (!previewEntries.includes("index.html") || !previewEntries.some(entry => entry.includes("pdf.worker") && entry.endsWith(".mjs")) ||
      previewEntries.some(entry => !/\.(?:html|js|mjs|css|wasm|bcmap|pfb|ttf|otf|txt|json)$/iu.test(entry))) {
    throw new Error("Installation package requires the offline local preview assets");
  }
  await fs.writeFile(path.join(resources, "client-package-audit.json"), JSON.stringify({
    schema_version: 1, platform: context.electronPlatformName,
    client_scope: "public_client_source_and_ui", application_entries: listPackage(path.join(resources, "app.asar")).length,
    ui_files: entries.length,
    preview_files: previewEntries.length,
    iscp_helper_sha256: helperSHA256,
  }), { mode: 0o644 });
}

async function walk(directory, prefix = "") {
  const result = [];
  for (const file of await fs.readdir(directory, { withFileTypes: true })) {
    const relative = `${prefix}${file.name}`;
    if (file.isSymbolicLink()) throw new Error("UI package resource cannot be a symbolic link");
    if (file.isDirectory()) result.push(...await walk(path.join(directory, file.name), `${relative}/`));
    else if (file.isFile()) result.push(relative);
    else throw new Error("UI package resource must be a regular file");
  }
  return result;
}
