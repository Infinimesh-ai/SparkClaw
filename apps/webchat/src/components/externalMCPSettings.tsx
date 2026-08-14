import { useCallback, useEffect, useState } from "react";
import {
  Check,
  ChevronDown,
  ChevronUp,
  Clipboard,
  KeyRound,
  Link2,
  Network,
  RefreshCw,
  ShieldCheck,
  Trash2,
  X
} from "lucide-react";
import { api } from "../api/client";
import type {
  ConnectorStatus,
  ISCPOnboarding,
  ISCPPairingStatus,
  IssuedISCPPairing,
  IssuedMCPAccessTicket,
  MCPAccessTicket,
  MCPBinding
} from "../api/types";
import type { Copy, Language } from "../i18n";
import { formatTime, shortId } from "../lib/format";

type ExternalMCPSettingsProps = {
  connector?: ConnectorStatus;
  text: Copy;
  language: Language;
  onUpdateConnector: (channel: string, enabled: boolean, expectedVersion: number) => Promise<ConnectorStatus>;
};

export function ExternalMCPSettings({ connector, text, language, onUpdateConnector }: ExternalMCPSettingsProps) {
  const [status, setStatus] = useState<ISCPPairingStatus | null>(null);
  const [connectorState, setConnectorState] = useState(connector);
  const [onboardings, setOnboardings] = useState<ISCPOnboarding[]>([]);
  const [accessScope, setAccessScope] = useState("");
  const [businessTool, setBusinessTool] = useState("");
  const [accessDomainID, setAccessDomainID] = useState("");
  const [iscpEnabled, setISCPEnabled] = useState(connector?.iscp_enabled === true);
  const [lanAccessEnabled, setLANAccessEnabled] = useState(connector?.lan_access_enabled === true);
  const [transportVersion, setTransportVersion] = useState(connector?.version ?? 0);
  const [endpointPath, setEndpointPath] = useState("/mcp");
  const [tickets, setTickets] = useState<MCPAccessTicket[]>([]);
  const [bindings, setBindings] = useState<MCPBinding[]>([]);
  const [displayName, setDisplayName] = useState("");
  const [issuedPairing, setIssuedPairing] = useState<IssuedISCPPairing | null>(null);
  const [issuedAccess, setIssuedAccess] = useState<IssuedMCPAccessTicket | null>(null);
  const [expanded, setExpanded] = useState(true);
  const [busy, setBusy] = useState("");
  const [copied, setCopied] = useState("");
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    const [pairing, onboardingList, catalog, ticketList, bindingList] = await Promise.all([
      api.iscpPairingStatus(),
      api.iscpOnboardings(),
      api.mcpAccessCatalog(),
      api.mcpAccessTickets(),
      api.mcpBindings()
    ]);
    setStatus(pairing);
    setOnboardings(onboardingList.onboardings ?? []);
    setAccessScope(catalog.scope);
    setBusinessTool(catalog.business_tool);
    setAccessDomainID(catalog.domain_id ?? "");
    setISCPEnabled(catalog.iscp_enabled === true);
    setLANAccessEnabled(catalog.lan_access_enabled === true);
    setTransportVersion(catalog.transport_version);
    setEndpointPath(catalog.endpoint_path || "/mcp");
    setTickets(ticketList.tickets ?? []);
    setBindings(bindingList.bindings ?? []);
  }, []);

  useEffect(() => {
    let cancelled = false;
    void refresh().catch((err: unknown) => {
      if (!cancelled) setError(err instanceof Error ? err.message : text.errors.externalMCP);
    });
    return () => { cancelled = true; };
  }, [refresh, text.errors.externalMCP]);

  useEffect(() => {
    setConnectorState(connector);
    if (connector) {
      setISCPEnabled(connector.iscp_enabled === true);
      setLANAccessEnabled(connector.lan_access_enabled === true);
      setTransportVersion(connector.version);
    }
  }, [connector]);

  const activeConnector = connectorState ?? connector;
  const enabled = activeConnector?.enabled === true;
  const canPair = enabled && iscpEnabled && status?.ready === true && displayName.trim().length > 0;
  const canIssue = enabled && (iscpEnabled || lanAccessEnabled) && Boolean(accessDomainID) && accessScope === "conversation";

  async function run(action: string, task: () => Promise<void>) {
    if (busy) return;
    setBusy(action);
    setError("");
    try {
      await task();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.externalMCP);
    } finally {
      setBusy("");
    }
  }

  async function toggleConnector() {
    if (!activeConnector) return;
    await run("connector", async () => {
      const updated = await onUpdateConnector(activeConnector.channel, !activeConnector.enabled, activeConnector.version);
      setConnectorState(updated);
      await refresh();
    });
  }

  async function toggleTransport(transport: "iscp" | "lan") {
    const nextISCP = transport === "iscp" ? !iscpEnabled : iscpEnabled;
    const nextLAN = transport === "lan" ? !lanAccessEnabled : lanAccessEnabled;
    await run(`transport:${transport}`, async () => {
      const updated = await api.updateMCPTransports(nextISCP, nextLAN, transportVersion);
      setConnectorState(updated);
      setISCPEnabled(updated.iscp_enabled === true);
      setLANAccessEnabled(updated.lan_access_enabled === true);
      setTransportVersion(updated.version);
    });
  }

  async function startPairing() {
    await run("pairing", async () => {
      const issued = await api.startISCPPairing(displayName.trim());
      setIssuedPairing(issued);
      setDisplayName("");
      await refresh();
    });
  }

  async function issueAccessTicket() {
    if (!accessDomainID) return;
    await run("access", async () => {
      const issued = await api.issueMCPAccessTicket(accessDomainID);
      setIssuedAccess(issued);
      await refresh();
    });
  }

  async function copyOnce(kind: string, value: string) {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(kind);
      window.setTimeout(() => setCopied((current) => current === kind ? "" : current), 1500);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.externalMCP);
    }
  }

  const connectorTitle = enabled ? text.settings.disableExternalMCP : text.settings.enableExternalMCP;
  const directEndpoint = typeof window === "undefined" ? endpointPath : `${window.location.origin}${endpointPath}`;
  return (
    <article className="settingsBlock externalMCPBlock">
      <div className="approvalTop">
        <span className="settingsTitle">
          <Network size={15} />
          <strong>{text.settings.externalMCP}</strong>
        </span>
        <div className="buttonRow compactButtons">
          <button className="edit" onClick={() => void run("refresh", refresh)} disabled={Boolean(busy)} title={text.common.refresh}>
            <RefreshCw size={15} />
          </button>
          <label className="connectorToggle" title={connectorTitle}>
            <input type="checkbox" checked={enabled} onChange={() => void toggleConnector()} disabled={!activeConnector || Boolean(busy)} aria-label={connectorTitle} />
            <span aria-hidden="true" />
          </label>
          <button className="edit" onClick={() => setExpanded((value) => !value)} title={expanded ? text.settings.collapseExternalMCP : text.settings.expandExternalMCP}>
            {expanded ? <ChevronUp size={15} /> : <ChevronDown size={15} />}
          </button>
        </div>
      </div>
      <div className="externalMCPStatusLine">
        <span className={`statusDot ${enabled && (lanAccessEnabled || (iscpEnabled && status?.ready)) ? "ready" : ""}`} />
        <span>{externalMCPStatusLabel(activeConnector, status, iscpEnabled, lanAccessEnabled, text)}</span>
        {bindings.filter((binding) => binding.status === "active").length > 0 && (
          <span className="pill">{bindings.filter((binding) => binding.status === "active").length} {text.settings.active}</span>
        )}
      </div>

      {expanded && (
        <>
          <dl className="statusGrid compact">
            <dt>{text.settings.domain}</dt>
            <dd>{accessDomainID || text.common.notSet}</dd>
            <dt>{text.settings.authority}</dt>
            <dd>{status?.authority_host || text.common.notSet}</dd>
            <dt>{text.settings.scope}</dt>
            <dd>{accessScope || text.common.notSet}</dd>
            <dt>{text.settings.protocol}</dt>
            <dd>MCP 2025-06-18</dd>
          </dl>

          <section className="externalMCPSection">
            <div className="externalMCPSectionTitle">
              <Network size={14} />
              <strong>{text.settings.connectionMethods}</strong>
            </div>
            <div className="externalMCPTransportList">
              <div className="externalMCPTransport">
                <strong>{text.settings.useISCP}</strong>
                <label className="connectorToggle" title={text.settings.useISCP}>
                  <input
                    type="checkbox"
                    checked={iscpEnabled}
                    onChange={() => void toggleTransport("iscp")}
                    disabled={!enabled || Boolean(busy)}
                    aria-label={text.settings.useISCP}
                  />
                  <span aria-hidden="true" />
                </label>
              </div>
              <div className="externalMCPTransport">
                <div>
                  <strong>{text.settings.allowLANAccess}</strong>
                  <code>{directEndpoint}</code>
                </div>
                <label className="connectorToggle" title={text.settings.allowLANAccess}>
                  <input
                    type="checkbox"
                    checked={lanAccessEnabled}
                    onChange={() => void toggleTransport("lan")}
                    disabled={!enabled || Boolean(busy)}
                    aria-label={text.settings.allowLANAccess}
                  />
                  <span aria-hidden="true" />
                </label>
              </div>
            </div>
          </section>

          <section className="externalMCPSection">
            <div className="externalMCPSectionTitle">
              <Link2 size={14} />
              <strong>{text.settings.iscpPairing}</strong>
            </div>
            <div className="externalMCPActionRow">
              <label className="inputGroup compact">
                <span>{text.settings.clientName}</span>
                <input value={displayName} onChange={(event) => setDisplayName(event.target.value)} maxLength={120} disabled={!enabled || !iscpEnabled || Boolean(busy)} />
              </label>
              <button className="approve externalMCPAction" onClick={() => void startPairing()} disabled={!canPair || Boolean(busy)} title={text.settings.issueISCPPairing}>
                <Link2 size={15} />
                <span>{text.settings.pair}</span>
              </button>
            </div>
            {issuedPairing && (
              <CopyOnceCredential
                title={text.settings.iscpPairingTicket}
                value={JSON.stringify(issuedPairing.ticket)}
                expiresAt={issuedPairing.ticket.expires_at}
                copied={copied === "iscp"}
                text={text}
                language={language}
                onCopy={() => void copyOnce("iscp", JSON.stringify(issuedPairing.ticket))}
                onDismiss={() => setIssuedPairing(null)}
              />
            )}
            <ReceiptList onboardings={onboardings} text={text} language={language} />
          </section>

          <section className="externalMCPSection">
            <div className="externalMCPSectionTitle">
              <ShieldCheck size={14} />
              <strong>{text.settings.capabilityAccess}</strong>
            </div>
            <div className="externalMCPGrant">
              <div>
                <strong>{businessTool || "sparkclaw.conversation.send"}</strong>
                <small>{text.settings.conversationScopeDescription}</small>
              </div>
            </div>
            <button className="approve externalMCPPrimary" onClick={() => void issueAccessTicket()} disabled={!canIssue || Boolean(busy)}>
              <KeyRound size={15} />
              <span>{text.settings.issueMCPAccess}</span>
            </button>
            {issuedAccess && (
              <CopyOnceCredential
                title={text.settings.mcpAccessTicket}
                value={issuedAccess.secret}
                expiresAt={issuedAccess.ticket.expires_at}
                copied={copied === "mcp"}
                text={text}
                language={language}
                onCopy={() => void copyOnce("mcp", issuedAccess.secret)}
                onDismiss={() => setIssuedAccess(null)}
              />
            )}
          </section>

          <AccessRecords
            tickets={tickets}
            bindings={bindings}
            text={text}
            language={language}
            busy={busy}
            onRevokeTicket={(id) => void run(`ticket:${id}`, async () => { await api.revokeMCPAccessTicket(id); await refresh(); })}
            onRevokeBinding={(id) => void run(`binding:${id}`, async () => { await api.revokeMCPBinding(id); await refresh(); })}
          />
        </>
      )}
      {error && <span className="compactError">{error}</span>}
    </article>
  );
}

