#!/usr/bin/env python3
"""Opt-in lab bridge: one fixed loopback port through one private Unix socket.

Run unix-to-loopback outside an isolated network namespace and loopback-to-unix
inside it. The client namespace has only lo and no external route. This carries
the unchanged encrypted ISCP lab transport; it is not a product fallback.
"""
import argparse
import os
from pathlib import Path
import selectors
import signal
import socket
import stat
import threading

parser = argparse.ArgumentParser()
parser.add_argument("mode", choices=["unix-to-loopback", "loopback-to-unix"])
parser.add_argument("--socket", required=True)
parser.add_argument("--port", required=True, type=int)
args = parser.parse_args()
def terminate(_signal, _frame):
    raise SystemExit(0)


signal.signal(signal.SIGTERM, terminate)
endpoint = Path(args.socket)
assert endpoint.is_absolute() and endpoint.name in ("relay.sock", "issuer.sock")
assert 1024 <= args.port <= 65535
owner = endpoint.parent.lstat()
assert endpoint.parent.is_dir() and not endpoint.parent.is_symlink()
assert owner.st_uid == os.getuid() and owner.st_mode & 0o077 == 0


def forward(incoming):
    outgoing = None
    try:
        if args.mode == "unix-to-loopback":
            outgoing = socket.create_connection(("127.0.0.1", args.port), timeout=10)
        else:
            outgoing = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            outgoing.settimeout(10)
            outgoing.connect(str(endpoint))
        incoming.settimeout(10)
        outgoing.settimeout(10)
        with selectors.DefaultSelector() as ready:
            ready.register(incoming, selectors.EVENT_READ, outgoing)
            ready.register(outgoing, selectors.EVENT_READ, incoming)
            while ready.get_map():
                for key, _ in ready.select(timeout=30):
                    data = key.fileobj.recv(65536)
                    if data:
                        key.data.sendall(data)
                    else:
                        ready.unregister(key.fileobj)
                        key.data.shutdown(socket.SHUT_WR)
    except OSError:
        # Transport loss stays a transport loss; never manufacture a reply or
        # retry bytes on a different connection.
        pass
    finally:
        incoming.close()
        if outgoing is not None:
            outgoing.close()


family = socket.AF_UNIX if args.mode == "unix-to-loopback" else socket.AF_INET
listener = socket.socket(family, socket.SOCK_STREAM)
bound_identity = None
try:
    if args.mode == "unix-to-loopback":
        # A stale endpoint needs explicit cleanup after proving its owner exited.
        assert not endpoint.exists()
        previous = os.umask(0o077)
        try:
            listener.bind(str(endpoint))
        finally:
            os.umask(previous)
        bound = endpoint.lstat()
        bound_identity = (bound.st_dev, bound.st_ino)
        os.chmod(endpoint, 0o600)
    else:
        peer = endpoint.lstat()
        assert stat.S_ISSOCK(peer.st_mode) and peer.st_uid == os.getuid()
        assert peer.st_mode & 0o077 == 0
        listener.bind(("127.0.0.1", args.port))
    listener.listen(16)
    print("offline_loopback_bridge_ready", flush=True)
    while True:
        client, _ = listener.accept()
        threading.Thread(target=forward, args=(client,), daemon=True).start()
finally:
    listener.close()
    if bound_identity is not None:
        try:
            remaining = endpoint.lstat()
        except FileNotFoundError:
            pass
        else:
            if (remaining.st_dev, remaining.st_ino) == bound_identity:
                endpoint.unlink()
