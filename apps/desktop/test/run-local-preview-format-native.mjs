import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
if(process.argv.length<4)throw new Error('Usage: node run-local-preview-format-native.mjs FIXTURES CONVERTED');
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../../..');
const temporary=await fs.mkdtemp(path.join(os.tmpdir(),'sparkx-format-native-'));
try{
 const env={...process.env,SPARKCLAW_FORMAT_NATIVE_TEMP:temporary,SPARKCLAW_FORMAT_FIXTURES:path.resolve(process.argv[2]),SPARKCLAW_FORMAT_CONVERTED:path.resolve(process.argv[3])};delete env.ELECTRON_RUN_AS_NODE;
 const {stdout}=await promisify(execFile)((await import('electron')).default,[path.join(root,'apps/desktop/test/local-preview-format-native-fixture.mjs')],{cwd:root,env,timeout:120000,maxBuffer:2**20});
 const result=JSON.parse(stdout.trim());if(!result.passed)throw new Error('Incomplete native format result');process.stdout.write(stdout);
}finally{await fs.rm(temporary,{recursive:true,force:true});}
