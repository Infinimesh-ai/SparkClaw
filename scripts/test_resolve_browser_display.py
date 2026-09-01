#!/usr/bin/env python3

from __future__ import annotations

import os
from pathlib import Path
import socket
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "resolve-browser-display.sh"

FAKE_XSET = r"""#!/usr/bin/env bash
set -euo pipefail

case ",${DISPLAY_TEST_USABLE:-}," in
  *",${DISPLAY},"*) ;;
  *) exit 1 ;;
esac
if [[ -n "${DISPLAY_TEST_XAUTHORITY:-}" &&
  "${XAUTHORITY:-}" != "${DISPLAY_TEST_XAUTHORITY}" ]]; then
  exit 1
fi
"""


class BrowserDisplayResolverTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.socket_dir = self.root / "X11"
        self.socket_dir.mkdir()
        self.bin_dir = self.root / "bin"
        self.bin_dir.mkdir()
        xset = self.bin_dir / "xset"
        xset.write_text(textwrap.dedent(FAKE_XSET), encoding="utf-8")
        xset.chmod(0o755)
        self.xauthority = self.root / "Xauthority"
        self.xauthority.write_text("test-cookie\n", encoding="utf-8")
        self.sockets: list[socket.socket] = []
        self.addCleanup(self.close_sockets)

    def close_sockets(self) -> None:
        for server in self.sockets:
            server.close()

    def add_socket(self, display_number: int) -> None:
        server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        server.bind(str(self.socket_dir / f"X{display_number}"))
        server.listen(1)
        self.sockets.append(server)

    def run_resolver(
        self,
        usable_displays: str,
        *,
        display: str = "",
        xauthority: Path | None = None,
        runtime_dir: Path | None = None,
        expected_xauthority: Path | None = None,
    ) -> subprocess.CompletedProcess[str]:
        environment = os.environ.copy()
        environment.update(
            {
                "PATH": f"{self.bin_dir}:{environment['PATH']}",
                "DISPLAY_TEST_USABLE": usable_displays,
                "SPARKCLAW_BROWSER_DISPLAY": display,
            }
        )
        environment.pop("DISPLAY", None)
        environment.pop("XAUTHORITY", None)
        if xauthority is None:
            environment.pop("SPARKCLAW_BROWSER_XAUTHORITY", None)
        else:
            environment["SPARKCLAW_BROWSER_XAUTHORITY"] = str(xauthority)
        if runtime_dir is not None:
            environment["XDG_RUNTIME_DIR"] = str(runtime_dir)
        if expected_xauthority is not None:
            environment["DISPLAY_TEST_XAUTHORITY"] = str(expected_xauthority)
        else:
            environment.pop("DISPLAY_TEST_XAUTHORITY", None)

        return subprocess.run(
            [
                "bash",
                "-c",
                'source "$1"; resolve_browser_display "$2"',
                "resolver-test",
                str(SCRIPT),
                str(self.socket_dir),
            ],
            cwd=ROOT,
            env=environment,
            check=False,
            capture_output=True,
            text=True,
        )

    def test_skips_initialization_socket_and_selects_usable_display(self) -> None:
        self.add_socket(0)
        self.add_socket(1)

        result = self.run_resolver(":0", xauthority=self.xauthority)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines(), [":0", str(self.xauthority)])

    def test_selects_lowest_number_when_multiple_displays_are_usable(self) -> None:
        self.add_socket(7)
        self.add_socket(3)

        result = self.run_resolver(":3,:7", xauthority=self.xauthority)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines(), [":3", str(self.xauthority)])

    def test_explicit_display_overrides_automatic_order(self) -> None:
        self.add_socket(3)
        self.add_socket(7)

        result = self.run_resolver(
            ":3,:7", display=":7", xauthority=self.xauthority
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines(), [":7", str(self.xauthority)])

    def test_tries_authority_candidates_until_one_opens_display(self) -> None:
        self.add_socket(0)
        runtime_dir = self.root / "runtime"
        (runtime_dir / "gdm").mkdir(parents=True)
        (runtime_dir / "gdm" / "Xauthority").write_text(
            "wrong-cookie\n", encoding="utf-8"
        )
        mutter_authority = runtime_dir / ".mutter-Xwaylandauth.TEST"
        mutter_authority.write_text("right-cookie\n", encoding="utf-8")

        result = self.run_resolver(
            ":0",
            runtime_dir=runtime_dir,
            expected_xauthority=mutter_authority,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines(), [":0", str(mutter_authority)])

    def test_fails_when_no_socket_accepts_available_authority(self) -> None:
        self.add_socket(0)
        self.add_socket(1)

        result = self.run_resolver("", xauthority=self.xauthority)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("no usable local X11/XWayland display", result.stderr)


if __name__ == "__main__":
    unittest.main()
