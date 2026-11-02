// Add and edit dialog. Adding takes two steps: a photo of the package front,
// one typed sentence, or nothing in step 1, then one shared review form in
// step 2. Nothing the backend read is saved until the owner confirms every
// field; the quantity left and the expiry date are never read from a photo,
// and notes are never read from text.
import { useEffect, useRef, useState, type ClipboardEvent, type DragEvent, type FormEvent, type KeyboardEvent } from "react";
import { ImageUp, X } from "lucide-react";
import type { Language } from "../../i18n";
import { api as backend } from "../../api/client";
import { useDismissibleLayer } from "../../hooks/useDismissibleLayer";
import { ActionIcon } from "../ActionIcon";
import { MEDICINE_CATEGORIES, MEDICINE_QUANTITY_MAX, MEDICINE_UNITS, medicineBox, medicineGroupKey, monthEnd, suggestMedicineCategory,
  type MedicineCategory, type MedicineItem, type MedicineUnit } from "../../desktop/medicineBox";
import { prepareMedicinePhoto } from "../../desktop/medicinePhoto";
import { fill, medicineBoxCopy } from "./copy";

type Draft = {
  name: string; category: MedicineCategory | ""; quantity: string; unit: MedicineUnit;
  expires_on: string; expires_month: string; month_only: boolean; no_expiry: boolean;
  location: string; usage: string; notes: string;
};
type Prefilled = "name" | "category" | "unit" | "quantity" | "expiry" | "location" | "usage";
const blankDraft = (category: MedicineCategory | ""): Draft => ({ name: "", category, quantity: "", unit: "tablet", expires_on: "", expires_month: "", month_only: false,
  no_expiry: false, location: "", usage: "", notes: "" });
const itemDraft = (item: MedicineItem): Draft => ({ name: item.name, category: item.category, quantity: String(item.quantity), unit: item.unit,
  expires_on: item.expires_on, expires_month: "", month_only: false, no_expiry: item.no_expiry, location: item.location, usage: item.usage, notes: item.notes });
