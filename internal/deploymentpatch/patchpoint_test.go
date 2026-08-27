package deploymentpatch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/basetenlabs/baseten-go/client/modelarchive"
	"github.com/stretchr/testify/require"
)

func TestBuildPatchPointRejectsSymlinkOutsideTree(t *testing.T) {
	skipWithoutSymlinks(t)
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", []byte("model_name: x\n"))
	require.NoError(t, os.Symlink("/etc/hosts", filepath.Join(dir, "hosts")))

	// A patch point is built from the archive walk, so watch refuses the tree
	// a push would refuse rather than hashing a file the server never receives.
	_, err := BuildPatchPoint(t.Context(), BuildPatchPointOptions{Dir: dir})
	require.Error(t, err)
	var invalid *modelarchive.InvalidSymlinkError
	require.True(t, errors.As(err, &invalid), "expected InvalidSymlinkError, got %v", err)
	require.Equal(t, "hosts", invalid.ArchivePath)
}

func TestBuildPatchPointIgnoredSymlinkNotRejected(t *testing.T) {
	skipWithoutSymlinks(t)
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", []byte("model_name: x\n"))
	writeFile(t, dir, ".truss_ignore", []byte(".devenv/\n"))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".devenv", "gc"), 0o755))
	require.NoError(t, os.Symlink("/nix/store/whatever", filepath.Join(dir, ".devenv", "gc", "shell")))

	// Ignoring is applied before an entry is classified, so ignoring an
	// unarchivable symlink is a way out of the error. The bare directory stays
	// as a null entry, as it does in Truss, but nothing inside it is walked.
	point, err := BuildPatchPoint(t.Context(), BuildPatchPointOptions{Dir: dir})
	require.NoError(t, err)
	require.Contains(t, point.ContentHashes, ".devenv")
	require.Nil(t, point.ContentHashes[".devenv"])
	require.NotContains(t, point.ContentHashes, ".devenv/gc")
}
