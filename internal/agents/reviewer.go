package agents

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

const reviewerSystemPrompt = `You are a code review agent. You review code against codebase conventions and patterns, flag deviations, identify missing tests, highlight complexity hotspots, and check for security issues. Answer based on the provided context and metrics.`

// Reviewer is the code review agent for diff review and convention checking.
type Reviewer struct {
	BaseAgent
}

// NewReviewer creates a new code review agent.
func NewReviewer(client llm.Client, ctxBuilder *ContextBuilder) *Reviewer {
	return &Reviewer{
		BaseAgent: BaseAgent{
			name:         "reviewer",
			llmClient:    client,
			ctxBuilder:   ctxBuilder,
			systemPrompt: reviewerSystemPrompt,
		},
	}
}

// Ask sends a review query about specific files or general code conventions
// to the LLM, enriched with file and metrics context.
func (r *Reviewer) Ask(ctx context.Context, query string) (string, error) {
	var parts []string

	// Extract file paths or entity names from the query.
	entityName := extractEntityName(query)
	if entityName != "" {
		fileCtx, err := r.ctxBuilder.BuildFileContext(ctx, entityName)
		if err == nil {
			parts = append(parts, fileCtx)
		}
		metricsCtx, err := r.ctxBuilder.BuildMetricsContext(ctx, entityName)
		if err == nil {
			parts = append(parts, metricsCtx)
		}
	}

	// If no specific context was found, add overview.
	if len(parts) == 0 {
		overview, err := r.ctxBuilder.BuildOverviewContext(ctx)
		if err != nil {
			return "", fmt.Errorf("build overview context: %w", err)
		}
		parts = append(parts, overview)
	}

	contextText := strings.Join(parts, "\n\n")
	return r.ask(ctx, contextText, query)
}

// ReviewDiff runs `git diff <diffRef>` to get changed files, builds context
// for those files, and sends a review-focused prompt to the LLM.
func (r *Reviewer) ReviewDiff(ctx context.Context, diffRef string) (string, error) {
	// Run git diff to get the diff output.
	diffOutput, err := runGitDiff(ctx, diffRef)
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}

	// Parse changed file paths from the diff.
	changedFiles := parseDiffFiles(diffOutput)
	if len(changedFiles) == 0 {
		return "No changed files found in the diff.", nil
	}

	var parts []string

	// Build diff context for changed files.
	diffCtx, err := r.ctxBuilder.BuildDiffContext(ctx, changedFiles)
	if err == nil {
		parts = append(parts, diffCtx)
	}

	// Add metrics for each changed file.
	for _, fp := range changedFiles {
		metricsCtx, err := r.ctxBuilder.BuildMetricsContext(ctx, fp)
		if err == nil && !strings.Contains(metricsCtx, "No indexed symbols found") {
			parts = append(parts, metricsCtx)
		}
	}

	contextText := strings.Join(parts, "\n\n")

	reviewQuery := fmt.Sprintf(
		"Review the following code changes (git diff %s).\n\n"+
			"Changed files: %s\n\n"+
			"Raw diff output:\n```\n%s\n```\n\n"+
			"Please review for: convention adherence, pattern deviations, missing tests, "+
			"complexity issues, and security concerns.",
		diffRef,
		strings.Join(changedFiles, ", "),
		diffOutput,
	)

	return r.ask(ctx, contextText, reviewQuery)
}

// gitDiffRunner is the function used to run git diff. It can be replaced in tests.
var gitDiffRunner = runGitDiffCommand

// runGitDiff executes git diff and returns the output.
func runGitDiff(ctx context.Context, diffRef string) (string, error) {
	return gitDiffRunner(ctx, diffRef)
}

// runGitDiffCommand is the real implementation that runs git diff.
func runGitDiffCommand(ctx context.Context, diffRef string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", diffRef)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("execute git diff %s: %w", diffRef, err)
	}
	return string(output), nil
}

// parseDiffFiles extracts file paths from git diff output.
// It looks for lines starting with "diff --git a/... b/..." or "+++ b/...".
func parseDiffFiles(diffOutput string) []string {
	seen := make(map[string]struct{})
	var files []string

	for _, line := range strings.Split(diffOutput, "\n") {
		if strings.HasPrefix(line, "+++ b/") {
			fp := strings.TrimPrefix(line, "+++ b/")
			if fp != "/dev/null" {
				if _, ok := seen[fp]; !ok {
					seen[fp] = struct{}{}
					files = append(files, fp)
				}
			}
		}
	}

	return files
}
