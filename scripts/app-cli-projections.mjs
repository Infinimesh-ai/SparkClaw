import {isDeepStrictEqual} from 'node:util';

export function projectApplicationScript(entry, previous, release) {
  const path = `/runtimes/browser/assets/mail/${entry.file}`;
  const prefix = `${release.source_repository}/blob/`;
  const {source, url, ...priorAsset} = previous ?? {};
  const revision = typeof url === 'string' && url.startsWith(prefix) && url.endsWith(path)
    ? url.slice(prefix.length, -path.length) : '';
  // Retain the provenance of an already frozen, byte-identical asset. Changing
  // its URL alone changes native provisioning and requires a browser reimport.
  const unchanged = source === 'app-cli' && /^[a-f0-9]{40}$/u.test(revision)
    && /^[a-f0-9]{64}$/u.test(entry.sha256) && isDeepStrictEqual(priorAsset, entry);
  return {...entry, source:'app-cli', url:unchanged ? url : `${prefix}${release.source_commit}${path}`};
}
