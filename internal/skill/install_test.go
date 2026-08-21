package skill

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func TestBundledAssetsMatchCheckedInSkill(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate installer test")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	sourceRoot := filepath.Join(root, ".claude", "skills", "cairn")
	bundledRoot := filepath.Join(filepath.Dir(file), "assets")

	sourceFiles := regularFiles(t, sourceRoot)
	bundledFiles := regularFiles(t, bundledRoot)
	if len(sourceFiles) != len(bundledFiles) {
		t.Fatalf("source files=%d bundled files=%d", len(sourceFiles), len(bundledFiles))
	}
	for relative, sourcePath := range sourceFiles {
		bundledPath, ok := bundledFiles[relative]
		if !ok {
			t.Fatalf("bundled Skill missing %s", relative)
		}
		source, err := os.ReadFile(sourcePath)
		if err != nil {
			t.Fatal(err)
		}
		bundled, err := os.ReadFile(bundledPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(source, bundled) {
			t.Fatalf("bundled Skill differs from source at %s", relative)
		}
	}
}

func TestInstallDefaultsToGlobalClaude(t *testing.T) {
	home := t.TempDir()
	results, err := Install(InstallOptions{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Target != TargetClaude || results[0].Scope != ScopeGlobal {
		t.Fatalf("results = %+v", results)
	}
	expected := filepath.Join(home, ".claude", "skills", "cairn", "SKILL.md")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("default Skill missing at %s: %v", expected, err)
	}
	second, err := Install(InstallOptions{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Installed != 0 || second[0].Skipped == 0 {
		t.Fatalf("repeat install = %+v", second[0])
	}
}

func TestInstallProjectCodexAndRepeatedGlobalTargets(t *testing.T) {
	project := t.TempDir()
	results, err := Install(InstallOptions{Targets: []string{TargetCodex}, Scope: ScopeProject, ProjectDir: project})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Destination != filepath.Join(project, ".codex", "skills", "cairn") {
		t.Fatalf("project result = %+v", results)
	}

	home, codexHome := t.TempDir(), t.TempDir()
	results, err = Install(InstallOptions{Targets: []string{TargetCodex, TargetClaude}, Scope: ScopeGlobal, HomeDir: home, CodexHome: codexHome})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("repeated target results = %+v", results)
	}
	if results[0].Target != TargetCodex || results[1].Target != TargetClaude {
		t.Fatalf("target order = %+v", results)
	}
	for _, path := range []string{
		filepath.Join(home, ".claude", "skills", "cairn", "SKILL.md"),
		filepath.Join(codexHome, "skills", "cairn", "SKILL.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("target Skill missing at %s: %v", path, err)
		}
	}
}

func TestInstallDeduplicatesRepeatedTargets(t *testing.T) {
	home, codexHome := t.TempDir(), t.TempDir()
	results, err := Install(InstallOptions{Targets: []string{TargetCodex, TargetClaude, TargetCodex}, HomeDir: home, CodexHome: codexHome})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Target != TargetCodex || results[1].Target != TargetClaude {
		t.Fatalf("deduplicated results = %+v", results)
	}
}

func TestInstallConflictRequiresForceAndPreservesUnrelatedFiles(t *testing.T) {
	project := t.TempDir()
	options := InstallOptions{Targets: []string{TargetClaude}, Scope: ScopeProject, ProjectDir: project}
	if _, err := Install(options); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(project, ".claude", "skills", "cairn")
	skillPath := filepath.Join(destination, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(destination, "user-notes.md")
	if err := os.WriteFile(unrelated, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(options); err == nil {
		t.Fatal("conflicting install unexpectedly succeeded")
	}
	content, _ := os.ReadFile(skillPath)
	if string(content) != "user edit" {
		t.Fatalf("conflict changed user file: %q", content)
	}
	if _, err := Install(InstallOptions{Targets: []string{TargetClaude}, Scope: ScopeProject, ProjectDir: project, Force: true}); err != nil {
		t.Fatal(err)
	}
	content, _ = os.ReadFile(skillPath)
	if bytes.Equal(content, []byte("user edit")) {
		t.Fatal("force did not replace conflicting Skill file")
	}
	if preserved, err := os.ReadFile(unrelated); err != nil || string(preserved) != "keep" {
		t.Fatalf("unrelated file was not preserved: %q, %v", preserved, err)
	}
}

func TestInstallRejectsUnknownTargetAndScope(t *testing.T) {
	if _, err := Install(InstallOptions{Targets: []string{"vim"}}); err == nil {
		t.Fatal("unknown target unexpectedly accepted")
	}
	if _, err := Install(InstallOptions{Targets: []string{"both"}}); err == nil {
		t.Fatal("removed both target unexpectedly accepted")
	}
	if _, err := Install(InstallOptions{Scope: "workspace"}); err == nil {
		t.Fatal("unknown scope unexpectedly accepted")
	}
}

func regularFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[relative] = path
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(result))
	for path := range result {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return result
}
