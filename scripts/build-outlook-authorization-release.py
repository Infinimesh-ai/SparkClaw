#!/usr/bin/env python3
"""Reproduce Outlook native header authorization without changing persistent state."""
from pathlib import Path
import runpy

if __name__ == '__main__':
    build = runpy.run_path(str(Path(__file__).with_name('build-workspace-mail-release.py')))['build']
    build(version='0.3.0-sparkclaw.23', base_version='0.3.0-sparkclaw.22',
          patch_name='outlook-original-authorization.patch', build_userscripts=True)
