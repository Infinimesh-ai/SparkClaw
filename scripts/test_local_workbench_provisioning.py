import json
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
PROVISION = ROOT / "scripts" / "provision-local-workbench.mjs"


class LocalWorkbenchProvisioningTest(unittest.TestCase):
    def run_provision(self, runtime: Path, origin: str, deployment_id: str = "", check: bool = False, local_webchat_enabled: str = "false"):
        command = [
            "node",
            str(PROVISION),
            "--runtime-dir",
            str(runtime),
            "--origin",
            origin,
            "--deployment-id",
            deployment_id,
            "--local-webchat-enabled",
            local_webchat_enabled,
        ]
        if check:
            command.append("--check")
        return subprocess.run(command, cwd=ROOT, check=False, capture_output=True, text=True)

    def test_local_webchat_provisioning_is_private_independent_and_stable_across_switches(self):
        with tempfile.TemporaryDirectory() as temporary:
            runtime = Path(temporary).resolve() / "runtime"
            first = self.run_provision(runtime, "http://127.0.0.1:18790", local_webchat_enabled="true")
            self.assertEqual(first.returncode, 0, first.stderr)
            desktop = json.loads((runtime / "desktop-client.json").read_text())
            management = json.loads((runtime / "local-management.json").read_text())
            local_path = runtime / "local-webchat.json"
            local = json.loads(local_path.read_text())
            self.assertTrue(local["client_id"].startswith("local_webchat_"))
            self.assertEqual(local["deployment_id"], desktop["deployment_id"])
            self.assertEqual(local["owner_id"], desktop["owner_id"])
            self.assertEqual(len({local["token"], desktop["token"], management["token"]}), 3)
            self.assertEqual(stat.S_IMODE(local_path.stat().st_mode), 0o600)
            self.assertEqual(stat.S_IMODE((runtime / "local-webchat").stat().st_mode), 0o700)
            snapshot = {file.name: file.read_bytes() for file in runtime.iterdir() if file.is_file()}
            for enabled, check in [("true", True), ("false", False), ("true", False)]:
                result = self.run_provision(runtime, "http://127.0.0.1:18790", check=check, local_webchat_enabled=enabled)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertNotIn(local["token"], result.stdout + result.stderr)
                self.assertEqual(snapshot, {file.name: file.read_bytes() for file in runtime.iterdir() if file.is_file()})

    def test_local_webchat_checks_reject_missing_insecure_and_shared_credentials_without_repair(self):
        with tempfile.TemporaryDirectory() as temporary:
            runtime = Path(temporary).resolve() / "runtime"
            result = self.run_provision(runtime, "http://127.0.0.1:18790")
            self.assertEqual(result.returncode, 0, result.stderr)
            path = runtime / "local-webchat.json"
            missing = self.run_provision(runtime, "http://127.0.0.1:18790", check=True, local_webchat_enabled="true")
            self.assertNotEqual(missing.returncode, 0)
            self.assertFalse(path.exists())
            provisioned = self.run_provision(runtime, "http://127.0.0.1:18790", local_webchat_enabled="true")
            self.assertEqual(provisioned.returncode, 0, provisioned.stderr)
            original = path.read_bytes()
            desktop = json.loads((runtime / "desktop-client.json").read_text())
            for key, value in [("token", desktop["token"]), ("owner_id", "other-owner"), ("deployment_id", "other-deployment"), ("client_id", "client_desktop_invalid")]:
                with self.subTest(key=key):
                    changed = json.loads(original)
                    changed[key] = value
                    path.write_text(json.dumps(changed))
                    rejected = self.run_provision(runtime, "http://127.0.0.1:18790", local_webchat_enabled="true")
                    self.assertNotEqual(rejected.returncode, 0)
                    self.assertEqual(json.loads(path.read_text()), changed)
                    self.assertNotIn(changed["token"], rejected.stdout + rejected.stderr)
            path.write_bytes(original)
            path.chmod(0o644)
            rejected = self.run_provision(runtime, "http://127.0.0.1:18790", check=True, local_webchat_enabled="true")
            self.assertNotEqual(rejected.returncode, 0)
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o644)

    def test_local_webchat_switch_rejects_non_boolean(self):
        with tempfile.TemporaryDirectory() as temporary:
            runtime = Path(temporary).resolve() / "runtime"
            result = self.run_provision(runtime, "http://127.0.0.1:18790", local_webchat_enabled="yes")
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("must be true or false", result.stderr)
            self.assertFalse(runtime.exists())

    def test_disabled_legacy_deployment_prepares_private_socket_directory_before_later_enable(self):
        with tempfile.TemporaryDirectory() as temporary:
            runtime = Path(temporary).resolve() / "runtime"
            initial = self.run_provision(runtime, "http://127.0.0.1:18790")
            self.assertEqual(initial.returncode, 0, initial.stderr)
            socket_directory = runtime / "local-webchat"
            socket_directory.rmdir()  # Existing deployment predates local WebChat.
            checked = self.run_provision(runtime, "http://127.0.0.1:18790", check=True)
            self.assertEqual(checked.returncode, 0, checked.stderr)
            self.assertFalse(socket_directory.exists(), "check cannot create the Docker mount source")
            reconciled = self.run_provision(runtime, "http://127.0.0.1:18790")
            self.assertEqual(reconciled.returncode, 0, reconciled.stderr)
            self.assertEqual(stat.S_IMODE(socket_directory.stat().st_mode), 0o700)
            self.assertFalse((runtime / "local-webchat.json").exists())
            enabled = self.run_provision(runtime, "http://127.0.0.1:18790", local_webchat_enabled="true")
            self.assertEqual(enabled.returncode, 0, enabled.stderr)
            self.assertEqual(stat.S_IMODE(socket_directory.stat().st_mode), 0o700)

    def test_doctor_preflight_checks_private_management_without_retrieving_credentials(self):
        with tempfile.TemporaryDirectory() as temporary:
            runtime = Path(temporary) / "runtime"
            first = self.run_provision(runtime, "http://127.0.0.1:18790")
            self.assertEqual(first.returncode, 0, first.stderr)
            deployment_id = first.stdout.strip()
            management = runtime / "local-management.json"
            private = json.loads(management.read_text())
            desktop = json.loads((runtime / "desktop-client.json").read_text())
            self.assertNotEqual(private["token"], desktop["token"])
            snapshots = {file.name: file.read_bytes() for file in runtime.iterdir() if file.is_file()}
            checked = self.run_provision(runtime, "http://127.0.0.1:18790", deployment_id, check=True)
            self.assertEqual(checked.returncode, 0, checked.stderr)
            self.assertNotIn(private["token"], checked.stdout + checked.stderr)
            self.assertEqual(snapshots, {file.name: file.read_bytes() for file in runtime.iterdir() if file.is_file()})
            management.chmod(0o644)
            insecure = self.run_provision(runtime, "http://127.0.0.1:18790", deployment_id, check=True)
            self.assertNotEqual(insecure.returncode, 0)
            self.assertEqual(stat.S_IMODE(management.stat().st_mode), 0o644)
            management.unlink()
            missing = self.run_provision(runtime, "http://127.0.0.1:18790", deployment_id, check=True)
            self.assertNotEqual(missing.returncode, 0)
            self.assertFalse(management.exists(), "doctor check must never generate or recover credentials")

    def test_interrupted_deploy_retry_reuses_the_stable_desktop_client(self):
        with tempfile.TemporaryDirectory() as temporary:
            runtime = Path(temporary) / "runtime"
            first = self.run_provision(runtime, "http://127.0.0.1:18790")
            self.assertEqual(first.returncode, 0, first.stderr)
            deployment_id = first.stdout.strip()
            credential_path = runtime / "desktop-client.json"
            descriptor_path = runtime / "local-workbench.json"
            first_credential = credential_path.read_bytes()

            retry = self.run_provision(runtime, "http://127.0.0.1:18790", deployment_id)
            self.assertEqual(retry.returncode, 0, retry.stderr)
            self.assertEqual(retry.stdout.strip(), deployment_id)
            self.assertEqual(credential_path.read_bytes(), first_credential)

            changed_port = self.run_provision(runtime, "http://127.0.0.1:28443", deployment_id)
            self.assertEqual(changed_port.returncode, 0, changed_port.stderr)
            self.assertEqual(credential_path.read_bytes(), first_credential)
            self.assertEqual(json.loads(descriptor_path.read_text())["origin"], "http://127.0.0.1:28443")

            checked = self.run_provision(runtime, "http://127.0.0.1:28443", deployment_id, check=True)
            self.assertEqual(checked.returncode, 0, checked.stderr)
            self.assertEqual(stat.S_IMODE(runtime.stat().st_mode), 0o700)
            self.assertEqual(stat.S_IMODE(credential_path.stat().st_mode), 0o600)

            conflict = self.run_provision(runtime, "http://127.0.0.1:28443", "different-deployment")
            self.assertNotEqual(conflict.returncode, 0)
            self.assertEqual(credential_path.read_bytes(), first_credential)


if __name__ == "__main__":
    unittest.main()
