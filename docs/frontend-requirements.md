# SparkClaw Web Frontend Functional Requirements

> Language: English | [简体中文](../zh-cn/docs/frontend-requirements.md)

> Handoff document for a dedicated frontend team. The Gateway remains the source of truth for execution, policy, approval state, traces and persistence. Current typed contracts live in `apps/webchat/src/api/client.ts` and `apps/webchat/src/api/types.ts`; backend route definitions live in `services/gateway/internal/gateway/server.go`.

## 1. Product Goal

SparkClaw WebChat should be an owner-facing workbench for a local-first agent runtime. It is not a marketing page and should not feel like a generic chatbot. The UI needs to make the agent loop visible: session, user message, model response, tool calls, approval gates, memory review, trace, artifact and audit.

Frontend responsibilities:

- Present Gateway state accurately and promptly.
- Collect user input for chat, approvals, memory review and settings.
- Make risky actions highly visible before calling approval APIs.
- Keep API payload data such as IDs, paths, tool names, messages and model output unmodified.
- Provide a calm, usable control surface for long-running local workflows.

Non-goals:

- Do not reimplement Gateway policy decisions in the browser.
- Do not execute tools directly from the browser outside existing Gateway APIs.
- Do not hide approval, risk, trace, model-call or audit state behind decorative UI.

## 2. Delivery Priority

### P0: Required For Frontend/Backend Integration

- Gateway boot, health, auth token and pairing flows.
- Session list, session creation, session switching and message history.
- Chat composer and message send flow.
- Data refresh by session events and polling fallback.
- Tool timeline for active session.
- Approval inbox with approve, reject and JSON argument modification.
- Memory candidates and accepted-memory review.
- Trace list and trace detail view.
- Runtime status and visible error states.
- Native Simplified Chinese and English UI strings.
- Responsive desktop and mobile layout.

### P1: Product-Complete Workbench

- Run feedback controls for assistant messages.
- Model-call telemetry, audit events and episode summaries.
- Artifacts catalog.
- Smoke eval runner and eval history.
- Skills registry view.
- Owner profile edit.
- Paired client list and revoke action.
- Tool policy read/edit surface.
- Model profiles, adapters, sandbox, storage, state and memory config read-only views.

### P2: Useful Follow-Ups

- Tool catalog and manual tool invocation UI using `/api/tools` and `/api/tools/{name}/invoke`.
- Better filtering/search for sessions, approvals, traces, memories, artifacts and evals.
- Artifact download/open flow after a dedicated backend artifact-serving API exists.
- Rich JSON diff for approval argument edits.
- Persisted panel layout preferences.

## 3. Information Architecture

Use a three-zone desktop workbench:

- Left navigation: brand, language switch, Gateway health, auth/token status, new-session action and session list.
- Center conversation: active session title, runtime chips, message stream, starter prompts and composer.
- Right inspector: tabbed operational panels for timeline, approvals, memory, traces, status and settings.

On narrow screens, collapse to a single-column flow in this order: navigation, conversation, inspector. Inspector tabs must remain reachable without horizontal overflow.

## 4. Application Boot And Auth

Required startup behavior:

1. Load language from `localStorage`; default to Chinese when browser locale starts with `zh`, otherwise English.
2. Call `GET /readyz`, which is public. Show Gateway unavailable state if it fails.
3. If `readyz.auth_required` is true and no token exists, show token input and pairing options before private API calls.
4. Use `VITE_SPARKCLAW_API_TOKEN` first; otherwise use `localStorage` key `sparkclaw.api_token`.
5. Send `Authorization: Bearer <token>` for private `/api/*` requests when a token is configured.
6. After auth is available, load global state and sessions.
7. If no session exists, create a default session through `POST /api/sessions`.

Auth and pairing UI requirements:

- Token input supports save, clear and retry.
- Pairing uses `POST /api/pairing/start` followed by `POST /api/pairing/claim`; pairing can fail when not required or not local, and the UI must show that cleanly.
- Handle `401` as an auth problem, `429` as rate-limited, and network errors as Gateway unavailable.
- Keep language switching available even when Gateway calls fail.

## 5. Refresh Model

Separate refresh into two scopes:

