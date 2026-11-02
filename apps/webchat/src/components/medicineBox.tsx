// Medicine box module: its own sidebar beside the module rail and its own
// workspace page. Medicines are added from a photo of the package front, which
// the backend's vision model reads and sorts by what the medicine treats; the
// owner confirms the result and enters the remaining quantity.
import { useCallback, useEffect, useRef, useState, type ClipboardEvent, type DragEvent, type FormEvent } from "react";
import { ImageUp, Pencil, Plus, Trash2, X } from "lucide-react";
import { dictionaries, type Language } from "../i18n";
import { api as backend } from "../api/client";
import { useDismissibleLayer } from "../hooks/useDismissibleLayer";
import { useConfirm } from "./confirmation";
import { ActionIcon } from "./ActionIcon";
import { MEDICINE_CATEGORIES, MEDICINE_UNITS, medicineBox, medicineDaysLeft, suggestMedicineCategory,
  type MedicineCategory, type MedicineItem, type MedicineUnit } from "../desktop/medicineBox";
import { prepareMedicinePhoto } from "../desktop/medicinePhoto";

const zh = {
  title: "药箱",
  description: "家中常备药品，按功能分类管理。",
  add: "添加药品",
  unavailable: "药箱暂不可用",
  all: "全部药品",
  byFunction: "按功能分类",
  categoryList: "功能分类",
  list: "药品",
  empty: "药箱里还没有药品",
  emptyDescription: "上传药盒正面照片，自动识别药名并分类。",
  emptyCategory: "这个分类下还没有药品",
  loadFailed: "药箱暂时无法读取。",
  retry: "重试",
  remaining: "剩余",
  expired: "已过期",
  expiresIn: "{days} 天后过期",
  expiresOn: "有效期至 {date}",
  edit: "编辑",
  remove: "删除",
  confirmRemove: "从药箱中删除“{name}”？",
  addTitle: "添加药品",
  editTitle: "编辑药品",
  photoIntro: "上传药盒正面照片，自动识别药名并分类。",
  choosePhoto: "选择照片",
  changePhoto: "重新选择",
  dropHint: "也可以把照片拖到这里或直接粘贴",
  recognizing: "正在识别药品…",
  recognized: "已识别为“{name}”，归入{category}。请核对并填写剩余数量。",
  notRecognized: "没能从照片中认出药品。请换一张清晰的包装正面照片，或直接填写。",
  recognitionUnavailable: "识别服务暂不可用，可以直接填写。",
  photoUnreadable: "无法读取这张图片，请换一张 JPG 或 PNG 照片。",
  name: "药品名称",
  namePlaceholder: "如 布洛芬缓释胶囊",
  spec: "规格",
  specPlaceholder: "如 0.3g×20粒",
  manufacturer: "生产企业",
  category: "功能分类",
  quantity: "剩余数量",
  unit: "单位",
  expiry: "有效期",
  save: "入库",
  saveChanges: "保存",
  saving: "正在保存…",
  cancel: "取消",
  close: "关闭",
  saveFailed: "保存失败，请稍后重试。",
  categories: { cold: "感冒", fever: "发烧", pain: "止痛", cough: "咳嗽", digestive: "肠胃", allergy: "过敏", wound: "外伤", other: "其他" },
  units: { tablet: "片", capsule: "粒", sachet: "袋", bottle: "瓶", tube: "支", patch: "贴", box: "盒", ml: "毫升", g: "克" },
};

