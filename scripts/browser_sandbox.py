#!/usr/bin/env python3
"""Allow user namespaces only for the pinned, root-owned SparkClaw Chrome.

Ubuntu ships the same named, unconfined profile for /opt/google/chrome/chrome.
This preserves that baseline while making its userns exception path-specific:
https://ubuntu.com/blog/ubuntu-23-10-restricted-unprivileged-user-namespaces
https://chromium.googlesource.com/chromium/src/+/main/docs/security/apparmor-userns-restrictions.md
"""
import argparse
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
RESTRICT = Path('/proc/sys/kernel/apparmor_restrict_unprivileged_userns')
PROFILES = Path('/sys/kernel/security/apparmor/profiles')
POLICY_DIR = Path('/etc/apparmor.d')
MARKER = '# Managed by SparkClaw: exact pinned root-owned Chrome userns exception.\n'


def root_owned_path(path, directory=False):
    """Reject symlinks and writable/non-root ancestors before trusting code/policy."""
    path = Path(path)
    if not path.is_absolute():
        raise ValueError('sandbox path must be absolute')
    for index, item in enumerate((path, *path.parents)):
        info = item.lstat()
        if stat.S_ISLNK(info.st_mode):
            raise ValueError('sandbox path cannot contain a symlink')
        want_directory = directory or index > 0
        if not (stat.S_ISDIR(info.st_mode) if want_directory else stat.S_ISREG(info.st_mode)):
            raise ValueError('sandbox path has an unexpected file type')
        if info.st_uid != 0 or info.st_mode & 0o022:
            raise ValueError('sandbox path must be root-owned and not writable by other users')
    return path


def pin():
    manifest = json.loads((ROOT / 'configs/host-browser-artifacts.json').read_text())
    version = manifest['version']
    if not re.fullmatch(r'[0-9]+(?:\.[0-9]+){3}', version):
        raise ValueError('invalid pinned browser version')
    machine = os.uname().machine
    architecture = {'x86_64': 'x86_64', 'amd64': 'x86_64', 'aarch64': 'arm64', 'arm64': 'arm64'}.get(machine)
    if architecture is None:
        raise ValueError('unsupported browser sandbox architecture')
    artifact = manifest['artifacts'][architecture]
    if not re.fullmatch(r'[0-9a-f]{64}', artifact['sha256']):
        raise ValueError('invalid pinned browser checksum')
    return version, architecture, artifact['sha256']


def guarded():
    try:
        value = RESTRICT.read_text().strip()
    except FileNotFoundError:
        return None
    if value not in ('0', '1'):
        raise ValueError('unrecognized AppArmor namespace restriction')
    return value


def profile(version, abi):
    if not re.fullmatch(r'[0-9]+(?:\.[0-9]+){3}', version) or abi not in ('4.0', '5.0'):
        raise ValueError('invalid sandbox profile input')
    name = 'sparkclaw-chromium-' + version
    executable = Path('/opt/sparkclaw') / ('chromium-' + version) / 'chrome'
    text = (MARKER + 'abi <abi/' + abi + '>,\ninclude <tunables/global>\n\n'
            + 'profile ' + name + ' "' + str(executable) + '" flags=(unconfined) {\n'
            + '  userns,\n  @{exec_path} mr,\n}\n')
    return name, executable, text


def expected_profile():
    version, architecture, checksum = pin()
    abi = next((value for value in ('5.0', '4.0') if (POLICY_DIR / 'abi' / value).is_file()), None)
    if abi is None:
        raise ValueError('AppArmor user namespace ABI is unavailable')
    name, executable, text = profile(version, abi)
    root_owned_path(executable)
    if not executable.stat().st_mode & 0o111:
        raise ValueError('pinned Chrome is not executable')
    installed = root_owned_path(executable.parent / 'sparkclaw-manifest.json')
    material = json.loads(installed.read_text())
    if (material.get('artifactVersion'), material.get('architecture'), material.get('archiveSHA256')) != (version, architecture, checksum):
        raise ValueError('browser sandbox requires the verified pinned installation')
    root_owned_path(POLICY_DIR, directory=True)
    # The earlier host repair used this unversioned file. Do not create a
    # second attachment or silently take ownership of an operator's policy.
    legacy = POLICY_DIR / 'sparkclaw-chromium'
    if legacy.exists() or legacy.is_symlink():
        root_owned_path(legacy)
        if str(executable) in legacy.read_text():
            raise ValueError('existing legacy Chrome AppArmor attachment needs operator reconciliation')
    return name, executable, POLICY_DIR / name, text


def verify_loaded(name):
    if name + ' (unconfined)' not in PROFILES.read_text().splitlines():
        raise ValueError('pinned Chrome AppArmor namespace profile is not loaded')


def verify_policy(path, text):
    root_owned_path(path)
    if stat.S_IMODE(path.stat().st_mode) != 0o644 or path.read_text() != text:
        raise ValueError('pinned Chrome AppArmor namespace profile is stale or unsafe')


def configure(check=False):
    before = guarded()
    if before != '1':
        return {'state': 'not-required', 'global_restriction': before}
    if os.geteuid() != 0:
        raise ValueError('checking/installing the loaded AppArmor profile requires root')
    name, executable, path, text = expected_profile()
    if check:
        verify_policy(path, text)
    else:
        if path.exists() or path.is_symlink():
            root_owned_path(path)
            if not path.read_text().startswith(MARKER):
                raise ValueError('refusing to replace an unmanaged AppArmor profile')
        temporary = None
        try:
            with tempfile.NamedTemporaryFile(mode='w', prefix='.' + name + '.', dir=POLICY_DIR, delete=False) as output:
                temporary = Path(output.name)
                output.write(text)
                output.flush()
                os.fsync(output.fileno())
                os.fchmod(output.fileno(), 0o644)
            # Parse before publication; only this profile is replaced/loaded.
            subprocess.run(['apparmor_parser', '-Q', '-T', str(temporary)], check=True)
            temporary.replace(path)
            subprocess.run(['apparmor_parser', '-r', '-W', str(path)], check=True)
        finally:
            if temporary is not None:
                temporary.unlink(missing_ok=True)
        verify_policy(path, text)
    verify_loaded(name)
    if guarded() != before:
        raise ValueError('global AppArmor namespace restriction changed during setup')
    return {'state': 'verified' if check else 'installed', 'profile': name,
            'executable': str(executable), 'global_restriction': before}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    print(json.dumps(configure(args.check), sort_keys=True))


if __name__ == '__main__':
    main()
