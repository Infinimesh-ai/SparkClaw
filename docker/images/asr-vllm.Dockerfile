ARG SPARKCLAW_VLLM_IMAGE=vllm/vllm-openai:cu130-nightly
FROM ${SPARKCLAW_VLLM_IMAGE}

COPY services/asr-runtime/requirements.txt /tmp/sparkclaw-asr-requirements.txt
RUN python3 -m pip install --no-cache-dir \
    "av" \
    "mistral_common[audio]" \
    "scipy" \
    "soundfile" \
    -r /tmp/sparkclaw-asr-requirements.txt \
    && python3 -m pip install --no-cache-dir --no-deps "qwen-asr==0.0.6" \
    && SPARKCLAW_QWEN_MODELING="$(python3 -c 'import importlib.metadata as m; print(m.distribution("qwen-asr").locate_file("qwen_asr/core/transformers_backend/modeling_qwen3_asr.py"))')" \
    && sed -i 's/@check_model_inputs()/@check_model_inputs/' "$SPARKCLAW_QWEN_MODELING" \
    && grep -q '^    @check_model_inputs$' "$SPARKCLAW_QWEN_MODELING" \
    && python3 -c 'import qwen_asr' \
    && rm /tmp/sparkclaw-asr-requirements.txt

COPY services/asr-runtime/runtime.py /opt/sparkclaw-asr/runtime.py
COPY services/asr-runtime/test_runtime.py /opt/sparkclaw-asr/test_runtime.py

ENTRYPOINT ["python3", "/opt/sparkclaw-asr/runtime.py"]
