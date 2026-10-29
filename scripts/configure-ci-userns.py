#!/usr/bin/env python3
"""Allow namespace creation only for explicit CI sandbox executable paths.

Ubuntu 24.04's unconfined-userns policy otherwise removes capabilities within
new namespaces. Per-program profiles retain the programs' existing unconfined
AppArmor mode while allowing their own bubblewrap/Chromium sandboxes to start.
See https://documentation.ubuntu.com/release-notes/24.04/#unprivileged-user-namespace-restrictions
"""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys


def profile_for(value):
    executable = Path(value).resolve(strict=True)
    # AppArmor attachment expressions must match one file, never a glob or an
    # injected rule. CI checkout and tool-cache paths use this character set.
    if not re.fullmatch(r"/[A-Za-z0-9._/-]+", str(executable)):
        raise ValueError("CI sandbox executable path contains unsupported characters")
    if not executable.is_file() or not os.access(executable, os.X_OK):
        raise ValueError("CI sandbox executable must be an executable regular file")
    name = "sparkclaw-ci-userns-" + hashlib.sha256(str(executable).encode()).hexdigest()[:20]
    source = (f'abi <abi/4.0>,\ninclude <tunables/global>\n'
              f'profile {name} "{executable}" flags=(unconfined) {{\n  userns,\n}}\n')
    return name, executable, source


def restrictions():
    paths = [Path('/proc/sys/kernel') / name for name in
             ['unprivileged_userns_clone', 'apparmor_restrict_unprivileged_userns']]
    return {p.name: p.read_text().strip() if p.exists() else None for p in paths}


def main(arguments):
    if sys.platform != 'linux' or os.geteuid() != 0:
        raise SystemExit('Run with sudo on the disposable Linux CI runner')
    if not arguments:
        raise SystemExit('Pass the actual bubblewrap/Electron executable paths')
    if Path('/sys/module/apparmor/parameters/enabled').read_text().strip() != 'Y':
        raise SystemExit('CI requires AppArmor to remain enabled')
    before = restrictions()
    # Validate every input before installing any profile.
    profiles = [profile_for(argument) for argument in arguments]
    for name, executable, source in profiles:
        filename = Path('/etc/apparmor.d') / name
        filename.write_text(source)
        filename.chmod(0o644)
        subprocess.run(['apparmor_parser', '--replace', '--skip-read-cache', str(filename)], check=True)
        loaded = Path('/sys/kernel/security/apparmor/profiles').read_text().splitlines()
        if not any(line.startswith(name + ' (') for line in loaded):
            raise RuntimeError('The CI namespace profile did not load')
        print(json.dumps({'profile': name, 'executable': str(executable), 'userns': True}))
    if restrictions() != before:
        raise RuntimeError('Global namespace policy changed during CI setup')
    print(json.dumps({'global_namespace_policy_unchanged': True, 'global_namespace_policy': before}))


if __name__ == '__main__':
    main(sys.argv[1:])
