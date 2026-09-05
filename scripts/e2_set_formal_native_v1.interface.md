# GB10 native set admission v1 interface

This interface implements accepted decision 0033 (`174432ff5b94a134dc509c3a2eff1b7ef1cc6e97`).
It does not authorize execution. Only the root operator runs reviewed, committed phase intents.
All objects below have closed keys. `Pin` means `{sha256,size_bytes}`; SHA256 is lowercase hex,
sizes are integer byte counts, and booleans never satisfy integer fields. Artifact JSON is UTF-8,
rejects duplicate keys/BOM/nonfinite numbers, and is bounded at 128 MiB. External raw pins are
checked before decoding. Canonical bytes mean sorted keys, compact separators, unescaped Unicode,
no HTML escaping, finite JSON numbers, and exactly one terminal LF. Request bodies use these bytes.

`epoch` is always `gb10-native-set-admission-v1`. `provenance` is always:
`{static_access:"unknown",strict_blindness:"unproven",incident_commit:"c86569c52abc44cd9fa38bfa95e8ef346428251f",incident_path:"docs/gb10-native-set-admission-2026-09-05.md",incident_pin:{sha256:"63dbfb4c1d071cf6ee37fca658bb32b88bc190c2c824a9476fa3f8f9e5856299",size_bytes:2858}}`.

## Inputs and catalog

Input fields: `artifact,version,epoch,family,cell_count,source_pins,provenance,cells`.
Artifact is `imms-gb10-native-set-cell-inputs`, version 1, family `calibration`, count 640.
`source_pins` is the Go builder's fixed ordered list of `{role,path,sha256,size_bytes}`.
Input cell fields: `ordinal,cell_id,query_id,depth,prefix_ids,input_commitment_sha256,body,body_pin`.
Ordinal is 0..639. Cell ID is `calibration.0001.depth.01` through `calibration.0020.depth.32`,
in query-major order. Depth is 1..32; prefix_ids preserves that many ordered candidate IDs.
The original IMMS input commitment is inherited and verified by the Go builder, not redefined.
Body is the established eleven-field native request with one compact ordered-set document.

Catalog has the same fields/content, except artifact is `imms-gb10-native-set-cell-catalog`,
and each cell adds `token_count,token_ids,token_ids_sha256`. Count is 1..8192. Token SHA is
SHA256 of u32BE(count) followed by u32BE(each ID). All input fields, source_pins and provenance
must remain equal to the externally pinned input artifact.

Independent reference token artifact fields:
`artifact,version,epoch,input_pin,reference_source_pin,tokenizer_source_pin,provenance,cases`.
Artifact is `imms-gb10-native-set-reference-tokens`, version 1.
Each case has `id,kind,rendered_utf8,rendered_sha256,rendered_size_bytes,token_ids,token_count,segments,untrusted_control_ids`.
The id is the catalog cell_id and kind is ordered_set.
Segments are the independent reference preparation's full six segment objects; names/order are
prefix/body_prefix/query/body_middle/document/suffix. The verifier checks concatenated complete
arrays and rendered bytes against the catalog and inputs; no trust in token counts alone.

## Preregistration and deployment

Prereg fields:
`artifact,version,epoch,input_pin,catalog_pin,reference_tokens_pin,deployment_record_pin,ca_pin,
source_files,score_rule,request_plans,provenance`.
Artifact is `imms-gb10-native-set-admission-prereg`, version 1.
source_files is an ASCII-path-sorted list of `{path,sha256,size_bytes}` for the frozen scripts
closure: `__init__.py`, `e2_set_formal_native_v1.py`, `e2_set_tokenizer_v1.py`.
score_rule is exactly `{rounding:"binary32_rne",repeat_abs_epsilon:0,cutoff_margin:0,
cutoff_derivation:"nextafter32(max640,+Inf)",comparison:"p_yes_float32>=cutoff"}`.
request_plans contains calibration-01 then calibration-02; each object has
`phase,expected_posts,requests`, with count 640 and requests `{ordinal,cell_id,request_id}`.
Request IDs are nonempty ASCII letters/digits/dot/underscore/hyphen, at most 128 bytes, globally
unique across both phases. Both request lists may be frozen together, but do not consume intents.

