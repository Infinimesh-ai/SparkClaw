import fs from "node:fs/promises";

// Binds a Unix domain socket that is owner-only from the moment it exists.
//
// The kernel creates the socket node with mode 0777 & ~umask, so listening
// under the process umask (typically 022) leaves a window in which any local
// user can connect before a later chmod tightens it. Node binds synchronously
// inside listen(), so narrowing the umask around that call is enough to make
// the node 0700 at creation; the chmod then settles it to the documented 0600
// before this promise resolves and before the caller announces readiness.
export async function listenOwnerOnlyUnixSocket(server, socketPath) {
  await new Promise((resolve, reject) => {
    const onError = (error) => reject(error);
    server.once("error", onError);
    const previousUmask = process.umask(0o077);
    try {
      server.listen(socketPath, () => {
        server.off("error", onError);
        resolve();
      });
    } finally {
      process.umask(previousUmask);
    }
  });
  await fs.chmod(socketPath, 0o600);
}
