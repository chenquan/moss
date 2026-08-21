package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cairn/internal/protocol"
)

func TestRootCommandExposesRuntimeAndInstaller(t *testing.T) {
	root := NewRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	commands := root.Commands()
	if len(commands) != 2 || commands[0].Name() != "call" || commands[1].Name() != "skill" {
		t.Fatalf("visible commands = %v", commands)
	}
}

func TestSkillInstallCommandInstallsProjectCodexSkill(t *testing.T) {
	project := t.TempDir()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	var stdout, stderr bytes.Buffer
	root := NewRootCommand(&stdout, &stderr)
	root.SetArgs([]string{"skill", "install", "--target", "codex", "--scope", "project"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 || !strings.Contains(stdout.String(), filepath.Join(".codex", "skills", "cairn")) {
		t.Fatalf("installer output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".codex", "skills", "cairn", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestCallRequiresPathsAndRejectsPositionals(t *testing.T) {
	for _, args := range [][]string{
		{"call"},
		{"call", "--request", "request.json"},
		{"call", "--response", "response.json"},
		{"call", "--request", "request.json", "--response", "response.json", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		root := NewRootCommand(&stdout, &stderr)
		root.SetArgs(args)
		if err := root.Execute(); err == nil {
			t.Fatalf("args %v unexpectedly succeeded", args)
		}
		if stdout.Len() != 0 {
			t.Fatalf("args %v stdout=%q stderr=%q", args, stdout.String(), stderr.String())
		}
	}
}

func TestMachineHelpDoesNotRenderUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	root := NewRootCommand(&stdout, &stderr)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("human help leaked: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestCallDispatchesToApplication(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CAIRN_DATA_DIR", filepath.Join(dir, "data"))
	requestPath := filepath.Join(dir, "request.json")
	responsePath := filepath.Join(dir, "response.json")
	request := protocol.Request{
		ProtocolVersion: protocol.SupportedVersion,
		RequestID:       "req-cobra-test",
		Operation:       "system.capabilities",
		Actor:           protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"},
		Arguments:       map[string]json.RawMessage{},
	}
	b, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requestPath, b, 0600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := NewRootCommand(&stdout, &stderr)
	root.SetArgs([]string{"call", "--request", requestPath, "--response", responsePath})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("machine output leaked: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	result, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatal(err)
	}
	var response protocol.Response
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.RequestID != request.RequestID {
		t.Fatalf("response = %+v", response)
	}
}
