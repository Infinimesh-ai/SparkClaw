// Imported only by client-preview.html; comparison settings stay in memory.
import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import "./clientTypographyPreview.css";

const controls = [
  { key: "body", label: "正文", min: 12, max: 18, property: "--client-reading-size" },
  { key: "sidebar", label: "侧栏", min: 12, max: 16, property: "--client-sidebar-size" },
] as const;
const defaultSizes = { body: 14, sidebar: 13 };

function ClientTypographyPreview() {
  const [sizes, setSizes] = useState(defaultSizes);
  const [original, setOriginal] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);

  useEffect(() => {
    document.documentElement.dataset.previewFullscreen = String(fullscreen);
    return () => { delete document.documentElement.dataset.previewFullscreen; };
  }, [fullscreen]);

  useEffect(() => {
    const root = document.documentElement;
    root.dataset.previewOriginal = String(original);
    for (const control of controls) {
      const size = original ? (control.key === "sidebar" ? 12 : 13) : sizes[control.key];
      root.style.setProperty(control.property, `${size}px`);
    }
    return () => {
      delete root.dataset.previewOriginal;
      for (const control of controls) root.style.removeProperty(control.property);
    };
  }, [sizes, original]);

  return <section className="typographyPreview" aria-label="字体比较预览">
    <div className="typographyPreviewHeading">
      <strong>字体预览</strong>
      <span>{original ? "原版 · 正文 13 / 侧栏 12" : "调整后 · 正文已加深"}</span>
    </div>
    {controls.map(control => <label className="typographyPreviewControl" key={control.key}>
      <span>{control.label}</span>
      <input
        type="range"
        aria-label={`${control.label}字号`}
        min={control.min}
        max={control.max}
        step={1}
        value={original ? (control.key === "sidebar" ? 12 : 13) : sizes[control.key]}
        disabled={original}
        onChange={event => setSizes(current => ({ ...current, [control.key]: Number(event.target.value) }))}
      />
      <output>{original ? (control.key === "sidebar" ? 12 : 13) : sizes[control.key]}px</output>
      <small>{control.min}–{control.max}</small>
    </label>)}
    <div className="typographyPreviewActions">
      <button type="button" aria-pressed={fullscreen} onClick={() => setFullscreen(value => !value)}>
        {fullscreen ? "返回窗口效果" : "全屏效果"}
      </button>
      <button type="button" aria-pressed={original} onClick={() => setOriginal(value => !value)}>
        {original ? "返回调整效果" : "对比原版"}
      </button>
      <button type="button" onClick={() => { setSizes(defaultSizes); setOriginal(false); }}>
        恢复默认
      </button>
    </div>
  </section>;
}

createRoot(document.getElementById("preview-controls")!).render(<ClientTypographyPreview />);
