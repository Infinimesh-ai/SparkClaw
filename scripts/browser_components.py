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
import stat
import struct
import tempfile
import time
import urllib.request
import uuid
import zipfile

ROOT = Path(__file__).resolve().parents[1]
SYSTEM = Path('/opt/sparkclaw/browser-components')
MANAGED_EXTENSION = Path('/opt/sparkclaw/tampermonkey')


def policy_location(machine):
    # The pinned x64 artifact is Chrome for Testing, whose compiled policy root
    # differs from both ordinary Google Chrome and the pinned arm64 Chromium.
    # https://chromium.googlesource.com/chromium/src/+/HEAD/docs/enterprise/policies.md
    if machine in ('x86_64', 'amd64'):
        return Path('/etc/opt/chrome_for_testing/policies/managed/sparkclaw-userscripts.json'), 4
    if machine in ('aarch64', 'arm64'):
        return Path('/etc/chromium/policies/managed/sparkclaw-userscripts.json'), 3
    raise ValueError('unsupported pinned browser policy architecture')


POLICY, POLICY_DIRECTORY_DEPTH = policy_location(os.uname().machine)
RETIRED_MANAGED_UUIDS = ('c6378bd8-6136-4f1b-81be-2c0fed94baf3',)
MANAGED_IMPORT_OPTIONS = {'haltOnError': True, 'installAsSystemScripts': True}


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
    if entry.get('source') == 'app-cli':
        import tarfile
        release = json.loads((ROOT / 'configs/app-cli-release.json').read_text())
        artifact = release['artifacts']['runtime']
        archive = ROOT / 'vendor/app-cli' / artifact['file']
        if digest(archive.read_bytes()) != artifact['sha256']:
            raise ValueError('App-CLI artifact digest mismatch')
        with tarfile.open(archive) as package:
            data = package.extractfile('package/assets/mail/' + entry['file']).read()
    else:
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


def provisioning_generation(m, provisioning):
    # Only public release inputs belong here, never time, staging paths or
    # profile state. Native consumed hashes must survive identical restaging.
    # A deliberate reimport requires a new productVersion, even if script
    # source versions are unchanged; verify_worker_update enforces that gate.
    inputs = {'schema': 'sparkclaw-native-provisioning-v1', 'manifest': m,
              'provisioning': provisioning, 'import_options': MANAGED_IMPORT_OPTIONS,
              'retired_managed_uuids': RETIRED_MANAGED_UUIDS}
    return digest(json.dumps(inputs, sort_keys=True, separators=(',', ':'),
                             ensure_ascii=False).encode('utf8'))


