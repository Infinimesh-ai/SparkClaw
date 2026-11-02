// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MedicineSidebar, MedicineWorkspace, useMedicineBox } from "./medicineBox";
import type { MedicineBoxAPI, MedicineFields, MedicineItem, MedicineRecognition } from "../desktop/medicineBox";

const backend = vi.hoisted(() => ({ recognizeMedicine: vi.fn() }));
vi.mock("../api/client", () => ({ api: backend }));
vi.mock("../desktop/medicinePhoto", () => ({ prepareMedicinePhoto: vi.fn(async () => ({ image: "cGhvdG8=", content_type: "image/jpeg" })) }));

const item = (fields: Partial<MedicineItem>): MedicineItem => ({ id: "med_" + "a".repeat(32), name: "布洛芬缓释胶囊", spec: "0.3g×20粒", manufacturer: "中美天津史克",
  category: "fever", quantity: 12, unit: "capsule", expires_on: "", created_at: "", updated_at: "", ...fields });
const recognition = (fields: Partial<MedicineRecognition>): MedicineRecognition => ({ recognized: true, name: "", spec: "", manufacturer: "", category: "other", unit: "tablet", ...fields });

function Harness({ recognition }: { recognition: boolean }) {
  const box = useMedicineBox(true, recognition);
  return <><MedicineSidebar language="zh" box={box} /><MedicineWorkspace language="zh" box={box} /></>;
}

