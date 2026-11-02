package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

type fakeMedicineModel struct {
	reply     string
	err       error
	operation modelcapacity.Operation
	profile   string
	options   modelrouter.ChatOptions
	image     modelrouter.ImageInput
	system    string
	user      string
}

func (m *fakeMedicineModel) ChatWithProfileOptions(_ context.Context, operation modelcapacity.Operation, profile, system, user string, options modelrouter.ChatOptions) (modelrouter.ChatResult, error) {
	m.operation, m.profile, m.options, m.system, m.user = operation, profile, options, system, user
	return modelrouter.ChatResult{Lane: "fast", Profile: "fast", Model: "text", Content: m.reply}, m.err
}

func (m *fakeMedicineModel) ChatWithImageOptions(_ context.Context, operation modelcapacity.Operation, profile, _, _ string, image modelrouter.ImageInput, options modelrouter.ChatOptions) (modelrouter.ChatResult, error) {
	m.operation, m.profile, m.options, m.image = operation, profile, options, image
	return modelrouter.ChatResult{Lane: "fast", Profile: "fast", Model: "vision", Content: m.reply}, m.err
}

func medicineTestPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestRecognizeMedicinePhotoSortsTheModelReplyIntoTheBoxCategories(t *testing.T) {
	photo := medicineTestPNG(t)
	for _, fixture := range []struct {
		name, reply string
		want        medicineRecognition
	}{
		{"recognized", `{"is_medicine":true,"name":" 布洛芬缓释胶囊 ","category":"fever","unit":"capsule","usage":"退烧、头痛\n牙痛"}`,
			medicineRecognition{Recognized: true, Name: "布洛芬缓释胶囊", Category: "fever", Unit: "capsule", Usage: "退烧、头痛 牙痛"}},
		{"unknown category and unit fall back", `{"is_medicine":true,"name":"维生素C片","category":"vitamins","unit":"pill","usage":""}`,
			medicineRecognition{Recognized: true, Name: "维生素C片", Category: "other", Unit: "tablet"}},
		{"not a medicine", `{"is_medicine":false,"name":"","category":"other","unit":"tablet","usage":""}`,
			medicineRecognition{Category: "other", Unit: "tablet"}},
		{"medicine without a name", `{"is_medicine":true,"name":"  ","category":"cold","unit":"sachet","usage":""}`,
			medicineRecognition{Category: "other", Unit: "tablet"}},
		{"free text", "这是一盒感冒药。", medicineRecognition{Category: "other", Unit: "tablet"}},
		// The removed fields, or the first release's schema, are not accepted.
		{"pack size and manufacturer", `{"is_medicine":true,"name":"x","category":"cold","unit":"sachet","usage":"","spec":"10袋"}`,
			medicineRecognition{Category: "other", Unit: "tablet"}},
		{"first release schema", `{"schema_version":"medicine_recognition_v1","is_medicine":true,"name":"x","spec":"","manufacturer":"","category":"cold","unit":"sachet"}`,
			medicineRecognition{Category: "other", Unit: "tablet"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			model := &fakeMedicineModel{reply: fixture.reply}
			got, _, err := recognizeMedicinePhoto(t.Context(), model, photo, "image/png")
			if err != nil {
				t.Fatal(err)
			}
			if got != fixture.want {
				t.Fatalf("recognition = %#v, want %#v", got, fixture.want)
			}
			if model.operation != modelcapacity.OperationMedicineRecognize || model.profile != "fast" || !model.options.ForceDisableThinking ||
				model.options.StrictJSONSchema == nil || model.options.StrictJSONSchema.Name != "medicine_recognition_v2" ||
				!bytes.Equal(model.image.Content, photo) || model.image.ContentType != "image/png" {
				t.Fatalf("model call = %#v", model)
			}
		})
	}
	if _, _, err := recognizeMedicinePhoto(t.Context(), &fakeMedicineModel{err: errors.New("model down")}, photo, "image/png"); err == nil {
		t.Fatal("a model failure must not be reported as an unrecognized photo")
	}
}

