import fs from "node:fs/promises";
import path from "node:path";
import { CONTROLLER_PROTOCOL, CONTROLLER_REQUIRED_FILES, CONTROLLER_VERSIONS, controllerBundleFiles } from "../src/browser/controller-bundle.mjs";

export async function syntheticControllerBundle(root) {
  for (const name of CONTROLLER_REQUIRED_FILES) {
    await fs.mkdir(path.dirname(path.join(root, name)), { recursive: true });
    await fs.writeFile(path.join(root, name), "synthetic bundled source");
  }
  for (const [name, version] of Object.entries(CONTROLLER_VERSIONS)) {
    const filename = path.join(root, "node_modules", name, "package.json");
    await fs.mkdir(path.dirname(filename), { recursive: true });
    await fs.writeFile(filename, JSON.stringify({ version }));
  }
  const manifest = { schema_version: 1, protocol: CONTROLLER_PROTOCOL, source_revision: "synthetic",
    versions: CONTROLLER_VERSIONS, ...await controllerBundleFiles(root) };
  await fs.writeFile(path.join(root, "manifest.json"), JSON.stringify(manifest));
  return manifest;
}
