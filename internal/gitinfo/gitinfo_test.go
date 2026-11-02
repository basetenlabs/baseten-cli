package gitinfo_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/gitinfo"
	"github.com/stretchr/testify/require"
)

// gitRepo creates a repository with one commit in a temp directory.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q")
	writeFile(t, dir, "config.yaml", "model_name: test\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := c.CombinedOutput()
	require.NoError(t, err, string(out))
	return string(out)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

func Test_Collect_NotARepo(t *testing.T) {
	require.Nil(t, gitinfo.Collect(t.Context(), t.TempDir()))
}

func Test_Collect_CleanUntaggedRepo(t *testing.T) {
	dir := gitRepo(t)

	info := gitinfo.Collect(t.Context(), dir)

	require.NotNil(t, info)
	require.Len(t, info.LatestCommitSHA, 40)
	require.Nil(t, info.LatestTag)
	require.Nil(t, info.CommitsSinceTag)
	require.False(t, info.HasUncommittedChanges)
}

func Test_Collect_TagCommitsSinceAndDirty(t *testing.T) {
	dir := gitRepo(t)
	run(t, dir, "tag", "v1.4.0")
	writeFile(t, dir, "model.py", "class Model: pass\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "second")
	// An uncommitted edit after the last commit.
	writeFile(t, dir, "model.py", "class Model:\n    pass\n")

	info := gitinfo.Collect(t.Context(), dir)

	require.NotNil(t, info)
	require.Equal(t, "v1.4.0", *info.LatestTag)
	require.Equal(t, 1, *info.CommitsSinceTag)
	require.True(t, info.HasUncommittedChanges)
}

// A model directory nested inside a repository reports the repository's state.
func Test_Collect_Subdirectory(t *testing.T) {
	dir := gitRepo(t)
	sub := filepath.Join(dir, "models", "llama")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	info := gitinfo.Collect(t.Context(), sub)

	require.NotNil(t, info)
	require.Equal(t, gitinfo.Collect(t.Context(), dir).LatestCommitSHA, info.LatestCommitSHA)
}
