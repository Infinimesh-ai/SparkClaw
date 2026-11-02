// Prepares a front-of-package photo for recognition: decoded and redrawn as a
// JPEG small enough to travel inline in one ISCP request, whose body is capped
// at 63488 bytes (`limit_bytes` of medicine.recognize). Each step shrinks the
// long edge or the quality until the base64 photo fits; the large name on a
// package front stays legible at 512 px. Metadata such as the camera location
// is not carried over.
const STEPS: Array<[longEdge: number, quality: number]> = [[1024, 0.7], [1024, 0.55], [896, 0.55], [768, 0.5], [640, 0.5], [512, 0.5], [512, 0.4]];
// The request body also carries the JSON keys and the content type.
export const MEDICINE_PHOTO_BASE64_BUDGET = 61_000;

function encode(bitmap: ImageBitmap, longEdge: number, quality: number) {
  const scale = Math.min(1, longEdge / Math.max(bitmap.width, bitmap.height));
  const canvas = document.createElement("canvas");
  canvas.width = Math.max(1, Math.round(bitmap.width * scale));
  canvas.height = Math.max(1, Math.round(bitmap.height * scale));
  const context = canvas.getContext("2d");
  if (!context) throw new Error("Canvas is unavailable");
  context.fillStyle = "#fff";
  context.fillRect(0, 0, canvas.width, canvas.height);
  context.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  return new Promise<Blob>((resolve, reject) => canvas.toBlob((blob) => blob ? resolve(blob) : reject(new Error("Photo could not be encoded")), "image/jpeg", quality));
}

export async function prepareMedicinePhoto(file: Blob): Promise<{ image: string; content_type: "image/jpeg" }> {
  const bitmap = await createImageBitmap(file, { imageOrientation: "from-image" });
  try {
    for (const [longEdge, quality] of STEPS) {
      const jpeg = await encode(bitmap, longEdge, quality);
      if (Math.ceil(jpeg.size / 3) * 4 > MEDICINE_PHOTO_BASE64_BUDGET) continue;
      const bytes = new Uint8Array(await jpeg.arrayBuffer());
      let binary = "";
      for (let offset = 0; offset < bytes.length; offset += 0x8000) binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
      return { image: btoa(binary), content_type: "image/jpeg" };
    }
    throw new Error("Photo is too detailed to send");
  } finally {
    bitmap.close();
  }
}
