from copy import deepcopy
from datetime import datetime, timedelta, timezone
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import Mock, patch

from ezmcd.errors import EzMcdError
from ezmcd.models import CheckoutInput
from sparkclaw_mcd.cart_session import CartTransport, ExecutionStore, execute, CART_OPERATIONS
from sparkclaw_mcd.canonical import digest
from sparkclaw_mcd.errors import AdapterError
from sparkclaw_mcd.sessions import seal
import test_sessions


class CartSessionTests(unittest.TestCase):
    def setUp(self):
        case = test_sessions.SessionTests(); case.setUp()
        self.value, self.execution = case.value, case.execution
        self.value['operations'] = sorted(CART_OPERATIONS)
        self.value = seal(self.value)
        self.tmp = tempfile.TemporaryDirectory(); self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve() / 'execution'
        self.request = {'account': self.value['identity']['account_ref'], 'store_code': '3330613',
                        'pickup_at': (datetime.now(timezone.utc) + timedelta(days=1)).replace(second=0, microsecond=0).isoformat(),
                        'max_amount_cny': '20.00', 'eat_type': 'locker-in'}
        self.request=CheckoutInput.model_validate(self.request).model_dump()

    def authority(self, operation, arguments):
        return {'operation_key': 'original-key', 'input_digest': digest({'operation': operation, 'arguments': arguments}),
                'deadline_ms': self.execution['deadline_ms']}

    def test_wrong_authority_or_scope_never_creates_store_or_provider(self):
        args = {'request': self.request, 'items': [{'product_code':'1', 'quantity':1}]}
        with patch('sparkclaw_mcd.cart_session.McdProvider') as provider:
            for authority in (None, self.authority('cart_clear', args)):
                with self.assertRaises(AdapterError):
                    execute(self.value, self.execution, 'cart_add_simple', args, self.root, authority)
            provider.assert_not_called(); self.assertFalse(self.root.exists())

    def test_transport_only_one_exact_write_even_after_unknown_response(self):
        transport = CartTransport(self.value['material'], operation='cart_clear', deadline_ms=self.execution['deadline_ms'])
        with patch.object(transport, '_transmit', side_effect=OSError('lost')) as send:
            for method, path in [('POST','/bff/order/orders'), ('PUT','/bff/cart/carts'), ('POST','/bff/payment/wallet/consume')]:
                with self.assertRaises(AdapterError): transport.send(method,path)
            send.assert_not_called()
            with self.assertRaises(OSError): transport.send('PUT','/bff/cart/carts/empty')
            with self.assertRaises(AdapterError): transport.send('PUT','/bff/cart/carts/empty')
            self.assertEqual(send.call_count,1)

    def test_preflight_error_proves_nondispatch_but_timeout_after_write_does_not(self):
        args={'request':self.request,'cart_ref':'cart-'+'a'*32}
        def provider_factory(account, transport):
            provider=Mock(); provider.wallet.return_value={'balance_cents':10000}
            provider.cart_state.return_value=({'items':[{'product_code':'1'}]}, {}, '1'*64)
            provider.clear_cart.side_effect=lambda body: transport.send('PUT','/bff/cart/carts/empty',body=body)
            return provider
        with patch('sparkclaw_mcd.cart_session.McdProvider', side_effect=provider_factory):
            early=execute(self.value,self.execution,'cart_clear',args,self.root,self.authority('cart_clear',args))
            self.assertEqual(early['dispatch_state'],'not_dispatched')
            account={'alias':self.request['account']}
            store=ExecutionStore(self.root,account,self.execution)
            store.save_cart_snapshot(self.value['identity']['member_digest'],args['cart_ref'],
                 {k:self.request[k] for k in ('store_code','pickup_at','eat_type')}, '1'*64,self.execution['deadline_ms']/1000)
            with patch.object(CartTransport,'_transmit',side_effect=OSError('lost')):
                late=execute(self.value,self.execution,'cart_clear',args,self.root,self.authority('cart_clear',args))
            self.assertEqual(late['dispatch_state'],'unknown'); self.assertTrue(late['write_claimed'])
        self.assertFalse(list((self.root/'accounts').iterdir()))
        with sqlite3.connect(self.root/'journal.sqlite3') as conn:
            self.assertEqual(conn.execute('SELECT count(*) FROM attempts').fetchone()[0],0)
            self.assertEqual(conn.execute('SELECT count(*) FROM writes').fetchone()[0],1)

    def test_missing_tables_are_not_recreated_or_rebound(self):
        ExecutionStore(self.root, {'alias':'account'}, self.execution)
        with sqlite3.connect(self.root/'journal.sqlite3') as conn: conn.execute('DROP TABLE writes')
        with self.assertRaisesRegex(AdapterError,'missing_recovery'):
            ExecutionStore(self.root, {'alias':'account'}, self.execution)
        with sqlite3.connect(self.root/'journal.sqlite3') as conn:
            self.assertFalse(conn.execute("SELECT 1 FROM sqlite_master WHERE name='writes'").fetchone())
        foreign=deepcopy(self.execution);foreign['execution_id']='other'
        with self.assertRaises(AdapterError): ExecutionStore(self.root, {'alias':'account'},foreign)
