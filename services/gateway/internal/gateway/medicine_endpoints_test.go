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
		{"recognized", `{"schema_version":"medicine_recognition_v1","is_medicine":true,"name":" 布洛芬缓释胶囊 ","spec":"0.3g×20粒","manufacturer":"中美天津史克","category":"fever","unit":"capsule"}`,
			medicineRecognition{Recognized: true, Name: "布洛芬缓释胶囊", Spec: "0.3g×20粒", Manufacturer: "中美天津史克", Category: "fever", Unit: "capsule"}},
		{"unknown category and unit fall back", `{"schema_version":"medicine_recognition_v1","is_medicine":true,"name":"维生素C片","spec":"","manufacturer":"","category":"vitamins","unit":"pill"}`,
			medicineRecognition{Recognized: true, Name: "维生素C片", Category: "other", Unit: "tablet"}},
		{"not a medicine", `{"schema_version":"medicine_recognition_v1","is_medicine":false,"name":"","spec":"","manufacturer":"","category":"other","unit":"tablet"}`,
			medicineRecognition{Category: "other", Unit: "tablet"}},
		{"medicine without a name", `{"schema_version":"medicine_recognition_v1","is_medicine":true,"name":"  ","spec":"","manufacturer":"","category":"cold","unit":"sachet"}`,
			medicineRecognition{Category: "other", Unit: "tablet"}},
		{"free text", "这是一盒感冒药。", medicineRecognition{Category: "other", Unit: "tablet"}},
		{"extra fields", `{"schema_version":"medicine_recognition_v1","is_medicine":true,"name":"x","spec":"","manufacturer":"","category":"cold","unit":"sachet","dose":"2"}`,
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
				model.options.StrictJSONSchema == nil || model.options.StrictJSONSchema.Name != "medicine_recognition_v1" ||
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
			"content": `{"schema_version":"medicine_recognition_v1","is_medicine":true,"name":"蒙脱石散","spec":"3g×10袋","manufacturer":"","category":"digestive","unit":"sachet"}`,
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
		got != (medicineRecognition{Recognized: true, Name: "蒙脱石散", Spec: "3g×10袋", Category: "digestive", Unit: "sachet"}) {
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
