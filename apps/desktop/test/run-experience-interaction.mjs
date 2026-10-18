import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { build } from "vite";
import react from "@vitejs/plugin-react";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const temporary = await fs.mkdtemp(path.join(os.tmpdir(), "sparkx-experience-interaction-"));
try {
  await build({ define: { "process.env.NODE_ENV": JSON.stringify("production") }, configFile: false, plugins: [react()], root: path.join(root, "apps/webchat"), logLevel: "error",
    build: { outDir: path.join(temporary, "ui"), emptyOutDir: true,
      lib: { entry: path.join(root, "apps/desktop/test/experience-interaction.tsx"), formats: ["iife"], name: "ExperienceInteraction", fileName: () => "fixture.js", cssFileName: "fixture" } } });
  const environment = { ...process.env, SPARKCLAW_INTERACTION_ROOT: temporary };
  delete environment.ELECTRON_RUN_AS_NODE;
  const result = await promisify(execFile)((await import("electron")).default,
    [path.join(root, "apps/desktop/test/experience-interaction-native.mjs")],
    { cwd: root, env: environment, timeout: 45000, maxBuffer: 1 << 20 });
  process.stdout.write(result.stdout);
} finally {
  await fs.rm(temporary, { recursive: true, force: true });
}
