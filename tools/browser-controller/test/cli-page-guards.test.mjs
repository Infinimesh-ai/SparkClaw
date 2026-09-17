import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import test from 'node:test';

// Follow the repository-owned static dependency closure, not just the helper's
// first import. A future guard import must not pull the task/UI stack back in.
test('batch transport depends on pure guards without loading task, DOM or download runtime',async()=>{
  const root=fileURLToPath(new URL('../src/',import.meta.url));
  const visited=new Set();
  async function visit(file){
    if(visited.has(file))return;
    visited.add(file);
    const source=await fs.readFile(file,'utf8');
    for(const match of source.matchAll(/(?:\bfrom\s*|\bimport\s*\(\s*|\brequire\s*\(\s*|\bimport\s*)['"]([^'"]+)['"]/gu)){
      const specifier=match[1];
      if(!specifier.startsWith('.')||specifier.includes('/node_modules/'))continue;
      const dependency=path.resolve(path.dirname(file),specifier);
      if(/\.(?:mjs|cjs|js)$/u.test(dependency))await visit(dependency);
    }
  }
  await visit(path.join(root,'cli-read-batch.mjs'));
  assert.ok(visited.has(path.join(root,'cli-page-guards.mjs')));
  for(const name of ['cli-task.mjs','dom-actions.mjs','cli-download.mjs','awaited-mail-read.cjs']){
    assert.equal(visited.has(path.join(root,name)),false,`batch imported ${name}`);
  }
  assert.equal([...visited].some(file=>file.includes('/scripts/email/')),false);
  const task=await fs.readFile(path.join(root,'cli-task.mjs'),'utf8');
  assert.match(task,/from "\.\/cli-page-guards\.mjs"/u);
  assert.doesNotMatch(task,/export\s+(?:function\s+(?:parseTabs|sanitizeTabListOutput|assertExpectedOrigin|assertTaskTopology|tabFingerprint)|\{[^}]*\b(?:parseTabs|sanitizeTabListOutput|assertExpectedOrigin|assertTaskTopology|tabFingerprint)\b)/u);
});
