import importlib.util
import json
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("model_capacity_entrypoint", ROOT / "scripts" / "model_capacity_entrypoint.py")
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


class ModelCapacityEntrypointTests(unittest.TestCase):
    def test_resolves_each_lane_from_the_selected_catalog_profile(self) -> None:
        catalog = ROOT / "configs" / "model.profiles.json"
        self.assertEqual(MODULE.resolve_context_tokens(str(catalog), "sparkclaw-product-v1", "fast"), 262144)
        self.assertEqual(MODULE.resolve_context_tokens(str(catalog), "sparkclaw-product-v1", "deep"), 262144)
        self.assertEqual(MODULE.resolve_context_tokens(str(catalog), "dgx-spark-dual-light-v1", "deep"), 65536)
        self.assertEqual(MODULE.resolve_context_tokens(str(catalog), "sparkclaw-product-v1", "embedding"), 8192)
        self.assertEqual(MODULE.resolve_context_tokens(str(catalog), "sparkclaw-product-v1", "guard"), 8192)
        self.assertEqual(MODULE.resolve_context_tokens(str(catalog), "sparkclaw-product-v1", "ocr"), 32768)

    def test_rejects_missing_zero_and_non_executable_capacity(self) -> None:
        fixtures = [
            ({"profiles": {}}, "missing"),
            ({"profiles": {"bad": {"executable": False}}}, "bad"),
            ({"profiles": {"bad": {"executable": True, "lanes": {"fast": {"physical_model": "p"}}, "physical_models": {"p": {"context_tokens": 0}}}}}, "bad"),
        ]
        for payload, profile in fixtures:
            with self.subTest(payload=payload):
                with tempfile.TemporaryDirectory() as directory:
                    path = pathlib.Path(directory) / "catalog.json"
                    path.write_text(json.dumps(payload), encoding="utf-8")
                    with self.assertRaises(ValueError):
                        MODULE.resolve_context_tokens(str(path), profile, "fast")

    def test_rejects_a_caller_supplied_physical_window(self) -> None:
        with self.assertRaisesRegex(ValueError, "must come from"):
            MODULE.vllm_command(["model", "--max-model-len", "1"], 32768)
        self.assertEqual(
            MODULE.vllm_command(["model", "--host", "0.0.0.0"], 32768)[-2:],
            ["--max-model-len", "32768"],
        )


if __name__ == "__main__":
    unittest.main()
