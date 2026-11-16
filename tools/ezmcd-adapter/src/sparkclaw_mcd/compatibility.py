"""Build recognition and v4 material extraction; no process injection/sampling."""
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
import hashlib
import hmac
import json
from pathlib import Path
import plistlib

from ezmcd.protocol.macho import MachO
from ezmcd.protocol.signing import sign_v4, constants, constants_build
from ezmcd.models import fingerprint

from . import ADAPTER_REVISION
from .errors import require
from .canonical import digest, fields

BUILDS = json.loads(Path(__file__).with_name("builds.json").read_text())["builds"]
HEADER_FIELDS = frozenset(("ct", "v", "language", "sid", "token", "meddyid", "routingid", "tid", "d"))


def recognized_build(binary):
    path = Path(binary)
    require(path.is_file() and path.stat().st_size <= 256 * 1024 * 1024, "official_app_unavailable")
    with path.open("rb") as stream:
        sha = hashlib.file_digest(stream, "sha256").hexdigest()
    build = next((row for row in BUILDS if row["sha256"] == sha), None)
    require(build is not None, "official_app_build_unsupported")
    info = plistlib.loads(path.with_name("Info.plist").read_bytes())
    require(info.get("CFBundleIdentifier") == "com.mcdonalds.gma.cn"
            and info.get("CFBundleShortVersionString") == build["version"]
            and info.get("CFBundleExecutable") == path.name, "official_app_identity_mismatch")
    return build


def qualify(binary):
    build = recognized_build(binary)
    return {"app_version": build["version"], "binary_sha256": build["sha256"],
            "adapter_revision": ADAPTER_REVISION, "build_recognized": True,
            "v4_material_available": True, "v4_sample_verified": False,
            "v5_status": build["v5"], "checkout_qualified": False,
            "payment_method": "ARCHCARD", "order_created": False, "payment_attempted": False}


def validate_material(material):
    fields(material, ("signing_profile", "app_version", "binary_sha256", "headers", "access_key", "secret"), ("member_id",))
    profile = material["signing_profile"]
    require(profile in ("mcd-v4-v1", "mcd-v4-v2"), "signing_profile_unsupported")
    require(("member_id" in material) == (profile == "mcd-v4-v2"), "invalid_member_binding")
    build = next((b for b in BUILDS if b["version"] == material["app_version"] and b["sha256"] == material["binary_sha256"]), None)
    require(build is not None, "official_app_build_unsupported")
    headers = material["headers"]
    require(type(headers) is dict and headers.keys() <= HEADER_FIELDS
            and {"ct", "v", "language", "sid", "token", "meddyid"} <= headers.keys(), "invalid_session_headers")
    values = [v for k, v in headers.items() if not (k == "meddyid" and profile == "mcd-v4-v2" and v == "")]
    if profile == "mcd-v4-v2":
        values.append(material["member_id"])
        require(headers["meddyid"] in ("", material["member_id"]), "invalid_member_binding")
    for value in [*values, material["access_key"], material["secret"]]:
        require(type(value) is str and 0 < len(value) <= 4096
                and value.isascii() and all(32 <= ord(c) < 127 for c in value), "invalid_session_headers")
    require(headers["ct"] == "101" and headers["v"] == build["client_header_version"], "session_version_mismatch")
    require(len(material["access_key"]) == 20 and len(material["secret"]) == 40, "invalid_signing_material")
    return material


def member_digest(material):
    validate_material(material)
    return digest({"provider": "mcd-cn", "member": member_id(material)})


def member_id(material):
    return material.get("member_id", material["headers"]["meddyid"])


def material_from_account(record):
    """Extract v4 only from a locally selected, verified upstream account.

    A live wallet/member check is mandatory before issuing a session. No v5,
    refresh token, permission, sample timestamp or purchase ledger is copied.
    """
    require(record.get("provider") == "mcd-cn" and not record.get("revoked")
            and record.get("session_refresh", {}).get("state") not in ("CLAIMED", "CANDIDATE", "STOPPED"),
            "official_account_unavailable")
    build = recognized_build(record["binary"])
    profile = record["profile"]
    require(profile.get("build_sha256") == build["sha256"]
            and profile.get("verified_endpoint") == "/bff/member/mwallet/balance", "official_account_unverified")
    data = profile["fields"]
    identity = {"provider": "mcd-cn", "member": data["meddyId"]}
    require(record["identity"] == fingerprint(identity), "member_mismatch")
    values = constants(record["binary"])
    require(constants_build(values) == build["sha256"], "official_app_changed")
    headers = {k: profile["client_headers"][k] for k in ("ct", "v", "language")}
    headers.update({"sid": data["sid"], "token": data["deviceToken"],
                    "meddyid": "" if profile.get("member_source") == "balance_response" else data["meddyId"]})
    for source, target in (("routingId", "routingid"), ("tid", "tid"), ("riskDeviceId", "d")):
        if data.get(source):
            headers[target] = data[source]
    return validate_material({"signing_profile": "mcd-v4-v2", "app_version": build["version"],
        "binary_sha256": build["sha256"], "headers": headers, "member_id": data["meddyId"],
        "access_key": values[0], "secret": values[1]})


def material_from_sample(binary, request):
    """Private caller supplies one official cached balance request, never a DB."""
    build = recognized_build(binary)
    fields(request, ("method", "host", "path", "headers"), ("body", "query"))
    require(request["method"] == "GET" and request["host"] == "api.mcd.cn"
            and request["path"] == "/bff/member/mwallet/balance"
            and not request.get("body") and not request.get("query"), "balance_sample_required")
    headers = request["headers"]
    require(type(headers) is dict and all(type(k) is str for k in headers), "invalid_session_headers")
    lowered = {k.lower(): v for k, v in headers.items()}
    require(len(lowered) == len(headers), "duplicate_session_headers")
    require(lowered.get("sv") == "v4" and lowered.get("x-mcd-gw-v") == "1", "signing_profile_unsupported")
    image = MachO(binary)
    # Verify the exact bytes being decoded too, not just a preceding stat/read.
    require(hashlib.sha256(image.blob).hexdigest() == build["sha256"], "official_app_changed")
    material = {"signing_profile": "mcd-v4-v1", "app_version": build["version"], "binary_sha256": build["sha256"],
                "headers": {k: v for k, v in lowered.items() if k in HEADER_FIELDS}}
    for key, address, length, delta in zip(("access_key", "secret"), build["v4_locations"], (20, 40), (5, 29)):
        material[key] = bytes(v ^ ((i - delta) & 255) for i, v in enumerate(image.read(int(address, 16), length))).decode("ascii")
    validate_material(material)
    parts = lowered.get("authorization", "").split("#")
    require(len(parts) == 6, "invalid_official_signature")
    moment = parsedate_to_datetime(parts[4])
    require(moment.tzinfo is not None and moment <= datetime.now(timezone.utc), "untrusted_sample_time")
    expected = sign_v4(lowered, material["access_key"], material["secret"], moment, "GET", request["path"])
    require(all(type(lowered.get(k)) is str and hmac.compare_digest(v, lowered[k]) for k, v in expected.items()),
            "invalid_official_signature")
    return material
