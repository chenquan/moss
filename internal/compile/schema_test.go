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
	if err := ValidateMulti("extract", []byte(`{"pipeline":{"extractor_version":"x","prompt_hash":"p","schema_version":"1","strategy":"multi"},"items":[{"source_id":"src_1","content":{"text":"ok"},"citations":[{"locator":"line 1"}]}]}`)); err != nil {
		t.Fatalf("multi extract rejected: %v", err)
	}
	if err := ValidateMulti("write", []byte(`{"articles":[],"facts":[{"operation":"create","fact_key":"k","kind":"fact","text":"v","status":"active","source_ids":["src_1"]}]}`)); err != nil {
		t.Fatalf("multi write rejected: %v", err)
	}
}

func TestNormalizeSourceIDsSortsAndDeduplicates(t *testing.T) {
	got := normalizeSourceIDs(" src_b ", []string{"src_a", "src_b", "", "src_a"})
	want := []string{"src_a", "src_b"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("normalizeSourceIDs() = %#v, want %#v", got, want)
	}
}
