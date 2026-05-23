# SparkClaw Policy Engine

The MVP policy engine lives in `services/gateway/internal/policy`.

Core invariant:

- denied tools never run
- dangerous tools require approval
- reversible tools require approval in the MVP
- mutating tools require sandbox policy coverage
- approval records include reason, resources and raw arguments

See `configs/tools.policy.json` and `configs/sandbox.policy.json` for the default policy surface.