function CopyOnceCredential({ title, value, expiresAt, copied, text, language, onCopy, onDismiss }: {
  title: string;
  value: string;
  expiresAt: string;
  copied: boolean;
  text: Copy;
  language: Language;
  onCopy: () => void;
  onDismiss: () => void;
}) {
  return (
    <div className="copyOnceCredential">
      <div className="approvalTop">
        <div>
          <strong>{title}</strong>
          <small>{text.settings.expires} {formatTime(expiresAt, language)}</small>
        </div>
        <div className="buttonRow compactButtons">
          <button className="approve" onClick={onCopy} title={text.common.copy}>{copied ? <Check size={15} /> : <Clipboard size={15} />}</button>
          <button className="edit" onClick={onDismiss} title={text.common.close}><X size={15} /></button>
        </div>
      </div>
      <code>{value}</code>
    </div>
  );
}

function ReceiptList({ onboardings, text, language }: { onboardings: ISCPOnboarding[]; text: Copy; language: Language }) {
  if (onboardings.length === 0) return <span className="muted">{text.settings.noPairingReceipts}</span>;
  return (
    <div className="externalMCPRecordList">
      {onboardings.slice(0, 5).map((item) => (
        <div className="externalMCPRecord" key={item.id}>
          <div><strong>{item.display_name}</strong><small>{shortId(item.ticket_id)} · {item.relay_id}</small></div>
          <span>{formatTime(item.ticket_expires_at, language)}</span>
        </div>
      ))}
    </div>
  );
}

