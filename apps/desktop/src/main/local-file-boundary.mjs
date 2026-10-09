import fs from "node:fs";
import path from "node:path";
import { WORKBENCH_LIMITS } from "../shared/workbench-limits.mjs";

const same = (left, right) => left.dev === right.dev && left.ino === right.ino;
const privateOwned = stat => !(stat.mode & 0o077) && (!process.getuid || stat.uid === process.getuid());
// Darwin's O_NOFOLLOW_ANY (sys/fcntl.h) rejects symlinks in every path component.
// Linux opens the UUID relative to the already verified directory descriptor.
const noFollowAny = process.platform === "darwin" ? 0x20000000 : 0;

export function captureFileBoundary(root) {
  const canonical = fs.realpathSync(root);
  const stat = fs.lstatSync(canonical);
  if (!stat.isDirectory() || !privateOwned(stat)) throw new Error("Local file directory is unsafe");
  return { root: canonical, stat };
}

export function readOwnedFile(boundary, id, size) {
  if (!["darwin", "linux"].includes(process.platform) || !Number.isSafeInteger(size) || size < 0 || size > WORKBENCH_LIMITS.fileBytes) throw new Error("Local file cannot be read safely");
  const checkRoot = () => {
    const current = fs.lstatSync(boundary.root);
    if (!current.isDirectory() || !privateOwned(current) || !same(current, boundary.stat)) throw new Error("Local file directory changed");
  };
  checkRoot();
  const directory = fs.openSync(boundary.root, fs.constants.O_RDONLY | fs.constants.O_DIRECTORY | (noFollowAny || fs.constants.O_NOFOLLOW));
  let descriptor;
  try {
    if (!same(fs.fstatSync(directory), boundary.stat)) throw new Error("Local file directory changed");
    const filename = path.join(boundary.root, id);
    descriptor = fs.openSync(process.platform === "linux" ? `/proc/self/fd/${directory}/${id}` : filename,
      fs.constants.O_RDONLY | fs.constants.O_NONBLOCK | (noFollowAny || fs.constants.O_NOFOLLOW));
    const before = fs.fstatSync(descriptor);
    checkRoot();
    const named = fs.lstatSync(filename);
    if (!before.isFile() || !privateOwned(before) || before.nlink !== 1 || before.size !== size || !same(before, named) || named.isSymbolicLink()) throw new Error("Local file verification failed: unsafe or changed");
    const bytes = Buffer.alloc(size + 1);
    let length = 0;
    while (length < bytes.length) {
      const count = fs.readSync(descriptor, bytes, length, bytes.length - length, length);
      if (!count) break;
      length += count;
    }
    const after = fs.fstatSync(descriptor);
    checkRoot();
    if (length !== size || !same(before, after) || after.size !== size || after.nlink !== 1 || before.mtimeMs !== after.mtimeMs || before.ctimeMs !== after.ctimeMs || !same(after, fs.lstatSync(filename))) throw new Error("Local file changed while reading");
    return bytes.subarray(0, size);
  } finally {
    if (descriptor !== undefined) fs.closeSync(descriptor);
    fs.closeSync(directory);
  }
}