const en: typeof zh = {
  title: "Medicine box",
  description: "Keep track of the medicines you have at home, sorted by what they treat.",
  add: "Add medicine",
  unavailable: "Medicine box unavailable",
  all: "All medicines",
  byFunction: "By function",
  categoryList: "Categories",
  list: "Medicines",
  empty: "No medicines yet",
  emptyDescription: "Upload a photo of the front of a box to recognize and sort it.",
  emptyCategory: "No medicines in this category",
  loadFailed: "The medicine box could not be read.",
  retry: "Retry",
  remaining: "Left",
  expired: "Expired",
  expiresIn: "Expires in {days} days",
  expiresOn: "Expires {date}",
  edit: "Edit",
  remove: "Delete",
  confirmRemove: "Delete “{name}” from the medicine box?",
  addTitle: "Add medicine",
  editTitle: "Edit medicine",
  photoIntro: "Upload a photo of the front of the box to recognize and sort it.",
  choosePhoto: "Choose photo",
  changePhoto: "Choose another",
  dropHint: "You can also drop or paste a photo here",
  recognizing: "Recognizing the medicine…",
  recognized: "Recognized as “{name}” under {category}. Check it and enter what is left.",
  notRecognized: "No medicine was recognized. Try a clear photo of the package front, or fill it in.",
  recognitionUnavailable: "Recognition is unavailable right now. You can fill it in.",
  photoUnreadable: "This image could not be read. Try a JPG or PNG photo.",
  name: "Name",
  namePlaceholder: "e.g. Ibuprofen capsules",
  spec: "Pack size",
  specPlaceholder: "e.g. 0.3 g × 20",
  manufacturer: "Manufacturer",
  category: "Category",
  quantity: "Quantity left",
  unit: "Unit",
  expiry: "Expiry date",
  save: "Add",
  saveChanges: "Save",
  saving: "Saving…",
  cancel: "Cancel",
  close: "Close",
  saveFailed: "Could not save. Try again later.",
  categories: { cold: "Cold", fever: "Fever", pain: "Pain", cough: "Cough", digestive: "Digestive", allergy: "Allergy", wound: "Wounds", other: "Other" },
  units: { tablet: "tablets", capsule: "capsules", sachet: "sachets", bottle: "bottles", tube: "tubes", patch: "patches", box: "boxes", ml: "ml", g: "g" },
};

export const medicineBoxCopy = { zh, en } satisfies Record<Language, typeof zh>;
type MedicineCopy = typeof zh;
type CategoryFilter = MedicineCategory | "all";

export function useMedicineBox(active: boolean, recognitionAvailable: boolean) {
  const api = medicineBox();
  const [items, setItems] = useState<MedicineItem[]>([]);
  const [failed, setFailed] = useState(false);
  const [category, setCategory] = useState<CategoryFilter>("all");
  const [editing, setEditing] = useState<{ item?: MedicineItem } | null>(null);
  const reload = useCallback(async () => {
    if (!api) return;
    try { setItems((await api.list()).items); setFailed(false); }
    catch { setFailed(true); }
  }, [api]);
  useEffect(() => { if (active) void reload(); }, [active, reload]);
  return { api, items, failed, category, setCategory, editing, setEditing, reload, recognitionAvailable };
}
export type MedicineBoxState = ReturnType<typeof useMedicineBox>;

const countIn = (items: MedicineItem[], category: CategoryFilter) => category === "all" ? items.length : items.filter((item) => item.category === category).length;

export function MedicineSidebar({ language, box }: { language: Language; box: MedicineBoxState }) {
  const copy = medicineBoxCopy[language];
  return <aside className="sidebar medicineSidebar">
    <div className="brandRow"><h2 className="medicineSidebarTitle"><ActionIcon name="medicine" /><span>{copy.title}</span></h2></div>
    <button className="sidebarNewConversation" type="button" disabled={!box.api} title={box.api ? undefined : copy.unavailable} onClick={() => box.setEditing({})}>
      <Plus size={18} strokeWidth={1.75} aria-hidden="true" />
      <span>{copy.add}</span>
    </button>
    <div className="conversationListHeader"><span>{copy.byFunction}</span></div>
    <nav className="sessionList medicineCategoryList" aria-label={copy.categoryList}>
      {(["all", ...MEDICINE_CATEGORIES] as CategoryFilter[]).map((category) => {
        const count = countIn(box.items, category);
        return <button key={category} type="button" className={`medicineCategory ${box.category === category ? "active" : ""} ${count ? "" : "isEmpty"}`}
          aria-current={box.category === category ? "page" : undefined} onClick={() => box.setCategory(category)}>
          <span>{category === "all" ? copy.all : copy.categories[category]}</span>
          <small>{count}</small>
        </button>;
      })}
    </nav>
  </aside>;
}

