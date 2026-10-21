"""Fail closed when a paired runtime and same-version managed Readers diverge."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import browser_components as components

SPEC = importlib.util.spec_from_file_location("qualify_app_cli_release", Path(__file__).with_name("qualify-app-cli-release.py"))
qualifier = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(qualifier)


def digest(value):
    return hashlib.sha256(value).hexdigest()


class InstalledMailQualificationTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.repo = self.base / "consumer"
        self.root = self.base / "installation"
        self.profile = self.base / "profile"
        self.extension = self.base / "tampermonkey"
        self.system = self.base / "components"
        for path in [self.repo / "configs", self.repo / "tools/browser-userscripts", self.root,
                     self.profile / "Default", self.extension, self.system]:
            path.mkdir(parents=True)
        self.uuid = "5b4323dc-326f-4fbb-a1a0-4ac8c00631c0"
        self.old_source = "// @name Mail Reader\n// @version 0.2.0\noldReader();\n"
        self.source = "// @name Mail Reader\n// @version 0.2.0\nnewReader();\n"
        entry = {"file": "reader.user.js", "version": "0.2.0", "url": "https://example.test/reader.user.js",
                 "sha256": digest(self.source.encode()), "uuid": self.uuid, "requires": []}
        self.manifest = {"tampermonkey": {"path": str(self.extension)}, "scripts": [entry]}
        (self.repo / "configs/browser-components.json").write_text(json.dumps(self.manifest))
        (self.repo / "tools/browser-userscripts/reader.user.js").write_text(self.source)
        (self.extension / "manifest.json").write_text('{"version":"5.5.0.11"}')
        self.identity = components.extension_id(str(self.extension))
        self.policy = self.base / "policy.json"
        self.policy.write_text(json.dumps({"3rdparty": {"extensions": {self.identity: {
            "jsonImport": [{"hash": "current-managed-import"}]}}}}))
        self.receipt = {"manifest": self.manifest, "policySHA256": digest(self.policy.read_bytes()),
                        "files": {"manifest.json": digest((self.extension / "manifest.json").read_bytes())}}
        (self.system / "receipt.json").write_text(json.dumps(self.receipt))
        (self.profile / "Default/Preferences").write_text(json.dumps({"extensions": {"settings": {
            self.identity: {"state": 1, "user_scripts_enabled": True}}}}))
        self.state = {"!misc.managed.consumed": {"current-managed-import": True},
                      "!extdb.@meta#" + self.uuid: {"name": "Mail Reader", "version": "0.2.0", "system": True,
                          "enabled": True, "options": {"check_for_updates": False}},
                      "!extdb.@source#" + self.uuid: self.source}
        self.config = {"browser_host_socket": str(self.base / "host.sock"),
                       "host_runtime_root": str(self.base / "host-runtime"),
                       "workspace_root": str(self.base / "workspace"), "profile_id": "fixture",
                       "node": "/fixture/node", "anchor": {"kind": "http", "socket": str(self.base / "anchor.sock"),
                           "token_file": str(self.base / "anchor-token")}, "storage": {"budget_bytes": 123456}}
        self.config_path = self.root / "config.json"
        self.config_path.write_text(json.dumps(self.config))
        self.pointer = {"config": str(self.config_path), "python": str(self.root / "python"), "release_id": "fixture.29"}
        (self.root / "current.json").write_text(json.dumps(self.pointer))
        self.patches = [patch.object(qualifier, "ROOT", self.repo), patch.object(components, "ROOT", self.repo),
                        patch.object(components, "SYSTEM", self.system), patch.object(components, "POLICY", self.policy),
                        patch.object(components, "verify_public_permissions"),
                        patch("browser_component_state.read_extension_state", return_value=self.state),
                        patch.object(qualifier.subprocess, "check_output", return_value=json.dumps(self.pointer))]
        self.mocks = [item.start() for item in self.patches]
        for item in self.patches:
            self.addCleanup(item.stop)
        self.install = self.mocks[-1]

    def qualify(self):
        return qualifier.qualify_installed(self.root, self.profile)

    def test_matching_runtime_system_and_native_import_pass_without_writes(self):
        before = {str(p): p.read_bytes() for p in self.base.rglob("*") if p.is_file()}
        result = self.qualify()
        self.assertTrue(result["active_application_release"])
        self.assertTrue(result["native_managed_reader_import"])
        self.assertFalse(result["browser_or_mail_actions"])
        self.assertEqual(before, {str(p): p.read_bytes() for p in self.base.rglob("*") if p.is_file()})
        args, kwargs = self.install.call_args
        command = args[0]
        self.assertIn("--check", command)
        self.assertNotIn("--activate", command)
        for flag, value in [("--host-socket", self.config["browser_host_socket"]),
                            ("--host-runtime-root", self.config["host_runtime_root"]),
                            ("--workspace-root", self.config["workspace_root"]), ("--profile", "fixture"),
                            ("--anchor-socket", self.config["anchor"]["socket"]),
                            ("--anchor-token-file", self.config["anchor"]["token_file"]),
                            ("--storage-budget-bytes", "123456"), ("--node", "/fixture/node")]:
            self.assertEqual(command[command.index(flag) + 1], value)
        self.assertEqual(kwargs["env"]["PYTHONDONTWRITEBYTECODE"], "1")

    def test_same_reader_version_with_old_system_receipt_is_rejected(self):
        old = copy.deepcopy(self.receipt)
        old["manifest"]["scripts"][0]["sha256"] = digest(self.old_source.encode())
        self.assertEqual(old["manifest"]["scripts"][0]["version"], self.manifest["scripts"][0]["version"])
        (self.system / "receipt.json").write_text(json.dumps(old))
        with self.assertRaisesRegex(ValueError, "deployment is stale"):
            self.qualify()

    def test_same_reader_version_with_old_native_source_is_rejected(self):
        self.state["!extdb.@source#" + self.uuid] = self.old_source
        with self.assertRaisesRegex(ValueError, "changed, or stale"):
            self.qualify()

    def test_new_policy_not_consumed_by_native_profile_is_rejected(self):
        self.state["!misc.managed.consumed"] = {"previous-managed-import": True}
        with self.assertRaisesRegex(ValueError, "has not been imported"):
            self.qualify()

    def test_checked_staged_runtime_cannot_qualify_different_active_pointer(self):
        self.install.return_value = json.dumps({**self.pointer, "release_id": "fixture.28"})
        with self.assertRaisesRegex(ValueError, "Active App-CLI release differs"):
            self.qualify()

    def test_runtime_check_failure_prevents_native_checks(self):
        self.install.side_effect = subprocess.CalledProcessError(1, ["installer", "--check"])
        with patch.object(components, "verify_system") as check:
            with self.assertRaises(subprocess.CalledProcessError):
                self.qualify()
            check.assert_not_called()

    def test_release_change_during_checks_is_rejected(self):
        def changed(_):
            (self.root / "current.json").write_text(json.dumps({**self.pointer, "release_id": "raced"}))
        with patch.object(components, "verify_profile", side_effect=changed):
            with self.assertRaisesRegex(ValueError, "changed during"):
                self.qualify()

    def test_anchor_url_is_preserved_without_socket_default(self):
        self.config["anchor"] = {"kind": "http", "url": "https://example.test/anchor", "token_file": "/private/token"}
        self.config_path.write_text(json.dumps(self.config))
        self.qualify()
        command = self.install.call_args.args[0]
        self.assertNotIn("--anchor-socket", command)
        self.assertEqual(command[command.index("--anchor-url") + 1], self.config["anchor"]["url"])


if __name__ == "__main__":
    unittest.main()
