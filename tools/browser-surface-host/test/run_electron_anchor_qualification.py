from __future__ import annotations

import argparse
import os
from pathlib import Path
import select
import shutil
import signal
import subprocess
import sys
import tempfile
import time


ROOT = Path(__file__).resolve().parents[1]
TESTS = {
    "electron-anchor": Path(__file__).resolve().with_name("test_electron_anchor.py"),
    "synthetic": Path(__file__).resolve().with_name("test_surface_host.py"),
}


def stop_group(process: subprocess.Popen[bytes] | None) -> None:
    if process is None or process.poll() is not None:
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    try:
        process.wait(timeout=3)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait(timeout=3)


def start_xvfb(xvfb: str) -> tuple[subprocess.Popen[bytes], str]:
    read_fd, write_fd = os.pipe()
    try:
        process = subprocess.Popen(
            [
                xvfb,
                "-displayfd", str(write_fd),
                "-screen", "0", "1280x800x24",
                "-nolisten", "tcp",
                "-noreset",
                "-ac",
            ],
            pass_fds=(write_fd,),
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            start_new_session=True,
        )
    finally:
        os.close(write_fd)
    ready, _, _ = select.select([read_fd], [], [], 5)
    if not ready:
        stop_group(process)
        stderr = process.stderr.read().decode("utf-8", "replace") if process.stderr else ""
        raise RuntimeError(f"Xvfb did not allocate a display: {stderr}")
    try:
        display_number = os.read(read_fd, 32).decode("ascii").strip()
    finally:
        os.close(read_fd)
    if not display_number.isdigit():
        stop_group(process)
        raise RuntimeError(f"Xvfb returned an invalid display: {display_number!r}")
    display = f":{display_number}"
    environment = os.environ.copy()
    environment["DISPLAY"] = display
    environment.pop("XAUTHORITY", None)
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        probe = subprocess.run(
            ["xprop", "-root", "_SPARKCLAW_DISPOSABLE_X11_PROBE"],
            env=environment,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )
        if probe.returncode == 0:
            return process, display
        if process.poll() is not None:
            break
        time.sleep(0.05)
    stderr = process.stderr.read().decode("utf-8", "replace") if process.stderr else ""
    stop_group(process)
    raise RuntimeError(f"Xvfb display {display} did not become ready: {stderr}")


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Run surface-host tests on a process-owned disposable Xvfb display"
    )
    parser.add_argument("--suite", choices=TESTS, default="electron-anchor")
    parser.add_argument("--electron", type=Path)
    arguments = parser.parse_args()
    if arguments.suite == "electron-anchor" and arguments.electron is None:
        parser.error("--electron is required for the electron-anchor suite")
    electron = arguments.electron.resolve(strict=True) if arguments.electron else None
    xvfb = shutil.which("Xvfb")
    if xvfb is None:
        raise SystemExit("Xvfb is required; the active desktop will never be used as a fallback")
    for command in ("xprop", "xwininfo"):
        if shutil.which(command) is None:
            raise SystemExit(f"{command} is required")

    original_display = os.environ.get("DISPLAY", "")
    xvfb_process: subprocess.Popen[bytes] | None = None
    with tempfile.TemporaryDirectory(prefix="sparkclaw-electron-anchor-runner.") as directory:
        temporary_root = Path(directory)
        try:
            xvfb_process, display = start_xvfb(xvfb)
            if display == original_display:
                raise RuntimeError("Xvfb unexpectedly selected the active desktop display")
            environment = os.environ.copy()
            environment.pop("XAUTHORITY", None)
            environment.update({
                "DISPLAY": display,
                "SPARKCLAW_SURFACE_TEST_ISOLATED": "1",
                "SPARKCLAW_SURFACE_TEST_XSERVER_PID": str(xvfb_process.pid),
                "SPARKCLAW_SURFACE_TEST_XSERVER_DISPLAY": display,
                "SPARKCLAW_SURFACE_TEST_ORIGINAL_DISPLAY": original_display,
            })
            if electron is not None:
                environment.update({
                    "SPARKCLAW_ELECTRON_ANCHOR_ELECTRON": str(electron),
                    "SPARKCLAW_ELECTRON_ANCHOR_RUN_ROOT": str(temporary_root),
                })
            completed = subprocess.run(
                [sys.executable, "-W", "error::ResourceWarning", str(TESTS[arguments.suite]), "-v"],
                cwd=ROOT,
                env=environment,
                check=False,
            )
            return completed.returncode
        finally:
            stop_group(xvfb_process)


if __name__ == "__main__":
    raise SystemExit(main())
