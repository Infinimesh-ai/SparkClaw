# PPTX Overlength Phase 0 Qualification

> Language: English | [简体中文](../zh-cn/benchmarks/pptx-overlength-phase0-qualification.md)

| Field | Result |
|---|---|
| Date | 2026-08-14 |
| Decision | **NO-GO** |
| Proposal | Gotenberg, LibreOffice, PDF.js geometry, and deterministic raster visibility evidence |
| Target | Linux `aarch64` |
| Corpus digest | `75aa95d309b53d6300fa7cf5b2df23c3d7d638471f2b3efd6365114516c025bd` |
| Machine result | [`pptx-overlength-phase0-result.json`](pptx-overlength-phase0-result.json) |
| Production effect | None; Gateway and ToolHub remain unchanged |

> Retention: the qualification harness is retained to re-run Phase 0 if the candidate policy changes, and is deleted together with the design document if the proposal is withdrawn.

## Decision

The current proposal does not advance to production integration. The pinned
ARM64 renderer stack passed the synthetic completeness, visibility,
determinism, preservation, digest, outage, and cancellation probes. It did not
pass the complete mandatory gate:

1. The unreduced request bound permits 64 shapes with 16 candidates each, or
   1,024 candidate conversions before combination renders. The fastest measured
   engine median was 0.1703 seconds per conversion, which projects to a
   174.3872-second lower bound. This already exceeds the proposed 90-second
   preparation deadline without proof renders, combination renders, queueing,
   mutation, PDF.js inspection, or final verification.
2. No owner deck with irreversible private-text replacement was supplied, so
   the required owner corpus was not run.
3. Microsoft PowerPoint reference-viewer compatibility was not run. No claim is
   made about clipping, repair prompts, or divergence in PowerPoint.

The conversion-cost failure is independently decisive. A smaller executable
candidate policy, qualified deterministic pruning, or a reduced admitted scope
requires a new Phase 0 run. PowerPoint and owner-corpus evidence must also pass
before any production code is authorized.

## Implemented Harness

The qualification-only implementation lives under
`benchmarks/pptx-overlength-phase0/` and is invoked by
`scripts/qualify-pptx-overlength.sh`. It is not a root workspace dependency and
does not register a tool, configuration field, feature flag, worker, or runtime
path.

The harness provides:

- generated 16:9 and 4:3 Latin, Simplified Chinese, mixed-text, bullet,
  soft-break, clipping, duplicate-attribution, opaque/partial/transparent
  occlusion, same-color concealment, missing-font, and rotated-target fixtures;
- native LibreOffice and private-loopback Gotenberg conversion with isolated
  LibreOffice profiles, byte limits, timeouts, and process-group cleanup;
- PDF.js 5.4.394 normalized text, transform geometry, font, and operator-list
  digests;
- counterfactual raster checks comparing candidate, target-removed, and
  target-topmost renders, so extracted-but-concealed text is rejected;
- canonical OOXML package digests independent of ZIP order and timestamps;
- deterministic semantic-group, partial-success, and `no_safe_change`
  qualification contracts; and
- a structured result that excludes document text, PDF bytes, and job-local
  paths.

## Pinned Runtime

| Component | Qualified artifact |
|---|---|
| Host architecture | Native Linux `aarch64` |
| Host LibreOffice | `24.2.7.2` |
| Gotenberg | `8.15.3`, ARM64 manifest `sha256:664f1851e03fc230f194c114efa3ad7694e29951ac9ba04991c7b6e47bc243a8` |
| Gotenberg LibreOffice | `24.8.4.2` |
| PDF.js | `pdfjs-dist` `5.4.394`, exact npm lockfile |
| PPTX writer | Existing `pptx_slide.py` through `python-pptx` 1.0.2 |
| Qualified fonts | Liberation Sans and Noto Sans CJK SC; absent declared fonts fail closed |

The Gotenberg container used a random loopback port, a unique name, two CPUs,
a 4 GiB memory ceiling, a PID limit, and a read-only host Noto font mount. It
did not alter or restart any existing SparkClaw container.

## Executable Results

