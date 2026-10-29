// Generated, non-user data. Each page stream spans multiple IPC read windows.
export function pdfRangeFixture(count = 3, mixed = false) {
  const objects = [null, '<< /Type /Catalog /Pages 2 0 R >>',
    `<< /Type /Pages /Kids [${Array.from({ length: count }, (_, i) => `${4 + i * 2} 0 R`).join(' ')}] /Count ${count} >>`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>'];
  for (let page = 1; page <= count; page++) {
    const stream = `%${'range-fixture '.repeat(12000)}\nBT /F1 12 Tf 20 150 Td (Range page ${page}) Tj ET\n`;
    objects.push(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 ${mixed && page % 2 === 0 ? 300 : 200}] /Resources << /Font << /F1 3 0 R >> >> /Contents ${objects.length + 1} 0 R >>`);
    objects.push(`<< /Length ${Buffer.byteLength(stream)} >>\nstream\n${stream}endstream`);
  }
  let pdf = '%PDF-1.4\n';
  const offsets = [0];
  for (let index = 1; index < objects.length; index++) {
    offsets.push(Buffer.byteLength(pdf));
    pdf += `${index} 0 obj\n${objects[index]}\nendobj\n`;
  }
  const start = Buffer.byteLength(pdf);
  pdf += `xref\n0 ${objects.length}\n0000000000 65535 f \n`;
  pdf += offsets.slice(1).map(offset => `${String(offset).padStart(10, '0')} 00000 n \n`).join('');
  pdf += `trailer\n<< /Size ${objects.length} /Root 1 0 R >>\nstartxref\n${start}\n%%EOF`;
  return Buffer.from(pdf);
}
