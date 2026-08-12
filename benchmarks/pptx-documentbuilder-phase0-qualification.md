# DocumentBuilder PPTX Phase 0 Qualification

> Language: English | [简体中文](../zh-cn/benchmarks/pptx-documentbuilder-phase0-qualification.md)

| Field | Result |
|---|---|
| Date | 2026-08-11 |
| Decision | **NO-GO** |
| Proposal | DocumentBuilder 9.4.0 plus OR-Tools deterministic PPTX layout runtime |
| Stop rule | Stop at the first failed mandatory qualification gate |
| Failed gates | Real text measurement, stable package identity, and preservation in the free edition |
| Repository revision | `8faa421c855969131bc99b272059231e4b79f1a9` |
| Host | Linux `aarch64`, kernel `6.17.0-1026-nvidia` |

## Outcome

The DocumentBuilder plus OR-Tools proposal is rejected. The pinned
DocumentBuilder 9.4.0 ARM64 artifact opens the test presentation and exposes
slide, shape, text, outer geometry, and mutation methods, but its tested
Presentation SDK does not expose actual laid-out text bounds, used text height,
or an effective overflow result.

The available `RecalculateAutoFit()` method is not an overflow measurement. It
returns `true` for both a short fitting string and an intentionally oversized
mixed English/Chinese string in equal-sized text boxes. After recalculation,
the public serialized state still contains only the `normAutoFit` mode and the
declared 24 pt run size. It contains no effective scale, used bounds, line
layout, or overflow state.

The mandatory measurement gate therefore failed. At the owner's request to
distinguish product suitability from integration error, one root-cause-only
save probe was then run through both official interfaces. The free edition
added an `Unregistered Version` watermark, rewrote non-visual shape IDs, and
changed unrelated package parts. These are additional mandatory identity and
preservation failures. The probe did not resume the migration.