Deployment fields:
`artifact,version,epoch,container,origin,backend_port,profile,backend,model_revision,model_files,
runtime_versions,tls_ca_pin,transport_source_pin,provenance`.
Artifact is `imms-gb10-native-set-deployment`, version 1.
Container: `imms-gb10-native-set-admission-v1-20260905`; origin `https://127.0.0.1:19483`;
backend_port 18485; profile `imms-set-native-v1`.
backend has the existing diagnostic identity's exact public keys:
`container_id,image_id,image_reference,started_at,entrypoint,cmd,user,readonly_rootfs,cap_drop,
security_opt,mounts,networks,set_tokenizer_profile`.
Each mount has `source,destination,rw`. Model revision is
`22e683669bc0f0bd69640a1354a6d0aebcfeede5`; model_files is the same 18 `{path,sha256,size_bytes}`
records. Image ID/reference must both be
`sha256:a727c4ae21174b2c87b3b8ab301fff389fbc052b0a2979b897022e86551e06c6`.
runtime_versions has `vllm,torch,transformers,tokenizers,safetensors` with
`0.23.0,2.11.0+cu130,5.12.0,0.22.2,0.8.0`, respectively.

## Separate phase intents

Intent fields: `artifact,version,epoch,phase,prereg_pin,prereg_commit,previous_phase_terminal,provenance`.
Artifact is `imms-gb10-native-set-phase-intent`, version 1.
calibration-01 previous_phase_terminal is null. calibration-02 has `{pin,commit}`, binding the
committed calibration-01 collector success/640 terminal with the same prereg/catalog.
Commit fields are full lowercase Git SHA1. Root checks clean Git provenance outside the transport.
Only the current intent is supplied to an execution CLI; there is no cross-phase automatic loop.

Paths are derived from `/home/infinimesh/imms-debug/20260905` and current phase:
runtime `runtime/native-set-admission-v1/<phase>/{provider-ledger,collector-ledger}`;
output `evidence/native-set-admission-v1/<phase>/`.
The phase output contains provider/collector observations, collector per-cell results and final result.
Whole-run intent/started are create-only and fsynced before the first POST. Each side persists one
`NNNN-admission.json` before I/O and one `NNNN-result.json` after validated success. No replay after
crash, failure or existing intent; a failure emits a short-code terminal without another forward.

Each role ledger's `intent.json` and `started.json` has fields
`artifact,version,epoch,role,phase,prereg_pin,intent_pin,expected_posts,provenance`.
The artifacts are `imms-gb10-native-set-run-intent` and `imms-gb10-native-set-run-started`;
expected_posts is 640. Each `NNNN-terminal.json` has fields
`artifact,version,epoch,role,phase,prereg_pin,intent_pin,ordinal,cell_id,request_id,admission_pin,status,code,result_pin`.
Artifact is `imms-gb10-native-set-cell-terminal`; status is success/failed, code is complete or a
short error code, and result_pin is the same side's durable result pin or null on failure.

Admission fields:
`artifact,version,epoch,role,phase,prereg_pin,intent_pin,ordinal,cell_id,input_commitment_sha256,
request_id,body_pin,previous_result_pin`.
Artifact is `imms-gb10-native-set-cell-admission`; role is provider or collector.
previous_result_pin is null for ordinal 0 and otherwise the same side's preceding result pin.

Provider result fields:
`artifact,version,epoch,role,phase,prereg_pin,intent_pin,ordinal,cell_id,input_commitment_sha256,
request_id,admission_pin,body_pin,raw_response_pin,http_status,score_decimal_lexeme,
score_float32_decimal,score_float32_bits,token_count,backend_before_pin,backend_after_pin`.
Artifact is `imms-gb10-native-set-provider-cell-result`; role is provider. HTTP status must be 200.

Collector result has the same common identification/score fields, but artifact is
`imms-gb10-native-set-collector-cell-result`, role collector, and replaces backend_before_pin /
backend_after_pin with `provider_record_pin,response_headers,raw_response_base64,cache_before,cache_after,tls`.
Raw body base64 is lossless; raw_response_pin verifies the decoded bytes. response_headers is an
ordered list of `[name,value]` pairs; duplicate names (case insensitive) are rejected.
Each cache object has `observation_pin,prefix_cache_queries_total,prefix_cache_hits_total`.
Metrics snapshots are actual native bytes: N+1 snapshots, with snapshot i reused as request i+1's
before evidence. Full models/version/health/TLS/backend evidence is saved at phase start/end.

