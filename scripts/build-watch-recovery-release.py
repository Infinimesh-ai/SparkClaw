#!/usr/bin/env python3
"""Reproduce expired read-only watch admission recovery; preserve installed state."""
from pathlib import Path
import runpy

if __name__ == '__main__':
    build = runpy.run_path(str(Path(__file__).with_name('build-workspace-mail-release.py')))['build']
    build(version='0.3.0-sparkclaw.22', base_version='0.3.0-sparkclaw.21',
          patch_name='watch-admission-recovery.patch')
