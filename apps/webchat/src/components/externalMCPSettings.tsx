import { useCallback, useEffect, useMemo, useState } from "react";
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
  MCPBinding,
  MCPGrantOption
} from "../api/types";
import type { Copy, Language } from "../i18n";
import { buildMCPRequestedGrants, isReadOnlyEffect, type MCPGrantSelection } from "../lib/externalMCP";
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
  const [catalogRevision, setCatalogRevision] = useState("");
  const [accessDomainID, setAccessDomainID] = useState("");
  const [lanDirectTest, setLanDirectTest] = useState(false);
  const [grantOptions, setGrantOptions] = useState<MCPGrantOption[]>([]);
  const [tickets, setTickets] = useState<MCPAccessTicket[]>([]);
  const [bindings, setBindings] = useState<MCPBinding[]>([]);
  const [selection, setSelection] = useState<MCPGrantSelection>({});
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
    setCatalogRevision(catalog.catalog_revision);
    setAccessDomainID(catalog.domain_id ?? "");
    setLanDirectTest(catalog.lan_direct_test_enabled === true);
    setGrantOptions(catalog.grants ?? []);
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
  }, [connector]);

  const requestedGrants = useMemo(() => buildMCPRequestedGrants(grantOptions, selection), [grantOptions, selection]);
  const activeConnector = connectorState ?? connector;
  const enabled = activeConnector?.enabled === true;
  const canPair = enabled && status?.ready === true && displayName.trim().length > 0;
  const canIssue = enabled && Boolean(accessDomainID) && requestedGrants.length > 0;

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
      const issued = await api.issueMCPAccessTicket(accessDomainID, requestedGrants);
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

  function toggleOperation(capabilityID: string, operation: string, checked: boolean) {
    setSelection((current) => {
      const selected = current[capabilityID] ?? { operations: [], allowApproval: false };
      const operations = checked
        ? [...new Set([...selected.operations, operation])]
        : selected.operations.filter((item) => item !== operation);
      if (operations.length === 0) {
        const next = { ...current };
        delete next[capabilityID];
        return next;
      }
      return { ...current, [capabilityID]: { ...selected, operations } };
    });
  }

  const connectorTitle = enabled ? text.settings.disableExternalMCP : text.settings.enableExternalMCP;
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
        <span className={`statusDot ${enabled && (lanDirectTest || status?.ready) ? "ready" : ""}`} />
        <span>{externalMCPStatusLabel(activeConnector, status, lanDirectTest, text)}</span>
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
            <dt>{text.settings.catalog}</dt>
            <dd>{catalogRevision || text.common.notSet}</dd>
            <dt>{text.settings.protocol}</dt>
            <dd>{lanDirectTest ? "MCP 2025-06-18 (LAN test)" : "MCP 2025-06-18"}</dd>
          </dl>

          <section className="externalMCPSection">
            <div className="externalMCPSectionTitle">
              <Link2 size={14} />
              <strong>{text.settings.iscpPairing}</strong>
            </div>
            <div className="externalMCPActionRow">
              <label className="inputGroup compact">
                <span>{text.settings.clientName}</span>
                <input value={displayName} onChange={(event) => setDisplayName(event.target.value)} maxLength={120} disabled={!enabled || Boolean(busy)} />
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
            <div className="externalMCPGrantList">
              {grantOptions.map((option) => {
                const selected = selection[option.capability_id];
                const selectedOperations = selected?.operations ?? [];
                const approvalApplicable = option.operations.some((item) => selectedOperations.includes(item.operation) && !isReadOnlyEffect(item.effect));
                return (
                  <div className="externalMCPGrant" key={option.capability_id}>
                    <div>
                      <strong>{option.capability_id}</strong>
                      <small>{option.description}</small>
                    </div>
                    <div className="externalMCPOperations">
                      {option.operations.map((item) => (
                        <label key={item.operation}>
                          <input
                            type="checkbox"
                            checked={selectedOperations.includes(item.operation)}
                            onChange={(event) => toggleOperation(option.capability_id, item.operation, event.target.checked)}
                            disabled={!enabled || Boolean(busy)}
                          />
                          <span>{item.operation}</span>
                        </label>
                      ))}
                      {approvalApplicable && (
                        <label>
                          <input
                            type="checkbox"
                            checked={selected?.allowApproval === true}
                            onChange={(event) => setSelection((current) => ({
                              ...current,
                              [option.capability_id]: { ...current[option.capability_id], allowApproval: event.target.checked }
                            }))}
                            disabled={!enabled || Boolean(busy)}
                          />
                          <span>{text.settings.allowApproval}</span>
                        </label>
                      )}
                    </div>
                  </div>
                );
              })}
              {grantOptions.length === 0 && <span className="muted">{text.settings.noRemoteCapabilities}</span>}
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
            <div><strong>{text.settings.device} {shortId(binding.requester_device_id)}</strong><small>{binding.grants.length} {text.settings.capabilities} · {binding.status}</small></div>
            <span>{formatTime(binding.updated_at, language)}</span>
            {binding.status !== "revoked" && (
              <button className="reject" onClick={() => onRevokeBinding(binding.id)} disabled={Boolean(busy)} title={text.settings.revokeBinding}><Trash2 size={14} /></button>
            )}
          </div>
        ))}
        {tickets.map((ticket) => (
          <div className="externalMCPRecord" key={ticket.id}>
            <div><strong>{text.settings.pendingAccess} {shortId(ticket.id)}</strong><small>{ticket.grants.length} {text.settings.capabilities} · {ticket.status}</small></div>
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

function externalMCPStatusLabel(connector: ConnectorStatus | undefined, status: ISCPPairingStatus | null, lanDirectTest: boolean, text: Copy) {
  if (!connector?.enabled) return text.settings.connectorDisabled;
  if (lanDirectTest) return text.settings.lanMCPTestReady;
  if (!status?.enabled) return text.settings.pairingNotConfigured;
  if (!status.ready) return text.settings.pairingUnavailable;
  return text.settings.ready;
}
