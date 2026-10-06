// Composer dock: attachment tray, message form with voice input, and the
// workspace document picker overlay. Extracted from App.tsx so the root
// component stays below the size baseline; document-picker and IME
// composition state is local because nothing outside the dock reads it.
// Message sending and the voice hook stay in the parent.
import { useMemo, useRef, useState } from "react";
import type { Dispatch, FormEvent, KeyboardEvent, MutableRefObject, SetStateAction } from "react";
import { ArrowUp, FileSearch, Upload, X } from "lucide-react";
import { api, openDocumentFile } from "../api/client";
import type { Copy as CopyText, Language } from "../i18n";
import { isImageAttachment, isImageContentType, WorkspaceFileImage } from "./messages";
import { VoiceInputControl, VoiceInputStatus } from "./VoiceInputButton";
import type { useVoiceInput } from "../hooks/useVoiceInput";
import { voiceInputLabel } from "../lib/voiceLabels";
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
import { EmailPopupEntry } from "./emailPopup";
import type { VoiceInputModel } from "../hooks/useVoiceInput";

export type ComposerSurfaceProps = {
  text: CopyText;
  language: Language;
  activeSession: string;
  activeInput: string;
  activeAttachments: MessageAttachment[];
  busy: boolean;
  voice: VoiceInputModel;
  composerInputRef: MutableRefObject<HTMLTextAreaElement | null>;
  uploadingDocument?: boolean;
  choosingDocument?: boolean;
  canCompose?: boolean;
  canSend?: boolean;
  onInputChange: (value: string) => void;
  onUploadDocument: (file: File) => void | Promise<unknown>;
  onChooseDocument: () => void | Promise<void>;
  onOpenAttachment: (attachment: MessageAttachment) => void | Promise<void>;
  onRemoveAttachment: (attachment: MessageAttachment) => void;
  onSend: () => void;
};