function expiryLabel(copy: MedicineCopy, expiresOn: string) {
  const days = medicineDaysLeft(expiresOn);
  if (days === null) return null;
  if (days < 0) return { text: copy.expired, state: "expired" };
  if (days <= 30) return { text: copy.expiresIn.replace("{days}", String(days)), state: "soon" };
  return { text: copy.expiresOn.replace("{date}", expiresOn), state: "" };
}

export function MedicineWorkspace({ language, box }: { language: Language; box: MedicineBoxState }) {
  const copy = medicineBoxCopy[language];
  const confirm = useConfirm(dictionaries[language]);
  const visible = box.category === "all" ? box.items : box.items.filter((item) => item.category === box.category);
  const remove = async (item: MedicineItem) => {
    if (!box.api || !await confirm(copy.confirmRemove.replace("{name}", item.name), { danger: true })) return;
    try { await box.api.remove(item.id); } finally { await box.reload(); }
  };
  return <div className="workbenchPage medicinePage">
    <div className="workbenchPageHeader">
      <div><h1>{box.category === "all" ? copy.title : copy.categories[box.category]}</h1><p>{copy.description}</p></div>
      <button className="primaryButton" type="button" disabled={!box.api} title={box.api ? undefined : copy.unavailable} onClick={() => box.setEditing({})}><Plus size={15} />{copy.add}</button>
    </div>
    {box.failed && <div className="localFeedback" role="alert"><p>{copy.loadFailed}</p><button type="button" onClick={() => void box.reload()}>{copy.retry}</button></div>}
    {visible.length ? <ul className="medicineList" aria-label={copy.list}>
      {visible.map((item) => {
        const expiry = expiryLabel(copy, item.expires_on);
        return <li className="medicineRow" key={item.id}>
          <div className="medicineRowName">
            <strong>{item.name}</strong>
            <span>{[item.spec, box.category === "all" ? copy.categories[item.category] : ""].filter(Boolean).join(" · ")}</span>
          </div>
          <div className="medicineRowQuantity"><span>{copy.remaining}</span><b>{item.quantity}</b><span>{copy.units[item.unit]}</span></div>
          <div className="medicineRowMeta">
            {expiry && <span className={`medicineExpiry ${expiry.state}`}>{expiry.text}</span>}
            {item.manufacturer && <small>{item.manufacturer}</small>}
          </div>
          <div className="medicineRowActions">
            <button className="iconButton" type="button" title={copy.edit} aria-label={`${copy.edit} ${item.name}`} onClick={() => box.setEditing({ item })}><Pencil size={15} strokeWidth={1.75} /></button>
            <button className="iconButton" type="button" title={copy.remove} aria-label={`${copy.remove} ${item.name}`} onClick={() => void remove(item)}><Trash2 size={15} strokeWidth={1.75} /></button>
          </div>
        </li>;
      })}
    </ul> : <section className="medicineEmpty" aria-label={copy.list}>
      <ActionIcon name="medicine" size={32} />
      <h2>{box.items.length ? copy.emptyCategory : copy.empty}</h2>
      {!box.items.length && <p>{copy.emptyDescription}</p>}
    </section>}
    {box.editing && box.api && <MedicineDialog language={language} item={box.editing.item} defaultCategory={box.category === "all" ? "" : box.category}
      recognitionAvailable={box.recognitionAvailable}
      onClose={() => box.setEditing(null)}
      onSaved={(saved) => { box.setEditing(null); if (box.category !== "all") box.setCategory(saved.category); void box.reload(); }} />}
  </div>;
}

