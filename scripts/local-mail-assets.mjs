import fs from 'node:fs';

const root = new URL('../tools/browser-controller/mail-runtime/', import.meta.url);

export function mailAsset(relative) {
  if (!/^(?:assets\/mail\/[a-z0-9.-]+|bindings\/provider-scripts\.json)$/u.test(relative)) {
    throw new Error('Invalid local mail asset');
  }
  return fs.readFileSync(new URL(relative, root), 'utf8');
}
