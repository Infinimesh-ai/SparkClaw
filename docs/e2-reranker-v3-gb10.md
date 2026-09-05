# GB10 reranker evidence v3

Decision 0030 corrects the real vLLM 0.23 logging flag and records the explicit
GPU memory fraction required by the isolated GB10 deployment. The historical
v1/v2 providers and central artifacts are unchanged. This tooling is separate
from the product runtime and can only submit the contract's fixed synthetic pair.

The provider pins central commit `84be856` and conformance root
`64cf1bbcefbc9fec1ae238d382cf69ba28c1d2f5c545bb041f3b9f19725eae51/15492`.
It validates actual ordered argv, canonical decimal/binary64 resource binding,
complete model/tokenizer catalog and native model/score semantics.

`scripts/e2_reranker_live_smoke_v3.py` requires a reviewed real manifest's SHA/size,
a trusted CA, a separately pinned resolved cache configuration, a new evidence
directory and a persistent attempt ledger. It only connects to numeric loopback
HTTPS. Native metrics require one exact engine/model series per counter; missing,
duplicate, nonfinite, fractional and unrepresentable values fail closed. Cache
configuration comes from the reviewed deployment export, never an invented gauge.

`scripts/e2_reranker_https_v3.py` adds the two identity headers while preserving
upstream bodies. It validates the actual container identity before and after each
forward, and restricts POST to the exact reviewed synthetic body and request ID.
Both the collector and boundary consume an exclusive, fsynced attempt record
before sending. An interrupted or failed POST cannot be retried with that ledger.
The boundary is intended for this isolated diagnostic deployment, not a general
public model gateway. Its TLS private key stays in the operator's private runtime
directory; only the CA certificate belongs in review evidence.

Run the offline checks from the SparkClaw repository with the sibling InfiniCenter:

```bash
python3 -m unittest scripts.test_e2_reranker_evidence_v3 scripts.test_e2_reranker_live_smoke_v3
```

The fake-server suite is explicitly synthetic. Neither its fixtures nor a passing
test may stand in for real deployment review. A real smoke receipt still requires
external byte-pin review before reference parity, tolerance or calibration work.

On 2026-09-05 UTC, the reviewed GB10 deployment completed its one planned native
POST with HTTP 200. Manifest `c08e03610a6da695b9b01cc70cb1b9db034aaf66a8087cb340f70e656984d8c4/12893`
and receipt `a5818f01f8cf961c3ddc60a625b80dcacaf2e04a7e673d5dc7c0d23bfec97bca/9926`
passed independent Go/Python implementations' external pin checks. The receipt is
stored in IMMS commit `2aef26ba54346c0d7eebe42e5150155b11df317f`, at
`docs/evidence/gb10-e2-v3-20260905/smoke-stable/receipt.json`. Both attempt ledgers
are consumed; this documentation does not authorize resending the POST. Full
scripts validation passed 148 tests. See the
[GB10 result](../../IMMS/docs/gb10-e2-admission-2026-09-05.md) for the prior GET-only
mount-order rejection, exact deployment review, patch candidates and remaining
parity/calibration/production work. Historical v1/v2 are unchanged.
