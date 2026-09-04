#!/usr/bin/env python3

from __future__ import annotations

import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "deploy_remote.sh"
PROFILE = ROOT / "docker" / "env" / "sparkclaw.remote.env"
PRODUCT_PROFILE = ROOT / "docker" / "env" / "sparkclaw.product.env"


FAKE_DOCKER = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

with Path(os.environ["REMOTE_DEPLOY_DOCKER_LOG"]).open("a", encoding="utf-8") as stream:
    stream.write(json.dumps(sys.argv[1:]) + "\n")
if sys.argv[1:] == ["compose", "version"]:
    print("Docker Compose version v2.40.0")
raise SystemExit(0)
"""

FAKE_CURL = r"""#!/usr/bin/env python3
print('{"ok":true,"auth_required":false,"model_mode":"external","state_backend":"postgres"}')
"""

FAKE_HOST_BROWSER_INSTALLER = r"""#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$REMOTE_DEPLOY_BROWSER_LOG"
"""


class DeployRemoteTest(unittest.TestCase):
    def run_script(
        self,
        private_text: str,
        *args: str,
        input_text: str | None = None,
    ) -> tuple[subprocess.CompletedProcess[str], list[list[str]], str, list[str]]:
        with tempfile.TemporaryDirectory() as directory:
            temp_path = Path(directory)
            private_env = temp_path / ".env.remote"
            endpoint_file = temp_path / "browserd" / "cdp-endpoint"
            endpoint_file.parent.mkdir()
            endpoint_file.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "profileID": "default",
                        "presentation": "headless",
                        "browserPID": os.getpid(),
                        "generation": 1,
                        "browserVersion": "Chromium 148.0.7778.0",
                        "webSocketURL": "ws://host.docker.internal:18791/test/devtools/browser/id",
                        "hostWebSocketURL": "ws://127.0.0.1:18791/test/devtools/browser/id",
                    }
                )
                + "\n",
                encoding="utf-8",
            )
            endpoint_file.chmod(0o600)
            private_env.write_text(
                f"SPARKCLAW_BROWSER_CDP_RUNTIME_DIR_HOST={endpoint_file.parent}\n"
                f"SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE_HOST={endpoint_file}\n"
                f"{private_text}",
                encoding="utf-8",
            )
            docker = temp_path / "docker"
            curl = temp_path / "curl"
            browser_installer = temp_path / "install-host-browser.sh"
            docker.write_text(textwrap.dedent(FAKE_DOCKER), encoding="utf-8")
            curl.write_text(textwrap.dedent(FAKE_CURL), encoding="utf-8")
            browser_installer.write_text(
                textwrap.dedent(FAKE_HOST_BROWSER_INSTALLER), encoding="utf-8"
            )
            for executable in (docker, curl, browser_installer):
                executable.chmod(0o755)
            docker_log = temp_path / "docker.jsonl"
            browser_log = temp_path / "browser.log"
            environment = os.environ.copy()
            environment.update(
                {
                    "DOCKER_BIN": str(docker),
                    "PATH": f"{temp_path}:{environment['PATH']}",
                    "REMOTE_DEPLOY_DOCKER_LOG": str(docker_log),
                    "REMOTE_DEPLOY_BROWSER_LOG": str(browser_log),
                    "SPARKCLAW_HOST_BROWSER_INSTALLER": str(browser_installer),
                }
            )
            result = subprocess.run(
                ["bash", str(SCRIPT), "--env-file", str(private_env), *args],
                cwd=ROOT,
                env=environment,
                input=input_text,
                check=False,
                capture_output=True,
                text=True,
                timeout=20,
            )
            docker_calls = []
            if docker_log.exists():
                docker_calls = [json.loads(line) for line in docker_log.read_text().splitlines()]
            browser_calls = browser_log.read_text().splitlines() if browser_log.exists() else []
            return result, docker_calls, private_env.read_text(encoding="utf-8"), browser_calls

    def test_remote_profile_disables_thinking_and_versions_endpoints(self) -> None:
        values = {}
        for path in (PRODUCT_PROFILE, PROFILE):
            values.update(
                line.split("=", 1)
                for line in path.read_text(encoding="utf-8").splitlines()
                if line and not line.startswith("#")
            )

        self.assertEqual(values["SPARKCLAW_MODEL_DISABLE_THINKING"], "true")
        self.assertEqual(values["SPARKCLAW_MODEL_CAPACITY_PROFILE"], "sparkclaw-product-v1")
        self.assertEqual(values["SPARKCLAW_WORKFLOW_STAGE_EVIDENCE_MAX_BYTES"], "200000")
        self.assertEqual(values["SPARKCLAW_WORKFLOW_RUN_OBSERVATION_COMPACTION_BYTES"], "72000")
        self.assertEqual(values["SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES"], "96000")
        self.assertEqual(values["SPARKCLAW_SPEECH_BASE_URL"], "https://sparkclaw.infinimesh.cloud/asr")

    def test_deploy_keeps_profile_defaults_out_of_private_env(self) -> None:
        result, calls, final_env, browser_calls = self.run_script("SPARKCLAW_WEBCHAT_PORT=19876\n")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("deployment complete", result.stdout)
        self.assertIn("SPARKCLAW_WEBCHAT_PORT=19876\n", final_env)
        self.assertIn("SPARKCLAW_CONTAINER_UID=", final_env)
        self.assertIn("SPARKCLAW_CONTAINER_GID=", final_env)
        self.assertIn("SPARKCLAW_SANDBOX_HOST_WORKSPACE_ROOT=", final_env)
        match = re.search(r"^SPARKCLAW_WEBCHAT_PROXY_TOKEN=([A-Za-z0-9_-]{43,128})$", final_env, re.MULTILINE)
        self.assertIsNotNone(match)
        self.assertNotIn(match.group(1), result.stdout)
        self.assertNotIn(match.group(1), result.stderr)
        for profile_key in (
            "SPARKCLAW_DEPLOYMENT_PROFILE",
            "SPARKCLAW_MODEL_CAPACITY_PROFILE",
            "SPARKCLAW_FAST_BASE_URL",
            "SPARKCLAW_EMBEDDING_BASE_URL",
            "SPARKCLAW_GUARD_BASE_URL",
            "SPARKCLAW_SPEECH_BASE_URL",
            "SPARKCLAW_OCR_BASE_URL",
        ):
            self.assertNotIn(f"{profile_key}=", final_env)
        self.assertTrue(any("stop" in call for call in calls))
        self.assertTrue(any("up" in call for call in calls))
        self.assertTrue(any(call.startswith("--env-file ") for call in browser_calls))
        self.assertTrue(any(call.startswith("--check --env-file ") for call in browser_calls))

    def test_check_does_not_change_containers_or_private_env(self) -> None:
        original = (
            "SPARKCLAW_WEBCHAT_PROXY_TOKEN=remote_check_webchat_proxy_token_0123456789abcd\n"
            "SPARKCLAW_WEBCHAT_PORT=19876\n"
        )
        result, calls, final_env, browser_calls = self.run_script(original, "--check")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("deployment check passed", result.stdout)
        self.assertFalse(any("stop" in call or "up" in call or "exec" in call for call in calls))
        self.assertTrue(any("config" in call for call in calls))
        self.assertTrue(final_env.endswith(original))
        self.assertTrue(any(call.startswith("--check --env-file ") for call in browser_calls))

    def test_configure_writes_only_optional_api_credential(self) -> None:
        result, _, final_env, _ = self.run_script(
            "", "--configure", input_text="secret-token\n"
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("OPENAI_API_KEY=secret-token\n", final_env)
        self.assertNotIn("SPARKCLAW_FAST_BASE_URL=", final_env)

    def test_local_endpoint_override_is_rejected_before_docker(self) -> None:
        result, calls, _, _ = self.run_script(
            "SPARKCLAW_FAST_BASE_URL=http://sparkclaw-fast:8001/v1\n",
            "--check",
        )

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not a supported credential or machine override", result.stderr)
        self.assertEqual(calls, [])


if __name__ == "__main__":
    unittest.main()