describe("medicine box", () => {
  let host: HTMLDivElement, root: ReturnType<typeof createRoot>, items: MedicineItem[], api: MedicineBoxAPI & { save: ReturnType<typeof vi.fn>; remove: ReturnType<typeof vi.fn> };
  const query = <T extends Element = HTMLElement>(selector: string) => host.querySelector<T>(selector)!;
  const type = async (selector: string, value: string) => act(async () => {
    const input = query<HTMLInputElement>(selector);
    Object.getOwnPropertyDescriptor(Object.getPrototypeOf(input), "value")!.set!.call(input, value);
    input.dispatchEvent(new Event(input instanceof HTMLSelectElement ? "change" : "input", { bubbles: true }));
  });
  const click = async (selector: string) => act(async () => query<HTMLButtonElement>(selector).click());
  const choosePhoto = async () => act(async () => {
    const input = query<HTMLInputElement>('.medicineDialog input[type="file"]');
    Object.defineProperty(input, "files", { value: [new File([new Uint8Array([1])], "front.jpg", { type: "image/jpeg" })], configurable: true });
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
  const render = async (recognitionAvailable = true) => act(async () => root.render(<Harness recognition={recognitionAvailable} />));

  beforeEach(async () => {
    items = [item({}), item({ id: "med_" + "b".repeat(32), name: "999感冒灵颗粒", spec: "10g×9袋", manufacturer: "", category: "cold", quantity: 5, unit: "sachet", expires_on: "2020-01-31" })];
    api = {
      list: vi.fn(async () => ({ items: structuredClone(items) })),
      save: vi.fn(async (fields: MedicineFields) => {
        const saved = item({ ...fields, id: fields.id || "med_" + "c".repeat(32) });
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
  });

  it("lists medicines and filters them by function with counts in the sidebar", async () => {
    await render();
    expect([...host.querySelectorAll(".medicineCategory")].map((element) => element.textContent))
      .toEqual(["全部药品2", "感冒1", "发烧1", "止痛0", "咳嗽0", "肠胃0", "过敏0", "外伤0", "其他0"]);
    expect([...host.querySelectorAll(".medicineRowName strong")].map((element) => element.textContent)).toEqual(["布洛芬缓释胶囊", "999感冒灵颗粒"]);
    expect(query(".medicineRow:nth-child(2) .medicineExpiry").className).toContain("expired");
    expect(query(".medicineRow .medicineRowQuantity").textContent).toBe("剩余12粒");
    expect(query(".medicineRow .medicineRowMeta small").textContent).toBe("中美天津史克");

    await click(".medicineCategory:nth-child(2)");
    expect(query(".medicinePage h1").textContent).toBe("感冒");
    expect([...host.querySelectorAll(".medicineRowName strong")].map((element) => element.textContent)).toEqual(["999感冒灵颗粒"]);
    await click(".medicineCategory:nth-child(4)");
    expect(query(".medicineEmpty h2").textContent).toBe("这个分类下还没有药品");
  });

  it("recognizes a front photo, sorts it by function and stores the quantity the owner enters", async () => {
    backend.recognizeMedicine.mockResolvedValue(recognition({ name: "复方氨酚烷胺胶囊", spec: "12粒", manufacturer: "吴太感康", category: "cold", unit: "capsule" }));
    await render();
    await click(".medicineSidebar .sidebarNewConversation");
    await choosePhoto();
    expect(backend.recognizeMedicine).toHaveBeenCalledWith({ image: "cGhvdG8=", content_type: "image/jpeg" }, expect.any(AbortSignal));
    expect(query(".medicineStatus").textContent).toBe("已识别为“复方氨酚烷胺胶囊”，归入感冒。请核对并填写剩余数量。");
    expect(query<HTMLSelectElement>('.medicineDialog select[name="category"]').value).toBe("cold");
    expect(query<HTMLSelectElement>('.medicineDialog select[name="unit"]').value).toBe("capsule");
    expect(query<HTMLButtonElement>(".medicineDialog button[type=submit]").disabled).toBe(true);
    await type('.medicineDialog input[name="quantity"]', "8");
    await click(".medicineDialog button[type=submit]");
    expect(api.save).toHaveBeenCalledWith({ id: "", name: "复方氨酚烷胺胶囊", spec: "12粒", manufacturer: "吴太感康", category: "cold", quantity: 8, unit: "capsule", expires_on: "" });
    expect(host.querySelector(".medicineDialog")).toBeNull();
    expect(query(".medicineCategory:nth-child(2)").textContent).toBe("感冒2");
  });

  it("lets the owner fill in a medicine the photo did not show and guesses its category from the name", async () => {
    backend.recognizeMedicine.mockResolvedValue(recognition({ recognized: false }));
    await render();
    await click(".medicinePage .workbenchPageHeader button");
    await choosePhoto();
    expect(query('.medicineDialog [role="alert"]').textContent).toContain("没能从照片中认出药品");
    await type('.medicineDialog input[name="name"]', "蒙脱石散");
    expect(query<HTMLSelectElement>('.medicineDialog select[name="category"]').value).toBe("digestive");
    await type('.medicineDialog select[name="unit"]', "sachet");
    await type('.medicineDialog input[name="quantity"]', "6");
    await click(".medicineDialog button[type=submit]");
    expect(api.save).toHaveBeenLastCalledWith(expect.objectContaining({ id: "", name: "蒙脱石散", category: "digestive", quantity: 6, unit: "sachet" }));
  });

  it("keeps manual entry when recognition is unavailable or fails", async () => {
    await render(false);
    await click(".medicineSidebar .sidebarNewConversation");
    expect(query<HTMLButtonElement>(".medicinePhotoButton").disabled).toBe(true);
    expect(query(".medicinePhotoDrop small").textContent).toBe("识别服务暂不可用，可以直接填写。");
    await click(".medicineDialog .secondaryButton:not(.medicinePhotoButton)");

    backend.recognizeMedicine.mockRejectedValue(new Error("503"));
    await render(true);
    await click(".medicineSidebar .sidebarNewConversation");
    await choosePhoto();
    expect(query('.medicineDialog [role="alert"]').textContent).toBe("识别服务暂不可用，可以直接填写。");
    expect(query<HTMLInputElement>('.medicineDialog input[name="name"]').disabled).toBe(false);
  });

  it("edits the remaining quantity of a medicine and deletes one after confirmation", async () => {
    await render();
    await click('.medicineRow:first-child button[aria-label^="编辑"]');
    expect(host.querySelector(".medicinePhotoDrop")).toBeNull();
    await type('.medicineDialog input[name="quantity"]', "3.5");
    await click(".medicineDialog button[type=submit]");
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ id: "med_" + "a".repeat(32), quantity: 3.5, category: "fever" }));

    const confirm = vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValueOnce(true);
    await click('.medicineRow:last-child button[aria-label^="删除"]');
    expect(api.remove).not.toHaveBeenCalled();
    const name = query(".medicineRow:last-child .medicineRowName strong").textContent;
    await click('.medicineRow:last-child button[aria-label^="删除"]');
    expect(api.remove).toHaveBeenCalledTimes(1);
    expect([...host.querySelectorAll(".medicineRowName strong")].map((element) => element.textContent)).not.toContain(name);
    confirm.mockRestore();
  });
});
