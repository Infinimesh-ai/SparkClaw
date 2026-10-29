import { useEffect, useRef, useState } from 'react';
import hljs from 'highlight.js/lib/core';
import { DocumentChannel, MAX_READ_BYTES } from './channel';
const languages: Record<string, () => Promise<{ default: import('highlight.js').LanguageFn }>> = {
  js: () => import('highlight.js/lib/languages/javascript'), ts: () => import('highlight.js/lib/languages/typescript'), tsx: () => import('highlight.js/lib/languages/typescript'), jsx: () => import('highlight.js/lib/languages/javascript'), py: () => import('highlight.js/lib/languages/python'), go: () => import('highlight.js/lib/languages/go'), rs: () => import('highlight.js/lib/languages/rust'), json: () => import('highlight.js/lib/languages/json'), css: () => import('highlight.js/lib/languages/css'), sh: () => import('highlight.js/lib/languages/bash'), yaml: () => import('highlight.js/lib/languages/yaml'), yml: () => import('highlight.js/lib/languages/yaml'), html: () => import('highlight.js/lib/languages/xml'), xml: () => import('highlight.js/lib/languages/xml'), sql: () => import('highlight.js/lib/languages/sql'),
};
export function decodeText(bytes: Uint8Array) {
  const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
  if (text.includes('\0')) throw new Error('Binary data cannot be shown as text');
  return text;
}
function scrollToLine(node: HTMLPreElement | null, source: string, line: number) {
  if(!node)return;let offset=0;
  for(let n=1;n<line;n++){const next=source.indexOf('\n',offset);if(next<0)break;offset=next+1;}
  const walker=document.createTreeWalker(node,NodeFilter.SHOW_TEXT);let current=walker.nextNode();
  while(current){if(offset<=current.textContent!.length){const range=document.createRange();range.setStart(current,offset);range.collapse(true);const rect=range.getBoundingClientRect();node.scrollTop+=rect.top-node.getBoundingClientRect().top-12;return;}offset-=current.textContent!.length;current=walker.nextNode();}
}
export default function TextPreview({ channel }: { channel: DocumentChannel }) {
  const zh = channel.document.language === 'zh';
  const [offset, setOffset] = useState(Math.max(0, Math.min(channel.document.size, channel.document.position?.offset || 0)));
  const [nextOffset, setNextOffset] = useState(offset); const previous = useRef<number[]>([]);
  const restored=useRef(false);
  const [text, setText] = useState(''); const [markup, setMarkup] = useState(''); const [error, setError] = useState(''); const [wrap, setWrap] = useState(true); const [line, setLine] = useState(1); const body = useRef<HTMLPreElement>(null);
  useEffect(() => {
    let active = true; setText(''); setMarkup(''); setError('');
    void (async () => {
      const bytes = await channel.read(offset, Math.min(MAX_READ_BYTES,channel.document.size-offset));
      const decoded = new TextDecoder('utf-8',{fatal:true,ignoreBOM:true}).decode(bytes,{stream:offset+bytes.length<channel.document.size});
      if(decoded.includes('\0'))throw new Error('Binary data cannot be shown as text');
      const consumed = new TextEncoder().encode(decoded).length;
      if(active){setText(decoded);setNextOffset(offset+consumed);}
      const extension = channel.document.name.split('.').pop()?.toLowerCase() || '';
      if (languages[extension] && decoded.length <= 100_000) { const language = await languages[extension](); hljs.registerLanguage(extension, language.default); const rendered = hljs.highlight(decoded, { language: extension, ignoreIllegals: true }).value; if (active) setMarkup(rendered); }
    })().catch((cause: Error) => { if (active) setError(cause.message); });
    channel.position({ offset }); return () => { active = false; };
  }, [channel, offset]);
  useEffect(()=>{if(!text||restored.current)return;restored.current=true;const frame=requestAnimationFrame(()=>{if(body.current)body.current.scrollTop=channel.document.position?.scrollTop||0;});return()=>cancelAnimationFrame(frame);},[text,channel]);
  return <div className="textPreview"><div className="previewToolbar"><label><input type="checkbox" checked={wrap} onChange={e => setWrap(e.target.checked)} />{zh ? '自动换行' : 'Wrap'}</label>
    <label>{zh ? '窗口内行号' : 'Line in window'} <input type="number" min={1} value={line} onChange={e => { const value = Math.max(1, Number(e.target.value)||1); setLine(value); scrollToLine(body.current,text,value); }} /></label>
    <button type="button" disabled={previous.current.length === 0} onClick={() => setOffset(previous.current.pop() || 0)}>{zh ? '上一段' : 'Previous'}</button><button type="button" disabled={nextOffset >= channel.document.size || nextOffset <= offset} onClick={() => {previous.current.push(offset);setOffset(nextOffset);}}>{zh ? '下一段' : 'Next'}</button>
    {channel.document.size > MAX_READ_BYTES && <span>{zh ? '字节窗口' : 'Byte window'} {offset}–{nextOffset}</span>}
  </div>{error ? <p role="alert" className="previewMessage">{error}</p> : <pre ref={body} onScroll={event=>channel.position({offset,scrollTop:event.currentTarget.scrollTop})} className={wrap ? 'textContent wrap' : 'textContent'}>{markup ? <code dangerouslySetInnerHTML={{ __html: markup }} /> : <code>{text}</code>}</pre>}</div>;
}