// The composer chrome is shared by the browser/Linux workbench and the R3
// desktop adapter. Data operations stay injected so a client-local desktop
// conversation never has to masquerade as a gateway session just to retain
// the same presentation.
export function ComposerSurface({
  text,
  language,
  activeSession,
  activeInput,
  activeAttachments,
  busy,
  voice,
  composerInputRef,
  uploadingDocument = false,
  choosingDocument = false,
  canCompose = Boolean(activeSession),
  canSend = canCompose,
  onInputChange,
  onUploadDocument,
  onChooseDocument,
  onOpenAttachment,
  onRemoveAttachment,
  onSend
}: ComposerSurfaceProps) {
  const [isComposingInput, setIsComposingInput] = useState(false);
  const [compositionEndedAt, setCompositionEndedAt] = useState(0);
  const uploadInputRef = useRef<HTMLInputElement | null>(null);
  const voiceLabel = voiceInputLabel(voice.state, voice.errorCode, voice.errorDetail, voice.deviceFallback, text);

  function currentVoiceAnchor() {
    const input = composerInputRef.current;
    return {
      sessionId: activeSession,
      draft: activeInput,
      selectionStart: input?.selectionStart ?? activeInput.length,
      selectionEnd: input?.selectionEnd ?? activeInput.length
    };
  }

  function submit(event: FormEvent) {
    event.preventDefault();
    if (!canSend || isComposingInput || Date.now() - compositionEndedAt < 80) return;
    onSend();
  }

  function keyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key !== "Enter" || event.shiftKey) return;
    if (!canSend || isComposingInput || event.nativeEvent.isComposing || Date.now() - compositionEndedAt < 80) return;
    event.preventDefault();
    onSend();
  }

  return <div className="composerDock">
    {activeAttachments.length > 0 && <div className="attachmentTray">
      {activeAttachments.map((attachment) => <div className="attachmentChip" key={`${attachment.artifact_id ?? attachment.rel_path}-${attachment.rel_path}`}>
        <button type="button" className={`attachmentOpen ${isImageAttachment(attachment) ? "image" : ""}`} title={text.chat.openAttachment} onClick={() => void onOpenAttachment(attachment)}>
          {isImageAttachment(attachment)
            ? <WorkspaceFileImage path={attachment.rel_path} sessionId={activeSession} alt={attachment.name || attachment.rel_path} />
            : <FileSearch size={15} />}
          <span>{attachment.name || attachment.rel_path}</span>
        </button>
        <button type="button" className="attachmentRemove" title={text.chat.removeAttachment} disabled={busy} onClick={() => onRemoveAttachment(attachment)}><X size={14} /></button>
      </div>)}
    </div>}
    <form className="composer" onSubmit={submit}>
      <input ref={uploadInputRef} className="documentUploadInput" type="file"
        accept=".txt,.md,.csv,.pdf,.docx,.xlsx,.pptx,.png,.jpg,.jpeg,.gif,.webp,image/png,image/jpeg,image/gif,image/webp"
        onChange={(event) => {
          const file = event.target.files?.[0];
          if (file) void Promise.resolve(onUploadDocument(file)).finally(() => { if (uploadInputRef.current) uploadInputRef.current.value = ""; });
        }} />
      <button className="uploadButton" type="button" disabled={busy || uploadingDocument || !canCompose}
        title={uploadingDocument ? text.chat.uploading : text.chat.upload} onClick={() => uploadInputRef.current?.click()}><Upload size={18} /></button>
      <button className="uploadButton" type="button" disabled={busy || choosingDocument || !canCompose}
        title={choosingDocument ? text.chat.choosingFile : text.chat.chooseFile} onClick={() => void onChooseDocument()}><FileSearch size={18} /></button>
      <EmailPopupEntry text={text} language={language} />
      <VoiceInputControl voice={voice} text={text} onToggle={() => voice.toggle(currentVoiceAnchor())} />
      <textarea ref={composerInputRef} value={activeInput} onChange={(event) => onInputChange(event.target.value)} onKeyDown={keyDown}
        onCompositionStart={() => setIsComposingInput(true)} onCompositionEnd={() => { setIsComposingInput(false); setCompositionEndedAt(Date.now()); }}
        placeholder={text.chat.placeholder} aria-label={text.chat.placeholder} disabled={busy || !canCompose} />
      <button className="sendButton" disabled={busy || !canSend || voice.active || (!activeInput.trim() && activeAttachments.length === 0)} title={text.chat.send}><ArrowUp size={18} /></button>
      <VoiceInputStatus state={voice.state} level={voice.level} elapsedMs={voice.elapsedMs} partialText={voice.partialText}
        partialFrozen={voice.partialFrozen} label={voiceLabel} retryable={voice.retryable} pendingInsert={voice.hasPendingTranscript}
        text={text} onRetry={voice.retry} onInsertPending={() => voice.insertPending(currentVoiceAnchor())} onDismiss={() => void voice.cancel()} />
    </form>
  </div>;
}

