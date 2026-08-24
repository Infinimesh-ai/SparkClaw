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
EXAMPLE_ENV = ROOT / "docker" / "env" / "sparkclaw.cloud.example.env"


FAKE_DOCKER = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

with Path(os.environ["CLOUD_TEST_DOCKER_LOG"]).open("a", encoding="utf-8") as stream:
    stream.write(json.dumps(sys.argv[1:]) + "\n")
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
print('{"ok":true,"model_mode":"%s","state_backend":"postgres"}' % mode)
"""


class CloudComposeTest(unittest.TestCase):
    def run_script(self, env_text: str, expected_mode: str) -> tuple[
        subprocess.CompletedProcess[str], list[list[str]], list[list[str]]
    ]:
        with tempfile.TemporaryDirectory() as directory:
            temp_path = Path(directory)
            env_file = temp_path / "cloud.env"
            env_file.write_text(textwrap.dedent(env_text), encoding="utf-8")
            docker = temp_path / "docker"
            curl = temp_path / "curl"
            docker.write_text(textwrap.dedent(FAKE_DOCKER), encoding="utf-8")
            curl.write_text(textwrap.dedent(FAKE_CURL), encoding="utf-8")
            docker.chmod(0o755)
            curl.chmod(0o755)
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
                }
            )
            result = subprocess.run(
                ["bash", str(SCRIPT)],
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
            SPARKCLAW_API_TOKEN=test-webchat-token
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
        self.assertIn("Authorization: Bearer test-webchat-token", curl_calls[0])

    def test_external_runtime_rejects_placeholder_endpoints(self) -> None:
        result, docker_calls, curl_calls = self.run_script(
            """
            SPARKCLAW_API_TOKEN=test-webchat-token
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
            SPARKCLAW_API_TOKEN=test-webchat-token
            SPARKCLAW_MODEL_MODE=external
            SPARKCLAW_STATE_BACKEND=postgres
            OPENAI_API_KEY=test-model-token
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

    def test_example_expands_to_mock_postgres_with_restart_policy(self) -> None:
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
        self.assertEqual(config["services"]["gateway"]["environment"]["SPARKCLAW_MODEL_MODE"], "mock")
        self.assertEqual(config["services"]["gateway"]["environment"]["SPARKCLAW_STATE_BACKEND"], "postgres")
        for service in ("postgres", "sandbox-runner", "gateway", "webchat"):
            self.assertEqual(config["services"][service]["restart"], "unless-stopped")


if __name__ == "__main__":
    unittest.main()

