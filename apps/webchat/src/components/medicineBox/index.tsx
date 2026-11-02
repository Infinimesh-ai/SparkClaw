// Medicine box module: its own sidebar beside the module rail and its own
// workspace page. The workspace follows open-medkit's structure (stats, search,
// expiry filters, cards or a list, a detail dialog and a two-step add flow) in
// SparkClaw's own look. Medicines are added from a photo of the package front,
// which the backend's vision model reads and sorts by what the medicine treats;
// the owner confirms every field before anything is saved.
import { useCallback, useEffect, useState } from "react";
import { LayoutGrid, List, Plus, Search, X } from "lucide-react";
import { dictionaries, type Language } from "../../i18n";
import { useConfirm } from "../confirmation";
import { ActionIcon } from "../ActionIcon";
import { MEDICINE_CATEGORIES, MEDICINE_EXPIRING_DAYS, filterMedicines, groupMedicines, medicineBox, medicineStats,
  type MedicineCategory, type MedicineItem, type MedicineSort, type MedicineStatusFilter } from "../../desktop/medicineBox";
import { fill, medicineBoxCopy } from "./copy";
import { MedicineCards, MedicineTable } from "./views";
import { MedicineDetailDialog } from "./detailDialog";
import { MedicineEntryDialog } from "./entryDialog";

export { medicineBoxCopy } from "./copy";
type CategoryFilter = MedicineCategory | "all";
type MedicineView = "cards" | "list";

// View and sort are UI preferences only; no medicine data goes to localStorage.
const VIEW_KEY = "sparkclaw.medicine.view";
const SORT_KEY = "sparkclaw.medicine.sort";
function preference<T extends string>(key: string, values: readonly T[], fallback: T): T {
  try { const value = localStorage.getItem(key); return values.includes(value as T) ? value as T : fallback; } catch { return fallback; }
}
function remember(key: string, value: string) { try { localStorage.setItem(key, value); } catch { /* preferences are optional */ } }

