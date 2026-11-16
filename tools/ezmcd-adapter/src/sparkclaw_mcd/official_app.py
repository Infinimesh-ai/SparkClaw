"""Narrow read-only Mac source discovery for the authorized P0 qualification.

No UUID is hardcoded. We inspect only containers whose system metadata names
the official bundle. Material stays in memory and never enters a result log.
"""
from pathlib import Path
import plistlib
import sqlite3
from urllib.parse import urlsplit

from .compatibility import material_from_sample
from .errors import require, AdapterError

BUNDLE = "com.mcdonalds.gma.cn"


def find_binary(apps=None):
    apps = [Path("/Applications"), Path.home() / "Applications"] if apps is None else apps
    candidates = []
    for apps_root in apps:
        for bundle in apps_root.glob("*.app"):
            for info in [bundle / "Info.plist", *bundle.glob("Wrapper/*.app/Info.plist"),
                         *bundle.glob("WrappedBundle/Info.plist")]:
                try:
                    meta = plistlib.loads(info.read_bytes())
                    if meta.get("CFBundleIdentifier") == BUNDLE:
                        executable = meta.get("CFBundleExecutable", "")
                        require(executable and "/" not in executable, "official_app_identity_mismatch")
                        candidates.append(info.parent / executable)
                except (OSError, ValueError, plistlib.InvalidFileException):
                    continue
    candidates = list(dict.fromkeys(p.resolve() for p in candidates))
    require(len(candidates) == 1, "official_app_missing_or_ambiguous")
    return candidates[0]


def find_container(containers=None):
    containers = Path.home() / "Library/Containers" if containers is None else Path(containers)
    candidates = []
    for entry in containers.iterdir():
        meta = entry / ".com.apple.containermanagerd.metadata.plist"
        try:
            value = plistlib.loads(meta.read_bytes())
        except (OSError, ValueError, plistlib.InvalidFileException):
            # Some macOS versions protect container-manager metadata while
            # explicitly allowing this App's data. Inspect only its exact
            # preference filename, never other applications' preference data.
            preferences = entry / "Data/Library/Preferences" / (BUNDLE + ".plist")
            if not preferences.is_file() or preferences.is_symlink():
                continue
            try:
                app_preferences = plistlib.loads(preferences.read_bytes())
            except (OSError, ValueError, plistlib.InvalidFileException):
                continue
            if app_preferences.get("MCDUserDefaultSid") and app_preferences.get("MCDUserDefaultMeddyId"):
                candidates.append(entry / "Data")
            continue
        if value.get("MCMMetadataIdentifier") == BUNDLE:
            candidates.append(entry / "Data")
    require(len(candidates) == 1, "official_container_missing_or_ambiguous")
    return candidates[0]


def cached_balance_request(container):
    path = container / "Library/Caches" / BUNDLE / "Cache.db"
    require(path.is_file(), "official_session_sample_unavailable")
    try:
        with sqlite3.connect(path.as_uri() + "?mode=ro", uri=True, timeout=2) as conn:
            conn.execute("PRAGMA query_only=ON")
            row = conn.execute("SELECT b.request_object FROM cfurl_cache_response r JOIN cfurl_cache_blob_data b USING(entry_ID) "
                               "WHERE r.request_key LIKE '%/bff/member/mwallet/balance%' ORDER BY r.entry_ID DESC LIMIT 1").fetchone()
        require(row is not None and len(row[0]) <= 65536, "official_session_sample_unavailable")
        sample = plistlib.loads(row[0])["Array"]
        url = urlsplit(sample[1]["_CFURLString"])
        require(url.scheme == "https" and url.netloc == "api.mcd.cn" and not url.fragment, "balance_sample_required")
        return {"method": sample[18], "host": url.netloc, "path": url.path, "query": url.query, "headers": sample[19]}
    except (sqlite3.Error, ValueError, KeyError, IndexError, TypeError):
        raise AdapterError("official_session_sample_unavailable") from None


def acquire():
    binary, container = find_binary(), find_container()
    request = cached_balance_request(container)
    material = material_from_sample(binary, request)
    prefs = plistlib.loads((container / "Library/Preferences" / (BUNDLE + ".plist")).read_bytes())
    require(material["headers"]["sid"] == prefs.get("MCDUserDefaultSid")
            and material["headers"]["meddyid"] == prefs.get("MCDUserDefaultMeddyId"), "official_account_changed")
    # Caller must perform a live member/balance read before provisioning. This
    # P0 function alone never claims a validated connection or persists it.
    return binary, material
