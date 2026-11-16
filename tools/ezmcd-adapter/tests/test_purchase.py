from copy import deepcopy
import unittest
from unittest.mock import patch

from ezmcd.errors import EzMcdError
from ezmcd.models import Policy
from sparkclaw_mcd.errors import AdapterError
from sparkclaw_mcd.ledger import PrepareLedger
from sparkclaw_mcd.purchase import commit, review, status, PurchaseStore
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
        self.assertEqual(self.writes,['order','preorder','consume'])
        cold=PrepareLedger(self.ledger.root,self.installation)
        second=commit(cold,self.scope,self.command,self.account,self.factory)
        self.assertEqual(second['state'],'PAID')
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
        import json
        q=self.envelope['quote']
        with self.ledger.store.connect() as db:
            db.execute('INSERT INTO attempts VALUES (?,?,?,?,?,?,?,?)',('original',self.scope['member_digest'],'old','old-intent',q['pickup_at'][:10],4000,'UNKNOWN',json.dumps({'store_code':'9000002','pickup_at':q['pickup_at']})))
        self.assertEqual(commit(self.ledger,self.scope,self.command,self.account,self.factory)['state'],'UNKNOWN')
        self.assertEqual(self.writes,[])
        with self.ledger.store.connect() as db:self.assertEqual(db.execute('SELECT count(*) FROM attempts').fetchone()[0],1)


if __name__=='__main__':unittest.main()
