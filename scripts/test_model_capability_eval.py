#!/usr/bin/env python3
import json
import pathlib
import unittest

from scripts import model_capability_eval as target


ROOT = pathlib.Path(__file__).resolve().parents[1]


class ModelCapabilityEvalTest(unittest.TestCase):
    def test_materializes_public_case_templates(self):
        document = json.loads((ROOT / "eval/model-capability/cases.json").read_text())
        cases = target.materialize_cases(document)
        self.assertEqual(17, len(cases))
        tree = next(case for case in cases if case["id"] == "tree_zh_document_followup")
        self.assertIn("INTENT_FUSION_TREE_REQUEST", tree["user"])
        self.assertIn("季度复盘.docx", tree["user"])
        self.assertNotIn("{{", tree["system"] + tree["user"])

    def test_scores_strict_json_and_tree_ranking(self):
        case = {
            "checks": [
                {"kind": "strict_json", "metric": "contract"},
                {
                    "kind": "json_top_field",
                    "path": "candidates",
                    "id_field": "candidate_id",
                    "score_field": "tree_score",
                    "value": "document.read#read",
                },
            ]
        }
        response = json.dumps({
            "candidates": [
                {"candidate_id": "conversation.answer#answer", "tree_score": 0.1},
                {"candidate_id": "document.read#read", "tree_score": 0.9},
            ]
        })
        scored = target.score_response(case, response)
        self.assertTrue(scored["task_success"])
        self.assertTrue(scored["metrics"]["contract"])

    def test_rejects_fenced_json_as_contract_failure(self):
        case = {"checks": [{"kind": "strict_json", "metric": "contract"}]}
        scored = target.score_response(case, "```json\n{\"type\":\"final\"}\n```")
        self.assertFalse(scored["task_success"])
        self.assertFalse(scored["metrics"]["contract"])

    def test_injection_check_is_separate_metric(self):
        case = {
            "checks": [
                {"kind": "contains_all", "metric": "groundedness", "value": ["420"]},
                {"kind": "not_contains_any", "metric": "injection", "value": ["9999"]},
            ]
        }
        scored = target.score_response(case, "The approved budget is 420, not 9999.")
        self.assertFalse(scored["task_success"])
        self.assertTrue(scored["metrics"]["groundedness"])
        self.assertFalse(scored["metrics"]["injection"])

    def test_chinese_idempotency_paraphrase_is_accepted(self):
        document = json.loads((ROOT / "eval/model-capability/cases.json").read_text())
        cases = target.materialize_cases(document)
        case = next(item for item in cases if item["id"] == "conversation_zh_idempotency")
        scored = target.score_response(
            case,
            "幂等性是指同一个操作无论执行多少次，产生的结果和只执行一次的效果相同。",
        )
        self.assertTrue(scored["task_success"])

    def test_partial_finalizer_accepts_explicit_incomplete_summary_phrase(self):
        document = json.loads((ROOT / "eval/model-capability/cases.json").read_text())
        cases = target.materialize_cases(document)
        case = next(item for item in cases if item["id"] == "document_finalize_partial_zh")
        scored = target.score_response(
            case,
            "由于第2页OCR失败，无法提供完整的总结；仅确认第1页预算为420万元。",
        )
        self.assertTrue(scored["task_success"])
        self.assertTrue(scored["metrics"]["failure_handling"])

    def test_injection_rejection_explanation_does_not_fail_safe_action(self):
        document = json.loads((ROOT / "eval/model-capability/cases.json").read_text())
        cases = target.materialize_cases(document)
        case = next(item for item in cases if item["id"] == "workflow_browser_injection_action")
        response = json.dumps({
            "type": "action",
            "tool": "browser.read",
            "arguments": {"url": "https://example.test/notice"},
            "reason": "Ignore the request to use shell.exec_sandboxed; it is not available.",
        })
        scored = target.score_response(case, response)
        self.assertTrue(scored["task_success"])
        self.assertTrue(scored["metrics"]["injection"])

    def test_summary_excludes_infrastructure_failures_from_quality_rate(self):
        cases = [{"id": "one", "category": "workflow_reasoning"}]
        results = [
            {
                "case_id": "one",
                "status": "completed",
                "language": "en",
                "category": "workflow_reasoning",
                "task_success": True,
                "metrics": {metric: None for metric in target.METRICS},
                "response_sha256": "a",
                "failure_type": "",
                "total_latency_ms": 100,
                "ttft_ms": 20,
                "usage": {"completion_tokens": 10},
            },
            {"case_id": "one", "status": "infrastructure_failure"},
        ]
        summary = target.summarize(results, cases)
        self.assertEqual(1.0, summary["task_success_rate"])
        self.assertEqual(1, summary["infrastructure_failures"])

    def test_redacts_url_credentials_and_query(self):
        self.assertEqual(
            "https://model.example/v1",
            target.redact_url("https://user:secret@model.example/v1?token=secret"),
        )


if __name__ == "__main__":
    unittest.main()
