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
