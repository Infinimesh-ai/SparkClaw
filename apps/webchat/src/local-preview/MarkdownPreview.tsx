import { useEffect, useRef, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { DocumentChannel } from './channel';
import { imageType, MAX_IMAGE_BYTES } from './imageSafety';
import { decodeText } from './TextPreview';
export function relativeImagePath(value: string) {
  if(!value || value.length>4096 || /^(?:[a-z][a-z\d+.-]*:|\/|\\)/iu.test(value) || value.includes('\0'))return null;
  try { const path=decodeURIComponent(value);if(path.split(/[\\/]/u).some(p=>p==='..')||/[?#]/u.test(path))return null;return path; }catch{return null;}
}
type ImageBudget={count:number;bytes:number;pixels:number};
function LocalImage({src,alt,channel,budget}:{src?:string;alt?:string;channel:DocumentChannel;budget:ImageBudget}) {
  const [url,setURL]=useState('');const [error,setError]=useState(false);
  useEffect(()=>{let active=true;let objectURL='';const path=relativeImagePath(src||'');if(!path || budget.count>=4){setError(true);return;}budget.count++;
    void channel.request('image',{relativePath:path}).then(value=>{const image=value as {bytes:Uint8Array;mime:string};if(!(image.bytes instanceof Uint8Array)||image.bytes.length>MAX_IMAGE_BYTES)throw new Error('Image limit');const type=imageType(image.bytes);if(budget.bytes+image.bytes.length>MAX_IMAGE_BYTES||budget.pixels+type.width*type.height>16_000_000)throw new Error('Image budget');budget.bytes+=image.bytes.length;budget.pixels+=type.width*type.height;objectURL=URL.createObjectURL(new Blob([image.bytes as Uint8Array<ArrayBuffer>],{type:type.mime}));if(active)setURL(objectURL);else URL.revokeObjectURL(objectURL);}).catch(()=>{if(active)setError(true);});
    return()=>{active=false;if(objectURL)URL.revokeObjectURL(objectURL);};},[src,channel,budget]);
  return url?<img src={url} onError={()=>{setURL('');setError(true);}} alt={alt||''}/>:<span className="imageUnavailable">{alt|| (channel.document.language==='zh'?'图片':'Image')}{error?(channel.document.language==='zh'?'（不可用）':' (unavailable)'):'…'}</span>;
}
export default function MarkdownPreview({channel}:{channel:DocumentChannel}) {
  const budget=useRef<ImageBudget>({count:0,bytes:0,pixels:0});
  const [text,setText]=useState('');const [error,setError]=useState('');
  useEffect(()=>{let active=true;void channel.all(2*1024*1024).then(bytes=>{const decoded=decodeText(bytes);if(active)setText(decoded);}).catch((cause:Error)=>{if(active)setError(cause.message);});return()=>{active=false;};},[channel]);
  if(error)return <p role="alert" className="previewMessage">{error}</p>;
  return <article className="markdownPreview" onScroll={event=>channel.position({scrollTop:event.currentTarget.scrollTop})} ref={node=>{if(node && channel.document.position?.scrollTop)node.scrollTop=channel.document.position.scrollTop;}}><ReactMarkdown skipHtml remarkPlugins={[remarkGfm]} urlTransform={value=>relativeImagePath(value)||''} components={{img:({src,alt})=><LocalImage src={src} alt={alt} channel={channel} budget={budget.current}/>,a:({children})=><span className="documentLink">{children}</span>,input:({checked})=><input type="checkbox" checked={checked} disabled readOnly/>}}>{text}</ReactMarkdown></article>;
}
