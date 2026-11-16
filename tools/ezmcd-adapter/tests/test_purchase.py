from copy import deepcopy
import json
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from ezmcd.errors import EzMcdError
from ezmcd.models import Policy
from sparkclaw_mcd.errors import AdapterError
from sparkclaw_mcd.ledger import PrepareLedger
from sparkclaw_mcd.purchase import commit, review, status, PurchaseStore
from sparkclaw_mcd.helper import dispatch
from sparkclaw_mcd.quotes import export_quote
from sparkclaw_mcd.receipt import order_fields
import test_adapter
from test_adapter import FixtureProvider


class PurchaseTests(unittest.TestCase):
    setUp = test_adapter.AdapterTests.setUp

    def prepare(self):
        self.account['policy'] = Policy(allow_checkout=False, daily_budget_cents=4000, max_orders_per_day=2).model_dump()
        self.ledger.store.add_account(self.account)
        self.ledger.prepare(self.scope,'prepare',self.envelope,self.provider)
        self.authority={'approval_id':'approval','approval_digest':'a'*64,'expires_at_ms':self.now+90000,
                        'review':review(self.scope,'purchase',self.envelope['quote'])}
        self.command={'operation_key':'commit','purchase_id':'purchase','handoff_epoch':1,'authority':self.authority}
        self.writes=[]
        parent=self
        class Provider(FixtureProvider):
            def create_order(self,q):
                parent.writes.append('order')
                return super().create_order(q)
            def preorder(self,body):
                parent.writes.append('preorder')
                return super().preorder(body)
            def consume(self,inputs):
                parent.writes.append('consume')
                return super().consume(inputs)
        self.factory=lambda account:Provider(account,self.source)

    def test_matching_native_authority_consumes_once_with_original_journal(self):
        self.prepare()
        before=self.ledger.store.account('account')
        first=commit(self.ledger,self.scope,self.command,self.account,self.factory)
        self.assertEqual(first['state'],'PAID',first)
        self.assertTrue(first['terminal_verified'])
        self.assertEqual(first['pickup_code'], 'DEMO-001')
        self.assertEqual(first['order_no'], json.loads(first['result_json'])['order_id'])
        self.assertEqual(self.writes,['order','preorder','consume'])
        cold=PrepareLedger(self.ledger.root,self.installation)
        second=commit(cold,self.scope,self.command,self.account,self.factory)
        self.assertEqual(second['state'],'PAID')
        self.assertEqual(second['pickup_code'], first['pickup_code'])
        self.assertEqual(second['order_no'], first['order_no'])
        self.assertEqual(self.writes,['order','preorder','consume'])
        self.assertEqual(self.ledger.store.account('account'),before)
        with self.ledger.store.connect() as db:
            self.assertEqual(db.execute('SELECT state FROM mcd_authorities').fetchone()[0],'consumed')
            self.assertEqual(db.execute('SELECT count(*) FROM attempts').fetchone()[0],1)
            self.assertEqual(db.execute('SELECT count(*) FROM writes').fetchone()[0],3)

    def test_drift_expiry_wrong_epoch_and_abort_prevent_every_write(self):
        self.prepare()
        for mutate in (lambda c:c['authority']['review'].update(amount_cents=1),
                       lambda c:c['authority'].update(expires_at_ms=self.now),
                       lambda c:c.update(handoff_epoch=2)):
            command=deepcopy(self.command);mutate(command)
            with self.assertRaises(AdapterError):commit(self.ledger,self.scope,command,self.account,self.factory)
        self.ledger.abort(self.scope,'abort','purchase',1,'user_abandonment')
        with self.assertRaises(AdapterError):commit(self.ledger,self.scope,self.command,self.account,self.factory)
        self.assertEqual(self.writes,[])

    def test_lost_order_reply_is_permanent_query_only_and_cannot_abort(self):
        self.prepare()
        class Lost(FixtureProvider):
            def create_order(inner,q):
                self.writes.append('order')
                raise OSError('reply lost')
        factory=lambda account:Lost(account,self.source)
        first=commit(self.ledger,self.scope,self.command,self.account,factory)
        self.assertEqual(first['state'],'UNKNOWN')
        cold=PrepareLedger(self.ledger.root,self.installation)
        commit(cold,self.scope,self.command,self.account,factory)
        status(cold,self.scope,'commit',self.account,factory)
        self.assertEqual(self.writes,['order'])
        self.assertEqual(cold.abort(self.scope,'abort','purchase',1,'user_abandonment')['state'],'reconciliation_required')
        drift=deepcopy(self.command);drift['authority']['approval_id']='replacement'
        with self.assertRaisesRegex(AdapterError,'operation_key_conflict'):commit(cold,self.scope,drift,self.account,factory)

    def test_crash_before_reserve_does_not_redispatch_and_abort_proves_no_attempt(self):
        self.prepare()
        with patch.object(PurchaseStore,'reserve',side_effect=SystemExit('crash')):
            with self.assertRaises(SystemExit):commit(self.ledger,self.scope,self.command,self.account,self.factory)
        self.assertEqual(commit(self.ledger,self.scope,self.command,self.account,self.factory)['state'],'reconciliation_required')
        self.assertEqual(self.writes,[])
        self.assertEqual(self.ledger.abort(self.scope,'abort','purchase',1,'quote_refresh')['state'],'aborted_before_write')
        with self.ledger.store.connect() as db:self.assertEqual(db.execute('SELECT state FROM mcd_authorities').fetchone()[0],'unused')

    def test_existing_unknown_budget_is_preserved(self):
        self.prepare()
        q=self.envelope['quote']
        with self.ledger.store.connect() as db:
            db.execute('INSERT INTO attempts VALUES (?,?,?,?,?,?,?,?)',('original',self.scope['member_digest'],'old','old-intent',q['pickup_at'][:10],4000,'UNKNOWN',json.dumps({'store_code':'9000002','pickup_at':q['pickup_at']})))
        result = commit(self.ledger,self.scope,self.command,self.account,self.factory)
        self.assertEqual(result['state'],'NOT_DISPATCHED')
        self.assertEqual(result['stop_reason'], 'daily_order_or_budget_limit_reached')
        self.assertEqual(self.writes,[])
        self.assertEqual(self.ledger.abort(self.scope, 'abort', 'purchase', 1, 'execution_termination')['state'], 'aborted_before_write')
        with self.ledger.store.connect() as db:
            original = db.execute('SELECT id,amount,state FROM attempts').fetchall()
            self.assertEqual([tuple(row) for row in original], [('original', 4000, 'UNKNOWN')])

    def test_paid_without_code_queries_original_order_then_gets_code_without_writes(self):
        self.prepare()
        base = self.factory
        def pending(account):
            provider = base(account)
            original_status = provider.status
            def without_code(attempt):
                result = original_status(attempt)
                result['order'].pop('pickupCode', None)
                return result
            provider.status = without_code
            return provider
        first = commit(self.ledger, self.scope, self.command, self.account, pending)
        self.assertEqual((first['state'], first['terminal_verified'], first['pickup_code']), ('PAID', True, ''))
        self.assertTrue(first['order_no'].startswith('DEMO-'))
        cold = PrepareLedger(self.ledger.root, self.installation)
        second = status(cold, self.scope, 'commit', self.account, self.factory)
        self.assertEqual((second['pickup_code'], second['order_no']), ('DEMO-001', first['order_no']))
        self.assertEqual(self.writes, ['order', 'preorder', 'consume'])

    def test_unverified_or_malformed_numbers_never_become_paid_reply_fields(self):
        for state, verified in (('UNKNOWN', True), ('CREATED', False), ('PAID', False), ('PAID', 1), ('CANCELLED', True)):
            projected = order_fields({'state': state, 'paid_verified': verified, 'pickup_code': 'FAKE', 'order_id': 'FAKE'})
            self.assertEqual((projected['pickup_code'], projected['order_no']), ('', ''))
        for value in (None, 123, True, {'pickupCode': 'FAKE'}, 'x' * 129, 'line\nbreak', '   '):
            projected = order_fields({'state': 'PAID', 'paid_verified': True, 'pickup_code': value, 'order_id': value})
            self.assertEqual((projected['pickup_code'], projected['order_no']), ('', ''))

    def test_successful_payment_response_and_pickup_code_do_not_prove_paid(self):
        self.prepare()
        class Unsettled(FixtureProvider):
            def consume(inner, inputs):
                return {'http_status': 200, 'response': {'code': '0'}}
            def status(inner, attempt):
                result = super().status(attempt)
                result['order']['pickupCode'] = 'UNVERIFIED'
                return result
        with patch('ezmcd.service.time.sleep'):
            result = commit(self.ledger, self.scope, self.command, self.account,
                            lambda account: Unsettled(account, self.source))
        self.assertEqual((result['state'], result['terminal_verified']), ('UNKNOWN', False))
        self.assertEqual((result['pickup_code'], result['order_no']), ('', ''))
        self.assertEqual(json.loads(result['result_json'])['pickup_code'], 'UNVERIFIED')
        self.assertEqual(self.ledger.abort(self.scope, 'abort', 'purchase', 1, 'execution_termination')['state'],
                         'reconciliation_required')

    def test_verified_cancellation_is_terminal_without_pickup_reply_fields(self):
        self.prepare()
        class Cancelled(FixtureProvider):
            def consume(inner, inputs):
                return {'http_status': 200, 'response': {'code': '0'}}
            def status(inner, attempt):
                result = super().status(attempt)
                result['order'].update(orderStatusTitle='已取消', pickupCode='CANCELLED-CODE')
                return result
        result = commit(self.ledger, self.scope, self.command, self.account,
                        lambda account: Cancelled(account, self.source))
        self.assertEqual((result['state'], result['terminal_verified']), ('CANCELLED', True))
        self.assertEqual((result['pickup_code'], result['order_no']), ('', ''))
        self.assertEqual(self.provider.wallet()['balance_cents'], 10000)

    def test_original_order_query_projects_verified_rows_and_native_limit_without_network(self):
        self.prepare()
        self.account['policy']['max_order_cents'] = 1700
        self.ledger.store.update_account(self.account)
        receipt = commit(self.ledger, self.scope, self.command, self.account, self.factory)
        with self.ledger.store.connect() as db:
            unknown = {'attempt_id': 'old-unknown', 'state': 'UNKNOWN', 'quote_id': 'missing', 'amount_cents': 500,
                       'paid_verified': False, 'pickup_code': 'UNVERIFIED', 'order_id': 'unverified-order'}
            db.execute('INSERT INTO attempts VALUES (?,?,?,?,?,?,?,?)',
                       ('old-unknown', self.scope['member_digest'], 'old', 'old-intent', '2026-01-01', 500, 'UNKNOWN', json.dumps(unknown)))
        request = {'operation': 'host.orders', 'params': {
            'source': {'root': str(self.ledger.root), 'alias': 'account'}, 'binding': self.scope,
            'material_digest': None, 'initialize': False, 'request': {'limit': 20, 'offset': 0}}}
        with patch('ezmcd.provider.McdProvider', side_effect=AssertionError('history must not connect')):
            result = dispatch('host', request)
        self.assertEqual(result['policy'], {'max_order_cents': 1700})
        self.assertEqual((result['source'], result['live_status_checked'], result['total']), ('local_project_journal', False, 2))
        paid, unknown = result['orders']
        self.assertEqual((paid['pickup_code'], paid['order_no'], paid['terminal_verified']),
                         (receipt['pickup_code'], receipt['order_no'], True))
        self.assertEqual((unknown['state'], unknown['pickup_code'], unknown['order_no'], unknown['terminal_verified']),
                         ('UNKNOWN', '', '', False))
        self.assertNotIn('account_json', result)
        self.assertEqual(self.writes, ['order', 'preorder', 'consume'])

    def test_insufficient_balance_is_durable_not_dispatched_and_abortable(self):
        self.prepare()
        base = self.factory
        def insufficient(account):
            provider = base(account)
            provider.wallet = lambda: {'balance_cents': 0}
            return provider
        receipt = commit(self.ledger, self.scope, self.command, self.account, insufficient)
        self.assertEqual((receipt['state'], receipt['non_dispatch_proven'], receipt['stop_reason']),
                         ('NOT_DISPATCHED', True, 'insufficient_wallet_balance'))
        self.assertFalse(receipt['terminal_verified'])
        cold = PrepareLedger(self.ledger.root, self.installation)
        def forbidden(_):
            raise AssertionError('a rejected command must not construct a provider again')
        self.assertEqual(status(cold, self.scope, 'commit', self.account, forbidden), receipt)
        self.assertEqual(commit(cold, self.scope, self.command, self.account, forbidden), receipt)
        aborted = cold.abort(self.scope, 'abort', 'purchase', 1, 'execution_termination')
        self.assertEqual((aborted['state'], aborted['non_dispatch_proven']), ('aborted_before_write', True))
        self.assertEqual(self.writes, [])
        with cold.store.connect() as db:
            self.assertEqual(db.execute('SELECT count(*) FROM attempts').fetchone()[0], 0)
            self.assertEqual(db.execute('SELECT count(*) FROM writes').fetchone()[0], 0)
            self.assertEqual(db.execute('SELECT state FROM mcd_authorities').fetchone()[0], 'revoked')

    def test_provider_setup_failure_does_not_lose_non_dispatch_evidence(self):
        self.prepare()
        def failed(_):
            raise OSError('private credential must not escape')
        result = commit(self.ledger, self.scope, self.command, self.account, failed)
        self.assertEqual(result['state'], 'NOT_DISPATCHED')
        self.assertNotIn('private credential', json.dumps(result))
        self.assertTrue(result['non_dispatch_proven'])

    def test_transport_close_failure_preserves_verified_payment(self):
        self.prepare()
        base = self.factory
        def failed_close(account):
            provider = base(account)
            def close():
                raise OSError('close failed')
            provider.http = SimpleNamespace(close=close)
            return provider
        result = commit(self.ledger, self.scope, self.command, self.account, failed_close)
        self.assertEqual((result['state'], result['pickup_code']), ('PAID', 'DEMO-001'))

    def test_reservation_metadata_gap_is_unknown_and_preserves_original_budget(self):
        self.prepare()
        reserve = PurchaseStore.reserve
        def interrupted(store, *args):
            reserve(store, *args)
            with store.connect() as db:
                db.execute('UPDATE mcd_commits SET attempt=NULL')
            raise OSError('reply lost after original reservation')
        with patch.object(PurchaseStore, 'reserve', interrupted):
            result = commit(self.ledger, self.scope, self.command, self.account, self.factory)
        self.assertEqual((result['state'], result['non_dispatch_proven']), ('UNKNOWN', False))
        self.assertEqual(self.ledger.abort(self.scope, 'abort', 'purchase', 1, 'quote_refresh')['state'], 'reconciliation_required')
        self.assertEqual(self.writes, [])
        with self.ledger.store.connect() as db:
            self.assertEqual(db.execute('SELECT count(*) FROM attempts').fetchone()[0], 1)
            self.assertEqual(db.execute('SELECT count(*) FROM request_keys').fetchone()[0], 1)
            self.assertEqual(db.execute('SELECT count(*) FROM writes').fetchone()[0], 0)

    def test_claim_before_failed_dispatch_is_unknown_and_cannot_abort(self):
        self.prepare()
        claim = PurchaseStore.claim
        def interrupted(store, *args):
            claim(store, *args)
            raise OSError('process interrupted before network call')
        with patch.object(PurchaseStore, 'claim', interrupted):
            result = commit(self.ledger, self.scope, self.command, self.account, self.factory)
        self.assertEqual(result['state'], 'UNKNOWN')
        self.assertFalse(result['non_dispatch_proven'])
        self.assertEqual(self.ledger.abort(self.scope, 'abort', 'purchase', 1, 'quote_refresh')['state'], 'reconciliation_required')
        self.assertEqual(self.writes, [])
        with self.ledger.store.connect() as db:
            self.assertEqual(db.execute('SELECT state FROM mcd_authorities').fetchone()[0], 'consumed')
            self.assertEqual(db.execute('SELECT count(*) FROM writes').fetchone()[0], 1)

    def test_coupon_free_requote_aborts_old_preparation_before_replacement(self):
        class Coupon(FixtureProvider):
            def _checkout_data(inner, args):
                cart, confirmation, daypart = super()._checkout_data(args)
                cart['products'][0]['coupon'] = {'id': 'coupon', 'code': 'code', 'promotionId': 'offer', 'rightCardId': 'card'}
                confirmation['productPriceInfo']['cartProductList'][0]['couponList'] = [
                    {'couponId': 'coupon', 'couponCode': 'code', 'promotionId': 'offer', 'cardId': 'card', 'couponName': 'no-discount-coupon'}]
                return cart, confirmation, daypart
        provider = Coupon(self.account, self.source)
        self.envelope = export_quote(provider, self.request, self.scope, 'purchase', 1, 1, clock=lambda: self.now / 1000)
        self.provider = provider
        self.prepare()
        self.assertEqual(self.envelope['quote']['discount_names'], ['no-discount-coupon'])
        self.assertEqual(self.envelope['quote']['discount_cents'], 0)
        plain = FixtureProvider(self.account, self.source)
        replacement = export_quote(plain, self.request, self.scope, 'purchase', 2, 2, clock=lambda: self.now / 1000)
        with self.assertRaisesRegex(AdapterError, 'member_preparation_unresolved'):
            self.ledger.prepare(self.scope, 'prepare2', replacement, plain)
        # Failure to commit the abort receipt also rolls back the retirement.
        with patch.object(self.ledger, '_record', side_effect=OSError('disk full')):
            with self.assertRaises(OSError):
                self.ledger.abort(self.scope, 'abort', 'purchase', 1, 'quote_refresh')
        with self.assertRaisesRegex(AdapterError, 'member_preparation_unresolved'):
            self.ledger.prepare(self.scope, 'prepare2', replacement, plain)
        aborted = self.ledger.abort(self.scope, 'abort', 'purchase', 1, 'quote_refresh')
        cold = PrepareLedger(self.ledger.root, self.installation)
        self.assertEqual(cold.abort(self.scope, 'abort', 'purchase', 1, 'quote_refresh'), aborted)
        self.assertTrue(aborted['non_dispatch_proven'])
        with self.assertRaisesRegex(AdapterError, 'purchase_not_prepared'):
            commit(cold, self.scope, self.command, self.account, self.factory)
        cold.prepare(self.scope, 'prepare2', replacement, plain)
        self.assertEqual(replacement['quote']['discount_names'], [])
        self.command['operation_key'] = 'commit2'
        self.command['handoff_epoch'] = 2
        self.authority['review'] = review(self.scope, 'purchase', replacement['quote'])
        result = commit(cold, self.scope, self.command, self.account, self.factory)
        self.assertEqual(result['state'], 'PAID')
        with cold.store.connect() as db:
            self.assertEqual([tuple(row) for row in db.execute('SELECT epoch,state FROM mcd_preparations ORDER BY epoch')],
                             [(1, 'aborted_before_write'), (2, 'PAID')])
            self.assertEqual(db.execute('SELECT count(*) FROM attempts').fetchone()[0], 1)
        self.assertEqual(self.writes, ['order', 'preorder', 'consume'])


if __name__=='__main__':unittest.main()
