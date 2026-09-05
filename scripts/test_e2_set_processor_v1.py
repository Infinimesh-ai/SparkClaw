"""CPU-only profile/patch gates; native_integration never loads model weights."""
import ast
import copy
import hashlib
import json
import os
import tempfile
import types
import unittest
from pathlib import Path
from unittest import mock

from scripts import e2_set_processor_v1 as hook
from scripts.patch_e2_set_processor_v1 import patch_source


def fake_processor():
    ns = types.SimpleNamespace
    hf = ns(architectures=["Qwen3ForSequenceClassification"],
            classifier_from_token=["no", "yes"], is_original_qwen3_reranker=True,
            method="from_2_way_softmax", num_labels=1, problem_type="single_label_classification")
    pooler = ns(task="classify", seq_pooling_type="LAST", use_activation=True,
                logit_mean=None, logit_sigma=None)
    model = ns(architecture="Qwen3ForSequenceClassification", revision=hook.MODEL_REVISION,
               tokenizer_revision=None, model="/models/" + hook.MODEL_REVISION,
               tokenizer="/models/" + hook.MODEL_REVISION, served_model_name=hook.SERVED_MODEL,
               runner_type="pooling", dtype="torch.bfloat16", head_dtype="torch.float32",
               quantization=None, max_model_len=8192, enforce_eager=True,
               trust_remote_code=False, is_multimodal_model=False, hf_config=hf,
               pooler_config=pooler)
    return ns(model_config=model, vllm_config=ns(cache_config=ns(enable_prefix_caching=False),
              parallel_config=ns(tensor_parallel_size=1), scheduler_config=ns(max_num_seqs=1)),
              supports_score_template=False, model=None, use_sep_token=False,
              tokenizer=ns(convert_tokens_to_ids=lambda s: {"no": 2152, "yes": 9693}[s]),
              _imms_set_profile=hook.PROFILE, _imms_set_tokenizer=object(),
              chat_template="frozen", _imms_set_template="frozen")


