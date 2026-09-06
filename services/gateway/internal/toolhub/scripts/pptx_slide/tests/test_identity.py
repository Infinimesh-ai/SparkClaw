import unittest

from lxml import etree

from pptx_slide.identity import shape_target_hash


class FakeShape:
    def __init__(self, xml):
        self._element = etree.fromstring(xml)


class ShapeTargetHashTest(unittest.TestCase):
    def test_hash_is_stable_across_equivalent_serializations(self):
        first = FakeShape('<p:sp xmlns:p="urn:p" b="2" a="1"><p:txBody>hi</p:txBody></p:sp>')
        second = FakeShape('<p:sp xmlns:p="urn:p" a="1" b="2"><p:txBody>hi</p:txBody></p:sp>')
        self.assertEqual(shape_target_hash("slide:1:shape:1", first), shape_target_hash("slide:1:shape:1", second))
        self.assertRegex(shape_target_hash("slide:1:shape:1", first), r"^[0-9a-f]{64}$")

    def test_hash_binds_shape_ref_and_content(self):
        shape = FakeShape('<p:sp xmlns:p="urn:p"><p:txBody>hi</p:txBody></p:sp>')
        edited = FakeShape('<p:sp xmlns:p="urn:p"><p:txBody>hi!</p:txBody></p:sp>')
        self.assertNotEqual(shape_target_hash("slide:1:shape:1", shape), shape_target_hash("slide:1:shape:2", shape))
        self.assertNotEqual(shape_target_hash("slide:1:shape:1", shape), shape_target_hash("slide:1:shape:1", edited))


if __name__ == "__main__":
    unittest.main()
