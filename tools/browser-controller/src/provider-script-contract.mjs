#!/usr/bin/env node
// Gateway projection of the mail bindings maintained in this repository.
import fs from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
const source = fileURLToPath(new URL('../mail-runtime/bindings/provider-scripts.json', import.meta.url));
const target = new URL('../../../services/gateway/internal/emailautomation/provider_scripts.json', import.meta.url);
const rendered = await fs.readFile(source, 'utf8');
if (process.argv.includes('--write')) await fs.writeFile(target, rendered);
else if (process.argv.includes('--check')) {
  if (await fs.readFile(target, 'utf8') !== rendered) throw new Error('Gateway mail projection is stale');
} else process.stdout.write(rendered);
