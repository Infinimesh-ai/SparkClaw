// The medicine box bridge exposed by the desktop preload. The main process owns
// the inventory and scopes it to the signed-in account; the renderer has the
// backend recognize a front-of-package photo and saves the confirmed fields.
export const MEDICINE_CATEGORIES = ["cold", "fever", "pain", "cough", "digestive", "allergy", "wound", "other"] as const;
export const MEDICINE_UNITS = ["tablet", "capsule", "sachet", "bottle", "tube", "patch", "box", "ml", "g"] as const;
export type MedicineCategory = typeof MEDICINE_CATEGORIES[number];
export type MedicineUnit = typeof MEDICINE_UNITS[number];

// `no_expiry` is the owner's explicit "this medicine has no expiry date"; an
// empty `expires_on` without it only occurs on rows saved before expiry
// became required.
export type MedicineItem = {
  id: string; name: string; category: MedicineCategory; quantity: number; unit: MedicineUnit;
  expires_on: string; no_expiry: boolean; location: string; usage: string; notes: string; created_at: string; updated_at: string;
};
// An empty id adds a medicine; an existing id updates that row.
export type MedicineFields = Omit<MedicineItem, "created_at" | "updated_at">;
// What the backend's vision model read from the photo. A gateway from the
// first release still sends a pack size and manufacturer, which are ignored,
// and no usage.
export type MedicineRecognition = { recognized: boolean; name: string; category: MedicineCategory; unit: MedicineUnit; usage?: string };
// What the backend read from one sentence the owner typed. Only what the text
// states is filled; `multiple` means it named more than one medicine.
export type MedicineTextParse = {
  recognized: boolean; multiple?: boolean; name: string; category: MedicineCategory; unit: MedicineUnit;
  quantity: number | null; expires_on: string; location: string; usage: string;
};

export type MedicineBoxAPI = {
  list: () => Promise<{ items: MedicineItem[] }>;
  save: (medicine: MedicineFields) => Promise<MedicineItem>;
  remove: (id: string) => Promise<{ deleted: boolean }>;
};

declare global {
  interface Window { sparkclawMedicineBox?: MedicineBoxAPI }
}

export function medicineBox() { return window.sparkclawMedicineBox; }

// Common household medicines by the words their names carry, checked in this
// order so a cold remedy that also lowers fever stays under colds. Used when
// the owner types a name the photo did not provide; it is only a first guess.
const CATEGORY_KEYWORDS: Array<[MedicineCategory, string[]]> = [
  ["cold", ["感冒", "风寒", "风热", "板蓝根", "连花清瘟", "双黄连", "银翘", "小柴胡", "氨酚", "抗病毒", "清热解毒", "伤风"]],
  ["cough", ["咳", "痰", "川贝", "枇杷", "氨溴索", "右美沙芬", "愈创", "桔梗", "急支", "镇咳"]],
  ["fever", ["布洛芬", "对乙酰氨基酚", "退热", "退烧", "美林", "泰诺林", "安乃近", "解热"]],
  ["pain", ["止痛", "镇痛", "去痛", "双氯芬酸", "扶他林", "萘普生", "塞来昔布", "洛索洛芬", "芬必得", "头痛", "牙痛"]],
  ["digestive", ["胃", "肠", "蒙脱石", "益生菌", "双歧杆菌", "乳酸菌", "奥美拉唑", "消食", "多潘立酮", "吗丁啉", "腹泻", "止泻", "便秘", "铝碳酸镁", "黄连素", "小檗碱", "藿香正气", "开塞露", "健脾"]],
  ["allergy", ["过敏", "氯雷他定", "西替利嗪", "氯苯那敏", "扑尔敏", "依巴斯汀", "抗组胺", "炉甘石", "荨麻疹"]],
  ["wound", ["创可贴", "碘伏", "酒精", "云南白药", "红花油", "莫匹罗星", "红霉素软膏", "烫伤", "跌打", "膏药", "纱布", "双氧水", "消毒", "百多邦"]],
];

export function suggestMedicineCategory(name: string): MedicineCategory | "" {
  const text = name.trim();
  if (!text) return "";
  for (const [category, words] of CATEGORY_KEYWORDS) if (words.some((word) => text.includes(word))) return category;
  return "";
}

export const medicineFields = (item: MedicineItem): MedicineFields => ({ id: item.id, name: item.name, category: item.category, quantity: item.quantity,
  unit: item.unit, expires_on: item.expires_on, no_expiry: item.no_expiry, location: item.location, usage: item.usage, notes: item.notes });

