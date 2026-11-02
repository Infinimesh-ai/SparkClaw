import { describe, expect, it } from "vitest";
import { filterMedicines, groupMedicines, medicineGroupKey, medicineStats, medicineStatus, monthEnd, sortMedicines, stepQuantity, type MedicineItem } from "./medicineBox";

const now = new Date(2026, 10, 2, 15, 30);
let serial = 0;
const item = (fields: Partial<MedicineItem>): MedicineItem => ({ id: `med_${String(++serial).padStart(32, "0")}`, name: "布洛芬缓释胶囊", category: "fever", quantity: 1,
  unit: "capsule", expires_on: "", no_expiry: false, location: "", usage: "", notes: "", created_at: `2026-10-${String(10 + serial).padStart(2, "0")}T00:00:00.000Z`, updated_at: "", ...fields });
const label = (category: MedicineItem["category"]) => ({ cold: "感冒", fever: "发烧", pain: "止痛", cough: "咳嗽", digestive: "肠胃", allergy: "过敏", wound: "外伤", other: "其他" })[category];

describe("medicine box rules", () => {
  it("reads expiry status with a fixed 30-day threshold that counts today and day 30", () => {
    expect(medicineStatus(item({ expires_on: "2026-11-01" }), now)).toBe("expired");
    expect(medicineStatus(item({ expires_on: "2026-11-02" }), now)).toBe("expiring");
    expect(medicineStatus(item({ expires_on: "2026-12-02" }), now)).toBe("expiring");
    expect(medicineStatus(item({ expires_on: "2026-12-03" }), now)).toBe("ok");
    expect(medicineStatus(item({ no_expiry: true }), now)).toBe("none");
    expect(medicineStatus(item({}), now)).toBe("unknown");
  });

  it("counts the whole box and leaves no-expiry medicines out of expiring and expired", () => {
    expect(medicineStats([item({ expires_on: "2026-01-01" }), item({ expires_on: "2026-11-10" }), item({ no_expiry: true }), item({})], now))
      .toEqual({ total: 4, expiring: 1, expired: 1 });
  });

  it("combines category, status and search, matching usage, location, notes and category labels", () => {
    const items = [
      item({ name: "布洛芬", usage: "退烧", expires_on: "2026-11-20" }),
      item({ name: "蒙脱石散", category: "digestive", location: "厨房药盒", expires_on: "2025-01-01" }),
      item({ name: "创可贴", category: "wound", notes: "旅行包里还有一盒", no_expiry: true }),
    ];
    const names = (filter: Parameters<typeof filterMedicines>[1]) => filterMedicines(items, filter, label, now).map((entry) => entry.name);
    expect(names({ category: "all", status: "all", search: "" })).toEqual(["布洛芬", "蒙脱石散", "创可贴"]);
    expect(names({ category: "all", status: "all", search: "  厨房 " })).toEqual(["蒙脱石散"]);
    expect(names({ category: "all", status: "all", search: "旅行" })).toEqual(["创可贴"]);
    expect(names({ category: "all", status: "all", search: "肠胃" })).toEqual(["蒙脱石散"]);
    expect(names({ category: "all", status: "expiring", search: "" })).toEqual(["布洛芬"]);
    expect(names({ category: "digestive", status: "expired", search: "蒙脱" })).toEqual(["蒙脱石散"]);
    expect(names({ category: "fever", status: "expired", search: "" })).toEqual([]);
  });

  it("sorts by expiry with no-expiry entries after dated ones and undated rows last, or by newest first", () => {
    const items = [item({ name: "C" }), item({ name: "B", no_expiry: true }), item({ name: "A", expires_on: "2027-01-01" }), item({ name: "D", expires_on: "2026-12-01" }), item({ name: "E", expires_on: "2026-12-01" })];
    expect(sortMedicines(items, "expiry").map((entry) => entry.name)).toEqual(["D", "E", "A", "B", "C"]);
    expect(sortMedicines(items, "created").map((entry) => entry.name)).toEqual(["E", "D", "A", "B", "C"]);
  });

  it("keeps same-name medicines together, ordered by expiry, where their first entry falls", () => {
    expect(medicineGroupKey("  Ｉｂｕ   ProFen ")).toBe(medicineGroupKey("ibu profen"));
    const items = [
      item({ name: "布洛芬缓释胶囊", expires_on: "2027-06-30" }),
      item({ name: "蒙脱石散", expires_on: "2026-12-01" }),
      item({ name: "布洛芬缓释胶囊 ", expires_on: "2026-11-20", unit: "box" }),
    ];
    const byExpiry = groupMedicines(items, "expiry");
    expect(byExpiry.map((group) => group.items.map((entry) => entry.expires_on))).toEqual([["2026-11-20", "2027-06-30"], ["2026-12-01"]]);
    // Recently added: the group follows its newest entry, entries still by expiry.
    expect(groupMedicines(items, "created").map((group) => group.items.map((entry) => entry.expires_on))).toEqual([["2026-11-20", "2027-06-30"], ["2026-12-01"]]);
    expect(groupMedicines([items[0], items[1]], "created").map((group) => group.items[0].name)).toEqual(["蒙脱石散", "布洛芬缓释胶囊"]);
  });

  it("saves a month-only expiry as the month's last day", () => {
    expect(monthEnd("2027-12")).toBe("2027-12-31");
    expect(monthEnd("2028-02")).toBe("2028-02-29");
    expect(monthEnd("2027-02")).toBe("2027-02-28");
    expect(monthEnd("2027-13")).toBe("");
    expect(monthEnd("")).toBe("");
  });

  it("steps the quantity by one unit between 0 and 100000", () => {
    expect(stepQuantity(14, -1)).toBe(13);
    expect(stepQuantity(2.5, -1)).toBe(1.5);
    expect(stepQuantity(1.5, -1)).toBe(0);
    expect(stepQuantity(1, -1)).toBe(0);
    expect(stepQuantity(0, -1)).toBe(0);
    expect(stepQuantity(2.5, 1)).toBe(3.5);
    expect(stepQuantity(100000, 1)).toBe(100000);
  });
});
