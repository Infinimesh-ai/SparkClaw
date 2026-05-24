## Summary

> 语言： [English](../../.github/pull_request_template.md) | 简体中文

描述变更。

## Verification

- [ ] `npm --workspace @sparkclaw/webchat run build`
- [ ] `go test ./services/gateway/...`
- [ ] `bash scripts/doctor.sh`
- [ ] `bash scripts/run-eval.sh`
- [ ] `docker compose --env-file .env -f docker/compose.yaml config --quiet`

## Risk

什么可能出问题，边界在哪里？

## Documentation

- [ ] README/docs updated, or not needed
- [ ] `zh-cn/` mirror updated for project docs, or not needed
- [ ] No secrets, traces, local state or model weights included
