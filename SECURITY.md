# Security Policy

> Language: English | [简体中文](zh-cn/SECURITY.md)

SparkClaw is a local-first personal agent runtime. It is not designed as a hostile multi-tenant service or a public internet Gateway.

## Supported Versions

The `main` branch is the supported development line until the project starts publishing tagged releases. Security fixes should target `main` first.

## Reporting A Vulnerability

Please do not open a public issue for a suspected vulnerability. Report privately to the maintainers through the repository owner's preferred private contact channel. If no private channel is available yet, open a minimal public issue asking for a security contact without including exploit details.

Include:

- affected commit or version
- setup details
- impact
- reproduction steps
- logs or traces with secrets removed
- suggested fix, if known

## Security Boundary

Expected secure defaults:

- Gateway binds to localhost by default.
- Shared machines should set `SPARKCLAW_API_TOKEN`.
- `.env`, state encryption keys, traces, local state and model caches must stay out of git.
- Browser reads reject loopback/private hosts unless explicitly allowlisted.
- Shell execution runs through the sandbox runner and is network-disabled.
- Reversible and dangerous actions require approval.
- Tool observations are untrusted data.

Issues that break these boundaries are security-sensitive.

## Out Of Scope

- Compromise of a host where the attacker already controls the owner account.
- Misconfigured public exposure of Gateway contrary to documented deployment guidance.
- Malicious model weights or third-party services outside SparkClaw's control.
- Secrets intentionally pasted into prompts, files or traces and then manually shared.
