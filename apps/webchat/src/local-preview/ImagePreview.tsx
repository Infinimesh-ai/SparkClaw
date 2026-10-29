import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { DocumentChannel } from './channel';
import { imageType, MAX_IMAGE_BYTES } from './imageSafety';
import { usePinchZoom, type ZoomPoint } from './usePinchZoom';

export default function ImagePreview({ channel }: { channel: DocumentChannel }) {
  const zh = channel.document.language === 'zh', saved = channel.document.position;
  const [url, setURL] = useState(''), [error, setError] = useState('');
  const [zoom, setZoom] = useState(Math.min(4, Math.max(.1, saved?.zoom || 1)));
  const [natural, setNatural] = useState({ width: 0, height: 0 });
  const [view, setView] = useState({ width: 0, height: 0, top: saved?.scrollTop || 0, left: saved?.scrollLeft || 0 });
  const viewport = useRef<HTMLDivElement>(null), picture = useRef<HTMLImageElement>(null);
  const pinch = useRef<(ZoomPoint & { horizontal: number; vertical: number }) | null>(null);
  const fit = Math.min(1, view.width / (natural.width || 1), view.height / (natural.height || 1));
  const width = natural.width * fit * zoom, height = natural.height * fit * zoom;
  const capture = () => {
    const node = viewport.current; if (node) setView(previous => ({ ...previous, top: node.scrollTop, left: node.scrollLeft }));
  };
  usePinchZoom(viewport, zoom, (next, point) => {
    const node = viewport.current, image = picture.current; if (!node || !image || !width || !height) return;
    const bounds = node.getBoundingClientRect(), rect = image.getBoundingClientRect();
    pinch.current = { ...point, horizontal: (point.x + bounds.left - rect.left) / rect.width, vertical: (point.y + bounds.top - rect.top) / rect.height };
    setZoom(next);
  }, .1, 4);
  useLayoutEffect(() => {
    const node = viewport.current; if (!node) return;
    const observer = new ResizeObserver(() => setView(previous => ({ ...previous, width: node.clientWidth, height: node.clientHeight })));
    observer.observe(node); return () => observer.disconnect();
  }, []);
  useLayoutEffect(() => {
    const node = viewport.current; if (!node) return;
    const gesture = pinch.current;
    node.scrollLeft = gesture ? (Math.max(view.width, width) - width) / 2 + gesture.horizontal * width - gesture.x : view.left;
    node.scrollTop = gesture ? (Math.max(view.height, height) - height) / 2 + gesture.vertical * height - gesture.y : view.top;
    pinch.current = null;
    if (natural.width) capture();
  }, [width, height]);
  useEffect(() => channel.position({ zoom, scrollTop: view.top, scrollLeft: view.left }), [channel, zoom, view.top, view.left]);
  useEffect(() => {
    let active = true, objectURL = '';
    void channel.all(MAX_IMAGE_BYTES).then(bytes => {
      const { mime } = imageType(bytes);
      objectURL = URL.createObjectURL(new Blob([bytes as Uint8Array<ArrayBuffer>], { type: mime }));
      if (active) setURL(objectURL); else URL.revokeObjectURL(objectURL);
    }).catch((cause: Error) => { if (active) setError(cause.message); });
    return () => { active = false; if (objectURL) URL.revokeObjectURL(objectURL); };
  }, [channel]);
  return <div className="imagePreview">{error ? <p role="alert" className="previewMessage">{error}</p> :
    <div className="imageViewport" ref={viewport} onScroll={capture} tabIndex={0} aria-label={zh ? '图片内容' : 'Image content'}>
      <div className="imageStage" style={{ width: Math.max(view.width, width), height: Math.max(view.height, height) }}>
        {url && <img ref={picture} src={url} alt={channel.document.name} draggable={false}
          onLoad={event => setNatural({ width: event.currentTarget.naturalWidth, height: event.currentTarget.naturalHeight })}
          onError={() => setError(zh ? '图片无法解码。' : 'Image could not be decoded.')}
          style={natural.width ? { width, height } : undefined} />}
      </div>
    </div>}
  </div>;
}
