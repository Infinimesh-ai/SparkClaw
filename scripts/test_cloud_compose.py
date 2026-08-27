#!/usr/bin/env python3

from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "start_cloud_compose.sh"
COMPOSE = ROOT / "docker" / "compose.yaml"
CLOUD_COMPOSE = ROOT / "docker" / "compose.cloud.yaml"
VISIBLE_BROWSER_COMPOSE = ROOT / "docker" / "compose.visible-browser.yaml"
EXAMPLE_ENV = ROOT / "docker" / "env" / "sparkclaw.cloud.example.env"


FAKE_DOCKER = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

with Path(os.environ["CLOUD_TEST_DOCKER_LOG"]).open("a", encoding="utf-8") as stream:
    stream.write(json.dumps(sys.argv[1:]) + "\n")
if os.environ.get("CLOUD_TEST_REQUIRE_SUDO") == "true":
    if os.environ.get("CLOUD_TEST_UNDER_SUDO") != "true":
        raise SystemExit(1)
    if sys.argv[1:] != ["ps"] and os.environ.get("CLOUD_TEST_BROWSER_MODE") == "visible":
        if os.environ.get("SPARKCLAW_BROWSER_DISPLAY") != ":77":
            raise SystemExit(2)
        if os.environ.get("SPARKCLAW_BROWSER_XAUTHORITY") != "/tmp/cloud-test-Xauthority":
            raise SystemExit(3)
raise SystemExit(0)
"""

FAKE_CURL = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

with Path(os.environ["CLOUD_TEST_CURL_LOG"]).open("a", encoding="utf-8") as stream:
    stream.write(json.dumps(sys.argv[1:]) + "\n")
mode = os.environ["CLOUD_TEST_MODEL_MODE"]
print('{"ok":true,"auth_required":false,"model_mode":"%s","state_backend":"postgres"}' % mode)
"""

FAKE_DISPLAY_RESOLVER = r"""#!/usr/bin/env bash
set -euo pipefail
if [[ "${CLOUD_TEST_BROWSER_MODE}" != "visible" ]]; then
  exit 1
fi
printf ':77\n/tmp/cloud-test-Xauthority\n'
"""

FAKE_SUDO = r"""#!/usr/bin/env python3
import os
import sys

args = sys.argv[1:]
if args and args[0] == "-n":
    args = args[1:]
environment = os.environ.copy()
environment["CLOUD_TEST_UNDER_SUDO"] = "true"
os.execvpe(args[0], args, environment)
"""