const draftExpiry = (draft: Draft) => draft.no_expiry ? "" : draft.month_only ? monthEnd(draft.expires_month) : /^\d{4}-\d{2}-\d{2}$/u.test(draft.expires_on) ? draft.expires_on : "";
type Status = { kind: "idle" | "busy" | "problem"; text: string };
const localToday = (now = new Date()) => `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;

export function MedicineEntryDialog({ language, item, items, defaultCategory, recognitionAvailable, parseAvailable, onClose, onSaved }: {
  language: Language; item?: MedicineItem; items: MedicineItem[]; defaultCategory: MedicineCategory | ""; recognitionAvailable: boolean; parseAvailable: boolean;
  onClose: () => void; onSaved: (item: MedicineItem) => void;
}) {
  const copy = medicineBoxCopy[language];
  const api = medicineBox()!;
  // With no way to read a medicine in, adding starts at the review form.
  const [step, setStep] = useState<"entry" | "review">(item || !recognitionAvailable && !parseAvailable ? "review" : "entry");
  const [mode, setMode] = useState<"photo" | "text">(!recognitionAvailable && parseAvailable ? "text" : "photo");
  const [text, setText] = useState("");
  // Which entry filled the review: its photo or text is shown there for checking.
  const [source, setSource] = useState<{ kind: "photo" | "text"; text: string } | null>(null);
  const [draft, setDraft] = useState<Draft>(item ? itemDraft(item) : blankDraft(defaultCategory));
  const [prefilled, setPrefilled] = useState<Set<Prefilled>>(new Set());
  // A category the owner or the model chose is kept; otherwise typing a name guesses one.
  const [categoryChosen, setCategoryChosen] = useState(Boolean(item || defaultCategory));
  const [photo, setPhoto] = useState("");
  const [photoLarge, setPhotoLarge] = useState(false);
  const [status, setStatus] = useState<Status>({ kind: "idle", text: "" });
  const [saving, setSaving] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const formRef = useRef<HTMLFormElement>(null);
  const reading = useRef<AbortController | null>(null);
  const busy = status.kind === "busy" || saving;
  const dialogRef = useDismissibleLayer<HTMLDivElement>({ open: true, onDismiss: onClose, modal: true, dismissible: !saving,
    initialFocus: step === "entry" ? mode === "text" ? "textarea[name=description]" : ".medicinePhotoButton" : undefined });
  useEffect(() => () => { if (photo) URL.revokeObjectURL(photo); }, [photo]);
  useEffect(() => () => reading.current?.abort(), []);
  // Entering the review lands on the first required field still empty, the
  // quantity when editing, and the name when everything is filled in.
  useEffect(() => {
    if (step !== "review") return;
    const form = formRef.current;
    const empty = item ? "quantity" : !draft.name.trim() ? "name" : !draft.category ? "category" : draft.quantity.trim() === "" ? "quantity" : !draftExpiry(draft) ? (draft.month_only ? "expires_month" : "expires_on") : "name";
    form?.querySelector<HTMLElement>(`[name="${empty}"]`)?.focus({ preventScroll: true });
    // Only on arriving at the review, not while the owner types.
  }, [step]);

  const recognize = async (file: File | undefined) => {
    if (!file || busy || !recognitionAvailable) return;
    setPhoto((current) => { if (current) URL.revokeObjectURL(current); return URL.createObjectURL(file); });
    setStatus({ kind: "busy", text: copy.recognizing });
    let prepared;
    try { prepared = await prepareMedicinePhoto(file); }
    catch { setStatus({ kind: "problem", text: copy.photoUnreadable }); return; }
    reading.current?.abort();
    const controller = new AbortController();
    reading.current = controller;
    try {
      const result = await backend.recognizeMedicine(prepared, controller.signal);
      if (controller.signal.aborted) return;
      if (!result.recognized) { setStatus({ kind: "problem", text: copy.notRecognized }); return; }
      // Only what the package front shows: never the quantity or the expiry.
      const usage = typeof result.usage === "string" ? result.usage.trim() : "";
      setDraft((current) => ({ ...current, name: result.name, category: result.category, unit: result.unit, ...(usage ? { usage } : {}) }));
      setPrefilled(new Set<Prefilled>(["name", "category", "unit", ...(usage ? ["usage" as const] : [])]));
      setCategoryChosen(true);
      setSource({ kind: "photo", text: "" });
      setStatus({ kind: "idle", text: "" });
      setStep("review");
    } catch {
      if (!controller.signal.aborted) setStatus({ kind: "problem", text: copy.recognitionUnavailable });
    }
  };
  const parse = async () => {
    const sentence = text.replace(/\s+/gu, " ").trim();
    if (!sentence || busy || !parseAvailable) return;
    setStatus({ kind: "busy", text: copy.parsing });
    reading.current?.abort();
    const controller = new AbortController();
    reading.current = controller;
    try {
      const result = await backend.parseMedicine({ text: sentence, today: localToday() }, controller.signal);
      if (controller.signal.aborted) return;
      if (result.multiple) { setStatus({ kind: "problem", text: copy.textMultiple }); return; }
      if (!result.recognized) { setStatus({ kind: "problem", text: copy.textNotRecognized }); return; }
      // Only what the sentence states; "no expiry" is always the owner's tick.
      const stated: Partial<Draft> = { name: result.name, category: result.category, unit: result.unit };
      const filled: Prefilled[] = ["name", "category", "unit"];
      if (typeof result.quantity === "number") { stated.quantity = String(result.quantity); filled.push("quantity"); }
      if (result.expires_on) { Object.assign(stated, { expires_on: result.expires_on, month_only: false, no_expiry: false }); filled.push("expiry"); }
      if (result.location.trim()) { stated.location = result.location.trim(); filled.push("location"); }
      if (result.usage.trim()) { stated.usage = result.usage.trim(); filled.push("usage"); }
      setDraft((current) => ({ ...current, ...stated }));
      setPrefilled(new Set(filled));
      setCategoryChosen(true);
      setSource({ kind: "text", text: sentence });
      setStatus({ kind: "idle", text: "" });
      setStep("review");
    } catch {
      if (!controller.signal.aborted) setStatus({ kind: "problem", text: copy.parseUnavailable });
    }
  };
  const onTextKey = (event: KeyboardEvent) => {
    if (event.key === "Enter" && (event.metaKey || event.ctrlKey) && !event.nativeEvent.isComposing) { event.preventDefault(); void parse(); }
  };

  const quantity = Number(draft.quantity);
  const expiry = draftExpiry(draft);
  const valid = Boolean(draft.name.trim() && draft.category && draft.quantity.trim() !== "" && Number.isFinite(quantity) && quantity >= 0 && quantity <= MEDICINE_QUANTITY_MAX
    && (draft.no_expiry || expiry));
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!valid || busy || !draft.category) return;
    setSaving(true);
    try {
      onSaved(await api.save({ id: item?.id ?? "", name: draft.name.trim(), category: draft.category, quantity: Math.round(quantity * 100) / 100, unit: draft.unit,
        expires_on: expiry, no_expiry: draft.no_expiry, location: draft.location.trim(), usage: draft.usage.trim(), notes: draft.notes.trim() }));
    } catch { setStatus({ kind: "problem", text: copy.saveFailed }); }
    finally { setSaving(false); }
  };
  // Editing a pre-filled field means the owner has checked it.
  const update = (patch: Partial<Draft>, checked: Prefilled) => {
    setDraft((current) => ({ ...current, ...patch }));
    setPrefilled((current) => { if (!current.has(checked)) return current; const next = new Set(current); next.delete(checked); return next; });
  };
  const onDrop = (event: DragEvent) => { event.preventDefault(); void recognize(event.dataTransfer.files[0]); };
  const onPaste = (event: ClipboardEvent) => {
    const file = [...event.clipboardData.files].find((candidate) => candidate.type.startsWith("image/"));
    if (file) { event.preventDefault(); void recognize(file); }
  };
  const goToReview = () => { reading.current?.abort(); setStatus({ kind: "idle", text: "" }); setStep("review"); };

  // Same-name entries stay separate; adding one only gets a notice.
  const sameName = item ? [] : items.filter((other) => draft.name.trim() && medicineGroupKey(other.name) === medicineGroupKey(draft.name));
  const sameExpiry = Boolean(expiry) && sameName.some((other) => other.expires_on === expiry);
  const fieldLabel = (key: Prefilled, label: string) => <span>{label}{prefilled.has(key) && <em className="medicineCheck">{copy.needsReview}</em>}</span>;
  const flash = (key: Prefilled) => prefilled.has(key) ? "medicineFieldFlash" : undefined;

  return <div ref={dialogRef} tabIndex={-1} className="documentPickerOverlay" role="dialog" aria-modal="true" aria-labelledby="medicine-dialog-title"
    onMouseDown={(event) => { if (event.target === event.currentTarget && !saving) onClose(); }} onPaste={!item && step === "entry" && mode === "photo" ? onPaste : undefined}>
    <form ref={formRef} className="scheduleDialog medicineDialog" onSubmit={(event) => void submit(event)} noValidate>
      <div className="documentPickerHeader scheduleCreateDialogHeader">
        <div className="scheduleCreateIntro">
          <ActionIcon name="medicine" />
          <div><h2 id="medicine-dialog-title">{item ? copy.editTitle : copy.addTitle}</h2>{!item && step === "entry" && <p>{mode === "text" ? copy.textIntro : copy.photoIntro}</p>}</div>
        </div>
        <button className="attachmentRemove" type="button" onClick={onClose} disabled={saving} title={copy.close} aria-label={copy.close}><X size={16} strokeWidth={1.75} /></button>
      </div>
      {!item && <ol className="medicineSteps" aria-label={copy.steps}>
        <li><button type="button" aria-current={step === "entry" ? "step" : undefined} disabled={busy} onClick={() => setStep("entry")}><b>1</b>{copy.stepEntry}</button></li>
        <li><button type="button" aria-current={step === "review" ? "step" : undefined} disabled={busy} onClick={goToReview}><b>2</b>{copy.stepReview}</button></li>
      </ol>}

      {step === "entry" && <>
        <div className="medicineSegments medicineEntryMethods" role="group" aria-label={copy.entryMethod}>
          <button type="button" aria-pressed={mode === "photo"} disabled={busy} onClick={() => { setMode("photo"); setStatus({ kind: "idle", text: "" }); }}>{copy.entryPhoto}</button>
          <button type="button" aria-pressed={mode === "text"} disabled={busy || !parseAvailable} title={parseAvailable ? undefined : copy.parseUnavailable}
            onClick={() => { setMode("text"); setStatus({ kind: "idle", text: "" }); }}>{copy.entryText}</button>
        </div>
        {mode === "text" ? <>
          <label className="medicineTextEntry"><span>{copy.textLabel}</span>
            <textarea name="description" value={text} maxLength={500} rows={4} placeholder={copy.textPlaceholder} disabled={busy}
              onChange={(event) => setText(event.target.value)} onKeyDown={onTextKey} /></label>
          <div className="medicineTextActions">
            <p className="medicinePrivacy">{copy.textPrivacy}</p>
            <button className="primaryButton" type="button" disabled={busy || !text.trim()} onClick={() => void parse()}>{status.kind === "busy" ? copy.parsing : copy.parse}</button>
          </div>
        </> : <>
        <div className={`medicinePhotoDrop ${photo ? "hasPhoto" : ""}`} onDragOver={(event) => event.preventDefault()} onDrop={onDrop}>
          {photo && <img src={photo} alt="" />}
          <div>
            <button className="secondaryButton medicinePhotoButton" type="button" disabled={busy || !recognitionAvailable}
              title={recognitionAvailable ? undefined : copy.recognitionUnavailable} onClick={() => fileInput.current?.click()}>
              <ImageUp size={16} strokeWidth={1.75} aria-hidden="true" /><span>{photo ? copy.changePhoto : copy.choosePhoto}</span>
            </button>
            <small>{recognitionAvailable ? copy.dropHint : copy.recognitionUnavailable}</small>
          </div>
          <input ref={fileInput} type="file" accept="image/*" hidden aria-label={copy.choosePhoto}
            onChange={(event) => { const file = event.target.files?.[0]; event.target.value = ""; void recognize(file); }} />
        </div>
        <p className="medicinePrivacy">{copy.photoPrivacy}</p>
        </>}
        {status.text && <p className={`medicineStatus ${status.kind}`} role={status.kind === "problem" ? "alert" : "status"}>{status.text}</p>}
        <div className="scheduleDialogActions">
          <button className="secondaryButton" type="button" onClick={onClose} disabled={saving}>{copy.cancel}</button>
          <button className="secondaryButton medicineSkip" type="button" onClick={goToReview}>{copy.skipPhoto}</button>
        </div>
      </>}

      {step === "review" && <>
        {!item && source?.kind === "photo" && photo && <button type="button" className={`medicineSourcePhoto ${photoLarge ? "isLarge" : ""}`} aria-expanded={photoLarge} aria-label={copy.photoPreview}
          onClick={() => setPhotoLarge((large) => !large)}><img src={photo} alt={copy.photoPreview} /></button>}
        {!item && source?.kind === "text" && <details className="medicineSourceText" open><summary>{copy.originalText}</summary><p>{source.text}</p></details>}
        {prefilled.size > 0 && <p className="medicineReviewHint">{copy.reviewHint}</p>}
        {sameName.length > 0 && <p className="medicineSameNameHint" role="status">
          {fill(copy.sameNameHint, { count: sameName.length })}{sameExpiry && ` ${copy.sameExpiryHint}`}
        </p>}
        <fieldset className="medicineFieldset">
          <legend>{copy.keyInfo}</legend>
          <div className="scheduleDialogGrid medicineDialogGrid">
            <label className={`medicineFieldWide ${flash("name") ?? ""}`}>{fieldLabel("name", copy.name)}
              <input name="name" value={draft.name} maxLength={80} required placeholder={copy.namePlaceholder} disabled={saving}
                onChange={(event) => { const name = event.target.value; update({ name, ...(categoryChosen ? {} : { category: suggestMedicineCategory(name) }) }, "name"); }} /></label>
            <label className={flash("category")}>{fieldLabel("category", copy.category)}
              <select name="category" value={draft.category} required disabled={saving} onChange={(event) => { setCategoryChosen(true); update({ category: event.target.value as MedicineCategory }, "category"); }}>
                {!draft.category && <option value="" disabled>—</option>}
                {MEDICINE_CATEGORIES.map((category) => <option key={category} value={category}>{copy.categories[category]}</option>)}
              </select></label>
            <label className={flash("quantity") ?? flash("unit")}><span>{copy.quantity}{prefilled.has("quantity") ? <em className="medicineCheck">{copy.needsReview}</em>
              : prefilled.has("unit") && <em className="medicineCheck">{copy.unitNeedsReview}</em>}</span>
              <div className="medicineQuantityInput">
                <input name="quantity" type="number" min={0} max={MEDICINE_QUANTITY_MAX} step="any" required value={draft.quantity} disabled={saving}
                  onChange={(event) => update({ quantity: event.target.value }, "quantity")} />
                <select name="unit" aria-label={copy.unit} value={draft.unit} disabled={saving} onChange={(event) => update({ unit: event.target.value as MedicineUnit }, "unit")}>
                  {MEDICINE_UNITS.map((unit) => <option key={unit} value={unit}>{copy.units[unit]}</option>)}
                </select>
              </div></label>
            <div className={`medicineFieldWide medicineExpiryField ${flash("expiry") ?? ""}`}>
              <label htmlFor="medicine-expiry">{fieldLabel("expiry", copy.expiry)}</label>
              <div className="medicineExpiryInputs">
                {draft.month_only
                  ? <input id="medicine-expiry" name="expires_month" type="month" value={draft.expires_month} disabled={saving || draft.no_expiry} required={!draft.no_expiry}
                    onChange={(event) => update({ expires_month: event.target.value }, "expiry")} />
                  : <input id="medicine-expiry" name="expires_on" type="date" value={draft.expires_on} disabled={saving || draft.no_expiry} required={!draft.no_expiry}
                    onChange={(event) => update({ expires_on: event.target.value }, "expiry")} />}
                <label className="medicineToggle"><input type="checkbox" name="month_only" checked={draft.month_only} disabled={saving || draft.no_expiry}
                  onChange={(event) => update({ month_only: event.target.checked, expires_month: event.target.checked ? draft.expires_on.slice(0, 7) : draft.expires_month,
                    expires_on: event.target.checked ? draft.expires_on : monthEnd(draft.expires_month) || draft.expires_on }, "expiry")} />{copy.monthOnly}</label>
                <label className="medicineToggle"><input type="checkbox" name="no_expiry" checked={draft.no_expiry} disabled={saving}
                  onChange={(event) => update({ no_expiry: event.target.checked, ...(event.target.checked ? { expires_on: "", expires_month: "" } : {}) }, "expiry")} />{copy.withoutExpiry}</label>
              </div>
            </div>
          </div>
        </fieldset>
        <fieldset className="medicineFieldset">
          <legend>{copy.moreInfo}</legend>
          <div className="scheduleDialogGrid medicineDialogGrid">
            <label className={flash("location")}>{fieldLabel("location", copy.location)}
              <input name="location" value={draft.location} maxLength={80} placeholder={copy.locationPlaceholder} disabled={saving} onChange={(event) => update({ location: event.target.value }, "location")} /></label>
            <label className={flash("usage")}>{fieldLabel("usage", copy.usage)}
              <input name="usage" value={draft.usage} maxLength={80} placeholder={copy.usagePlaceholder} disabled={saving} onChange={(event) => update({ usage: event.target.value }, "usage")} />
              {source?.kind === "photo" && prefilled.has("usage") && <small className="medicineFieldHint">{copy.usageFromModel}</small>}</label>
            <label className="medicineFieldWide"><span>{copy.notes}</span>
              <textarea name="notes" value={draft.notes} maxLength={500} rows={3} disabled={saving} onChange={(event) => setDraft((current) => ({ ...current, notes: event.target.value }))} /></label>
          </div>
        </fieldset>
        {status.text && <p className={`medicineStatus ${status.kind}`} role={status.kind === "problem" ? "alert" : "status"}>{status.text}</p>}
        <div className="scheduleDialogActions">
          {!item && <button className="secondaryButton medicineBack" type="button" onClick={() => setStep("entry")} disabled={saving}>{copy.back}</button>}
          <button className="secondaryButton" type="button" onClick={onClose} disabled={saving}>{copy.cancel}</button>
          <button className="primaryButton" type="submit" disabled={busy || !valid}>{saving ? copy.saving : item ? copy.saveChanges : copy.confirmSave}</button>
        </div>
      </>}
    </form>
  </div>;
}
