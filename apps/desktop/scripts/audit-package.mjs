import fs from "node:fs/promises";
import path from "node:path";

const privatePattern = /(?:^|\/)(?:\.git|\.env(?:\.[^/]*)?|data|runtime|qualifications?|private|credentials?|\.tools|services|configs|InfiniCenter)(?:\/|$)|(?:^|\/)(?:desktop-client|local-workbench|desktop-connection|backend|credential)\.(?:json|bin)$|\.(?:pem|key|sqlite(?:-wal|-shm)?|db|dump)$/iu;
const desktopRoots = ["package.json", "src", "bin", "node_modules"];

export function auditApplicationEntries(entries) {
  for (const raw of entries) {
    const entry = raw.replaceAll("\\", "/").replace(/^\//u, "");
    const parts = entry.split("/");
    if (!entry || parts.some((part) => part === ".." || part === ".") || !desktopRoots.includes(parts[0]) || privatePattern.test(entry)) {
      throw new Error("Installation package contains a file outside the public client allowlist");
    }
    if (parts[0] === "node_modules" && parts.length >= 2 &&
        !(parts[1] === "@sparkclaw" && (parts.length === 2 || parts[2] === "browser-bridge"))) {
      throw new Error("Installation package contains an unapproved client dependency");
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
  const entries = await walk(webchat);
  if (!entries.includes("index.html") || entries.some((entry) => privatePattern.test(entry) || !/\.(?:html|js|css|png|svg|woff2|json|ico)$/iu.test(entry))) {
    throw new Error("Installation package WebChat resources are outside the built UI allowlist");
  }
  await fs.writeFile(path.join(resources, "client-package-audit.json"), JSON.stringify({
    schema_version: 1, platform: context.electronPlatformName,
    client_scope: "public_client_source_and_ui", application_entries: listPackage(path.join(resources, "app.asar")).length,
    ui_files: entries.length,
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
