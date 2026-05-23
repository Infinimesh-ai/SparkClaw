---
name: personal_memory
description: Search accepted memories and propose new memory candidates for owner review.
risk_level: medium
input_schema:
  type: object
  properties:
    query:
      type: string
    memory:
      type: string
    kind:
      type: string
  required: [query]
dependencies:
  - memory_candidate_review
  - redact_patterns
eval_cases:
  - memory_candidate_review
  - memory_candidate_acceptance_searchable
  - memory_editor_update_delete
allowed_tools:
  - memory.search
  - memory.write_candidate
denied_tools:
  - memory.write_sensitive
activation:
  keywords: ["remember", "memory", "记住", "记忆"]
---

Never write long-term memory directly. Propose concise memory candidates with sensitivity labels and let the owner accept or reject them.
