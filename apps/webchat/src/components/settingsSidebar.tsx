import { useMemo, useState } from "react";
import { ArrowLeft, FileSearch, Gauge, MemoryStick, ScrollText, Search, Settings, ShieldAlert } from "lucide-react";
import type { Copy, Language } from "../i18n";
import type { PanelTab } from "./inspector";
import { workbenchCopy } from "./workbench";

type WorkspaceSettingsSidebarProps = {
  text: Copy;
  language: Language;
  tab: PanelTab;
  pendingApprovalCount: number;
  pendingCandidateCount: number;
  onTabChange: (tab: PanelTab) => void;
  onBack: () => void;
};

export function WorkspaceSettingsSidebar({
  text,
  language,
  tab,
  pendingApprovalCount,
  pendingCandidateCount,
  onTabChange,
  onBack
}: WorkspaceSettingsSidebarProps) {
  const copy = workbenchCopy[language];
  const [query, setQuery] = useState("");
  const items = useMemo(() => [
    { id: "settings" as const, label: copy.general, icon: Settings, count: 0 },
    { id: "approvals" as const, label: text.tabs.approvals, icon: ShieldAlert, count: pendingApprovalCount },
    { id: "memory" as const, label: text.tabs.memory, icon: MemoryStick, count: pendingCandidateCount },
    { id: "timeline" as const, label: text.tabs.timeline, icon: FileSearch, count: 0 },
    { id: "trace" as const, label: text.tabs.trace, icon: ScrollText, count: 0 },
    { id: "status" as const, label: text.tabs.status, icon: Gauge, count: 0 }
  ], [copy.general, pendingApprovalCount, pendingCandidateCount, text.tabs]);
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const visibleItems = normalizedQuery
    ? items.filter((item) => item.label.toLocaleLowerCase().includes(normalizedQuery))
    : items;

  return (
    <aside className="settingsPageSidebar">
      <button className="settingsPageBack" type="button" onClick={onBack}>
        <ArrowLeft size={17} />
        <span>{copy.backToApp}</span>
      </button>

      <label className="settingsPageSearch">
        <Search size={15} aria-hidden="true" />
        <input
          type="search"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          aria-label={copy.searchSettings}
          placeholder={copy.searchSettings}
        />
      </label>

      <nav className="settingsPageNavigation" aria-label={copy.settingsNavigation}>
        <span className="settingsPageGroupLabel">{copy.settingsGroup}</span>
        {visibleItems.map(({ id, label, icon: Icon, count }) => (
          <button
            className={tab === id ? "selected" : ""}
            type="button"
            key={id}
            aria-current={tab === id ? "page" : undefined}
            onClick={() => onTabChange(id)}
          >
            <Icon size={16} />
            <span>{label}</span>
            {count > 0 && <small>{count}</small>}
          </button>
        ))}
        {visibleItems.length === 0 && <p>{copy.noSettingsMatches}</p>}
      </nav>
    </aside>
  );
}
