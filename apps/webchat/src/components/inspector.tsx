// Inspector column: tab strip plus the timeline/approvals/memory/trace/
// status/settings panels, together with the gateway calls only these panels
// trigger. Extracted from App.tsx so the root component stays below the
// size baseline; shared state stays in the parent and is refreshed through
// the injected callbacks, so behavior is unchanged.
import { useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import { FileSearch, Gauge, MemoryStick, ScrollText, Settings, ShieldAlert } from "lucide-react";
import { api } from "../api/client";
import type { Copy, Language } from "../i18n";
import { isBindingSetupPending } from "../lib/connectors";
import { notificationBindingErrorMessage } from "../lib/bindingError";
import {
  ApprovalPanel,
  MemoryPanel,
  SettingsPanel,
  StatusStack,
  ToolTimelinePanel,
  TracePanel
} from "./panels";
import type {
  Approval,
  ArtifactObject,
  AuditEvent,
  Client,
  ConnectorStatus,
  EpisodeSummary,
  EvalRun,
  Memory,
  MemoryCandidate,
  ModelCall,
  NotificationBinding,
  OwnerProfile,
  PublicConfig,
  ReadyStatus,
  RunTrace,
  ToolCall,
  TraceMetadata
} from "../api/types";

export type PanelTab = "timeline" | "approvals" | "memory" | "trace" | "status" | "settings";

type InspectorColumnProps = {
  tab: PanelTab;
  onTabChange: (tab: PanelTab) => void;
  text: Copy;
  language: Language;
  pendingApprovalCount: number;
  pendingCandidateCount: number;
  toolCalls: ToolCall[];
  approvals: Approval[];
  candidates: MemoryCandidate[];
  memories: Memory[];
  traceRun: RunTrace | null;
  traceList: TraceMetadata[];
  traceLoading: boolean;
  ready: ReadyStatus | null;
  modelCalls: ModelCall[];
  auditEvents: AuditEvent[];
  artifacts: ArtifactObject[];
  episodes: EpisodeSummary[];
  evalRuns: EvalRun[];
  runtimeConfig: PublicConfig | null;
  ownerProfile: OwnerProfile | null;
  clients: Client[];
  connectors: ConnectorStatus[];
  notificationBindings: NotificationBinding[];
  onOpenTrace: (runId: string) => void;
  setError: (message: string) => void;
  refreshGlobal: () => Promise<void>;
  refreshActiveSession: () => Promise<void>;
  setEvalRuns: (runs: EvalRun[]) => void;
  setNotificationBindings: Dispatch<SetStateAction<NotificationBinding[]>>;
  setConnectors: Dispatch<SetStateAction<ConnectorStatus[]>>;
  setRuntimeConfig: (config: PublicConfig) => void;
  setOwnerProfile: (owner: OwnerProfile) => void;
};

export function InspectorColumn({
  tab,
  onTabChange,
  text,
  language,
  pendingApprovalCount,
  pendingCandidateCount,
  toolCalls,
  approvals,
  candidates,
  memories,
  traceRun,
  traceList,
  traceLoading,
  ready,
  modelCalls,
  auditEvents,
  artifacts,
  episodes,
  evalRuns,
  runtimeConfig,
  ownerProfile,
  clients,
  connectors,
  notificationBindings,
  onOpenTrace,
  setError,
  refreshGlobal,
  refreshActiveSession,
  setEvalRuns,
  setNotificationBindings,
  setConnectors,
  setRuntimeConfig,
  setOwnerProfile
}: InspectorColumnProps) {
  const [evalRun, setEvalRun] = useState<EvalRun | null>(null);

  async function resolveApproval(id: string, accepted: boolean) {
    try {
      setError("");
      if (accepted) await api.approve(id);
      else await api.reject(id);
      await Promise.all([refreshGlobal(), refreshActiveSession()]);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.approval);
    }
  }

  async function modifyApproval(id: string, args: Record<string, unknown>) {
    try {
      setError("");
      await api.modifyApproval(id, args);
      await Promise.all([refreshGlobal(), refreshActiveSession()]);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.approvalEdit);
    }
  }

  async function modifyApprovalPlan(id: string, plan: string) {
    try {
      setError("");
      await api.modifyApprovalPlan(id, plan);
      await Promise.all([refreshGlobal(), refreshActiveSession()]);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.approvalEdit);
    }
  }

  async function resolveMemory(id: string, accepted: boolean) {
    try {
      setError("");
      if (accepted) await api.acceptMemory(id);
      else await api.rejectMemory(id);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.memory);
    }
  }

  async function updateMemory(id: string, kind: string, content: string) {
    try {
      setError("");
      await api.updateMemory(id, kind, content);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.memoryEdit);
      throw err;
    }
  }

  async function deleteMemory(id: string) {
    try {
      setError("");
      await api.deleteMemory(id);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.memoryDelete);
      throw err;
    }
  }

  async function archiveMemoryExport() {
    try {
      setError("");
      await api.archiveMemoryExport();
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.memoryExport);
      throw err;
    }
  }

  async function revokeClient(id: string) {
    try {
      setError("");
      await api.revokeClient(id);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.clientRevoke);
      throw err;
    }
  }

  async function startNotificationBinding(channel: string, botToken = "") {
    try {
      setError("");
      const binding = await api.startNotificationBinding(channel, botToken);
      setNotificationBindings((current) => [binding, ...current.filter((item) => item.id !== binding.id)]);
      if (channel === "telegram") {
        setRuntimeConfig(await api.config());
      } else {
        await refreshGlobal();
      }
      onTabChange("settings");
    } catch (err) {
      const message = notificationBindingErrorMessage(err, text);
      setError(message);
      throw new Error(message);
    }
  }

  async function updateConnector(channel: string, enabled: boolean, expectedVersion: number) {
    try {
      setError("");
      const updated = await api.updateConnector(channel, enabled, expectedVersion);
      setConnectors((current) => current.map((item) => item.channel === updated.channel ? updated : item));
      await refreshGlobal();
      return updated;
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.connectorUpdate);
      throw err;
    }
  }

  async function refreshNotificationBinding(id: string, signal?: AbortSignal) {
    const binding = await api.notificationBinding(id, signal);
    setNotificationBindings((current) => [binding, ...current.filter((item) => item.id !== binding.id)]);
    if (!isBindingSetupPending(binding)) {
      await refreshGlobal();
    }
    return binding;
  }

  async function openNotificationBindingBrowser(id: string) {
    await api.openNotificationBindingBrowser(id);
  }

  async function revokeNotificationBinding(id: string) {
    try {
      setError("");
      await api.revokeNotificationBinding(id);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.binding);
      throw err;
    }
  }

  async function updateToolPolicy(deny: string[], approvalRequired: string[]) {
    try {
      setError("");
      await api.updateToolPolicy(deny, approvalRequired);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.policyUpdate);
      throw err;
    }
  }

  async function updateOwner(displayName: string, email: string, preferences: Record<string, string>) {
    try {
      setError("");
      const updated = await api.updateOwner(displayName, email, preferences);
      setOwnerProfile(updated);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.ownerUpdate);
      throw err;
    }
  }

  return (
    <aside className="inspectorColumn">
      <div className="inspectorTitle">INSPECTOR</div>
      <div className="tabs">
        <button className={tab === "timeline" ? "selected" : ""} onClick={() => onTabChange("timeline")} title={text.tabs.timeline}>
          <FileSearch size={16} />
          <span>{text.tabs.timeline}</span>
        </button>
        <button className={tab === "approvals" ? "selected" : ""} onClick={() => onTabChange("approvals")} title={text.tabs.approvals}>
          <ShieldAlert size={16} />
          <span>{pendingApprovalCount}</span>
        </button>
        <button className={tab === "memory" ? "selected" : ""} onClick={() => onTabChange("memory")} title={text.tabs.memory}>
          <MemoryStick size={16} />
          <span>{pendingCandidateCount}</span>
        </button>
        <button className={tab === "trace" ? "selected" : ""} onClick={() => onTabChange("trace")} title={text.tabs.trace}>
          <ScrollText size={16} />
          <span>{text.tabs.trace}</span>
        </button>
        <button className={tab === "status" ? "selected" : ""} onClick={() => onTabChange("status")} title={text.tabs.status}>
          <Gauge size={16} />
          <span>{text.tabs.status}</span>
        </button>
        <button className={tab === "settings" ? "selected" : ""} onClick={() => onTabChange("settings")} title={text.tabs.settings}>
          <Settings size={16} />
          <span>{text.tabs.settings}</span>
        </button>
      </div>

      {tab === "timeline" && <ToolTimelinePanel calls={toolCalls} text={text} onTrace={onOpenTrace} />}
      {tab === "approvals" && (
        <ApprovalPanel
          approvals={approvals}
          text={text}
          onResolve={(id, accepted) => void resolveApproval(id, accepted)}
          onModify={(id, args) => void modifyApproval(id, args)}
          onModifyPlan={(id, plan) => void modifyApprovalPlan(id, plan)}
        />
      )}
      {tab === "memory" && (
        <MemoryPanel
          candidates={candidates}
          memories={memories}
          text={text}
          onResolve={(id, accepted) => void resolveMemory(id, accepted)}
          onUpdate={(id, kind, content) => updateMemory(id, kind, content)}
          onDelete={(id) => deleteMemory(id)}
          onExport={() => archiveMemoryExport()}
        />
      )}
      {tab === "trace" && (
        <TracePanel trace={traceRun} traces={traceList} loading={traceLoading} text={text} language={language} onOpen={onOpenTrace} />
      )}
      {tab === "status" && (
        <StatusStack
          ready={ready}
          modelCalls={modelCalls}
          auditEvents={auditEvents}
          artifacts={artifacts}
          episodes={episodes}
          evalRun={evalRun}
          evalRuns={evalRuns}
          text={text}
          language={language}
          onRunEval={async () => {
            setError("");
            const result = await api.runEval("smoke");
            setEvalRun(result);
            setEvalRuns([result, ...evalRuns.filter((run) => run.id !== result.id)]);
          }}
          onSelectEval={async (id) => {
            setError("");
            setEvalRun(await api.evalRun(id));
          }}
          onError={(message) => setError(message)}
        />
      )}
      {tab === "settings" && (
        <SettingsPanel
          runtimeConfig={runtimeConfig}
          ownerProfile={ownerProfile}
          clients={clients}
          connectors={connectors}
          notificationBindings={notificationBindings}
          text={text}
          language={language}
          onUpdateOwner={(displayName, email, preferences) => updateOwner(displayName, email, preferences)}
          onRevokeClient={(id) => revokeClient(id)}
          onStartNotificationBinding={(channel, botToken) => startNotificationBinding(channel, botToken)}
          onRefreshNotificationBinding={(id, signal) => refreshNotificationBinding(id, signal)}
          onOpenNotificationBindingBrowser={(id) => openNotificationBindingBrowser(id)}
          onRevokeNotificationBinding={(id) => revokeNotificationBinding(id)}
          onUpdateConnector={(channel, enabled, version) => updateConnector(channel, enabled, version)}
          onUpdatePolicy={(deny, approvalRequired) => updateToolPolicy(deny, approvalRequired)}
        />
      )}
    </aside>
  );
}
