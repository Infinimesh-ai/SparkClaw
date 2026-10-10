#!/usr/bin/env python3
"""Exercise Controller kill ordering with disposable systemd --user units.

Linux with a running user manager is required. No browser, application release,
credentials or existing unit is accessed. Only uniquely named fixture units and
their private temporary files are created and removed. Run beside the matching
setup-browser-controller.sh; successful output is a sanitized JSON receipt.
"""
import argparse
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time
import uuid


def record(root, name, **fields):
    temporary = root / (name + '.tmp')
    temporary.write_text(json.dumps(fields), encoding='utf-8')
    temporary.replace(root / (name + '.json'))


def wait_for(predicate, seconds=10):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = predicate()
        if value:
            return value
        time.sleep(0.01)
    raise AssertionError('Fixture synchronization deadline exceeded')


def command(root, value):
    with socket.socket(socket.AF_UNIX) as connection:
        connection.settimeout(1)
        connection.connect(str(root / 'child.sock'))
        connection.sendall(value.encode())
        return connection.recv(1024).decode()


def child(root):
    def terminate(signum, frame):
        record(root, 'child-sigterm')
        raise SystemExit(0)

    signal.signal(signal.SIGTERM, terminate)
    with socket.socket(socket.AF_UNIX) as listener:
        listener.bind(str(root / 'child.sock'))
        listener.listen()
        record(root, 'child-ready', pid=os.getpid())
        while True:
            connection, _ = listener.accept()
            with connection:
                value = connection.recv(1024).decode()
                if value == 'close-owned-resource':
                    record(root, 'owned-resource-closed')
                    connection.sendall(b'owned-resource-closed')
                    return
                assert value == 'ping'
                connection.sendall(b'alive')


def worker(root, behavior):
    stopped = threading.Event()

    def terminate(signum, frame):
        record(root, 'main-sigterm')
        stopped.set()

    signal.signal(signal.SIGTERM, terminate)
    daemon = subprocess.Popen([sys.executable, __file__, '--child', str(root)],
                              start_new_session=True)
    wait_for(lambda: (root / 'child-ready.json').exists())
    assert command(root, 'ping') == 'alive'
    record(root, 'main-ready', pid=os.getpid(), child_pid=daemon.pid)
    stopped.wait()
    if behavior == 'fail':
        record(root, 'main-failed')
        return 7
    if behavior == 'timeout':
        while True:
            signal.pause()
    # Model the asynchronous drain before final owned-resource cleanup. The
    # orchestrator releases this explicit barrier only after observing delivery
    # of systemd's first signal; no timing sleep determines signal ordering.
    wait_for(lambda: (root / 'allow-cleanup.json').exists())
    try:
        assert command(root, 'close-owned-resource') == 'owned-resource-closed'
        daemon.wait(timeout=2)
        assert daemon.returncode == 0
    except (OSError, AssertionError, subprocess.TimeoutExpired):
        record(root, 'cleanup-failed')
        return 1
    record(root, 'cleanup-completed')
    return 0


def systemctl(*arguments, check=True):
    return subprocess.run(['systemctl', '--user', *arguments], check=check,
                          capture_output=True, text=True, timeout=15)


def cgroup_empty(group):
    path = Path('/sys/fs/cgroup') / group.lstrip('/') / 'cgroup.procs'
    return not path.exists() or not path.read_text().strip()


