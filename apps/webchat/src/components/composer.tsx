// Composer dock: delivery target toolbar, attachment/external-part trays,
// the message form with voice input, the workspace document picker overlay,
// and the delivery review dialog. Extracted from App.tsx so the root
// component stays below the size baseline; document-picker and IME
// composition state is local because nothing outside the dock reads it.
// Message sending and the voice/delivery hooks stay in the parent.
import { useMemo, useRef, useState } from "react";
import type { Dispatch, FormEvent, KeyboardEvent, MutableRefObject, SetStateAction } from "react";
import { FileSearch, Send, Upload, X } from "lucide-react";
import { api, openDocumentFile } from "../api/client";
import type { Copy as CopyText, Language } from "../i18n";
import { isImageAttachment, isImageContentType, WorkspaceFileImage } from "./messages";
import { DeliveryReceiptSummary, DeliveryReviewDialog, ExternalPartTray } from "./delivery";
import { VoiceInputButton, VoiceInputStatus } from "./VoiceInputButton";
import type { useExternalDelivery } from "../hooks/useExternalDelivery";
import type { useVoiceInput } from "../hooks/useVoiceInput";
import type { VoiceInputState } from "../hooks/useVoiceInput";
import {
  fileKindLabel,
  fileNameFromPath,
  formatBytes,
  formatDateTime,
  loadDocumentUsage,
  saveDocumentUsage,
  sortDocumentsByUsage
} from "../lib/format";
import type { DocumentUsage } from "../lib/format";
import {
  deliveryPartIDFromAttachment,
  deliveryPartFromAttachment,
  moveDeliveryPart
} from "../lib/deliveryDraft";
import type { ArtifactObject, MessageAttachment } from "../api/types";

type DeliveryProps = Pick<
  ReturnType<typeof useExternalDelivery>,
  | "deliveryBusy"
  | "deliveryReviewOpen"
  | "setDeliveryReviewOpen"
  | "activeDeliveryEndpoint"
  | "activeExternalDraft"
  | "externalDeliveryIntent"
  | "activeDeliveryValidation"
  | "activeLastDelivery"
  | "updateExternalDraft"
  | "updateExternalPart"
  | "removeExternalPart"
  | "openDeliveryReview"
  | "confirmExternalDelivery"
  | "retryExternalDelivery"
>;

type ComposerDockProps = DeliveryProps & {
  text: CopyText;
  language: Language;
  activeSession: string;
  activeInput: string;
  activeAttachments: MessageAttachment[];
  busy: boolean;
  voice: ReturnType<typeof useVoiceInput>;
  composerInputRef: MutableRefObject<HTMLTextAreaElement | null>;
  setDraftsBySession: Dispatch<SetStateAction<Record<string, string>>>;
  setAttachmentsBySession: Dispatch<SetStateAction<Record<string, MessageAttachment[]>>>;
  setError: (message: string) => void;
  refreshGlobal: () => Promise<void>;
  onSend: () => void;
};

