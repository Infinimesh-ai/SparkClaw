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
SCRIPT = ROOT / "scripts" / "serve_models_compose.sh"
PRODUCT_SERVICES = (
    "sparkclaw-fast",
    "sparkclaw-embedding",
    "sparkclaw-guard",
    "sparkclaw-asr",
    "sparkclaw-ocr",
)


FAKE_DOCKER = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

args = sys.argv[1:]
with Path(os.environ["FAKE_DOCKER_LOG"]).open("a", encoding="utf-8") as stream:
    stream.write(json.dumps(args) + "\n")

if args == ["ps"]:
    raise SystemExit(0)

state = json.loads(os.environ["FAKE_DOCKER_STATE"])

if args and args[0] == "inspect":
    container_id = args[-1]
    service = next(
        (name for name, entry in state.items() if entry.get("id") == container_id),
        None,
    )
    if service is None:
        raise SystemExit(1)
    entry = state[service]
    output_format = args[args.index("--format") + 1]
    if output_format == "{{.State.Status}}":
        print(entry.get("status", "running"))
    elif ".State.Health" in output_format:
        print(entry.get("health", "healthy"))
    elif "com.docker.compose.config-hash" in output_format:
        print(entry.get("actual_hash", "current"))
    else:
        raise SystemExit(2)
    raise SystemExit(0)

if "ps" in args and "--all" in args and "-q" in args:
    service = args[-1]
    container_id = state.get(service, {}).get("id", "")
    if container_id:
        print(container_id)
    raise SystemExit(0)

if "config" in args and "--hash" in args:
    service = args[-1]
    print(service, state.get(service, {}).get("expected_hash", "current"))
    raise SystemExit(0)

