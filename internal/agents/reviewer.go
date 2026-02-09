package agents

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/gitutil"
	"github.com/imyousuf/CodeEagle/internal/graph"
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
	if r.verbose && r.log != nil {
		r.log("Starting reviewer query...")
	}
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
// If diffRef is empty, it auto-detects the branch diff against the default branch.
func (r *Reviewer) ReviewDiff(ctx context.Context, diffRef string, repoPaths ...string) (string, error) {
	// Auto-detect branch diff when no explicit ref is provided.
	if diffRef == "" && len(repoPaths) > 0 {
		repoPath := repoPaths[0]
		branchDiff, err := gitutil.GetBranchDiff(repoPath)
		if err != nil {
			return "", fmt.Errorf("auto-detect branch diff: %w", err)
		}
		if !branchDiff.IsFeatureBranch {
			return "Currently on the default branch. Provide a diff ref (e.g. HEAD~1) or switch to a feature branch.", nil
		}
		// Use the default branch as the diff ref.
		diffRef = branchDiff.DefaultBranch + "...HEAD"
	}

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

	// Include branch context if a repo path is available.
	if len(repoPaths) > 0 {
		branchCtx, err := r.ctxBuilder.BuildBranchContext(ctx, repoPaths[0])
		if err == nil {
			parts = append(parts, branchCtx)
		}
	}

	// Build diff context for changed files.
	diffCtx, err := r.ctxBuilder.BuildDiffContext(ctx, changedFiles)
	if err == nil {
		parts = append(parts, diffCtx)
	}

	// Add architecture context so reviewer can flag pattern violations.
	archCtx, err := r.ctxBuilder.BuildArchitectureContext(ctx)
	if err == nil && !strings.Contains(archCtx, "No architectural metadata") {
		parts = append(parts, archCtx)
	}

	// Add metrics for each changed file. Also include model impact for model files.
	for _, fp := range changedFiles {
		metricsCtx, err := r.ctxBuilder.BuildMetricsContext(ctx, fp)
		if err == nil && !strings.Contains(metricsCtx, "No indexed symbols found") {
			parts = append(parts, metricsCtx)
		}
		// Check if changed file contains model nodes and add model impact context.
		fileNodes, err := r.ctxBuilder.store.QueryNodes(ctx, graph.NodeFilter{FilePath: fp})
		if err == nil {
			for _, n := range fileNodes {
				if n.Type == graph.NodeDBModel || n.Type == graph.NodeDomainModel {
					impactCtx, err := r.ctxBuilder.BuildModelImpactContext(ctx, n.ID)
					if err == nil {
						parts = append(parts, impactCtx)
					}
				}
			}
		}
	}

	contextText := strings.Join(parts, "\n\n")

	// Build the branch description for the prompt.
	branchDesc := fmt.Sprintf("git diff %s", diffRef)
	if len(repoPaths) > 0 {
		info, err := gitutil.GetBranchInfo(repoPaths[0])
		if err == nil && info.IsFeatureBranch {
			branchDesc = fmt.Sprintf("changes on branch %s compared to %s", info.CurrentBranch, info.DefaultBranch)
		}
	}

	// Include commits on the branch for additional context.
	commitInfo := ""
	if len(repoPaths) > 0 {
		info, err := gitutil.GetBranchInfo(repoPaths[0])
		if err == nil && info.IsFeatureBranch {
			commits, err := gitutil.GetCommitsBetween(repoPaths[0], info.DefaultBranch)
			if err == nil && len(commits) > 0 {
				var commitLines []string
				for _, c := range commits {
					commitLines = append(commitLines, fmt.Sprintf("- %s: %s (%s)", c.Hash[:8], c.Message, c.Author))
				}
				commitInfo = "\n\nCommits on this branch:\n" + strings.Join(commitLines, "\n")
			}
		}
	}

	reviewQuery := fmt.Sprintf(
		"Review the following code changes (%s).\n\n"+
			"Changed files: %s%s\n\n"+
			"Raw diff output:\n```\n%s\n```\n\n"+
			"Please review for: convention adherence, pattern deviations, missing tests, "+
			"complexity issues, and security concerns.",
		branchDesc,
		strings.Join(changedFiles, ", "),
		commitInfo,
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
