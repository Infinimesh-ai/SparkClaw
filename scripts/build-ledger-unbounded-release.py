#!/usr/bin/env python3
"""Reproduce the unlimited task-history default; no installed state changes."""
from pathlib import Path
import runpy

if __name__ == '__main__':
    build = runpy.run_path(str(Path(__file__).with_name('build-workspace-mail-release.py')))['build']
    build(version='0.3.0-sparkclaw.21', base_version='0.3.0-sparkclaw.20',
          patch_name='ledger-unbounded-history.patch')
