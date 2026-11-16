"""Synthetic native-host integration fixture. Network is unconditionally denied.

This file is outside the packaged adapter and never used by product helpers.
"""
from datetime import datetime,timedelta,timezone
import json
from pathlib import Path
import sys
from unittest.mock import patch

from ezmcd.models import Policy
from ezmcd.storage import Store
from ezmcd.transport import Transport
from sparkclaw_mcd import helper
from sparkclaw_mcd.canonical import digest
from sparkclaw_mcd.quotes import export_quote
from sparkclaw_mcd.purchase import review
from test_adapter import FixtureProvider


def main():
    root=Path(sys.argv[2]).resolve()
    material={"synthetic":True}
    binding={"deployment_id":"deployment","owner_id":"owner","client_id":"client","installation_id":"mac"}
    scope={**binding,"account_ref":"account","member_digest":"1"*64,"credential_revision":1,"execution_id":"execution"}
    original=Store(root/'original')
    fixture=Store(root/'provider')
    account={"alias":"account","identity":"1"*64,"provider":"mcd-cn","policy":Policy().model_dump()}
    def factory(account):
        from types import SimpleNamespace
        provider=FixtureProvider(account,fixture)
        provider.http=SimpleNamespace(close=lambda:None)
        return provider
    if sys.argv[1]=='init':
        original.add_account(account)
        request={"account":"account","store_code":"9000001","pickup_at":(datetime.now(timezone.utc)+timedelta(days=1)).replace(second=0,microsecond=0).isoformat(),"eat_type":"locker-in","max_amount_cny":"20.00"}
        envelope=export_quote(factory(account),request,scope,'purchase',1,1)
        return {"scope":scope,"envelope":envelope,"review":review(scope,'purchase',envelope['quote']),"material_digest":digest(material),"account_json":json.dumps(original.account('account')),"source":{"root":str(original.root),"alias":"account"}}
    request=json.loads(sys.stdin.buffer.read(320*1024+1))
    # Mock only the external provider and acquisition; actual scope validation,
    # original-ledger adoption, quote import, spending and write claims execute.
    with patch.object(Transport,'_open',side_effect=AssertionError('fixture cannot use network')),\
         patch('sparkclaw_mcd.imported_account.acquire',return_value=(None,material)),\
         patch.object(helper,'provider',side_effect=lambda *_:factory(account)),\
         patch('ezmcd.provider.McdProvider',side_effect=factory):
        result=helper.dispatch('host',request)
    return result


if __name__=='__main__':print(json.dumps(main(),ensure_ascii=False))