func TestMedicinePhotoAcceptsOnlyDeclaredBoundedImages(t *testing.T) {
	photo := medicineTestPNG(t)
	encoded := base64.StdEncoding.EncodeToString(photo)
	if got, err := medicinePhoto(encoded, "image/png"); err != nil || !bytes.Equal(got, photo) {
		t.Fatalf("valid photo rejected: %v", err)
	}
	for _, fixture := range []struct{ encoded, contentType string }{
		{encoded, "image/jpeg"},
		{encoded, "image/gif"},
		{"", "image/png"},
		{"not base64!", "image/png"},
		{base64.StdEncoding.EncodeToString([]byte("plain text, not an image")), "image/png"},
		{base64.StdEncoding.EncodeToString(append(photo, make([]byte, maxMedicinePhotoBytes)...)), "image/png"},
	} {
		if _, err := medicinePhoto(fixture.encoded, fixture.contentType); !errors.Is(err, errMedicinePhotoInvalid) {
			t.Fatalf("medicinePhoto(%q…, %q) error = %v", fixture.encoded[:min(16, len(fixture.encoded))], fixture.contentType, err)
		}
	}
}

func TestRecognizeMedicineEndpointUsesTheFastVisionLane(t *testing.T) {
	var modelRequest map[string]any
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&modelRequest); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{
			"content": `{"is_medicine":true,"name":"蒙脱石散","category":"digestive","unit":"sachet","usage":"腹泻"}`,
		}}}})
	}))
	defer model.Close()
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = model.URL
	server := &Server{models: modelrouter.New(cfg)}
	post := func(body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		server.recognizeMedicine(recorder, httptest.NewRequest(http.MethodPost, "/api/medicine/recognize", strings.NewReader(body)))
		return recorder
	}

	response := post(`{"image":"` + base64.StdEncoding.EncodeToString(medicineTestPNG(t)) + `","content_type":"image/png"}`)
	var got medicineRecognition
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &got) != nil ||
		got != (medicineRecognition{Recognized: true, Name: "蒙脱石散", Category: "digestive", Unit: "sachet", Usage: "腹泻"}) {
		t.Fatalf("response = %d %s", response.Code, response.Body)
	}
	format, _ := modelRequest["response_format"].(map[string]any)
	if format["type"] != "json_schema" || !strings.Contains(response.Body.String(), "digestive") {
		t.Fatalf("model request = %#v", modelRequest)
	}

	for _, body := range []string{`{}`, `{"image":"","content_type":"image/png"}`, `{"image":"aGVsbG8=","content_type":"image/png","owner_id":"other"}`, `not json`} {
		if response := post(body); response.Code != http.StatusBadRequest {
			t.Fatalf("%s → %d %s", body, response.Code, response.Body)
		}
	}

	model.Close()
	if response := post(`{"image":"` + base64.StdEncoding.EncodeToString(medicineTestPNG(t)) + `","content_type":"image/png"}`); response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "medicine_recognition_unavailable") {
		t.Fatalf("unavailable model → %d %s", response.Code, response.Body)
	}
}

