import unittest
from types import SimpleNamespace

from pptx_slide.layout import geometry_change, horizontal_overlap, shape_bounds, vertical_overlap


class FakeShape:
    left = 10
    top = 20
    width = 30
    height = 40
    has_text_frame = False


class LayoutHelpersTest(unittest.TestCase):
    def test_shape_bounds_keep_integer_geometry(self):
        self.assertEqual(shape_bounds(FakeShape()), {
            "x": 10,
            "y": 20,
            "width": 30,
            "height": 40,
            "word_wrap": None,
            "font_size_pt": None,
        })

    def test_overlap_requires_positive_intersection(self):
        left = SimpleNamespace(left=0, top=0, width=10, height=10)
        touching = SimpleNamespace(left=10, top=10, width=10, height=10)
        crossing = SimpleNamespace(left=5, top=5, width=10, height=10)
        self.assertEqual(horizontal_overlap(left, touching), 0)
        self.assertEqual(vertical_overlap(left, touching), 0)
        self.assertGreater(horizontal_overlap(left, crossing), 0)
        self.assertGreater(vertical_overlap(left, crossing), 0)

    def test_geometry_change_reports_only_changed_fields(self):
        before = {"x": 1, "y": 2, "width": 3, "height": 4}
        after = {"x": 1, "y": 3, "width": 3, "height": 5}
        self.assertEqual(geometry_change(7, before, after), {
            "shape_index": 7,
            "before": before,
            "after": after,
        })


if __name__ == "__main__":
    unittest.main()
