// The medicine box bridge exposed by the desktop preload. The main process owns
// the inventory and scopes it to the signed-in account; the renderer has the
// backend recognize a front-of-package photo and saves the confirmed fields.
export const MEDICINE_CATEGORIES = ["cold", "fever", "pain", "cough", "digestive", "allergy", "wound", "other"] as const;
export const MEDICINE_UNITS = ["tablet", "capsule", "sachet", "bottle", "tube", "patch", "box", "ml", "g"] as const;
export type MedicineCategory = typeof MEDICINE_CATEGORIES[number];
export type MedicineUnit = typeof MEDICINE_UNITS[number];

export type MedicineItem = {
  id: string; name: string; spec: string; manufacturer: string; category: MedicineCategory;
  quantity: number; unit: MedicineUnit; expires_on: string; created_at: string; updated_at: string;
};
// An empty id adds a medicine; an existing id updates that row.
export type MedicineFields = Pick<MedicineItem, "id" | "name" | "spec" | "manufacturer" | "category" | "quantity" | "unit" | "expires_on">;
// What the backend's vision model read from the photo.
export type MedicineRecognition = { recognized: boolean; name: string; spec: string; manufacturer: string; category: MedicineCategory; unit: MedicineUnit };

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

// Days until the end of the expiry date in local time; negative once expired.
export function medicineDaysLeft(expiresOn: string, now = new Date()) {
  if (!expiresOn) return null;
  const [year, month, day] = expiresOn.split("-").map(Number);
  const end = new Date(year, month - 1, day + 1).getTime();
  return Math.ceil((end - now.getTime()) / 86_400_000) - 1;
}
