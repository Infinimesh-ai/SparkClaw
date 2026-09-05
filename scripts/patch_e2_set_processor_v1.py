"""Exact-source build-time patch. Never patches a running process or imports vLLM."""
from __future__ import annotations

import argparse
import ast
import hashlib
from pathlib import Path

UPSTREAM_SHA256 = "023be1d03e2c024b3fe162cbbe6b30a565aa90dceb6077c01d5d5eb4e08953c2"
UPSTREAM_SIZE = 27702


def patch_source(raw: bytes) -> bytes:
    if len(raw) != UPSTREAM_SIZE or hashlib.sha256(raw).hexdigest() != UPSTREAM_SHA256:
        raise ValueError("set_upstream_source_identity")
    text = raw.decode("utf-8")
    before, cross = text.split("class CrossEncoderIOProcessor(ScoringIOProcessor):\n", 1)
    cross, after = cross.split("class JinaRankingIOProcessorMixin:\n", 1)
    replacements = [
        ("        self.use_sep_token = self.model_config.use_sep_token\n",
         "        self.use_sep_token = self.model_config.use_sep_token\n"
         "        from scripts import e2_set_processor_v1 as imms_set\n"
         "        imms_set.initialize_profile(self)\n"),
        ("    def pre_process_online(self, ctx: ScoringServeContext):\n        request = ctx.request\n",
         "    def pre_process_online(self, ctx: ScoringServeContext):\n"
         "        from scripts import e2_set_processor_v1 as imms_set\n"
         "        imms_set.validate_online(self, ctx)\n        request = ctx.request\n"),
        ("        model_config = self.model_config\n        tokenizer = self.tokenizer\n",
         "        from scripts import e2_set_processor_v1 as imms_set\n"
         "        prepared = imms_set.get_score_prompt(\n"
         "            self, data_1, data_2, encode_kwargs, chat_template,\n"
         "            max_tokens_per_query, max_tokens_per_doc, chat_template_kwargs,\n"
         "        )\n"
         "        if prepared is not None:\n            return prepared\n"
         "        model_config = self.model_config\n        tokenizer = self.tokenizer\n"),
        ("            if token_type_ids := engine_prompt.pop(\"token_type_ids\", None):\n",
         "            from scripts import e2_set_processor_v1 as imms_set\n"
         "            expected_tokens = imms_set.capture_expected_tokens(self, engine_prompt)\n"
         "            if token_type_ids := engine_prompt.pop(\"token_type_ids\", None):\n"),
        ("            engine_inputs.append(\n"
         "                self.renderer.process_for_engine(engine_prompt, arrival_time)\n"
         "            )\n",
         "            engine_input = self.renderer.process_for_engine(engine_prompt, arrival_time)\n"
         "            imms_set.validate_engine_tokens(\n"
         "                self, expected_tokens, tok_params, engine_prompt, engine_input\n"
         "            )\n"
         "            engine_inputs.append(engine_input)\n"),
    ]
    for old, new in replacements:
        if cross.count(old) != 1:
            raise ValueError("set_upstream_hook_identity")
        cross = cross.replace(old, new, 1)
    output = before + "class CrossEncoderIOProcessor(ScoringIOProcessor):\n" + cross
    output += "class JinaRankingIOProcessorMixin:\n" + after
    ast.parse(output)
    return output.encode("utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    raw = args.source.read_bytes()
    patched = patch_source(raw)
    with args.output.open("xb") as stream:
        stream.write(patched)
    print(hashlib.sha256(patched).hexdigest())


if __name__ == "__main__":
    main()
