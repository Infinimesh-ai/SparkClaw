import { Copy, KeyRound, LogOut, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { APIError } from "../../api/client";
import type { Client, IssuedClientCredential, WorkbenchIdentity } from "../../api/types";
import type { Copy as CopyText, Language } from "../../i18n";
import { formatDateTime } from "../../lib/format";

export type ClientSettingsActions = {
  accessMode?: WorkbenchIdentity["access_mode"];
  currentClientID?: string;
  clientsLoading?: boolean;
  clientsError?: string;
  onReloadClients?: () => Promise<Client[]>;
  onCurrentClientRevoked?: () => Promise<void>;
  onLogout?: () => Promise<void>;
};

type IssuanceRequest = { name: string; key: string };

export function PairedClientsSettings({
  clients, text, language, currentClientID, accessMode, clientsLoading = false, clientsError = "",
  onIssueClient, onRevokeClient, onReloadClients, onCurrentClientRevoked, onLogout
}: ClientSettingsActions & {
  clients: Client[];
  text: CopyText;
  language: Language;
  onIssueClient?: (name: string, idempotencyKey: string) => Promise<IssuedClientCredential>;
  onRevokeClient: (id: string) => Promise<void>;
}) {
  const [clientName, setClientName] = useState("");
  const [issued, setIssued] = useState<IssuedClientCredential | null>(null);
  const [pending, setPending] = useState<IssuanceRequest | null>(null);
  const [issuanceError, setIssuanceError] = useState("");
  const [recovery, setRecovery] = useState<{ clientID?: string } | null>(null);
  const [copyFeedback, setCopyFeedback] = useState("");
  const [actionError, setActionError] = useState("");
  const [busy, setBusy] = useState("");
  const [loadedClients, setLoadedClients] = useState<Client[] | null>(null);
  const [loading, setLoading] = useState(Boolean(onReloadClients) || clientsLoading);
  const [loadError, setLoadError] = useState("");
  const mounted = useRef(false);
  const action = useRef("");
  const reload = useRef(onReloadClients);
  reload.current = onReloadClients;
  const listGeneration = useRef(0);
  const visibleClients = loadedClients ?? clients;
  const listLoading = loading || clientsLoading;
  const listFailed = Boolean(loadError || clientsError);
  const identityKnown = Boolean(currentClientID) || accessMode === "local";
  const issuedLabel = issued?.connection_credential ? text.settings.issuedClientConnectionCredential : text.settings.issuedClientToken;
  const issuedWarning = issued?.connection_credential ? text.settings.issuedClientConnectionCredentialWarning : text.settings.issuedClientTokenWarning;

  async function loadClients() {
    if (!reload.current) return;
    const generation = ++listGeneration.current;
    setLoading(true);
    setLoadError("");
    try {
      const next = await reload.current();
      if (mounted.current && generation === listGeneration.current) setLoadedClients(next);
    } catch {
      if (mounted.current && generation === listGeneration.current) setLoadError(text.settings.clientsLoadFailed);
    } finally {
      if (mounted.current && generation === listGeneration.current) setLoading(false);
    }
  }

  useEffect(() => {
    mounted.current = true;
    void loadClients();
    return () => { mounted.current = false; listGeneration.current++; };
    // Each visit starts a fresh list request. Callback identity changes must
    // not reload or discard the credential while this view stays open.
  }, []);

  useEffect(() => {
    if (!recovery?.clientID || !pending) return;
    if (!visibleClients.some((client) => client.id === recovery.clientID && client.revoked_at)) return;
    setRecovery(null);
    setPending(null);
    setClientName(pending.name);
    setIssuanceError("");
  }, [pending, recovery, visibleClients]);

  async function issueClient() {
    if (!onIssueClient || action.current || issued || recovery) return;
    let request = pending;
    if (!request) {
      try {
        const bytes = globalThis.crypto.getRandomValues(new Uint8Array(24));
        request = { name: clientName.trim() || text.settings.webClientDefaultName,
          key: `client-${Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("")}` };
      } catch { setIssuanceError(text.settings.issueClientFailed); return; }
    }
    setPending(request);
    setIssuanceError("");
    action.current = "issue";
    setBusy("issue");
    try {
      const result = await onIssueClient(request.name, request.key);
      if (!mounted.current) return;
      setIssued(result);
      setPending(null);
      setClientName("");
      setCopyFeedback("");
      // Receiving the one-time secret is complete independently of list refresh.
      void loadClients();
    } catch (error) {
      if (!mounted.current) return;
      if (error instanceof APIError && error.code === "CLIENT_REVOKED") {
        setPending(null);
        setIssuanceError(text.settings.clientAlreadyRevoked);
      } else if (error instanceof APIError && error.status === 409) {
        const details = error.details as { client_id?: unknown } | undefined;
        setRecovery({ clientID: typeof details?.client_id === "string" ? details.client_id : undefined });
        setIssuanceError(text.settings.clientCredentialUnrecoverable);
        void loadClients();
      } else if (error instanceof APIError && [400, 401, 403, 404, 422].includes(error.status)) {
        setPending(null);
        setIssuanceError(text.settings.issueClientFailed);
      } else {
        setIssuanceError(text.settings.clientIssuanceUnknown);
      }
    } finally {
      action.current = "";
      if (mounted.current) setBusy("");
    }
  }

  async function copyToken() {
    if (!issued) return;
    try {
      await navigator.clipboard.writeText(issued.connection_credential ?? issued.token);
      if (mounted.current) setCopyFeedback(text.settings.clientTokenCopied);
    } catch {
      if (mounted.current) setCopyFeedback(text.settings.clientTokenCopyFailed);
    }
  }

  async function revokeClient(client: Client) {
    if (action.current) return;
    const current = client.id === currentClientID;
    if (current && !onCurrentClientRevoked && !onLogout) {
      setActionError(text.settings.clientLogoutFailed);
      return;
    }
    if (current && !window.confirm(text.settings.revokeCurrentClientConfirm)) return;
    action.current = client.id;
    setBusy(client.id);
    setActionError("");
    let revoked = false;
    try {
      await onRevokeClient(client.id);
      revoked = true;
      // Stop this device's protected channels before trying another API call.
      if (current) {
        await (onCurrentClientRevoked ?? onLogout)?.();
        return;
      }
      if (!mounted.current) return;
      setLoadedClients(visibleClients.map((item) => item.id === client.id ? { ...item, revoked_at: new Date().toISOString() } : item));
      if (issued?.client.id === client.id) { setIssued(null); setCopyFeedback(""); }
      if (recovery?.clientID === client.id && pending) {
        setRecovery(null);
        setPending(null);
        setClientName(pending.name);
        setIssuanceError("");
      }
      void loadClients();
    } catch {
      if (mounted.current) setActionError(revoked ? text.settings.clientLogoutFailed : text.settings.clientRevokeFailed);
    } finally {
      action.current = "";
      if (mounted.current) setBusy("");
    }
  }

  async function logout() {
    if (!onLogout || action.current) return;
    action.current = "logout";
    setBusy("logout");
    setActionError("");
    try { await onLogout(); }
    catch { if (mounted.current) setActionError(text.settings.clientLogoutFailed); }
    finally { action.current = ""; if (mounted.current) setBusy(""); }
  }

  return <article className="settingsBlock clientSettings">
    <div className="approvalTop">
      <strong>{text.settings.clients}</strong>
      {onReloadClients && <button type="button" className="ghost" disabled={listLoading} onClick={() => void loadClients()}><RefreshCw size={14} /> {text.settings.refreshClients}</button>}
    </div>
    <p className="muted">{text.settings.clientsDescription}</p>
    {accessMode === "local" && <p className="muted">{text.auth.localAccess}</p>}
    {onIssueClient && <div className="clientIssuance">
      <label><span>{text.settings.newWebClient}</span><input value={clientName} disabled={Boolean(pending) || Boolean(busy) || Boolean(issued)} maxLength={80} onChange={(event) => setClientName(event.target.value)} placeholder={text.settings.webClientDefaultName} /></label>
      <button className="approve" type="button" disabled={Boolean(busy) || Boolean(issued) || Boolean(recovery)} onClick={() => void issueClient()}>
        <KeyRound size={14} /> {busy === "issue" ? text.settings.issuingClient : pending ? text.settings.retryClientIssuance : text.settings.issueClient}
      </button>
      {issued && <div className="issuedClientCredential" role="status">
        <strong>{issuedLabel}</strong>
        <small>{issued.client.name} · {issued.client.id}</small>
        <code tabIndex={0} aria-label={issuedLabel}>{issued.connection_credential ?? issued.token}</code>
        <small>{issuedWarning}</small>
        <div className="clientCredentialActions">
          <button type="button" className="ghost" onClick={() => void copyToken()}><Copy size={14} /> {text.common.copy}</button>
          <button type="button" className="ghost" onClick={() => { setIssued(null); setCopyFeedback(""); }}>{text.settings.hideClientToken}</button>
        </div>
        {copyFeedback && <small role="status">{copyFeedback}</small>}
      </div>}
      {issuanceError && <p className="credentialValidationFeedback error" role="alert">{issuanceError}</p>}
      {pending && <small>{text.settings.pendingClientName}: {pending.name}</small>}
      {recovery?.clientID && <small>{text.settings.clientID}: {recovery.clientID}</small>}
    </div>}
    {listLoading && <p className="muted" role="status">{text.settings.loadingClients}</p>}
    {listFailed && <div className="credentialValidationFeedback error" role="alert">
      <p>{text.settings.clientsLoadFailed}</p>
      {onReloadClients && <button type="button" className="ghost" disabled={listLoading} onClick={() => void loadClients()}>{text.settings.retryClients}</button>}
    </div>}
    {!listLoading && !listFailed && visibleClients.length === 0 && <span className="muted">{text.settings.noClients}</span>}
    {!listLoading && visibleClients.length > 0 && !identityKnown && <p className="credentialValidationFeedback error" role="alert">{text.settings.clientsIdentityUnavailable}</p>}
    {visibleClients.length > 0 && <div className="clientList">
      {visibleClients.map((client) => <div className="clientItem" key={client.id}>
        <div>
          <strong>{client.name} {client.id === currentClientID && <span className="pill">{text.settings.currentClient}</span>}</strong>
          <small>{text.settings.clientID}: {client.id}</small>
          <small>{text.settings.clientCreated}: <time dateTime={client.created_at} title={client.created_at}>{formatDateTime(client.created_at, language)}</time></small>
          <small>{client.last_seen_at ? <>{text.settings.seen}: <time dateTime={client.last_seen_at} title={client.last_seen_at}>{formatDateTime(client.last_seen_at, language)}</time></> : text.settings.notSeen}</small>
          <small>{client.revoked_at ? <>{text.common.revoked}: <time dateTime={client.revoked_at} title={client.revoked_at}>{formatDateTime(client.revoked_at, language)}</time></> : text.settings.active}</small>
        </div>
        {!client.revoked_at && <button type="button" className="reject" onClick={() => void revokeClient(client)} disabled={Boolean(busy) || !identityKnown} aria-label={`${text.settings.revokeClient}: ${client.name}`} title={text.settings.revokeClient}><Trash2 size={14} /></button>}
      </div>)}
    </div>}
    {actionError && <p className="credentialValidationFeedback error" role="alert">{actionError}</p>}
    {onLogout && <button type="button" className="ghost clientLogout" disabled={Boolean(busy)} onClick={() => void logout()}><LogOut size={14} /> {text.settings.logoutClient}</button>}
  </article>;
}
