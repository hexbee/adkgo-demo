package skillsruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, root, name, description, body string) string {
	t.Helper()
	dir := filepath.Join(root, RelativeRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBuildMissingDirectory(t *testing.T) {
	result, err := Build(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.Found || result.Count != 0 || result.Root != "" || result.Toolset != nil {
		t.Fatalf("result = %#v", result)
	}
}

func TestBuildEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, RelativeRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := Build(t.Context(), root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !result.Found || result.Count != 0 || result.Toolset != nil {
		t.Fatalf("result = %#v", result)
	}
	wantRoot, _ := filepath.Abs(filepath.Join(root, RelativeRoot))
	if result.Root != wantRoot {
		t.Fatalf("Root = %q, want %q", result.Root, wantRoot)
	}
}

func TestBuildRejectsRegularFileAtSkillsPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, RelativeRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(t.Context(), root); err == nil || !strings.Contains(err.Error(), RelativeRoot) {
		t.Fatalf("Build error = %v", err)
	}
}

func TestBuildValidSkills(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "alpha-skill", "Alpha description", "Alpha instructions.")
	writeSkill(t, root, "beta-skill", "Beta description", "Beta instructions.")
	result, err := Build(t.Context(), root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !result.Found || result.Count != 2 || result.Toolset == nil {
		t.Fatalf("result = %#v", result)
	}
}

func TestBuildRejectsMalformedFrontmatter(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, RelativeRoot, "broken-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("not frontmatter"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(t.Context(), root); err == nil || !strings.Contains(err.Error(), RelativeRoot) {
		t.Fatalf("Build error = %v", err)
	}
}

func TestBuildRejectsNameDirectoryMismatch(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, RelativeRoot, "directory-name")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: another-name\ndescription: mismatch\n---\n\nInstructions.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(t.Context(), root); err == nil || !strings.Contains(err.Error(), RelativeRoot) {
		t.Fatalf("Build error = %v", err)
	}
}
