package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
)

type medicineEditPlan struct {
	Operation string                 `json:"operation"`
	Items     []medicinePlannedEntry `json:"items"`
}
type medicineTarget struct {
	Name      string `json:"name"`
	ExpiresOn string `json:"expires_on,omitempty"`
	Location  string `json:"location,omitempty"`
}
type medicineUnresolved struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}
type medicinePlannedEntry struct {
	Target     *medicineTarget      `json:"target,omitempty"`
	Changes    map[string]any       `json:"changes"`
	Unresolved []medicineUnresolved `json:"unresolved"`
	Evidence   map[string]string    `json:"evidence,omitempty"`
}
type frozenMedicineEntry struct {
	EntryID    string               `json:"entry_id"`
	Plan       medicinePlannedEntry `json:"plan"`
	Candidates []medicine.Item      `json:"candidates,omitempty"`
}
type frozenMedicineDraft struct {
	OperationID string                `json:"operation_id"`
	Operation   string                `json:"operation"`
	Items       []frozenMedicineEntry `json:"items"`
}

var errMedicineMixedOperations = errors.New("同一批次只支持一种新增、修改或删除操作；请拆分请求，整批未写入")

var errMedicineBatchLimit = errors.New("药箱每批支持 1–20 条新增或修改，删除仅支持一条；请拆分请求，整批未写入")

var medicineChangeFields = []string{"name", "category", "quantity", "quantity_delta", "unit", "expires_on", "no_expiry", "location", "usage", "notes", "notes_append"}

