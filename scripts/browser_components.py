#!/usr/bin/env python3
"""Pinned product browser components and Tampermonkey native provisioning."""
import argparse
import base64
import decimal
import hashlib
import io
import json
import math
import os
from pathlib import Path
import shutil
import struct
import tempfile
import time
import urllib.request
import uuid
import zipfile

ROOT = Path(__file__).resolve().parents[1]
SYSTEM = Path('/opt/sparkclaw/browser-components')
POLICY = Path('/etc/chromium/policies/managed/sparkclaw-userscripts.json')


def digest(data):
    return hashlib.sha256(data).hexdigest()


def provisioning_hash(value):
    # Tampermonkey 5.5.0 No()/Do(): sorted UTF-16 object keys, values only.
    if isinstance(value, (dict, list)):
        values = [value[k] for k in sorted(value, key=lambda k: k.encode('utf-16-be'))] if isinstance(value, dict) else value
        text = ''.join(provisioning_hash(v) for v in values)
    else:
        if value is None:
            text = 'object:null'
        elif isinstance(value, bool):
            text = 'boolean:' + str(value).lower()
        elif isinstance(value, str):
            text = 'string:' + value
        elif isinstance(value, (int, float)) and math.isfinite(value):
            if value == 0:
                number = '0'
            elif 1e-6 <= abs(value) < 1e21:
                number = format(decimal.Decimal(str(value)), 'f')
                if '.' in number:
                    number = number.rstrip('0').rstrip('.')
            else:
                mantissa, exponent = format(float(value), '.15e').split('e')
                number = mantissa.rstrip('0').rstrip('.') + 'e' + ('+' if int(exponent) >= 0 else '-') + str(abs(int(exponent)))
            text = 'number:' + number
        else:
            raise ValueError('unsupported provisioning value')
    return digest(text.encode('latin1' if all(ord(c) <= 255 for c in text) else 'utf8'))


def extension_id(path):
    return ''.join(chr(ord('a') + int(c, 16)) for c in digest(str(path).encode())[:32])


def manifest():
    return json.loads((ROOT / 'configs/browser-components.json').read_text())


def pinned_file(entry):
    path = ROOT / 'tools/browser-userscripts' / entry['file']
    data = path.read_bytes()
    if digest(data) != entry['sha256']:
        raise ValueError('userscript artifact checksum mismatch: ' + entry['file'])
    return data


def make_provisioning(m):
    scripts = []
    for entry in m['scripts']:
        if uuid.UUID(entry['uuid']).version != 4:
            raise ValueError('Tampermonkey requires stable UUIDv4 script identities')
        source = pinned_file(entry)
        name = next(line.split('@name', 1)[1].strip() for line in source.decode().splitlines() if line.startswith('// @name '))
        scripts.append({'uuid': entry['uuid'], 'name': name, 'file_url': entry['url'], 'source': base64.b64encode(source).decode(),
                        'enabled': True, 'options': {'check_for_updates': False},
                        'requires': [{'url': r['url'], 'ts': 0, 'content': pinned_file(r).decode(),
                                      'mimetype': 'text/javascript', 'modified': False} for r in entry['requires']]})
    return {'version': '1', 'scripts': scripts}


