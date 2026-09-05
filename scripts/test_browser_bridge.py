#!/usr/bin/env python3

from __future__ import annotations

import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))

from browser_bridge_artifacts import load_manifest, verify_tree


ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / "configs" / "browser-bridge-artifacts.json"
SOURCE = ROOT / "tools" / "browser-bridge"
LAUNCHER = ROOT / "scripts" / "sparkclaw-browser-launcher.sh"
CONTROLLER_SETUP = ROOT / "scripts" / "setup-browser-controller.sh"
GATEWAY_DOCKERFILE = ROOT / "docker" / "images" / "gateway.Dockerfile"


class BrowserBridgeArtifactTest(unittest.TestCase):
    def test_repository_package_matches_the_pinned_source_closure(self) -> None:
        manifest = load_manifest(MANIFEST)
        verify_tree(SOURCE, manifest, require_root_owner=False)
        self.assertEqual(manifest["extensionID"], "mmlmfjhmonkocbjadbfplnigmagldckm")
        self.assertEqual(
            manifest["upstreamCommit"],
            "260eae31113073927b93c5c7591b5ae039952dd0",
        )
        extension = json.loads((SOURCE / "manifest.json").read_text(encoding="utf-8"))
        expected_worker = f'src/bootstrap-{manifest["version"]}.mjs'
        self.assertEqual(extension["background"]["service_worker"], expected_worker)
        bootstrap = (SOURCE / expected_worker).read_text(encoding="utf-8")
        self.assertIn(f'const BOOTSTRAP_VERSION = "{manifest["version"]}";', bootstrap)

    def test_verifier_rejects_changed_and_extra_files(self) -> None:
        manifest = load_manifest(MANIFEST)
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / "bridge"
            shutil.copytree(SOURCE, target, ignore=shutil.ignore_patterns("test"))
            verify_tree(target, manifest, require_root_owner=False)
            (target / "ui.css").write_text("changed", encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                verify_tree(target, manifest, require_root_owner=False)
            shutil.copyfile(SOURCE / "ui.css", target / "ui.css")
            (target / "extra.mjs").write_text("", encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "source closure"):
                verify_tree(target, manifest, require_root_owner=False)

    def test_manifest_closure_uses_sorted_relative_paths(self) -> None:
        manifest = load_manifest(MANIFEST)
        closure = hashlib.sha256()
        for relative, digest in sorted(manifest["files"].items()):
            closure.update(f"{digest}  {relative}\n".encode())
        self.assertEqual(closure.hexdigest(), manifest["sourceSHA256"])

    def test_install_script_has_no_token_or_browser_automation_flags(self) -> None:
        script = (ROOT / "scripts" / "install-browser-bridge.sh").read_text(encoding="utf-8")
        for forbidden in (
            "PLAYWRIGHT_MCP_EXTENSION_TOKEN",
            "--remote-debugging",
            "--enable-automation",
            "--headless",
        ):
            self.assertNotIn(forbidden, script)
        result = subprocess.run(
            ["bash", "-n", str(ROOT / "scripts" / "install-browser-bridge.sh")],
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_persistent_browser_uses_only_profile_and_bridge_startup_flags(self) -> None:
        script = LAUNCHER.read_text(encoding="utf-8")
        self.assertIn('--user-data-dir="$profile_dir"', script)
        self.assertIn('--disable-extensions-except="$bridge_dir"', script)
        self.assertIn('--load-extension="$bridge_dir"', script)
        for forbidden in ("--remote-debugging-", "--enable-automation", "--headless"):
            self.assertNotIn(forbidden, script)

    def test_browser_manifest_validation_normalizes_only_version_whitespace(self) -> None:
        script = (ROOT / "scripts" / "install-browser.sh").read_text(encoding="utf-8")
        self.assertIn('value["executableVersion"].strip() != executable_version', script)
        self.assertIn('"archiveSHA256": archive_sha256', script)
        self.assertIn('"architecture": architecture', script)
        self.assertIn('command_line = " ".join(parts)', script)
        self.assertIn("contains_argument(value)", script)

    def test_controller_checks_loaded_bridge_before_health(self) -> None:
        script = CONTROLLER_SETUP.read_text(encoding="utf-8")
        bridge_check = script.index('  "$bridge_launcher" --check ||')
        health_check = script.index('  health="$(curl')
        self.assertLess(bridge_check, health_check)
        self.assertIn("systemctl --user restart sparkclaw-browser-controller.service", script)
        self.assertIn("systemctl --user restart sparkclaw-browser.service", script)
        self.assertIn("PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1", script)

    def test_gateway_image_contains_only_the_controller_smoke_client(self) -> None:
        dockerfile = GATEWAY_DOCKERFILE.read_text(encoding="utf-8")
        self.assertIn("browser_controller_smoke.mjs", dockerfile)
        for forbidden in ("agent-browser", "playwright install", "chromium"):
            self.assertNotIn(forbidden, dockerfile.lower())


if __name__ == "__main__":
    unittest.main()
