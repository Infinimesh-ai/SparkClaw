// Prepares a front-of-package photo for recognition: decoded and redrawn as a
// JPEG whose long edge is at most 1600 px, which keeps the label legible while
// staying far below the backend's 4 MiB photo limit. Metadata such as the
// camera location is not carried over.
const LONG_EDGE = 1600;
const QUALITY = 0.85;

export async function prepareMedicinePhoto(file: Blob): Promise<{ image: string; content_type: "image/jpeg" }> {
  const bitmap = await createImageBitmap(file, { imageOrientation: "from-image" });
  try {
    const scale = Math.min(1, LONG_EDGE / Math.max(bitmap.width, bitmap.height));
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(bitmap.width * scale));
    canvas.height = Math.max(1, Math.round(bitmap.height * scale));
    const context = canvas.getContext("2d");
    if (!context) throw new Error("Canvas is unavailable");
    context.fillStyle = "#fff";
    context.fillRect(0, 0, canvas.width, canvas.height);
    context.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    const jpeg = await new Promise<Blob>((resolve, reject) => canvas.toBlob((blob) => blob ? resolve(blob) : reject(new Error("Photo could not be encoded")), "image/jpeg", QUALITY));
    const bytes = new Uint8Array(await jpeg.arrayBuffer());
    let binary = "";
    for (let offset = 0; offset < bytes.length; offset += 0x8000) binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
    return { image: btoa(binary), content_type: "image/jpeg" };
  } finally {
    bitmap.close();
  }
}
