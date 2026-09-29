#!/usr/bin/env node
// Consumer projection only. Application identity and budgets are owned by the
// verified App-CLI package; no handler registry is loaded by this generator.
import fs from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
const source = fileURLToPath(import.meta.resolve('@infinimesh/app-cli-runtime/bindings/provider-scripts.json'));
const target = new URL('../../../services/gateway/internal/emailautomation/provider_scripts.json', import.meta.url);
const rendered = await fs.readFile(source, 'utf8');
if (process.argv.includes('--write')) await fs.writeFile(target, rendered);
else if (process.argv.includes('--check')) {
  if (await fs.readFile(target, 'utf8') !== rendered) throw new Error('Gateway App-CLI projection is stale');
} else process.stdout.write(rendered);
