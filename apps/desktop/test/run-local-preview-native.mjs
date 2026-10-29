import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../../..');
const temporary=await fs.mkdtemp(path.join(os.tmpdir(),'sparkx-preview-native-'));
try{const env={...process.env,SPARKCLAW_PREVIEW_TEST_ROOT:temporary};delete env.ELECTRON_RUN_AS_NODE;const result=await promisify(execFile)((await import('electron')).default,[path.join(root,'apps/desktop/test/local-preview-native-fixture.mjs')],{cwd:root,env,timeout:60000,maxBuffer:2**20});if(!result.stdout.includes('\"navigationBlocked\":true'))throw new Error('Native preview fixture returned no completed evidence');process.stdout.write(result.stdout);}finally{await fs.rm(temporary,{recursive:true,force:true});}