func decodeMedicinePlan(raw string) (medicineEditPlan, error) {
	var plan medicineEditPlan
	if len(raw) > 64<<10 {
		return plan, errors.New("medicine plan too large")
	}
	if err := appstream.DecodeStrict([]byte(raw), &plan); err != nil {
		return plan, err
	}
	if plan.Operation == "mixed" {
		return plan, errMedicineMixedOperations
	}
	if len(plan.Items) < 1 || len(plan.Items) > medicine.MaxBatch || !slices.Contains([]string{medicine.OperationCreate, medicine.OperationUpdate, medicine.OperationDelete}, plan.Operation) || plan.Operation == medicine.OperationDelete && len(plan.Items) != 1 {
		return plan, errMedicineBatchLimit
	}
	for _, entry := range plan.Items {
		if entry.Changes == nil || entry.Unresolved == nil {
			return plan, errors.New("missing medicine draft fields")
		}
		for key, value := range entry.Changes {
			if !slices.Contains(medicineChangeFields, key) || value == nil {
				return plan, errors.New("unknown or null medicine field")
			}
		}
		for key := range entry.Evidence {
			if !slices.Contains(medicineChangeFields, key) && key != "target" {
				return plan, errors.New("unknown medicine evidence field")
			}
		}
		for _, u := range entry.Unresolved {
			if (!slices.Contains(medicineChangeFields, u.Field) && u.Field != "target" && u.Field != "expiry") || u.Reason != "missing" && u.Reason != "ambiguous" {
				return plan, errors.New("invalid unresolved medicine field")
			}
		}
		if _, a := entry.Changes["quantity"]; a {
			if _, b := entry.Changes["quantity_delta"]; b {
				return plan, errors.New("quantity and delta conflict")
			}
		}
		if entry.Changes["no_expiry"] == true && medicineString(entry.Changes["expires_on"]) != "" {
			return plan, errors.New("expiry and no_expiry conflict")
		}
		if plan.Operation == medicine.OperationDelete && len(entry.Changes) != 0 {
			return plan, errors.New("delete cannot change fields")
		}
		if _, a := entry.Changes["notes"]; a {
			if _, b := entry.Changes["notes_append"]; b {
				return plan, errors.New("notes changes conflict")
			}
		}
		if plan.Operation != medicine.OperationCreate && (entry.Target == nil || strings.TrimSpace(entry.Target.Name) == "") {
			return plan, errors.New("请写明要修改的药品名称；整批未写入")
		}
	}
	return plan, nil
}
func medicineMonthEnd(value string) string {
	if len(value) != 7 {
		return value
	}
	t, e := time.Parse("2006-01", value)
	if e != nil {
		return value
	}
	return t.AddDate(0, 1, -1).Format("2006-01-02")
}
func medicineInputField(name string) app.ApprovalInputField {
	field := app.ApprovalInputField{Name: name, Label: name, Type: app.ApprovalInputText, Required: true, MaxLength: 80}
	labels := map[string]string{"name": "药品名称", "quantity": "数量", "quantity_delta": "增减数量", "unit": "单位", "expiry": "有效期或无有效期", "location": "存放位置", "usage": "用途", "notes": "备注", "notes_append": "追加备注", "category": "分类", "target": "选择药品记录"}
	if label, ok := labels[name]; ok {
		field.Label = label
	}
	switch name {
	case "quantity", "quantity_delta":
		field.Type = app.ApprovalInputNumber
		min, max := float64(0), float64(100000)
		if name == "quantity_delta" {
			min = -100000
		}
		field.Min = &min
		field.Max = &max
		field.MaxLength = 0
	case "expiry":
		field.Type = app.ApprovalInputDateOrNone
		field.MaxLength = 0
	case "unit", "category":
		field.Type = app.ApprovalInputSelect
		field.MaxLength = 0
		values := medicine.Units
		if name == "category" {
			values = medicine.Categories
		}
		for _, v := range values {
			field.Options = append(field.Options, app.ApprovalInputOption{Value: v, Label: medicineLabel(v)})
		}
	case "notes", "notes_append":
		field.MaxLength = 500
	}
	return field
}
func medicineLabel(value string) string {
	labels := map[string]string{"tablet": "片", "capsule": "粒", "sachet": "袋", "bottle": "瓶", "tube": "支", "patch": "贴", "box": "盒", "ml": "毫升", "g": "克", "cold": "感冒", "fever": "退烧", "pain": "止痛", "cough": "止咳", "digestive": "肠胃", "allergy": "过敏", "wound": "外伤", "other": "其他"}
	if label, ok := labels[value]; ok {
		return label
	}
	return value
}
func freezeMedicinePlan(plan medicineEditPlan, items []medicine.Item, runID, original string) (frozenMedicineDraft, *app.ApprovalInputForm, error) {
	draft := frozenMedicineDraft{OperationID: "medicine_" + runID, Operation: plan.Operation}
	form := &app.ApprovalInputForm{SchemaVersion: app.ApprovalInputSchemaVersion}
	needsInput := false
	for index, p := range plan.Items {
		entry := frozenMedicineEntry{EntryID: fmt.Sprintf("entry_%d", index+1), Plan: p}
		fields := map[string]bool{}
		for _, u := range p.Unresolved {
			key := u.Field
			if u.Reason == "missing" && (key == "location" || key == "usage" || key == "notes") && strings.TrimSpace(p.Evidence[key]) == "" {
				continue
			}
			if key == "category" && plan.Operation == medicine.OperationCreate {
				continue
			}
			if key == "expires_on" || key == "no_expiry" {
				key = "expiry"
			}
			fields[key] = true
			if key == "expiry" {
				delete(entry.Plan.Changes, "expires_on")
				delete(entry.Plan.Changes, "no_expiry")
			} else {
				delete(entry.Plan.Changes, u.Field)
			}
		}
		if entry.Plan.Changes["no_expiry"] == false {
			delete(entry.Plan.Changes, "no_expiry")
		}
		// Only a verbatim owner span can ground a value; classification alone may default.
		for key := range entry.Plan.Changes {
			if key == "category" && plan.Operation == medicine.OperationCreate {
				continue
			}
			quote := strings.TrimSpace(p.Evidence[key])
			if quote == "" || !strings.Contains(original, quote) {
				delete(entry.Plan.Changes, key)
				if key == "expires_on" || key == "no_expiry" {
					fields["expiry"] = true
				} else {
					fields[key] = true
				}
			}
		}
		if plan.Operation != medicine.OperationCreate {
			quote := strings.TrimSpace(p.Evidence["target"])
			if quote == "" || !strings.Contains(original, quote) {
				return draft, nil, errors.New("请明确要修改的药品名称和定位信息；整批未写入")
			}
			exact := []medicine.Item{}
			normal := []medicine.Item{}
			for _, item := range items {
				if p.Target.ExpiresOn != "" && medicineMonthEnd(p.Target.ExpiresOn) != item.ExpiresOn || p.Target.Location != "" && p.Target.Location != item.Location {
					continue
				}
				if item.Name == p.Target.Name {
					exact = append(exact, item)
				} else if medicine.NormalizeName(item.Name) == medicine.NormalizeName(p.Target.Name) {
					normal = append(normal, item)
				}
			}
			entry.Candidates = exact
			if len(exact) == 0 {
				entry.Candidates = normal
			}
			if len(entry.Candidates) == 0 {
				return draft, nil, fmt.Errorf("药箱里没有 %s 的记录；整批未写入", p.Target.Name)
			}
			if len(entry.Candidates) > 1 || fields["target"] {
				fields["target"] = true
			}
		} else {
			for _, key := range []string{"name", "quantity", "unit"} {
				value, ok := entry.Plan.Changes[key]
				if !ok || value == "" {
					fields[key] = true
				}
			}
			date, _ := entry.Plan.Changes["expires_on"].(string)
			no, _ := entry.Plan.Changes["no_expiry"].(bool)
			if date == "" && !no {
				fields["expiry"] = true
			}
			if _, ok := entry.Plan.Changes["category"]; !ok {
				entry.Plan.Changes["category"] = "other"
			}
		}
		label := medicineString(entry.Plan.Changes["name"])
		if label == "" && p.Target != nil {
			label = p.Target.Name
		}
		if label == "" {
			label = fmt.Sprintf("药品 %d", index+1)
		}
		formEntry := app.ApprovalInputEntry{ID: entry.EntryID, Label: label, Summary: medicineChangeSummary(entry.Plan.Changes), Fields: []app.ApprovalInputField{}}
		for _, key := range append([]string{"target", "expiry"}, medicineChangeFields...) {
			if !fields[key] {
				continue
			}
			delete(fields, key)
			field := medicineInputField(key)
			if key == "target" {
				field.Type = app.ApprovalInputSelect
				field.MaxLength = 0
				for _, c := range entry.Candidates {
					field.Options = append(field.Options, app.ApprovalInputOption{Value: c.ID, Label: fmt.Sprintf("%s · %g%s · %s · %s", c.Name, c.Quantity, medicineLabel(c.Unit), medicineExpiryText(c), c.Location)})
				}
			}
			formEntry.Fields = append(formEntry.Fields, field)
			needsInput = true
		}
		form.Entries = append(form.Entries, formEntry)
		draft.Items = append(draft.Items, entry)
	}
	if !needsInput {
		return draft, nil, nil
	}
	if err := app.ValidateApprovalInputForm(form); err != nil {
		return draft, nil, err
	}
	raw, _ := json.Marshal(draft)
	if len(raw) > 48<<10 {
		return draft, nil, errors.New("待补全药品候选过多，请缩小范围；整批未写入")
	}
	return draft, form, nil
}

