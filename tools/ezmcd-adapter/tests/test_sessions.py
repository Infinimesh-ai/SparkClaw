from copy import deepcopy
import json
from pathlib import Path
import time
import unittest
from unittest.mock import Mock, patch

from sparkclaw_mcd.canonical import canonical, digest
from sparkclaw_mcd.errors import AdapterError
from sparkclaw_mcd.sessions import validate, seal, execute, issue, MAX_LEASE_MS


class SessionTests(unittest.TestCase):
    def setUp(self):
        self.value = json.loads(Path(__file__).with_name("session-envelope-v1.json").read_text())
        self.now = int(time.time() * 1000)
        self.value.update(issued_at_ms=self.now - 1000, session_not_after_ms=self.now + 60000)
        self.value = seal(self.value)
        self.execution = {k: self.value[k] for k in ("identity", "connection_id", "credential_revision", "grant_revision")}
        self.execution.update(execution_id="execution", deadline_ms=self.now + 30000, max_requests=8)

    def test_shared_fixture_and_canonical_digest(self):
        fixture = json.loads(Path(__file__).with_name("session-envelope-v1.json").read_text())
        self.assertEqual(fixture, validate(fixture, fixture["issued_at_ms"]))
        self.assertEqual(self.value["material_digest"], digest(self.value["material"]))

    def test_lease_scope_digest_and_header_rejections_before_network(self):
        variants = []
        for field, val in (("issued_at_ms", self.now + 1), ("session_not_after_ms", self.now),
                           ("session_not_after_ms", self.now + MAX_LEASE_MS), ("operations", ["checkout.commit"]),
                           ("operations", ["store_get", "store_get"]), ("max_requests", 129)):
            value = deepcopy(self.value); value[field] = val; variants.append(seal(value))
        value = deepcopy(self.value); value["material"]["refresh_token"] = "forbidden"; variants.append(seal(value))
        value = deepcopy(self.value); value["material"]["headers"]["sid"] = "bad\r\n"; variants.append(seal(value))
        value = deepcopy(self.value); value["identity"]["member_digest"] = "0" * 64; variants.append(seal(value))
        value = deepcopy(self.value); value["max_requests"] = 4; variants.append(value)
        with patch("sparkclaw_mcd.sessions.provider") as provider:
            for value in variants:
                with self.subTest(value=value["envelope_digest"]), self.assertRaises(AdapterError):
                    validate(value, self.now)
            provider.assert_not_called()

    def test_execution_binding_and_deadline_reject_without_provider(self):
        with patch("sparkclaw_mcd.sessions.provider") as provider:
            for field, value in (("connection_id", "other"), ("credential_revision", 2),
                                 ("grant_revision", 2), ("max_requests", 65), ("deadline_ms", self.now - 1)):
                execution = deepcopy(self.execution); execution[field] = value
                with self.subTest(field=field), self.assertRaises(AdapterError):
                    execute(self.value, execution, "store_get", {"store_code": "3330613"})
            provider.assert_not_called()

    def test_read_budget_and_expiry_are_passed_to_transport(self):
        client = Mock(); client.store.return_value = {"store_code": "3330613", "latitude": 30.1234}
        with patch("sparkclaw_mcd.sessions.provider", return_value=client) as provider:
            result = execute(self.value, self.execution, "store_get", {"store_code": "3330613"})
            self.assertEqual(json.loads(result["result_json"])["latitude"], 30.1234)
            self.assertEqual(provider.call_args.kwargs["requests"], 8)
            self.assertLessEqual(provider.call_args.args[2], self.value["session_not_after_ms"])
            client.wallet.assert_called_once(); client.http.close.assert_called_once()

    def test_issue_requires_live_member_read_and_never_renews_in_validate(self):
        client = Mock()
        with patch("sparkclaw_mcd.sessions.provider", return_value=client):
            value = issue(self.value["material"], self.value["identity"], "connection", "request", 1, 1, lease_ms=1000)
            client.wallet.assert_called_once()
        before = canonical(value)
        self.assertEqual(before, canonical(validate(value)))
        self.assertEqual(value["session_not_after_ms"] - value["issued_at_ms"], 1000)


if __name__ == "__main__":
    unittest.main()
