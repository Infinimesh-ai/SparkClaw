package toolhub

import "testing"

func TestRequiredNullableSchemaDistinguishesMissingFromNull(t *testing.T) {
	schema := strictObjectSchema([]string{"capture"}, map[string]any{"capture": map[string]any{"type": []any{"object", "null"}}})
	if err := validateSchemaValue(map[string]any{"capture": nil}, schema, "output"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []map[string]any{{}, {"capture": "invalid"}} {
		if err := validateSchemaValue(value, schema, "output"); err == nil {
			t.Fatal("accepted missing or wrongly typed nullable value")
		}
	}
	schema = strictObjectSchema([]string{"capture"}, map[string]any{"capture": objectValueSchema()})
	if err := validateSchemaValue(map[string]any{"capture": nil}, schema, "output"); err == nil {
		t.Fatal("accepted null for non-nullable property")
	}
}