export function ComposerDocumentPicker({
  documents,
  text,
  language,
  onChoose,
  onClose
}: {
  documents: ArtifactObject[];
  text: CopyText;
  language: Language;
  onChoose: (document: ArtifactObject) => void;
  onClose: () => void;
}) {
  const [documentUsage, setDocumentUsage] = useState<Record<string, DocumentUsage>>(() => loadDocumentUsage());
  const sortedDocuments = useMemo(() => sortDocumentsByUsage(documents, documentUsage), [documents, documentUsage]);

  function choose(document: ArtifactObject) {
    setDocumentUsage((current) => {
      const previous = current[document.key] ?? { count: 0, last_used_at: "" };
      const next = { ...current, [document.key]: { count: previous.count + 1, last_used_at: new Date().toISOString() } };
      saveDocumentUsage(next);
      return next;
    });
    onChoose(document);
  }

  return <div className="documentPickerOverlay" role="dialog" aria-modal="true" aria-label={text.chat.chooseFile}>
    <div className="documentPicker">
      <div className="documentPickerHeader">
        <strong>{text.chat.chooseFile}</strong>
        <button type="button" className="attachmentRemove" onClick={onClose} title={text.common.cancel}><X size={14} /></button>
      </div>
      {sortedDocuments.length === 0 ? <span className="muted">{text.chat.noUploadedFiles}</span> : <div className="documentPickerList">
        <div className="finderHeader">
          <span>{text.chat.fileName}</span><span>{text.chat.fileUsage}</span><span>{text.chat.fileRecentUse}</span>
          <span>{text.chat.fileSize}</span><span>{text.chat.fileKind}</span>
        </div>
        {sortedDocuments.map((document) => {
          const usage = documentUsage[document.key];
          return <button className="finderRow file" key={document.id} type="button" onClick={() => choose(document)}>
            <span className="finderName fileName"><FileSearch size={16} /><strong>{fileNameFromPath(document.key)}</strong></span>
            <span>{usage ? `${usage.count} ${text.chat.usedTimes}` : text.chat.neverUsed}</span>
            <span>{usage ? formatDateTime(usage.last_used_at, language) : "--"}</span>
            <span>{formatBytes(document.bytes)}</span><span>{fileKindLabel(document)}</span>
          </button>;
        })}
      </div>}
    </div>
  </div>;
}

type ComposerDockProps = {
  text: CopyText;
  language: Language;
  activeSession: string;
  activeInput: string;
  activeAttachments: MessageAttachment[];
  busy: boolean;
  canCompose?: boolean;
  canSend?: boolean;
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
  canCompose = Boolean(activeSession),
  canSend = canCompose,
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
  const [uploadingDocument, setUploadingDocument] = useState(false);
  function stageAttachment(attachment: MessageAttachment) {
    setAttachmentsBySession((current) => {
      const existing = current[activeSession] ?? [];
      return {
        ...current,
        [activeSession]: [...existing.filter((item) => (item.artifact_id ?? item.rel_path) !== (attachment.artifact_id ?? attachment.rel_path)), attachment]
      };
    });
  }

  async function uploadDocument(file: File | null) {
    if (!file || uploadingDocument) return;
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
    }
  }

  async function openDocumentPicker() {
    if (choosingDocument) return;
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
    const attachment: MessageAttachment = {
      artifact_id: document.id,
      name: fileNameFromPath(document.key),
      rel_path: document.key,
      uri: document.uri,
      content_type: document.content_type,
      bytes: document.bytes
    };
    stageAttachment(attachment);
    setDocumentPickerOpen(false);
  }

  function removeAttachment(sessionId: string, attachment: MessageAttachment) {
    setAttachmentsBySession((current) => ({
      ...current,
      [sessionId]: (current[sessionId] ?? []).filter((item) => item !== attachment)
    }));
  }

  return (
    <>
      <ComposerSurface text={text} language={language} activeSession={activeSession} activeInput={activeInput}
        activeAttachments={activeAttachments} busy={busy} voice={voice} composerInputRef={composerInputRef} canCompose={canCompose}
        canSend={canSend && !uploadingDocument && !choosingDocument && !documentPickerOpen}
        uploadingDocument={uploadingDocument} choosingDocument={choosingDocument}
        onInputChange={(value) => setDraftsBySession((current) => ({ ...current, [activeSession]: value }))}
        onUploadDocument={(file) => uploadDocument(file)} onChooseDocument={openDocumentPicker}
        onOpenAttachment={(attachment) => openDocumentFile(attachment.rel_path, activeSession).catch(() => undefined)}
        onRemoveAttachment={(attachment) => removeAttachment(activeSession, attachment)} onSend={onSend} />
      {documentPickerOpen && <ComposerDocumentPicker documents={availableDocuments} text={text} language={language}
        onChoose={chooseAvailableDocument} onClose={() => setDocumentPickerOpen(false)} />}
    </>
  );
}
