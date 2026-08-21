package knowledge

import "testing"

func TestParseManagedMarkdownAndSnippet(t *testing.T) {
	contents := []byte("---\n" +
		"cairn_article_id: \"art_1\"\n" +
		"title: \"Decision\\\" title\"\n" +
		"slug: \"decision\"\n" +
		"summary: \"A summary\"\n" +
		"sensitivity: normal\n" +
		"version: 2\n" +
		"tags:\n" +
		"  - \"zeta\"\n" +
		"  - \"alpha\"\n" +
		"sources:\n" +
		"  - \"src_1\"\n" +
		"citations:\n" +
		"  - source_id: \"src_1\"\n" +
		"    locator: \"line 2\"\n" +
		"---\n\n" +
		"The decision was made locally.\n")
	parsed, err := parseManagedMarkdown(contents)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Title != `Decision" title` || parsed.Version != 2 || len(parsed.Citations) != 1 || parsed.Citations[0].Locator != "line 2" {
		t.Fatalf("parsed = %+v", parsed)
	}
	if got := snippet(parsed.Body, "decision", 20); got == "" || len(got) > 20 {
		t.Fatalf("snippet = %q", got)
	}
	if got := uniqueStrings([]string{"z", "a", "z"}); len(got) != 2 || got[0] != "a" {
		t.Fatalf("unique strings = %v", got)
	}
}

func TestParseManagedMarkdownRejectsMalformedMetadata(t *testing.T) {
	for _, contents := range [][]byte{
		[]byte("plain text"),
		[]byte("---\ncairn_article_id: \"art_1\"\n---\n\nbody\n"),
		[]byte("---\ncairn_article_id: \"art_1\"\ntitle: \"Title\"\nslug: \"title\"\nsummary: \"Summary\"\nsensitivity: normal\nversion: nope\n---\n\nbody\n"),
	} {
		if _, err := parseManagedMarkdown(contents); err == nil {
			t.Fatalf("malformed content accepted: %q", contents)
		}
	}
}

func TestTokenizationAndLimitsAreDeterministic(t *testing.T) {
	if got := tokenize("Local 决策 v2"); len(got) != 3 || got[0] != "local" || got[1] != "决策" || got[2] != "v2" {
		t.Fatalf("tokens = %v", got)
	}
	if got, err := boundedLimit(0, 10, 20); err != nil || got != 10 {
		t.Fatalf("default limit = %d, %v", got, err)
	}
	if _, err := boundedLimit(21, 10, 20); err == nil || err.Code != "REQUEST_INVALID" {
		t.Fatalf("invalid limit error = %+v", err)
	}
}
