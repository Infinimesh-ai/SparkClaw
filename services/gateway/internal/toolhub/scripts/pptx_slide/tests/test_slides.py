import unittest

from pptx_slide.clone import slide_index_for_ref
from pptx_slide.slides import positive_index, slide_at


class FakePresentation:
    def __init__(self, slides):
        self.slides = slides


class SlideHelpersTest(unittest.TestCase):
    def test_positive_index_is_one_based(self):
        self.assertEqual(positive_index(2, "slide_index"), 2)
        with self.assertRaisesRegex(ValueError, "positive 1-based"):
            positive_index(0, "slide_index")

    def test_slide_at_rejects_stale_indexes(self):
        presentation = FakePresentation(["first", "second"])
        self.assertEqual(slide_at(presentation, 2), "second")
        with self.assertRaisesRegex(ValueError, "out of range"):
            slide_at(presentation, 3)

    def test_slide_ref_requires_the_canonical_shape(self):
        presentation = FakePresentation(["first", "second"])
        self.assertEqual(slide_index_for_ref(presentation, "slide:2"), 2)
        with self.assertRaisesRegex(ValueError, "invalid"):
            slide_index_for_ref(presentation, "slide:02")


if __name__ == "__main__":
    unittest.main()