def stage(target):
    m = manifest()
    target.mkdir(parents=True, exist_ok=True)
    cache = Path(os.environ.get('XDG_CACHE_HOME', str(Path.home() / '.cache'))) / 'sparkclaw/browser-components'
    cache.mkdir(parents=True, exist_ok=True)
    package = cache / (m['tampermonkey']['sha256'] + '.crx')
    if not package.exists():
        with urllib.request.urlopen(m['tampermonkey']['url'], timeout=60) as response:
            data = response.read(10 << 20)
        if digest(data) != m['tampermonkey']['sha256']:
            raise ValueError('official Tampermonkey download changed; update the reviewed component pin')
        package.write_bytes(data)
    data = package.read_bytes()
    if digest(data) != m['tampermonkey']['sha256'] or data[:4] != b'Cr24' or struct.unpack_from('<I', data, 4)[0] != 3:
        raise ValueError('invalid pinned Tampermonkey CRX')
    offset = 12 + struct.unpack_from('<I', data, 8)[0]
    extension = target / 'tampermonkey'
    extension.mkdir()
    with zipfile.ZipFile(io.BytesIO(data[offset:])) as archive:
        for item in archive.infolist():
            relative = Path(item.filename)
            if relative.is_absolute() or '..' in relative.parts or (item.external_attr >> 16) & 0o170000 == 0o120000:
                raise ValueError('unsafe CRX entry')
            archive.extract(item, extension)
    if json.loads((extension / 'manifest.json').read_text())['version'] != m['tampermonkey']['version']:
        raise ValueError('Tampermonkey version mismatch')
    provisioning = make_provisioning(m)
    provisioning['deployment'] = str(time.time_ns())
    expected_hash = '1:' + provisioning_hash(provisioning)
    pending = '!e||!e.jsonImport?.some(x=>x.hash===' + json.dumps(expected_hash) + ')'
    # Chromium 148 initializes managed policy after about five seconds. Upstream
    # abandons it after one second; retain its bounded algorithm with a bounded 20s budget.
    background = extension / 'background.js'
    source = background.read_text()
    old = 'for(let t=5;!e&&t>0;--t)5!==t&&console.warn(`Managed storage is slow or not responding! Retrying (attempt ${5-t})...`)'
    new = 'for(let t=50;(' + pending + ')&&t>0;--t)50!==t&&console.warn(`Managed storage is slow or not responding! Retrying (attempt ${50-t})...`)'
    if source.count(old) != 1:
        raise ValueError('Tampermonkey managed-policy compatibility patch no longer matches')
    source = source.replace(old, new)
    # An immediate empty object must not exhaust all retries in milliseconds.
    wait_old = 'e=n,t(!0)}))}))]);return e})();return{\n...e}})();if(!e||!e.jsonImport)return;'
    wait_new = 'e=n,t(!0)}))}))]),(' + pending + ')&&await new Promise(e=>se(e,200));return e})();return{\n...e}})();if(!e||!e.jsonImport)return;'
    if source.count(wait_old) != 1:
        raise ValueError('Tampermonkey policy backoff patch no longer matches')
    worker = f"sparkclaw-background-{m['tampermonkey']['productVersion']}-{expected_hash[2:14]}.js"
    source = source.replace(wait_old, wait_new)
    # Native provisioning otherwise generates a new UUID on every deployment,
    # even for an existing system script. Honor the product's stable manifest ID.
    uuid_old = 'const n=Pe(),{file_url:r,source:s,storage:i,options:c,resources:l,requires:u}=e'
    uuid_new = 'const n=e.uuid||Pe(),{file_url:r,source:s,storage:i,options:c,resources:l,requires:u}=e'
    if source.count(uuid_old) != 1:
        raise ValueError('Tampermonkey managed script identity patch no longer matches')
    source = source.replace(uuid_old, uuid_new)
    reconcile_old = 'defaultscript:!1!==o,replace:!0,internal:!0})}catch(e){}if(p&&p.installed)'
    reconcile_new = 'defaultscript:!1!==o,replace:!0,internal:!0,sparkclaw_reconcile:!!e.uuid})}catch(e){}if(p&&p.installed)'
    same_old = 'if(e.defaultscript)return y.Breach();if(e.noreinstall)return y.Breach()'
    same_new = 'if(e.defaultscript&&!e.sparkclaw_reconcile)return y.Breach();if(e.noreinstall)return y.Breach()'
    if source.count(reconcile_old) != 1 or source.count(same_old) != 1:
        raise ValueError('Tampermonkey managed reconciliation patch no longer matches')
    source = source.replace(reconcile_old, reconcile_new).replace(same_old, same_new)
    (extension / worker).write_text(source)
    extension_manifest = extension / 'manifest.json'
    meta = json.loads(extension_manifest.read_text())
    meta['background']['service_worker'] = worker
    meta['version'] = m['tampermonkey']['productVersion']
    meta['version_name'] = m['tampermonkey']['version'] + f" (SparkClaw r{m['tampermonkey']['patchRevision']})"
    extension_manifest.write_text(json.dumps(meta, indent=2))
    identity = extension_id(m['tampermonkey']['path'])
    policy = {'3rdparty': {'extensions': {identity: {'jsonImport': [{
        'hash': expected_hash,
        'url': 'data:application/json;base64,' + base64.b64encode(json.dumps(provisioning, ensure_ascii=False).encode()).decode(),
        'haltOnError': True, 'installAsSystemScripts': True}]}}}}
    (target / 'policy.json').write_text(json.dumps(policy, ensure_ascii=False))
    receipt = {'manifest': m, 'extensionID': identity, 'policySHA256': digest((target / 'policy.json').read_bytes()),
               'files': {str(p.relative_to(extension)): digest(p.read_bytes()) for p in extension.rglob('*') if p.is_file()}}
    (target / 'receipt.json').write_text(json.dumps(receipt, sort_keys=True))
    print('Staged pinned Tampermonkey and', len(provisioning['scripts']), 'managed scripts')


