import copy
import json
import ssl
import subprocess
import tempfile
import threading
import unittest
import urllib.request
import http.client
from unittest import mock
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from scripts.e2_reranker_evidence_v3 import EvidenceFailure, V3ContractBundle, canonical_bytes
from scripts.e2_reranker_fake_smoke_v3 import _NoRedirect
from scripts.e2_reranker_live_smoke_v3 import LiveNativeRerankClient, durable_new, native_counters
from scripts.e2_reranker_https_v3 import Boundary, backend_identity


def metrics(queries="10.0", hits="4e0", labels='engine="0",model_name="sparkclaw-reranker"'):
    return (f'vllm:prefix_cache_queries_total{{{labels}}} {queries}\n'
            f'vllm:prefix_cache_hits_total{{{labels}}} {hits}\n').encode()


class NativeMetricsTests(unittest.TestCase):
    def test_native_zero_and_nonzero_scientific_notation(self):
        self.assertEqual(native_counters(metrics()), {"prefix_cache_queries_total":10,"prefix_cache_hits_total":4})
        self.assertEqual(native_counters(metrics("0.0","0.0"))["prefix_cache_hits_total"],0)
        self.assertEqual(native_counters(metrics(labels='model_name="sparkclaw-reranker",engine="0"'))["prefix_cache_queries_total"],10)

    def test_ambiguous_missing_nonfinite_and_wrong_identity_rejected(self):
        cases = [b"", metrics()+metrics(), metrics().splitlines()[0]+b"\n",
            metrics("nan"), metrics("inf"), metrics("-1"), metrics("-0"), metrics("1.5","0"),
            metrics("9007199254740992","0"), metrics("0","1"),
            metrics(labels='engine="1",model_name="sparkclaw-reranker"'),
            metrics(labels='engine="0",model_name="another-model"'),
            metrics(labels='engine="0",engine="0",model_name="sparkclaw-reranker"'),
            metrics(labels='engine="0",model_name="sparkclaw-reranker",extra="x"'),
            b"vllm:prefix_cache_queries_total 0\nvllm:prefix_cache_hits_total 0\n"]
        for raw in cases:
            with self.subTest(raw=raw), self.assertRaises(EvidenceFailure): native_counters(raw)

    def test_central_fixture_rejected_before_transport_setup(self):
        bundle=V3ContractBundle.load()
        raw=bundle.pinned_raw['fixtures/artifacts/valid-synthetic-deployment-manifest.json']
        import hashlib
        with self.assertRaises(EvidenceFailure) as caught:
            LiveNativeRerankClient(bundle,manifest=json.loads(raw),manifest_raw=raw,
                expected_sha256=hashlib.sha256(raw).hexdigest(),expected_size=len(raw),
                ca_file=Path('/absent'),config_file=Path('/absent'),evidence_dir=Path('/absent'),ledger=Path('/absent'))
        self.assertEqual(caught.exception.code,"synthetic_artifact_not_live")


class Handler(BaseHTTPRequestHandler):
    post_count=0
    status=200
    def log_message(self,*args): pass
    def do_GET(self):
        self.send_response(type(self).status)
        if type(self).status==302:self.send_header('Location','https://127.0.0.1/forbidden')
        self.send_header('Content-Length','0');self.end_headers()
    def do_POST(self):
        type(self).post_count+=1
        self.rfile.read(int(self.headers['Content-Length']))
        self.send_response(500);self.send_header('Content-Length','0');self.end_headers()


class LiveTransportTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp=tempfile.TemporaryDirectory();cls.root=Path(cls.tmp.name)
        cls.cert=cls.root/'cert.pem';key=cls.root/'key.pem'
        subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1',
            '-subj','/CN=localhost','-addext','subjectAltName=IP:127.0.0.1',
            '-keyout',str(key),'-out',str(cls.cert)],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        cls.server=ThreadingHTTPServer(('127.0.0.1',0),Handler)
        context=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);context.load_cert_chain(cls.cert,key)
        cls.server.socket=context.wrap_socket(cls.server.socket,server_side=True)
        cls.thread=threading.Thread(target=cls.server.serve_forever,daemon=True);cls.thread.start()
    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown();cls.server.server_close();cls.thread.join();cls.tmp.cleanup()
    def client(self, trusted=True):
        # Exercise transport directly. No fixture is represented as a live deployment.
        c=LiveNativeRerankClient.__new__(LiveNativeRerankClient)
        c.origin=f'https://127.0.0.1:{self.server.server_port}';c.timeout=2;c.actual_order=[]
        context=ssl.create_default_context(cafile=str(self.cert)) if trusted else ssl.create_default_context()
        c.opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),_NoRedirect(),urllib.request.HTTPSHandler(context=context))
        c.evidence_dir=Path(tempfile.mkdtemp(dir=self.root));c.ledger=c.evidence_dir/'post-ledger.json'
        c.manifest={'request_contract':{'headers':[{'value':'transport-test'}]}};c.manifest_raw=b'transport-test'
        return c
    def test_tls_trust_and_redirect_fail_before_any_post(self):
        Handler.status=200;Handler.post_count=0
        c=self.client(False)
        with self.assertRaises(EvidenceFailure):c._request('health_pre','GET','/health')
        self.assertFalse(c.ledger.exists())
        c=self.client();self.assertEqual(c._request('health_pre','GET','/health')[0],b'')
        Handler.status=302
        with self.assertRaises(EvidenceFailure) as caught:c._request('version_pre','GET','/version')
        self.assertEqual(caught.exception.code,'redirect_forbidden');self.assertEqual(Handler.post_count,0)
    def test_failed_post_consumes_durable_attempt_and_cannot_repeat(self):
        Handler.post_count=0;c=self.client()
        with self.assertRaises(EvidenceFailure):c._request('rerank','POST','/v1/rerank',body=b'{}')
        self.assertEqual(json.loads(c.ledger.read_bytes())['state'],'consumed_before_io')
        with self.assertRaises(FileExistsError):c._request('rerank','POST','/v1/rerank',body=b'{}')
        self.assertEqual(Handler.post_count,1)
        with self.assertRaises(FileExistsError):durable_new(c.ledger,b'overwrite')


