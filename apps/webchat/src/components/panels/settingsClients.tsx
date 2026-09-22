// Owner clients section of the settings panel: issue, list, and revoke.
// Pure move from settings.tsx.
import { KeyRound, Trash2 } from "lucide-react";
import { useState } from "react";
import type { Client, IssuedClientCredential } from "../../api/types";
import type { Copy as CopyText, Language } from "../../i18n";
import { useAsyncAction } from "../../hooks/useAsyncAction";
import { formatTime } from "../../lib/format";

export function PairedClientsSettings({
  clients,
  text,
  language,
  onIssueClient,
  onRevokeClient
}: {
  clients: Client[];
  text: CopyText;
  language: Language;
  onIssueClient?: (name: string, idempotencyKey: string) => Promise<IssuedClientCredential>;
  onRevokeClient: (id: string) => Promise<void>;
}) {
  const clientAction = useAsyncAction();
  const revokingClient = clientAction.busy;
  const [clientName, setClientName] = useState("");
  const [issued, setIssued] = useState<IssuedClientCredential | null>(null);
  const [issuanceError, setIssuanceError] = useState("");
  const [issueBusy, setIssueBusy] = useState(false);

  async function issueClient() {
    if (!onIssueClient) return;
    setIssuanceError("");
    setIssued(null);
    setIssueBusy(true);
    const requestKey = globalThis.crypto?.randomUUID?.() ?? `client-${Date.now()}-${Math.random().toString(16).slice(2)}`;
    try {
      const result = await onIssueClient(clientName.trim() || text.settings.webClientDefaultName, requestKey);
      setIssued(result);
      setClientName("");
    } catch (error) {
      setIssuanceError(error instanceof Error ? error.message : text.settings.issueClientFailed);
    } finally {
      setIssueBusy(false);
    }
  }

  async function revokeClient(id: string) {
    await clientAction.run(id, async () => {
      await onRevokeClient(id);
    });
  }

  return (
    <article className="settingsBlock">
      <div className="approvalTop">
        <strong>{text.settings.clients}</strong>
        <span className="pill">{clients.length}</span>
      </div>
      {onIssueClient ? (
        <div className="clientIssuance">
          <label>
            <span>{text.settings.newWebClient}</span>
            <input value={clientName} maxLength={80} onChange={(event) => setClientName(event.target.value)} placeholder={text.settings.webClientDefaultName} />
          </label>
          <button className="approve" type="button" disabled={issueBusy || Boolean(clientAction.busy)} onClick={() => void issueClient()}>
            <KeyRound size={14} /> {text.settings.issueClient}
          </button>
          {issued ? <div className="issuedClientCredential" role="status">
            <strong>{text.settings.issuedClientToken}</strong>
            <code>{issued.token}</code>
            <small>{text.settings.issuedClientTokenWarning}</small>
          </div> : null}
          {issuanceError ? <small className="credentialValidationFeedback error">{issuanceError}</small> : null}
        </div>
      ) : null}
      {clients.length === 0 ? (
        <span className="muted">{text.settings.noClients}</span>
      ) : (
        <div className="clientList">
          {clients.map((client) => (
            <div className="clientItem" key={client.id}>
              <div>
                <strong>{client.name}</strong>
                <small>
                  {client.revoked_at
                    ? text.common.revoked
                    : client.last_seen_at
                      ? `${text.settings.seen} ${formatTime(client.last_seen_at, language)}`
                      : text.settings.notSeen}
                </small>
              </div>
              {!client.revoked_at && (
                <button className="reject" onClick={() => void revokeClient(client.id)} disabled={revokingClient === client.id} title={text.settings.revokeClient}>
                  <Trash2 size={14} />
                </button>
              )}
            </div>
          ))}
        </div>
      )}
    </article>
  );
}
