#!/usr/bin/env python3
"""Explicit build-time setup. Runtime helpers never download or install code."""
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
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "tools/ezmcd-adapter"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--python", default="python3.11")
    parser.add_argument("--archive", type=Path)
    parser.add_argument("--output", type=Path, default=ROOT / ".cache/ezmcd-adapter")
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    lock = json.loads((SOURCE / "upstream.lock.json").read_text())
    patches = []
    for patch in lock.get("patches", []):
        relative = Path(patch["path"])
        if relative.is_absolute() or ".." in relative.parts:
            raise RuntimeError("unsafe upstream patch path")
        source = SOURCE / relative
        if hashlib.sha256(source.read_bytes()).hexdigest() != patch["sha256"]:
            raise RuntimeError("upstream patch integrity check failed")
        patches.append(source)
    destination = args.output.absolute()
    if args.check:
        manifest = json.loads((destination / "manifest.json").read_text())
        if manifest["upstream"] != lock:
            raise RuntimeError("helper build is stale")
        for name, expected in manifest["files"].items():
            if hashlib.sha256((destination / name).read_bytes()).hexdigest() != expected:
                raise RuntimeError("helper integrity check failed")
        for source in (SOURCE / "src").rglob("*"):
            if source.is_file() and "__pycache__" not in source.parts and not source.name.endswith(".pyc"):
                built = destination / "adapter" / source.relative_to(SOURCE / "src")
                if not built.is_file() or source.read_bytes() != built.read_bytes():
                    raise RuntimeError("adapter source changed; build a new output directory")
        for name in ("requirements.txt", "NOTICE"):
            if (SOURCE / name).read_bytes() != (destination / name).read_bytes():
                raise RuntimeError("helper dependency/attribution manifest is stale")
        subprocess.run([str(destination / "venv/bin/python"), "-c", "import pydantic,blake3,ezmcd; import sparkclaw_mcd.helper"], check=True, timeout=20)
        verify = "import importlib.metadata,pathlib,platform; assert platform.python_version()==" + repr(lock["python"]) + "; "
        verify += "lines=pathlib.Path('requirements.txt').read_text().splitlines(); "
        verify += "assert all(importlib.metadata.version(s.split('==')[0])==s.split('==')[1] for s in lines if s and not s.startswith('#'))"
        subprocess.run([str(destination / "venv/bin/python"), "-c", verify], cwd=destination, check=True, timeout=20)
        print("EzMcDonalds helper integrity/dependencies ready; product qualification is independent")
        return
    python = shutil.which(args.python)
    if not python:
        raise RuntimeError("Python 3.11.15 required; select a supported interpreter with --python")
    version = subprocess.check_output([python, "-c", "import platform; print(platform.python_version())"], text=True, timeout=10).strip()
    if version != lock["python"]:
        raise RuntimeError("Python version does not match upstream.lock.json")
    destination.parent.mkdir(parents=True, exist_ok=True)
    if destination.exists():
        raise RuntimeError("build destination exists; use --check or a new --output")
    with tempfile.TemporaryDirectory(prefix="sparkclaw-ezmcd-build-") as temporary:
        archive = Path(temporary) / "upstream.tar.gz"
        if args.archive:
            shutil.copyfile(args.archive, archive)
        else:
            with urllib.request.urlopen(lock["archive"], timeout=60) as response:
                data = response.read(16 * 1024 * 1024 + 1)
            if len(data) > 16 * 1024 * 1024:
                raise RuntimeError("upstream archive exceeds limit")
            archive.write_bytes(data)
        if hashlib.sha256(archive.read_bytes()).hexdigest() != lock["archive_sha256"]:
            raise RuntimeError("pinned upstream archive checksum mismatch")
        destination.mkdir(mode=0o700)
        with tarfile.open(archive) as bundle:
            for entry in bundle.getmembers():
                parts = Path(entry.name).parts[1:]
                if not parts or (parts[0] not in ("src", "tests") and parts != ("LICENSE",)):
                    continue
                if not entry.isfile() or any(p in ("..", ".") for p in parts) or entry.size > 2 * 1024 * 1024:
                    if entry.isdir():
                        continue
                    raise RuntimeError("unsafe upstream archive entry")
                target = destination / "upstream" / Path(*parts)
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(bundle.extractfile(entry).read())
        for patch in patches:
            # Build-time only; the patch is hash pinned and contains public
            # source/fixtures, never account material or a runtime sampler.
            # Run outside the enclosing SparkClaw checkout: git apply inside
            # its ignored build directory silently skips every relative path.
            command = ["git", "apply", "--unsafe-paths", "--directory=" + str(destination / "upstream")]
            subprocess.run([*command, "--check", str(patch)], cwd=temporary, check=True, timeout=30)
            subprocess.run([*command, str(patch)], cwd=temporary, check=True, timeout=30)
        for name, expected in lock.get("patched_source_sha256", {}).items():
            source = destination / "upstream" / name
            if hashlib.sha256(source.read_bytes()).hexdigest() != expected:
                raise RuntimeError("patched upstream source differs from the handoff")
        shutil.copytree(SOURCE / "src", destination / "adapter", ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
        shutil.copyfile(SOURCE / "NOTICE", destination / "NOTICE")
        shutil.copyfile(SOURCE / "requirements.txt", destination / "requirements.txt")
        subprocess.run([python, "-m", "venv", str(destination / "venv")], check=True, timeout=60)
        executable = destination / "venv/bin/python"
        subprocess.run([str(executable), "-m", "pip", "install", "--disable-pip-version-check", "--no-cache-dir", "--only-binary=:all:",
                        "-r", str(destination / "requirements.txt")], check=True, timeout=180)
        site = subprocess.check_output([str(executable), "-c", "import site; print(site.getsitepackages()[0])"], text=True, timeout=10).strip()
        # Relative .pth entries keep module resolution independent of cwd/PYTHONPATH.
        paths = [os.path.relpath(destination / p, site) for p in ("upstream/src", "adapter")]
        (Path(site) / "sparkclaw-mcd.pth").write_text("\n".join(paths) + "\n")
        files = {str(p.relative_to(destination)): hashlib.sha256(p.read_bytes()).hexdigest()
                 for folder in ("upstream", "adapter") for p in sorted((destination / folder).rglob("*"))
                 if p.is_file() and "__pycache__" not in p.parts}
        for name in ("requirements.txt", "NOTICE"):
            files[name] = hashlib.sha256((destination / name).read_bytes()).hexdigest()
        (destination / "manifest.json").write_text(json.dumps({"upstream": lock, "python": version, "files": files}, indent=2) + "\n")
    print("Built private helpers. This development venv is not a self-contained release runtime.")


if __name__ == "__main__":
    main()
