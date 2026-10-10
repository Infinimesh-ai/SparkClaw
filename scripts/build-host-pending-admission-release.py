#!/usr/bin/env python3
"""Reproduce the pending Host admission lease fix; no installed state changes."""
from pathlib import Path
import runpy

if __name__ == '__main__':
    build = runpy.run_path(str(Path(__file__).with_name('build-workspace-mail-release.py')))['build']
    build(version='0.3.0-sparkclaw.20', base_version='0.3.0-sparkclaw.19',
          patch_name='host-pending-admission.patch')
