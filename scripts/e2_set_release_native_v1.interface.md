# GB10 native set release transport v1

This is a new one-shot tool under accepted 0033. Epoch remains
`gb10-native-set-admission-v1`, authority remains unissued, and landing budget is not reset.
The calibration source, records, policy and consumed ledgers remain immutable.

All objects have closed keys, UTF-8 duplicate-free finite JSON and external SHA256/size pins.
Pins precede decoding. Canonical artifacts are sorted compact JSON, unescaped Unicode, one LF.
External input/catalog/reference bytes need not be sorted. Files are bounded at 128 MiB;
HTTP POST responses at 1 MiB, GET responses at 64 KiB, deadline 300 seconds, no retries.

`Pin={sha256,size_bytes}`. `provenance` is the same five-key unknown/unproven incident identity
as calibration. `phase` is always `release-bundle`.
Runtime: `/home/infinimesh/imms-debug/20260905/runtime/native-set-admission-v1/release-bundle/`.
Evidence: `/home/infinimesh/imms-debug/20260905/evidence/native-set-admission-v1/release-bundle/`.
Each side owns a new `{provider,collector}-ledger` under runtime; CAS observations and
`phase-result.json` live under evidence. No single-stream execution/resume CLI exists.

The existing physical deployment record, model container, image, CA, 19483 HTTPS origin and
18485 backend relay are reused exactly. The old deployment record's transport_source_pin is
the historical calibration helper source. Current release proxy identity comes from the new
prereg source closure; the historical record is not rewritten or relabeled.

## Inputs and preparation

Input fields: `artifact,version,epoch,family,cell_count,source_pins,provenance,cells`.
Artifact is `imms-gb10-native-set-release-cell-inputs`, family release-bundle, count 3849.
Catalog has the same fields, artifact `imms-gb10-native-set-release-cell-catalog`.
Every final input cell has `ordinal,stream,stream_ordinal,cell_id,query_id,depth,prefix_ids,
input_commitment_sha256,body,body_pin,expected_yes`. Catalog adds exactly
`token_count,token_ids,token_ids_sha256`. expected_yes is a boolean used by the offline verifier;
it never enters the native HTTP request. Bodies retain the fixed eleven-field request shape.

Stream order is heldout (640), old100 (3200), injection (9). Global ordinals are 0..3848;
stream ordinals start at zero in each stream. For the first two streams the cell ID is
`<stream>.<query ordinal 0001..0020/0100>.depth.<01..32>` in query-major order. Injection cell ID
is `injection.<original case ID>`, query_id is the original case ID, and depth is source document
count. Injection prefix member IDs retain `injection.<case ID>.document.<1-based position>`.
Only final input/catalog artifacts qualify; request drafts do not authorize execution.

Reference artifact fields: `artifact,version,epoch,input_pin,reference_source_pin,
tokenizer_source_pin,provenance,cases`. Artifact is
`imms-gb10-native-set-release-reference-tokens`. Cases are the existing six-segment independent
reference preparation objects: `id,kind,rendered_utf8,rendered_sha256,rendered_size_bytes,
segments,token_ids,token_count,untrusted_control_ids`. id equals cell_id; kind is ordered_set.
All complete arrays, segment strings, trust bits, rendered bytes and token hashes must match.
No tokenizer or formatter rules change in response to injection text.

## Prerequisites and preregistration

Prereg fields: `artifact,version,epoch,phase,input_pin,catalog_pin,reference_tokens_pin,
deployment_record_pin,ca_pin,policy_pin,policy_commit,policy_replay_pin,bge_terminal_pin,
bge_terminal_commit,source_files,expected_posts,request_plans,provenance`.
Artifact is `imms-gb10-native-set-release-prereg`. expected_posts is 3849.
source_files is the sorted exact list `{path,sha256,size_bytes}` of script basenames
`__init__.py,e2_set_formal_native_v1.py,e2_set_release_native_v1.py,e2_set_tokenizer_v1.py`.
The empty init and frozen formal/tokenizer have fixed pins; the new self file is pinned before
importing any frozen helpers. No new model or HTTP dependency is introduced.

request_plans is a three-item ordered list `{stream,expected_posts,requests}`;
each request is `{ordinal,stream_ordinal,cell_id,request_id}`. IDs are unique, ASCII
letters/digits/dot/underscore/hyphen, at most 128 bytes, and disjoint from calibration IDs.
No request/model/cutoff/batch/count override flags are exposed.

