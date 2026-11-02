// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MedicineSidebar, MedicineWorkspace, useMedicineBox } from "./medicineBox";
import type { MedicineBoxAPI, MedicineFields, MedicineItem, MedicineRecognition, MedicineTextParse } from "../desktop/medicineBox";

const backend = vi.hoisted(() => ({ recognizeMedicine: vi.fn(), parseMedicine: vi.fn() }));
vi.mock("../api/client", () => ({ api: backend }));
vi.mock("../desktop/medicinePhoto", () => ({ prepareMedicinePhoto: vi.fn(async () => ({ image: "cGhvdG8=", content_type: "image/jpeg" })) }));

const ID = (letter: string) => "med_" + letter.repeat(32);
const item = (fields: Partial<MedicineItem>): MedicineItem => ({ id: ID("a"), name: "布洛芬缓释胶囊", category: "fever", quantity: 12, unit: "capsule",
  expires_on: "2027-12-31", no_expiry: false, location: "客厅抽屉", usage: "退烧、止痛", notes: "", created_at: "2026-10-01T00:00:00.000Z", updated_at: "", ...fields });
const recognition = (fields: Partial<MedicineRecognition>): MedicineRecognition => ({ recognized: true, name: "", category: "other", unit: "tablet", ...fields });
const parsed = (fields: Partial<MedicineTextParse>): MedicineTextParse => ({ recognized: true, name: "", category: "other", unit: "tablet", quantity: null, expires_on: "", location: "", usage: "", ...fields });
const fieldsOf = ({ created_at: _created, updated_at: _updated, ...fields }: MedicineItem): MedicineFields => fields;

function Harness({ recognition, parse }: { recognition: boolean; parse: boolean }) {
  const box = useMedicineBox(true, recognition, parse);
  return <><MedicineSidebar language="zh" box={box} /><MedicineWorkspace language="zh" box={box} /></>;
}