- Global scope: readiness, config, owner, clients, approvals, memory candidates, accepted memories, skills, eval runs, artifacts and trace metadata.
- Active-session scope: messages, tool calls, model calls, audit events and episode summaries.

Use session events when possible:

- `GET /api/sessions/{id}/events/stream` emits server-sent events.
- Native `EventSource` cannot set an `Authorization` header. When bearer auth is required, use polling or an EventSource-compatible authenticated transport.
- Poll active-session and global state every few seconds as fallback.
- Refresh immediately after chat send, approval resolution, approval modification, memory action, feedback save, owner update, client revoke, policy update and eval run.

Important event types currently emitted by the store include:

- `message.created`
- `tool_call.started`, `tool_call.completed`, `tool_call.approval_pending`, `tool_call.running_after_approval`, `tool_call.completed_after_approval`, `tool_call.failed_after_approval`, `tool_call.rejected`
- `approval.pending`, `approval.approved`, `approval.rejected`
- `memory_candidate.created`, `memory_candidate.accepted`, `memory_candidate.rejected`
- `episode_summary.saved`

## 6. Functional Modules

### 6.1 Navigation And Session List

Required behavior:

- Show Gateway ready/offline state and model mode.
- Show count badges for sessions, pending approvals and pending memory candidates.
- Create a new session.
- Switch active session without losing global state.
- Display session title, short ID and updated time.
- Long titles and IDs must truncate or wrap without breaking layout.

Backend:

- `GET /readyz`
- `GET /api/sessions`
- `POST /api/sessions`

### 6.2 Chat

Required behavior:

- Render user and assistant messages in chronological order.
- Display role, timestamp and assistant `run_id` affordance when present.
- Submit non-empty messages through the active session.
- Show busy state while a message is being processed.
- Refresh messages, tools, approvals, traces and status after send.
- Provide starter prompts for common local workflows: file search, browser read, email search, calendar read, memory proposal and sandbox command.
- Assistant messages with `run_id` expose feedback: helpful, unhelpful and corrected answer.

Backend:

- `GET /api/sessions/{id}/messages`
- `POST /api/sessions/{id}/messages`
- `POST /api/runs/{id}/feedback`

### 6.3 Tool Timeline

Required behavior:

- Show every tool call for the active session.
- Each tool row/card includes tool icon, tool name, risk, status, observation summary, error, approval ID and trace action.
- Expandable detail should show arguments and result in formatted JSON.
- Risk levels use consistent visual treatment: `read`, `draft`, `reversible`, `dangerous`.
- Approval-pending and dangerous states must be visually prominent.

Backend:

- `GET /api/sessions/{id}/tool-calls`
- `GET /api/tool-calls/{id}`
- `GET /api/traces/{run_id}`

### 6.4 Approval Inbox

Required behavior:

- Show pending approvals first, then resolved approvals.
- Include tool, risk, status, summary, reason, resources, session ID, run ID and created/resolved time.
- Approve and reject pending approvals.
- Allow editing JSON arguments before approval.
- Treat arguments starting with `_`, such as `_verifier`, as system/verifier metadata: show read-only or separate from editable user arguments.
- Validate JSON before calling modify.
- After modification, show updated resources/arguments returned by Gateway.
- After approve/reject, refresh approvals, active-session timeline, traces and audit.

Backend:

- `GET /api/approvals`
- `GET /api/approvals?status=pending`
- `POST /api/approvals/{id}/approve`
- `POST /api/approvals/{id}/reject`
- `POST /api/approvals/{id}/modify`

### 6.5 Memory Review

Required behavior:

- Separate pending/resolved memory candidates from accepted memories.
- Candidate cards show kind, sensitivity, status, reason, content, session ID and run ID.
- Pending candidates support accept and reject.
- Accepted memories support edit and delete.
- Memory edit validates non-empty kind and content before submit.
- Show sensitive-memory errors returned by Gateway without masking the reason.
- Archive/export memory snapshot and show the resulting artifact reference.

Backend:

- `GET /api/memory-candidates`
- `GET /api/memory-candidates?status=pending`
- `POST /api/memory-candidates/{id}/accept`
- `POST /api/memory-candidates/{id}/reject`
- `GET /api/memories`
- `GET /api/memories?query=...`
- `POST /api/memories/{id}/update`
- `POST /api/memories/{id}/delete`
- `GET /api/memories/export`
- `POST /api/memories/export`

### 6.6 Trace And Run Diagnostics

Required behavior:

- Show recent trace metadata and open trace detail by `run_id`.
- Trace summary includes run state, risk, model lane, model, model-call count, token count, average latency, tool count, approval count, feedback count, audit count and artifact reference.
- Trace detail shows model note, model calls, messages, tool calls, approvals, feedback, audit and episode summary when present.
- Links from assistant messages, tool calls and approvals should open the relevant run trace.
- Display redacted data exactly as returned by Gateway.

Backend:

- `GET /api/traces`
- `GET /api/traces?limit=...`
- `GET /api/traces/{run_id}`
- `GET /api/sessions/{id}/model-calls`
- `GET /api/sessions/{id}/audit`
- `GET /api/sessions/{id}/episodes`

### 6.7 Runtime Status

Required behavior:

- Show Gateway binding, model mode, workspace root, trace dir, state backend/path/DSN status and rate limit.
- Show recent model-call telemetry for the active session.
- Show recent audit events for the active session.
- Show artifact catalog entries: kind, backend, URI/path, content type, size, run/eval/session reference and created time.
- Show episode summaries: goal, outcome, risk, model lane, tools, approvals, failures and repair flag.
- Show registered skills: name, description, risk, allowed/denied tools, dependencies, eval cases and path.

Backend:

- `GET /readyz`
- `GET /api/config`
- `GET /api/artifacts`
- `GET /api/skills`
- `GET /api/sessions/{id}/model-calls`
- `GET /api/sessions/{id}/audit`
- `GET /api/sessions/{id}/episodes`
- Optional raw diagnostics link: `GET /metrics`

### 6.8 Eval Panel

Required behavior:

- Run the smoke profile from the UI.
- Display current run status, summary, case list, durations and failure archive references.
- Display eval history and allow selecting a previous run.
- Disable run button while the request is in flight.
- Refresh artifacts after eval completion.

Backend:

- `POST /api/evals/run` with `{ "profile": "smoke" }`
- `GET /api/evals`
- `GET /api/evals/{id}`

### 6.9 Settings

Required behavior:

- Owner profile: view and edit display name, email and preferences.
- Preferences can be edited as key-value rows or a validated structured editor.
- Paired clients: show active/revoked clients, created time, last seen and revoke action.
- Tool policy: show policy path, risk counts, definition approval tools, configured approval-required tools, denied tools and browser allow hosts.
- Tool policy edit must prevent the same tool from being both denied and approval-required before submit, while still surfacing Gateway validation errors.
- Model profiles: show fast, deep, embedding, reranker and guard profile name, model, base URL, context tokens, max tokens and MTP flag.
- Runtime boundaries: show gateway bind/port/remote access, workspace allowlist, sandbox config, storage, state, adapters, memory config and skill dirs.

Backend:

- `GET /api/owner`
- `POST /api/owner`
- `GET /api/clients`
- `POST /api/clients/{id}/revoke`
- `GET /api/config`
- `POST /api/tool-policy`

### 6.10 Native Bilingual UX

Required behavior:

- Support Simplified Chinese (`zh`) and English (`en`) with local dictionaries.
- Persist language in `localStorage` key `sparkclaw.language`.
- Default to Chinese for browser locales starting with `zh`; otherwise English.
- Localize static UI strings, tab labels, placeholders, starter prompts, empty states, action labels, validation messages, error fallbacks and status summaries.
- Do not translate API data, IDs, tool names, paths, user messages or assistant messages.

## 7. API Contract Summary