Policy raw pin is fixed to cb2b33040451990586ef053bf79a684cc2cdbee4fe745d92491a5e692894da85/2406,
committed at b075a37c49bf08fcf0ec1ffbb37d0670b789388b. Policy-replay fields are
`artifact,version,epoch,policy_commit,policy_pin,prereg_pin,phases,replayed_posts,
repeat_cells_bits_exact,cutoff_float32_bits,provenance,quality_pass,authority,counter`.
Artifact is `imms-gb10-native-set-release-policy-replay`; phases equal the policy's two original
five-field phase pins. Replayed posts 1280, exact cells 640, cutoff bits 3f7e3eb1,
quality_pass=false, authority=unissued, counter=0/5. The final Go verifier replays the raw
calibration evidence and compares the exact replay artifact, rather than trusting its claims.
BGE20 terminal is separately pinned and must bind this committed policy, exact 20 successful
query embeddings, no retries and its complete new BGE source/vector/receipt lineage.
Its closed fields are `artifact,version,epoch,phase,status,intent_pin,started_pin,policy_pin,
policy_commit,deployment_pin,vector_set_pin,receipt_pin,runtime_poststate_pin,execution_commit,
execution_host,consumed_posts,completed_posts,count_known,retry_count,failure_code,provenance`.
Artifact is `imms-gb10-native-set-heldout-bge-terminal`, phase heldout-bge-20, status success,
execution_host local-development-cpu, both counts 20, count_known true, retry_count 0 and
failure_code empty. execution_commit binds the prior execution intent; it differs from the
later terminal archive commit in prereg. Input source roles are `native_policy` and
`heldout_bge_intent,heldout_bge_started,heldout_bge_deployment,heldout_bge_runtime_prestate,
heldout_bge_runtime_poststate,heldout_bge_vectors,heldout_bge_receipt,heldout_bge_terminal`.
Historical calibration `bge_*` and `release_policy` roles retain their original meaning.

CLI modes are `validate-plan`, `boundary` and `collect`. Required flags are `--prereg`,
`--expected-sha256`, `--expected-size`, `--inputs`, `--catalog`, `--reference-tokens`,
`--deployment-record`, `--ca-file`, `--policy`, `--policy-replay`, `--bge-terminal`.
Execution additionally requires `--intent`, `--intent-sha256`, `--intent-size`; boundary also
requires `--cert-file`, `--key-file`. Optional `--calibration-prereg` supplies the immutable
a5d6d07c1575ad7ccb94ded15e82c91ed5f274b12f7301541e8fe0f1da4ac320/146100 prereg
for old request-ID disjointness; its default is the original remote prereg path. There is no
phase selector. Preparation details initially bind a draft; Go finalization checks both CPU
preparations and full reference details, carries every case unchanged, and binds final inputs.

## Common one-shot intent and evidence

Intent fields: `artifact,version,epoch,phase,prereg_pin,prereg_commit,policy_pin,policy_commit,
bge_terminal_pin,bge_terminal_commit,expected_posts,provenance`.
Artifact is `imms-gb10-native-set-release-intent`, count 3849. Root checks the current clean
committed intent before dispatch. The remote transport needs no full Git checkout.

Journal objects retain the calibration field sets with artifact prefix changed from
`imms-gb10-native-set-` to `imms-gb10-native-set-release-`. Every cell admission/result/terminal
adds `stream,stream_ordinal`; global ordinal determines `NNNN-admission/result/terminal.json`.
Whole intent/started and each admission are O_EXCL and fsynced before corresponding I/O.
Success result precedes cell terminal. Any error consumes the whole bundle; neither a failed
stream nor remaining streams can resume. Started-without-terminal remains consumed evidence.

Each stream has its own models/version/health start/end observations and N+1 actual metrics
snapshots: 641 + 3201 + 10 = 3852. HTTP observation adds `stream`, while index is the local
metrics index (null for metadata). CAS path is `observations/<sha256>.json` under evidence.
Current backend identity is checked before and after every request; identical snapshots may
share a CAS file. TLS peer DER hash binds the same pinned CA/self-signed server certificate.
Response identity headers remain X-IMMS-Native-Set-Prereg-SHA256, -Intent-SHA256 and
-Deployment-SHA256; POST also returns exact X-Request-Id. No GET request ID exists.

Phase result retains the calibration result fields and release artifact prefix, count 3849,
but replaces observation_pins with `stream_observations`: three objects in fixed stream order,
each `{stream,models_start,version_start,health_start,models_end,version_end,health_end,metrics}`.
Result indices additionally contain stream/stream_ordinal. Raw decimal lexeme, direct RNE
float32 exact decimal and bits are recorded unchanged; thresholding belongs to the Go verifier.
All role terminals use the release prefix and count 3849. Provider success follows the last
injection metrics GET; collector success additionally requires final stream metadata.

Failure HTTP bytes, if obtained, are retained only in create-only failure-response.json, with
the same supplemental shape as calibration and release artifact prefix. They carry no score.
Normal termination after durable provider success exits zero. No new model request is made by
validation, CPU preparation, metrics, unit tests or cleanup.
