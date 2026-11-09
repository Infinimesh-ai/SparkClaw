"""Audit a clean Linux cross-build without installing or executing its payload.

Compare platform-neutral files with an already qualified Linux build. Native
qualification and AppImage execution are deliberately separate requirements.
"""

import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import struct
import subprocess
import tarfile


def record(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return {"path": str(path), "bytes": path.stat().st_size, "sha256": digest.hexdigest()}


def elf(path, machine):
    with path.open("rb") as source:
        header = source.read(64)
    assert header[:6] == b"\x7fELF\x02\x01", path
    actual = struct.unpack_from("<H", header, 18)[0]
    assert actual == machine, (path, actual, machine)
    return {"path": str(path), "class": 64, "endian": "little", "machine": actual}


def ar_members(path):
    with path.open("rb") as source:
        assert source.read(8) == b"!<arch>\n"
        result = {}
        while header := source.read(60):
            assert len(header) == 60 and header[58:60] == b"`\n"
            name = header[:16].decode().strip().rstrip("/")
            size = int(header[48:58])
            assert name not in result
            result[name] = source.read(size)
            assert len(result[name]) == size
            if size % 2:
                assert source.read(1) == b"\n"
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--assembly", type=Path, required=True)
    parser.add_argument("--reference", type=Path, required=True)
    parser.add_argument("--arch", choices=["arm64", "x64"], required=True)
    parser.add_argument("--source", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    root, reference = args.assembly.resolve(), args.reference.resolve()
    assert not args.output.exists(), "Evidence must not overwrite an earlier result"
    assert subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True) == ""
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
    assert revision == args.source
    dist = root / "apps/desktop/dist"
    arm = args.arch == "arm64"
    unpacked = dist / ("linux-arm64-unpacked" if arm else "linux-unpacked")
    manifest_path = dist / ("release-manifest.json" if arm else "release-manifest-linux-x64.json")
    manifest = json.loads(manifest_path.read_text())
    assert manifest["platform"] == "linux" and manifest["architecture"] == args.arch
    assert len(manifest["artifacts"]) == 2
    artifacts = []
    for artifact in manifest["artifacts"]:
        assert Path(artifact["name"]).name == artifact["name"]
        actual = record(dist / artifact["name"])
        assert all(actual[key] == artifact[key] for key in ["bytes", "sha256"])
        artifacts.append(actual)
    image = next(Path(item["path"]) for item in artifacts if item["path"].endswith(".AppImage"))
    deb = next(Path(item["path"]) for item in artifacts if item["path"].endswith(".deb"))
    architecture = [elf(path, 183 if arm else 62) for path in [
        unpacked / "sparkx", unpacked / "resources/iscp-workbench", image,
    ]]
    resources, previous = unpacked / "resources", reference / "resources"
    assert (resources / "app.asar").read_bytes() == (previous / "app.asar").read_bytes()
    controller = json.loads((resources / "browser-controller/manifest.json").read_text())
    assert controller == json.loads((previous / "browser-controller/manifest.json").read_text())
    assert controller["source_revision"] == revision
    for name in controller["files"]:
        relative = Path(name)
        assert not relative.is_absolute() and ".." not in relative.parts
        assert (resources / "browser-controller" / name).read_bytes() == (
            previous / "browser-controller" / name
        ).read_bytes(), name
    ui = []
    for directory in ["webchat", "local-preview"]:
        current, prior = resources / directory, previous / directory
        names = sorted(str(path.relative_to(current)) for path in current.rglob("*") if path.is_file())
        assert names == sorted(str(path.relative_to(prior)) for path in prior.rglob("*") if path.is_file())
        for name in names:
            assert (current / name).read_bytes() == (prior / name).read_bytes(), name
        ui.append({"directory": directory, "files": len(names), "bytes_equal": True})

    # Inspect only; never extract paths or execute Debian maintainer scripts.
    members = ar_members(deb)
    assert members["debian-binary"] == b"2.0\n"
    with tarfile.open(fileobj=io.BytesIO(members[next(n for n in members if n.startswith("control.tar"))]), mode="r:*") as archive:
        control = next(member for member in archive if member.name.removeprefix("./") == "control")
        text = archive.extractfile(control).read().decode()
    fields = dict(line.split(": ", 1) for line in text.splitlines() if ": " in line and not line.startswith(" "))
    assert fields["Architecture"] == ("arm64" if arm else "amd64") and fields["Package"] == "sparkclaw"
    for dependency in ["bubblewrap", "procps", "libgbm1", "libsecret-1-0"]:
        assert dependency in fields["Depends"]
    checked = []
    with tarfile.open(fileobj=io.BytesIO(members[next(n for n in members if n.startswith("data.tar"))]), mode="r:*") as archive:
        for member in archive:
            name = member.name.removeprefix("./")
            prefix = "opt/SparkX/"
            if not name.startswith(prefix) or name == prefix:
                continue
            relative = name[len(prefix):]
            assert ".." not in Path(relative).parts
            path = unpacked / relative
            if member.isdir():
                assert path.is_dir(), relative
            elif member.issym():
                assert path.is_symlink() and os.readlink(path) == member.linkname, relative
                checked.append(relative)
            elif member.isfile():
                digest = hashlib.sha256()
                with archive.extractfile(member) as source:
                    for chunk in iter(lambda: source.read(1024 * 1024), b""):
                        digest.update(chunk)
                assert path.is_file() and not path.is_symlink() and digest.hexdigest() == record(path)["sha256"], relative
                assert member.mode & 0o111 == path.stat().st_mode & 0o111, relative
                checked.append(relative)
            else:
                raise AssertionError(("unexpected archive entry type", relative))
    expected = sorted(str(path.relative_to(unpacked)) for path in unpacked.rglob("*") if path.is_file() or path.is_symlink())
    assert sorted(checked) == expected, (len(checked), len(expected))
    result = {
        "passed": True, "scope": "clean_cross_build_and_static_payload_audit_not_native_or_package_manager_install",
        "source_commit": revision, "source_clean": True, "target": "linux-" + args.arch,
        "verifier": record(Path(__file__).resolve()), "artifacts": artifacts, "ELF": architecture,
        "release_manifest": record(manifest_path),
        "runtime_versions": manifest["runtime"],
        "versions_observation": "Build-host Electron supplied these versions; target runtime was not executed",
        "asar": record(resources / "app.asar"), "asar_equal_reference": True,
        "controller_manifest": record(resources / "browser-controller/manifest.json"),
        "controller_files_equal": len(controller["files"]), "UI_equal": ui,
        "helper": record(resources / "iscp-workbench"),
        "deb_architecture": fields["Architecture"], "deb_application_entries_equal": len(checked),
        "package_audit": record(resources / "client-package-audit.json"),
        "AppImage_payload_execution": False, "native_qualification": False,
        "remote_writes": False, "production_installation": False,
    }
    with args.output.open("x") as output:
        os.chmod(args.output, 0o600)
        output.write(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"passed": True, "target": result["target"], "deb_entries": len(checked), "evidence": str(args.output)}))


if __name__ == "__main__":
    main()
