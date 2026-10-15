#!/usr/bin/env python3
"""Reproduce native attachment association and durable recovery; no installed state changes."""
from pathlib import Path
import runpy
if __name__ == '__main__':
    build = runpy.run_path(str(Path(__file__).with_name('build-workspace-mail-release.py')))['build']
    build(version='0.3.0-sparkclaw.16', base_version='0.3.0-sparkclaw.15', patch_name='native-mail-attachments.patch')
