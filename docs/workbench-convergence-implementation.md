# Workbench convergence implementation

> Language: English | [简体中文](../zh-cn/docs/workbench-convergence-implementation.md)

Date: 2026-10-06. Implementation branch: `codex/workbench-runtime-convergence`.
Starting commit: `9b92968d`. This record covers source and isolated verification,
not a running-service upgrade. The [design](workbench-runtime-convergence-design.md)
defines the confirmed product behavior.

## Phase 0: baseline and boundaries

All eight design/architecture/index documents exactly matched the source checkout
before editing, including section 6.1. The source checkout remains untouched.
InfiniCenter was found on the known Linux host at `/home/infinimesh/InfiniCenter`.
Registry, empty inbox, central C0001, proposed 0031, and the accepted JingSi,
App-CLI and IMMS contracts were reviewed. The implementation review is appended
to 0031; it remains proposed. No external protocol or retention change is accepted
by this internal plan.

| Area | Starting behavior | Disposition |
|---|---|---|
| History | Host Store versus desktop SQLite/files | Preserve local ownership and authenticated scope; unify selection rules |
| Runtime | Desktop constructs a second ToolHub and policy | Share live providers, policy and registry; isolate repositories and resource adapters |
| Context | Host typed history versus desktop 32-message, 96 KiB envelope | Separate transport bounds from common model-context selection |
| Host resources | Host workspace and acquisition browser versus explicitly bound desktop Host | Preserve resource authorization; no implicit access to another workbench |
| Delivery | Host result persistence versus desktop verified files and ACK | Preserve durable completion and query-only recovery; no retry of uncertain writes |
| Schedules | Host overdue scan; desktop renewable backend lease | Explicit behavior change: local occurrence claims, permanently missed offline rounds |
| Mail | Backend authoritative; desktop cache | Preserve one collector and service authority |
| External callers | JingSi durable results/events, App-CLI journals, IMMS evidence | Unchanged frozen contracts and retention; no reset/import |

Baseline environment: macOS ARM64, Go 1.25.12, Node 26.2.0/npm 11; isolated
Linux ARM64 Go 1.25.5/Node 26.2.0. Declared document dependencies were installed
with `npm run setup:document-tools` before tests on both hosts.

| Baseline check | Result |
|---|---|
| Mac Go build | Passed |
| Mac Go full tests | Existing failures in Gateway, cmd/sparkclaw and browser-host tests: canonical temporary-path guards and Linux `/dev/shm` requirement; remaining packages passed |
| Linux Go build / vet / full tests | Passed in a fresh isolated source snapshot, with central JingSi conformance manifest |
| Desktop | 102 passed, no skips |
| WebChat | 196 passed; TypeScript/Vite build passed |
| Bilingual docs/local links | 103 English documents checked after phase-1 guide updates |

Mac logs are under ignored `.cache/convergence/`; Linux baseline logs and snapshot
are under `/home/infinimesh/.cache/sparkclaw-convergence-20261006-DIrlhW`.
Tests use isolated files and do not connect to production business services.

## Phase 1: current guides

The README, architecture/index, Store and WebChat guides now distinguish host
workbench persistence from execution content. The Mac guide uses `main`, points
to dated native evidence, and no longer describes already-delivered facilities
as unimplemented. Existing hardware acceptance gaps remain explicit.

## Remaining phase gates

Public runtime assembly, workbench behavior, neutral naming/API/storage cutover
and release qualification are still being implemented. Each following commit
must add its actual validation here before these gates are declared complete.
No push, main merge, running-service deployment or real-data deletion is authorized
by this implementation record.

## Common context admission

`configs/workbench-limits.json` is the limit source; the checked generator projects
constants into Go and the packaged desktop client. Both workbenches accept up to
64 KiB UTF-8 owner input; submitted context is at most 1 MiB/32 messages. The model
context selector uses the same last-eight conversation rule and UTF-8 message
bounds for host and submitted repositories. Active model token admission remains
authoritative: these byte caps do not promise that every 64 KiB input fits the
configured embedding/guard models. The larger transport allowance removes the
old desktop-only 16 KiB bottleneck and leaves room for the selected history.
Result/file and 24-hour delivery limits are unchanged.

Focused tests compare host FileStore and submitted MemoryStore selection, reject
cross-conversation history, verify UTF-8 boundaries and equal 64 KiB HTTP/envelope
admission, and check generated contract freshness. Go build/vet plus agent,
workbench and execution packages passed; all 116 desktop tests passed after the
new limits and scheduler integration.