def install_system(target):
    if os.getuid() != 0:
        raise ValueError('system component installation requires root')
    receipt = json.loads((target / 'receipt.json').read_text())
    extension = Path(receipt['manifest']['tampermonkey']['path'])
    if str(extension) != '/opt/sparkclaw/tampermonkey':
        raise ValueError('unexpected product extension path')
    pending = extension.with_name('tampermonkey.pending')
    if pending.exists():
        shutil.rmtree(pending)
    shutil.copytree(target / 'tampermonkey', pending)
    for p in [pending, *pending.rglob('*')]:
        os.chown(p, 0, 0)
        p.chmod(0o755 if p.is_dir() else 0o644)
    previous = extension.with_name('tampermonkey.previous')
    if extension.exists():
        if previous.exists():
            shutil.rmtree(previous)
        extension.rename(previous)
    pending.rename(extension)
    SYSTEM.mkdir(parents=True, exist_ok=True)
    POLICY.parent.mkdir(parents=True, exist_ok=True)
    for source, dest in [(target / 'receipt.json', SYSTEM / 'receipt.json'), (target / 'policy.json', POLICY)]:
        temp = dest.with_suffix('.tmp')
        shutil.copyfile(source, temp)
        temp.chmod(0o644)
        temp.replace(dest)


def prepare_profile(profile):
    # Caller stops the dedicated browser before touching its deployment preferences.
    default = profile / 'Default'
    default.mkdir(parents=True, exist_ok=True)
    pref = default / 'Preferences'
    value = json.loads(pref.read_text()) if pref.exists() else {}
    identity = extension_id(manifest()['tampermonkey']['path'])
    settings = value.setdefault('extensions', {}).setdefault('settings', {})
    backup = profile.parent / 'component-backups' / str(time.time_ns())
    backup.mkdir(parents=True, mode=0o700)
    if pref.exists():
        shutil.copy2(pref, backup / 'Preferences')
    destination = default / 'Local Extension Settings' / identity
    if not destination.exists():
        # Archive the legacy manager, but do not clone ordinary scripts into the
        # product database: native system imports would duplicate them.
        for old_id, setting in settings.items():
            path = Path(setting.get('path', '/nonexistent'))
            try:
                ext = json.loads((path / 'manifest.json').read_text())
                name = json.loads((path / '_locales/en/messages.json').read_text()).get('extName', {}).get('message', '')
            except (OSError, ValueError):
                continue
            source = default / 'Local Extension Settings' / old_id
            if name == 'Tampermonkey' and ext.get('version') == '5.5.0' and source.is_dir():
                shutil.copytree(source, backup / 'tampermonkey-storage')
                break
    settings.setdefault(identity, {})['user_scripts_enabled'] = True
    temp = pref.with_suffix('.tmp')
    temp.write_text(json.dumps(value))
    temp.chmod(0o600)
    temp.replace(pref)
    print('Prepared product extension identity:', identity)


