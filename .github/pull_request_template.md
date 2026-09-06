## Summary

> Language: English | [简体中文](../zh-cn/.github/pull_request_template.md)

Describe the change.

## Verification

- [ ] `npm --workspace @sparkclaw/webchat run build`
- [ ] `go test ./services/gateway/...`
- [ ] `bash scripts/doctor.sh`
- [ ] `bash scripts/run-eval.sh`
- [ ] Local and remote Compose profiles both expand successfully

## Risk

What can break, and how is it bounded?

## Documentation

- [ ] README/docs updated, or not needed
- [ ] `zh-cn/` mirror updated for project docs, or not needed
- [ ] No `.env.local`/`.env.remote`, secrets, traces, local state or model weights included
