// Package skillsruntime constructs the project-local ADK Agent Skills Toolset.
package skillsruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/skilltoolset"
	"google.golang.org/adk/v2/tool/skilltoolset/skill"
)

const RelativeRoot = ".agents/skills"

// Result describes the project Skills directory and its optional ADK Toolset.
type Result struct {
	Found   bool
	Count   int
	Root    string
	Skills  []Summary
	Toolset tool.Toolset
}

// Summary is the public, presentation-safe portion of a loaded Skill.
type Summary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Build discovers and completely preloads project-local Agent Skills.
func Build(ctx context.Context, projectRoot string) (Result, error) {
	root, err := filepath.Abs(filepath.Join(projectRoot, RelativeRoot))
	if err != nil {
		return Result{}, fmt.Errorf("resolve %s: %w", RelativeRoot, err)
	}
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("inspect %s: %w", RelativeRoot, err)
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("%s is not a directory", RelativeRoot)
	}

	source := skill.NewFileSystemSource(os.DirFS(root))
	preloaded, _, err := skill.WithCompletePreloadSource(ctx, source)
	if err != nil {
		return Result{}, fmt.Errorf("preload %s: %w", RelativeRoot, err)
	}
	frontmatters, err := preloaded.ListFrontmatters(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("list %s: %w", RelativeRoot, err)
	}
	summaries := make([]Summary, 0, len(frontmatters))
	for _, frontmatter := range frontmatters {
		summaries = append(summaries, Summary{
			Name:        frontmatter.Name,
			Description: frontmatter.Description,
		})
	}
	result := Result{Found: true, Count: len(frontmatters), Root: root, Skills: summaries}
	if result.Count == 0 {
		return result, nil
	}
	toolset, err := skilltoolset.New(ctx, skilltoolset.Config{Source: preloaded})
	if err != nil {
		return Result{}, fmt.Errorf("create toolset for %s: %w", RelativeRoot, err)
	}
	result.Toolset = toolset
	return result, nil
}
