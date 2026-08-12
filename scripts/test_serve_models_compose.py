#!/usr/bin/env python3

import json
import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "serve_models_compose.sh"


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

raise SystemExit(0)
"""


class ServeModelsComposeTest(unittest.TestCase):
    def run_script(self, lane):
        with tempfile.TemporaryDirectory() as temp_dir:
            temp_path = Path(temp_dir)
            fake_docker = temp_path / "docker"
            fake_docker.write_text(textwrap.dedent(FAKE_DOCKER), encoding="utf-8")
            fake_docker.chmod(0o755)
            log_path = temp_path / "docker.jsonl"
            env = os.environ.copy()
            env.update(
                {
                    "DOCKER_BIN": str(fake_docker),
                    "FAKE_DOCKER_LOG": str(log_path),
                    "SPARKCLAW_MODEL_STARTUP_TIMEOUT_SECONDS": "17",
                }
            )
            result = subprocess.run(
                ["bash", str(SCRIPT), lane],
                cwd=ROOT,
                env=env,
                check=True,
                capture_output=True,
                text=True,
            )
            calls = [json.loads(line) for line in log_path.read_text().splitlines()]
            return result, calls

    def test_product_start_always_recreates_the_whole_group(self):
        result, calls = self.run_script("single-fast")

        self.assertIn("fresh runtime caches", result.stdout)
        up_call = next(call for call in calls if "up" in call)
        self.assertIn("--force-recreate", up_call)
        stop_call = next(
            call
            for call in calls
            if "stop" in call and "sparkclaw-fast" in call
        )
        for service in (
            "sparkclaw-fast",
            "sparkclaw-embedding",
            "sparkclaw-guard",
            "sparkclaw-ocr",
        ):
            self.assertIn(service, stop_call)

    def test_standalone_model_is_always_recreated(self):
        _, calls = self.run_script("guard")

        up_call = next(call for call in calls if "up" in call)
        self.assertIn("--force-recreate", up_call)
        self.assertIn("sparkclaw-guard", up_call)
        stop_call = next(
            call
            for call in calls if "stop" in call and "sparkclaw-guard" in call
        )
        self.assertIn("sparkclaw-guard", stop_call)


if __name__ == "__main__":
    unittest.main()
