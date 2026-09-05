"""Scoped native-vLLM input hook for accepted decision 0031.

This module never loads model weights or performs inference. HTTP route/attempt
admission belongs to the separately pinned HTTPS boundary, not this hook.
"""
from __future__ import annotations

import hashlib
import os
import stat
from pathlib import Path

PROFILE_ENV = "IMMS_SET_TOKENIZER_PROFILE"
PROFILE = "imms-set-native-v1"
MODEL_REVISION = "22e683669bc0f0bd69640a1354a6d0aebcfeede5"
MODEL_CONFIG_SHA256 = "38bff5eac700032a185745e4076eccad7aa453473cafc2a27de412cdb7b79e19"
MODEL_CONFIG_SIZE = 727
SERVED_MODEL = "sparkclaw-reranker"


class SetProcessorError(ValueError):
    def __init__(self, code):
        self.code = code
        super().__init__(code)


def _require(ok, code="set_profile_mismatch"):
    if not ok:
        raise SetProcessorError(code)


def _read_regular(path, sha256, size):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        before = os.fstat(fd)
        _require(stat.S_ISREG(before.st_mode) and before.st_size == size, "set_source_identity")
        raw = b""
        while len(raw) <= size:
            chunk = os.read(fd, min(1024 * 1024, size + 1 - len(raw)))
            if not chunk:
                break
            raw += chunk
        after = os.fstat(fd)
        _require((before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns,
                  before.st_ctime_ns) == (after.st_dev, after.st_ino, after.st_size,
                  after.st_mtime_ns, after.st_ctime_ns), "set_source_identity")
        _require(len(raw) == size and hashlib.sha256(raw).hexdigest() == sha256,
                 "set_source_identity")
        return raw
    finally:
        os.close(fd)


def _implementation():
    from scripts import e2_set_tokenizer_v1
    return e2_set_tokenizer_v1


def _validate_model(processor):
    m = processor.model_config
    v = processor.vllm_config
    h = m.hf_config
    p = m.pooler_config
    _require(m.architecture == "Qwen3ForSequenceClassification")
    _require(m.revision == MODEL_REVISION and m.tokenizer_revision in (None, MODEL_REVISION))
    _require(Path(m.model).is_absolute() and Path(m.model).name == MODEL_REVISION)
    _require(str(m.tokenizer) == str(m.model))
    _require(m.served_model_name == SERVED_MODEL)
    _require(m.runner_type == "pooling" and str(m.dtype) == "torch.bfloat16")
    _require(str(m.head_dtype) == "torch.float32" and m.quantization is None)
    _require(m.max_model_len == 8192 and m.enforce_eager is True)
    _require(m.trust_remote_code is False and m.is_multimodal_model is False)
    _require(h.architectures == ["Qwen3ForSequenceClassification"])
    _require(h.classifier_from_token == ["no", "yes"])
    _require(h.is_original_qwen3_reranker is True and h.method == "from_2_way_softmax")
    _require(h.num_labels == 1 and h.problem_type == "single_label_classification")
    _require(p.task == "classify" and p.seq_pooling_type == "LAST")
    _require(p.use_activation is True and p.logit_mean is None and p.logit_sigma is None)
    _require(v.cache_config.enable_prefix_caching is False)
    _require(v.parallel_config.tensor_parallel_size == 1 and v.scheduler_config.max_num_seqs == 1)
    _require(processor.supports_score_template is False and processor.model is None)
    _require(processor.use_sep_token is False)
    _require(processor.tokenizer.convert_tokens_to_ids("no") == 2152)
    _require(processor.tokenizer.convert_tokens_to_ids("yes") == 9693)


def initialize_profile(processor):
    selected = os.environ.get(PROFILE_ENV)
    processor._imms_set_profile = selected
    processor._imms_set_tokenizer = None
    if selected is None:
        return
    _require(selected == PROFILE, "set_profile_unknown")
    _validate_model(processor)
    implementation = _implementation()
    root = Path(processor.model_config.model)
    _read_regular(root / "config.json", MODEL_CONFIG_SHA256, MODEL_CONFIG_SIZE)
    raw = _read_regular(root / "tokenizer.json", implementation.TOKENIZER_SHA256,
                        implementation.TOKENIZER_SIZE)
    template = _read_regular(root / "serving/qwen3_reranker.jinja",
                             implementation.TEMPLATE_SHA256, implementation.TEMPLATE_SIZE)
    _require(processor.chat_template == template.decode("utf-8"), "set_template_mismatch")
    processor._imms_set_template = processor.chat_template
    processor._imms_set_tokenizer = implementation.SetTokenizerV1(raw)


