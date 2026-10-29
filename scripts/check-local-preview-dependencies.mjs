import { createRequire } from 'node:module';
const require = createRequire(new URL('../package.json', import.meta.url));
for (const name of ['react-pdf', 'pdfjs-dist', 'react-markdown', 'remark-gfm', 'highlight.js', '@fortune-sheet/react', 'exceljs', 'papaparse', 'yauzl']) require.resolve(name);
const kit = require('@deepseek-ai/libreoffice-kit/package.json');
const engine = process.platform === 'darwin' ? `@deepseek-ai/libreoffice-kit-darwin-${process.arch}` : '@deepseek-ai/libreoffice-kit-wasm';
const installed = require(`${engine}/package.json`);
if (kit.version !== installed.version) throw new Error('The local Office API and engine versions must match');
console.log(`Offline preview dependencies present; Office API/engine ${kit.version}. Runtime isolation and package qualification are separate checks.`);