def run_case(directory, mode, behavior):
    root = directory / (mode + '-' + behavior)
    root.mkdir(mode=0o700)
    unit = 'sparkclaw-stop-qualification-' + uuid.uuid4().hex + '.service'
    args = ['systemd-run', '--user', '--quiet', '--wait', '--unit=' + unit,
            '--property=Type=exec', '--property=KillMode=' + mode,
            '--property=TimeoutStopSec=3', '--property=SendSIGKILL=yes',
            '--property=KillSignal=SIGTERM', '--property=FinalKillSignal=SIGKILL',
            '--property=UMask=0077', sys.executable, str(Path(__file__).resolve()),
            '--worker', str(root), '--behavior', behavior]
    launched = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        wait_for(lambda: (root / 'main-ready.json').exists())
        ready = json.loads((root / 'main-ready.json').read_text())
        group = systemctl('show', unit, '--property=ControlGroup', '--value').stdout.strip()
        assert group and all(
            Path(f'/proc/{pid}/cgroup').read_text().strip().endswith(':' + group)
            for pid in [ready['pid'], ready['child_pid']])
        systemctl('stop', '--no-block', unit)
        wait_for(lambda: (root / 'main-sigterm.json').exists())
        if behavior == 'normal':
            if mode == 'control-group':
                wait_for(lambda: (root / 'child-sigterm.json').exists())
                # Wait for the process to leave its socket, not a guessed delay.
                wait_for(lambda: 'Z' == Path(f'/proc/{ready["child_pid"]}/stat').read_text().split()[2]
                         if Path(f'/proc/{ready["child_pid"]}/stat').exists() else True)
            else:
                assert command(root, 'ping') == 'alive'
                assert not (root / 'child-sigterm.json').exists()
            record(root, 'allow-cleanup')
        stdout, stderr = launched.communicate(timeout=12)
        state = systemctl('show', unit, '--property=Result', '--property=ExecMainCode',
                          '--property=ExecMainStatus', check=False).stdout
        journal = subprocess.run(['journalctl', '--user', '-u', unit, '--no-pager', '-o', 'cat'],
                                 check=True, capture_output=True, text=True, timeout=10).stdout
        wait_for(lambda: cgroup_empty(group))
        wait_for(lambda: all(not Path(f'/proc/{pid}').exists() for pid in [ready['pid'], ready['child_pid']]))
        events = sorted(p.stem for p in root.glob('*.json'))
        if mode == 'control-group':
            assert launched.returncode == 1 and 'cleanup-failed' in events
            assert 'child-sigterm' in events and 'owned-resource-closed' not in events
            assert 'Result=exit-code' in state
        elif behavior == 'normal':
            assert launched.returncode == 0 and 'cleanup-completed' in events
            assert 'owned-resource-closed' in events and 'child-sigterm' not in events
            assert 'SIGKILL' not in journal
        elif behavior == 'fail':
            assert launched.returncode == 7 and 'main-failed' in events
            assert 'child-sigterm' not in events and 'owned-resource-closed' not in events
            assert 'Result=exit-code' in state and 'SIGKILL' in journal
        else:
            assert launched.returncode != 0 and 'Result=timeout' in state
            assert 'child-sigterm' not in events and 'owned-resource-closed' not in events
            assert 'ExecMainStatus=9' in state and 'SIGKILL' in journal
        return {'mode': mode, 'behavior': behavior, 'events': events,
                'runner_exit': launched.returncode,
                'result': dict(line.split('=', 1) for line in state.splitlines() if '=' in line),
                'final_sigkill_observed': 'SIGKILL' in journal,
                'owned_pids_gone': True, 'cgroup_empty': True}
    except Exception as error:
        raise AssertionError(f'{mode}/{behavior} failed: {error}') from error
    finally:
        # Exact fixture name only. Never stop/restart an existing application.
        systemctl('stop', unit, check=False)
        systemctl('reset-failed', unit, check=False)
        if launched.poll() is None:
            launched.communicate(timeout=12)


def qualify():
    assert sys.platform == 'linux', 'Requires Linux and a running systemd user manager'
    setup = Path(__file__).with_name('setup-browser-controller.sh').read_text()
    controller = setup.split('cat >"$unit_path" <<EOF\n', 1)[1].split('\nEOF', 1)[0]
    fields = dict(line.split('=', 1) for line in controller.splitlines()
                  if '=' in line and not line.startswith('#'))
    assert fields['KillMode'] == 'mixed' and fields['TimeoutStopSec'] == '60'
    assert fields['SendSIGKILL'] == 'yes'
    version = subprocess.check_output(['systemd-run', '--version'], text=True).splitlines()[0]
    with tempfile.TemporaryDirectory(prefix='sc-stop-') as temporary:
        cases = [run_case(Path(temporary), mode, behavior) for mode, behavior in
                 [('control-group', 'normal'), ('mixed', 'normal'), ('mixed', 'fail'), ('mixed', 'timeout')]]
    print(json.dumps({'schema_version': 1, 'systemd': version,
                      'production': {key: fields[key] for key in ['KillMode', 'TimeoutStopSec', 'SendSIGKILL']},
                      'fixture_timeout_seconds': 3, 'browser_access': False,
                      'existing_units_modified': False, 'cases': cases}, indent=2))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--worker', type=Path)
    parser.add_argument('--child', type=Path)
    parser.add_argument('--behavior', choices=['normal', 'fail', 'timeout'], default='normal')
    options = parser.parse_args()
    if options.child:
        child(options.child)
    elif options.worker:
        sys.exit(worker(options.worker, options.behavior))
    else:
        qualify()
