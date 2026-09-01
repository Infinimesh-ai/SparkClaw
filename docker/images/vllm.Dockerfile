ARG SPARKCLAW_VLLM_IMAGE=vllm/vllm-openai:cu130-nightly
FROM ${SPARKCLAW_VLLM_IMAGE}

COPY scripts/model_readiness.py /opt/sparkclaw/model_readiness.py
COPY scripts/model_capacity_entrypoint.py /opt/sparkclaw/model_capacity_entrypoint.py
COPY configs/model.profiles.json /opt/sparkclaw/model.profiles.json
