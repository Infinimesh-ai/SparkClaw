import { createRoot } from 'react-dom/client';
import { Component, Suspense, lazy, type ReactNode } from 'react';
import { DocumentChannel, validDocument, WORKBENCH_ORIGIN } from './channel';
import './preview.css';

const Pdf = lazy(() => import('./PdfPreview'));
const Text = lazy(() => import('./TextPreview'));
const Markdown = lazy(() => import('./MarkdownPreview'));
const Image = lazy(() => import('./ImagePreview'));
const Sheet = lazy(() => import('./SpreadsheetPreview'));
const root = createRoot(document.getElementById('root')!);
let channel: DocumentChannel | undefined;
class PreviewBoundary extends Component<{ children: ReactNode }, { error: string }> {
  state = { error: '' };
  static getDerivedStateFromError(error: Error) { return { error: error.message || 'Preview unavailable' }; }
  render() { return this.state.error ? <p role="alert" className="previewMessage">{this.state.error}</p> : this.props.children; }
}
function clear() { channel?.close(); channel = undefined; root.render(<p className="previewMessage">Preview closed</p>); }
window.addEventListener('message', (event) => {
  if (event.source !== window.parent || event.origin !== WORKBENCH_ORIGIN || event.data?.type !== 'sparkclaw-preview:init' || event.data?.protocol !== 1 || event.ports.length !== 1 || !validDocument(event.data.document)) return;
  // A frame receives exactly one document authority. Reuse requires a new frame.
  if (channel) { event.ports[0].close(); return; }
  channel = new DocumentChannel(event.ports[0], event.data.document, clear);
  const value = channel; const format = value.document.format; const zh = value.document.language === 'zh';
  root.render(<PreviewBoundary><Suspense fallback={<p className="previewMessage">{zh ? '正在加载…' : 'Loading…'}</p>}>
    {format === 'pdf' ? <Pdf channel={value} /> : format === 'text' ? <Text channel={value} /> : format === 'markdown' ? <Markdown channel={value} /> : format === 'image' ? <Image channel={value} /> : format === 'spreadsheet' ? <Sheet channel={value} /> : <p className="previewMessage">{zh ? '暂不支持此文件格式的预览。' : 'Preview is unavailable for this file format.'}</p>}
  </Suspense></PreviewBoundary>);
});
window.addEventListener('keydown', event => { if(event.key==='Escape'){event.preventDefault();channel?.escape();} },true);
window.addEventListener('pagehide', clear, { once: true });
window.parent.postMessage({ type: 'sparkclaw-preview:ready', protocol: 1 }, WORKBENCH_ORIGIN);
