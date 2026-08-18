import { useState } from "react";
import { Check, Database, Download, MemoryStick, Pencil, Trash2, X } from "lucide-react";
import type { Memory, MemoryCandidate } from "../../api/types";
import type { Copy as CopyText } from "../../i18n";
import { formatState } from "../../lib/format";
import { SectionHeader } from "./primitives";

export function MemoryPanel({
  candidates,
  memories,
  text,
  onResolve,
  onUpdate,
  onDelete,
  onExport
}: {
  candidates: MemoryCandidate[];
  memories: Memory[];
  text: CopyText;
  onResolve: (id: string, accepted: boolean) => void;
  onUpdate: (id: string, kind: string, content: string) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
  onExport: () => Promise<void>;
}) {
  const [editingId, setEditingId] = useState("");
  const [editKind, setEditKind] = useState("");
  const [editContent, setEditContent] = useState("");
  const [savingId, setSavingId] = useState("");
  const [exporting, setExporting] = useState(false);

  function startEdit(memory: Memory) {
    setEditingId(memory.id);
    setEditKind(memory.kind);
    setEditContent(memory.content);
  }

  function cancelEdit() {
    setEditingId("");
    setEditKind("");
    setEditContent("");
  }

  async function saveEdit(memory: Memory) {
    if (!editKind.trim() || !editContent.trim() || savingId) return;
    setSavingId(memory.id);
    try {
      await onUpdate(memory.id, editKind.trim(), editContent.trim());
      cancelEdit();
    } catch {
      return;
    } finally {
      setSavingId("");
    }
  }

  async function removeMemory(memory: Memory) {
    if (savingId) return;
    setSavingId(memory.id);
    try {
      await onDelete(memory.id);
      if (editingId === memory.id) cancelEdit();
    } catch {
      return;
    } finally {
      setSavingId("");
    }
  }

  async function archiveExport() {
    if (exporting) return;
    setExporting(true);
    try {
      await onExport();
    } catch {
      return;
    } finally {
      setExporting(false);
    }
  }

  return (
    <div className="panelStack">
      <SectionHeader icon={<MemoryStick size={17} />} title={text.memory.title} />
      {candidates.length === 0 ? (
        <span className="muted">{text.memory.emptyCandidates}</span>
      ) : (
        candidates.map((candidate) => (
          <article className="approvalItem" key={candidate.id}>
            <div className="approvalTop">
              <strong>{candidate.kind}</strong>
              <span className="pill">{candidate.sensitivity}</span>
            </div>
            <p>{candidate.content}</p>
            {candidate.status === "pending" ? (
              <div className="buttonRow">
                <button className="approve" onClick={() => onResolve(candidate.id, true)} title={text.memory.acceptMemory}>
                  <Check size={16} />
                </button>
                <button className="reject" onClick={() => onResolve(candidate.id, false)} title={text.memory.rejectMemory}>
                  <X size={16} />
                </button>
              </div>
            ) : (
              <span className="resolved">{formatState(candidate.status, text)}</span>
            )}
          </article>
        ))
      )}
      <div className="sectionHeader smallHeader">
        <Database size={15} />
        <h2>{text.memory.accepted}</h2>
        <button className="miniIconButton headerAction" onClick={() => void archiveExport()} disabled={exporting} title={text.memory.archiveExport}>
          <Download size={14} />
        </button>
      </div>
      <dl className="statusGrid compact memoryCounts">
        <dt>{text.memory.accepted}</dt>
        <dd>{memories.length}</dd>
        <dt>{text.memory.pending}</dt>
        <dd>{candidates.filter((candidate) => candidate.status === "pending").length}</dd>
      </dl>
      {memories.map((memory) => (
        <article className="memoryItem" key={memory.id}>
          {editingId === memory.id ? (
            <div className="memoryEdit">
              <input
                aria-label={text.memory.kind}
                value={editKind}
                onChange={(event) => setEditKind(event.target.value)}
                disabled={savingId === memory.id}
              />
              <textarea
                aria-label={text.memory.content}
                value={editContent}
                onChange={(event) => setEditContent(event.target.value)}
                disabled={savingId === memory.id}
              />
              <div className="buttonRow">
                <button
                  className="approve"
                  onClick={() => void saveEdit(memory)}
                  disabled={!editKind.trim() || !editContent.trim() || savingId === memory.id}
                  title={text.memory.saveMemory}
                >
                  <Check size={16} />
                </button>
                <button className="edit" onClick={cancelEdit} disabled={savingId === memory.id} title={text.memory.cancelEdit}>
                  <X size={16} />
                </button>
              </div>
            </div>
          ) : (
            <>
              <div className="approvalTop">
                <strong>{memory.kind}</strong>
                <div className="buttonRow compactButtons">
                  <button className="edit" onClick={() => startEdit(memory)} disabled={savingId === memory.id} title={text.memory.editMemory}>
                    <Pencil size={15} />
                  </button>
                  <button className="reject" onClick={() => void removeMemory(memory)} disabled={savingId === memory.id} title={text.memory.deleteMemory}>
                    <Trash2 size={15} />
                  </button>
                </div>
              </div>
              <p>{memory.content}</p>
            </>
          )}
        </article>
      ))}
    </div>
  );
}
