import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { build } from "vite";
import react from "@vitejs/plugin-react";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const output = await fs.mkdtemp(path.join(os.tmpdir(), "sparkx-experience-visual-"));
await build({ define: { "process.env.NODE_ENV": JSON.stringify("production") }, configFile: false, plugins: [react()], root: path.join(root, "apps/webchat"), logLevel: "error",
  build: { outDir: path.join(output, "ui"), emptyOutDir: true, lib: { entry: path.join(root, "apps/desktop/test/experience-visual.tsx"), formats: ["iife"], name: "ExperienceVisual", fileName: () => "fixture.js", cssFileName: "fixture" } } });
const environment = { ...process.env, SPARKCLAW_VISUAL_OUTPUT: output };
delete environment.ELECTRON_RUN_AS_NODE;
try {
  const result = await promisify(execFile)((await import("electron")).default, [path.join(root, "apps/desktop/test/experience-visual-native.mjs")], { cwd: root, env: environment, timeout: 180000, maxBuffer: 4 << 20 });
  process.stdout.write(result.stdout);
} catch (error) {
  process.stdout.write(error.stdout ?? "");
  process.stderr.write(error.stderr ?? "");
  process.stderr.write(`Visual evidence retained at ${output}\n`);
  process.exitCode = 1;
} finally {
  // Evidence survives; the temporary profile and generated UI never enter Git.
  await fs.rm(path.join(output, "profile"), { recursive: true, force: true });
}
