# ASR Runtime CI 设计

> 语言：[English](../../docs/asr-runtime-ci-design.md) | 简体中文

> 状态：独立交付草案，2026-08-19。本文档与 Store 阶段链分开审查和实现。

## 目标

在 CI 中运行现有确定性 fake-model ASR 协议 suite，不需要 GPU、模型下载、
vLLM runtime 或导入 `qwen-asr`。

## 依赖契约

增加 `services/asr-runtime/requirements-test.txt`，精确 pin `runtime.py` 和
`test_runtime.py` 的必要 import：

- NumPy；
- FastAPI 和 Starlette TestClient 依赖，包括兼容的 HTTP client；
- SoundFile；
- Uvicorn；
- multipart form 支持。

生产模型 package 保留在 image-only requirements 中，不由该 job 安装。CI
import guard 证明导入 runtime 并构造 fake-model app 不会导入 `qwen_asr` 或
`vllm` module。

## 测试契约

suite 覆盖：

- FastAPI 构造、lifespan cleanup、health、version 和 model listing；
- 使用 `FakeModel` 的 OpenAI-compatible batch request/response；
- model 在 owner worker thread 上构造和 warm-up；
- realtime start、ready、有序 frame、ack、partial revision、finalization 和
  最短时长拒绝；
- sequence gap 和畸形 control/frame 拒绝；
- batch/realtime 成功和每个已覆盖失败路径后的 operation-gate release；
- 显式关闭 TestClient/worker，确保 job 不残留 executor thread。

测试使用内存生成的 WAV/PCM fixture，不执行网络访问。

## CI Job

增加独立 Python 3.12 `asr-runtime` job：

```bash
python -m pip install -r services/asr-runtime/requirements-test.txt
python -m unittest discover -s services/asr-runtime -p 'test_*.py'
```

该 job 不依赖 Gateway、WebChat、Compose、PostgreSQL、NVIDIA 或 model cache。
依赖 cache key 包含测试 manifest。

## 审查门禁

设计 `GO` 要求 pin、import guard、lifecycle cleanup 和测试覆盖已接受。实现
`GO` 要求本地干净运行、隔离 CI job 绿色、证明没有安装/导入生产模型 package，
且 PostgreSQL CI 配置没有变化。

ASR 失败不使已接受 Store 阶段失效，Store 失败也不掩盖 ASR job 的 owner。

## 审查记录

| 审查 | 修订/commit | 结论 | 证据和未解决风险 | 审查人/日期 |
|---|---|---|---|---|
| 设计 | pending | pending | pending | pending |
| 实现 | pending | pending | pending | pending |
