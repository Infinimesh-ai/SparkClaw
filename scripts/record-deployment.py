#!/usr/bin/env python3
"""Record the first successful deployment, without resetting existing instances."""
import datetime
import json
import os
from pathlib import Path
import sys
import tempfile


def record(workspace: Path) -> None:
    workspace.mkdir(parents=True, exist_ok=True)
    destination = workspace / '.sparkclaw-deployment.json'
    if destination.exists():
        return
    fd, temporary = tempfile.mkstemp(prefix='.deployment-', dir=workspace)
    try:
        with os.fdopen(fd, 'w') as stream:
            json.dump({'started_at': datetime.datetime.now(datetime.timezone.utc).isoformat()}, stream)
            stream.write('\n')
            stream.flush()
            os.fchmod(stream.fileno(), 0o644)  # Non-secret; also readable by container UID.
            os.fsync(stream.fileno())
        try:
            os.link(temporary, destination)  # Atomic publication, never overwrite.
        except FileExistsError:
            pass
        directory = os.open(workspace, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        os.unlink(temporary)


if __name__ == '__main__':
    record(Path(sys.argv[1]))
