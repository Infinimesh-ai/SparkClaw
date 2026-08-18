import unittest

from pptx_slide.text import logical_text_lines, visual_text_units
from pptx_slide.text_edit import distribute_replacement


class TextHelpersTest(unittest.TestCase):
    def test_visual_units_keep_wide_characters_heavier_than_ascii(self):
        self.assertEqual(visual_text_units("ab"), 1.1)
        self.assertEqual(visual_text_units("文a"), 1.55)

    def test_logical_lines_preserve_explicit_empty_lines(self):
        self.assertEqual(logical_text_lines("first\n\nthird"), ["first", "", "third"])

    def test_replacement_distribution_preserves_all_text(self):
        pieces = distribute_replacement("abcdef", [1, 2, 3])
        self.assertEqual(pieces, ["a", "bc", "def"])
        self.assertEqual("".join(pieces), "abcdef")


if __name__ == "__main__":
    unittest.main()
