import { useMemo, useState } from "react";
import { ArrowLeft, Brain, Bot, CircleAlert, History, KeyRound, Palette, PlugZap, Search, ShieldCheck, SlidersHorizontal } from "lucide-react";
import type { Copy, Language } from "../i18n";
import type { PanelTab } from "./inspector";
import { workbenchCopy } from "./workbench";

type WorkspaceSettingsSidebarProps = {
  text: Copy;
  language: Language;
  tab: PanelTab;
  pendingApprovalCount: number;
  pendingCandidateCount: number;
  availableTabs?: PanelTab[];
  onTabChange: (tab: PanelTab) => void;
  onBack: () => void;
};

export function WorkspaceSettingsSidebar({
  text,
  language,
  tab,
  pendingApprovalCount,
  pendingCandidateCount,
  availableTabs,
  onTabChange,
  onBack
}: WorkspaceSettingsSidebarProps) {
  const copy = workbenchCopy[language];
  const [query, setQuery] = useState("");
  const items = useMemo(() => [
    { group: "workspace" as const, id: "settings" as const, label: copy.general, icon: SlidersHorizontal, count: 0 },
    { group: "workspace" as const, id: "appearance" as const, label: copy.appearance, icon: Palette, count: 0 },
    { group: "workspace" as const, id: "devices" as const, label: copy.devices, icon: KeyRound, count: 0 },
    { group: "agent" as const, id: "models-tools" as const, label: copy.modelsTools, icon: Bot, count: 0 },
    { group: "agent" as const, id: "permissions" as const, label: copy.permissions, icon: ShieldCheck, count: 0 },
    { group: "agent" as const, id: "connections" as const, label: copy.connections, icon: PlugZap, count: 0 },
    { group: "context" as const, id: "memory" as const, label: text.tabs.memory, icon: Brain, count: pendingCandidateCount },
    { group: "activity" as const, id: "approvals" as const, label: text.tabs.approvals, icon: CircleAlert, count: pendingApprovalCount },
    { group: "activity" as const, id: "timeline" as const, label: text.tabs.timeline, icon: History, count: 0 },
  ], [copy.appearance, copy.connections, copy.devices, copy.general, copy.modelsTools, copy.permissions, pendingApprovalCount, pendingCandidateCount, text.tabs]);
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const visibleItems = items.filter((item) =>
    (!availableTabs || availableTabs.includes(item.id)) &&
    (!normalizedQuery || item.label.toLocaleLowerCase().includes(normalizedQuery))
  );

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
        {(["workspace", "agent", "context", "activity"] as const).map((group) => {
          const groupItems = visibleItems.filter((item) => item.group === group);
          if (groupItems.length === 0) return null;
          return <div className="settingsPageNavigationGroup" key={group}>
            <span className="settingsPageGroupLabel">{copy.settingsGroups[group]}</span>
            {groupItems.map(({ id, label, icon: Icon, count }) => (
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
          </div>;
        })}
        {visibleItems.length === 0 && <p>{copy.noSettingsMatches}</p>}
      </nav>
    </aside>
  );
}
