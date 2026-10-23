import test from 'node:test';
import assert from 'node:assert/strict';
import {projectApplicationScript} from '../app-cli-projections.mjs';

const release = {source_repository:'https://github.com/example/App-CLI', source_commit:'b'.repeat(40)};
const asset = {file:'gmail-mail-reader.user.js', version:'0.2.0', sha256:'c'.repeat(64), requires:[], uuid:'1b57e83c-a914-48c1-a490-77d4bca191d5'};
const path = `/runtimes/browser/assets/mail/${asset.file}`;
const previous = {...asset, source:'app-cli', url:`${release.source_repository}/blob/${'a'.repeat(40)}${path}`};
const fresh = `${release.source_repository}/blob/${release.source_commit}${path}`;

test('unchanged frozen asset retains its original commit provenance', () => {
  assert.deepEqual(projectApplicationScript(structuredClone(asset), previous, release), previous);
  assert.equal(projectApplicationScript(asset, undefined, release).url, fresh);
});

test('changed bytes, identity or import metadata selects the new source pin', () => {
  for (const changed of [
    {...asset, sha256:'d'.repeat(64)}, {...asset, uuid:'5b4323dc-326f-4fbb-a1a0-4ac8c00631c0'},
    {...asset, version:'0.3.0'}, {...asset, requires:[{url:'https://example.invalid/library.js'}]},
  ]) assert.equal(projectApplicationScript(changed, previous, release).url, fresh);
});

test('untrusted repositories, mutable refs and unrelated paths cannot retain provenance', () => {
  for (const url of [
    previous.url.replace('example/App-CLI','other/App-CLI'),
    previous.url.replace('a'.repeat(40),'main'), previous.url.replace('a'.repeat(40),'v0.3.0'),
    previous.url.replace('a'.repeat(40),'a'.repeat(39)),
    previous.url.replace('/assets/mail/','/elsewhere/'), `${previous.url}?raw=1`,
  ]) assert.equal(projectApplicationScript(asset, {...previous,url}, release).url, fresh);
  assert.equal(projectApplicationScript(asset, {...previous,source:'other'}, release).url, fresh);
});
