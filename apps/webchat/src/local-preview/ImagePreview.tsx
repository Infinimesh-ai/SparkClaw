import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { DocumentChannel } from './channel';
import { imageType, MAX_IMAGE_BYTES } from './imageSafety';
import { usePinchZoom, type ZoomPoint } from './usePinchZoom';
import { ZoomControl } from './ZoomControl';
import { scrollPadding } from './zoomGeometry';

export default function ImagePreview({ channel }: { channel: DocumentChannel }) {
  const zh = channel.document.language === 'zh', saved = channel.document.position;
  const [url, setURL] = useState(''), [error, setError] = useState('');
  const [zoom, setZoom] = useState(Math.min(4, Math.max(.1, saved?.zoom || 1)));
  const [natural, setNatural] = useState({ width: 0, height: 0 });
  const [view, setView] = useState({ width: 0, height: 0, top: saved?.scrollTop || 0, left: saved?.scrollLeft || 0 });
  const [padding, setPadding] = useState({ left: 0, right: 0, top: 0, bottom: 0 });
  const viewport = useRef<HTMLDivElement>(null), picture = useRef<HTMLImageElement>(null);
  const pinch = useRef<(ZoomPoint & { horizontal: number; vertical: number }) | null>(null);
  const fit = Math.min(1, view.width / (natural.width || 1), view.height / (natural.height || 1));
  const width = natural.width * fit * zoom, height = natural.height * fit * zoom;
  const stageWidth = Math.max(view.width, width), stageHeight = Math.max(view.height, height);
  const capture = () => {
    const node = viewport.current; if (node) setView(previous => {
      const top = node.scrollTop - padding.top, left = node.scrollLeft - padding.left;
      return previous.top === top && previous.left === left ? previous : { ...previous, top, left };
    });
  };
  const changeZoom = (next: number, point: ZoomPoint, origin?: ZoomPoint) => {
    const node = viewport.current, image = picture.current; if (!node || !image || !width || !height) return;
    const bounds = node.getBoundingClientRect(), rect = image.getBoundingClientRect();
    if (origin || !pinch.current) {
      const from = origin || point;
      pinch.current = { ...point, horizontal: (from.x + bounds.left - rect.left) / rect.width, vertical: (from.y + bounds.top - rect.top) / rect.height };
    } else Object.assign(pinch.current, point);
    setZoom(next);
  };
  const busy = usePinchZoom(viewport, zoom, changeZoom, .1, 4, () => { pinch.current = null; });
  useLayoutEffect(() => {
    const node = viewport.current; if (!node) return;
    const observer = new ResizeObserver(() => { pinch.current = null; setView(previous => ({ ...previous, width: node.clientWidth, height: node.clientHeight })); });
    observer.observe(node); return () => observer.disconnect();
  }, []);
  useLayoutEffect(() => {
    const node = viewport.current; if (!node) return;
    const gesture = pinch.current;
    const left = gesture ? (stageWidth - width) / 2 + gesture.horizontal * width - gesture.x : view.left;
    const top = gesture ? (stageHeight - height) / 2 + gesture.vertical * height - gesture.y : view.top;
    if (gesture) {
      const next = scrollPadding(left, top, stageWidth, stageHeight, view.width, view.height);
      if (Object.keys(next).some(key => next[key as keyof typeof next] !== padding[key as keyof typeof next])) { setPadding(next); return; }
    }
    node.scrollLeft = left + padding.left;
    node.scrollTop = top + padding.top;
    if (natural.width) capture();
  }, [width, height, padding]);
  useEffect(() => channel.position({ zoom, scrollTop: Math.max(0, view.top), scrollLeft: Math.max(0, view.left) }), [channel, zoom, view.top, view.left]);
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
      <div className="imageStage" style={{ width: stageWidth + padding.left + padding.right, height: stageHeight + padding.top + padding.bottom }}>
        {url && <img ref={picture} src={url} alt={channel.document.name} draggable={false}
          onLoad={event => setNatural({ width: event.currentTarget.naturalWidth, height: event.currentTarget.naturalHeight })}
          onError={() => setError(zh ? '图片无法解码。' : 'Image could not be decoded.')}
          style={natural.width ? { width, height, left: padding.left + (stageWidth - width) / 2, top: padding.top + (stageHeight - height) / 2 } : undefined} />}
      </div>
    </div>}
    {!error && <ZoomControl zoom={zoom} min={.1} max={4} active={busy} viewport={viewport} zh={zh} onZoom={next => {
      const point = { x: view.width / 2, y: view.height / 2 }; changeZoom(next, point, point);
    }} />}
  </div>;
}
