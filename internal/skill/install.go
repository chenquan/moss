package skill

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The checked-in Skill tree is copied into assets as part of the build so an
// installed binary can install the Skill without a source checkout.
//
//go:embed assets
var bundledAssets embed.FS

const (
	TargetClaude = "claude"
	TargetCodex  = "codex"

	ScopeGlobal  = "global"
	ScopeProject = "project"
)

type InstallOptions struct {
	Targets    []string
	Scope      string
	Force      bool
	HomeDir    string
	ProjectDir string
	CodexHome  string
}

type InstallResult struct {
	Target      string
	Scope       string
	Destination string
	Installed   int
	Skipped     int
}

type assetFile struct {
	Path string
	Data []byte
}

func Install(options InstallOptions) ([]InstallResult, error) {
	targets, err := normalizeTargets(options.Targets)
	if err != nil {
		return nil, err
	}
	scope := strings.ToLower(strings.TrimSpace(options.Scope))
	if scope == "" {
		scope = ScopeGlobal
	}
	if scope != ScopeGlobal && scope != ScopeProject {
		return nil, fmt.Errorf("unsupported skill scope %q; use global or project", options.Scope)
	}
	assets, err := loadAssets()
	if err != nil {
		return nil, fmt.Errorf("load bundled Skill: %w", err)
	}

	results := make([]InstallResult, 0, len(targets))
	roots := make([]string, 0, len(targets))
	for _, target := range targets {
		root, err := resolveDestination(target, scope, options)
		if err != nil {
			return nil, err
		}
		if err := preflight(root, assets, options.Force); err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}

	for index, target := range targets {
		installed, skipped, err := installFiles(roots[index], assets, options.Force)
		if err != nil {
			return nil, fmt.Errorf("install %s Skill: %w", target, err)
		}
		results = append(results, InstallResult{Target: target, Scope: scope, Destination: roots[index], Installed: installed, Skipped: skipped})
	}
	return results, nil
}

func normalizeTargets(values []string) ([]string, error) {
	if len(values) == 0 {
		return []string{TargetClaude}, nil
	}

	targets := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		target := strings.ToLower(strings.TrimSpace(value))
		if target == "" {
			continue
		}
		switch target {
		case TargetClaude, TargetCodex:
			// accepted target
		case "both":
			return nil, fmt.Errorf("unsupported skill target %q; repeat --target with claude and codex", value)
		default:
			return nil, fmt.Errorf("unsupported skill target %q; use claude or codex and repeat --target for multiple targets", value)
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return []string{TargetClaude}, nil
	}
	return targets, nil
}

func resolveDestination(target, scope string, options InstallOptions) (string, error) {
	if scope == ScopeProject {
		projectDir := options.ProjectDir
		if projectDir == "" {
			var err error
			projectDir, err = os.Getwd()
			if err != nil {
				return "", fmt.Errorf("resolve project directory: %w", err)
			}
		}
		projectDir, err := filepath.Abs(projectDir)
		if err != nil {
			return "", fmt.Errorf("resolve project directory: %w", err)
		}
		return filepath.Join(projectDir, "."+target, "skills", "moss"), nil
	}

	homeDir := options.HomeDir
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
	}
	homeDir, err := filepath.Abs(homeDir)
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	if target == TargetClaude {
		return filepath.Join(homeDir, ".claude", "skills", "moss"), nil
	}
	codexHome := options.CodexHome
	if codexHome == "" {
		codexHome = os.Getenv("CODEX_HOME")
	}
	if codexHome == "" {
		codexHome = filepath.Join(homeDir, ".codex")
	}
	codexHome, err = filepath.Abs(codexHome)
	if err != nil {
		return "", fmt.Errorf("resolve Codex home: %w", err)
	}
	return filepath.Join(codexHome, "skills", "moss"), nil
}

func loadAssets() ([]assetFile, error) {
	assets := make([]assetFile, 0)
	err := fs.WalkDir(bundledAssets, "assets", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(bundledAssets, path)
		if err != nil {
			return err
		}
		relative := strings.TrimPrefix(path, "assets/")
		assets = append(assets, assetFile{Path: filepath.FromSlash(relative), Data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Path < assets[j].Path })
	return assets, nil
}

func preflight(root string, assets []assetFile, force bool) error {
	if info, err := os.Lstat(root); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("skill destination is not a directory: %s", root)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect skill destination %s: %w", root, err)
	}
	for _, asset := range assets {
		path := filepath.Join(root, asset.Path)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect existing Skill file %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("existing Skill path is not a regular file: %s", path)
		}
		existing, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read existing Skill file %s: %w", path, err)
		}
		if string(existing) != string(asset.Data) && !force {
			return fmt.Errorf("Skill file conflict at %s; rerun with --force to overwrite", path)
		}
	}
	return nil
}

func installFiles(root string, assets []assetFile, force bool) (installed, skipped int, err error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return 0, 0, err
	}
	for _, asset := range assets {
		path := filepath.Join(root, asset.Path)
		existing, readErr := os.ReadFile(path)
		if readErr == nil && string(existing) == string(asset.Data) {
			skipped++
			continue
		}
		if readErr != nil && !os.IsNotExist(readErr) {
			return installed, skipped, fmt.Errorf("read existing Skill file %s: %w", path, readErr)
		}
		if readErr == nil && !force {
			return installed, skipped, fmt.Errorf("Skill file conflict at %s; rerun with --force to overwrite", path)
		}
		if err := writeAtomic(path, asset.Data); err != nil {
			return installed, skipped, err
		}
		installed++
	}
	return installed, skipped, nil
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create Skill directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".moss-skill-*")
	if err != nil {
		return fmt.Errorf("create temporary Skill file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set Skill file permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write Skill file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync Skill file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Skill file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace Skill file %s: %w", path, err)
	}
	return nil
}
