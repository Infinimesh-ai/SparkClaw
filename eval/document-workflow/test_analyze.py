import hashlib
import unittest

from analyze import validate_review


class ReviewBindingTest(unittest.TestCase):
    def test_review_does_not_transfer_to_rerun_or_changed_answer(self):
        row = {"run_id": "run1", "answer_sha256": hashlib.sha256(b"answer").hexdigest(),
               "dimensions": {"task_fulfillment": "pass", "source_locations": "pass", "visual_relations": "not_applicable", "limitations": "pass"},
               "source_evidence": ["source hash and native paragraphs"], "notes": "checked original source"}
        self.assertTrue(validate_review(row, "run1", "answer")["accepted"])
        self.assertFalse(validate_review(row, "run2", "answer")["accepted"])
        self.assertFalse(validate_review(row, "run1", "changed")["accepted"])
        self.assertFalse(validate_review(None, "run1", "answer")["accepted"])
        row["dimensions"]["task_fulfillment"] = "fail"
        self.assertFalse(validate_review(row, "run1", "answer")["accepted"])
        row["dimensions"]["task_fulfillment"] = "not_applicable"
        self.assertFalse(validate_review(row, "run1", "answer")["accepted"])


if __name__ == "__main__":
    unittest.main()
