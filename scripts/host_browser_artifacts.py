#!/usr/bin/env python3
"""Validate and resolve the pinned host Chromium artifact manifest."""

from __future__ import annotations

import argparse
from pathlib import Path
import json
import re
from typing import Any
from urllib.parse import urlsplit


SUPPORTED_ARCHITECTURES = {"arm64", "x86_64"}


def load_artifact(path: Path, architecture: str) -> dict[str, str]:
    if architecture not in SUPPORTED_ARCHITECTURES:
        raise ValueError(f"unsupported host browser architecture: {architecture}")
    with path.open(encoding="utf-8") as stream:
        manifest: dict[str, Any] = json.load(stream)
    if set(manifest) != {"version", "artifacts"}:
        raise ValueError("host browser manifest fields are invalid")
    version = manifest.get("version")
    artifacts = manifest.get("artifacts")
    if not isinstance(version, str) or not version.strip():
        raise ValueError("host browser manifest version is invalid")
    if not isinstance(artifacts, dict) or set(artifacts) != SUPPORTED_ARCHITECTURES:
        raise ValueError("host browser manifest architecture set is invalid")
    artifact = artifacts.get(architecture)
    if not isinstance(artifact, dict):
        raise ValueError(f"host browser artifact is missing for {architecture}")
    allowed = {"url", "sha256", "archiveRoot", "playwrightBuild"}
    if set(artifact) - allowed or not {"url", "sha256", "archiveRoot"} <= set(
        artifact
    ):
        raise ValueError(f"host browser artifact fields are invalid for {architecture}")
    url = artifact.get("url")
    sha256 = artifact.get("sha256")
    archive_root = artifact.get("archiveRoot")
    playwright_build = artifact.get("playwrightBuild", "")
    parsed = urlsplit(url) if isinstance(url, str) else None
    if (
        parsed is None
        or parsed.scheme != "https"
        or not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
    ):
        raise ValueError(f"host browser artifact URL is invalid for {architecture}")
    if not isinstance(sha256, str) or not re.fullmatch(r"[0-9a-f]{64}", sha256):
        raise ValueError(f"host browser artifact checksum is invalid for {architecture}")
    if (
        not isinstance(archive_root, str)
        or not re.fullmatch(r"[A-Za-z0-9._-]+", archive_root)
        or archive_root in {".", ".."}
    ):
        raise ValueError(f"host browser archive root is invalid for {architecture}")
    if not isinstance(playwright_build, str):
        raise ValueError(f"host browser Playwright build is invalid for {architecture}")
    return {
        "version": version,
        "url": url,
        "sha256": sha256,
        "archiveRoot": archive_root,
        "playwrightBuild": playwright_build,
    }


def main() -> int:
    parser = argparse.ArgumentParser(prog="host-browser-artifacts")
    parser.add_argument("manifest", type=Path)
    parser.add_argument("architecture")
    args = parser.parse_args()
    artifact = load_artifact(args.manifest, args.architecture)
    for key in ("version", "url", "sha256", "archiveRoot", "playwrightBuild"):
        print(artifact[key])
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