Response identity headers are `X-IMMS-Native-Set-Prereg-SHA256`,
`X-IMMS-Native-Set-Intent-SHA256`, `X-IMMS-Native-Set-Deployment-SHA256`, equal to the respective
current pins. POST additionally returns `X-Request-Id`, equal to the planned request ID.
`tls` has `version,cipher,peer_certificate_sha256`, from that actual verified TLS connection.
cipher is the cipher name string; peer_certificate_sha256 must equal SHA256 of the pinned
ca.pem's DER certificate. This fixed profile uses that same self-signed certificate as the server
certificate; the boundary checks exact PEM bytes, and the collector also requires normal SSL
certificate and literal-IP hostname validation. GET never returns X-Request-Id.
All observations live at `observations/<sha256>.json` under the phase output and are located by pin.
HTTP observation fields are `artifact,version,epoch,phase,kind,index,http_status,response_headers,
raw_body_base64,raw_body_pin,tls`; artifact is `imms-gb10-native-set-http-observation`.
kind is models_start/version_start/health_start/metrics/models_end/version_end/health_end;
index is 0..640 for metrics, null otherwise. GET raw bodies are bounded at 64 KiB.
Backend observations are the canonical public deployment.backend object itself, using the same CAS
location. Actual checks run before and after every POST; identical facts share one durable file.

The decimal lexeme is retained exactly from the JSON numeric token. Direct decimal-to-binary32 RNE
produces the eight lowercase hex bits; score_float32_decimal is the exact finite decimal expansion
of that binary32 value (a string). Scores must be finite and in [0,1]. No clamp or second activation.

## Phase result and terminals

Phase result fields:
`artifact,version,epoch,phase,status,prereg_pin,intent_pin,catalog_pin,deployment_record_pin,
post_count,retry_count,results,observation_pins,provenance`.
Artifact is `imms-gb10-native-set-phase-result`, version 1, status success, count 640, retries 0.
Each result index has `ordinal,cell_id,input_commitment_sha256,request_id,score_decimal_lexeme,
score_float32_decimal,score_float32_bits,collector_record_pin,provider_record_pin`.
observation_pins has models_start/version_start/health_start/models_end/version_end/health_end
and metrics, the latter an ordered 641-pin array. Provider success terminal follows the final
metrics GET; collector success additionally requires all end observations. A later collector failure
does not rewrite a durable provider terminal and does prevent phase success.

Failure only: if an HTTP response was obtained before rejection, the relevant role ledger adds
create-only `failure-response.json` with fields `artifact,version,epoch,role,phase,prereg_pin,
intent_pin,http_status,response_headers,raw_body_base64,raw_body_pin,tls` (artifact
`imms-gb10-native-set-failure-response`). Backend HTTP has tls=null. This supplemental artifact
contains no score and never permits retry. Normal stopping of a boundary whose durable provider
terminal is already successful returns completed_stop with process exit 0.

Each role's terminal fields:
`artifact,version,epoch,role,phase,status,prereg_pin,intent_pin,catalog_pin,completed_posts,
consumed_posts,count_known,code,phase_result_pin,provenance`.
Artifact is `imms-gb10-native-set-phase-terminal`; status success or failed. On success both counts
are 640, count_known=true and code="complete"; collector phase_result_pin is the phase result pin,
provider phase_result_pin is null. Failure never grants score/no-match authority; an admitted I/O
without a durable result makes count_known=false. Failed terminals cannot be replaced or resumed.

The Go verifier reconstructs canonical body from the input artifact, validates catalog equality,
full token arrays, both journals and raw HTTP identities, and then compares same-cell score bits
between passes. Request IDs, raw response bodies and latency are not cross-pass equality criteria.
Policy generation is pure Go and occurs only after both independently committed successful stages.
No held-out/old100/injection input is loaded by this calibration-only implementation.