function AccessRecords({ tickets, bindings, text, language, busy, onRevokeTicket, onRevokeBinding }: {
  tickets: MCPAccessTicket[];
  bindings: MCPBinding[];
  text: Copy;
  language: Language;
  busy: string;
  onRevokeTicket: (id: string) => void;
  onRevokeBinding: (id: string) => void;
}) {
  return (
    <section className="externalMCPSection">
      <div className="externalMCPSectionTitle"><KeyRound size={14} /><strong>{text.settings.accessRecords}</strong></div>
      <div className="externalMCPRecordList">
        {bindings.map((binding) => (
          <div className="externalMCPRecord" key={binding.id}>
            <div><strong>{text.settings.device} {shortId(binding.requester_device_id)}</strong><small>{binding.scope} · {binding.status}</small></div>
            <span>{formatTime(binding.updated_at, language)}</span>
            {binding.status !== "revoked" && (
              <button className="reject" onClick={() => onRevokeBinding(binding.id)} disabled={Boolean(busy)} title={text.settings.revokeBinding}><Trash2 size={14} /></button>
            )}
          </div>
        ))}
        {tickets.map((ticket) => (
          <div className="externalMCPRecord" key={ticket.id}>
            <div><strong>{text.settings.pendingAccess} {shortId(ticket.id)}</strong><small>{ticket.scope} · {ticket.status}</small></div>
            <span>{formatTime(ticket.expires_at, language)}</span>
            {ticket.status === "pending" && (
              <button className="reject" onClick={() => onRevokeTicket(ticket.id)} disabled={Boolean(busy)} title={text.settings.revokeBinding}><Trash2 size={14} /></button>
            )}
          </div>
        ))}
        {bindings.length === 0 && tickets.length === 0 && <span className="muted">{text.settings.noAccessRecords}</span>}
      </div>
    </section>
  );
}

function externalMCPStatusLabel(connector: ConnectorStatus | undefined, status: ISCPPairingStatus | null, iscpEnabled: boolean, lanAccessEnabled: boolean, text: Copy) {
  if (!connector?.enabled) return text.settings.connectorDisabled;
  if (!iscpEnabled && !lanAccessEnabled) return text.settings.noMCPTransport;
  if (lanAccessEnabled) return text.settings.lanMCPReady;
  if (!status?.enabled) return text.settings.pairingNotConfigured;
  if (!status.ready) return text.settings.pairingUnavailable;
  return text.settings.ready;
}