export function ComposerDock({
  text,
  language,
  activeSession,
  activeInput,
  activeAttachments,
  busy,
  voice,
  composerInputRef,
  setDraftsBySession,
  setAttachmentsBySession,
  setError,
  refreshGlobal,
  onSend,
  deliveryBusy,
  deliveryReviewOpen,
  setDeliveryReviewOpen,
  activeDeliveryEndpoint,
  activeExternalDraft,
  externalDeliveryIntent,
  activeDeliveryValidation,
  activeLastDelivery,
  updateExternalDraft,
  updateExternalPart,
  removeExternalPart,
  openDeliveryReview,
  confirmExternalDelivery,
  retryExternalDelivery
}: ComposerDockProps) {
  const [availableDocuments, setAvailableDocuments] = useState<ArtifactObject[]>([]);
  const [choosingDocument, setChoosingDocument] = useState(false);
  const [documentPickerOpen, setDocumentPickerOpen] = useState(false);
  const [documentUsage, setDocumentUsage] = useState<Record<string, DocumentUsage>>(() => loadDocumentUsage());
  const [isComposingInput, setIsComposingInput] = useState(false);
  const [compositionEndedAt, setCompositionEndedAt] = useState(0);
  const [uploadingDocument, setUploadingDocument] = useState(false);
  const uploadInputRef = useRef<HTMLInputElement | null>(null);

  const sortedAvailableDocuments = useMemo(
    () => sortDocumentsByUsage(availableDocuments, documentUsage),
    [availableDocuments, documentUsage]
  );
  const voiceLabel = voiceInputLabel(voice.state, voice.errorCode, voice.errorDetail, text);
  const voiceTitle = voiceInputTitle(voice.state, voiceLabel, text);

  function onSubmit(event: FormEvent) {
    event.preventDefault();
    if (isComposingInput || Date.now() - compositionEndedAt < 80) return;
    if (externalDeliveryIntent) {
      openDeliveryReview();
    } else {
      onSend();
    }
  }

  function onComposerKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key !== "Enter" || event.shiftKey) return;
    if (isComposingInput || event.nativeEvent.isComposing || Date.now() - compositionEndedAt < 80) {
      return;
    }
    event.preventDefault();
    if (externalDeliveryIntent) {
      openDeliveryReview();
    } else {
      onSend();
    }
  }

  function stageAttachment(attachment: MessageAttachment) {
    if (!activeSession) return;
    const partID = deliveryPartIDFromAttachment(attachment);
    setAttachmentsBySession((current) => {
      const existing = externalDeliveryIntent ? current[activeSession] ?? [] : [];
      return {
        ...current,
        [activeSession]: [...existing.filter((item) => deliveryPartIDFromAttachment(item) !== partID), attachment]
      };
    });
    updateExternalDraft((draft) => {
      const existing = externalDeliveryIntent ? draft.parts : [];
      return {
        ...draft,
        parts: [...existing.filter((part) => part.id !== partID), deliveryPartFromAttachment(partID, attachment)]
      };
    });
  }

  async function uploadDocument(file: File | null) {
    if (!file || !activeSession || uploadingDocument) return;
    try {
      setUploadingDocument(true);
      setError("");
      const result = await api.uploadDocument(activeSession, file);
      const attachment: MessageAttachment = {
        artifact_id: result.artifact?.id,
        name: file.name,
        rel_path: result.rel_path || result.artifact?.key || file.name,
        uri: result.artifact?.uri,
        content_type: result.artifact?.content_type || file.type,
        bytes: result.bytes || result.artifact?.bytes,
        width: result.media?.width,
        height: result.media?.height,
        sha256: result.media?.sha256,
        source: isImageContentType(result.artifact?.content_type || file.type) ? "web_upload" : undefined
      };
      stageAttachment(attachment);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.upload);
    } finally {
      setUploadingDocument(false);
      if (uploadInputRef.current) {
        uploadInputRef.current.value = "";
      }
    }
  }

  async function openDocumentPicker() {
    if (!activeSession || choosingDocument) return;
    try {
      setChoosingDocument(true);
      setError("");
      const result = await api.availableDocuments(activeSession);
      const documents = result.documents ?? [];
      setAvailableDocuments(documents);
      setDocumentPickerOpen(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.upload);
    } finally {
      setChoosingDocument(false);
    }
  }

  function chooseAvailableDocument(document: ArtifactObject) {
    if (!activeSession) return;
    const attachment: MessageAttachment = {
      artifact_id: document.id,
      name: fileNameFromPath(document.key),
      rel_path: document.key,
      uri: document.uri,
      content_type: document.content_type,
      bytes: document.bytes
    };
    stageAttachment(attachment);
    setDocumentUsage((current) => {
      const previous = current[document.key] ?? { count: 0, last_used_at: "" };
      const next = {
        ...current,
        [document.key]: { count: previous.count + 1, last_used_at: new Date().toISOString() }
      };
      saveDocumentUsage(next);
      return next;
    });
    setDocumentPickerOpen(false);
  }

  function removeAttachment(sessionId: string, attachment: MessageAttachment) {
    if (!sessionId) return;
    const partID = deliveryPartIDFromAttachment(attachment);
    setAttachmentsBySession((current) => ({
      ...current,
      [sessionId]: (current[sessionId] ?? []).filter((item) => item !== attachment)
    }));
    if (sessionId === activeSession) {
      updateExternalDraft((draft) => ({ ...draft, parts: draft.parts.filter((part) => part.id !== partID) }));
    }
  }

  return (
    <>
      <div className="composerDock">
        <DeliveryReceiptSummary
          delivery={activeLastDelivery}
          retrying={deliveryBusy}
          text={text}
          onRetry={() => void retryExternalDelivery()}
        />
        {!externalDeliveryIntent && activeAttachments.length > 0 && (
          <div className="attachmentTray">
            {activeAttachments.map((attachment) => (
              <div className="attachmentChip" key={`${attachment.artifact_id ?? attachment.rel_path}-${attachment.rel_path}`}>
                <button
                  type="button"
                  className={`attachmentOpen ${isImageAttachment(attachment) ? "image" : ""}`}
                  title={text.chat.openAttachment}
                  onClick={() => void openDocumentFile(attachment.rel_path, activeSession).catch(() => undefined)}
                >
                  {isImageAttachment(attachment) ? (
                    <WorkspaceFileImage path={attachment.rel_path} sessionId={activeSession} alt={attachment.name || attachment.rel_path} />
                  ) : (
                    <FileSearch size={15} />
                  )}
                  <span>{attachment.name || attachment.rel_path}</span>
                </button>
                <button
                  type="button"
                  className="attachmentRemove"
                  title={text.chat.removeAttachment}
                  onClick={() => removeAttachment(activeSession, attachment)}
                >
                  <X size={14} />
                </button>
              </div>
            ))}
          </div>
        )}
        {externalDeliveryIntent && (
          <ExternalPartTray
            parts={activeExternalDraft.parts}
            fallbackPartIds={activeDeliveryValidation.fallbackPartIds}
            supportsCaption={activeDeliveryEndpoint?.capabilities.supports_caption === true}
            text={text}
            onChange={updateExternalPart}
            onMove={(index, offset) => updateExternalDraft((draft) => ({ ...draft, parts: moveDeliveryPart(draft.parts, index, offset) }))}
            onRemove={removeExternalPart}
          />
        )}
        {externalDeliveryIntent && activeDeliveryValidation.error && (
          <span className="deliveryValidation">{deliveryValidationMessage(activeDeliveryValidation.error, text)}</span>
        )}
        <form className={`composer ${externalDeliveryIntent ? "external" : ""}`} onSubmit={onSubmit}>
          <input
            ref={uploadInputRef}
            className="documentUploadInput"
            type="file"
            accept={externalDeliveryIntent ? undefined : ".txt,.md,.csv,.pdf,.docx,.xlsx,.pptx,.png,.jpg,.jpeg,.gif,.webp,image/png,image/jpeg,image/gif,image/webp"}
            onChange={(event) => void uploadDocument(event.target.files?.[0] ?? null)}
          />
          <button
            className="uploadButton"
            type="button"
            disabled={busy || deliveryBusy || uploadingDocument || !activeSession}
            title={uploadingDocument ? text.chat.uploading : text.chat.upload}
            onClick={() => uploadInputRef.current?.click()}
          >
            <Upload size={18} />
          </button>
          <button
            className="uploadButton"
            type="button"
            disabled={busy || deliveryBusy || choosingDocument || !activeSession}
            title={choosingDocument ? text.chat.choosingFile : text.chat.chooseFile}
            onClick={() => void openDocumentPicker()}
          >
            <FileSearch size={18} />
          </button>
          {!externalDeliveryIntent && (
            <VoiceInputButton
              state={voice.state}
              disabled={voice.disabled}
              title={voiceTitle}
              onClick={() => {
                const input = composerInputRef.current;
                voice.toggle({
                  sessionId: activeSession,
                  draft: activeInput,
                  selectionStart: input?.selectionStart ?? activeInput.length,
                  selectionEnd: input?.selectionEnd ?? activeInput.length
                });
              }}
            />
          )}
          <textarea
            ref={composerInputRef}
            value={activeInput}
            onChange={(event) => {
              if (!activeSession) return;
              setDraftsBySession((current) => ({ ...current, [activeSession]: event.target.value }));
            }}
            onKeyDown={onComposerKeyDown}
            onCompositionStart={() => setIsComposingInput(true)}
            onCompositionEnd={() => {
              setIsComposingInput(false);
              setCompositionEndedAt(Date.now());
            }}
            placeholder={text.chat.placeholder}
            disabled={busy || deliveryBusy}
          />
          <button
            className="sendButton"
            disabled={
              externalDeliveryIntent
                ? deliveryBusy || !activeDeliveryValidation.valid
                : busy || voice.active || (!activeInput.trim() && activeAttachments.length === 0)
            }
            title={externalDeliveryIntent ? text.chat.reviewSend : text.chat.send}
          >
            <Send size={18} />
          </button>
          {!externalDeliveryIntent && (
            <VoiceInputStatus state={voice.state} level={voice.level} elapsedMs={voice.elapsedMs} label={voiceLabel} />
          )}
        </form>
      </div>
      {documentPickerOpen && (
        <div className="documentPickerOverlay" role="dialog" aria-modal="true" aria-label={text.chat.chooseFile}>
          <div className="documentPicker">
            <div className="documentPickerHeader">
              <strong>{text.chat.chooseFile}</strong>
              <button type="button" className="attachmentRemove" onClick={() => setDocumentPickerOpen(false)} title={text.common.cancel}>
                <X size={14} />
              </button>
            </div>
            {sortedAvailableDocuments.length === 0 ? (
              <span className="muted">{text.chat.noUploadedFiles}</span>
            ) : (
              <div className="documentPickerList">
                <div className="finderHeader">
                  <span>{text.chat.fileName}</span>
                  <span>{text.chat.fileUsage}</span>
                  <span>{text.chat.fileRecentUse}</span>
                  <span>{text.chat.fileSize}</span>
                  <span>{text.chat.fileKind}</span>
                </div>
                {sortedAvailableDocuments.map((document) => {
                  const usage = documentUsage[document.key];
                  return (
                    <button className="finderRow file" key={document.id} type="button" onClick={() => chooseAvailableDocument(document)}>
                      <span className="finderName fileName">
                        <FileSearch size={16} />
                        <strong>{fileNameFromPath(document.key)}</strong>
                      </span>
                      <span>{usage ? `${usage.count} ${text.chat.usedTimes}` : text.chat.neverUsed}</span>
                      <span>{usage ? formatDateTime(usage.last_used_at, language) : "--"}</span>
                      <span>{formatBytes(document.bytes)}</span>
                      <span>{fileKindLabel(document)}</span>
                    </button>
                  );
                })}
              </div>
            )}
          </div>
        </div>
      )}
      {deliveryReviewOpen && activeDeliveryEndpoint && activeDeliveryValidation.valid && (
        <DeliveryReviewDialog
          endpoint={activeDeliveryEndpoint}
          draft={activeExternalDraft}
          validation={activeDeliveryValidation}
          busy={deliveryBusy}
          text={text}
          onCancel={() => setDeliveryReviewOpen(false)}
          onConfirm={() => void confirmExternalDelivery()}
        />
      )}
    </>
  );
}

