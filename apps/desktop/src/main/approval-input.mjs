import crypto from "node:crypto";

const object = (value) =>
  value !== null && typeof value === "object" && !Array.isArray(value);
// Serialize sorted keys directly: JSON.stringify would reorder integer-like
// property names even after inserting them in Go's lexicographic map order.
const canonical = (value) =>
  object(value)
    ? `{${Object.keys(value)
        .sort()
        .map((key) => `${JSON.stringify(key)}:${canonical(value[key])}`)
        .join(",")}}`
    : JSON.stringify(value);

export function approvalInputs(form, decision, input) {
  if (!form || decision === "reject") {
    if (input !== undefined) throw new Error("Unexpected approval inputs");
    return { digest: "", json: "", payload: {} };
  }
  if (
    form.schema_version !== 1 ||
    !object(input) ||
    !Object.values(input).every(
      (entry) =>
        object(entry) &&
        Object.values(entry).every(
          (value) =>
            typeof value === "string" ||
            (typeof value === "number" && Number.isFinite(value)),
        ),
    ) ||
    Buffer.byteLength(JSON.stringify(input)) > 32 * 1024
  ) {
    throw new Error("Complete this approval using a supported client");
  }
  const json = canonical(input).replace(
    /[<>&\u2028\u2029]/gu,
    (character) =>
      `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`,
  );
  return {
    json,
    digest: crypto.createHash("sha256").update(json).digest("hex"),
    payload: { input_schema_version: 1, inputs: JSON.parse(json) },
  };
}
