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
SCRIPT = ROOT / "scripts" / "deploy_cloud_vm.sh"
TEMPLATE = ROOT / "docker" / "env" / "sparkclaw.cloud.example.env"


FAKE_DOCKER = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

with Path(os.environ["VM_TEST_DOCKER_LOG"]).open("a", encoding="utf-8") as stream:
    stream.write(json.dumps(sys.argv[1:]) + "\n")
if sys.argv[1:] == ["compose", "version"]:
    print("Docker Compose version v2.40.0")
raise SystemExit(0)
"""

FAKE_CURL = r"""#!/usr/bin/env python3
print('{"ok":true,"auth_required":false,"model_mode":"external","state_backend":"postgres"}')
"""


def set_env_value(text: str, key: str, value: str) -> str:
    lines = text.splitlines()
    replacement = f"{key}={value}"
    for index, line in enumerate(lines):
        if line.startswith(f"{key}="):
            lines[index] = replacement
            break
    else:
        lines.append(replacement)
    return "\n".join(lines) + "\n"


def configured_env() -> str:
    text = TEMPLATE.read_text(encoding="utf-8")
    values = {
        "SPARKCLAW_API_TOKEN": "legacy-owner-token",
        "SPARKCLAW_FAST_BASE_URL": "https://models.test/fast/v1",
        "SPARKCLAW_FAST_MODEL": "fast-model",
        "SPARKCLAW_DEEP_BASE_URL": "https://models.test/deep/v1",
        "SPARKCLAW_DEEP_MODEL": "deep-model",
        "SPARKCLAW_EMBEDDING_BASE_URL": "https://models.test/embedding/v1",
        "SPARKCLAW_EMBEDDING_MODEL": "embedding-model",
        "SPARKCLAW_GUARD_BASE_URL": "https://models.test/guard/v1",
        "SPARKCLAW_GUARD_MODEL": "guard-model",
    }
    for key, value in values.items():
        text = set_env_value(text, key, value)
    return text


class DeployCloudVMTest(unittest.TestCase):
    def test_cloud_template_disables_model_thinking(self) -> None:
        values = dict(
            line.split("=", 1)
            for line in TEMPLATE.read_text(encoding="utf-8").splitlines()
            if line and not line.startswith("#")
        )

        self.assertEqual(values["SPARKCLAW_MODEL_DISABLE_THINKING"], "true")

    def run_script(
        self, env_text: str, *args: str, input_text: str | None = None
    ) -> tuple[subprocess.CompletedProcess[str], list[list[str]], str]:
        with tempfile.TemporaryDirectory() as directory:
            temp_path = Path(directory)
            env_file = temp_path / "cloud.env"
            env_file.write_text(env_text, encoding="utf-8")
            docker = temp_path / "docker"
            curl = temp_path / "curl"
            docker.write_text(textwrap.dedent(FAKE_DOCKER), encoding="utf-8")
            curl.write_text(textwrap.dedent(FAKE_CURL), encoding="utf-8")
            docker.chmod(0o755)
            curl.chmod(0o755)
            docker_log = temp_path / "docker.jsonl"
            environment = os.environ.copy()
            environment.update(
                {
                    "DOCKER_BIN": str(docker),
                    "PATH": f"{temp_path}:{environment['PATH']}",
                    "VM_TEST_DOCKER_LOG": str(docker_log),
                }
            )
            result = subprocess.run(
                ["bash", str(SCRIPT), "--env-file", str(env_file), *args],
                cwd=ROOT,
                env=environment,
                input=input_text,
                check=False,
                capture_output=True,
                text=True,
                timeout=20,
            )
            calls = []
            if docker_log.exists():
                calls = [json.loads(line) for line in docker_log.read_text().splitlines()]
            return result, calls, env_file.read_text(encoding="utf-8")

    def test_deploy_accepts_models_without_api_key_and_runs_browser_smoke(self) -> None:
        result, calls, final_env = self.run_script(configured_env())

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("deployment complete", result.stdout)
        self.assertIn("SPARKCLAW_API_TOKEN=\n", final_env)
        self.assertNotIn("legacy-owner-token", final_env)
        self.assertNotIn("OPENAI_API_KEY=", final_env)
        self.assertTrue(any("up" in call for call in calls))
        browser_call = next(call for call in calls if "exec" in call)
        self.assertIn("agent-browser", " ".join(browser_call))

    def test_check_does_not_change_containers(self) -> None:
        result, calls, _ = self.run_script(configured_env(), "--check")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("deployment check passed", result.stdout)
        self.assertFalse(any("up" in call for call in calls))
        self.assertFalse(any("exec" in call for call in calls))

    def test_first_configuration_keeps_endpoints_private_and_key_optional(self) -> None:
        answers = "\n".join(
            (
                "https://private.models.test/fast/v1",
                "",
                "https://private.models.test/embedding/v1",
                "https://private.models.test/guard/v1",
                "",
                "",
                "",
            )
        ) + "\n"
        result, _, final_env = self.run_script(TEMPLATE.read_text(encoding="utf-8"), input_text=answers)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("SPARKCLAW_FAST_BASE_URL=https://private.models.test/fast/v1", final_env)
        self.assertIn("SPARKCLAW_DEEP_BASE_URL=https://private.models.test/fast/v1", final_env)
        self.assertIn("SPARKCLAW_FAST_MODEL=sparkclaw-fast", final_env)
        self.assertIn("SPARKCLAW_DEEP_MODEL=sparkclaw-fast", final_env)
        self.assertIn("SPARKCLAW_EMBEDDING_MODEL=sparkclaw-embedding", final_env)
        self.assertIn("SPARKCLAW_GUARD_MODEL=sparkclaw-guard", final_env)
        self.assertIn("OPENAI_API_KEY=\n", final_env)
        self.assertNotIn("model name", result.stderr.lower())

    def test_separate_deep_endpoint_uses_standard_deep_name(self) -> None:
        answers = "\n".join(
            (
                "https://private.models.test/fast/v1",
                "n",
                "https://private.models.test/deep/v1",
                "https://private.models.test/embedding/v1",
                "https://private.models.test/guard/v1",
                "",
                "",
                "",
            )
        ) + "\n"
        result, _, final_env = self.run_script(
            TEMPLATE.read_text(encoding="utf-8"), input_text=answers
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(
            "SPARKCLAW_DEEP_BASE_URL=https://private.models.test/deep/v1", final_env
        )
        self.assertIn("SPARKCLAW_DEEP_MODEL=sparkclaw-deep", final_env)
        self.assertIn("SPARKCLAW_DEEP_SERVED_NAME=sparkclaw-deep", final_env)
        self.assertNotIn("model name", result.stderr.lower())

    def test_reconfiguration_preserves_existing_model_names_without_prompting(self) -> None:
        result, _, final_env = self.run_script(
            configured_env(), "--configure", input_text="\n" * 8
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("SPARKCLAW_FAST_MODEL=fast-model", final_env)
        self.assertIn("SPARKCLAW_DEEP_MODEL=deep-model", final_env)
        self.assertIn("SPARKCLAW_EMBEDDING_MODEL=embedding-model", final_env)
        self.assertIn("SPARKCLAW_GUARD_MODEL=guard-model", final_env)
        self.assertNotIn("model name", result.stderr.lower())

    def test_optional_services_use_standard_names_without_prompting(self) -> None:
        answers = "\n".join(
            (
                "https://private.models.test/fast/v1",
                "",
                "https://private.models.test/embedding/v1",
                "https://private.models.test/guard/v1",
                "",
                "y",
                "https://private.models.test/asr",
                "y",
                "https://private.models.test/ocr/v1",
            )
        ) + "\n"
        result, _, final_env = self.run_script(
            TEMPLATE.read_text(encoding="utf-8"), input_text=answers
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("SPARKCLAW_SPEECH_MODEL=sparkclaw-asr", final_env)
        self.assertIn("SPARKCLAW_OCR_MODEL=sparkclaw-ocr", final_env)
        self.assertNotIn("model name", result.stderr.lower())

    def test_legacy_placeholder_key_is_rejected_without_reconfiguration(self) -> None:
        env_text = set_env_value(configured_env(), "OPENAI_API_KEY", "replace-with-cloud-api-key")
        result, calls, _ = self.run_script(env_text, "--check")

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("OPENAI_API_KEY", result.stderr)
        self.assertEqual(calls, [])


if __name__ == "__main__":
    unittest.main()