function voiceInputTitle(state: VoiceInputState, label: string, text: CopyText) {
  if (state === "recording") return text.chat.voiceStop;
  if (state === "encoding" || state === "transcribing") return text.chat.voiceCancel;
  if (state === "requesting_permission") return text.chat.voiceRequesting;
  if (state === "disabled") return label;
  return text.chat.voiceStart;
}

function voiceInputLabel(state: VoiceInputState, errorCode: string, errorDetail: string, text: CopyText) {
  if (state === "requesting_permission") return text.chat.voiceRequesting;
  if (state === "recording") return text.chat.voiceRecording;
  if (state === "encoding") return text.chat.voicePreparing;
  if (state === "transcribing") return text.chat.voiceTranscribing;
  switch (errorCode) {
    case "voice_capture_unsupported":
      return text.chat.voiceUnsupported;
    case "voice_permission_denied":
      return text.chat.voicePermissionDenied;
    case "voice_no_device":
      return text.chat.voiceNoDevice;
    case "voice_capture_failed":
      return text.chat.voiceCaptureFailed;
    case "speech_too_short":
      return text.chat.voiceTooShort;
    case "speech_no_speech":
      return text.chat.voiceNoSpeech;
    case "speech_too_large":
      return text.chat.voiceTooLarge;
    case "speech_busy":
      return text.chat.voiceBusy;
    case "speech_disabled":
    case "speech_model_unavailable":
      return state === "disabled" ? text.chat.voiceUnavailable : errorDetail || text.chat.voiceUnavailable;
    case "speech_timeout":
      return text.chat.voiceTimeout;
    case "speech_inference_failed":
      return errorDetail || text.chat.voiceFailed;
    default:
      return state === "error" ? errorDetail || text.chat.voiceFailed : text.chat.voiceUnavailable;
  }
}

function deliveryValidationMessage(error: string, text: CopyText) {
  switch (error) {
    case "recipient_required":
      return text.chat.recipientRequired;
    case "content_required":
      return text.chat.contentRequired;
    case "part_unsupported":
      return text.chat.partUnsupported;
    case "too_many_parts":
      return text.chat.tooManyParts;
    case "payload_too_large":
      return text.chat.payloadTooLarge;
    default:
      return "";
  }
}
