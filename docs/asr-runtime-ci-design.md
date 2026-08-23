# ASR Runtime CI Design

> Language: English | [简体中文](../zh-cn/docs/asr-runtime-ci-design.md)

> Status: draft independent delivery, 2026-08-19. This document is reviewed and
> implemented separately from the Store stage chain.

## Objective

Run the existing deterministic fake-model ASR protocol suite in CI without a
GPU, model download, vLLM runtime, or `qwen-asr` import.

## Dependency Contract

Add `services/asr-runtime/requirements-test.txt` with exact pins for imports
needed by `runtime.py` and `test_runtime.py`:

- NumPy;
- FastAPI and Starlette TestClient dependencies, including the compatible HTTP
  client;
- SoundFile;
- Uvicorn;
- multipart form support.

Production model packages remain in the image-only requirements and are not
installed by this job. A CI import guard proves that importing the runtime and
constructing the fake-model app does not import `qwen_asr` or `vllm` modules.

## Test Contract

The suite covers:

- FastAPI construction, lifespan cleanup, health, version, and model listing;
- OpenAI-compatible batch request and response behavior with `FakeModel`;
- model construction and warm-up on the owner worker thread;
- realtime start, ready, ordered frames, acknowledgements, partial revisions,
  finalization, and minimum-duration rejection;
- sequence gaps and malformed control/frame rejection;
- operation-gate release after successful batch/realtime completion and every
  covered failure path;
- explicit TestClient/worker close so the job leaves no executor thread.

Tests use generated in-memory WAV/PCM fixtures and perform no network access.

## CI Job

Add an independent `asr-runtime` job on Python 3.12:

```bash
python -m pip install -r services/asr-runtime/requirements-test.txt
python -m unittest discover -s services/asr-runtime -p 'test_*.py'
```

The job does not depend on Gateway, WebChat, Compose, PostgreSQL, NVIDIA, or
model caches. Its dependency cache key includes the test manifest.

## Review Gate

Design `GO` requires accepted pins, import guard, lifecycle cleanup, and test
coverage. Implementation `GO` requires a clean local run, a green isolated CI
job, proof that no production model package was installed/imported, and no
change to PostgreSQL CI configuration.

ASR failure does not invalidate an accepted Store stage, and Store failure does
not obscure ASR job ownership.

## Review Record

| Review | Revision/commit | Decision | Evidence and unresolved risks | Reviewer/date |
|---|---|---|---|---|
| Design | pending | pending | pending | pending |
| Implementation | pending | pending | pending | pending |
