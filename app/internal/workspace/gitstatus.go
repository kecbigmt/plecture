package workspace

import (
	"context"
	"strconv"
	"strings"

	"github.com/plecture/plect/app/internal/procexec"
)

// WorktreeStatus holds the git status of a worktree.
// WorktreeStatus holds the git status of a worktree.
// Dirty reports tracked file modifications only (staged + unstaged).
// UntrackedFiles is counted separately. Both contribute to whether
// 'git worktree remove' requires --force.
type WorktreeStatus struct {
	Dirty          bool `json:"dirty"`
	UntrackedFiles int  `json:"untracked_files"`
	Ahead          int  `json:"ahead"`
	Behind         int  `json:"behind"`
}

// GetWorktreeStatus gathers git status information for a worktree path. ctx
// bounds each underlying git invocation; a cancelled or expired ctx
// terminates the in-flight process and GetWorktreeStatus returns its error.
// Returns nil if the path does not exist or is not a git worktree.
func GetWorktreeStatus(ctx context.Context, wtPath string) (*WorktreeStatus, error) {
	status := &WorktreeStatus{}

	// Check dirty state (modified/staged files)
	dirty, err := isDirty(ctx, wtPath)
	if err != nil {
		return nil, err
	}
	status.Dirty = dirty

	// Count untracked files
	untracked, err := countUntrackedFiles(ctx, wtPath)
	if err != nil {
		return nil, err
	}
	status.UntrackedFiles = untracked

	// Get ahead/behind counts
	ahead, behind, err := getAheadBehind(ctx, wtPath)
	if err != nil {
		// Not fatal: branch may not have an upstream
		ahead, behind = 0, 0
	}
	status.Ahead = ahead
	status.Behind = behind

	return status, nil
}

// isDirty checks if the worktree has staged or unstaged changes.
func isDirty(ctx context.Context, wtPath string) (bool, error) {
	out, _, err := procexec.Default.Run(ctx, wtPath, false, "git", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// countUntrackedFiles counts untracked files in the worktree.
func countUntrackedFiles(ctx context.Context, wtPath string) (int, error) {
	out, _, err := procexec.Default.Run(ctx, wtPath, false, "git", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return 0, err
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return 0, nil
	}
	return len(strings.Split(trimmed, "\n")), nil
}

// getAheadBehind returns the number of commits ahead and behind the upstream.
func getAheadBehind(ctx context.Context, wtPath string) (ahead, behind int, err error) {
	out, _, err := procexec.Default.Run(ctx, wtPath, false, "git", "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
	if err != nil {
		return 0, 0, err
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) != 2 {
		return 0, 0, nil
	}
	behind, _ = strconv.Atoi(parts[0])
	ahead, _ = strconv.Atoi(parts[1])
	return ahead, behind, nil
}
