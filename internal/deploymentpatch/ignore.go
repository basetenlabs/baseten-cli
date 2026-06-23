package deploymentpatch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/basetenlabs/baseten-go/client/modelarchive"
	gitignore "github.com/sabhiram/go-gitignore"
)

const trussIgnoreFileName = ".truss_ignore"

// ResolveTrussIgnore returns the ignore predicate for a model directory: a
// .truss_ignore file at the root of dir takes precedence, otherwise the
// built-in defaults ([modelarchive.DefaultIgnoreFile]) apply.
//
// The same predicate must drive both the archive upload (push) and the local
// source signature (watch); if they disagree on which paths exist, the watch
// client's content hashes will not line up with the server's.
func ResolveTrussIgnore(dir string) (modelarchive.IgnoreFileFunc, error) {
	contents, err := os.ReadFile(filepath.Join(dir, trussIgnoreFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return modelarchive.DefaultIgnoreFile, nil
	}
	if err != nil {
		return nil, fmt.Errorf("deploymentpatch: read %s: %w", trussIgnoreFileName, err)
	}
	return CompileTrussIgnore(contents), nil
}

// CompileTrussIgnore compiles raw .truss_ignore contents into an ignore
// predicate using gitignore semantics.
func CompileTrussIgnore(contents []byte) modelarchive.IgnoreFileFunc {
	gi := gitignore.CompileIgnoreLines(strings.Split(string(contents), "\n")...)
	return func(_ context.Context, opts modelarchive.IgnoreFileOptions) (bool, error) {
		return gi.MatchesPath(opts.RelPath), nil
	}
}
