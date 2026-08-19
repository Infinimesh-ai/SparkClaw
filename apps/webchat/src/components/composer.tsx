// Composer dock: attachment tray, message form with voice input, and the
// workspace document picker overlay. Extracted from App.tsx so the root
// component stays below the size baseline; document-picker and IME
// composition state is local because nothing outside the dock reads it.
// Message sending and the voice hook stay in the parent.
import { useMemo, useRef, useState } from "react";
import type { Dispatch, FormEvent, KeyboardEvent, MutableRefObject, SetStateAction } from "react";
import { FileSearch, Send, Upload, X } from "lucide-react";
import { api, openDocumentFile } from "../api/client";
import type { Copy as CopyText, Language } from "../i18n";
import { isImageAttachment, isImageContentType, WorkspaceFileImage } from "./messages";
import { VoiceInputControl, VoiceInputStatus } from "./VoiceInputButton";
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
import type { ArtifactObject, MessageAttachment } from "../api/types";

type ComposerDockProps = {
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
  onSend
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
  const voiceLabel = voiceInputLabel(voice.state, voice.errorCode, voice.errorDetail, voice.deviceFallback, text);
  const voiceTitle = voiceInputTitle(voice.state, voiceLabel, text);

  function currentVoiceAnchor() {
    const input = composerInputRef.current;
    return {
      sessionId: activeSession,
      draft: activeInput,
      selectionStart: input?.selectionStart ?? activeInput.length,
      selectionEnd: input?.selectionEnd ?? activeInput.length
    };
  }

  function onSubmit(event: FormEvent) {
    event.preventDefault();
    if (isComposingInput || Date.now() - compositionEndedAt < 80) return;
    onSend();
  }

  function onComposerKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key !== "Enter" || event.shiftKey) return;
    if (isComposingInput || event.nativeEvent.isComposing || Date.now() - compositionEndedAt < 80) {
      return;
    }
    event.preventDefault();
    onSend();
  }

  function stageAttachment(attachment: MessageAttachment) {
    if (!activeSession) return;
    setAttachmentsBySession((current) => {
      const existing = current[activeSession] ?? [];
      return {
        ...current,
        [activeSession]: [...existing.filter((item) => (item.artifact_id ?? item.rel_path) !== (attachment.artifact_id ?? attachment.rel_path)), attachment]
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
    setAttachmentsBySession((current) => ({
      ...current,
      [sessionId]: (current[sessionId] ?? []).filter((item) => item !== attachment)
    }));
  }

  return (
    <>
      <div className="composerDock">
        {activeAttachments.length > 0 && (
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
        <form className="composer" onSubmit={onSubmit}>
          <input
            ref={uploadInputRef}
            className="documentUploadInput"
            type="file"
            accept=".txt,.md,.csv,.pdf,.docx,.xlsx,.pptx,.png,.jpg,.jpeg,.gif,.webp,image/png,image/jpeg,image/gif,image/webp"
            onChange={(event) => void uploadDocument(event.target.files?.[0] ?? null)}
          />
          <button
            className="uploadButton"
            type="button"
            disabled={busy || uploadingDocument || !activeSession}
            title={uploadingDocument ? text.chat.uploading : text.chat.upload}
            onClick={() => uploadInputRef.current?.click()}
          >
            <Upload size={18} />
          </button>
          <button
            className="uploadButton"
            type="button"
            disabled={busy || choosingDocument || !activeSession}
            title={choosingDocument ? text.chat.choosingFile : text.chat.chooseFile}
            onClick={() => void openDocumentPicker()}
          >
            <FileSearch size={18} />
          </button>
          <VoiceInputControl
            state={voice.state}
            disabled={voice.disabled}
            active={voice.active}
            title={voiceTitle}
            devices={voice.devices}
            selectedDeviceId={voice.selectedDeviceId}
            silenceMode={voice.silenceMode}
            previewState={voice.previewState}
            previewLevel={voice.previewLevel}
            previewError={voiceCaptureFailureLabel(voice.previewErrorCode, "", text)}
            text={text}
            onClick={() => voice.toggle(currentVoiceAnchor())}
            onRefreshDevices={() => void voice.refreshDevices()}
            onSelectDevice={voice.selectDevice}
            onSelectSilenceMode={voice.setSilenceMode}
            onTogglePreview={voice.togglePreview}
            onClosePicker={voice.stopPreview}
          />
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
            disabled={busy}
          />
          <button
            className="sendButton"
            disabled={busy || voice.active || (!activeInput.trim() && activeAttachments.length === 0)}
            title={text.chat.send}
          >
            <Send size={18} />
          </button>
          <VoiceInputStatus
            state={voice.state}
            level={voice.level}
            elapsedMs={voice.elapsedMs}
            partialText={voice.partialText}
            partialFrozen={voice.partialFrozen}
            label={voiceLabel}
            retryable={voice.retryable}
            pendingInsert={voice.hasPendingTranscript}
            text={text}
            onRetry={voice.retry}
            onInsertPending={() => voice.insertPending(currentVoiceAnchor())}
            onDismiss={() => void voice.cancel()}
          />
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
    </>
  );
}

function voiceInputTitle(state: VoiceInputState, label: string, text: CopyText) {
  if (state === "recording_realtime" || state === "recording_batch_only") return text.chat.voiceStop;
  if (state !== "idle" && state !== "disabled" && state !== "error" && state !== "retryable_error" && state !== "pending_insert") {
    return text.chat.voiceCancel;
  }
  if (state === "disabled") return label;
  return text.chat.voiceStart;
}

function voiceInputLabel(state: VoiceInputState, errorCode: string, errorDetail: string, deviceFallback: boolean, text: CopyText) {
  if (state === "acquiring_microphone") return text.chat.voiceRequesting;
  if (state === "connecting_realtime") return text.chat.voiceConnectingRealtime;
  if (state === "starting_capture") return text.chat.voiceStarting;
  if (state === "starting_batch_capture") return text.chat.voiceStartingBatch;
  if (state === "recording_realtime") return deviceFallback ? `${text.chat.voiceRecordingRealtime} · ${text.chat.voiceFallback}` : text.chat.voiceRecordingRealtime;
  if (state === "recording_batch_only") return deviceFallback ? `${text.chat.voiceRecordingBatch} · ${text.chat.voiceFallback}` : text.chat.voiceRecordingBatch;
  if (state === "finalizing_realtime") return text.chat.voiceFinalizingRealtime;
  if (state === "recovering_batch") return text.chat.voiceRecoveringBatch;
  if (state === "encoding") return text.chat.voicePreparing;
  if (state === "transcribing") return text.chat.voiceTranscribing;
  if (state === "pending_insert") return text.chat.voicePendingInsert;
  if (state === "retryable_error") return voiceCaptureFailureLabel(errorCode, errorDetail, text);
  return voiceCaptureFailureLabel(errorCode, errorDetail, text, state);
}

function voiceCaptureFailureLabel(errorCode: string, errorDetail: string, text: CopyText, state?: VoiceInputState) {
  switch (errorCode) {
    case "voice_capture_unsupported":
      return text.chat.voiceUnsupported;
    case "voice_permission_denied":
      return text.chat.voicePermissionDenied;
    case "voice_no_device":
      return text.chat.voiceNoDevice;
    case "voice_capture_failed":
      return text.chat.voiceCaptureFailed;
    case "voice_capture_start_timeout":
      return text.chat.voiceCaptureStartTimeout;
    case "voice_device_disconnected":
      return text.chat.voiceDeviceDisconnected;
    case "voice_capture_interrupted":
      return text.chat.voiceCaptureInterrupted;
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
    case "speech_stream_overrun":
      return text.chat.voiceStreamOverrun;
    case "speech_stream_protocol_error":
      return text.chat.voiceStreamFailed;
    case "speech_retry_expired":
      return text.chat.voiceRetryExpired;
    case "speech_inference_failed":
      return errorDetail || text.chat.voiceFailed;
    default:
      return state === "error" ? errorDetail || text.chat.voiceFailed : errorDetail;
  }
}
