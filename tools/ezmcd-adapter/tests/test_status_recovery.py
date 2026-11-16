from copy import deepcopy
import json
import unittest
from unittest.mock import patch

from sparkclaw_mcd.errors import AdapterError
from sparkclaw_mcd.helper import dispatch
from sparkclaw_mcd.purchase import commit, PurchaseStore
from sparkclaw_mcd.quotes import seal
from sparkclaw_mcd.recovery import commit_key, retire_status
import test_adapter
import test_purchase


class StatusRecoveryTests(unittest.TestCase):
    setUp = test_adapter.AdapterTests.setUp
    prepare = test_purchase.PurchaseTests.prepare

    def host_status(self, key, scope=None, account_json=None):
        params = {'source': {'root': str(self.ledger.root), 'alias': 'account'},
                  'binding': scope or self.scope, 'material_digest': None, 'initialize': False,
                  'request': {'operation_key': key}}
        if account_json is not None:
            params['account_json'] = account_json
        return dispatch('host', {'operation': 'host.status', 'params': params})

    def tables(self):
        with self.ledger.store.connect() as db:
            return {name: [tuple(row) for row in db.execute('SELECT * FROM ' + name)] for name in (
                'attempts', 'writes', 'request_keys', 'mcd_commits', 'mcd_preparations', 'mcd_authorities', 'mcd_purchases')}

    def test_frozen_preparation_retires_locally_and_cold_repeats_same_proof(self):
        self.prepare()
        key = commit_key('purchase', 1)
        receipt = self.host_status(key)
        self.assertEqual((receipt['state'], receipt['operation_key'], receipt['original_state']),
                         ('aborted_before_write', key, 'prepared'))
        self.assertTrue(receipt['non_dispatch_proven'])
        self.assertTrue(receipt['purchase_closed'])
        self.assertEqual(self.host_status(key), receipt)
        self.command['operation_key'] = key
        with self.assertRaisesRegex(AdapterError, 'purchase_not_prepared'):
            commit(self.ledger, self.scope, self.command, self.account, self.factory)
        self.assertEqual(self.writes, [])
        self.assertEqual(self.tables()['attempts'], [])

    def test_not_dispatched_retires_before_stale_material_check_preserving_original_receipt(self):
        self.prepare()
        key = self.command['operation_key'] = commit_key('purchase', 1)
        base = self.factory
        def insufficient(account):
            provider = base(account)
            provider.wallet = lambda: {'balance_cents': 0}
            return provider
        original = commit(self.ledger, self.scope, self.command, self.account, insufficient)
        before = self.tables()
        result = self.host_status(key, account_json='stale material is deliberately not parsed')
        self.assertEqual((result['original_state'], result['stop_reason']),
                         ('NOT_DISPATCHED', 'insufficient_wallet_balance'))
        self.assertEqual(self.tables()['mcd_commits'], before['mcd_commits'])
        self.assertEqual(json.loads(self.tables()['mcd_commits'][0][-1]), original)
        self.assertEqual(self.tables()['mcd_authorities'][0][-2], 'revoked')
        self.assertEqual(self.host_status(key), result)
        self.assertEqual(self.writes, [])

    def test_absent_wrong_key_and_wrong_scope_never_prove_non_dispatch(self):
        self.prepare()
        before = self.tables()
        for key, scope in ((commit_key('missing', 1), self.scope), ('commit-wrong', self.scope),
                           (commit_key('purchase', 1), self.scope | {'execution_id': 'other'}),
                           (commit_key('purchase', 1), self.scope | {'credential_revision': 2})):
            with self.subTest(key=key, scope=scope):
                self.assertEqual(self.host_status(key, scope), {'state': 'not_found', 'non_dispatch_proven': False})
                self.assertEqual(self.tables(), before)

    def test_old_epoch_status_does_not_close_newer_payable_preparation(self):
        self.prepare()
        self.ledger.abort(self.scope, 'abort', 'purchase', 1, 'quote_refresh')
        replacement = deepcopy(self.envelope)
        replacement.update(handoff_epoch=2, quote_revision=2)
        self.ledger.prepare(self.scope, 'prepare2', seal(replacement), self.provider)
        before = self.tables()
        self.assertEqual(self.host_status(commit_key('purchase', 1)), {'state': 'not_found', 'non_dispatch_proven': False})
        self.assertEqual(self.tables(), before)
        self.assertEqual(self.host_status(commit_key('purchase', 2))['handoff_epoch'], 2)

    def test_failed_negative_receipt_rolls_back_every_retirement_change(self):
        self.prepare()
        before = self.tables()
        with patch.object(self.ledger, '_record', side_effect=OSError('disk full')):
            with self.assertRaises(OSError):
                retire_status(self.ledger, self.scope, commit_key('purchase', 1))
        self.assertEqual(self.tables(), before)
        self.assertTrue(self.host_status(commit_key('purchase', 1))['purchase_closed'])

    def test_unknown_original_attempt_and_write_claim_are_never_retired(self):
        self.prepare()
        key = self.command['operation_key'] = commit_key('purchase', 1)
        class Lost(test_adapter.FixtureProvider):
            def create_order(inner, quote):
                raise OSError('lost order response')
        result = commit(self.ledger, self.scope, self.command, self.account, lambda account: Lost(account, self.source))
        self.assertEqual(result['state'], 'UNKNOWN')
        before = self.tables()
        self.assertEqual(self.host_status(key), result)
        self.assertEqual(self.tables(), before)

    def test_crash_before_reserve_can_retire_but_reserved_budget_cannot(self):
        self.prepare()
        key = self.command['operation_key'] = commit_key('purchase', 1)
        with patch.object(PurchaseStore, 'reserve', side_effect=SystemExit('crash')):
            with self.assertRaises(SystemExit):
                commit(self.ledger, self.scope, self.command, self.account, self.factory)
        result = self.host_status(key)
        self.assertEqual((result['original_state'], result['state']), ('started', 'aborted_before_write'))
        self.assertEqual(self.tables()['attempts'], [])

    def test_original_reservation_survives_status_even_without_any_write(self):
        self.prepare()
        key = self.command['operation_key'] = commit_key('purchase', 1)
        reserve = PurchaseStore.reserve
        def interrupted(store, *args):
            reserve(store, *args)
            raise OSError('reservation committed')
        with patch.object(PurchaseStore, 'reserve', interrupted):
            result = commit(self.ledger, self.scope, self.command, self.account, self.factory)
        before = self.tables()
        self.assertEqual(self.host_status(key), result)
        self.assertEqual(self.tables(), before)
        self.assertEqual(len(before['attempts']), 1)
        self.assertEqual(before['writes'], [])


if __name__ == '__main__':
    unittest.main()
