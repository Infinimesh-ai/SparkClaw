import { useEffect, useState } from 'react';
import { DocumentChannel } from './channel';
import { imageType, MAX_IMAGE_BYTES } from './imageSafety';
export default function ImagePreview({ channel }: { channel: DocumentChannel }) {
  const zh = channel.document.language === 'zh'; const [url,setURL]=useState(''); const [error,setError]=useState(''); const [zoom,setZoom]=useState(Math.min(4,Math.max(.1,channel.document.position?.zoom||1)));
  useEffect(()=>channel.position({zoom}),[channel,zoom]);
  useEffect(() => { let active=true;let objectURL=''; void channel.all(MAX_IMAGE_BYTES).then(bytes => {
    const {mime}=imageType(bytes); objectURL=URL.createObjectURL(new Blob([bytes as Uint8Array<ArrayBuffer>],{type:mime})); if(active)setURL(objectURL);else URL.revokeObjectURL(objectURL);
  }).catch((cause:Error)=>{if(active)setError(cause.message);}); return()=>{active=false;if(objectURL)URL.revokeObjectURL(objectURL);};},[channel]);
  return <div className="imagePreview"><div className="previewToolbar"><button type="button" onClick={()=>setZoom(Math.max(.1,zoom-.25))} aria-label={zh?'缩小':'Zoom out'}>−</button><span>{Math.round(zoom*100)}%</span><button type="button" onClick={()=>setZoom(Math.min(4,zoom+.25))} aria-label={zh?'放大':'Zoom in'}>＋</button><button type="button" onClick={()=>setZoom(1)}>{zh?'适合窗口':'Fit'}</button></div>{error?<p role="alert" className="previewMessage">{error}</p>:<div className="imageViewport">{url&&<img src={url} onError={()=>setError(zh?'图片无法解码。':'Image could not be decoded.')} alt={channel.document.name} style={{maxWidth:`${zoom*100}%`,maxHeight:`${zoom*100}%`}}/>}</div>}</div>;
}
