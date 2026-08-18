import type * as React from "react";
import { Copy } from "lucide-react";
import type { Copy as CopyText } from "../../i18n";
import { formatRisk } from "../../lib/format";

export function SectionHeader({ icon, title }: { icon: React.ReactNode; title: string }) {
  return (
    <div className="sectionHeader">
      {icon}
      <h2>{title}</h2>
    </div>
  );
}

export function JsonBlock({ value }: { value: unknown }) {
  const raw = JSON.stringify(value, null, 2);
  async function copy() {
    await navigator.clipboard?.writeText(raw).catch(() => undefined);
  }
  return (
    <div className="jsonBlock">
      <button className="miniIconButton jsonCopy" onClick={() => void copy()} title="Copy JSON">
        <Copy size={13} />
      </button>
      <pre>{raw}</pre>
    </div>
  );
}

export function RiskPill({ risk, text }: { risk: string; text: CopyText }) {
  return <span className={`riskPill ${risk}`}>{formatRisk(risk, text)}</span>;
}
