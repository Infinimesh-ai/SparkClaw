#!/usr/bin/env python3
"""Check synthetic Office PDF page geometry/text and render a visual contact sheet."""
import argparse
import json
from pathlib import Path

from PIL import Image, ImageDraw
from pypdf import PdfReader
import pypdfium2 as pdfium


def spatial_text(page):
    text = page.get_textpage()
    lines = []
    for index in range(text.count_chars()):
        char = text.get_text_range(index, 1)
        if not char.strip():
            continue
        left, bottom, right, top = text.get_charbox(index)
        middle = (bottom + top) / 2
        line = next((line for line in lines if abs(line['middle']-middle) < 6), None)
        if line is None:
            line = {'middle': middle, 'chars': []}
            lines.append(line)
        line['chars'].append((left, char))
    text.close()
    return '\n'.join(''.join(char for _, char in sorted(line['chars'])) for line in sorted(lines, key=lambda line: -line['middle']))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', type=Path)
    args = parser.parse_args()
    root = args.directory.resolve()
    matrix = json.loads((root / 'matrix.json').read_text())
    panels, checks = [], []
    for row in matrix['rows']:
        if row['actual'] != 'pdf':
            continue
        filename = root / row['output']
        document = PdfReader(filename)
        expected_pages = 3 if '.ppt' in row['name'] else 1 if row['name'] == 'missing-font.docx' else 2
        assert len(document.pages) == expected_pages, row['name']
        extracted = [page.extract_text() for page in document.pages]
        rendered = pdfium.PdfDocument(filename)
        positioned = [spatial_text(rendered[index]) for index in range(len(rendered))]
        sizes = [[float(page.mediabox.width), float(page.mediabox.height)] for page in document.pages]
        if '.ppt' in row['name']:
            for index, text in enumerate(extracted, 1):
                assert f'SPARKX SLIDE {index}' in text, (row['name'], index, text)
                assert '中文幻灯片' in positioned[index-1], (row['name'], index, positioned[index-1])
                assert f'S{index} R3 C2' in text, (row['name'], index, text)
            assert all(w > h for w, h in sizes)
        elif row['name'] == 'missing-font.docx':
            assert 'MISSING FONT FALLBACK' in extracted[0]
            assert '中文字体回退' in positioned[0]
        else:
            assert 'SPARKX PORTRAIT PAGE' in extracted[0]
            assert 'MERGED CELL' in extracted[0]
            assert 'SPARKX LANDSCAPE PAGE' in extracted[1]
            assert '中文兼容性测试' in positioned[0], positioned[0]
            assert sizes[0][0] < sizes[0][1] and sizes[1][0] > sizes[1][1], (row['name'], sizes)
        ink_counts = []
        for index in range(len(rendered)):
            bitmap = rendered[index].render(scale=1.0)
            image = bitmap.to_pil().convert('RGB')
            gray = image.convert('L')
            histogram = gray.histogram()
            ink = sum(histogram[:200])
            assert ink > 500, (row['name'], index, ink)
            ink_counts.append(ink)
            image.thumbnail((480, 330))
            panel = Image.new('RGB', (500, 380), '#e8ebee')
            ImageDraw.Draw(panel).text((10, 8), f'{row["name"]} / page {index+1}', fill='black')
            panel.paste(image, ((500-image.width)//2, 35+(330-image.height)//2))
            panels.append(panel)
            bitmap.close()
        rendered.close()
        checks.append({'name': row['name'], 'pages': len(document.pages), 'geometry': sizes,
                       'expectedTextPresent': True, 'chineseSpatialOrderCorrect': True,
                       'chineseLogicalExtractionOrderCorrect': '.ppt' in row['name'] or row['name'] == 'missing-font.docx' or '中文兼容性测试' in extracted[0].replace(' ', ''),
                       'nonWhitePixels': ink_counts})
    sheet = Image.new('RGB', (1500, 380*((len(panels)+2)//3)), 'white')
    for index, panel in enumerate(panels):
        sheet.paste(panel, ((index % 3)*500, (index//3)*380))
    sheet.save(root / 'format-contact-sheet.png')
    (root / 'pdf-content-checks.json').write_text(json.dumps({'passed': True, 'checks': checks}, indent=2)+'\n')
    print(json.dumps({'passed': True, 'documents': len(checks), 'pages': len(panels), 'contactSheet': str(root/'format-contact-sheet.png')}))


if __name__ == '__main__':
    main()
