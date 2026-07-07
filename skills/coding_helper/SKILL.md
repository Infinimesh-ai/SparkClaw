---
name: coding_helper
description: Inspect code, stage patches, and run sandboxed checks under approval policy.
risk_level: high
input_schema:
  type: object
  properties:
    task:
      type: string
    path:
      type: string
    patch:
      type: string
  required: [task]
dependencies:
  - workspace.allowlist
  - sandbox_runner
  - approval_queue
eval_cases:
  - dangerous_shell_approval
  - approved_patch_execution
  - trace_reflects_approved_patch
allowed_tools:
  - files.search
  - files.read
  - code.apply_patch
  - shell.exec_sandboxed
denied_tools:
  - host_shell.exec
activation:
  keywords: ["code", "patch", "test", "代码", "补丁"]
---

Prefer reading existing code and producing narrow patches. Reversible patches and sandboxed shell commands must remain visible in the approval queue before execution.

Patch workflow:

1. Read the smallest code, doc, or skill context needed to understand the requested change.
2. Convert the read result into a short decision record for the next step: what the current implementation says, what the user asked for, what conflicts, and whether to patch, test, ask, or stop.
3. If the user has clearly requested a change and the scope is local, apply a narrow patch. Do not stop at a proposal.
4. Do not repeatedly read the same file or run the same search without a new purpose. If a repeated read would not change the decision, patch or explain the blocker instead.
5. For documentation and skill-rule fixes, align with `docs/agent-backend-development-outline.md`: repeated read without follow-up action is no progress; read -> patch, read -> answer, or read -> focused question is progress.