func materializeMedicineBatch(draft frozenMedicineDraft, inputs app.ApprovalInputValues) (medicine.Batch, error) {
	batch := medicine.Batch{OperationID: draft.OperationID, Operation: draft.Operation}
	seen := map[string]bool{}
	for _, entry := range draft.Items {
		var base medicine.Item
		values := inputs[entry.EntryID]
		if draft.Operation != medicine.OperationCreate {
			id, _ := values["target"].(string)
			if len(entry.Candidates) == 1 && id == "" {
				base = entry.Candidates[0]
			} else {
				for _, candidate := range entry.Candidates {
					if candidate.ID == id {
						base = candidate
						break
					}
				}
			}
			if base.ID == "" || seen[base.ID] {
				return batch, errors.New("目标未明确或同一药品重复修改；整批未写入")
			}
			seen[base.ID] = true
		}
		fields := base.Fields
		if draft.Operation == medicine.OperationCreate {
			fields.Category = "other"
		}
		changes := clonePlanArgs(entry.Plan.Changes)
		for key, value := range values {
			switch key {
			case "target":
				continue
			case "expiry":
				text, ok := value.(string)
				if !ok {
					return batch, errors.New("invalid expiry input")
				}
				changes["no_expiry"] = text == "no_expiry"
				changes["expires_on"] = ""
				if text != "no_expiry" {
					changes["expires_on"] = text
				}
			default:
				changes[key] = value
			}
		}
		var err error
		fields, err = applyMedicineChanges(fields, changes)
		if err != nil {
			return batch, err
		}
		if draft.Operation != medicine.OperationDelete {
			fields, err = medicine.ValidateFields(fields, draft.Operation == medicine.OperationUpdate && base.ExpiresOn == "" && !base.NoExpiry && fields.ExpiresOn == "" && !fields.NoExpiry)
			if err != nil {
				return batch, fmt.Errorf("药品字段不合法：%w；整批未写入", err)
			}
		}
		if draft.Operation == medicine.OperationDelete {
			fields = medicine.Fields{}
		}
		batch.Items = append(batch.Items, medicine.Entry{EntryID: entry.EntryID, ID: base.ID, BaseVersion: base.Version, Fields: fields})
	}
	return batch, nil
}
func applyMedicineChanges(f medicine.Fields, changes map[string]any) (medicine.Fields, error) {
	if changes["no_expiry"] == true && medicineString(changes["expires_on"]) != "" {
		return f, errors.New("有效期和无有效期不能同时填写")
	}
	for key, value := range changes {
		switch key {
		case "quantity", "quantity_delta":
			n, ok := value.(float64)
			if !ok || math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n*100-math.Round(n*100)) > 1e-7 {
				return f, errors.New("数量必须是数值")
			}
			if key == "quantity_delta" {
				if f.Quantity+n < 0 {
					return f, fmt.Errorf("当前数量为 %g，不能减少 %g；整批未写入", f.Quantity, -n)
				}
				f.Quantity = math.Round((f.Quantity+n)*100) / 100
			} else {
				f.Quantity = n
			}
		case "no_expiry":
			v, ok := value.(bool)
			if !ok {
				return f, errors.New("无有效期必须明确选择")
			}
			f.NoExpiry = v
			if v {
				f.ExpiresOn = ""
			}
		default:
			v, ok := value.(string)
			if !ok {
				return f, errors.New("药品文本字段格式不正确")
			}
			switch key {
			case "name":
				f.Name = v
			case "category":
				f.Category = v
			case "unit":
				f.Unit = v
			case "expires_on":
				f.ExpiresOn = medicineMonthEnd(v)
				if v != "" {
					f.NoExpiry = false
				}
			case "location":
				f.Location = v
			case "usage":
				f.Usage = v
			case "notes":
				f.Notes = v
			case "notes_append":
				if f.Notes != "" && v != "" {
					f.Notes += "\n"
				}
				f.Notes += v
			default:
				return f, errors.New("unknown medicine change")
			}
		}
	}
	return f, nil
}

func medicineChangeSummary(changes map[string]any) string {
	parts := []string{}
	for _, key := range medicineChangeFields {
		value, ok := changes[key]
		if !ok {
			continue
		}
		if key == "no_expiry" {
			if value == true {
				parts = append(parts, "有效期：无有效期")
			}
			continue
		}
		label := medicineInputField(key).Label
		if key == "expires_on" {
			label = "有效期"
			value = medicineMonthEnd(medicineString(value))
		}
		if key == "unit" || key == "category" {
			value = medicineLabel(medicineString(value))
		}
		parts = append(parts, fmt.Sprintf("%s：%v", label, value))
	}
	return strings.Join(parts, "；")
}

func medicineString(value any) string { text, _ := value.(string); return text }
