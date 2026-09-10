import json
from pathlib import Path
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
from browser_extensions import extension_paths


class BrowserExtensionsTest(unittest.TestCase):
    def test_product_extension_is_mandatory_without_personal_configuration(self):
        receipt = {'manifest': {'tampermonkey': {'path': '/opt/sparkclaw/tampermonkey'}}}
        with patch.object(Path, 'read_text', return_value=json.dumps(receipt)), \
             patch.object(Path, 'is_symlink', return_value=False), \
             patch.object(Path, 'stat') as info, patch.object(Path, 'is_file', return_value=True):
            info.return_value.st_uid = 0
            self.assertEqual(extension_paths(Path('/new-user/browser.json'), '/bridge'), '/bridge,/opt/sparkclaw/tampermonkey')

    def test_missing_or_personalized_product_install_fails_closed(self):
        with patch.object(Path, 'read_text', side_effect=FileNotFoundError):
            with self.assertRaises(FileNotFoundError):
                extension_paths(Path('/browser.json'), '/bridge')
        receipt = {'manifest': {'tampermonkey': {'path': '/home/user/random-extension'}}}
        with patch.object(Path, 'read_text', return_value=json.dumps(receipt)):
            with self.assertRaises(ValueError):
                extension_paths(Path('/browser.json'), '/bridge')


if __name__ == '__main__':
    unittest.main()
