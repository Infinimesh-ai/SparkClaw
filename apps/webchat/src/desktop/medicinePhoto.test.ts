// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { MEDICINE_PHOTO_BASE64_BUDGET, prepareMedicinePhoto } from "./medicinePhoto";

// A stand-in encoder whose JPEG size grows with the pixel count and quality.
function stubCanvas(bytesFor: (width: number, height: number, quality: number) => number) {
  const encoded: Array<[number, number, number]> = [];
  const bitmap = { width: 4000, height: 3000, close: vi.fn() };
  vi.stubGlobal("createImageBitmap", vi.fn(async () => bitmap));
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({ fillRect: vi.fn(), drawImage: vi.fn(), fillStyle: "" } as unknown as CanvasRenderingContext2D);
  vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation(function (this: HTMLCanvasElement, callback, _type, quality) {
    encoded.push([this.width, this.height, quality as number]);
    callback(new Blob([new Uint8Array(bytesFor(this.width, this.height, quality as number))], { type: "image/jpeg" }));
  });
  return { encoded, bitmap };
}

describe("prepareMedicinePhoto", () => {
  afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

  it("shrinks the photo until it fits one ISCP request", async () => {
    const { encoded, bitmap } = stubCanvas((width, height, quality) => Math.round(width * height * quality * 0.12));
    const photo = await prepareMedicinePhoto(new Blob());
    expect(photo.content_type).toBe("image/jpeg");
    expect(photo.image.length).toBeLessThanOrEqual(MEDICINE_PHOTO_BASE64_BUDGET);
    expect(encoded[0]).toEqual([1024, 768, 0.7]);
    // 896 × 672 at 0.55 is the first step under the budget.
    expect(encoded).toEqual([[1024, 768, 0.7], [1024, 768, 0.55], [896, 672, 0.55]]);
    expect(bitmap.close).toHaveBeenCalled();
  });

  it("keeps a small photo at its first step and refuses one that never fits", async () => {
    stubCanvas(() => 20_000);
    expect((await prepareMedicinePhoto(new Blob())).image.length).toBe(Math.ceil(20_000 / 3) * 4);
    vi.restoreAllMocks();
    const { bitmap } = stubCanvas(() => 200_000);
    await expect(prepareMedicinePhoto(new Blob())).rejects.toThrow("too detailed");
    expect(bitmap.close).toHaveBeenCalled();
  });
});