| Area | Endpoints |
|---|---|
| Health/auth | `GET /readyz`, `POST /api/pairing/start`, `POST /api/pairing/claim` |
| Config/settings | `GET /api/config`, `GET /api/owner`, `POST /api/owner`, `GET /api/clients`, `POST /api/clients/{id}/revoke`, `POST /api/tool-policy` |
| Sessions/chat | `GET /api/sessions`, `POST /api/sessions`, `GET /api/sessions/{id}`, `GET /api/sessions/{id}/messages`, `POST /api/sessions/{id}/messages` |
| Events | `GET /api/sessions/{id}/events`, `GET /api/sessions/{id}/events/stream` |
| Runs/telemetry | `POST /api/runs/{id}/feedback`, `GET /api/sessions/{id}/tool-calls`, `GET /api/sessions/{id}/model-calls`, `GET /api/sessions/{id}/audit`, `GET /api/sessions/{id}/episodes` |
| Approvals | `GET /api/approvals`, `POST /api/approvals/{id}/approve`, `POST /api/approvals/{id}/reject`, `POST /api/approvals/{id}/modify` |
| Memory | `GET /api/memories`, `GET /api/memories/export`, `POST /api/memories/export`, `POST /api/memories/{id}/update`, `POST /api/memories/{id}/delete`, `GET /api/memory-candidates`, `POST /api/memory-candidates/{id}/accept`, `POST /api/memory-candidates/{id}/reject` |
| Trace/artifacts | `GET /api/traces`, `GET /api/traces/{run_id}`, `GET /api/artifacts` |
| Evals/skills/tools | `GET /api/evals`, `POST /api/evals/run`, `GET /api/evals/{id}`, `GET /api/skills`, `GET /api/tools`, `POST /api/tools/{name}/invoke` |

## 8. UX And Visual Requirements

- Overall tone: quiet operator console, not decorative landing page.
- Use dense but readable typography; do not scale font size directly with viewport width.
- Use neutral surfaces with restrained green, amber, red, blue and graphite accents.
- Use cards only for repeated objects: messages, tool calls, approvals, traces, memories, clients, eval cases and artifacts.
- Keep controls and cards at 8px border radius or below unless a future design system says otherwise.
- Prefer icon buttons with tooltips for compact actions.
- Long paths, IDs, model names and JSON payloads must wrap or truncate cleanly.
- Empty, loading, success, failed, disabled and offline states must be designed for every panel.
- Dangerous approvals and failed-after-approval tool calls must be visually hard to miss.

## 9. Accessibility And Responsiveness

- Keyboard users must be able to switch sessions, send messages, change tabs, approve/reject, edit JSON and save settings.
- Icon-only controls need accessible labels and visible focus states.
- Color cannot be the only risk/status signal.
- Mobile layout must avoid horizontal page scrolling.
- JSON/code blocks, long URIs and model names must not overflow their containers.
- Composer and approval editors must remain usable on narrow screens.

## 10. Suggested Frontend Structure

The current `App.tsx` is too large for long-term ownership. A frontend rewrite should split by responsibility:

```text
apps/webchat/src/
  api/
    client.ts
    types.ts
  i18n/
    dictionaries.ts
  app/
    AppShell.tsx
    useBootstrap.ts
    useRefresh.ts
  features/
    sessions/
    chat/
    timeline/
    approvals/
    memory/
    traces/
    status/
    evals/
    settings/
  components/
    Button.tsx
    EmptyState.tsx
    JsonBlock.tsx
    RiskPill.tsx
    StatusChip.tsx
    Tabs.tsx
```

Keep state management lightweight unless real complexity requires otherwise. Existing dependencies are React, Vite, TypeScript and `lucide-react`; avoid heavy i18n or state libraries unless the team deliberately accepts that cost.

## 11. Acceptance Checklist

- `npm --workspace @sparkclaw/webchat run build` passes.
- With mock Gateway, the UI can boot, create a session, send a message and show the assistant response.
- A tool-using message updates the timeline and opens the run trace.
- A dangerous or reversible action appears in approvals, can be modified, approved and rejected, and the timeline reflects the result.
- A memory candidate can be accepted/rejected; an accepted memory can be edited/deleted; memory export creates an artifact reference.
- Trace view shows model calls, tools, approvals, feedback, audit and episode data when available.
- Smoke eval can be started, inspected and selected from history.
- Owner profile, client revoke and tool policy edit work against Gateway APIs.
- Auth-required mode supports token save, clear/retry and pairing failure states.
- Chinese and English can be switched without Gateway availability.
- Desktop and mobile layouts show no overlapping text, clipped buttons or horizontal overflow.