In accordance with the [design's fail-fast rule](../docs/pptx-deterministic-layout-runtime-design.md),
the remaining qualification gates were not executed and no production
integration was started.

## Root Cause Classification

The result is a **DocumentBuilder project/free-edition suitability failure for
this role**, not a SparkClaw integration failure.

- Both failures were reproduced by invoking the unpacked upstream Python SDK
  and the upstream `docbuilder` CLI directly. No SparkClaw package, adapter,
  schema, process runner, or model call participated.
- The official Presentation API surface lacks the measurement result required
  by the design. This is an API capability mismatch, not incorrect argument
  binding.
- The official installation documentation states that the free version adds a
  watermark to every generated document. The native save result reproduced
  that documented edition behavior.
- OR-Tools was not the failing component. It can optimize finite candidates,
  but it cannot manufacture trustworthy text measurements that the document
  engine does not provide.

DocumentBuilder can still create, edit, convert, and render presentations. The
No-Go conclusion is narrower: the free AGPL artifact cannot serve as
SparkClaw's preservation-safe, deterministic PPTX measurement and write backend
under the accepted design.

## Baseline

The existing repository baseline was established before qualification:

| Check | Result |
|---|---|
| `npm run setup:document-tools` | Passed |
| `go build ./...` in `services/gateway` | Passed |
| `go vet ./...` in `services/gateway` | Passed |
| `go test ./...` in `services/gateway` | Passed |
| `npm --workspace @sparkclaw/webchat test` | Passed, 10 files and 21 tests |
| `npm --workspace @sparkclaw/webchat run build` | Passed |

This separates the No-Go result from the current Python PPTX adapter and
Gateway test baseline, which remained green.

## Qualified Artifact

The test used the exact release selected by the design:

```text
Release: ONLYOFFICE DocumentBuilder v9.4.0
Asset: onlyoffice-documentbuilder-linux-aarch64.tar.xz
Release size: 66971536 bytes
Expected SHA-256: 8756b0b57a4ab4b27608a6b1fbeb13f67670bde0245da166907277821afbeb8e
Observed SHA-256: 8756b0b57a4ab4b27608a6b1fbeb13f67670bde0245da166907277821afbeb8e
Runtime version: 9.4.0.130
```

`file` identified `docbuilder`, `x2t`, `libdocbuilder.c.so`, and
`libdoctrenderer.so` as ARM aarch64 ELF binaries. `ldd` resolved the packaged
libraries and host glibc without emulation. Artifact identity and native startup
passed; this does not imply that the complete Linux ARM64 gate passed because
that gate also requires every other mandatory test to pass.

## Measurement Probe

### Fixture

A temporary 16:9 PPTX was generated with two text shapes using the same outer
box:

```text
width: 2011680 EMU (2.2 inches)
height: 502920 EMU (0.55 inches)
font: Carlito, 24 pt
word wrap: enabled
margins: zero
```

One shape contained `Fits`. The other contained a long English sentence plus
Simplified Chinese text. A second fixture enabled PowerPoint normal AutoFit on
both shapes. The fixtures and downloaded binary were kept outside the
repository and removed after the report was recorded.

### Public API inventory

The official 9.4-era Presentation API exposes the following relevant methods:

- `ApiShape.GetPosX/GetPosY/GetWidth/GetHeight` for the drawing's outer box;
- `ApiShape.GetContent` for text content;
- `ApiDocumentContent.GetText` and paragraph/run structure;
- font and paragraph property getters;
- shape position, size, padding, and text-alignment mutation.

It does not document a text-bounds, used-height, line-layout, or overflow
method. `ApiShape.ToJSON()` on the live file returned the external transform,
text body properties, paragraphs, runs, and declared font size, but no rendered
text extent or overflow value.

### Executable results

The native Python SDK opened the fixture with status `0` and returned the
expected shape text and outer geometry. Direct execution of a text-bounds call
failed explicitly:

```text
TypeError: ...GetTextBounds is not a function
execute False
```

The AutoFit comparison returned:

| Shape | Content class | `RecalculateAutoFit()` | Width | Height |
|---|---|---:|---:|---:|
| `Fits` | Short, fitting | `true` | 2011680 EMU | 502920 EMU |
| `Overflows` | Long mixed text | `true` | 2011680 EMU | 502920 EMU |

With normal AutoFit enabled, `ToJSON()` returned the same state before and
after recalculation for both shapes:

```json
{
  "bodyPr": {"textFit": {"type": "normAutoFit"}},
  "run": {"font": "Carlito", "sz": 2400}
}
```

`2400` is the declared 24 pt size in hundredths of a point. No effective font
size, font scale, text width, text height, line count, clipping, or overflow
field was returned. The Boolean result only reports that recalculation was
accepted; it does not distinguish fit from overflow.

Rendering the shape and inferring bounds from pixels would introduce a new
measurement heuristic. It would not satisfy the design's requirement for a
qualified text-layout result and was therefore not used to bypass the failed
gate.

### Independent CLI save result

To exclude the Python binding as the cause, the same source was opened and
saved with the upstream executable and its native script format:

```text
./docbuilder cli-save.docbuilder
docbuilder: license is invalid!
exit: 0
```

Both the Python SDK and CLI outputs added this slide text:

```xml
<a:t>Unregistered Version</a:t>
```

This matches the upstream installation documentation: the free version adds a
watermark to all generated documents and a commercial license is required to
remove it. The free AGPL license is a source-code license; it is not a
DocumentBuilder registration key.

The save also changed the source shape identities:

| Shape | Source `p:cNvPr` ID | CLI output `p:cNvPr` ID |
|---|---:|---:|
| `FitsAutoFit` | 2 | 1752396050 |
| `OverflowsAutoFit` | 3 | 989754690 |

The CLI save added notes master, notes slide, relationship, and theme parts,
while removing the source thumbnail and printer-settings part. Those changes
occurred without a SparkClaw mutation plan and are outside the design's package
allowlist. The inserted watermark is independently sufficient to fail
preservation.

## Gate Matrix

| Mandatory gate | Result | Evidence or reason |
|---|---|---|
| Stable identity across save/reopen | **FAIL** | Direct CLI save rewrote source non-visual shape IDs 2 and 3 to different generated values |
| Real text bounds or effective overflow | **FAIL** | No public result; explicit bounds call is unavailable; AutoFit result does not distinguish fit from overflow |
| Geometry mutation and rounding | NOT RUN | Stopped after the mandatory failure |
| Rich-text preservation | NOT RUN | Stopped after the mandatory failure |
| OOXML package preservation | **FAIL** | Free CLI/SDK save inserted a watermark and changed unrelated package parts |
| Rendering determinism | NOT RUN | Stopped after the mandatory failure |
| Microsoft PowerPoint viewer compatibility | NOT RUN | Stopped after the mandatory failure |
| Complete Linux ARM64 corpus | NOT RUN | Artifact identity/startup passed, but the complete gate requires all tests |
| Cancellation and orphan cleanup | NOT RUN | Stopped after the mandatory failure |
| 100-run repeatability | NOT RUN | Stopped after the mandatory failure |

The identity and preservation checks are limited root-cause probes requested
after the initial stop; they do not represent a resumed full corpus. `NOT RUN`
is not a failure claim about the other capabilities. It records the required
early stop after decisive mandatory failures.

## Decision And Repository Effect

The following work is prohibited under this proposal:

- adding the DocumentBuilder worker or binary to SparkClaw;
- adding OR-Tools to a production dependency manifest;
- changing `pptx.update_slide` or `pptx.update_deck` execution;
- adding layout engine configuration, schemas, Store records, or feature flags;
- changing Fast context, prompts, retries, or semantic repair for this engine;
- substituting LibreOffice, Aspose, another renderer, or a pixel/character
  measurement heuristic;
- continuing to Phase 1 through Phase 5.

SparkClaw retains the current PPTX implementation and behavior. This execution
changes only the bilingual design status and adds this bilingual qualification
record. Reconsidering another engine or a future DocumentBuilder release
requires a new explicit design decision rather than continuing this rejected
proposal.

## References

- [Rejected deterministic layout design](../docs/pptx-deterministic-layout-runtime-design.md)
- [DocumentBuilder v9.4.0 release](https://github.com/ONLYOFFICE/DocumentBuilder/releases/tag/v9.4.0)
- [DocumentBuilder installation and free-version watermark notice](https://api.onlyoffice.com/docs/document-builder/get-started/installing/)
- [ApiShape method inventory](https://api.onlyoffice.com/docs/office-api/usage-api/presentation-api/ApiShape/)
- [ApiDocumentContent method inventory](https://api.onlyoffice.com/docs/office-api/usage-api/presentation-api/ApiDocumentContent/)
