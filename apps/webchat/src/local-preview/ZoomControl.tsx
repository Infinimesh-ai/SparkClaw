import { useEffect, useRef, useState, type RefObject } from 'react';

const PRESETS = [.5, .75, 1, 1.25, 1.5, 2];

export function ZoomControl({ zoom, min, max, active, viewport, zh, onZoom }: {
  zoom: number; min: number; max: number; active: boolean; viewport: RefObject<HTMLElement | null>; zh: boolean; onZoom: (zoom: number) => void;
}) {
  const [visible, setVisible] = useState(false), [open, setOpen] = useState(false);
  const [interaction, setInteraction] = useState(0);
  const trigger = useRef<HTMLButtonElement>(null), previous = useRef(zoom);
  const reveal = () => setInteraction(value => value + 1);
  useEffect(() => {
    const node = viewport.current; if (!node) return;
    const show = (event: PointerEvent) => { if (event.clientY > node.getBoundingClientRect().bottom - 64) reveal(); };
    node.addEventListener('pointermove', show);
    return () => node.removeEventListener('pointermove', show);
  }, [viewport]);
  useEffect(() => {
    const changed = previous.current !== zoom; previous.current = zoom;
    if (changed || active || interaction) setVisible(true);
    if (active) return;
    const timer = window.setTimeout(() => { setVisible(false); setOpen(false); }, 2000);
    return () => clearTimeout(timer);
  }, [zoom, active, interaction]);
  const choose = (value: number) => { onZoom(value); setOpen(false); reveal(); trigger.current?.focus(); };
  const steps = [...new Set([min, .25, ...PRESETS, 3, max])].filter(value => value >= min && value <= max).sort((a, b) => a - b);
  return <div className="zoomControl" data-visible={visible} onPointerEnter={reveal} onPointerMove={reveal} onFocus={reveal} onKeyDown={reveal}
    onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false); }}>
    {open && <div className="zoomOptions" role="group" aria-label={zh ? '缩放档位' : 'Zoom levels'}>
      {PRESETS.filter(value => value >= min && value <= max).map(value => <button key={value} type="button" aria-pressed={Math.abs(zoom - value) < .005}
        onMouseDown={event => event.preventDefault()} onClick={() => choose(value)}>{Math.round(value * 100)}%</button>)}
    </div>}
    <div className="zoomPill">
      <button type="button" className="zoomStep" aria-label={zh ? '缩小' : 'Zoom out'} disabled={zoom <= min}
        onClick={() => choose([...steps].reverse().find(value => value < zoom - .001) ?? min)}>−</button>
      <button type="button" ref={trigger} className="zoomPercentage" aria-expanded={open} aria-label={zh ? `缩放 ${Math.round(zoom * 100)}%，选择档位` : `Zoom ${Math.round(zoom * 100)}%, choose level`}
        onClick={() => { setOpen(value => !value); reveal(); }}>{Math.round(zoom * 100)}%</button>
      <button type="button" className="zoomStep" aria-label={zh ? '放大' : 'Zoom in'} disabled={zoom >= max}
        onClick={() => choose(steps.find(value => value > zoom + .001) ?? max)}>+</button>
    </div>
  </div>;
}
