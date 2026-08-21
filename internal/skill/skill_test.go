package skill

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cairn/internal/protocol"
)

func TestCairnSkillIsMachineOnlyAndRoutesProtocol(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	content, err := os.ReadFile(filepath.Join(root, ".claude", "skills", "cairn", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, required := range []string{"name: cairn", "user-invocable: false", "cairn-cli call", "system.handshake", "source.ingest", "source.mark_sensitive", "system.health", "system.export", "system.restore", "compile.start", "compile.next", "compile.submit", "compile.status", "compile.preview", "compile.apply", "compile.abort", "plan.apply", "plan.inspect", "plan.undo", "confirmed: true", "knowledge.catalog", "knowledge.candidates", "knowledge.materialize", "knowledge.history", "knowledge.rollback.plan", "source.forget.plan", "audit.query", "WIKI_DRIFT", "SENSITIVITY_DENIED", "action.create.plan", "action.update.plan", "action.apply", "action.query"} {
		if !strings.Contains(text, required) {
			t.Fatalf("Skill missing %q", required)
		}
	}
	for _, forbidden := range []string{"assistant today", "assistant search", "assistant wiki list", "assistant commitment add", "assistant compile preview"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("Skill contains removed human CLI wording %q", forbidden)
		}
	}
	for _, resource := range []string{
		"workflows/capture.md", "workflows/compile.md", "workflows/retrieve.md", "workflows/action.md", "workflows/forget.md", "workflows/maintenance.md",
		"policies/privacy.md", "policies/confirmation.md", "policies/prompt-injection.md", "protocol/cli-protocol.md", "protocol/operation-guide.md",
	} {
		if _, err := os.Stat(filepath.Join(root, ".claude", "skills", "cairn", resource)); err != nil {
			t.Fatalf("Skill resource %q is missing: %v", resource, err)
		}
	}

	guideBytes, err := os.ReadFile(filepath.Join(root, ".claude", "skills", "cairn", "protocol", "operation-guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	guide := string(guideBytes)
	for _, required := range []string{
		"Request envelope", "request_id", "idempotency_key", "source.ingest", "compile.start", "compile.next", "compile.submit", "compile.preview", "compile.apply", "knowledge.candidates", "knowledge.materialize", "action.create.plan", "action.apply", "source.forget.plan", "plan.apply", "system.export", "system.restore", "retryable", "WIKI_DRIFT", "PATH_INVALID", "untrusted evidence",
	} {
		if !strings.Contains(guide, required) {
			t.Fatalf("operation guide missing %q", required)
		}
	}
	for _, capability := range protocol.SupportedCapabilities() {
		if !strings.Contains(guide, capability.Operation) {
			t.Fatalf("operation guide missing supported operation %q", capability.Operation)
		}
	}
}
