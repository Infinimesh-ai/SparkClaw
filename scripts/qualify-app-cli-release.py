#!/usr/bin/env python3
"""Qualify installed paired releases and whole-set rollback in disposable state.

No browser or mailbox is contacted. The second release changes only a NOTICE
fixture, with matching runtime, wheel and consumer hashes, to exercise rollback.
"""
import base64
import csv
import fcntl
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def sha(data):
    return hashlib.sha256(data).hexdigest()


def encoded(value):
    return (json.dumps(value, indent=2) + "\n").encode()


def fixture_release(source, target):
    shutil.copytree(source, target)
    release = json.loads((target / "release.json").read_bytes())
    runtime = target / release["artifacts"]["runtime"]["file"]
    with tarfile.open(runtime) as archive:
        entries = {member.name: (member, archive.extractfile(member).read())
                   for member in archive if member.isfile()}
    notice = entries["package/NOTICE"][1] + b"\nDisposable paired-release rollback fixture.\n"
    manifest = json.loads(entries["package/release.json"][1])
    manifest["files"]["NOTICE"] = sha(notice)
    for name, data in {"package/NOTICE": notice, "package/release.json": encoded(manifest)}.items():
        member = entries[name][0]
        member.size = len(data)
        entries[name] = member, data
    with tarfile.open(runtime, "w:gz") as archive:
        for member, data in entries.values():
            archive.addfile(member, io.BytesIO(data))
    release["runtime_digest"] = sha(encoded(manifest))
    wheel = target / release["artifacts"]["wheel"]["file"]
    with zipfile.ZipFile(wheel) as archive:
        files = {name: archive.read(name) for name in archive.namelist()}
    files["app_cli/release.json"] = encoded({"id": release["id"], "runtime_digest": release["runtime_digest"]})
    record = next(name for name in files if name.endswith(".dist-info/RECORD"))
    rows = io.StringIO()
    writer = csv.writer(rows, lineterminator="\n")
    for name, data in files.items():
        if name != record:
            digest = base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()
            writer.writerow([name, "sha256=" + digest, len(data)])
    writer.writerow([record, "", ""])
    files[record] = rows.getvalue().encode()
    with zipfile.ZipFile(wheel, "w", zipfile.ZIP_DEFLATED) as archive:
        for name, data in files.items():
            archive.writestr(name, data)
    for artifact in release["artifacts"].values():
        artifact["sha256"] = sha((target / artifact["file"]).read_bytes())
    (target / "release.json").write_bytes(encoded(release))


def main():
    with tempfile.TemporaryDirectory(prefix="app-cli-paired-") as temp:
        base = Path(temp)
        consumer = base / "consumer"
        (consumer / "scripts").mkdir(parents=True)
        (consumer / "configs").mkdir()
        installer = consumer / "scripts/install-app-cli.py"
        shutil.copyfile(ROOT / "scripts/install-app-cli.py", installer)
        first = ROOT / "vendor/app-cli"
        second = base / "second-bundle"
        fixture_release(first, second)
        root, host = base / "installation", base / "host.sock"
        args = [sys.executable, str(installer), "--root", str(root), "--host-socket", str(host),
                "--host-runtime-root", str(base / "cli-runtime"), "--workspace-root", str(base / "workspace")]

        def install(bundle, *extra, success=True):
            result = subprocess.run([*args, "--bundle", str(bundle), *extra], capture_output=True, text=True)
            if success:
                if result.returncode:
                    raise AssertionError(result.stderr[-3000:])
                return json.loads(result.stdout)
            assert result.returncode != 0, "Invalid release unexpectedly accepted"
            return result.stderr

        def select(bundle):
            shutil.copyfile(bundle / "release.json", consumer / "configs/app-cli-release.json")

        def node(config, program):
            value = json.loads(Path(config).read_bytes())
            script = "import {Ledger} from " + json.dumps(Path(value["runtime_directory"]).joinpath("src/ledger.mjs").as_uri()) + ";\n" + program
            return subprocess.check_output([value["node"], "--input-type=module", "-e", script, value["state_directory"]], text=True).strip()

        select(first)
        a = install(first, "--activate")
        install(first, "--check")
        assert "configuration differs" in install(first, "--check", "--profile", "different-fixture", success=False)
        for app in ["calculator", "calculator-cli", "calculator-runtime"]:
            value = json.loads(subprocess.check_output([a["python"], "-m", "app_cli", app, "multiply", "--a", "6", "--b", "7"], cwd=base))
            assert value["data"]["value"] == 42
        epoch_a = int(node(a["config"], "const l=new Ledger(process.argv[1]); l.db.exec(\"INSERT INTO metadata VALUES('qualification_sentinel',42)\"); console.log(l.epoch); l.close();"))
        # A listener blocks activation, even if no Executor holds the lock.
        listener = socket.socket(socket.AF_UNIX)
        listener.bind(str(host))
        listener.listen()
        try:
            assert "Stop and drain" in install(first, "--activate", success=False)
        finally:
            listener.close()
            host.unlink()
        with (root / "state/executor/executor.lock").open("a+") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            install(first, "--activate", success=False)
        # A consumer projection from A cannot load the B bundle.
        install(second, success=False)
        select(second)
        b = install(second, "--activate")
        assert a["config"] != b["config"]
        assert json.loads((root / "previous.json").read_bytes())["config"] == a["config"]
        assert Path(json.loads(Path(b["config"]).read_bytes())["state_directory"]) == root / "state/executor"
        epoch_b = int(node(b["config"], "const l=new Ledger(process.argv[1]); console.log(l.epoch); l.close();"))
        assert epoch_b > epoch_a
        # Python A paired with runtime B is rejected before any operation.
        mixed = subprocess.run([a["python"], "-c", "from app_cli.release import verify_release;import json,sys;verify_release(json.load(open(sys.argv[1])))", b["config"]], capture_output=True)
        assert mixed.returncode != 0
        # Restore the matching consumer+wheel+runtime set, preserving state.
        select(first)
        restored = install(first, "--activate")
        assert restored == a
        evidence = json.loads(node(a["config"], "const l=new Ledger(process.argv[1]); console.log(JSON.stringify({epoch:l.epoch,sentinel:l.db.prepare(\"SELECT value FROM metadata WHERE key='qualification_sentinel'\").get().value})); l.close();"))
        assert evidence["epoch"] > epoch_b and evidence["sentinel"] == 42
        config = json.loads(Path(a["config"]).read_bytes())
        notice = Path(config["runtime_directory"]) / "NOTICE"
        original = notice.read_bytes()
        notice.write_bytes(original + b"corruption")
        install(first, "--check", success=False)
        notice.write_bytes(original)
        install(first, "--check")
        print(json.dumps({"fresh_install": True, "configuration_drift_rejected": True, "native_cli_runtime": True, "installed_file_tamper_rejected": True,
            "mixed_python_runtime_rejected": True, "mixed_consumer_rejected": True,
            "live_host_activation_rejected": True, "live_executor_activation_rejected": True,
            "whole_set_rollback": True, "durable_state_preserved": True, "epoch_monotonic": True}))


if __name__ == "__main__":
    main()
