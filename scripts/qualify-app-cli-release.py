#!/usr/bin/env python3
"""Qualify paired releases in disposable state or an installed mail deployment.

The default uses a NOTICE fixture to exercise compatible whole-set rollback.
The explicit installed mode reads native managed-script import evidence without
opening pages, sending mail, changing a profile or activating a release.
"""
import argparse
import base64
import contextlib
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


def qualify_installed(root, profile):
    root = root.resolve()
    current = root / "current.json"
    original = current.read_bytes()
    pointer = json.loads(original)
    config_path = Path(pointer["config"])
    config_bytes = config_path.read_bytes()
    config = json.loads(config_bytes)
    command = [sys.executable, str(ROOT / "scripts/install-app-cli.py"), "--check",
               "--root", str(root), "--host-socket", config["browser_host_socket"],
               "--host-runtime-root", config["host_runtime_root"],
               "--workspace-root", config["workspace_root"], "--profile", config["profile_id"],
               "--node", config["node"]]
    anchor = config.get("anchor")
    if anchor:
        endpoint = "socket" if "socket" in anchor else "url"
        command.extend(["--anchor-" + endpoint, anchor[endpoint],
                        "--anchor-token-file", anchor["token_file"]])
    if config.get("storage", {}).get("budget_bytes") is not None:
        command.extend(["--storage-budget-bytes", str(config["storage"]["budget_bytes"])])
    checked = json.loads(subprocess.check_output(command, text=True,
                        env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"}))
    if checked != pointer:
        raise ValueError("Active App-CLI release differs from the checked installed release")
    # Installation alone does not update Linux's separate managed Reader policy
    # or its native import. Same-version script changes must match source bytes.
    sys.dont_write_bytecode = True
    import browser_components
    with contextlib.redirect_stdout(sys.stderr):
        browser_components.verify_system()
        browser_components.verify_profile(profile.resolve())
    if current.read_bytes() != original or config_path.read_bytes() != config_bytes:
        raise ValueError("Active App-CLI release changed during mail qualification")
    return {"release_id": pointer["release_id"], "active_application_release": True,
            "installed_browser_components": True, "native_managed_reader_import": True,
            "browser_or_mail_actions": False}


def qualify_disposable():
    with tempfile.TemporaryDirectory(prefix="app-cli-paired-") as temp:
        base = Path(temp).resolve()
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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--installed-root", type=Path,
                        help="read-only final gate for this installed application release")
    parser.add_argument("--browser-profile", type=Path,
                        help="existing dedicated user-data directory; required with --installed-root")
    args = parser.parse_args()
    if bool(args.installed_root) != bool(args.browser_profile):
        parser.error("--installed-root and --browser-profile must be supplied together")
    if args.installed_root:
        print(json.dumps(qualify_installed(args.installed_root, args.browser_profile)))
    else:
        qualify_disposable()


if __name__ == "__main__":
    main()
