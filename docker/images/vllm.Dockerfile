ARG SPARKCLAW_VLLM_IMAGE=vllm/vllm-openai:cu130-nightly
FROM ${SPARKCLAW_VLLM_IMAGE}

COPY scripts/model_readiness.py /opt/sparkclaw/model_readiness.py
