import { ArrowDown, ArrowUp, FileAudio, FileImage, FileText, RotateCcw, Send, X } from "lucide-react";
import type { DeliveryEndpoint, DeliveryPart, MessageDelivery, MessageHistoryItem } from "../api/types";
import type { Copy as CopyText, Language } from "../i18n";
import type { DeliveryDraftValidation, ExternalDeliveryDraft } from "../lib/deliveryDraft";
import { deliveryDraftParts } from "../lib/deliveryDraft";
import { formatBytes, formatDateTime } from "../lib/format";

export function DeliveryHistory({
  items,
  text,
  language
}: {
  items: MessageHistoryItem[];
  text: CopyText;
  language: Language;
}) {
  if (items.length === 0) {
    return (
      <div className="emptyState externalEmptyState">
        <Send size={25} />
        <span>{text.chat.noDeliveryHistory}</span>
      </div>
    );
  }
  return (
    <div className="deliveryHistory">
      {items.map((item) => (
        <article className={`deliveryHistoryItem ${item.direction}`} key={`${item.direction}-${item.id}`}>
          <div className="deliveryHistoryHeader">
            <strong>
              {item.direction === "receive" ? text.chat.receivedFrom : text.chat.sentTo} {item.recipient_display_name}
            </strong>
            <span className={`deliveryStatus ${item.status}`}>{item.status.replaceAll("_", " ")}</span>
          </div>
          <span className="deliveryIdentity">
            {item.software_display_name} · {item.account_display_name}
          </span>
          {item.content && <p>{item.content}</p>}
          <time>{formatDateTime(item.created_at, language)}</time>
        </article>
      ))}
    </div>
  );
}

export function ExternalPartTray({
  parts,
  fallbackPartIds,
  supportsCaption,
  text,
  onChange,
  onMove,
  onRemove
}: {
  parts: DeliveryPart[];
  fallbackPartIds: string[];
  supportsCaption: boolean;
  text: CopyText;
  onChange: (id: string, update: Partial<DeliveryPart>) => void;
  onMove: (index: number, offset: -1 | 1) => void;
  onRemove: (id: string) => void;
}) {
  if (parts.length === 0) return null;
  return (
    <div className="externalPartTray">
      {parts.map((part, index) => {
        const Icon = part.kind === "image" ? FileImage : part.kind === "audio" ? FileAudio : FileText;
        return (
          <div className="externalPart" key={part.id}>
            <div className="externalPartSummary">
              <Icon size={17} />
              <span>
                <strong>{part.name || part.kind}</strong>
                <small>
                  {part.kind} · {formatBytes(part.bytes ?? 0)}
                  {fallbackPartIds.includes(part.id) ? ` · ${text.chat.fileFallback}` : ""}
                </small>
              </span>
            </div>
            {part.kind === "audio" && (
              <select
                aria-label={text.chat.audioFile}
                value={part.disposition}
                onChange={(event) => onChange(part.id, { disposition: event.target.value as DeliveryPart["disposition"] })}
              >
                <option value="attachment">{text.chat.audioFile}</option>
                <option value="voice_note">{text.chat.voiceNote}</option>
              </select>
            )}
            {supportsCaption && part.kind !== "file" && (
              <input
                aria-label={text.chat.caption}
                value={part.caption ?? ""}
                onChange={(event) => onChange(part.id, { caption: event.target.value })}
                placeholder={text.chat.caption}
              />
            )}
            <div className="externalPartActions">
              <button type="button" className="miniIconButton" disabled={index === 0} onClick={() => onMove(index, -1)} title={text.chat.moveEarlier}>
                <ArrowUp size={14} />
              </button>
              <button type="button" className="miniIconButton" disabled={index === parts.length - 1} onClick={() => onMove(index, 1)} title={text.chat.moveLater}>
                <ArrowDown size={14} />
              </button>
              <button type="button" className="miniIconButton dangerIcon" onClick={() => onRemove(part.id)} title={text.chat.removeAttachment}>
                <X size={14} />
              </button>
            </div>
          </div>
        );
      })}
    </div>
  );
}

export function DeliveryReceiptSummary({
  delivery,
  retrying,
  text,
  onRetry
}: {
  delivery: MessageDelivery | null;
  retrying: boolean;
  text: CopyText;
  onRetry: () => void;
}) {
  if (!delivery) return null;
  const statusLabel = deliveryStatusLabel(delivery.status, text);
  const retryable = delivery.receipt?.retry_state === "retryable" && delivery.receipt.error_code !== "outcome_unknown";
  return (
    <div className={`deliveryReceiptSummary ${delivery.status}`} role="status">
      <span>
        <strong>{statusLabel}</strong>
        <small>
          {delivery.software_display_name} · {delivery.recipient_display_name} · {delivery.account_display_name}
        </small>
      </span>
      {retryable && (
        <button type="button" className="secondaryButton" onClick={onRetry} disabled={retrying} title={text.chat.retryDelivery}>
          <RotateCcw size={15} />
          <span>{text.chat.retryDelivery}</span>
        </button>
      )}
    </div>
  );
}

export function DeliveryReviewDialog({
  endpoint,
  draft,
  validation,
  busy,
  text,
  onCancel,
  onConfirm
}: {
  endpoint: DeliveryEndpoint;
  draft: ExternalDeliveryDraft;
  validation: DeliveryDraftValidation;
  busy: boolean;
  text: CopyText;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const parts = deliveryDraftParts(draft);
  return (
    <div className="documentPickerOverlay" role="dialog" aria-modal="true" aria-label={text.chat.sendReview}>
      <div className="deliveryReview">
        <div className="documentPickerHeader">
          <strong>{text.chat.sendReview}</strong>
          <button type="button" className="attachmentRemove" onClick={onCancel} disabled={busy} title={text.common.cancel}>
            <X size={14} />
          </button>
        </div>
        <dl className="deliveryReviewDestination">
          <dt>{text.chat.destination}</dt>
          <dd>
            <strong>{endpoint.recipient.display_name}</strong>
            <span>
              {endpoint.software_display_name} · {endpoint.account_display_name}
              {endpoint.conversation_label ? ` · ${endpoint.conversation_label}` : ""}
            </span>
          </dd>
        </dl>
        <div className="deliveryReviewParts">
          {parts.map((part) => (
            <div key={part.id}>
              <strong>{part.kind}</strong>
              <span>{part.kind === "text" ? part.text : part.name || part.kind}</span>
              {validation.fallbackPartIds.includes(part.id) && <small>{text.chat.fileFallback}</small>}
            </div>
          ))}
        </div>
        <div className="deliveryReviewFooter">
          <span>
            {text.chat.totalSize}: {formatBytes(validation.totalBytes)}
          </span>
          <div className="buttonRow">
            <button type="button" className="secondaryButton" onClick={onCancel} disabled={busy}>
              {text.common.cancel}
            </button>
            <button type="button" className="primaryButton" onClick={onConfirm} disabled={busy || !validation.valid}>
              <Send size={16} />
              <span>{text.chat.confirmSend}</span>
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

function deliveryStatusLabel(status: MessageDelivery["status"], text: CopyText) {
  if (status === "sent") return text.chat.deliverySent;
  if (status === "partially_sent") return text.chat.deliveryPartial;
  if (status === "outcome_unknown") return text.chat.deliveryUnknown;
  return text.chat.deliveryFailed;
}
