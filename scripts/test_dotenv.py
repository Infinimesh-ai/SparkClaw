from __future__ import annotations

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
LIBRARY = ROOT / "scripts" / "lib" / "dotenv.sh"


class DotenvTest(unittest.TestCase):
    def run_bash(
        self,
        script: str,
        *,
        env_file: Path,
        extra_env: dict[str, str] | None = None,
    ) -> subprocess.CompletedProcess[str]:
        environment = os.environ.copy()
        environment.pop("SPARKCLAW_TEST_VALUE", None)
        environment.update(extra_env or {})
        return subprocess.run(
            ["bash", "-c", f'source "$1"\n{script}', "bash", str(LIBRARY), str(env_file)],
            cwd=ROOT,
            env=environment,
            check=False,
            capture_output=True,
            text=True,
        )

    def test_value_uses_last_definition_and_removes_matching_quotes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            env_file = Path(directory) / ".env"
            env_file.write_text(
                "SPARKCLAW_TEST_VALUE=first\n"
                "SPARKCLAW_TEST_VALUE='second value'\n",
                encoding="utf-8",
            )

            result = self.run_bash(
                'sparkclaw_dotenv_value "$2" SPARKCLAW_TEST_VALUE',
                env_file=env_file,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout, "second value")

    def test_value_does_not_evaluate_shell_content(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            env_file = Path(directory) / ".env"
            env_file.write_text(
                "SPARKCLAW_TEST_VALUE=$(printf unsafe)\n",
                encoding="utf-8",
            )

            result = self.run_bash(
                'sparkclaw_dotenv_value "$2" SPARKCLAW_TEST_VALUE',
                env_file=env_file,
            )

            self.assertEqual(result.stdout, "$(printf unsafe)")

    def test_resolution_prefers_process_environment_and_preserves_empty(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            env_file = Path(directory) / ".env"
            env_file.write_text("SPARKCLAW_TEST_VALUE=file\n", encoding="utf-8")

            process = self.run_bash(
                'sparkclaw_resolve_env_value "$2" SPARKCLAW_TEST_VALUE default',
                env_file=env_file,
                extra_env={"SPARKCLAW_TEST_VALUE": "process"},
            )
            empty = self.run_bash(
                'sparkclaw_resolve_env_value "$2" SPARKCLAW_TEST_VALUE default',
                env_file=env_file,
                extra_env={"SPARKCLAW_TEST_VALUE": ""},
            )

            self.assertEqual(process.stdout, "process")
            self.assertEqual(empty.stdout, "")

    def test_resolution_distinguishes_missing_from_empty_file_value(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            env_file = Path(directory) / ".env"
            env_file.write_text("SPARKCLAW_TEST_VALUE=\n", encoding="utf-8")

            empty = self.run_bash(
                'sparkclaw_resolve_env_value "$2" SPARKCLAW_TEST_VALUE default',
                env_file=env_file,
            )
            missing = self.run_bash(
                'sparkclaw_resolve_env_value "$2" SPARKCLAW_OTHER_VALUE default',
                env_file=env_file,
            )

            self.assertEqual(empty.stdout, "")
            self.assertEqual(missing.stdout, "default")

    def test_merge_missing_adds_template_defaults_without_replacing_existing_values(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            temp_path = Path(directory)
            template = temp_path / "template.env"
            env_file = temp_path / ".env"
            output = temp_path / "merged.env"
            template.write_text(
                "KEEP=template\n"
                "EMPTY=template\n"
                "NEW=added\n"
                "SECOND=new\n",
                encoding="utf-8",
            )
            env_file.write_text(
                "# local values\n"
                "KEEP=custom\n"
                "EMPTY=\n"
                "EXTRA=local\n",
                encoding="utf-8",
            )

            result = subprocess.run(
                [
                    "bash",
                    "-c",
                    'source "$1"\nsparkclaw_dotenv_merge_missing "$2" "$3" "$4"',
                    "bash",
                    str(LIBRARY),
                    str(template),
                    str(env_file),
                    str(output),
                ],
                cwd=ROOT,
                check=False,
                capture_output=True,
                text=True,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout, "2")
            self.assertEqual(
                output.read_text(encoding="utf-8"),
                "# local values\n"
                "KEEP=custom\n"
                "EMPTY=\n"
                "EXTRA=local\n"
                "NEW=added\n"
                "SECOND=new\n",
            )

            second_output = temp_path / "merged-again.env"
            second = subprocess.run(
                [
                    "bash",
                    "-c",
                    'source "$1"\nsparkclaw_dotenv_merge_missing "$2" "$3" "$4"',
                    "bash",
                    str(LIBRARY),
                    str(template),
                    str(output),
                    str(second_output),
                ],
                cwd=ROOT,
                check=False,
                capture_output=True,
                text=True,
            )

            self.assertEqual(second.returncode, 0, second.stderr)
            self.assertEqual(second.stdout, "0")
            self.assertEqual(
                second_output.read_text(encoding="utf-8"),
                output.read_text(encoding="utf-8"),
            )

    def test_tcp_port_validation(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            env_file = Path(directory) / ".env"
            env_file.write_text("", encoding="utf-8")
            for value in ("1", "18790", "65535"):
                with self.subTest(value=value):
                    result = self.run_bash(
                        f"sparkclaw_tcp_port_valid {value}",
                        env_file=env_file,
                    )
                    self.assertEqual(result.returncode, 0, result.stderr)
            for value in ("", "0", "080", "65536", "abc", "18790/tcp"):
                with self.subTest(value=value):
                    result = self.run_bash(
                        f"sparkclaw_tcp_port_valid '{value}'",
                        env_file=env_file,
                    )
                    self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
