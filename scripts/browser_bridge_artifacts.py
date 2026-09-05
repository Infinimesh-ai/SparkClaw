#!/usr/bin/env python3

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import stat


HEX64 = re.compile(r"[0-9a-f]{64}")
HEX40 = re.compile(r"[0-9a-f]{40}")
VERSION = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+")
EXTENSION_ID = re.compile(r"[a-p]{32}")


def load_manifest(path: Path) -> dict[str, object]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict) or value.get("schemaVersion") != 1:
        raise ValueError("browser bridge artifact manifest schema is invalid")
    if not VERSION.fullmatch(str(value.get("version", ""))):
        raise ValueError("browser bridge version is invalid")
    if not EXTENSION_ID.fullmatch(str(value.get("extensionID", ""))):
        raise ValueError("browser bridge extension ID is invalid")
    if not HEX64.fullmatch(str(value.get("sourceSHA256", ""))):
        raise ValueError("browser bridge source checksum is invalid")
    if not HEX40.fullmatch(str(value.get("upstreamCommit", ""))):
        raise ValueError("browser bridge upstream commit is invalid")
    files = value.get("files")
    if not isinstance(files, dict) or not files:
        raise ValueError("browser bridge file manifest is empty")
    for relative, digest in files.items():
        path_value = Path(str(relative))
        if path_value.is_absolute() or ".." in path_value.parts or str(path_value) != str(relative):
            raise ValueError("browser bridge file path is unsafe")
        if not HEX64.fullmatch(str(digest)):
            raise ValueError("browser bridge file checksum is invalid")
    return value


def verify_tree(root: Path, manifest: dict[str, object], *, require_root_owner: bool) -> None:
    if root.is_symlink() or not root.is_dir():
        raise ValueError("browser bridge root must be a real directory")
    files = manifest["files"]
    assert isinstance(files, dict)
    actual_paths = []
    for path in root.rglob("*"):
        if not (path.is_file() or path.is_symlink()):
            continue
        relative = path.relative_to(root)
        if not require_root_owner and relative.parts[0] == "test":
            continue
        actual_paths.append(str(relative))
    actual_paths.sort()
    expected_paths = sorted(str(path) for path in files)
    if actual_paths != expected_paths:
        raise ValueError("browser bridge source closure does not match the file manifest")
    closure = hashlib.sha256()
    for relative in expected_paths:
        path = root / relative
        metadata = path.lstat()
        if stat.S_ISLNK(metadata.st_mode) or not stat.S_ISREG(metadata.st_mode):
            raise ValueError(f"browser bridge file is unsafe: {relative}")
        if require_root_owner and (metadata.st_uid != 0 or stat.S_IMODE(metadata.st_mode) != 0o644):
            raise ValueError(f"installed browser bridge file ownership or mode is invalid: {relative}")
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        if digest != files[relative]:
            raise ValueError(f"browser bridge file checksum mismatch: {relative}")
        closure.update(f"{digest}  {relative}\n".encode())
    if closure.hexdigest() != manifest["sourceSHA256"]:
        raise ValueError("browser bridge source closure checksum is invalid")
    extension = json.loads((root / "manifest.json").read_text(encoding="utf-8"))
    package = json.loads((root / "package.json").read_text(encoding="utf-8"))
    if extension.get("version") != manifest["version"] or package.get("version") != manifest["version"]:
        raise ValueError("browser bridge package version does not match the artifact manifest")
    if extension.get("name") != "SparkClaw Browser Bridge":
        raise ValueError("browser bridge package identity is invalid")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("manifest", type=Path)
    parser.add_argument("root", type=Path)
    parser.add_argument("--installed", action="store_true")
    parser.add_argument("--print-field", choices=("version", "extensionID", "sourceSHA256"))
    args = parser.parse_args()
    manifest = load_manifest(args.manifest)
    verify_tree(args.root, manifest, require_root_owner=args.installed)
    if args.print_field:
        print(manifest[args.print_field])


if __name__ == "__main__":
    main()
