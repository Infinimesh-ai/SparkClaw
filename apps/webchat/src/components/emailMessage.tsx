import { EmailRenderPreview } from "./emailRenderPreview";
import { FileDown, Paperclip, RefreshCw } from "lucide-react";
import type { EmailEntry, EmailMessage, EmailPresentation } from "../api/email";
import { openEmailFile } from "../api/client";
import type { Copy, Language } from "../i18n";
import { formatDateTime } from "../lib/format";
import { EmailVerification } from "./emailVerification";
import { EmailClassificationControls } from "./emailClassification";
import { EmailProgress } from "./emailCommon";

export function EmailMessageCard({ mail, viewed, text, language, busy, onReanalyze, onError, presentation, onClassify, onReply, onRetryPresentation, onOpenConversation, onOpenMail, conversationMode = false }: {
  mail: EmailMessage; viewed: boolean; text: Copy; language: Language; busy: boolean;
  onReanalyze: (id: string) => void; onError: (reason: unknown) => void;
  presentation?: EmailPresentation;
  onClassify?: (mail: EmailMessage, entry: EmailEntry, remember: boolean) => Promise<void>;
  onReply?: (mail: EmailMessage, all: boolean) => void;
  onRetryPresentation?: () => void;
  onOpenConversation?: (id: string) => void;
  onOpenMail?: (id: string) => void;
  conversationMode?: boolean;
}) {
  const time = mail.sent_at || mail.arrived_at;
  const outgoing = ["sent", "outbound"].includes(mail.direction);
  const direction = outgoing ? text.email.outgoing : ["received", "inbound"].includes(mail.direction) ? text.email.incoming : text.email.title;
  const sender = outgoing ? text.email.me : mail.from || text.common.notSet;
  return (
    <article className={`emailMessage ${conversationMode ? `emailConversationMessage ${outgoing ? "outgoing" : "incoming"}` : ""} ${viewed ? "" : "unseen"}`} data-email-mail-id={mail.id} aria-label={mail.subject || text.email.untitled}>
      <header className="emailMessageHeading">
        {conversationMode && <span className="emailConversationAvatar" aria-hidden="true">{sender.trim().slice(0, 1).toUpperCase() || "@"}</span>}
        {conversationMode ? <span className="emailConversationSender"><strong>{sender}</strong><small>{direction}</small></span> : <span>{direction}</span>}
        {!viewed && <span className="emailUnseen">{text.email.unseen}</span>}
        <time dateTime={time}>{!mail.sent_at && `${text.email.arrivalTime}: `}{formatDateTime(time, language)}</time>
      </header>
      {!conversationMode && mail.history_state && mail.history_state !== "complete" && <p className="emailWarning" role="status">{mail.history_state === "pending" ? text.email.historyPending : mail.history_state === "failed" ? text.email.historyFailed : text.email.historyMissing}</p>}
      {mail.local_send_id && !mail.original_available && <p className="emailNotice">{mail.confirmation_source === "owner_confirmed_capture" ? text.email.sendOwnerConfirmed : text.email.sendSucceeded} · {text.email.sendEvidencePending}</p>}
      {presentation?.state === "ready" && presentation.service_label && <strong>{presentation.service_label}</strong>}
      {!conversationMode && <><small>{mail.local_send_id && !mail.original_available ? text.email.subject : text.email.originalSubject}</small><h3>{mail.subject || text.email.untitled}</h3></>}
      {!conversationMode && <dl className="emailAddresses">
        <dt>{text.email.from}</dt><dd>{mail.from || text.common.notSet}</dd>
        <dt>{text.email.to}</dt><dd>{(mail.to ?? []).join(", ") || text.common.notSet}</dd>
        {(mail.cc ?? []).length > 0 && <><dt>{text.email.cc}</dt><dd>{mail.cc.join(", ")}</dd></>}
        <dt>{text.email.receivingAddress}</dt><dd>{mail.receiving_address}</dd>
      </dl>}
      {mail.verification && <EmailVerification mailId={mail.id} value={mail.verification} purpose={presentation?.state === "ready" ? presentation.purpose : undefined} text={text} language={language} />}
      {!conversationMode && onClassify && <EmailClassificationControls mail={mail} presentation={presentation} text={text} busy={busy} onChange={onClassify} />}
      {(mail.attachments ?? []).length > 0 && <p className="emailWarning">{text.email.attachmentNotAnalyzed}</p>}
      {!conversationMode && mail.summary && <p className="emailSummary">{mail.summary}</p>}
      {!conversationMode && <EmailProgress state={mail.summary_state} text={text} />}
      {!conversationMode && mail.summary_partial && <p className="emailWarning">{text.email.summaryPartial}</p>}
      {conversationMode && presentation?.state === "ready" && presentation.summary
        ? <p className="emailConversationSummary">{presentation.summary}</p>
        : conversationMode && <div className="emailConversationSummaryState" role="status"><EmailProgress state={presentation?.state ?? "waiting"} text={text} />{presentation?.state === "failed" && onRetryPresentation && <button className="emailTextButton" onClick={onRetryPresentation}>{text.common.refresh}</button>}</div>}
      {!conversationMode && (mail.body_available || mail.body_text) && <EmailRenderPreview key={`${mail.id}:${mail.body_revision ?? "source"}`} mail={mail} text={text} />}
      {conversationMode && (mail.body_available || mail.body_text) && <EmailRenderPreview key={`${mail.id}:${mail.body_revision ?? "source"}`} mail={mail} text={text} />}
      {conversationMode && <details className="emailConversationTechnical"><summary>{text.email.messageDetails}</summary>
        {onClassify && <EmailClassificationControls mail={mail} presentation={presentation} text={text} busy={busy} onChange={onClassify} />}
        <div className="emailMessageActions">
          <button className="emailTextButton" disabled={busy} onClick={() => onReanalyze(mail.id)}><RefreshCw size={14} className={busy ? "spin" : ""} />{text.email.reanalyze}</button>
        </div>
      </details>}
      <div className="emailMessageActions">
        {!conversationMode && mail.reply_mail_id && onOpenMail && <button className="emailTextButton" onClick={() => onOpenMail(mail.reply_mail_id!)}>{text.email.openReplyOriginal}</button>}
        {!conversationMode && mail.conversation_id && onOpenConversation && <button className="emailTextButton" onClick={() => onOpenConversation(mail.conversation_id!)}>{text.email.relatedConversation}</button>}
        {!conversationMode && onReply && <><button className="emailTextButton" onClick={() => onReply(mail, false)}>{text.email.reply}</button><button className="emailTextButton" onClick={() => onReply(mail, true)}>{text.email.replyAll}</button></>}
        {!conversationMode && <EmailProgress state={mail.processing_state} text={text} />}
        {mail.original_purged && <span className="emailNotice">{text.email.originalPurged}</span>}
        {mail.original_available && <button className="emailTextButton" onClick={() => void openEmailFile(mail.id).catch(onError)}><FileDown size={14} />{text.email.originalDownload}</button>}
        {(mail.attachments ?? []).map((attachment) => <button className="emailTextButton" key={attachment.id} disabled={!attachment.available} onClick={() => void openEmailFile(mail.id, attachment.id, attachment.name).catch(onError)} title={attachment.available ? attachment.name : text.email.sourceUnavailable}>
          <Paperclip size={14} /><span>{attachment.name}</span>{attachment.size !== undefined && <small>{Math.ceil(attachment.size / 1024)} KB</small>}
        </button>)}
        {!conversationMode && <button className="emailTextButton" disabled={busy} onClick={() => onReanalyze(mail.id)}><RefreshCw size={14} className={busy ? "spin" : ""} />{text.email.reanalyze}</button>}
      </div>
    </article>
  );
}
