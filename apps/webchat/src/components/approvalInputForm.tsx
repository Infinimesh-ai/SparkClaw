import type { Copy } from "../i18n";
export type ApprovalInputValues = Record<
  string,
  Record<string, string | number>
>;
export type ApprovalInputField = {
  name: string;
  label: string;
  type: "text" | "number" | "select" | "date_or_none";
  required: boolean;
  options?: Array<{
    value: string;
    label: string;
  }>;
  min?: number;
  max?: number;
  max_length?: number;
};
export type ApprovalInputForm = {
  schema_version: number;
  entries: Array<{
    id: string;
    label: string;
    summary: string;
    fields: ApprovalInputField[] | null;
  }>;
};
export function supportedApprovalForm(form: ApprovalInputForm) {
  if (
    form?.schema_version !== 1 ||
    !Array.isArray(form.entries) ||
    form.entries.length < 1 ||
    form.entries.length > 20
  )
    return false;
  const seen = new Set<string>();
  return form.entries.every((entry) => {
    if (
      !entry ||
      typeof entry.id !== "string" ||
      !entry.id ||
      seen.has(entry.id) ||
      typeof entry.label !== "string" ||
      typeof entry.summary !== "string" ||
      (entry.fields !== null &&
        (!Array.isArray(entry.fields) || entry.fields.length > 20))
    )
      return false;
    seen.add(entry.id);
    const fields = new Set<string>();
    return (entry.fields ?? []).every((field) => {
      if (
        !field ||
        typeof field.name !== "string" ||
        !field.name ||
        fields.has(field.name) ||
        typeof field.label !== "string" ||
        !["text", "number", "select", "date_or_none"].includes(field.type) ||
        typeof field.required !== "boolean"
      )
        return false;
      fields.add(field.name);
      return (
        field.type !== "select" ||
        (Array.isArray(field.options) &&
          field.options.length > 0 &&
          field.options.length <= 2000 &&
          field.options.every(
            (option) =>
              typeof option.value === "string" &&
              typeof option.label === "string",
          ))
      );
    });
  });
}
export function approvalFieldValid(field: ApprovalInputField, value: unknown) {
  if (value === undefined || value === "") return !field.required;
  if (field.type === "number")
    return (
      typeof value === "number" &&
      Number.isFinite(value) &&
      Math.abs(value * 100 - Math.round(value * 100)) < 1e-7 &&
      (field.min === undefined || value >= field.min) &&
      (field.max === undefined || value <= field.max)
    );
  if (
    typeof value !== "string" ||
    value.length > (field.max_length ?? 500) ||
    /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/u.test(value)
  )
    return false;
  if (field.type === "select")
    return field.options?.some((option) => option.value === value) ?? false;
  if (field.type === "date_or_none")
    return (
      value === "no_expiry" ||
      (/^\d{4}-\d{2}-\d{2}$/u.test(value) &&
        !Number.isNaN(Date.parse(value)) &&
        new Date(value).toISOString().slice(0, 10) === value)
    );
  return !field.required || value.trim().length > 0;
}
export function approvalFormValid(
  form: ApprovalInputForm,
  values: ApprovalInputValues,
) {
  return (
    supportedApprovalForm(form) &&
    form.entries.every((entry) =>
      (entry.fields ?? []).every((field) =>
        approvalFieldValid(field, values[entry.id]?.[field.name]),
      ),
    )
  );
}
export function ApprovalFields({
  form,
  values,
  onChange,
  disabled,
  text,
}: {
  form: ApprovalInputForm;
  values: ApprovalInputValues;
  onChange: (values: ApprovalInputValues) => void;
  disabled: boolean;
  text: Copy;
}) {
  const copy = text.approval.composer;
  const change = (id: string, field: string, value: string | number) =>
    onChange({ ...values, [id]: { ...values[id], [field]: value } });
  return (
    <div className="approvalInputForm">
      {form.entries.map((entry) => (
        <fieldset key={entry.id} disabled={disabled}>
          <legend>{entry.label}</legend>
          <p>{entry.summary}</p>
          {(entry.fields ?? []).map((field) => {
            const value = values[entry.id]?.[field.name];
            const invalid =
              value !== undefined && !approvalFieldValid(field, value);
            const shared = {
              "aria-invalid": invalid,
              required: field.required,
            };
            return (
              <div className="approvalInputField" key={field.name}>
                <label>
                  <span>
                    {field.label}
                    {field.required ? " *" : ""}
                  </span>
                  {field.type === "select" ? (
                    <select
                      {...shared}
                      value={value ?? ""}
                      onChange={(event) =>
                        change(entry.id, field.name, event.target.value)
                      }
                    >
                      <option value="">{copy.chooseValue}</option>
                      {field.options?.map((option) => (
                        <option key={option.value} value={option.value}>
                          {option.label}
                        </option>
                      ))}
                    </select>
                  ) : field.type === "date_or_none" ? (
                    <input
                      {...shared}
                      type="date"
                      disabled={disabled || value === "no_expiry"}
                      value={value === "no_expiry" ? "" : (value ?? "")}
                      onChange={(event) =>
                        change(entry.id, field.name, event.target.value)
                      }
                    />
                  ) : (
                    <input
                      {...shared}
                      type={field.type === "number" ? "number" : "text"}
                      value={value ?? ""}
                      min={field.min}
                      max={field.max}
                      step={field.type === "number" ? "0.01" : undefined}
                      maxLength={field.max_length ?? 500}
                      onChange={(event) =>
                        change(
                          entry.id,
                          field.name,
                          field.type === "number" && event.target.value !== ""
                            ? Number(event.target.value)
                            : event.target.value,
                        )
                      }
                    />
                  )}
                </label>
                {field.type === "date_or_none" && (
                  <label>
                    <input
                      type="checkbox"
                      checked={value === "no_expiry"}
                      onChange={(event) =>
                        change(
                          entry.id,
                          field.name,
                          event.target.checked ? "no_expiry" : "",
                        )
                      }
                    />
                    {copy.noExpiry}
                  </label>
                )}
                {invalid && (
                  <p className="compactError" role="alert">
                    {copy.invalidField}
                  </p>
                )}
              </div>
            );
          })}
        </fieldset>
      ))}
    </div>
  );
}