describe("medicine box", () => {
  let host: HTMLDivElement, root: ReturnType<typeof createRoot>, items: MedicineItem[], api: MedicineBoxAPI & { list: ReturnType<typeof vi.fn>; save: ReturnType<typeof vi.fn>; remove: ReturnType<typeof vi.fn> };
  const query = <T extends Element = HTMLElement>(selector: string) => host.querySelector<T>(selector)!;
  const all = (selector: string) => [...host.querySelectorAll<HTMLElement>(selector)];
  const texts = (selector: string) => all(selector).map((element) => element.textContent);
  const type = async (selector: string, value: string) => act(async () => {
    const input = query<HTMLInputElement>(selector);
    Object.getOwnPropertyDescriptor(Object.getPrototypeOf(input), "value")!.set!.call(input, value);
    input.dispatchEvent(new Event(input instanceof HTMLSelectElement ? "change" : "input", { bubbles: true }));
  });
  const click = async (selector: string | HTMLElement) => act(async () => (typeof selector === "string" ? query<HTMLButtonElement>(selector) : selector).click());
  const button = (scope: string, text: string) => all(`${scope} button`).find((candidate) => candidate.textContent === text)!;
  const choosePhoto = async () => act(async () => {
    const input = query<HTMLInputElement>('.medicineDialog input[type="file"]');
    Object.defineProperty(input, "files", { value: [new File([new Uint8Array([1])], "front.jpg", { type: "image/jpeg" })], configurable: true });
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
  const render = async (recognitionAvailable = true, parseAvailable = true) => act(async () => root.render(<Harness recognition={recognitionAvailable} parse={parseAvailable} />));
  const describeMedicine = async (text: string) => {
    await click(button(".medicineEntryMethods", "文字"));
    await type('.medicineDialog textarea[name="description"]', text);
    await click(button(".medicineTextActions", "解析"));
  };
  const submit = () => query<HTMLButtonElement>(".medicineDialog button[type=submit]");

  beforeEach(async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 10, 2, 10, 0));
    localStorage.clear();
    items = [
      item({}),
      item({ id: ID("b"), name: "999感冒灵颗粒", category: "cold", quantity: 5, unit: "sachet", expires_on: "2020-01-31", location: "", usage: "", created_at: "2026-10-05T00:00:00.000Z" }),
      item({ id: ID("d"), name: "复方甘草口服溶液", category: "cough", quantity: 85, unit: "ml", expires_on: "2026-11-20", usage: "", notes: "饭后服用\n每次 10 毫升", created_at: "2026-10-03T00:00:00.000Z" }),
    ];
    api = {
      list: vi.fn(async () => ({ items: structuredClone(items) })),
      save: vi.fn(async (fields: MedicineFields) => {
        const saved = item({ ...fields, id: fields.id || ID("c"), created_at: items.find((current) => current.id === fields.id)?.created_at ?? "2026-11-02T00:00:00.000Z" });
        items = [...items.filter((current) => current.id !== saved.id), saved];
        return saved;
      }),
      remove: vi.fn(async (id: string) => { items = items.filter((current) => current.id !== id); return { deleted: true }; }),
    };
    window.sparkclawMedicineBox = api;
    URL.createObjectURL = vi.fn(() => "blob:photo"); URL.revokeObjectURL = vi.fn();
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    delete window.sparkclawMedicineBox;
    backend.recognizeMedicine.mockReset();
    backend.parseMedicine.mockReset();
    vi.useRealTimers();
  });

  it("shows cards by default with box-wide stats, status badges and sidebar counts", async () => {
    await render();
    expect(texts(".medicineCategory")).toEqual(["全部药品3", "感冒1", "发烧1", "止痛0", "咳嗽1", "肠胃0", "过敏0", "外伤0", "其他0"]);
    expect(texts(".medicineStats > span")).toEqual(["药品总数 3", "30天内过期 1", "已过期 1"]);
    // By expiry: expired, expiring, then in date.
    expect(texts(".medicineCard strong")).toEqual(["999感冒灵颗粒", "复方甘草口服溶液", "布洛芬缓释胶囊"]);
    expect(texts(".medicineCard .medicineBadge")).toEqual(["已过期", "18 天后过期", "未临期"]);
    expect(all(".medicineCard").map((card) => card.className)).toEqual(["medicineCard expired", "medicineCard expiring", "medicineCard ok"]);
    expect(query(".medicineCard.ok .medicineCardFacts").textContent).toBe("有效期2027年12月31日剩余12粒存放位置客厅抽屉");
    expect(query(".medicineCard.expired .medicineCardFacts").textContent).toContain("存放位置未填写");
    // One card has a usage, so every card keeps the row.
    expect(all(".medicineCardUsage")).toHaveLength(3);
    expect(query(".medicineCard.ok").getAttribute("aria-label")).toBe("布洛芬缓释胶囊，未临期");
  });

  it("filters by sidebar category, status and search together, with chips and one clear", async () => {
    await render();
    await click(".medicineCategory:nth-child(5)");
    expect(query(".medicinePage h1").textContent).toBe("咳嗽");
    expect(texts(".medicineCard strong")).toEqual(["复方甘草口服溶液"]);
    expect(texts(".medicineChip")).toEqual(["分类 · 咳嗽"]);
    await click(".medicineCategory:nth-child(1)");
    await click(button(".medicineToolbar", "已过期"));
    expect(texts(".medicineCard strong")).toEqual(["999感冒灵颗粒"]);
    await type(".medicineSearch input", "饭后");
    expect(query(".medicineNoMatches p").textContent).toBe("没有符合当前筛选条件的药品");
    expect(texts(".medicineChip")).toEqual(["状态 · 已过期", "搜索 · 饭后"]);
    await click(button(".medicineToolbar", "全部"));
    expect(texts(".medicineCard strong")).toEqual(["复方甘草口服溶液"]);
    await type(".medicineSearch input", "咳嗽");
    expect(texts(".medicineCard strong")).toEqual(["复方甘草口服溶液"]);
    await click(".medicineCategory:nth-child(2)");
    await click(".medicineClear");
    expect(query(".medicineCategory.active").textContent).toBe("全部药品3");
    expect(query<HTMLInputElement>(".medicineSearch input").value).toBe("");
    expect(host.querySelector(".medicineChips")).toBeNull();
    expect(all(".medicineCard")).toHaveLength(3);
  });

  it("leaves out the usage row when no result has one and keeps the view and sort choices", async () => {
    items = items.map((entry) => ({ ...entry, usage: "" }));
    await render();
    expect(all(".medicineCardUsage")).toHaveLength(0);
    await click('.medicineViewToggle button[aria-label="列表视图"]');
    await click(button(".medicineToolbar", "按添加时间"));
    expect(texts(".medicineRowOpen")).toEqual(["999感冒灵颗粒", "复方甘草口服溶液", "布洛芬缓释胶囊"]);
    expect(localStorage.getItem("sparkclaw.medicine.view")).toBe("list");
    expect(localStorage.getItem("sparkclaw.medicine.sort")).toBe("created");
    await act(async () => root.unmount());
    root = createRoot(host);
    await render();
    expect(host.querySelector(".medicineTable")).not.toBeNull();
    expect(query('.medicineSegments button[aria-pressed="true"]:not([aria-label])').textContent).toBe("全部");
    expect(all('.medicineToolbar button[aria-pressed="true"]').map((element) => element.textContent || element.getAttribute("aria-label"))).toEqual(["全部", "列表视图", "按添加时间"]);
  });

  it("keeps same-name medicines together with a count, a list header and totals per unit", async () => {
    items.push(item({ id: ID("e"), quantity: 1, unit: "box", expires_on: "2026-11-25", location: "", created_at: "2026-10-20T00:00:00.000Z" }));
    await render();
    expect(texts(".medicineCard strong")).toEqual(["999感冒灵颗粒", "复方甘草口服溶液", "布洛芬缓释胶囊", "布洛芬缓释胶囊"]);
    expect(texts(".medicineGroupTag")).toEqual(["共 2 条", "共 2 条"]);
    await click('.medicineViewToggle button[aria-label="列表视图"]');
    expect(texts(".medicineGroupRow")).toEqual(["布洛芬缓释胶囊 · 共 2 条 · 合计 1盒 + 12粒"]);
    expect(texts(".medicineRowQuantity")).toEqual(["5袋", "85毫升", "1盒", "12粒"]);
    // Filtering first: the group shows only what is left.
    await type(".medicineSearch input", "客厅");
    expect(host.querySelector(".medicineGroupRow")).toBeNull();
  });

  it("recognizes a front photo, then has the owner enter the quantity and expiry before confirming", async () => {
    backend.recognizeMedicine.mockResolvedValue(recognition({ name: "复方氨酚烷胺胶囊", category: "cold", unit: "capsule", usage: "感冒发热" }));
    await render();
    await click(".medicineSidebar .sidebarNewConversation");
    expect(query('.medicineSteps [aria-current="step"]').textContent).toBe("1录入");
    expect(query(".medicinePrivacy").textContent).toBe("照片只用于识别，不会保存。剩余数量和有效期请在下一步填写。");
    await choosePhoto();
    expect(backend.recognizeMedicine).toHaveBeenCalledWith({ image: "cGhvdG8=", content_type: "image/jpeg" }, expect.any(AbortSignal));
    expect(query('.medicineSteps [aria-current="step"]').textContent).toBe("2核对并入库");
    expect(query<HTMLImageElement>(".medicineSourcePhoto img").src).toBe("blob:photo");
    expect(query(".medicineReviewHint").textContent).toBe("以下内容由照片识别／文字解析填入，请逐项核对后确认入库。");
    expect(query<HTMLInputElement>('.medicineDialog input[name="name"]').value).toBe("复方氨酚烷胺胶囊");
    expect(query<HTMLSelectElement>('.medicineDialog select[name="category"]').value).toBe("cold");
    expect(query<HTMLSelectElement>('.medicineDialog select[name="unit"]').value).toBe("capsule");
    expect(query<HTMLInputElement>('.medicineDialog input[name="usage"]').value).toBe("感冒发热");
    // The photo never fills the quantity, expiry, location or notes.
    for (const name of ["quantity", "expires_on", "location"]) expect(query<HTMLInputElement>(`.medicineDialog input[name="${name}"]`).value).toBe("");
    expect(query<HTMLTextAreaElement>('.medicineDialog textarea[name="notes"]').value).toBe("");
    expect(texts(".medicineCheck")).toEqual(["待核对", "待核对", "单位待核对", "待核对"]);
    expect(document.activeElement?.getAttribute("name")).toBe("quantity");
    expect(api.save).not.toHaveBeenCalled();

    await type('.medicineDialog input[name="quantity"]', "8");
    expect(submit().disabled).toBe(true);
    await click('.medicineDialog input[name="month_only"]');
    await type('.medicineDialog input[name="expires_month"]', "2027-02");
    expect(submit().disabled).toBe(false);
    // Back keeps what was entered.
    await click(".medicineBack");
    await click(".medicineSkip");
    expect(query<HTMLInputElement>('.medicineDialog input[name="quantity"]').value).toBe("8");
    await click(submit());
    expect(api.save).toHaveBeenCalledWith({ id: "", name: "复方氨酚烷胺胶囊", category: "cold", quantity: 8, unit: "capsule", expires_on: "2027-02-28", no_expiry: false,
      location: "", usage: "感冒发热", notes: "" });
    expect(host.querySelector(".medicineDialog")).toBeNull();
    expect(query(".medicineCategory:nth-child(2)").textContent).toBe("感冒2");
  });

  it("lets the owner fill in a medicine by hand, guesses its category and notes same-name entries", async () => {
    backend.recognizeMedicine.mockResolvedValue(recognition({ recognized: false }));
    await render();
    await click(".medicinePage .workbenchPageHeader button");
    await choosePhoto();
    expect(query('.medicineDialog [role="alert"]').textContent).toContain("没能从照片中认出药品");
    expect(query('.medicineSteps [aria-current="step"]').textContent).toBe("1录入");
    await click(".medicineSkip");
    expect(document.activeElement?.getAttribute("name")).toBe("name");
    expect(host.querySelector(".medicineReviewHint")).toBeNull();
    await type('.medicineDialog input[name="name"]', "蒙脱石散");
    expect(query<HTMLSelectElement>('.medicineDialog select[name="category"]').value).toBe("digestive");
    await type('.medicineDialog select[name="unit"]', "sachet");
    await type('.medicineDialog input[name="quantity"]', "6");
    await click('.medicineDialog input[name="no_expiry"]');
    expect(query<HTMLInputElement>('.medicineDialog input[name="expires_on"]').disabled).toBe(true);
    await type('.medicineDialog input[name="name"]', " 布洛芬缓释胶囊");
    expect(query(".medicineSameNameHint").textContent).toBe("药箱里已有 1 条同名药品，保存后会和它们放在一起，分开记录。");
    await click('.medicineDialog input[name="no_expiry"]');
    await type('.medicineDialog input[name="expires_on"]', "2027-12-31");
    expect(query(".medicineSameNameHint").textContent).toContain("其中一条有效期相同，请确认是另外一份。");
    await type('.medicineDialog input[name="name"]', "蒙脱石散");
    await click('.medicineDialog input[name="no_expiry"]');
    await type('.medicineDialog input[name="location"]', " 厨房药盒 ");
    await type('.medicineDialog textarea[name="notes"]', "饭前\n空腹");
    await click(submit());
    expect(api.save).toHaveBeenLastCalledWith({ id: "", name: "蒙脱石散", category: "digestive", quantity: 6, unit: "sachet", expires_on: "", no_expiry: true,
      location: "厨房药盒", usage: "", notes: "饭前\n空腹" });
  });

  it("reads one typed sentence into the review with every stated field marked to check", async () => {
    backend.parseMedicine.mockResolvedValue(parsed({ name: "布洛芬缓释胶囊", category: "fever", unit: "capsule", quantity: 14, expires_on: "2027-12-31", location: "客厅抽屉", usage: "退烧止痛" }));
    await render();
    await click(".medicineSidebar .sidebarNewConversation");
    await describeMedicine("布洛芬缓释胶囊，还剩 14 粒，\n2027 年 12 月到期，放在客厅抽屉，退烧止痛");
    expect(backend.parseMedicine).toHaveBeenCalledWith({ text: "布洛芬缓释胶囊，还剩 14 粒， 2027 年 12 月到期，放在客厅抽屉，退烧止痛", today: "2026-11-02" }, expect.any(AbortSignal));
    expect(query('.medicineSteps [aria-current="step"]').textContent).toBe("2核对并入库");
    expect(query(".medicineSourceText p").textContent).toBe("布洛芬缓释胶囊，还剩 14 粒， 2027 年 12 月到期，放在客厅抽屉，退烧止痛");
    expect(host.querySelector(".medicineSourcePhoto")).toBeNull();
    expect(query<HTMLInputElement>('.medicineDialog input[name="quantity"]').value).toBe("14");
    expect(query<HTMLInputElement>('.medicineDialog input[name="expires_on"]').value).toBe("2027-12-31");
    expect(query<HTMLInputElement>('.medicineDialog input[name="location"]').value).toBe("客厅抽屉");
    expect(query<HTMLTextAreaElement>('.medicineDialog textarea[name="notes"]').value).toBe("");
    expect(query<HTMLInputElement>('.medicineDialog input[name="no_expiry"]').checked).toBe(false);
    expect(all(".medicineFieldFlash")).toHaveLength(6);
    expect(texts(".medicineCheck")).toHaveLength(6);
    expect(document.activeElement?.getAttribute("name")).toBe("name");
    // Editing a field marks it checked.
    await type('.medicineDialog input[name="location"]', "卧室");
    expect(texts(".medicineCheck")).toHaveLength(5);
    expect(api.save).not.toHaveBeenCalled();
    await click(submit());
    expect(api.save).toHaveBeenCalledWith({ id: "", name: "布洛芬缓释胶囊", category: "fever", quantity: 14, unit: "capsule", expires_on: "2027-12-31", no_expiry: false,
      location: "卧室", usage: "退烧止痛", notes: "" });
  });

  it("leaves unstated fields empty and keeps the owner on step 1 for several, none or an unavailable reader", async () => {
    await render();
    await click(".medicineSidebar .sidebarNewConversation");
    backend.parseMedicine.mockResolvedValueOnce({ recognized: false, multiple: true });
    await describeMedicine("布洛芬和蒙脱石散各一盒");
    expect(query('.medicineDialog [role="alert"]').textContent).toBe("一次只能录入一种药品，请分开录入。");
    expect(query('.medicineSteps [aria-current="step"]').textContent).toBe("1录入");
    backend.parseMedicine.mockResolvedValueOnce(parsed({ recognized: false }));
    await click(button(".medicineTextActions", "解析"));
    expect(query('.medicineDialog [role="alert"]').textContent).toBe("没能从文字里认出药品，请写出药品名称，或直接填写。");
    backend.parseMedicine.mockRejectedValueOnce(new Error("503"));
    await click(button(".medicineTextActions", "解析"));
    expect(query('.medicineDialog [role="alert"]').textContent).toBe("解析服务暂不可用，可以直接填写。");
    expect(query<HTMLTextAreaElement>('.medicineDialog textarea[name="description"]').value).toBe("布洛芬和蒙脱石散各一盒");

    backend.parseMedicine.mockResolvedValueOnce(parsed({ name: "蒙脱石散", category: "digestive", unit: "sachet" }));
    await act(async () => {
      query('.medicineDialog textarea[name="description"]').dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", metaKey: true, bubbles: true }));
    });
    expect(query('.medicineSteps [aria-current="step"]').textContent).toBe("2核对并入库");
    for (const name of ["quantity", "expires_on", "location", "usage"]) expect(query<HTMLInputElement>(`.medicineDialog input[name="${name}"]`).value).toBe("");
    expect(texts(".medicineCheck")).toEqual(["待核对", "待核对", "单位待核对"]);
    expect(document.activeElement?.getAttribute("name")).toBe("quantity");
  });

  it("keeps the Text segment but disables it without the reader, and defaults to it without recognition", async () => {
    await render(true, false);
    await click(".medicineSidebar .sidebarNewConversation");
    const textSegment = button(".medicineEntryMethods", "文字") as HTMLButtonElement;
    expect(textSegment.disabled).toBe(true);
    expect(textSegment.title).toBe("解析服务暂不可用，可以直接填写。");
    await click(button(".medicineDialog", "取消"));

    await render(false, true);
    await click(".medicineSidebar .sidebarNewConversation");
    expect(query('.medicineSteps [aria-current="step"]').textContent).toBe("1录入");
    expect(query('.medicineEntryMethods [aria-pressed="true"]').textContent).toBe("文字");
    expect(document.activeElement?.getAttribute("name")).toBe("description");
  });

  it("stores the same record whether a medicine came in by photo, by text or by hand", async () => {
    backend.recognizeMedicine.mockResolvedValue(recognition({ name: "蒙脱石散", category: "digestive", unit: "sachet" }));
    backend.parseMedicine.mockResolvedValue(parsed({ name: "蒙脱石散", category: "digestive", unit: "sachet", quantity: 6, location: "厨房" }));
    await render();
    const finish = async (fill: string[]) => {
      if (fill.includes("quantity")) await type('.medicineDialog input[name="quantity"]', "6");
      if (fill.includes("location")) await type('.medicineDialog input[name="location"]', "厨房");
      await type('.medicineDialog input[name="expires_on"]', "2027-06-30");
      await click(submit());
    };
    await click(".medicineSidebar .sidebarNewConversation");
    await choosePhoto();
    await finish(["quantity", "location"]);
    await click(".medicineSidebar .sidebarNewConversation");
    await describeMedicine("蒙脱石散还有 6 袋，放在厨房");
    await finish([]);
    await click(".medicineSidebar .sidebarNewConversation");
    await click(".medicineSkip");
    await type('.medicineDialog input[name="name"]', "蒙脱石散");
    await type('.medicineDialog select[name="unit"]', "sachet");
    await finish(["quantity", "location"]);
    const saved = api.save.mock.calls.map(([fields]) => fields);
    expect(saved).toHaveLength(3);
    expect(saved[1]).toEqual(saved[0]);
    expect(saved[2]).toEqual(saved[0]);
    expect(saved[0]).toEqual({ id: "", name: "蒙脱石散", category: "digestive", quantity: 6, unit: "sachet", expires_on: "2027-06-30", no_expiry: false, location: "厨房", usage: "", notes: "" });
  });

  it("starts at the review form when neither entry is available and keeps the photo entry disabled", async () => {
    await render(false, false);
    await click(".medicineSidebar .sidebarNewConversation");
    expect(query('.medicineSteps [aria-current="step"]').textContent).toBe("2核对并入库");
    await click(".medicineSteps li:first-child button");
    expect(query<HTMLButtonElement>(".medicinePhotoButton").disabled).toBe(true);
    expect(query(".medicinePhotoDrop small").textContent).toBe("识别服务暂不可用，可以直接填写。");
    await click(button(".medicineDialog", "取消"));

    backend.recognizeMedicine.mockRejectedValue(new Error("503"));
    await render(true, false);
    await click(".medicineSidebar .sidebarNewConversation");
    await choosePhoto();
    expect(query('.medicineDialog [role="alert"]').textContent).toBe("识别服务暂不可用，可以直接填写。");
    await click(".medicineSkip");
    expect(query<HTMLInputElement>('.medicineDialog input[name="name"]').disabled).toBe(false);
  });

  it("opens details from a card and adjusts the quantity by one unit through the same save as an edit", async () => {
    await render();
    await click(".medicineCard.ok");
    expect(query("#medicine-detail-title").textContent).toBe("布洛芬缓释胶囊");
    expect(query(".medicineBanner").textContent).toBe("状态良好。");
    expect(texts(".medicineDetailSection p")).toEqual(["退烧、止痛", "未填写"]);
    await click('.medicineStep[aria-label="减少 1 粒"]');
    expect(api.save).toHaveBeenLastCalledWith({ ...fieldsOf(items.find((entry) => entry.id === ID("a"))!), quantity: 11 });
    expect(api.save.mock.calls.at(-1)![0]).toEqual({ ...fieldsOf(item({})), quantity: 11 });
    expect(query(".medicineQuantityCard [aria-live]").textContent).toBe("11粒");
    expect(query(".medicineCard.ok .medicineCardFacts").textContent).toContain("剩余11粒");

    // A failed save goes back to the previous number with a message.
    api.save.mockRejectedValueOnce(new Error("disk"));
    await click('.medicineStep[aria-label="增加 1 粒"]');
    expect(query(".medicineQuantityCard [aria-live]").textContent).toBe("11粒");
    expect(query('.medicineDetail [role="alert"]').textContent).toBe("剩余数量没有保存，请重试。");
  });

  it("shows used up at 0, disables the decrease and gives millilitres no quick buttons", async () => {
    items[0] = { ...items[0], quantity: 1 };
    await render();
    await click(".medicineCard.ok");
    await click('.medicineStep[aria-label="减少 1 粒"]');
    expect(query(".medicineQuantityCard [aria-live]").textContent).toBe("已用完");
    expect(query<HTMLButtonElement>('.medicineStep[aria-label="减少 1 粒"]').disabled).toBe(true);
    expect(query(".medicineCard.ok .medicineUsedUp").textContent).toBe("已用完");
    await click(".medicineDetailClose");
    await click(".medicineCard.expiring");
    expect(query(".medicineBanner").textContent).toBe("这个药品将在 18 天内过期，请尽快使用或更换。");
    expect(host.querySelector(".medicineStep")).toBeNull();
    expect(query(".medicineNotes").textContent).toBe("饭后服用\n每次 10 毫升");
  });

  it("switches between same-name entries and edits or deletes from the detail dialog", async () => {
    items.push(item({ id: ID("e"), quantity: 3, expires_on: "", location: "", created_at: "2026-10-20T00:00:00.000Z" }));
    await render();
    await click(".medicineCard.ok");
    expect(texts(".medicineSameName button")).toEqual(["未填写3粒未填写"]);
    await click(".medicineSameName button");
    expect(query(".medicineBanner").textContent).toBe("还没有填写有效期，建议补上。");
    // An earlier row without an expiry date must get one when it is edited.
    await click(button(".medicineDetail", "编辑"));
    expect(host.querySelector(".medicineDetail")).toBeNull();
    expect(host.querySelector(".medicineSteps")).toBeNull();
    expect(document.activeElement?.getAttribute("name")).toBe("quantity");
    expect(submit().disabled).toBe(true);
    await type('.medicineDialog input[name="expires_on"]', "2028-03-31");
    await click(submit());
    expect(api.save).toHaveBeenLastCalledWith(expect.objectContaining({ id: ID("e"), expires_on: "2028-03-31", no_expiry: false, quantity: 3 }));

    const confirm = vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValueOnce(true);
    await click(".medicineCard.expired");
    await click(button(".medicineDetail", "删除"));
    expect(api.remove).not.toHaveBeenCalled();
    expect(host.querySelector(".medicineDetail")).not.toBeNull();
    await click(button(".medicineDetail", "删除"));
    expect(api.remove).toHaveBeenCalledWith(ID("b"));
    expect(host.querySelector(".medicineDetail")).toBeNull();
    expect(texts(".medicineCard strong")).not.toContain("999感冒灵颗粒");
    confirm.mockRestore();
  });

  it("keeps the list view's inline edit and delete and opens details from a row", async () => {
    await render();
    await click('.medicineViewToggle button[aria-label="列表视图"]');
    await click('.medicineRow.ok button[aria-label^="编辑"]');
    expect(host.querySelector(".medicinePhotoDrop")).toBeNull();
    await type('.medicineDialog input[name="quantity"]', "3.5");
    await click(submit());
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ id: ID("a"), quantity: 3.5, category: "fever", location: "客厅抽屉" }));

    await click(".medicineRow.expired .medicineRowCategory");
    expect(query("#medicine-detail-title").textContent).toBe("999感冒灵颗粒");
    await click(".medicineDetailClose");

    const confirm = vi.spyOn(window, "confirm").mockReturnValueOnce(true);
    await click('.medicineRow.expired button[aria-label^="删除"]');
    expect(api.remove).toHaveBeenCalledWith(ID("b"));
    expect(host.querySelector(".medicineDetail")).toBeNull();
    expect(texts(".medicineRowOpen")).not.toContain("999感冒灵颗粒");
    confirm.mockRestore();
  });

  it("shows the empty box with its add entry and a read failure with retry", async () => {
    items = [];
    await render();
    expect(query(".medicineEmpty h2").textContent).toBe("药箱里还没有药品");
    expect(query(".medicineEmpty p").textContent).toBe("家中常备药品，按功能分类管理。");
    await click(".medicineEmpty .secondaryButton");
    expect(host.querySelector(".medicineDialog")).not.toBeNull();
    await click(button(".medicineDialog", "取消"));

    api.list.mockRejectedValueOnce(new Error("locked"));
    await act(async () => root.unmount());
    root = createRoot(host);
    await render();
    expect(query(".localFeedback p").textContent).toBe("药箱暂时无法读取。");
    items = [item({})];
    await click(".localFeedback button");
    expect(host.querySelector(".localFeedback")).toBeNull();
    expect(all(".medicineCard")).toHaveLength(1);
  });
});
