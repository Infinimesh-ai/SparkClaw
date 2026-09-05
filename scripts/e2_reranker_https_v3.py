#!/usr/bin/env python3
"""Loopback-only, body-preserving HTTPS identity boundary for the GB10 E2 probe."""
import argparse
import hashlib
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import ssl
import subprocess

from scripts.e2_reranker_evidence_v3 import (
    V3ContractBundle, canonical_bytes, pinned_canonical_artifact, MANIFEST_HEADER, DEPLOYMENT_HEADER,
)
from scripts.e2_reranker_live_smoke_v3 import durable_new


def backend_identity(name: str) -> dict:
    info=json.loads(subprocess.check_output(['docker','inspect',name],timeout=10))[0]
    if not info['State']['Running']:
        raise ValueError('backend unavailable')
    # Only non-secret deployment facts. No runtime environment values enter evidence.
    return {'container_id':info['Id'],'image_id':info['Image'],'image_reference':info['Config']['Image'],
        'started_at':info['State']['StartedAt'],'entrypoint':info['Config']['Entrypoint'],
        'cmd':info['Config']['Cmd'],'user':info['Config']['User'],
        'readonly_rootfs':info['HostConfig']['ReadonlyRootfs'],'cap_drop':info['HostConfig']['CapDrop'],
        'security_opt':info['HostConfig']['SecurityOpt'],
        'mounts':[{'source':m['Source'],'destination':m['Destination'],'rw':m['RW']} for m in info['Mounts']],
        'networks':sorted(info['NetworkSettings']['Networks'])}


class Boundary(BaseHTTPRequestHandler):
    server_version='E2Evidence'
    sys_version=''
    def setup(self):
        super().setup()
        self.connection.settimeout(10)
    def log_message(self,*args):pass
    def reject(self,status):
        self.send_response(status);self.send_header('Content-Length','0');self.end_headers()
    def do_GET(self):self.forward('GET')
    def do_POST(self):self.forward('POST')
    def forward(self,method):
        state=self.server.state
        manifest=state['manifest']
        allowed={'/health','/version','/v1/models','/metrics'}
        if (method=='GET' and self.path not in allowed) or (method=='POST' and self.path!='/v1/rerank'):
            self.reject(404);return
        if self.headers.get_all('Transfer-Encoding'):
            self.reject(400);return
        lengths=self.headers.get_all('Content-Length',[])
        body=None;headers={}
        if method=='POST':
            ids=self.headers.get_all('X-Request-Id',[])
            expected_id=manifest['request_contract']['headers'][0]['value']
            expected=canonical_bytes(manifest['request_contract']['body'],stage='https_boundary')
            if len(lengths)!=1 or lengths[0]!=str(len(expected)) or ids!=[expected_id]:
                self.reject(400);return
            body=self.rfile.read(len(expected))
            if body!=expected:
                self.reject(400);return
            headers={'Content-Type':'application/json','X-Request-Id':expected_id}
        elif lengths and lengths!=['0']:
            self.reject(400);return
        try:
            if backend_identity(state['container'])!=state['record']['backend']:
                self.reject(503);return
            if method=='POST':
                # O_EXCL + file/parent fsync, before opening the backend connection.
                durable_new(state['ledger'],canonical_bytes({'state':'consumed_before_io',
                    'manifest_sha256':state['manifest_sha'],'request_sha256':hashlib.sha256(body).hexdigest()},
                    stage='https_boundary'))
            connection=http.client.HTTPConnection('127.0.0.1',state['backend_port'],timeout=300)
            try:
                connection.request(method,self.path,body=body,headers=headers)
                response=connection.getresponse();raw=response.read((1<<20)+1)
                status=response.status;content_type=response.getheader('Content-Type','application/octet-stream')
            finally:connection.close()
            if len(raw)>1<<20 or backend_identity(state['container'])!=state['record']['backend']:
                self.reject(503);return
            self.send_response(status)
            self.send_header('Content-Type',content_type)
            self.send_header('Content-Length',str(len(raw)))
            self.send_header(MANIFEST_HEADER,state['manifest_sha'])
            self.send_header(DEPLOYMENT_HEADER,manifest['runtime']['deployment_revision'])
            self.end_headers();self.wfile.write(raw)
        except FileExistsError:
            self.reject(409)
        except (OSError,ValueError,subprocess.SubprocessError,http.client.HTTPException):
            self.reject(503)


def main():
    p=argparse.ArgumentParser(description=__doc__)
    for name in ['manifest','record','ca-file','cert-file','key-file','ledger']:p.add_argument('--'+name,type=Path,required=True)
    p.add_argument('--expected-manifest-sha256',required=True);p.add_argument('--expected-manifest-size',type=int,required=True)
    p.add_argument('--container',required=True);p.add_argument('--port',type=int,default=19481);p.add_argument('--backend-port',type=int,default=18481)
    args=p.parse_args();bundle=V3ContractBundle.load()
    manifest,raw=pinned_canonical_artifact(bundle,args.manifest,expected_sha256=args.expected_manifest_sha256,
        expected_size=args.expected_manifest_size,target='deployment_manifest',allow_synthetic=False)
    if manifest['routes']['origin']!=f'https://127.0.0.1:{args.port}':raise ValueError('origin mismatch')
    record_raw=args.record.read_bytes();record=json.loads(record_raw)
    if canonical_bytes(record,stage='https_boundary')!=record_raw or hashlib.sha256(record_raw).hexdigest()!=manifest['runtime']['deployment_revision']:
        raise ValueError('deployment record mismatch')
    if record['backend']!=backend_identity(args.container) or record['proxy_source_sha256']!=hashlib.sha256(Path(__file__).read_bytes()).hexdigest():
        raise ValueError('deployment identity mismatch')
    if record['backend_port']!=args.backend_port or record['https_port']!=args.port:
        raise ValueError('reviewed port mismatch')
    if record['tls_ca_sha256']!=hashlib.sha256(args.ca_file.read_bytes()).hexdigest():raise ValueError('CA mismatch')
    context=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);context.minimum_version=ssl.TLSVersion.TLSv1_2
    context.load_cert_chain(args.cert_file,args.key_file)
    server=ThreadingHTTPServer(('127.0.0.1',args.port),Boundary)
    server.state={'manifest':manifest,'manifest_sha':hashlib.sha256(raw).hexdigest(),'record':record,
        'container':args.container,'ledger':args.ledger,'backend_port':args.backend_port}
    server.socket=context.wrap_socket(server.socket,server_side=True);server.serve_forever()


if __name__=='__main__':main()