def extract_unpacked_extension(archive, extension):
    for item in archive.infolist():
        relative = Path(item.filename)
        if relative.is_absolute() or '..' in relative.parts or (item.external_attr >> 16) & 0o170000 == 0o120000:
            raise ValueError('unsafe CRX entry')
        # Signed CRX verification metadata is reserved by Chromium and prevents
        # loading this reviewed package as an unpacked extension. The original
        # CRX digest is verified before extraction; never copy its signature cache.
        if relative.parts and relative.parts[0] == '_metadata':
            continue
        archive.extract(item, extension)


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
        extract_unpacked_extension(archive, extension)
    if json.loads((extension / 'manifest.json').read_text())['version'] != m['tampermonkey']['version']:
        raise ValueError('Tampermonkey version mismatch')
    provisioning = make_provisioning(m)
    provisioning['deployment'] = provisioning_generation(m, provisioning)
    expected_hash = '1:' + provisioning_hash(provisioning)
    pending = '!e||!e.jsonImport?.some(x=>x.hash===' + json.dumps(expected_hash) + ')'
    # Chromium 148 initializes managed policy after about five seconds. Upstream
    # can see empty storage while the policy service initializes. Allow a bounded
    # 100s budget, including delayed worker startup on an existing work2 profile.
    background = extension / 'background.js'
    source = background.read_text()
    old = 'for(let t=5;!e&&t>0;--t)5!==t&&console.warn(`Managed storage is slow or not responding! Retrying (attempt ${5-t})...`)'
    new = 'for(let t=250;(' + pending + ')&&t>0;--t)250!==t&&console.warn(`Managed storage is slow or not responding! Retrying (attempt ${250-t})...`)'
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
    # Retire only the product-owned identity through Tampermonkey's native
    # database/cache APIs. Never match by name or remove ordinary user scripts.
    retire_old = 'const{version:p,scripts:h,settings:f}=u;if("1"===p){if(h)'
    retire_new = ('const{version:p,scripts:h,settings:f}=u;if("1"===p){'
                  'i.push(async()=>{for(const id of ' + json.dumps(RETIRED_MANAGED_UUIDS) + '){'
                  'const record=sa.getByUid(id);if(record?.script?.system===true){'
                  'await sa.removeByUid(id);await sa.setStorageByUid(id,void 0);await Lc.dropAll(id);}}});if(h)')
    if source.count(retire_old) != 1:
        raise ValueError('Tampermonkey managed retirement patch no longer matches')
    source = source.replace(retire_old, retire_new)
    # Native first-install inspection runs after managed imports and otherwise
    # labels even policy-provisioned scripts as foisted. Recognize only the
    # exact pinned system bytes; keep native inspection for every other script.
    known = {entry['uuid']: entry['sha256'] for entry in m['scripts']}
    unfamiliar_old = 't.script&&t.cond&&t.script.evilness!=Uc.SEVERITY_FOISTED_SCRIPT&&('
    unfamiliar_new = ('t.script&&t.cond&&!(t.script.system===true&&'
                      + json.dumps(known, separators=(',', ':'))
                      + '[e]===Do(t.script.textContent,"UTF-8"))&&'
                      't.script.evilness!=Uc.SEVERITY_FOISTED_SCRIPT&&(')
    if source.count(unfamiliar_old) != 1:
        raise ValueError('Tampermonkey pinned managed origin patch no longer matches')
    source = source.replace(unfamiliar_old, unfamiliar_new)
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
        **MANAGED_IMPORT_OPTIONS}]}}}}
    (target / 'policy.json').write_text(json.dumps(policy, ensure_ascii=False))
    receipt = {'manifest': m, 'extensionID': identity, 'policySHA256': digest((target / 'policy.json').read_bytes()),
               'files': {str(p.relative_to(extension)): digest(p.read_bytes()) for p in extension.rglob('*') if p.is_file()}}
    (target / 'receipt.json').write_text(json.dumps(receipt, sort_keys=True))
    print('Staged pinned Tampermonkey and', len(provisioning['scripts']), 'managed scripts')


def install_public_directories():
    # The owner browser must read these root-installed, non-secret assets even
    # when deployment runs with a private umask. Never chmod profile/key trees.
    # A finite chain belonging to the selected artifact, including /etc/opt on
    # a fresh CfT host. Do not traverse or chmod private browser/profile paths.
    for directory in (SYSTEM.parent, SYSTEM, *reversed(list(POLICY.parents)[:POLICY_DIRECTORY_DEPTH])):
        if directory.is_symlink():
            raise ValueError('public browser component directory is a symlink')
        directory.mkdir(parents=True, exist_ok=True)
        directory.chmod(0o755)


def worker_identity(extension, version):
    value = json.loads((extension / 'manifest.json').read_text())
    if value.get('version') != version:
        raise ValueError('managed extension version differs from its receipt')
    worker = value.get('background', {}).get('service_worker')
    if not isinstance(worker, str) or not worker or Path(worker).name != worker or worker in ('.', '..'):
        raise ValueError('invalid managed background worker path')
    return worker, digest((extension / worker).read_bytes())


