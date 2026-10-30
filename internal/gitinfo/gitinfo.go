// Package gitinfo collects the git details attached to a model push.
package gitinfo

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

// Info is the state of the git checkout a model is pushed from.
type Info struct {
	LatestCommitSHA string `json:"latest_commit_sha"`
	// LatestTag is the nearest tag reachable from HEAD; nil when there is none.
	LatestTag *string `json:"latest_tag"`
	// CommitsSinceTag is nil when there is no tag.
	CommitsSinceTag       *int `json:"commits_since_tag"`
	HasUncommittedChanges bool `json:"has_uncommitted_changes"`
}

// Collect returns the git info for dir, or nil when dir is not inside a git
// repository or git is not installed.
func Collect(ctx context.Context, dir string) *Info {
	sha, ok := runGit(ctx, dir, "rev-parse", "HEAD")
	if !ok || sha == "" {
		return nil
	}
	info := &Info{LatestCommitSHA: sha}
	if tag, ok := runGit(ctx, dir, "describe", "--tags", "--abbrev=0"); ok && tag != "" {
		info.LatestTag = &tag
		if count, ok := runGit(ctx, dir, "rev-list", tag+"..HEAD", "--count"); ok {
			if n, err := strconv.Atoi(count); err == nil {
				info.CommitsSinceTag = &n
			}
		}
	}
	status, _ := runGit(ctx, dir, "status", "--porcelain")
	info.HasUncommittedChanges = status != ""
	return info
}

// runGit returns git's trimmed stdout and whether it exited successfully.
func runGit(ctx context.Context, dir string, args ...string) (string, bool) {
	c := exec.CommandContext(ctx, "git", args...)
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}
