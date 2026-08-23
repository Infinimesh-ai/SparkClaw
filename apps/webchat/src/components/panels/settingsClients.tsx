// Paired-clients section of the settings panel: list plus revoke action.
// Pure move from settings.tsx.
import { Trash2 } from "lucide-react";
import type { Client } from "../../api/types";
import type { Copy as CopyText, Language } from "../../i18n";
import { useAsyncAction } from "../../hooks/useAsyncAction";
import { formatTime } from "../../lib/format";

export function PairedClientsSettings({
  clients,
  text,
  language,
  onRevokeClient
}: {
  clients: Client[];
  text: CopyText;
  language: Language;
  onRevokeClient: (id: string) => Promise<void>;
}) {
  const clientAction = useAsyncAction();
  const revokingClient = clientAction.busy;

  async function revokeClient(id: string) {
    await clientAction.run(id, async () => {
      await onRevokeClient(id);
    });
  }

  return (
    <article className="settingsBlock">
      <div className="approvalTop">
        <strong>{text.settings.pairedClients}</strong>
        <span className="pill">{clients.length}</span>
      </div>
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
