from copy import deepcopy
import json
from pathlib import Path
import time
import unittest
from unittest.mock import patch

from ezmcd.errors import EzMcdError
from sparkclaw_mcd.compatibility import validate_material, member_digest
from sparkclaw_mcd.errors import AdapterError
from sparkclaw_mcd.transport import provider


class HandoffTests(unittest.TestCase):
    def setUp(self):
        self.material = json.loads(Path(__file__).with_name("session-envelope-v1.json").read_text())["material"]

    def test_empty_signed_header_needs_explicit_member_binding(self):
        original = member_digest(self.material)
        self.material["signing_profile"] = "mcd-v4-v2"
        self.material["member_id"] = self.material["headers"]["meddyid"]
        self.material["headers"]["meddyid"] = ""
        validate_material(self.material)
        self.assertEqual(member_digest(self.material), original)
        for changes in ({"member_id": ""}, {"signing_profile": "mcd-v4-v1"}, {"runtime": {}}, {"member_id": "bad\r\n"}):
            value = deepcopy(self.material); value.update(changes)
            with self.assertRaises(AdapterError): validate_material(value)
        value = deepcopy(self.material); del value["member_id"]
        with self.assertRaises(AdapterError): validate_material(value)
        self.material["headers"]["meddyid"] = "different-member"
        with self.assertRaises(AdapterError): validate_material(self.material)

    def test_v2_live_wallet_rejects_other_member_and_preserves_header(self):
        self.material.update(signing_profile="mcd-v4-v2", member_id="bound-member")
        self.material["headers"]["meddyid"] = ""
        client = provider(self.material, "account", int(time.time()*1000)+10000)
        with patch.object(client.http, "send", return_value={"http_status": 200, "response": {"success": True, "data": {"userId": "other", "balance": "50.00"}, "code": 200}}):
            with self.assertRaisesRegex(EzMcdError, "wallet_member_mismatch"): client.wallet()
        self.assertEqual(client.http.material["headers"]["meddyid"], "")
        client.http.close()