// Days until the end of the expiry date in local time; negative once expired.
export function medicineDaysLeft(expiresOn: string, now = new Date()) {
  if (!expiresOn) return null;
  const [year, month, day] = expiresOn.split("-").map(Number);
  const end = new Date(year, month - 1, day + 1).getTime();
  return Math.ceil((end - now.getTime()) / 86_400_000) - 1;
}

// The status rule follows open-medkit (MIT, MonoYan/open-medkit 8c2217ba):
// past the date is expired, within a fixed 30 days (today included) expiring.
export const MEDICINE_EXPIRING_DAYS = 30;
export type MedicineStatus = "expired" | "expiring" | "ok" | "none" | "unknown";
export type MedicineStatusFilter = "all" | "expiring" | "expired";
export type MedicineSort = "expiry" | "created";

export function medicineStatus(item: Pick<MedicineItem, "expires_on" | "no_expiry">, now = new Date()): MedicineStatus {
  if (item.no_expiry) return "none";
  const days = medicineDaysLeft(item.expires_on, now);
  if (days === null) return "unknown";
  return days < 0 ? "expired" : days <= MEDICINE_EXPIRING_DAYS ? "expiring" : "ok";
}

// Counts over the whole box, whatever is filtered.
export function medicineStats(items: MedicineItem[], now = new Date()) {
  const stats = { total: items.length, expiring: 0, expired: 0 };
  for (const item of items) {
    const status = medicineStatus(item, now);
    if (status === "expiring") stats.expiring++;
    else if (status === "expired") stats.expired++;
  }
  return stats;
}

const normalize = (value: string) => value.normalize("NFKC").trim().replace(/\s+/gu, " ").toLowerCase();
// Medicines whose names match after normalisation are lots of one medicine.
export const medicineGroupKey = (name: string) => normalize(name);

export function filterMedicines(items: MedicineItem[], filter: { category: MedicineCategory | "all"; status: MedicineStatusFilter; search: string },
  categoryLabel: (category: MedicineCategory) => string, now = new Date()) {
  const search = normalize(filter.search);
  return items.filter((item) => (filter.category === "all" || item.category === filter.category)
    && (filter.status === "all" || medicineStatus(item, now) === filter.status)
    && (!search || [item.name, item.usage, item.location, item.notes, categoryLabel(item.category)].some((field) => normalize(field).includes(search))));
}

// Dated medicines first, then those marked as having no expiry, then earlier
// rows without a date; ties go by name.
const expiryRank = (item: MedicineItem) => item.no_expiry ? 1 : item.expires_on ? 0 : 2;
const byName = (a: MedicineItem, b: MedicineItem) => a.name.localeCompare(b.name, "zh-Hans-CN") || a.created_at.localeCompare(b.created_at);
const byExpiry = (a: MedicineItem, b: MedicineItem) => expiryRank(a) - expiryRank(b) || a.expires_on.localeCompare(b.expires_on) || byName(a, b);
const byCreated = (a: MedicineItem, b: MedicineItem) => b.created_at.localeCompare(a.created_at) || byName(a, b);

export function sortMedicines(items: MedicineItem[], sort: MedicineSort) {
  return [...items].sort(sort === "expiry" ? byExpiry : byCreated);
}

// Same-name medicines stay together: each group sits where its first entry
// falls in the chosen order, and its entries are ordered by expiry.
export function groupMedicines(items: MedicineItem[], sort: MedicineSort) {
  const groups = new Map<string, MedicineItem[]>();
  for (const item of sortMedicines(items, sort)) {
    const key = medicineGroupKey(item.name);
    groups.set(key, [...groups.get(key) ?? [], item]);
  }
  return [...groups].map(([key, entries]) => ({ key, items: entries.sort(byExpiry) }));
}

// "2027-12" → "2027-12-31": an expiry known only to the month.
export function monthEnd(yearMonth: string) {
  const match = /^(\d{4})-(\d{2})$/u.exec(yearMonth);
  if (!match) return "";
  const [year, month] = [Number(match[1]), Number(match[2])];
  if (month < 1 || month > 12) return "";
  return `${match[1]}-${match[2]}-${String(new Date(Date.UTC(year, month, 0)).getUTCDate()).padStart(2, "0")}`;
}

// Quick adjustment moves by one unit between 0 and 100000; going down to less
// than one unit drops to 0. Millilitres and grams are changed through edit only.
export const MEDICINE_QUANTITY_MAX = 100000;
export const medicineStepAdjustable = (unit: MedicineUnit) => unit !== "ml" && unit !== "g";
export function stepQuantity(quantity: number, delta: 1 | -1) {
  const next = Math.round((quantity + delta) * 100) / 100;
  return delta < 0 && next < 1 ? 0 : Math.min(MEDICINE_QUANTITY_MAX, next);
}