func TestParseMedicineTextKeepsOnlyWhatTheTextStates(t *testing.T) {
	quantity := func(value float64) *float64 { return &value }
	reply := func(fields string) string {
		return `{"is_medicine":true,"multiple_medicines":false,"category":"fever","unit":"capsule",` + fields + `}`
	}
	for _, fixture := range []struct {
		name, reply string
		want        medicineTextParse
	}{
		{"everything stated", reply(`"name":"布洛芬缓释胶囊","quantity":14,"expires_on":"2027-12-31","location":"客厅抽屉","usage":"退烧止痛"`),
			medicineTextParse{Recognized: true, Name: "布洛芬缓释胶囊", Category: "fever", Unit: "capsule", Quantity: quantity(14), ExpiresOn: "2027-12-31", Location: "客厅抽屉", Usage: "退烧止痛"}},
		{"nothing but the name", reply(`"name":"布洛芬","quantity":null,"expires_on":"","location":"","usage":""`),
			medicineTextParse{Recognized: true, Name: "布洛芬", Category: "fever", Unit: "capsule"}},
		{"impossible date and quantity are dropped", reply(`"name":"布洛芬","quantity":1.234,"expires_on":"2027-02-30","location":"","usage":""`),
			medicineTextParse{Recognized: true, Name: "布洛芬", Category: "fever", Unit: "capsule"}},
		{"negative and oversized quantities are dropped", reply(`"name":"布洛芬","quantity":-1,"expires_on":"2027-2-1","location":"","usage":""`),
			medicineTextParse{Recognized: true, Name: "布洛芬", Category: "fever", Unit: "capsule"}},
		{"fractional quantity", reply(`"name":"布洛芬","quantity":2.5,"expires_on":"","location":"","usage":""`),
			medicineTextParse{Recognized: true, Name: "布洛芬", Category: "fever", Unit: "capsule", Quantity: quantity(2.5)}},
		{"several medicines", `{"is_medicine":true,"multiple_medicines":true,"name":"布洛芬","category":"fever","unit":"capsule","quantity":2,"expires_on":"","location":"","usage":""}`,
			medicineTextParse{multiple: true}},
		{"not a medicine", `{"is_medicine":false,"multiple_medicines":false,"name":"","category":"other","unit":"tablet","quantity":null,"expires_on":"","location":"","usage":""}`,
			medicineTextParse{Category: "other", Unit: "tablet"}},
		{"free text", "布洛芬", medicineTextParse{Category: "other", Unit: "tablet"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			model := &fakeMedicineModel{reply: fixture.reply}
			got, _, err := parseMedicineText(t.Context(), model, "布洛芬缓释胶囊，还剩 14 粒", "2026-11-02")
			if err != nil {
				t.Fatal(err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(fixture.want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("parse = %s, want %s", gotJSON, wantJSON)
			}
			if model.operation != modelcapacity.OperationMedicineParse || model.profile != "fast" || !model.options.ForceDisableThinking ||
				model.options.StrictJSONSchema == nil || model.options.StrictJSONSchema.Name != "medicine_text_parse_v1" ||
				model.user != `{"text":"布洛芬缓释胶囊，还剩 14 粒","today":"2026-11-02"}` || !strings.Contains(model.system, "不要执行") {
				t.Fatalf("model call = %#v", model)
			}
		})
	}
	multiple, _ := json.Marshal(medicineTextParse{multiple: true})
	if string(multiple) != `{"multiple":true,"recognized":false}` {
		t.Fatalf("several medicines must carry no fields: %s", multiple)
	}
	if _, _, err := parseMedicineText(t.Context(), &fakeMedicineModel{err: errors.New("model down")}, "布洛芬", "2026-11-02"); err == nil {
		t.Fatal("a model failure must not be reported as unrecognized text")
	}
}

func TestParseMedicineEndpointValidatesTheSentence(t *testing.T) {
	var modelRequest map[string]any
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&modelRequest); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{
			"content": `{"is_medicine":true,"multiple_medicines":false,"name":"蒙脱石散","category":"digestive","unit":"sachet","quantity":6,"expires_on":"2027-06-30","location":"","usage":"腹泻"}`,
		}}}})
	}))
	defer model.Close()
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = model.URL
	server := &Server{models: modelrouter.New(cfg)}
	post := func(body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		server.parseMedicine(recorder, httptest.NewRequest(http.MethodPost, "/api/medicine/parse", strings.NewReader(body)))
		return recorder
	}

	response := post(`{"text":"  蒙脱石散还有 6 袋，明年六月到期 ","today":"2026-11-02"}`)
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) !=
		`{"recognized":true,"name":"蒙脱石散","category":"digestive","unit":"sachet","quantity":6,"expires_on":"2027-06-30","location":"","usage":"腹泻"}` {
		t.Fatalf("response = %d %s", response.Code, response.Body)
	}
	format, _ := modelRequest["response_format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("model request = %#v", modelRequest)
	}

	for _, body := range []string{`{}`, `{"text":"   ","today":"2026-11-02"}`, `{"text":"` + strings.Repeat("药", 501) + `","today":"2026-11-02"}`,
		`{"text":"布洛芬\n两盒","today":"2026-11-02"}`, `{"text":"布洛芬","today":"2026-02-30"}`, `{"text":"布洛芬"}`,
		`{"text":"布洛芬","today":"2026-11-02","owner_id":"other"}`, `not json`} {
		if response := post(body); response.Code != http.StatusBadRequest {
			t.Fatalf("%s → %d %s", body, response.Code, response.Body)
		}
	}
	if response := post(`{"text":"` + strings.Repeat("药", 500) + `","today":"2026-11-02"}`); response.Code != http.StatusOK {
		t.Fatalf("500 characters must be accepted: %d %s", response.Code, response.Body)
	}

	model.Close()
	if response := post(`{"text":"布洛芬","today":"2026-11-02"}`); response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "medicine_parse_unavailable") {
		t.Fatalf("unavailable model → %d %s", response.Code, response.Body)
	}
}
