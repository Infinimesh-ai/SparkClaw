#!/usr/bin/env python3
"""Record successful deployment boundaries without mistaking .gitkeep for state."""
import argparse
import datetime
import json
import os
from pathlib import Path
import tempfile


def atomic_json(destination: Path, value: dict, *, replace=False) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix='.deployment-', dir=destination.parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            json.dump(value, stream)
            stream.write('\n')
            stream.flush()
            os.fchmod(stream.fileno(), 0o644)
            os.fsync(stream.fileno())
        if replace:
            os.replace(temporary, destination)
        else:
            try:
                os.link(temporary, destination)
            except FileExistsError:
                pass
        directory = os.open(destination.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        Path(temporary).unlink(missing_ok=True)


def state(workspace: Path) -> dict:
    path = workspace / '.sparkclaw-deployment-state.json'
    if path.stat().st_size > 4096:
        raise ValueError('deployment state is too large')
    value = json.loads(path.read_text())
    if value.get('version') != 1 or value.get('mode') not in ('fresh', 'legacy', 'recorded') or value.get('phase') not in ('pending', 'complete'):
        raise ValueError('invalid deployment state; do not reset its boundary')
    return value


def begin(workspace: Path, legacy: bool) -> None:
    # Persist BEFORE starting services. A failed fresh attempt may create a DB,
    # containers or credentials; the retry must retain its original fresh status.
    mode = 'recorded' if (workspace / '.sparkclaw-deployment.json').exists() else 'legacy' if legacy else 'fresh'
    atomic_json(workspace / '.sparkclaw-deployment-state.json', {'version': 1, 'mode': mode, 'phase': 'pending'})
    state(workspace)  # Validate an existing marker, never silently overwrite it.


def complete(workspace: Path, now=None) -> None:
    value = state(workspace)  # No begin marker => do not invent a successful install.
    if value['phase'] == 'complete':
        return
    if value['mode'] == 'fresh':
        timestamp = now or datetime.datetime.now(datetime.timezone.utc)
        atomic_json(workspace / '.sparkclaw-deployment.json', {'started_at': timestamp.isoformat()})
    # Legacy installations deliberately retain the first-enable fallback.
    atomic_json(workspace / '.sparkclaw-deployment-state.json', {**value, 'phase': 'complete'}, replace=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('operation', choices=('begin', 'complete'))
    parser.add_argument('workspace', type=Path)
    parser.add_argument('--legacy', action='store_true')
    args = parser.parse_args()
    if args.operation == 'begin':
        begin(args.workspace, args.legacy)
    else:
        complete(args.workspace)
