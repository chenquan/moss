package compile

import "testing"

func TestEmbeddedSchemasValidateStageContracts(t *testing.T) {
	valid := map[string][]byte{
		"extract":  []byte(`{"facts":[],"decisions":[],"preferences":[],"projects":[],"people":[],"relationships":[],"actions":[],"conflicts":[],"citations":[]}`),
		"classify": []byte(`{"categories":[],"outline":[]}`),
		"write":    []byte(`{"title":"Title","slug":"title","summary":"Summary","body":"Body","sensitivity":"normal","tags":[],"source_ids":["src_1"],"citations":[]}`),
	}
	for stage, contents := range valid {
		if err := Validate(stage, contents); err != nil {
			t.Fatalf("%s valid result rejected: %v", stage, err)
		}
	}
	if err := Validate("write", []byte(`{"title":"missing body"}`)); err == nil {
		t.Fatal("invalid write result was accepted")
	}
	if _, err := SchemaBytes("unknown"); err == nil {
		t.Fatal("unknown schema was accepted")
	}
}
