import { useEffect, useRef, useState, type RefObject } from 'react';

export function ZoomControl({ zoom, min, max, active, viewport, zh, onZoom }: {
  zoom: number; min: number; max: number; active: boolean; viewport: RefObject<HTMLElement | null>; zh: boolean; onZoom: (zoom: number) => void;
}) {
  const [visible, setVisible] = useState(false), [open, setOpen] = useState(false);
  const [hover, setHover] = useState(false), [focus, setFocus] = useState(false);
  const [draft, setDraft] = useState(''), [reveal, setReveal] = useState(0);
  const control = useRef<HTMLDivElement>(null), trigger = useRef<HTMLButtonElement>(null);
  const skipBlur = useRef(false);
  const previous = useRef(zoom);
  useEffect(() => {
    const node = viewport.current; if (!node) return;
    const show = (event: PointerEvent) => {
      if (event.clientY > node.getBoundingClientRect().bottom - 64) setReveal(value => value + 1);
    };
    node.addEventListener('pointermove', show);
    return () => node.removeEventListener('pointermove', show);
  }, [viewport]);
  useEffect(() => {
    const changed = previous.current !== zoom; previous.current = zoom;
    if (changed || active || reveal || hover || focus) setVisible(true);
    if (active || hover || focus) return;
    const timer = window.setTimeout(() => { setVisible(false); setOpen(false); }, 3000);
    return () => clearTimeout(timer);
  }, [zoom, active, reveal, hover, focus]);
  const apply = (value: string) => {
    if (!value.trim() || !Number.isFinite(Number(value))) return;
    onZoom(Math.max(min, Math.min(max, Number(value) / 100)));
  };
  return <div ref={control} className="zoomControl" data-visible={visible} onPointerEnter={() => setHover(true)} onPointerLeave={() => setHover(false)}
    onFocus={() => setFocus(true)} onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget)) { setFocus(false); setOpen(false); } }}>
    {open && <div className="zoomOptions">
      <label><span>{zh ? '缩放' : 'Zoom'}</span><input type="number" autoFocus min={min * 100} max={max * 100} step="5" value={draft}
        aria-label={zh ? '缩放百分比' : 'Zoom percentage'} onChange={event => setDraft(event.target.value)}
        onBlur={() => { if (!skipBlur.current) apply(draft); skipBlur.current = false; }} onKeyDown={event => {
          if (event.key === 'Enter') { skipBlur.current = true; apply(draft); setOpen(false); trigger.current?.focus(); }
        }} /><span>%</span></label>
      <div className="zoomPresets">{[.75, 1, 1.25, 1.5, 2].map(value => <button key={value} type="button" onMouseDown={event => event.preventDefault()}
        onClick={() => { skipBlur.current = true; onZoom(value); setDraft(String(value * 100)); setOpen(false); trigger.current?.focus(); }}>{Math.round(value * 100)}%</button>)}</div>
    </div>}
    <button type="button" ref={trigger} className="zoomPercentage" aria-expanded={open} aria-label={zh ? `缩放 ${Math.round(zoom * 100)}%，点击调节` : `Zoom ${Math.round(zoom * 100)}%, click to adjust`}
      onClick={() => { skipBlur.current = false; setDraft(String(Math.round(zoom * 100))); setOpen(value => !value); }}>{Math.round(zoom * 100)}%</button>
  </div>;
}