def _enabled(processor):
    selected = os.environ.get(PROFILE_ENV)
    _require(selected == processor._imms_set_profile, "set_profile_changed")
    if selected is None:
        return False
    _require(selected == PROFILE and processor._imms_set_tokenizer is not None)
    _require(processor.chat_template == processor._imms_set_template, "set_template_mismatch")
    _validate_model(processor)
    return True


def validate_online(processor, ctx):
    if not _enabled(processor):
        return
    from vllm.entrypoints.pooling.scoring.protocol import RerankRequest
    request = ctx.request
    _require(type(request) is RerankRequest, "set_rerank_only")
    expected_fields = {"model", "query", "documents", "top_n", "instruction",
                       "max_tokens_per_query", "max_tokens_per_doc", "priority",
                       "truncate_prompt_tokens", "truncation_side", "use_activation"}
    # Pydantic's instruction validator also sets the derived kwargs field.
    _require(request.model_fields_set - {"chat_template_kwargs"} == expected_fields,
             "set_request_options")
    _require(not getattr(request, "model_extra", None), "set_request_options")
    _require(type(request.query) is str and type(request.documents) is list
             and len(request.documents) == 1 and type(request.documents[0]) is str,
             "set_single_pair")
    _require(request.model == SERVED_MODEL and request.top_n == 1)
    _require(request.instruction == _implementation().INSTRUCTION, "set_instruction_mismatch")
    _require(request.chat_template_kwargs == {"instruction": _implementation().INSTRUCTION},
             "set_instruction_mismatch")
    _require(request.max_tokens_per_query == 0 and request.max_tokens_per_doc == 0)
    _require(request.priority == 0 and request.use_activation is True)
    _require(request.truncate_prompt_tokens is None and request.truncation_side is None)
    _require(getattr(request, "pad_prompt_tokens", None) is None)
    _require(getattr(request, "cache_salt", None) is None)
    _require(getattr(request, "mm_processor_kwargs", None) is None)


def get_score_prompt(processor, data_1, data_2, encode_kwargs, chat_template,
                     max_tokens_per_query, max_tokens_per_doc, chat_template_kwargs):
    if not _enabled(processor):
        return None
    implementation = _implementation()
    _require(type(data_1) is str and type(data_2) is str, "set_single_pair")
    _require(chat_template == processor.chat_template, "set_template_mismatch")
    _require(chat_template_kwargs == {"instruction": implementation.INSTRUCTION},
             "set_instruction_mismatch")
    _require(max_tokens_per_query == 0 and max_tokens_per_doc == 0, "set_token_options")
    # vLLM's encode kwargs cap its old tokenizer at max_input+1. They are
    # validated here but deliberately never passed to the exact tokenizer.
    _require(encode_kwargs == {"add_special_tokens": True, "truncation": True,
                               "max_length": 8193}, "set_token_options")
    prepared = processor._imms_set_tokenizer.prepare(data_1, data_2, allow_capability_raw=True)
    return prepared["formatted_text"], {"prompt_token_ids": list(prepared["token_ids"])}


def capture_expected_tokens(processor, prompt):
    if not _enabled(processor):
        return None
    _require(set(prompt) == {"prompt_token_ids"}, "set_engine_shape")
    ids = prompt["prompt_token_ids"]
    _require(type(ids) is list and 0 < len(ids) <= 8192
             and all(type(i) is int and i >= 0 for i in ids), "set_engine_tokens")
    return tuple(ids)


def validate_engine_tokens(processor, expected, tok_params, prompt, engine_input):
    if not _enabled(processor):
        _require(expected is None, "set_profile_changed")
        return
    _require(tok_params.pad_prompt_tokens is None and tok_params.truncate_prompt_tokens is None,
             "set_token_options")
    _require(tok_params.max_input_tokens == 8192 and tok_params.max_output_tokens == 0,
             "set_token_options")
    _require(type(expected) is tuple and tuple(prompt.get("prompt_token_ids", ())) == expected,
             "set_post_tokenization_changed")
    _require(set(prompt) == {"prompt_token_ids"}, "set_engine_shape")
    _require(engine_input.get("type") == "token" and
             tuple(engine_input.get("prompt_token_ids", ())) == expected,
             "set_engine_tokens_changed")
    _require(set(engine_input) == {"type", "prompt_token_ids", "arrival_time"},
             "set_engine_shape")