raise SystemExit(0)
"""


def healthy_state() -> dict[str, dict[str, str]]:
    return {
        service: {
            "id": f"{service}-id",
            "status": "running",
            "health": "healthy",
            "expected_hash": "current",
            "actual_hash": "current",
        }
        for service in (*PRODUCT_SERVICES, "sparkclaw-deep")
    }


class ServeModelsComposeTest(unittest.TestCase):
    def run_script(
        self,
        lane: str,
        *,
        state: dict[str, dict[str, str]] | None = None,
        extra_env: dict[str, str] | None = None,
        check: bool = True,
    ) -> tuple[subprocess.CompletedProcess[str], list[list[str]]]:
        with tempfile.TemporaryDirectory() as temp_dir:
            temp_path = Path(temp_dir)
            fake_docker = temp_path / "docker"
            fake_docker.write_text(textwrap.dedent(FAKE_DOCKER), encoding="utf-8")
            fake_docker.chmod(0o755)
            log_path = temp_path / "docker.jsonl"
            env = os.environ.copy()
            env.pop("SPARKCLAW_FORCE_MODEL_RECREATE", None)
            env.update(
                {
                    "DOCKER_BIN": str(fake_docker),
                    "FAKE_DOCKER_LOG": str(log_path),
                    "FAKE_DOCKER_STATE": json.dumps(state or healthy_state()),
                    "SPARKCLAW_FORCE_MODEL_RECREATE": "false",
                    "SPARKCLAW_MODEL_STARTUP_TIMEOUT_SECONDS": "17",
                }
            )
            env.update(extra_env or {})
            result = subprocess.run(
                ["bash", str(SCRIPT), lane],
                cwd=ROOT,
                env=env,
                check=check,
                capture_output=True,
                text=True,
            )
            calls = []
            if log_path.exists():
                calls = [json.loads(line) for line in log_path.read_text().splitlines()]
            return result, calls

    @staticmethod
    def group_stop(calls: list[list[str]]) -> list[str] | None:
        return next(
            (
                call
                for call in calls
                if "stop" in call and "sparkclaw-fast" in call
            ),
            None,
        )

    @staticmethod
    def up_call(calls: list[list[str]]) -> list[str]:
        return next(call for call in calls if "up" in call)

    def assert_product_group_recreated(self, calls: list[list[str]]) -> None:
        stop_call = self.group_stop(calls)
        self.assertIsNotNone(stop_call)
        for service in PRODUCT_SERVICES:
            self.assertIn(service, stop_call)
        up_call = self.up_call(calls)
        self.assertIn("--force-recreate", up_call)
        self.assertNotIn("--no-recreate", up_call)
        self.assertIn("--build", up_call)

    def test_healthy_product_group_is_retained(self) -> None:
        result, calls = self.run_script("single-fast")

        self.assertIn("retaining containers", result.stdout)
        self.assertIsNone(self.group_stop(calls))
        up_call = self.up_call(calls)
        self.assertNotIn("--force-recreate", up_call)
        self.assertIn("--no-recreate", up_call)
        self.assertIn("--build", up_call)
        deep_stop = next(call for call in calls if "stop" in call)
        self.assertIn("sparkclaw-deep", deep_stop)
        self.assertIn("docker/env/sparkclaw.asr.env", up_call)
        self.assertIn("docker/compose.asr.yaml", up_call)

    def test_missing_asr_recreates_the_whole_product_group(self) -> None:
        state = healthy_state()
        state["sparkclaw-asr"]["id"] = ""

        result, calls = self.run_script("single-fast", state=state)

        self.assertIn("sparkclaw-asr is absent", result.stdout)
        self.assert_product_group_recreated(calls)

    def test_missing_member_recreates_the_whole_product_group(self) -> None:
        state = healthy_state()
        state["sparkclaw-ocr"]["id"] = ""

        result, calls = self.run_script("single-fast", state=state)

        self.assertIn("sparkclaw-ocr is absent", result.stdout)
        self.assert_product_group_recreated(calls)

    def test_stopped_member_with_stale_healthy_status_recreates_group(self) -> None:
        state = healthy_state()
        state["sparkclaw-fast"]["status"] = "exited"
        state["sparkclaw-fast"]["health"] = "healthy"

        result, calls = self.run_script("single-fast", state=state)

        self.assertIn("sparkclaw-fast is exited", result.stdout)
        self.assert_product_group_recreated(calls)

    def test_unhealthy_member_recreates_group(self) -> None:
        state = healthy_state()
        state["sparkclaw-guard"]["health"] = "unhealthy"

        result, calls = self.run_script("single-fast", state=state)

        self.assertIn("sparkclaw-guard health is unhealthy", result.stdout)
        self.assert_product_group_recreated(calls)

    def test_configuration_drift_recreates_group(self) -> None:
        state = healthy_state()
        state["sparkclaw-embedding"]["actual_hash"] = "stale"

        result, calls = self.run_script("single-fast", state=state)

        self.assertIn("sparkclaw-embedding configuration drifted", result.stdout)
        self.assert_product_group_recreated(calls)

    def test_inspection_failure_preserves_the_selected_group(self) -> None:
        state = healthy_state()
        state["sparkclaw-guard"]["expected_hash"] = ""

        result, calls = self.run_script("single-fast", state=state, check=False)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("invalid configuration hash", result.stderr)
        self.assertIsNone(self.group_stop(calls))
        self.assertFalse(any("up" in call for call in calls))

    def test_explicit_force_recreates_a_healthy_group(self) -> None:
        result, calls = self.run_script(
            "single-fast",
            extra_env={"SPARKCLAW_FORCE_MODEL_RECREATE": "true"},
        )

        self.assertIn("requested by SPARKCLAW_FORCE_MODEL_RECREATE", result.stdout)
        self.assert_product_group_recreated(calls)

    def test_healthy_standalone_lane_is_retained(self) -> None:
        result, calls = self.run_script("guard")

        self.assertIn("retaining containers", result.stdout)
        self.assertFalse(any("stop" in call for call in calls))
        up_call = self.up_call(calls)
        self.assertNotIn("--force-recreate", up_call)
        self.assertIn("--no-recreate", up_call)
        self.assertIn("sparkclaw-guard", up_call)

    def test_unhealthy_standalone_lane_is_recreated(self) -> None:
        state = healthy_state()
        state["sparkclaw-guard"]["health"] = "starting"

        result, calls = self.run_script("guard", state=state)

        self.assertIn("sparkclaw-guard health is starting", result.stdout)
        stop_call = next(call for call in calls if "stop" in call)
        self.assertIn("sparkclaw-guard", stop_call)
        self.assertIn("--force-recreate", self.up_call(calls))
        self.assertNotIn("--no-recreate", self.up_call(calls))

    def test_invalid_force_setting_fails_before_docker_access(self) -> None:
        result, calls = self.run_script(
            "guard",
            extra_env={"SPARKCLAW_FORCE_MODEL_RECREATE": "sometimes"},
            check=False,
        )

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must be true or false", result.stderr)
        self.assertEqual(calls, [])


if __name__ == "__main__":
    unittest.main()
