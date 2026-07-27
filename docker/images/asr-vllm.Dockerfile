ARG SPARKCLAW_VLLM_IMAGE=vllm/vllm-openai:cu130-nightly
FROM ${SPARKCLAW_VLLM_IMAGE}

RUN python3 -m pip install --no-cache-dir \
    "av" \
    "mistral_common[audio]" \
    "scipy" \
    "soundfile"
