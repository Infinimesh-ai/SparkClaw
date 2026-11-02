// The two result views of the medicine box workspace. Same-name medicines
// arrive grouped; cards sit side by side with an "N entries" tag, and the list
// gives each group a header row.
import type { CSSProperties } from "react";
import { Pencil, Trash2 } from "lucide-react";
import { medicineStatus, type MedicineItem, type MedicineStatus } from "../../desktop/medicineBox";
import { expiryText, fill, quantityText, statusText, type MedicineCopy } from "./copy";

export type MedicineGroup = { key: string; items: MedicineItem[] };

export function StatusBadge({ copy, item, status = medicineStatus(item) }: { copy: MedicineCopy; item: MedicineItem; status?: MedicineStatus }) {
  return <span className={`medicineBadge ${status}`}>{statusText(copy, item, status)}</span>;
}

function QuantityValue({ copy, item }: { copy: MedicineCopy; item: MedicineItem }) {
  return item.quantity === 0 ? <span className="medicineUsedUp">{copy.usedUp}</span> : <>{quantityText(copy, item.quantity, item.unit)}</>;
}

// Groups that hold one item get no header; quantities add up per unit.
function groupTotal(copy: MedicineCopy, items: MedicineItem[]) {
  const totals = new Map<MedicineItem["unit"], number>();
  for (const item of items) totals.set(item.unit, Math.round(((totals.get(item.unit) ?? 0) + item.quantity) * 100) / 100);
  return [...totals].map(([unit, quantity]) => quantityText(copy, quantity, unit)).join(" + ");
}

export function MedicineCards({ copy, groups, onOpen }: { copy: MedicineCopy; groups: MedicineGroup[]; onOpen: (item: MedicineItem) => void }) {
  const items = groups.flatMap((group) => group.items.map((item) => ({ item, count: group.items.length })));
  // The usage row stays only when some result has one, so cards keep aligned.
  const usageRow = items.some(({ item }) => item.usage);
  return <ul className="medicineGrid" aria-label={copy.list}>
    {items.map(({ item, count }, index) => {
      const status = medicineStatus(item);
      const label = statusText(copy, item, status);
      return <li key={item.id} style={{ "--medicine-index": Math.min(index, 11) } as CSSProperties}>
        <button type="button" className={`medicineCard ${status}`} aria-label={`${item.name}，${label}`} onClick={() => onOpen(item)}>
          <span className="medicineCardHead">
            <strong>{item.name}</strong>
            {count > 1 && <span className="medicineGroupTag">{fill(copy.sameNameCount, { count })}</span>}
            <StatusBadge copy={copy} item={item} status={status} />
          </span>
          <span className="medicineCardCategory">{copy.categories[item.category]}</span>
          {usageRow && <span className="medicineCardUsage">{item.usage}</span>}
          <span className="medicineCardFacts">
            <span className="medicineFact"><span>{copy.expiry}</span><b className={`medicineExpiry ${status}`}>{expiryText(copy, item)}</b></span>
            <span className="medicineFactRow">
              <span className="medicineFact"><span>{copy.remaining}</span><b><QuantityValue copy={copy} item={item} /></b></span>
              <span className="medicineFact"><span>{copy.location}</span><b className={item.location ? "" : "isUnset"}>{item.location || copy.notSet}</b></span>
            </span>
          </span>
        </button>
      </li>;
    })}
  </ul>;
}

export function MedicineTable({ copy, groups, writable = true, onOpen, onEdit, onRemove }: {
  writable?: boolean; copy: MedicineCopy; groups: MedicineGroup[]; onOpen: (item: MedicineItem) => void; onEdit: (item: MedicineItem) => void; onRemove: (item: MedicineItem) => void;
}) {
  return <table className="medicineTable" aria-label={copy.list}>
    <thead><tr>
      <th scope="col">{copy.columnName}</th><th scope="col">{copy.category}</th><th scope="col">{copy.expiry}</th><th scope="col">{copy.filterStatus}</th>
      <th scope="col" className="medicineNumber">{copy.remaining}</th><th scope="col" className="medicineLocationColumn">{copy.location}</th>
      <th scope="col" aria-label={copy.columnActions} />
    </tr></thead>
    {groups.map((group) => <tbody key={group.key}>
      {group.items.length > 1 && <tr className="medicineGroupRow"><th scope="rowgroup" colSpan={7}>
        {fill(copy.sameNameGroup, { name: group.items[0].name, count: group.items.length, total: fill(copy.sameNameTotal, { quantity: groupTotal(copy, group.items) }) })}
      </th></tr>}
      {group.items.map((item) => {
        const status = medicineStatus(item);
        return <tr className={`medicineRow ${status}`} key={item.id} onClick={(event) => { if (!(event.target as HTMLElement).closest(".medicineRowActions")) onOpen(item); }}>
          <td className="medicineRowName"><button type="button" className="medicineRowOpen" onClick={(event) => { event.stopPropagation(); onOpen(item); }}><strong>{item.name}</strong></button></td>
          <td className="medicineRowCategory">{copy.categories[item.category]}</td>
          <td className={`medicineExpiry ${status}`}>{expiryText(copy, item)}</td>
          <td><StatusBadge copy={copy} item={item} status={status} /></td>
          <td className="medicineNumber medicineRowQuantity"><QuantityValue copy={copy} item={item} /></td>
          <td className="medicineLocationColumn medicineRowLocation">{item.location || "—"}</td>
          <td><div className="medicineRowActions">
            <button className="iconButton" type="button" disabled={!writable} title={!writable ? copy.connectToEdit : copy.edit} aria-label={`${copy.edit} ${item.name}`} onClick={() => onEdit(item)}><Pencil size={15} strokeWidth={1.75} /></button>
            <button className="iconButton" type="button" disabled={!writable} title={!writable ? copy.connectToEdit : copy.remove} aria-label={`${copy.remove} ${item.name}`} onClick={() => onRemove(item)}><Trash2 size={15} strokeWidth={1.75} /></button>
          </div></td>
        </tr>;
      })}
    </tbody>)}
  </table>;
}