type Draft = { name: string; spec: string; manufacturer: string; category: MedicineCategory | ""; quantity: string; unit: MedicineUnit; expires_on: string };
const blankDraft = (category: MedicineCategory | ""): Draft => ({ name: "", spec: "", manufacturer: "", category, quantity: "", unit: "tablet", expires_on: "" });
type Status = { kind: "idle" | "recognizing" | "recognized" | "problem"; text: string };

function MedicineDialog({ language, item, defaultCategory, recognitionAvailable, onClose, onSaved }: {
  language: Language; item?: MedicineItem; defaultCategory: MedicineCategory | ""; recognitionAvailable: boolean;
  onClose: () => void; onSaved: (item: MedicineItem) => void;
}) {
  const copy = medicineBoxCopy[language];
  const api = medicineBox()!;
  const [draft, setDraft] = useState<Draft>(item ? { ...item, quantity: String(item.quantity) } : blankDraft(defaultCategory));
  // A category the owner or the model chose is kept; otherwise typing a name guesses one.
  const [categoryChosen, setCategoryChosen] = useState(Boolean(item || defaultCategory));
  const [photo, setPhoto] = useState("");
  const [status, setStatus] = useState<Status>({ kind: "idle", text: "" });
  const [saving, setSaving] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const recognition = useRef<AbortController | null>(null);
  const recognizing = status.kind === "recognizing";
  const busy = recognizing || saving;
  const dialogRef = useDismissibleLayer<HTMLDivElement>({ open: true, onDismiss: onClose, modal: true, dismissible: !saving,
    initialFocus: item ? "input[name=quantity]" : recognitionAvailable ? ".medicinePhotoButton" : "input[name=name]" });
  useEffect(() => () => { if (photo) URL.revokeObjectURL(photo); }, [photo]);
  useEffect(() => () => recognition.current?.abort(), []);

  const recognize = async (file: File | undefined) => {
    if (!file || busy || !recognitionAvailable) return;
    setPhoto((current) => { if (current) URL.revokeObjectURL(current); return URL.createObjectURL(file); });
    setStatus({ kind: "recognizing", text: copy.recognizing });
    let prepared;
    try { prepared = await prepareMedicinePhoto(file); }
    catch { setStatus({ kind: "problem", text: copy.photoUnreadable }); return; }
    recognition.current?.abort();
    const controller = new AbortController();
    recognition.current = controller;
    try {
      const result = await backend.recognizeMedicine(prepared, controller.signal);
      if (controller.signal.aborted) return;
      if (!result.recognized) { setStatus({ kind: "problem", text: copy.notRecognized }); return; }
      setDraft((current) => ({ ...current, name: result.name, spec: result.spec, manufacturer: result.manufacturer, category: result.category, unit: result.unit }));
      setCategoryChosen(true);
      setStatus({ kind: "recognized", text: copy.recognized.replace("{name}", result.name).replace("{category}", copy.categories[result.category]) });
    } catch {
      if (!controller.signal.aborted) setStatus({ kind: "problem", text: copy.recognitionUnavailable });
    }
  };
  const quantity = Number(draft.quantity);
  const valid = Boolean(draft.name.trim() && draft.category && draft.quantity.trim() !== "" && Number.isFinite(quantity) && quantity >= 0);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!valid || busy || !draft.category) return;
    setSaving(true);
    try {
      onSaved(await api.save({ id: item?.id ?? "", name: draft.name.trim(), spec: draft.spec.trim(), manufacturer: draft.manufacturer.trim(), category: draft.category,
        quantity: Math.round(quantity * 100) / 100, unit: draft.unit, expires_on: draft.expires_on }));
    } catch { setStatus({ kind: "problem", text: copy.saveFailed }); }
    finally { setSaving(false); }
  };
  const update = (patch: Partial<Draft>) => setDraft((current) => ({ ...current, ...patch }));
  const onDrop = (event: DragEvent) => { event.preventDefault(); void recognize(event.dataTransfer.files[0]); };
  const onPaste = (event: ClipboardEvent) => {
    const file = [...event.clipboardData.files].find((candidate) => candidate.type.startsWith("image/"));
    if (file) { event.preventDefault(); void recognize(file); }
  };

  return <div ref={dialogRef} tabIndex={-1} className="documentPickerOverlay" role="dialog" aria-modal="true" aria-labelledby="medicine-dialog-title"
    onMouseDown={(event) => { if (event.target === event.currentTarget && !saving) onClose(); }} onPaste={item ? undefined : onPaste}>
    <form className="scheduleDialog medicineDialog" onSubmit={(event) => void submit(event)}>
      <div className="documentPickerHeader scheduleCreateDialogHeader">
        <div className="scheduleCreateIntro">
          <ActionIcon name="medicine" />
          <div><h2 id="medicine-dialog-title">{item ? copy.editTitle : copy.addTitle}</h2>{!item && <p>{copy.photoIntro}</p>}</div>
        </div>
        <button className="attachmentRemove" type="button" onClick={onClose} disabled={saving} title={copy.close} aria-label={copy.close}><X size={16} strokeWidth={1.75} /></button>
      </div>
      {!item && <div className={`medicinePhotoDrop ${photo ? "hasPhoto" : ""}`} onDragOver={(event) => event.preventDefault()} onDrop={onDrop}>
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
      </div>}
      {status.text && <p className={`medicineStatus ${status.kind}`} role={status.kind === "problem" ? "alert" : "status"}>{status.text}</p>}
      <div className="scheduleDialogGrid medicineDialogGrid">
        <label className="medicineFieldWide"><span>{copy.name}</span>
          <input name="name" value={draft.name} maxLength={80} required placeholder={copy.namePlaceholder} disabled={busy}
            onChange={(event) => { const name = event.target.value; update({ name, ...(categoryChosen ? {} : { category: suggestMedicineCategory(name) }) }); }} /></label>
        <label><span>{copy.spec}</span>
          <input name="spec" value={draft.spec} maxLength={80} placeholder={copy.specPlaceholder} disabled={busy} onChange={(event) => update({ spec: event.target.value })} /></label>
        <label><span>{copy.manufacturer}</span>
          <input name="manufacturer" value={draft.manufacturer} maxLength={80} disabled={busy} onChange={(event) => update({ manufacturer: event.target.value })} /></label>
        <label><span>{copy.category}</span>
          <select name="category" value={draft.category} required disabled={busy} onChange={(event) => { setCategoryChosen(true); update({ category: event.target.value as MedicineCategory }); }}>
            {!draft.category && <option value="" disabled>—</option>}
            {MEDICINE_CATEGORIES.map((category) => <option key={category} value={category}>{copy.categories[category]}</option>)}
          </select></label>
        <label><span>{copy.expiry}</span>
          <input name="expires_on" type="date" value={draft.expires_on} disabled={busy} onChange={(event) => update({ expires_on: event.target.value })} /></label>
        <label className="medicineFieldWide"><span>{copy.quantity}</span>
          <div className="medicineQuantityInput">
            <input name="quantity" type="number" min={0} max={100000} step="any" required value={draft.quantity} disabled={busy} onChange={(event) => update({ quantity: event.target.value })} />
            <select name="unit" aria-label={copy.unit} value={draft.unit} disabled={busy} onChange={(event) => update({ unit: event.target.value as MedicineUnit })}>
              {MEDICINE_UNITS.map((unit) => <option key={unit} value={unit}>{copy.units[unit]}</option>)}
            </select>
          </div></label>
      </div>
      <div className="scheduleDialogActions">
        <button className="secondaryButton" type="button" onClick={onClose} disabled={saving}>{copy.cancel}</button>
        <button className="primaryButton" type="submit" disabled={busy || !valid}>{saving ? copy.saving : item ? copy.saveChanges : copy.save}</button>
      </div>
    </form>
  </div>;
}
