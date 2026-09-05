# GB10 native set reranker diagnostic

Decisions 0031/0032 add a separate, explicit `imms-set-native-v1` profile. The
fixed vLLM 0.23 default tokenizer produced different token boundaries and admitted
control IDs from untrusted text. `e2_set_tokenizer_v1.py` preserves the six exact
segments and strips only the 26 added-token matchers from a private untrusted
backend. Exact roundtrip, length and zero-control checks remain mandatory.
`e2_set_processor_v1.py` validates the model/template/request and the final engine
IDs; `patch_e2_set_processor_v1.py` patches only the pinned upstream source.

The separate HTTPS/collector module, `e2_set_diagnostic_v1.py`, pins an exact
16-request synthetic plan and actual backend profile. Both sides keep exclusive,
fsynced intent/started/terminal records. Failed or completed runs cannot resume.
Historical v1/v2/v3 implementations and their consumed ledgers are unchanged.

On 2026-09-05, the isolated GB10 image config ID
`sha256:a727c4ae21174b2c87b3b8ab301fff389fbc052b0a2979b897022e86551e06c6`
completed all 16 native POSTs, without retries. Eight native/reference token arrays
and rendered bytes match exactly; adjacent repeats have identical float32 bits.
The image is a locally built immutable config/layer identity, not a published
registry manifest. Its scan retained all 3199 existing advisory IDs without new
IDs or dependency changes; this does not grant production security admission.

IMMS's first reference attempt failed before any forward because the checkpoint
ties the head to `model.embed_tokens.weight`. The failure remains archived.
A separately reviewed reference-only correction completed 16 forwards with zero
additional native requests. The offline Go comparison preserves both plan IDs and
passes integrity checks. Maximum native versus FP32-reference probability
difference is `0.013222754001617432`; this is an observation, not an accepted
quality tolerance. Native eager execution uses FlashAttention2/custom kernels,
whereas the independent reference uses HF eager attention.

All task diagnostic containers are stopped after their fixed runs. Original
model and product services were not restarted. The 189-test Python aggregate
passed. Formal numeric/calibration policy and quality authority remain pending.
Full artifacts and scope are in the [IMMS report](../../IMMS/docs/gb10-set-reranker-2026-09-05.md).