def verify_system():
    m = manifest()
    receipt = json.loads((SYSTEM / 'receipt.json').read_text())
    if receipt['manifest'] != m or digest(POLICY.read_bytes()) != receipt['policySHA256']:
        raise ValueError('browser component deployment is stale')
    extension = Path(m['tampermonkey']['path'])
    if extension.is_symlink() or extension.stat().st_uid != 0:
        raise ValueError('product Tampermonkey must be root-owned')
    files = {str(p.relative_to(extension)): digest(p.read_bytes()) for p in extension.rglob('*') if p.is_file()}
    if files != receipt['files']:
        raise ValueError('installed Tampermonkey checksum mismatch')
    make_provisioning(m)  # Also verify all repository source/dependency pins.
    print('Pinned browser components current')


def verify_profile(profile):
    from browser_component_state import read_extension_state
    m = manifest()
    identity = extension_id(m['tampermonkey']['path'])
    default = profile / 'Default'
    preferences = json.loads((default / 'Preferences').read_text())
    extension_settings = preferences.get('extensions', {}).get('settings', {}).get(identity, {})
    if extension_settings.get('disable_reasons') or extension_settings.get('state') == 0:
        raise ValueError('managed Tampermonkey extension is disabled')
    if extension_settings.get('user_scripts_enabled') is not True:
        raise ValueError('managed userscript permission is not enabled')
    state = read_extension_state(default / 'Local Extension Settings' / identity)
    policy = json.loads(POLICY.read_text())
    current_hash = policy['3rdparty']['extensions'][identity]['jsonImport'][0]['hash']
    if current_hash not in state.get('!misc.managed.consumed', {}):
        raise ValueError('current managed script deployment has not been imported')
    for expected, item in zip(make_provisioning(m)['scripts'], m['scripts']):
        matches = [(key, value) for key, value in state.items() if key.startswith('!extdb.@meta#') and value.get('name') == expected['name']]
        if len(matches) != 1:
            raise ValueError('managed script missing or duplicated: ' + expected['name'])
        key, meta = matches[0]
        source = state.get('!extdb.@source#' + key.split('#', 1)[1], '')
        if meta.get('enabled') is not True or meta.get('system') is not True or meta.get('version') != item['version'] or meta.get('options', {}).get('check_for_updates') is not False or digest(source.encode()) != item['sha256']:
            raise ValueError('managed script is disabled, changed, or stale: ' + expected['name'])
        caches = [value for key_, value in state.items() if key_.startswith('!extdb.@ext#' + key.split('#', 1)[1] + ':')]
        for requirement in item['requires']:
            cached = [value for value in caches if value.get('url') == requirement['url']]
            if len(cached) != 1 or digest(base64.b64decode(cached[0].get('resource', {}).get('base', ''), validate=True)) != requirement['sha256']:
                raise ValueError('managed script dependency is missing or changed: ' + requirement['file'])
    print('Tampermonkey and', len(m['scripts']), 'managed scripts are current, enabled, and imported')


def wait_profile(profile):
    error = None
    for _ in range(30):
        try:
            verify_profile(profile)
            return
        except (OSError, ValueError) as current:
            error = current
            time.sleep(2)
    raise ValueError('managed browser readiness timed out: ' + str(error))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('action', choices=['stage', 'install-system', 'prepare-profile', 'check', 'check-profile', 'wait-profile'])
    parser.add_argument('path', nargs='?', type=Path)
    args = parser.parse_args()
    if args.action == 'stage': stage(args.path)
    elif args.action == 'install-system': install_system(args.path)
    elif args.action == 'prepare-profile': prepare_profile(args.path)
    elif args.action == 'check-profile': verify_profile(args.path)
    elif args.action == 'wait-profile': wait_profile(args.path)
    else: verify_system()


if __name__ == '__main__':
    main()
