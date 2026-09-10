"""Differential tests against Tampermonkey 5.5.0's provisioning digest.

No() below is the unmodified function from official background.js (line 504).
Its Do() dependency is reproduced with Node WebCrypto instead of bundled forge.
Set SPARKCLAW_TEST_TAMPERMONKEY_BACKGROUND to also check the downloaded source.
"""

import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))

from browser_components import provisioning_hash


OFFICIAL_NO = '''async e=>{const t=async(e,n)=>{const r=e,s=typeof r;if("object"==s){if(null===r)return Do(`${s}:${r}`);{
const s=[];if(n){if(n.includes(r))throw"Found circular structure";n.push(r)}else n=[r];if(Array.isArray(r))for(const e of r)s.push(await t(e,n));else{const i=Object.keys(e).sort();for(const e of i)s.push(await t(r[e],n))}return n.pop(),Do(s.join(""))}}if("function"===s)throw"Functions are not supported";return Do(`${s}:${r}`)};return await t(e)}'''


def extract_official_no(source):
    match = re.search(r"\bNo=(async e=>\{.*?\}),Vo=", source, re.S)
    if not match:
        raise ValueError("Tampermonkey provisioning hash function not found")
    return match.group(1)


def node_digests(values, function=OFFICIAL_NO):
    # forge's default is binary, except when a UTF-16 code unit exceeds 255.
    program = '''
const { webcrypto } = require('node:crypto');
const fs = require('node:fs');
const Do = async value => {
  let wide = false;
  for (let i = 0; i < value.length; i++) {
    if (value.charCodeAt(i) >>> 8) { wide = true; break; }
  }
  const bytes = Buffer.from(value, wide ? 'utf8' : 'latin1');
  return Buffer.from(await webcrypto.subtle.digest('SHA-256', bytes)).toString('hex');
};
const No = ''' + function + ''';
(async () => {
  const values = JSON.parse(fs.readFileSync(0, 'utf8'));
  process.stdout.write(JSON.stringify(await Promise.all(values.map(No))));
})().catch(error => { console.error(error); process.exitCode = 1; });
'''
    result = subprocess.run(
        ["node", "-e", program], input=json.dumps(values, ensure_ascii=True),
        text=True, capture_output=True, check=True, timeout=20,
    )
    return json.loads(result.stdout)


@unittest.skipUnless(shutil.which("node"), "Node is needed for independent hash verification")
class ProvisioningHashTest(unittest.TestCase):
    def test_json_values_match_original_javascript(self):
        cases = [
            None, True, False, 0, -0.0, 1, -37, 1.5,
            1e-7, 1e-6, 1e20, 1e21, 9007199254740991,
            "", "ASCII", "éÿ", "中文", "😀", "é中文",
            [], {}, [None, True, False, "é", {"z": 1, "a": 2}],
            {"z": 1, "a": 2}, {"a": 2, "z": 1},
            # JS sorts UTF-16 units, so astral U+10000 precedes BMP U+E000.
            {"\ue000": "BMP", "\U00010000": "astral", "a": "ASCII"},
            {"version": "1", "scripts": [
                {"name": "导出", "source": "Ly8gPT1Vc2VyU2NyaXB0PT0=",
                 "enabled": True, "position": 1,
                 "requires": [{"url": "https://example.com/a.js", "content": "/*é*/", "ts": None}]},
            ]},
        ]
        for value, expected in zip(cases, node_digests(cases)):
            with self.subTest(value=value):
                actual = provisioning_hash(value)
                self.assertIn(actual, (expected, "1:" + expected))

    def test_original_source_extraction(self):
        source = "const No=" + OFFICIAL_NO + ",Vo=e=>e;"
        self.assertEqual(extract_official_no(source), OFFICIAL_NO)
        with self.assertRaises(ValueError):
            extract_official_no("const No=unknown;")

    @unittest.skipUnless(os.environ.get("SPARKCLAW_TEST_TAMPERMONKEY_BACKGROUND"),
                         "Optional downloaded official Tampermonkey source not configured")
    def test_downloaded_official_source_matches_reference(self):
        path = Path(os.environ["SPARKCLAW_TEST_TAMPERMONKEY_BACKGROUND"])
        extracted = extract_official_no(path.read_text())
        self.assertEqual(extracted, OFFICIAL_NO)
        values = [{"version": "1", "scripts": []}, [None, "é", "😀", False]]
        self.assertEqual(node_digests(values, extracted), node_digests(values))


if __name__ == "__main__":
    unittest.main()
