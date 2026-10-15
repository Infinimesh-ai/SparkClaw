#!/usr/bin/env python3
"""Stage and verify a complete application release; never change live authority.

Activation/rollback requires stopped Controller and Executor services. The same
durable state directory is retained for every release, including rollback.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def write(file, value):
    file.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    temporary = file.with_suffix(".tmp")
    with temporary.open("w") as stream:
        json.dump(value, stream, indent=2)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    temporary.chmod(0o600)
    temporary.replace(file)
    fd = os.open(file.parent, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def run(*args, **kw):
    kw.setdefault("stdout", sys.stderr)
    subprocess.run(args, check=True, **kw)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--bundle", type=Path, default=ROOT / "vendor/app-cli")
    parser.add_argument("--host-socket", type=Path, required=True)
    parser.add_argument("--host-runtime-root", type=Path, required=True)
    parser.add_argument("--workspace-root", type=Path, required=True)
    parser.add_argument("--node", default=shutil.which("node"))
    parser.add_argument("--profile", default="default")
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--activate", action="store_true")
    args = parser.parse_args()
    os.umask(0o077)
    root, bundle = args.root.resolve(), args.bundle.resolve()
    release = json.loads((bundle / "release.json").read_bytes())
    if release["schema_version"] != 1 or release["ledger_version"] != 1:
        raise ValueError("Unsupported release/state version")
    for artifact in release["artifacts"].values():
        file = bundle / artifact["file"]
        if file.parent != bundle or hashlib.sha256(file.read_bytes()).hexdigest() != artifact["sha256"]:
            raise ValueError("Artifact digest mismatch")
    # Every consumer projection (including Desktop assets) must belong to this
    # release. Restore the matching SparkClaw checkout before whole-set rollback.
    if json.loads((ROOT / "configs/app-cli-release.json").read_bytes()) != release:
        raise ValueError("SparkClaw projections belong to another release")
    destination = root / "releases" / release["runtime_digest"]
    config = destination / "config.json"
    python = destination / "python/bin/python"
    if not args.check and not config.exists():
        destination.mkdir(parents=True, exist_ok=False, mode=0o700)
        try:
            runtime = destination / "runtime"
            with tarfile.open(bundle / release["artifacts"]["runtime"]["file"]) as archive:
                for member in archive.getmembers():
                    if not member.name.startswith("package/") or member.issym() or member.islnk():
                        raise ValueError("Unsafe runtime archive")
                archive.extractall(destination, filter="data")
            (destination / "package").rename(runtime)
            run("npm", "ci", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund", cwd=runtime)
            run(sys.executable, "-m", "venv", str(destination / "python"))
            run(str(python), "-m", "pip", "install", "--disable-pip-version-check",
                "-r", str(bundle / release["artifacts"]["python_dependencies"]["file"]),
                str(bundle / release["artifacts"]["wheel"]["file"]))
            state = root / "state"
            for directory in [state, state / "authorization", state / "executor", state / "host"]:
                directory.mkdir(parents=True, exist_ok=True, mode=0o700)
                info = directory.lstat()
                if directory.is_symlink() or info.st_uid != os.getuid() or info.st_mode & 0o077:
                    raise ValueError("State directory must be owner-private")
            key = state / "issuer.key"
            if not key.exists():
                with key.open("xb") as stream:
                    stream.write(os.urandom(32))
                    stream.flush()
                    os.fsync(stream.fileno())
            # Canonical digest is an App-CLI protocol, not a second implementation.
            program = "from app_cli.lifecycle_protocol import digest;import json,sys;print(digest(json.load(open(sys.argv[1]))))"
            bindings = [{"path": str(file), "digest": subprocess.check_output(
                [str(python), "-c", program, str(file)], text=True).strip()}
                for file in sorted((runtime / "bindings").glob("mail-*.json"))]
            write(config, {"release_id": release["id"], "release_digest": release["runtime_digest"],
                "runtime_directory": str(runtime), "assembly_module": str(runtime / "src/assembly.mjs"),
                "node": str(Path(args.node).absolute()), "bindings": bindings,
                "owner_id": f"owner-{os.getuid()}", "profile_id": args.profile,
                "state_directory": str(state / "executor"), "socket": str(state / "executor/executor.sock"),
                "host_state_directory": str(state / "host"), "issuer_key_file": str(key),
                "grants_directory": str(state / "authorization"), "browser_host_socket": str(args.host_socket.absolute()),
                "host_runtime_root": str(args.host_runtime_root.absolute()), "workspace_root": str(args.workspace_root.resolve())})
        except BaseException:
            shutil.rmtree(destination)  # Unactivated staged files only; never state.
            raise
    configured = json.loads(config.read_bytes())
    expected = {"browser_host_socket": str(args.host_socket.absolute()),
                "host_runtime_root": str(args.host_runtime_root.absolute()),
                "workspace_root": str(args.workspace_root.resolve()), "profile_id": args.profile}
    if any(configured.get(key) != value for key, value in expected.items()):
        raise ValueError("Existing release configuration differs from the requested host/workspace/profile; drain services before explicitly reconfiguring it")
    run(str(python), "-c", "from app_cli.release import verify_release;import json,sys;verify_release(json.load(open(sys.argv[1])))", str(config))
    if args.activate:
        # flock proves that the resident service is stopped. Host resources also
        # need to drain; the setup command stops the host before reaching here.
        import fcntl
        import socket
        import errno
        probe = socket.socket(socket.AF_UNIX)
        probe.settimeout(1)
        try:
            probe.connect(str(args.host_socket))
        except OSError as error:
            if error.errno not in (errno.ENOENT, errno.ECONNREFUSED):
                raise
        else:
            raise ValueError("Stop and drain the Browser Controller before activation")
        finally:
            probe.close()
        with (root / "state/executor/executor.lock").open("a+") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            previous = root / "current.json"
            if previous.exists():
                write(root / "previous.json", json.loads(previous.read_bytes()))
            write(previous, {"config": str(config), "python": str(python), "release_id": release["id"]})
    print(json.dumps({"config": str(config), "python": str(python), "release_id": release["id"]}))


if __name__ == "__main__":
    main()