def verify_worker_update(target, extension, receipt):
    version = receipt['manifest']['tampermonkey']['productVersion']
    candidate = worker_identity(target / 'tampermonkey', version)
    previous = SYSTEM / 'receipt.json'
    if not previous.exists():
        return
    installed = json.loads(previous.read_text())
    prior_version = installed['manifest']['tampermonkey']['productVersion']
    prior = worker_identity(extension, prior_version)
    # Chromium retains an unpacked extension's worker registration by version.
    # A changed policy hash changes our worker URL; replacing its file under the
    # same version can strand native imports on the removed previous worker.
    if version == prior_version and candidate != prior:
        raise ValueError('managed background worker changed; increase productVersion before installing')


def install_system(target):
    if os.getuid() != 0:
        raise ValueError('system component installation requires root')
    receipt = json.loads((target / 'receipt.json').read_text())
    extension = Path(receipt['manifest']['tampermonkey']['path'])
    if extension != MANAGED_EXTENSION:
        raise ValueError('unexpected product extension path')
    # Validate before touching public directories, pending trees or the policy.
    # A different version also permits a deliberate paired release rollback.
    verify_worker_update(target, extension, receipt)
    install_public_directories()
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


def verify_public_permissions(extension):
    paths = [SYSTEM.parent, SYSTEM, *list(POLICY.parents)[:POLICY_DIRECTORY_DEPTH],
             SYSTEM / 'receipt.json', POLICY, extension, *extension.rglob('*')]
    for path in paths:
        info = path.lstat()
        directory = stat.S_ISDIR(info.st_mode)
        if (info.st_uid != 0 or stat.S_ISLNK(info.st_mode)
                or not (directory or stat.S_ISREG(info.st_mode))
                or stat.S_IMODE(info.st_mode) != (0o755 if directory else 0o644)):
            raise ValueError('public browser component ownership or permissions are unsafe')


def verify_system():
    m = manifest()
    extension = Path(m['tampermonkey']['path'])
    if (extension / '_metadata').exists():
        raise ValueError('signed CRX metadata prevents unpacked extension loading')
    verify_public_permissions(extension)
    receipt = json.loads((SYSTEM / 'receipt.json').read_text())
    if receipt['manifest'] != m or digest(POLICY.read_bytes()) != receipt['policySHA256']:
        raise ValueError('browser component deployment is stale')
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
    for retired in RETIRED_MANAGED_UUIDS:
        if state.get('!extdb.@meta#' + retired, {}).get('system') is True:
            raise ValueError('retired managed script remains installed: ' + retired)
    for expected, item in zip(make_provisioning(m)['scripts'], m['scripts']):
        matches = [(key, value) for key, value in state.items() if key.startswith('!extdb.@meta#') and value.get('name') == expected['name'] and value.get('system') is True]
        if len(matches) != 1:
            raise ValueError('managed script missing or duplicated: ' + expected['name'])
        key, meta = matches[0]
        if key != '!extdb.@meta#' + expected['uuid']:
            raise ValueError('managed script identity mismatch: ' + expected['name'])
        source = state.get('!extdb.@source#' + key.split('#', 1)[1], '')
        if meta.get('evilness', 0) != 0 or meta.get('enabled') is not True or meta.get('system') is not True or meta.get('version') != item['version'] or meta.get('options', {}).get('check_for_updates') is not False or digest(source.encode()) != item['sha256']:
            raise ValueError('managed script is disabled, changed, or stale: ' + expected['name'])
        caches = [value for key_, value in state.items() if key_.startswith('!extdb.@ext#' + key.split('#', 1)[1] + ':')]
        for requirement in item['requires']:
            cached = [value for value in caches if value.get('url') == requirement['url']]
            if len(cached) != 1 or digest(base64.b64decode(cached[0].get('resource', {}).get('base', ''), validate=True)) != requirement['sha256']:
                raise ValueError('managed script dependency is missing or changed: ' + requirement['file'])
    print('Tampermonkey and', len(m['scripts']), 'managed scripts are current, enabled, and imported')


def wait_profile(profile):
    error = None
    for _ in range(90):
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
