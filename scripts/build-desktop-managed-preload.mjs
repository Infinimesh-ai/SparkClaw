import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const componentManifestPath = path.join(ROOT, "configs", "browser-components.json");
const templatePath = path.join(ROOT, "apps", "desktop", "src", "preload", "managed-script-runtime.template.cjs");
const preloadPath = path.join(ROOT, "apps", "desktop", "src", "preload", "managed-scripts.cjs");
const desktopManifestPath = path.join(ROOT, "apps", "desktop", "src", "browser", "managed-scripts.json");
const check = process.argv.includes("--check");

const componentManifest = JSON.parse(await fs.readFile(componentManifestPath, "utf8"));
if (componentManifest?.version !== 1 || !Array.isArray(componentManifest.scripts)) {
  throw new Error("Browser component manifest is invalid");
}

const scripts = [];
for (const entry of componentManifest.scripts) {
  if (!entry || typeof entry.file !== "string" || typeof entry.uuid !== "string" ||
      typeof entry.version !== "string" || !Array.isArray(entry.requires) || entry.requires.length !== 0) {
    throw new Error("Managed script manifest entry is invalid");
  }
  const scriptPath = path.join(ROOT, "tools", "browser-userscripts", entry.file);
  const source = await fs.readFile(scriptPath, "utf8");
  const sha256 = crypto.createHash("sha256").update(source).digest("hex");
  if (sha256 !== entry.sha256) throw new Error(`Managed script checksum mismatch: ${entry.file}`);
  const metadata = parseMetadata(source, entry.file);
  if (metadata.version !== entry.version) throw new Error(`Managed script version mismatch: ${entry.file}`);
  scripts.push({
    id: entry.uuid,
    file: entry.file,
    version: entry.version,
    sha256,
    matches: metadata.matches,
    origins: [...new Set(metadata.matches.map((match) => match.origin))].sort(),
    runAt: metadata.runAt,
    grants: metadata.grants,
    noframes: metadata.noframes,
    source,
  });
}

const template = await fs.readFile(templatePath, "utf8");
const marker = "/*__SPARKCLAW_MANAGED_SCRIPTS__*/";
if (template.split(marker).length !== 2) throw new Error("Managed script preload template marker is invalid");
const preloadDefinitions = scripts.map((script) => {
  const metadata = JSON.stringify({ ...script, source: undefined });
  if (script.grants.length === 1 && script.grants[0] === "none") {
    return `Object.assign(${metadata},{install:function(){${script.source}\n` +
      `return {state:globalThis.SparkClawMailReader?.version===${JSON.stringify(script.version)}?"ready":"unavailable"};}})`;
  }
  return `Object.assign(${metadata},{source:${JSON.stringify(script.source)}})`;
});
const preload = template.replace(marker, `[${preloadDefinitions.join(",\n")}]`);
const desktopManifest = `${JSON.stringify({
  schema_version: 1,
  scripts: scripts.map(({ source: _source, matches, ...script }) => ({ ...script, matches })),
}, null, 2)}\n`;

if (check) {
  if (await fs.readFile(preloadPath, "utf8") !== preload) throw new Error("Desktop managed preload is stale");
  if (await fs.readFile(desktopManifestPath, "utf8") !== desktopManifest) {
    throw new Error("Desktop managed script manifest is stale");
  }
} else {
  await fs.writeFile(preloadPath, preload);
  await fs.writeFile(desktopManifestPath, desktopManifest);
}

function parseMetadata(source, file) {
  const block = source.match(/\/\/ ==UserScript==\n([\s\S]*?)\/\/ ==\/UserScript==/u)?.[1];
  if (!block) throw new Error(`Managed script metadata is missing: ${file}`);
  const values = new Map();
  for (const line of block.split("\n")) {
    const match = /^\/\/ @([A-Za-z-]+)(?:\s+(.+?))?\s*$/u.exec(line);
    if (!match) continue;
    const rows = values.get(match[1]) ?? [];
    rows.push(match[2] ?? "");
    values.set(match[1], rows);
  }
  const matches = (values.get("match") ?? []).map((raw) => {
    const match = /^(https:\/\/[^/*?#]+)(\/\*)$/u.exec(raw);
    if (!match) throw new Error(`Unsupported managed-script match pattern in ${file}`);
    const url = new URL(`${match[1]}/`);
    if (url.username || url.password || url.port || url.pathname !== "/") {
      throw new Error(`Unsafe managed-script match pattern in ${file}`);
    }
    return { origin: url.origin, pathPrefix: "/" };
  });
  if (!matches.length) throw new Error(`Managed script has no match origins: ${file}`);
  const grants = values.get("grant") ?? [];
  const allowedGrants = new Set(["none", "GM_getValue", "GM_setValue", "GM_registerMenuCommand"]);
  if (grants.some((grant) => !allowedGrants.has(grant)) || grants.includes("none") && grants.length !== 1) {
    throw new Error(`Managed script grant is unsupported: ${file}`);
  }
  return {
    version: values.get("version")?.[0] ?? "",
    matches,
    runAt: values.get("run-at")?.[0] ?? "document-end",
    grants,
    noframes: values.has("noframes"),
  };
}
