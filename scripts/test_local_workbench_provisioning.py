import json
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
PROVISION = ROOT / "scripts" / "provision-local-workbench.mjs"


class LocalWorkbenchProvisioningTest(unittest.TestCase):
    def run_provision(self, runtime: Path, origin: str, deployment_id: str = "", check: bool = False):
        command = [
            "node",
            str(PROVISION),
            "--runtime-dir",
            str(runtime),
            "--origin",
            origin,
            "--deployment-id",
            deployment_id,
        ]
        if check:
            command.append("--check")
        return subprocess.run(command, cwd=ROOT, check=False, capture_output=True, text=True)

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
