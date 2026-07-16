package skillsruntime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
)

type runnableTool interface {
	Run(agent.Context, any) (map[string]any, error)
}

type requestProcessor interface {
	ProcessRequest(agent.Context, *model.LLMRequest) error
}

type testToolContext struct {
	agent.ContextMock
	context.Context
}

func (c *testToolContext) Deadline() (time.Time, bool) { return c.Context.Deadline() }
func (c *testToolContext) Done() <-chan struct{}       { return c.Context.Done() }
func (c *testToolContext) Err() error                  { return c.Context.Err() }
func (c *testToolContext) Value(key any) any           { return c.Context.Value(key) }

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

func findRunnableTool(t *testing.T, result Result, name string) runnableTool {
	t.Helper()
	tools, err := result.Toolset.Tools(nil)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	for _, candidate := range tools {
		if candidate.Name() == name {
			runnable, ok := candidate.(runnableTool)
			if !ok {
				t.Fatalf("tool %q is not runnable", name)
			}
			return runnable
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
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
	wantSkills := []Summary{
		{Name: "alpha-skill", Description: "Alpha description"},
		{Name: "beta-skill", Description: "Beta description"},
	}
	if !reflect.DeepEqual(result.Skills, wantSkills) {
		t.Fatalf("Skill summaries = %#v, want %#v", result.Skills, wantSkills)
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

func TestBuildExposesOfficialToolsAndCatalog(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "sample-skill", "Sample description", "Follow sample instructions.")
	result, err := Build(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := result.Toolset.Tools(nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(tools))
	for _, candidate := range tools {
		names = append(names, candidate.Name())
	}
	sort.Strings(names)
	want := []string{"list_skills", "load_skill", "load_skill_resource"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tool names = %v, want %v", names, want)
	}

	processor, ok := result.Toolset.(requestProcessor)
	if !ok {
		t.Fatalf("toolset %T does not process requests", result.Toolset)
	}
	request := &model.LLMRequest{}
	if err := processor.ProcessRequest(nil, request); err != nil {
		t.Fatal(err)
	}
	if request.Config == nil || request.Config.SystemInstruction == nil {
		t.Fatal("Skills system instruction was not injected")
	}
	text := request.Config.SystemInstruction.Parts[0].Text
	for _, want := range []string{"SKILL.md", "<available_skills>", "sample-skill", "Sample description"} {
		if !strings.Contains(text, want) {
			t.Errorf("system instruction does not contain %q: %q", want, text)
		}
	}
}

func TestBuildPreloadsInstructionsAndResources(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, "resource-skill", "Uses resources", "Read the bundled files.")
	for path, content := range map[string]string{
		"references/guide.md": "reference content",
		"scripts/run.py":      "print('script content')",
	} {
		fullPath := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Build(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	toolContext := &testToolContext{Context: t.Context()}
	loaded, err := findRunnableTool(t, result, "load_skill").Run(toolContext, map[string]any{"name": "resource-skill"})
	if err != nil {
		t.Fatal(err)
	}
	instructions, ok := loaded["instructions"].(string)
	if !ok || !strings.Contains(instructions, "Read the bundled files.") {
		t.Fatalf("load_skill result = %#v", loaded)
	}
	for path, want := range map[string]string{
		"references/guide.md": "reference content",
		"scripts/run.py":      "print('script content')",
	} {
		resource, err := findRunnableTool(t, result, "load_skill_resource").Run(toolContext, map[string]any{
			"skill_name": "resource-skill", "resource_path": path,
		})
		if err != nil {
			t.Fatal(err)
		}
		if resource["content"] != want {
			t.Fatalf("resource %q = %#v", path, resource)
		}
	}
}

func TestBuildUsesStartupSnapshot(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, "snapshot-skill", "Snapshot description", "Original instructions.")
	result, err := Build(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	updated := "---\nname: snapshot-skill\ndescription: Snapshot description\n---\n\nUpdated instructions.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := findRunnableTool(t, result, "load_skill").Run(
		&testToolContext{Context: t.Context()},
		map[string]any{"name": "snapshot-skill"},
	)
	if err != nil {
		t.Fatal(err)
	}
	instructions, ok := loaded["instructions"].(string)
	if !ok || !strings.Contains(instructions, "Original instructions.") || strings.Contains(instructions, "Updated instructions.") {
		t.Fatalf("load_skill result = %#v", loaded)
	}
}

func TestBuildToolsetSupportsConcurrentAccess(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "concurrent-skill", "Concurrent description", "Concurrent instructions.")
	result, err := Build(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	processor, ok := result.Toolset.(requestProcessor)
	if !ok {
		t.Fatalf("toolset %T does not process requests", result.Toolset)
	}
	loadTool := findRunnableTool(t, result, "load_skill")

	const workers = 20
	errors := make(chan error, workers*3)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := result.Toolset.Tools(nil)
			errors <- err
			errors <- processor.ProcessRequest(nil, &model.LLMRequest{})
			_, err = loadTool.Run(
				&testToolContext{Context: t.Context()},
				map[string]any{"name": "concurrent-skill"},
			)
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Errorf("concurrent access: %v", err)
		}
	}
}
