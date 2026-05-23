# SparkClaw Tool Schema

Tool definitions must declare:

- name and description
- JSON Schema input contract
- risk level
- approval requirement
- idempotency
- timeout
- sandbox policy
- audit policy

The Go ToolHub registers the MVP tools today. These schemas are the portable contract for future service splits and external tool packs.
