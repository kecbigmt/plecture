package workspace

import (
	"os/exec"
	"strconv"
	"strings"
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

// GetWorktreeStatus gathers git status information for a worktree path.
// Returns nil if the path does not exist or is not a git worktree.
func GetWorktreeStatus(wtPath string) (*WorktreeStatus, error) {
	status := &WorktreeStatus{}

	// Check dirty state (modified/staged files)
	dirty, err := isDirty(wtPath)
	if err != nil {
		return nil, err
	}
	status.Dirty = dirty

	// Count untracked files
	untracked, err := countUntrackedFiles(wtPath)
	if err != nil {
		return nil, err
	}
	status.UntrackedFiles = untracked

	// Get ahead/behind counts
	ahead, behind, err := getAheadBehind(wtPath)
	if err != nil {
		// Not fatal: branch may not have an upstream
		ahead, behind = 0, 0
	}
	status.Ahead = ahead
	status.Behind = behind

	return status, nil
}

// isDirty checks if the worktree has staged or unstaged changes.
func isDirty(wtPath string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=no")
	cmd.Dir = wtPath
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// countUntrackedFiles counts untracked files in the worktree.
func countUntrackedFiles(wtPath string) (int, error) {
	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = wtPath
	out, err := cmd.Output()
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
func getAheadBehind(wtPath string) (ahead, behind int, err error) {
	cmd := exec.Command("git", "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
	cmd.Dir = wtPath
	out, err := cmd.Output()
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