export function useMedicineBox(active: boolean, recognitionAvailable: boolean) {
  const api = medicineBox();
  const [items, setItems] = useState<MedicineItem[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [failed, setFailed] = useState(false);
  const [category, setCategory] = useState<CategoryFilter>("all");
  const [status, setStatus] = useState<MedicineStatusFilter>("all");
  const [search, setSearch] = useState("");
  const [view, setViewState] = useState<MedicineView>(() => preference(VIEW_KEY, ["cards", "list"] as const, "cards"));
  const [sort, setSortState] = useState<MedicineSort>(() => preference(SORT_KEY, ["expiry", "created"] as const, "expiry"));
  const [editing, setEditing] = useState<{ item?: MedicineItem } | null>(null);
  const [detail, setDetail] = useState<string | null>(null);
  const reload = useCallback(async () => {
    if (!api) return;
    try { setItems((await api.list()).items); setFailed(false); }
    catch { setFailed(true); }
    finally { setLoaded(true); }
  }, [api]);
  useEffect(() => { if (active) void reload(); }, [active, reload]);
  const setView = useCallback((value: MedicineView) => { setViewState(value); remember(VIEW_KEY, value); }, []);
  const setSort = useCallback((value: MedicineSort) => { setSortState(value); remember(SORT_KEY, value); }, []);
  const clearFilters = useCallback(() => { setCategory("all"); setStatus("all"); setSearch(""); }, []);
  return { api, items, loaded: loaded || !api, failed, category, setCategory, status, setStatus, search, setSearch, view, setView, sort, setSort, clearFilters,
    editing, setEditing, detail, setDetail, reload, recognitionAvailable };
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

export function MedicineWorkspace({ language, box }: { language: Language; box: MedicineBoxState }) {
  const copy = medicineBoxCopy[language];
  const confirm = useConfirm(dictionaries[language]);
  const stats = medicineStats(box.items);
  const filtered = filterMedicines(box.items, { category: box.category, status: box.status, search: box.search }, (category) => copy.categories[category]);
  const groups = groupMedicines(filtered, box.sort);
  const detail = box.detail ? box.items.find((item) => item.id === box.detail) : undefined;
  const filtering = box.category !== "all" || box.status !== "all" || box.search.trim() !== "";
  const remove = async (item: MedicineItem) => {
    if (!box.api || !await confirm(fill(copy.confirmRemove, { name: item.name }), { danger: true })) return;
    try { await box.api.remove(item.id); } finally { await box.reload(); }
  };
  const statusLabel = { all: copy.statusAll, expiring: fill(copy.expiringWithin, { days: MEDICINE_EXPIRING_DAYS }), expired: copy.expiredCount };
  const addButton = (className: string) => <button className={className} type="button" disabled={!box.api} title={box.api ? undefined : copy.unavailable} onClick={() => box.setEditing({})}><Plus size={15} />{copy.add}</button>;

  return <div className="workbenchPage medicinePage">
    <div className="workbenchPageHeader">
      <div>
        <h1>{box.category === "all" ? copy.title : copy.categories[box.category]}</h1>
        <p className="medicineStats">
          <span>{copy.total} {stats.total}</span>
          {stats.expiring > 0 && <span className="expiring">{statusLabel.expiring} {stats.expiring}</span>}
          {stats.expired > 0 && <span className="expired">{copy.expiredCount} {stats.expired}</span>}
        </p>
      </div>
      {addButton("primaryButton")}
    </div>
    {box.failed && <div className="localFeedback" role="alert"><p>{copy.loadFailed}</p><button type="button" onClick={() => void box.reload()}>{copy.retry}</button></div>}
    {!box.loaded ? <ul className="medicineGrid medicineSkeleton" aria-hidden="true">{Array.from({ length: 6 }, (_, index) => <li key={index}><span /></li>)}</ul>
      : !box.items.length ? !box.failed && <section className="medicineEmpty" aria-label={copy.list}>
        <ActionIcon name="medicine" size={32} />
        <h2>{copy.empty}</h2>
        <p>{copy.description}</p>
        {addButton("secondaryButton")}
      </section>
      : <section className="medicinePanel" aria-label={copy.list}>
        <div className="medicineToolbar">
          <div className="medicineToolbarGroup">
            <label className="medicineSearch">
              <Search size={15} strokeWidth={1.75} aria-hidden="true" />
              <input type="search" value={box.search} placeholder={copy.searchPlaceholder} aria-label={copy.searchPlaceholder} onChange={(event) => box.setSearch(event.target.value)} />
            </label>
            <div className="medicineSegments" role="group" aria-label={copy.statusFilter}>
              {(["all", "expiring", "expired"] as const).map((value) => <button key={value} type="button" className={value} aria-pressed={box.status === value} onClick={() => box.setStatus(value)}>{statusLabel[value]}</button>)}
            </div>
          </div>
          <div className="medicineToolbarGroup">
            <div className="medicineSegments medicineViewToggle" role="group" aria-label={copy.viewMode}>
              <button type="button" aria-pressed={box.view === "cards"} title={copy.cardView} aria-label={copy.cardView} onClick={() => box.setView("cards")}><LayoutGrid size={15} strokeWidth={1.75} /></button>
              <button type="button" aria-pressed={box.view === "list"} title={copy.listView} aria-label={copy.listView} onClick={() => box.setView("list")}><List size={15} strokeWidth={1.75} /></button>
            </div>
            <div className="medicineSegments" role="group" aria-label={copy.sortMode}>
              <button type="button" aria-pressed={box.sort === "expiry"} onClick={() => box.setSort("expiry")}>{copy.sortExpiry}</button>
              <button type="button" aria-pressed={box.sort === "created"} onClick={() => box.setSort("created")}>{copy.sortCreated}</button>
            </div>
          </div>
        </div>
        {filtering && <div className="medicineChips">
          {box.category !== "all" && <span className="medicineChip">{copy.filterCategory} · {copy.categories[box.category]}</span>}
          {box.status !== "all" && <span className="medicineChip">{copy.filterStatus} · {statusLabel[box.status]}</span>}
          {box.search.trim() && <span className="medicineChip">{copy.filterSearch} · {box.search.trim()}</span>}
          <button type="button" className="medicineClear" onClick={box.clearFilters}><X size={13} strokeWidth={1.75} aria-hidden="true" />{copy.clearFilters}</button>
        </div>}
        {!filtered.length ? <div className="medicineNoMatches"><p>{copy.noMatches}</p><button type="button" className="secondaryButton" onClick={box.clearFilters}>{copy.clearFilters}</button></div>
          : box.view === "cards" ? <MedicineCards copy={copy} groups={groups} onOpen={(item) => box.setDetail(item.id)} />
          : <MedicineTable copy={copy} groups={groups} onOpen={(item) => box.setDetail(item.id)} onEdit={(item) => box.setEditing({ item })} onRemove={(item) => void remove(item)} />}
      </section>}
    {detail && box.api && !box.editing && <MedicineDetailDialog key={detail.id} language={language} api={box.api} item={detail} items={box.items}
      onClose={() => box.setDetail(null)} onSelect={(item) => box.setDetail(item.id)} onChanged={box.reload}
      onEdit={(item) => { box.setDetail(null); box.setEditing({ item }); }} />}
    {box.editing && box.api && <MedicineEntryDialog language={language} item={box.editing.item} items={box.items} defaultCategory={box.category === "all" ? "" : box.category}
      recognitionAvailable={box.recognitionAvailable}
      onClose={() => box.setEditing(null)}
      onSaved={(saved) => { box.setEditing(null); if (box.category !== "all") box.setCategory(saved.category); void box.reload(); }} />}
  </div>;
}
