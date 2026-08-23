// Owner-profile section of the settings panel: display, edit form, and
// preference parsing. Pure move from settings.tsx.
import { useState } from "react";
import { Check, Pencil, UserRound, X } from "lucide-react";
import type { OwnerProfile } from "../../api/types";
import type { Copy as CopyText } from "../../i18n";
import { useAsyncAction } from "../../hooks/useAsyncAction";
import { formatPreferences, parsePreferences } from "../../lib/format";

export function OwnerProfileSettings({
  ownerProfile,
  text,
  onUpdateOwner
}: {
  ownerProfile: OwnerProfile | null;
  text: CopyText;
  onUpdateOwner: (displayName: string, email: string, preferences: Record<string, string>) => Promise<void>;
}) {
  const [editingOwner, setEditingOwner] = useState(false);
  const [ownerName, setOwnerName] = useState("");
  const [ownerEmail, setOwnerEmail] = useState("");
  const [ownerPrefsText, setOwnerPrefsText] = useState("");
  const [ownerError, setOwnerError] = useState("");
  const ownerAction = useAsyncAction({
    clearError: () => setOwnerError(""),
    onError: (error) => setOwnerError(error instanceof Error ? error.message : text.errors.ownerUpdate)
  });
  const savingOwner = Boolean(ownerAction.busy);
  const preferences = ownerProfile?.preferences ?? {};

  function startOwnerEdit() {
    setOwnerName(ownerProfile?.display_name ?? "");
    setOwnerEmail(ownerProfile?.email ?? "");
    setOwnerPrefsText(formatPreferences(preferences));
    setOwnerError("");
    setEditingOwner(true);
  }

  function cancelOwnerEdit() {
    setEditingOwner(false);
    setOwnerName("");
    setOwnerEmail("");
    setOwnerPrefsText("");
    setOwnerError("");
  }

  async function saveOwnerEdit() {
    await ownerAction.run("owner", async () => {
      await onUpdateOwner(ownerName, ownerEmail, parsePreferences(ownerPrefsText, text));
      cancelOwnerEdit();
    });
  }

  return (
    <article className="settingsBlock">
      <div className="approvalTop">
        <span className="settingsTitle">
          <UserRound size={15} />
          <strong>{text.settings.ownerProfile}</strong>
        </span>
        <div className="buttonRow compactButtons">
          {editingOwner ? (
            <>
              <button className="approve" onClick={() => void saveOwnerEdit()} disabled={savingOwner} title={text.settings.saveOwner}>
                <Check size={15} />
              </button>
              <button className="edit" onClick={cancelOwnerEdit} disabled={savingOwner} title={text.settings.cancelOwner}>
                <X size={15} />
              </button>
            </>
          ) : (
            <button className="edit" onClick={startOwnerEdit} title={text.settings.editOwner}>
              <Pencil size={15} />
            </button>
          )}
        </div>
      </div>
      {editingOwner ? (
        <div className="ownerEditor">
          <label>
            <span>{text.settings.name}</span>
            <input value={ownerName} onChange={(event) => setOwnerName(event.target.value)} disabled={savingOwner} />
          </label>
          <label>
            <span>{text.settings.email}</span>
            <input value={ownerEmail} onChange={(event) => setOwnerEmail(event.target.value)} disabled={savingOwner} />
          </label>
          <label>
            <span>{text.settings.preferences}</span>
            <textarea value={ownerPrefsText} onChange={(event) => setOwnerPrefsText(event.target.value)} disabled={savingOwner} />
          </label>
          {ownerError && <span className="compactError">{ownerError}</span>}
        </div>
      ) : ownerProfile ? (
        <>
          <dl className="statusGrid compact">
            <dt>{text.settings.name}</dt>
            <dd>{ownerProfile.display_name}</dd>
            <dt>{text.settings.email}</dt>
            <dd>{ownerProfile.email || text.common.notSet}</dd>
          </dl>
          <div className="evalCases">
            {Object.entries(preferences).map(([key, value]) => (
              <span key={key}>{key}:{value}</span>
            ))}
            {Object.keys(preferences).length === 0 && <span>{text.common.none}</span>}
          </div>
        </>
      ) : (
        <span className="muted">{text.settings.ownerUnavailable}</span>
      )}
    </article>
  );
}
