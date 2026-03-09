package solo

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func gitRun(repoRoot string, args ...string) (string, string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	return strings.TrimSpace(out.String()), strings.TrimSpace(errBuf.String()), err
}

func ensureRepoHasCommit(repoRoot string) error {
	_, stderr, err := gitRun(repoRoot, "rev-parse", "HEAD")
	if err != nil {
		if strings.Contains(stderr, "unknown revision") || strings.Contains(stderr, "Needed a single revision") {
			return errRepoEmpty()
		}
		return errRepoEmpty()
	}
	return nil
}

func createWorktree(repoRoot, path, branch, baseRef string) error {
	for i := 0; i < 3; i++ {
		_, stderr, err := gitRun(repoRoot, "worktree", "add", path, "-b", branch, baseRef)
		if err == nil {
			return nil
		}
		if strings.Contains(stderr, "index.lock") {
			if i < 2 {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return errGitIndexLocked()
		}
		if strings.Contains(stderr, "already exists") {
			if strings.Contains(stderr, "already exists") && strings.Contains(stderr, "worktree") {
				return errWorktreeExists(path)
			}
			if strings.Contains(stderr, "already exists") && strings.Contains(stderr, "branch") {
				return errBranchExists(branch)
			}
		}
		if strings.Contains(stderr, "not a commit") || strings.Contains(stderr, "unknown revision") {
			return errBaseRefNotFound(baseRef)
		}
		if strings.Contains(strings.ToLower(stderr), "no space") {
			return errWith("DISK_FULL", "Disk full during worktree creation.", false, "Free disk and retry")
		}
		return errWorktreeError(strings.TrimSpace(stderr))
	}
	return errWorktreeError("unknown worktree error")
}

func worktreeGitStatus(repoRoot, path string) (string, []string, error) {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		rel = path
	}
	out, _, err := gitRun(repoRoot, "-C", rel, "status", "--porcelain")
	if err != nil {
		return "missing", nil, err
	}
	if strings.TrimSpace(out) == "" {
		return "clean", []string{}, nil
	}
	lines := strings.Split(out, "\n")
	files := make([]string, 0, len(lines))
	state := "dirty_unstaged"
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if len(ln) >= 3 {
			files = append(files, strings.TrimSpace(ln[3:]))
		}
		if strings.HasPrefix(ln, "UU") || strings.HasPrefix(ln, "AA") || strings.HasPrefix(ln, "DD") {
			state = "conflicts"
		} else if len(ln) >= 1 && ln[0] != ' ' {
			if state != "conflicts" {
				state = "dirty_staged"
			}
		}
	}
	return state, files, nil
}

func removeWorktree(repoRoot, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	_, stderr, err := gitRun(repoRoot, args...)
	if err != nil {
		return errGitError(stderr)
	}
	return nil
}

func deleteBranch(repoRoot, branch string, force bool) error {
	mode := "-d"
	if force {
		mode = "-D"
	}
	_, _, _ = gitRun(repoRoot, "branch", mode, branch)
	return nil
}

func commitCountAheadBehind(repoRoot, path, baseRef string) (int, int) {
	rev := fmt.Sprintf("%s...HEAD", baseRef)
	out, _, err := gitRun(repoRoot, "-C", path, "rev-list", "--left-right", "--count", rev)
	if err != nil {
		return 0, 0
	}
	var behind, ahead int
	_, _ = fmt.Sscanf(out, "%d\t%d", &behind, &ahead)
	return ahead, behind
}

// getBaseCommitSHA resolves ref to its commit SHA. Returns errBaseRefNotFound when
// git cannot resolve the ref (missing remote, typo, etc.) so the caller can surface
// the error rather than silently persisting an empty or NULL base_commit_sha.
func getBaseCommitSHA(repoRoot, ref string) (string, error) {
	sha, _, err := gitRun(repoRoot, "rev-parse", ref)
	if err != nil {
		return "", errBaseRefNotFound(ref)
	}
	return sha, nil
}
