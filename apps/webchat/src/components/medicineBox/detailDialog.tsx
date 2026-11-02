// Detail dialog opened from a card or a list row. The quantity card adjusts by
// one unit and saves at once through the same save() an edit uses.
import { useState } from "react";
import { Minus, Plus, X } from "lucide-react";
import { dictionaries, type Language } from "../../i18n";
import { useDismissibleLayer } from "../../hooks/useDismissibleLayer";
import { useConfirm } from "../confirmation";
import { medicineDaysLeft, medicineFields, medicineGroupKey, medicineStatus, medicineStepAdjustable, sortMedicines, stepQuantity,
  MEDICINE_QUANTITY_MAX, type MedicineBoxAPI, type MedicineItem } from "../../desktop/medicineBox";
import { expiryText, fill, formatDate, medicineBoxCopy, medicineWriteProblem, quantityText } from "./copy";
import { StatusBadge } from "./views";

export function MedicineDetailDialog({ language, writable = true, api, item, items, onClose, onSelect, onEdit, onChanged }: {
  writable?: boolean; language: Language; api: MedicineBoxAPI; item: MedicineItem; items: MedicineItem[];
  onClose: () => void; onSelect: (item: MedicineItem) => void; onEdit: (item: MedicineItem) => void; onChanged: () => Promise<void>;
}) {
  const copy = medicineBoxCopy[language];
  const confirm = useConfirm(dictionaries[language]);
  const [pending, setPending] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState("");
  const dialogRef = useDismissibleLayer<HTMLDivElement>({ open: true, onDismiss: onClose, modal: true, dismissible: !busy, initialFocus: ".medicineDetailClose" });
  const status = medicineStatus(item);
  const days = medicineDaysLeft(item.expires_on) ?? 0;
  const banner = { expired: copy.bannerExpired, expiring: days === 0 ? copy.bannerToday : fill(copy.bannerExpiring, { days }), ok: copy.bannerOk,
    none: copy.bannerNone, unknown: copy.bannerUnknown }[status];
  const quantity = pending ?? item.quantity;
  const unit = copy.units[item.unit];
  const sameName = sortMedicines(items.filter((other) => other.id !== item.id && medicineGroupKey(other.name) === medicineGroupKey(item.name)), "expiry");

  const adjust = async (delta: 1 | -1) => {
    if (busy || !writable) return;
    const next = stepQuantity(quantity, delta);
    setBusy(true); setPending(next); setProblem("");
    try {
      await api.save({ ...medicineFields(item), quantity: next });
      await onChanged();
    } catch (error) {
      setProblem(medicineWriteProblem(error, copy, copy.quantitySaveFailed));
    } finally { setPending(null); setBusy(false); }
  };
  const remove = async () => {
    if (busy || !writable || !await confirm(fill(copy.confirmRemove, { name: item.name }), { danger: true })) return;
    setBusy(true); setProblem("");
    try {
      if (item.version !== undefined) await api.remove(item.id, item.version); else await api.remove(item.id);
      setBusy(false);
      onClose();
      await onChanged();
    } catch (error) {
      setBusy(false);
      setProblem(medicineWriteProblem(error, copy, copy.removeFailed));
    }
  };

  return <div ref={dialogRef} tabIndex={-1} className="documentPickerOverlay" role="dialog" aria-modal="true" aria-labelledby="medicine-detail-title"
    onMouseDown={(event) => { if (event.target === event.currentTarget && !busy) onClose(); }}>
    <section className="scheduleDialog medicineDialog medicineDetail">
      <div className="documentPickerHeader medicineDetailHeader">
        <div>
          <h2 id="medicine-detail-title">{item.name}</h2>
          <div className="medicineDetailTags"><span className="medicineCategoryTag">{copy.categories[item.category]}</span><StatusBadge copy={copy} item={item} status={status} /></div>
        </div>
        <button className="attachmentRemove medicineDetailClose" type="button" onClick={onClose} disabled={busy} title={copy.close} aria-label={copy.close}><X size={16} strokeWidth={1.75} /></button>
      </div>
      <p className={`medicineBanner ${status}`}>{banner}</p>
      <dl className="medicineInfoGrid">
        <div><dt>{copy.expiry}</dt><dd className={`medicineExpiry ${status}`}>{expiryText(copy, item)}</dd></div>
        <div className="medicineQuantityCard"><dt>{copy.quantity}</dt><dd>
          {medicineStepAdjustable(item.unit) && <button type="button" className="medicineStep" disabled={busy || !writable || quantity <= 0}
            aria-label={fill(copy.decrease, { unit })} title={fill(copy.decrease, { unit })} onClick={() => void adjust(-1)}><Minus size={14} strokeWidth={2} /></button>}
          <span aria-live="polite" className={quantity === 0 ? "medicineUsedUp" : ""}>{quantity === 0 ? copy.usedUp : quantityText(copy, quantity, item.unit)}</span>
          {medicineStepAdjustable(item.unit) && <button type="button" className="medicineStep" disabled={busy || !writable || quantity >= MEDICINE_QUANTITY_MAX}
            aria-label={fill(copy.increase, { unit })} title={fill(copy.increase, { unit })} onClick={() => void adjust(1)}><Plus size={14} strokeWidth={2} /></button>}
        </dd></div>
        <div><dt>{copy.location}</dt><dd className={item.location ? "" : "isUnset"}>{item.location || copy.notSet}</dd></div>
        <div><dt>{copy.category}</dt><dd>{copy.categories[item.category]}</dd></div>
        <div><dt>{copy.createdAt}</dt><dd>{item.created_at ? formatDate(copy, localDay(item.created_at)) : copy.notSet}</dd></div>
      </dl>
      <section className="medicineDetailSection"><h3>{copy.usage}</h3><p className={item.usage ? "" : "isUnset"}>{item.usage || copy.notSet}</p></section>
      <section className="medicineDetailSection"><h3>{copy.notes}</h3><p className={item.notes ? "medicineNotes" : "isUnset"}>{item.notes || copy.notSet}</p></section>
      {sameName.length > 0 && <section className="medicineDetailSection">
        <h3>{copy.sameNameRecords}</h3>
        <ul className="medicineSameName">
          {sameName.map((other) => <li key={other.id}><button type="button" onClick={() => onSelect(other)}>
            <span className={`medicineExpiry ${medicineStatus(other)}`}>{expiryText(copy, other)}</span>
            <span>{other.quantity === 0 ? copy.usedUp : quantityText(copy, other.quantity, other.unit)}</span>
            <span className={other.location ? "" : "isUnset"}>{other.location || copy.notSet}</span>
          </button></li>)}
        </ul>
      </section>}
      <div className="scheduleDialogActions medicineDetailActions">
        {problem && <p className="medicineStatus problem" role="alert">{problem}</p>}
        <button className="secondaryButton medicineDanger" type="button" disabled={busy || !writable} title={!writable ? copy.connectToEdit : undefined} onClick={() => void remove()}>{copy.remove}</button>
        <button className="secondaryButton" type="button" disabled={busy || !writable} title={!writable ? copy.connectToEdit : undefined} onClick={() => onEdit(item)}>{copy.edit}</button>
      </div>
    </section>
  </div>;
}

// created_at is a UTC timestamp; the date added is the owner's local day.
function localDay(timestamp: string) {
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) return timestamp.slice(0, 10);
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
}