class RawBackend(BaseHTTPRequestHandler):
    calls=[]
    raw=b'{"native": true, "bytes": "preserved"}\n'
    def log_message(self,*args):pass
    def do_POST(self):
        type(self).calls.append((self.path,self.rfile.read(int(self.headers['Content-Length'])),self.headers.get('X-Request-Id')))
        self.send_response(200);self.send_header('Content-Type','application/json')
        self.send_header('Content-Length',str(len(self.raw)));self.end_headers();self.wfile.write(self.raw)


class HTTPSBoundaryTests(unittest.TestCase):
    def test_docker_mount_enumeration_order_is_not_deployment_drift(self):
        info={'State':{'Running':True,'StartedAt':'fixed'},'Id':'fixed','Image':'fixed',
            'Config':{'Image':'fixed','Entrypoint':['vllm'],'Cmd':['serve'],'User':'1000:1000'},
            'HostConfig':{'ReadonlyRootfs':True,'CapDrop':['ALL'],'SecurityOpt':['no-new-privileges']},
            'NetworkSettings':{'Networks':{'internal':{}}},
            'Mounts':[{'Source':'/model','Destination':'/model','RW':False},{'Source':'/cache','Destination':'/cache','RW':True}]}
        reversed_info=copy.deepcopy(info);reversed_info['Mounts'].reverse()
        with mock.patch('scripts.e2_reranker_https_v3.subprocess.check_output',side_effect=[json.dumps([info]).encode(),json.dumps([reversed_info]).encode()]):
            self.assertEqual(backend_identity('fake'),backend_identity('fake'))
        changed=copy.deepcopy(info);changed['Mounts'][0]['RW']=True
        with mock.patch('scripts.e2_reranker_https_v3.subprocess.check_output',side_effect=[json.dumps([info]).encode(),json.dumps([changed]).encode()]):
            self.assertNotEqual(backend_identity('fake'),backend_identity('fake'))

    def test_exact_body_identity_drift_and_single_forward(self):
        # The boundary component uses fixtures only against this explicitly fake backend.
        bundle=V3ContractBundle.load();manifest=json.loads(bundle.pinned_raw['fixtures/artifacts/valid-synthetic-deployment-manifest.json'])
        with tempfile.TemporaryDirectory() as directory:
            backend=ThreadingHTTPServer(('127.0.0.1',0),RawBackend)
            boundary=ThreadingHTTPServer(('127.0.0.1',0),Boundary)
            boundary.state={'manifest':manifest,'manifest_sha':'a'*64,'record':{'backend':{'identity':'expected'}},
                'container':'fake-only','ledger':Path(directory)/'proxy-ledger.json','backend_port':backend.server_port}
            threads=[threading.Thread(target=s.serve_forever,daemon=True) for s in [backend,boundary]]
            for t in threads:t.start()
            def request(method,path,body=None,headers=None):
                c=http.client.HTTPConnection('127.0.0.1',boundary.server_port,timeout=3)
                try:
                    c.request(method,path,body=body,headers=headers or {});r=c.getresponse();return r.status,r.read(),dict(r.headers)
                finally:c.close()
            try:
                RawBackend.calls=[]
                body=canonical_bytes(manifest['request_contract']['body'],stage='test')
                headers={'X-Request-Id':manifest['request_contract']['headers'][0]['value']}
                with mock.patch('scripts.e2_reranker_https_v3.backend_identity',return_value={'identity':'expected'}):
                    self.assertEqual(request('POST','/v1/rerank',b'wrong',headers)[0],400)
                    status,raw,received=request('POST','/v1/rerank',body,headers)
                    self.assertEqual((status,raw),(200,RawBackend.raw))
                    self.assertEqual(received['X-SparkClaw-Evidence-Manifest-SHA256'],'a'*64)
                    self.assertEqual(request('POST','/v1/rerank',body,headers)[0],409)
                self.assertEqual(RawBackend.calls,[('/v1/rerank',body,headers['X-Request-Id'])])
                with mock.patch('scripts.e2_reranker_https_v3.backend_identity',return_value={'identity':'drifted'}):
                    status,_,received=request('GET','/health')
                    self.assertEqual(status,503);self.assertNotIn('X-SparkClaw-Evidence-Manifest-SHA256',received)
            finally:
                for s in [backend,boundary]:s.shutdown();s.server_close()
                for t in threads:t.join()


if __name__=='__main__':unittest.main()
