#!/usr/bin/env python3

import importlib.util
import json
import sys
import tempfile
import unittest
import zipfile
from pathlib import Path


HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("phase0", HERE / "phase0.py")
PHASE0 = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = PHASE0
SPEC.loader.exec_module(PHASE0)


class Phase0UnitTests(unittest.TestCase):
    def test_normalize_text_is_nfkc_and_whitespace_stable(self):
        self.assertEqual(PHASE0.normalize_text("Ａ\n  B\v中"), "A B 中")
        self.assertEqual(PHASE0.comparison_text("Ａ\n  B\v中"), "A B 中")
        self.assertEqual(PHASE0.comparison_text("18% ， retention"), "18%,retention")

    def test_canonical_package_digest_ignores_zip_metadata_and_entry_order(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            first = root / "first.zip"
            second = root / "second.zip"
            with zipfile.ZipFile(first, "w") as package:
                left = zipfile.ZipInfo("b.xml", date_time=(2020, 1, 1, 0, 0, 0))
                package.writestr(left, b"<b/>")
                package.writestr("a.xml", b"<a/>")
            with zipfile.ZipFile(second, "w") as package:
                package.writestr("a.xml", b"<a/>")
                right = zipfile.ZipInfo("b.xml", date_time=(2026, 8, 14, 8, 0, 0))
                package.writestr(right, b"<b/>")
            self.assertNotEqual(PHASE0.sha256_file(first), PHASE0.sha256_file(second))
            self.assertEqual(PHASE0.canonical_package_digest(first), PHASE0.canonical_package_digest(second))

    def test_invalid_group_metadata_collapses_to_operation_atomicity(self):
        updates = [{"id": "one"}, {"id": "two", "atomic_group_id": "valid"}]
        self.assertEqual(PHASE0.normalize_atomic_groups(updates), [{"id": "operation", "updates": updates}])
        result = PHASE0.select_effective_groups(updates, {"one"})
        self.assertEqual(result["status"], "no_safe_change")
        self.assertIsNone(result["artifact"])

    def test_group_selection_never_partially_accepts_a_group(self):
        updates = [
            {"id": "title", "atomic_group_id": "same"},
            {"id": "body", "atomic_group_id": "same"},
            {"id": "caption", "atomic_group_id": "other"},
        ]
        result = PHASE0.select_effective_groups(updates, {"title", "caption"})
        self.assertEqual(result["status"], "completed_with_skips")
        self.assertEqual(result["accepted_groups"], ["other"])
        self.assertEqual(result["skipped_groups"], ["same"])

    def test_unreduced_candidate_plan_fails_when_median_exceeds_deadline(self):
        policy = {
            "candidate_plan": {
                "max_shapes": 64,
                "max_candidates_per_shape": 16,
                "preparation_deadline_seconds": 90,
            },
        }
        engines = [{
            "engine": "gotenberg",
            "timing_seconds": {"median": 0.1703, "p95": 0.8326, "worst": 0.9046},
        }]
        result = PHASE0.qualify_conversion_cost(engines, policy)
        self.assertEqual(result["status"], "FAIL")
        self.assertEqual(result["candidate_evaluations"], 1024)
        self.assertGreater(result["projected_seconds"]["median"], 90)

    def test_fixture_manifest_covers_required_synthetic_boundaries(self):
        with tempfile.TemporaryDirectory() as raw:
            manifest_path = PHASE0.generate_fixtures(Path(raw))
            manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
            identifiers = {
                case["id"]
                for deck in manifest["decks"]
                for case in deck["cases"]
            }
            self.assertTrue({
                "latin_fit",
                "cjk_fit",
                "mixed_fit",
                "latin_clipped",
                "cjk_clipped",
                "mixed_clipped",
                "duplicate_attribution",
                "fully_occluded",
                "partly_occluded",
                "transparent_overlay",
                "same_color_concealment",
                "missing_font",
                "aspect_4_3_fit",
                "aspect_4_3_clipped",
            }.issubset(identifiers))
            for deck in manifest["decks"]:
                for key in ("candidate", "without_target", "target_topmost"):
                    self.assertTrue((Path(raw) / deck[key]).is_file())


if __name__ == "__main__":
    unittest.main()