class CloudComposeTest(unittest.TestCase):
    def run_script(
        self,
        env_text: str,
        expected_mode: str,
        *args: str,
        visible_browser: bool = False,
        require_sudo: bool = False,
    ) -> tuple[
        subprocess.CompletedProcess[str], list[list[str]], list[list[str]]
    ]:
        with tempfile.TemporaryDirectory() as directory:
            temp_path = Path(directory)
            env_file = temp_path / "cloud.env"
            env_file.write_text(textwrap.dedent(env_text), encoding="utf-8")
            docker = temp_path / "docker"
            curl = temp_path / "curl"
            sudo = temp_path / "sudo"
            display_resolver = temp_path / "resolve-browser-display.sh"
            docker.write_text(textwrap.dedent(FAKE_DOCKER), encoding="utf-8")
            curl.write_text(textwrap.dedent(FAKE_CURL), encoding="utf-8")
            sudo.write_text(textwrap.dedent(FAKE_SUDO), encoding="utf-8")
            display_resolver.write_text(
                textwrap.dedent(FAKE_DISPLAY_RESOLVER), encoding="utf-8"
            )
            docker.chmod(0o755)
            curl.chmod(0o755)
            sudo.chmod(0o755)
            display_resolver.chmod(0o755)
            docker_log = temp_path / "docker.jsonl"
            curl_log = temp_path / "curl.jsonl"
            environment = os.environ.copy()
            environment.update(
                {
                    "DOCKER_BIN": str(docker),
                    "PATH": f"{temp_path}:{environment['PATH']}",
                    "SPARKCLAW_CLOUD_ENV_FILE": str(env_file),
                    "CLOUD_TEST_DOCKER_LOG": str(docker_log),
                    "CLOUD_TEST_CURL_LOG": str(curl_log),
                    "CLOUD_TEST_MODEL_MODE": expected_mode,
                    "CLOUD_TEST_BROWSER_MODE": "visible" if visible_browser else "hidden",
                    "CLOUD_TEST_REQUIRE_SUDO": "true" if require_sudo else "false",
                    "SPARKCLAW_BROWSER_DISPLAY_RESOLVER": str(display_resolver),
                }
            )
            result = subprocess.run(
                ["bash", str(SCRIPT), *args],
                cwd=ROOT,
                env=environment,
                check=False,
                capture_output=True,
                text=True,
            )
            docker_calls = []
            if docker_log.exists():
                docker_calls = [json.loads(line) for line in docker_log.read_text().splitlines()]
            curl_calls = []
            if curl_log.exists():
                curl_calls = [json.loads(line) for line in curl_log.read_text().splitlines()]
            return result, docker_calls, curl_calls

    def test_mock_runtime_starts_only_server_services(self) -> None:
        result, docker_calls, curl_calls = self.run_script(
            """
            SPARKCLAW_API_TOKEN=legacy-owner-token
            SPARKCLAW_MODEL_MODE=mock
            SPARKCLAW_STATE_BACKEND=postgres
            SPARKCLAW_WEBCHAT_PORT=19876
            """,
            "mock",
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("mock/postgres", result.stdout)
        up_call = next(call for call in docker_calls if "up" in call)
        self.assertEqual(up_call[-4:], ["postgres", "sandbox-runner", "gateway", "webchat"])
        self.assertIn(str(CLOUD_COMPOSE), up_call)
        self.assertNotIn("sparkclaw-fast", up_call)
        self.assertEqual(len(curl_calls), 1)
        self.assertIn("http://127.0.0.1:19876/readyz", curl_calls[0])
        self.assertNotIn("Authorization", " ".join(curl_calls[0]))
        browser_call = next(call for call in docker_calls if "exec" in call)
        self.assertIn("gateway", browser_call)
        self.assertIn("agent-browser", " ".join(browser_call))
        self.assertNotIn(str(VISIBLE_BROWSER_COMPOSE), up_call)

    def test_desktop_runtime_stacks_visible_browser_overlay(self) -> None:
        result, docker_calls, _ = self.run_script(
            """
            SPARKCLAW_MODEL_MODE=mock
            SPARKCLAW_STATE_BACKEND=postgres
            """,
            "mock",
            visible_browser=True,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Visible Chromium display: :77", result.stdout)
        self.assertIn("hidden and visible Chromium ready", result.stdout)
        up_call = next(call for call in docker_calls if "up" in call)
        self.assertIn(str(VISIBLE_BROWSER_COMPOSE), up_call)
        browser_call = next(call for call in docker_calls if "exec" in call)
        self.assertIn(str(VISIBLE_BROWSER_COMPOSE), browser_call)
        self.assertIn("AGENT_BROWSER_HEADED=true", " ".join(browser_call))

    def test_desktop_runtime_preserves_display_when_docker_requires_sudo(self) -> None:
        result, docker_calls, _ = self.run_script(
            """
            SPARKCLAW_MODEL_MODE=mock
            SPARKCLAW_STATE_BACKEND=postgres
            """,
            "mock",
            visible_browser=True,
            require_sudo=True,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        up_call = next(call for call in docker_calls if "up" in call)
        self.assertIn(str(VISIBLE_BROWSER_COMPOSE), up_call)

    def test_external_runtime_rejects_placeholder_endpoints(self) -> None:
        result, docker_calls, curl_calls = self.run_script(
            """
            SPARKCLAW_MODEL_MODE=external
            SPARKCLAW_STATE_BACKEND=postgres
            SPARKCLAW_FAST_BASE_URL=https://fast.models.example.invalid/v1
            """,
            "external",
        )

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("SPARKCLAW_FAST_BASE_URL", result.stderr)
        self.assertEqual(docker_calls, [])
        self.assertEqual(curl_calls, [])

    def test_external_runtime_accepts_complete_openai_compatible_config(self) -> None:
        result, docker_calls, _ = self.run_script(
            """
            SPARKCLAW_MODEL_MODE=external
            SPARKCLAW_STATE_BACKEND=postgres
            SPARKCLAW_FAST_BASE_URL=https://models.test/v1
            SPARKCLAW_FAST_MODEL=fast-model
            SPARKCLAW_DEEP_BASE_URL=https://models.test/v1
            SPARKCLAW_DEEP_MODEL=deep-model
            SPARKCLAW_EMBEDDING_BASE_URL=https://models.test/v1
            SPARKCLAW_EMBEDDING_MODEL=embedding-model
            SPARKCLAW_GUARD_BASE_URL=https://models.test/v1
            SPARKCLAW_GUARD_MODEL=guard-model
            """,
            "external",
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any("up" in call for call in docker_calls))

    def test_check_validates_compose_without_starting_services(self) -> None:
        result, docker_calls, curl_calls = self.run_script(
            """
            SPARKCLAW_MODEL_MODE=external
            SPARKCLAW_STATE_BACKEND=postgres
            SPARKCLAW_FAST_BASE_URL=https://models.test/v1
            SPARKCLAW_FAST_MODEL=fast-model
            SPARKCLAW_DEEP_BASE_URL=https://models.test/v1
            SPARKCLAW_DEEP_MODEL=deep-model
            SPARKCLAW_EMBEDDING_BASE_URL=https://models.test/v1
            SPARKCLAW_EMBEDDING_MODEL=embedding-model
            SPARKCLAW_GUARD_BASE_URL=https://models.test/v1
            SPARKCLAW_GUARD_MODEL=guard-model
            """,
            "external",
            "--check",
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("configuration valid", result.stdout)
        self.assertFalse(any("up" in call for call in docker_calls))
        self.assertFalse(any("exec" in call for call in docker_calls))
        self.assertEqual(curl_calls, [])

    def test_example_contains_standard_model_names_but_no_endpoints_or_api_key(self) -> None:
        values = {}
        for line in EXAMPLE_ENV.read_text(encoding="utf-8").splitlines():
            if line and not line.startswith("#") and "=" in line:
                key, value = line.split("=", 1)
                values[key] = value

        self.assertNotIn("OPENAI_API_KEY", values)
        self.assertEqual(values["SPARKCLAW_API_TOKEN"], "")
        for key in (
            "SPARKCLAW_FAST_BASE_URL",
            "SPARKCLAW_DEEP_BASE_URL",
            "SPARKCLAW_EMBEDDING_BASE_URL",
            "SPARKCLAW_GUARD_BASE_URL",
            "SPARKCLAW_SPEECH_BASE_URL",
            "SPARKCLAW_OCR_BASE_URL",
        ):
            self.assertEqual(values[key], "", key)
        self.assertEqual(values["SPARKCLAW_FAST_MODEL"], "sparkclaw-fast")
        self.assertEqual(values["SPARKCLAW_DEEP_MODEL"], "sparkclaw-deep")
        self.assertEqual(values["SPARKCLAW_EMBEDDING_MODEL"], "sparkclaw-embedding")
        self.assertEqual(values["SPARKCLAW_GUARD_MODEL"], "sparkclaw-guard")
        self.assertEqual(values["SPARKCLAW_SPEECH_MODEL"], "sparkclaw-asr")
        self.assertEqual(values["SPARKCLAW_OCR_MODEL"], "sparkclaw-ocr")

    def test_cloud_overlay_keeps_restart_policy(self) -> None:
        result = subprocess.run(
            [
                "docker",
                "compose",
                "--env-file",
                str(EXAMPLE_ENV),
                "-f",
                str(COMPOSE),
                "-f",
                str(CLOUD_COMPOSE),
                "--profile",
                "models-local",
                "config",
                "--format",
                "json",
            ],
            cwd=ROOT,
            check=False,
            capture_output=True,
            text=True,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        config = json.loads(result.stdout)
        environment = config["services"]["gateway"]["environment"]
        self.assertEqual(environment["SPARKCLAW_MODEL_MODE"], "external")
        self.assertEqual(environment["SPARKCLAW_STATE_BACKEND"], "postgres")
        self.assertEqual(environment["SPARKCLAW_API_TOKEN"], "")
        self.assertEqual(
            config["services"]["gateway"]["depends_on"]["postgres"]["condition"],
            "service_healthy",
        )
        for service in ("postgres", "sandbox-runner", "gateway", "webchat"):
            self.assertEqual(config["services"][service]["restart"], "unless-stopped")


if __name__ == "__main__":
    unittest.main()
