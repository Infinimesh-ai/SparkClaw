import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]


class DesktopLauncherTest(unittest.TestCase):
    def test_installs_checks_and_passes_only_paths_to_desktop(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            runtime = root / "runtime"
            runtime.mkdir(mode=0o700)
            (runtime / "local-workbench.json").write_text(json.dumps({
                "schema_version": 1,
                "origin": "http://127.0.0.1:28443",
                "deployment_id": "deployment-test",
            }), encoding="utf-8")
            credential = runtime / "desktop-client.json"
            credential.write_text(json.dumps({
                "schema_version": 1,
                "deployment_id": "deployment-test",
                "client_id": "client-desktop",
                "owner_id": "owner",
                "client_name": "Desktop",
                "token": "secret-that-must-not-appear-in-launcher",
            }), encoding="utf-8")
            credential.chmod(0o600)
            executable = root / "desktop"
            executable.write_text("#!/usr/bin/env bash\nprintf '%s\\n%s\\n' \"$SPARKCLAW_DESKTOP_CONNECTION_FILE\" \"$SPARKCLAW_DESKTOP_CREDENTIAL_FILE\"\n", encoding="utf-8")
            executable.chmod(executable.stat().st_mode | stat.S_IXUSR)
            env = {
                **os.environ,
                "XDG_DATA_HOME": str(root / "data"),
                "XDG_CONFIG_HOME": str(root / "config"),
            }
            command = ["bash", str(ROOT / "scripts/install-desktop-launcher.sh"), "--executable", str(executable), "--runtime-dir", str(runtime)]
            subprocess.run(command, cwd=ROOT, env=env, check=True, capture_output=True, text=True)
            subprocess.run([*command, "--check"], cwd=ROOT, env=env, check=True, capture_output=True, text=True)
            launcher = root / "data/sparkclaw/desktop/bin/sparkclaw-desktop"
            desktop_entry = root / "data/applications/sparkclaw.desktop"
            installed_icon = root / "data/icons/hicolor/512x512/apps/sparkclaw.png"
            result = subprocess.run([str(launcher)], env=env, check=True, capture_output=True, text=True)
            self.assertEqual(result.stdout.splitlines(), [str(runtime / "local-workbench.json"), str(credential)])
            self.assertIn("Icon=sparkclaw", desktop_entry.read_text(encoding="utf-8"))
            self.assertEqual(installed_icon.read_bytes(), (ROOT / "apps/desktop/src/assets/icon.png").read_bytes())
            self.assertNotIn("secret-that-must-not-appear", launcher.read_text(encoding="utf-8"))
            self.assertNotIn("secret-that-must-not-appear", (root / "config/sparkclaw/desktop-launcher.conf").read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()