class ProcessorProfileTests(unittest.TestCase):
    def setUp(self):
        self.env = mock.patch.dict(os.environ, {hook.PROFILE_ENV: hook.PROFILE})
        self.env.start()
        self.addCleanup(self.env.stop)
        self.processor = fake_processor()

    def test_closed_model_profile_and_each_drift(self):
        hook._validate_model(self.processor)
        changes = [("architecture", "Qwen2ForSequenceClassification"), ("revision", "0" * 40),
                   ("head_dtype", "torch.bfloat16"), ("dtype", "torch.float32"),
                   ("quantization", "fp8"), ("max_model_len", 8193),
                   ("served_model_name", "other"), ("enforce_eager", False),
                   ("trust_remote_code", True), ("is_multimodal_model", True)]
        for name, value in changes:
            with self.subTest(name=name):
                altered = copy.deepcopy(self.processor)
                setattr(altered.model_config, name, value)
                with self.assertRaises(hook.SetProcessorError):
                    hook._validate_model(altered)
        for obj, attr, value in [(self.processor.vllm_config.cache_config, "enable_prefix_caching", True),
                                 (self.processor.model_config.pooler_config, "logit_mean", 1),
                                 (self.processor, "supports_score_template", True)]:
            old = getattr(obj, attr)
            setattr(obj, attr, value)
            with self.assertRaises(hook.SetProcessorError):
                hook._validate_model(self.processor)
            setattr(obj, attr, old)

    def test_disabled_profile_does_not_inspect_model_or_load_tokenizer(self):
        with mock.patch.dict(os.environ, {}, clear=True), mock.patch.object(hook, "_implementation") as load:
            p = types.SimpleNamespace()
            hook.initialize_profile(p)
            hook.validate_online(p, None)
            self.assertIsNone(hook.get_score_prompt(p, None, None, None, None, None, None, None))
            self.assertIsNone(hook.capture_expected_tokens(p, None))
            hook.validate_engine_tokens(p, None, None, None, None)
            load.assert_not_called()

    def test_unknown_or_changed_profile_rejected(self):
        with mock.patch.dict(os.environ, {hook.PROFILE_ENV: "other"}):
            with self.assertRaises(hook.SetProcessorError):
                hook.initialize_profile(types.SimpleNamespace())
            with self.assertRaises(hook.SetProcessorError):
                hook._enabled(self.processor)
        self.processor.chat_template = "changed"
        with self.assertRaises(hook.SetProcessorError):
            hook._enabled(self.processor)

    def test_engine_tokens_exact_and_post_changes_rejected(self):
        prompt = {"prompt_token_ids": [1, 2, 3]}
        expected = hook.capture_expected_tokens(self.processor, prompt)
        params = types.SimpleNamespace(pad_prompt_tokens=None, truncate_prompt_tokens=None,
                                       max_input_tokens=8192, max_output_tokens=0)
        engine = {"type": "token", "prompt_token_ids": [1, 2, 3], "arrival_time": 1}
        hook.validate_engine_tokens(self.processor, expected, params, prompt, engine)
        for changed in ([1, 2], [1, 2, 3, 0], [1, 3, 2]):
            with self.subTest(changed=changed), self.assertRaises(hook.SetProcessorError):
                hook.validate_engine_tokens(self.processor, expected, params,
                                           {"prompt_token_ids": changed}, engine)
            with self.assertRaises(hook.SetProcessorError):
                hook.validate_engine_tokens(self.processor, expected, params, prompt,
                                           dict(engine, prompt_token_ids=changed))
        for key in ("pad_prompt_tokens", "truncate_prompt_tokens"):
            altered = copy.copy(params)
            setattr(altered, key, 8192)
            with self.assertRaises(hook.SetProcessorError):
                hook.validate_engine_tokens(self.processor, expected, altered, prompt, engine)
        for key in ("token_type_ids", "multi_modal_data", "prompt_embeds"):
            with self.subTest(key=key), self.assertRaises(hook.SetProcessorError):
                hook.validate_engine_tokens(self.processor, expected, params, prompt,
                                           dict(engine, **{key: []}))
            with self.assertRaises(hook.SetProcessorError):
                hook.validate_engine_tokens(self.processor, expected, params,
                                           dict(prompt, **{key: []}), engine)

    def test_invalid_or_oversized_token_shapes_rejected(self):
        for prompt in ({"prompt_token_ids": []}, {"prompt_token_ids": [True]},
                       {"prompt_token_ids": [-1]}, {"prompt_token_ids": [1] * 8193},
                       {"prompt_token_ids": [1], "token_type_ids": [0]}):
            with self.subTest(shape=list(prompt)), self.assertRaises(hook.SetProcessorError):
                hook.capture_expected_tokens(self.processor, prompt)

    def test_regular_source_pin_rejects_symlink_size_and_hash(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "source"
            path.write_bytes(b"source")
            sha = hashlib.sha256(b"source").hexdigest()
            self.assertEqual(hook._read_regular(path, sha, 6), b"source")
            for wrong_sha, wrong_size in (("0" * 64, 6), (sha, 7)):
                with self.assertRaises(hook.SetProcessorError):
                    hook._read_regular(path, wrong_sha, wrong_size)
            link = Path(directory) / "link"
            link.symlink_to(path)
            with self.assertRaises(OSError):
                hook._read_regular(link, sha, 6)


class ExactSourcePatchTests(unittest.TestCase):
    @staticmethod
    def source():
        path = Path(__file__).resolve().parents[2] / "IMMS/docs/evidence/gb10-e2-v3-20260905/resolved-runtime.json"
        return json.loads(path.read_text())["source_files"]["entrypoints/pooling/scoring/io_processor.py"]["source_utf8"].encode()

    def test_only_cross_encoder_class_is_changed_and_all_hooks_compile(self):
        original = self.source()
        patched = patch_source(original)
        ast.parse(patched)
        marker = b"class CrossEncoderIOProcessor(ScoringIOProcessor):\n"
        tail = b"class JinaRankingIOProcessorMixin:\n"
        self.assertEqual(original.split(marker)[0], patched.split(marker)[0])
        self.assertEqual(original.split(tail)[1], patched.split(tail)[1])
        for name in ("initialize_profile", "validate_online", "get_score_prompt",
                     "capture_expected_tokens", "validate_engine_tokens"):
            self.assertIn(("imms_set." + name).encode(), patched)

    def test_other_version_or_second_patch_is_rejected(self):
        original = self.source()
        for raw in (original + b"\n", original.replace(b"import time", b"import math", 1),
                    patch_source(original)):
            with self.assertRaisesRegex(ValueError, "^set_upstream_source_identity$"):
                patch_source(raw)


def native_integration(argv):
    """Run in an isolated Python process in the pinned GB10 image; no engine."""
    from vllm.entrypoints.openai.cli_args import make_arg_parser
    from vllm.utils.argparse_utils import FlexibleArgumentParser
    from vllm.engine.arg_utils import AsyncEngineArgs
    from vllm.entrypoints.chat_utils import ChatTemplateConfig
    from vllm.entrypoints.pooling.scoring.protocol import RerankRequest, ScoreTextRequest
    from vllm.renderers import renderer_from_config
    from vllm.entrypoints.pooling.scoring import io_processor as native
    from scripts import e2_set_tokenizer_v1 as tokenizer_module

    args = make_arg_parser(FlexibleArgumentParser()).parse_args(argv[1:])
    args.model = args.model_tag
    config = AsyncEngineArgs.from_cli_args(args).create_engine_config()
    renderer = renderer_from_config(config)
    template = Path(args.chat_template).read_text()
    template_config = ChatTemplateConfig(chat_template=template, chat_template_content_format="auto",
                                         trust_request_chat_template=False)
    original = Path(native.__file__).read_bytes()
    namespace = dict(native.__dict__)
    exec(compile(patch_source(original), native.__file__, "exec"), namespace)
    cls = namespace["CrossEncoderIOProcessor"]
    os.environ[hook.PROFILE_ENV] = hook.PROFILE
    processor = cls(vllm_config=config, renderer=renderer, chat_template_config=template_config)
    reference = tokenizer_module.SetTokenizerV1((Path(config.model_config.model) / "tokenizer.json").read_bytes())
    def body(query, document):
        return dict(model=hook.SERVED_MODEL, query=query, documents=[document], top_n=1,
                    instruction=tokenizer_module.INSTRUCTION, max_tokens_per_query=0,
                    max_tokens_per_doc=0, priority=0, truncate_prompt_tokens=None,
                    truncation_side=None, use_activation=True)
    def run(request):
        ctx = types.SimpleNamespace(request=request)
        processor.pre_process_online(ctx)
        return ctx.engine_inputs[0]["prompt_token_ids"]
    def wire(items):
        # Independent serialization: explicit control characters use the ADR's
        # lowercase six-byte escape, then ordinary JSON string quoting.
        def quote(s):
            return '"' + ''.join('\\u%04x' % ord(c) if ord(c) < 32 else
                                  '\\"' if c == '"' else '\\\\' if c == '\\' else c for c in s) + '"'
        return '{"documents":[' + ','.join(quote(s) for s in items) + ']}'
    cases = [("capability_raw", tokenizer_module.CAPABILITY_QUERY, tokenizer_module.CAPABILITY_DOCUMENT),
             ("set", "Which box?", wire(["Synthetic bronze box."])),
             ("mixed", "合成记录 café：?", wire(['quote " slash / backslash \\', "line\n\ttext"])),
             ("set32", "Which record?", wire(["Synthetic record %d" % i for i in range(32)]))]
    raw_tokenizer = json.loads((Path(config.model_config.model) / "tokenizer.json").read_bytes())
    for token in raw_tokenizer["added_tokens"]:
        cases.append(("query_control_%d" % token["id"], token["content"], wire(["Synthetic record."])))
        cases.append(("document_control_%d" % token["id"], "Synthetic query?", wire([token["content"]])))
    results = []
    for name, query, document in cases:
        expected = reference.prepare(query, document, allow_capability_raw=True)
        ids = run(RerankRequest(**body(query, document)))
        if ids != expected["token_ids"]:
            raise AssertionError("native_final_engine_token_difference")
        results.append({"case": name, "token_count": len(ids),
                        "token_ids_sha256": expected["commitments"]["token_ids_sha256"]})
    failures = []
    negative_bodies = {
        "nfd": body("e\u0301", wire(["synthetic"])),
        "bom": body("\ufeffquery", wire(["synthetic"])),
        "oversized": body(" word" * 9000, wire(["synthetic"])),
        "noncanonical": body("query", '{ "documents":["synthetic"]}'),
        "extra_document": dict(body("query", wire(["synthetic"])), documents=["a", "b"]),
        "truncation": dict(body("query", wire(["synthetic"])), truncate_prompt_tokens=10),
        "instruction": dict(body("query", wire(["synthetic"])), instruction="other"),
        "activation": dict(body("query", wire(["synthetic"])), use_activation=False),
        "query_limit": dict(body("query", wire(["synthetic"])), max_tokens_per_query=1),
        "score_request": None,
    }
    for name, value in negative_bodies.items():
        try:
            run(ScoreTextRequest(text_1="query", text_2="document") if value is None else RerankRequest(**value))
        except ValueError:
            failures.append(name)
        else:
            raise AssertionError("native_negative_accepted_" + name)
    # Mutate the real renderer output in this test process. Capturing token IDs
    # as a tuple before either postprocessor must detect same-list mutation too.
    original_render = renderer.process_for_engine
    for name in ("renderer_same_list_mutation", "renderer_token_type_ids"):
        def changed_render(*args, **kwargs):
            output = original_render(*args, **kwargs)
            if name == "renderer_same_list_mutation":
                output["prompt_token_ids"][0] = 0
            else:
                output["token_type_ids"] = []
            return output
        with mock.patch.object(renderer, "process_for_engine", changed_render):
            try:
                run(RerankRequest(**body("Synthetic query?", wire(["Synthetic record."]))))
            except hook.SetProcessorError:
                failures.append(name)
            else:
                raise AssertionError("native_negative_accepted_" + name)
    base = body(tokenizer_module.CAPABILITY_QUERY, tokenizer_module.CAPABILITY_DOCUMENT)
    with mock.patch.dict(os.environ, {}, clear=True):
        old = native.CrossEncoderIOProcessor(vllm_config=config, renderer=renderer, chat_template_config=template_config)
        off = cls(vllm_config=config, renderer=renderer, chat_template_config=template_config)
        old_ctx = types.SimpleNamespace(request=RerankRequest(**base))
        off_ctx = types.SimpleNamespace(request=RerankRequest(**base))
        old.pre_process_online(old_ctx)
        off.pre_process_online(off_ctx)
        if old_ctx.engine_inputs[0]["prompt_token_ids"] != off_ctx.engine_inputs[0]["prompt_token_ids"]:
            raise AssertionError("disabled_profile_changed_native_tokens")
    return {"scope": "actual_vllm_processor_renderer_no_model_instance_or_forward_or_POST",
            "vllm_upstream_source_sha256": hashlib.sha256(original).hexdigest(),
            "vllm_patched_source_sha256": hashlib.sha256(patch_source(original)).hexdigest(),
            "accepted_cases": results, "rejected_cases": failures, "profile_off_native_exact": True,
            "model_forward_count": 0, "HTTP_POST_count": 0, "heldout": "untouched"}


if __name__ == "__main__":
    unittest.main()