Both engines produced the expected decision for all 17 synthetic cases. Fitting
Latin, CJK, mixed, bullet, soft-break, and 4:3 content was accepted. Deliberate
Latin/CJK/mixed clipping, 4:3 clipping, duplicate strings, opaque and partial
occlusion, transparency, same-color concealment, missing fonts, and the
unsupported rotated target were rejected.

| Engine | Repeat count per fixture deck | Normalized text/geometry | Raster | Median | p95 | Worst |
|---|---:|---|---|---:|---:|---:|
| Host LibreOffice | 100 | Stable | Stable | 1.1924 s | 1.3027 s | 1.3471 s |
| Gotenberg | 100 | Stable | Stable | 0.1703 s | 0.8326 s | 0.9046 s |

Each engine performed 204 conversions: six initial candidate/evidence renders
across two decks plus 198 repeat renders. All 100 combined normalized digests
and all 100 combined raster digests were identical for each engine.

The existing writer was replayed five times. The raw package diff contained
only `ppt/slides/slide1.xml`; the raw PPTX SHA-256 and canonical package digest
were repeatable. The canonical output digest was
`7c9bc214a83dd392a0c50850777622e6350a13ef37c7b864f0a72471e7d0e764`.

An unreachable renderer returned the qualification equivalent of
`runtime_unavailable` and left no PDF. A deliberately hung parent/child process
tree was stopped in 0.0032 seconds and left no live child, below the two-second
cleanup bound.

## Gate Matrix

| Mandatory gate | Result | Evidence |
|---|---|---|
| Native deployment | PASS | Pinned Gotenberg and both LibreOffice versions ran natively on ARM64 |
| Text completeness | PASS | All fitting and deliberately clipped Latin/CJK/mixed cases matched expected verdicts |
| Attribution and visibility | PASS | Duplicate, occlusion, transparency, and same-color cases failed closed |
| Geometry | PASS | Contributing PDF.js text transforms remained inside qualified target regions |
| Font determinism | PASS | Qualified fonts were inventoried; the missing-font case was rejected |
| Render repeatability | PASS | 100 normalized and raster digests per engine were stable |
| Owner corpus | **NOT RUN** | No owner private-text corpus was supplied |
| PowerPoint compatibility | **NOT RUN** | No reference-viewer evidence was supplied |
| Writer preservation | PASS | Only the declared slide XML part changed across five writes |
| Partial application | PASS | An independent eligible group survived another group's rejection |
| Semantic atomicity | PASS | One ineligible member rejected its full group; invalid metadata collapsed operation-wide |
| No-safe-change | PASS | All-ineligible input returned no artifact |
| Pipeline outcomes | PASS | Qualification contracts separated requested/effective plans and zero-change outcome |
| Renderer outage | PASS | Typed unavailable outcome, no unchecked output |
| Cancellation | PASS | Complete process tree stopped under two seconds |
| Conversion cost | **FAIL** | 174.3872 s median lower bound for 1,024 conversions exceeds 90 s |
| Digest determinism | PASS | Canonical package plus normalized render digests repeated |
| Confidentiality | PASS | Structured results contain no fixture text, PDF bytes, or job paths |

## Repository Effect

The following remain prohibited under this result:

- Gateway or ToolHub adaptation worker integration;
- Gotenberg, PDF.js, renderer, font, or policy additions to production setup,
  Compose, doctor, configuration, or deployment;
- changes to `pptx.update_slide`, `pptx.update_deck`, approval, prepared
  artifacts, Workflow outcomes, or semantic repair; and
- claims that the admitted patterns are compatible with Microsoft PowerPoint or
  representative owner decks.

The current heuristic PPTX behavior remains unchanged. The repository effect is
limited to the bilingual design/report, the independent qualification harness,
its pinned benchmark dependency, and the structured machine result.

## Reproduction

Run from the repository root on the target ARM64 host:

```bash
scripts/qualify-pptx-overlength.sh
```

The script uses an isolated container name and random loopback port. Set
`PPTX_PHASE0_KEEP_WORK=1` only when the generated fixtures and diagnostic
artifacts must be retained for the owner-corpus or PowerPoint follow-up.

## References

- [PPTX overlength resilience design](../docs/pptx-overlength-resilience-design.md)
- [Previous DocumentBuilder Phase 0 qualification](pptx-documentbuilder-phase0-qualification.md)
