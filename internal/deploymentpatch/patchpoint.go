// Package deploymentpatch builds the deployment-patch primitives the watch
// loop needs: a [managementapi.DeploymentPatchPoint] describing a source tree
// (BuildPatchPoint) and the patch ops between two points (BuildPatchOps).
//
// The per-file content hashes here must reproduce Truss's Python implementation
// bit-for-bit: the server-side build computes the signature with Python
// (truss/truss_handle/patch/{signature,dir_signature}.py), so a Go watch client
// that diffs against it has to match exactly.
package deploymentpatch

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/basetenlabs/baseten-go/client/modelarchive"
	"github.com/zeebo/blake3"
	"gopkg.in/yaml.v3"
)

const (
	patchPointConfigFileName = "config.yaml"
	pyprojectFileName        = "pyproject.toml"
	uvLockFileName           = "uv.lock"
)

// BuildPatchPointOptions configures [BuildPatchPoint].
type BuildPatchPointOptions struct {
	// Dir is the model directory whose source state is captured. Required.
	Dir string
}

// BuildPatchPoint walks opts.Dir and returns the patch point describing its
// current source state: the per-path content hashes, the verbatim config.yaml,
// and the requirements resolved from the config's requirements file (empty when
// requirements are inline).
//
// This reproduces Truss's watch-client signature (calc_truss_signature over
// the local directory with the .truss_ignore patterns), so the result can be
// diffed against the server's patch state.
func BuildPatchPoint(ctx context.Context, opts BuildPatchPointOptions) (*managementapi.DeploymentPatchPoint, error) {
	if opts.Dir == "" {
		return nil, errors.New("deploymentpatch: Dir is required")
	}
	ignore, err := ResolveTrussIgnore(opts.Dir)
	if err != nil {
		return nil, err
	}

	contentHashes, err := walkContentHashes(ctx, opts.Dir, ignore)
	if err != nil {
		return nil, err
	}

	// The config is the on-disk config.yaml read verbatim. Truss's patch path
	// (calc_truss_patch) likewise reads config.yaml straight off disk; the
	// config-override knob (modelarchive ConfigYAMLOverride) is push/archive
	// only and has no analog in watch/patch, so there is nothing to override
	// here.
	configPath := filepath.Join(opts.Dir, patchPointConfigFileName)
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("deploymentpatch: read %s: %w", configPath, err)
	}

	requirements, err := resolveRequirements(opts.Dir, configBytes)
	if err != nil {
		return nil, err
	}

	return &managementapi.DeploymentPatchPoint{
		ContentHashes: contentHashes,
		Config:        string(configBytes),
		Requirements:  &requirements,
	}, nil
}

// fileContentHashHex returns the hex-encoded blake3 digest of a file's bytes,
// matching Truss's file_content_hash_str.
func fileContentHashHex(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("deploymentpatch: open %s: %w", path, err)
	}
	defer f.Close()

	hasher := blake3.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", fmt.Errorf("deploymentpatch: hash %s: %w", path, err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// walkContentHashes builds the content-hashes map: every non-ignored path
// relative to dir (forward-slash) maps to its file digest (hex) or nil for a
// directory. Directories (including empty ones) are retained to mirror Truss's
// glob("**/*") path set; an ignored directory prunes its whole subtree.
func walkContentHashes(ctx context.Context, dir string, ignore modelarchive.IgnoreFileFunc) (map[string]*string, error) {
	contentHashes := map[string]*string{}
	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == dir {
			return nil
		}

		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)

		ignored, err := ignore(ctx, modelarchive.IgnoreFileOptions{RelPath: relSlash, Entry: d})
		if err != nil {
			return err
		}
		if ignored {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			contentHashes[relSlash] = nil
			return nil
		}
		// Non-regular files (symlinks, devices, sockets) are not part of the
		// archived tree, so they are skipped here too.
		if !d.Type().IsRegular() {
			return nil
		}

		hashHex, err := fileContentHashHex(p)
		if err != nil {
			return err
		}
		contentHashes[relSlash] = &hashHex
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return contentHashes, nil
}

// resolveRequirements reproduces TrussConfig.load_requirements_file_from_filepath
// (and load_requirements_from_file): it returns ONLY the requirements pulled
// from the config's requirements_file. Inline requirements (the config's
// `requirements:` list) deliberately yield an empty list here.
//
// Why empty for inline: the patch point's requirements field exists so the
// next patch can be diffed against it cross-tick. But Truss's calc_patch only
// reads this field as the "previous" side when a requirements_file is set
// (_calc_requirements_patches); for inline requirements it diffs the config's
// `requirements:` list out of the (verbatim) config text instead. So inline
// requirement changes ride the config diff, not this field, and duplicating
// them here would be redundant.
//
// File-type detection mirrors TrussConfig._detect_requirements_file_type: the
// basename decides. uv.lock and pyproject.toml are resolved from a
// pyproject.toml's [project].dependencies; everything else is treated as a pip
// requirements file.
func resolveRequirements(dir string, configBytes []byte) ([]string, error) {
	var cfg struct {
		RequirementsFile *string `yaml:"requirements_file"`
	}
	if err := yaml.Unmarshal(configBytes, &cfg); err != nil {
		return nil, fmt.Errorf("deploymentpatch: parse %s: %w", patchPointConfigFileName, err)
	}
	if cfg.RequirementsFile == nil || *cfg.RequirementsFile == "" {
		return []string{}, nil
	}
	relReq := filepath.FromSlash(*cfg.RequirementsFile)

	switch path.Base(filepath.ToSlash(*cfg.RequirementsFile)) {
	case uvLockFileName:
		// Truss bypasses uv.lock for patching and resolves the sibling
		// pyproject.toml instead (TrussConfig._resolve_pyproject_path: the
		// uv.lock's parent dir / pyproject.toml), since the lock file is large
		// and harder to parse and the declared deps are the source of truth.
		return parsePyprojectDependencies(filepath.Join(dir, filepath.Dir(relReq), pyprojectFileName))
	case pyprojectFileName:
		return parsePyprojectDependencies(filepath.Join(dir, relReq))
	}

	// Pip requirements file: parse_requirement_string strips each line and
	// drops blank and comment (#) lines, keeping the rest verbatim.
	reqPath := filepath.Join(dir, relReq)
	data, err := os.ReadFile(reqPath)
	if err != nil {
		return nil, fmt.Errorf("deploymentpatch: read requirements_file %s: %w", reqPath, err)
	}
	requirements := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		stripped := strings.TrimSpace(line)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}
		requirements = append(requirements, stripped)
	}
	return requirements, nil
}

// parsePyprojectDependencies reads [project].dependencies from a pyproject.toml,
// reproducing Truss's parse_requirements_from_pyproject.
//
// Divergence from Truss (intentional): Truss filters the dependencies through
// packaging.Requirement (_is_valid_requirement) and drops any that fail to
// parse. We do not replicate PEP 508 validation - the patch op only carries
// requirement strings, and project.dependencies entries are PEP 508 specifiers
// by spec, so the filter drops nothing for a well-formed pyproject. We keep
// every non-blank entry verbatim; a malformed entry (which Truss would drop and
// which would fail the build anyway) is the only case where the lists would
// differ.
func parsePyprojectDependencies(pyprojectPath string) ([]string, error) {
	var parsed struct {
		Project struct {
			Dependencies []string `toml:"dependencies"`
		} `toml:"project"`
	}
	if _, err := toml.DecodeFile(pyprojectPath, &parsed); err != nil {
		return nil, fmt.Errorf("deploymentpatch: parse pyproject %s: %w", pyprojectPath, err)
	}
	requirements := []string{}
	for _, dep := range parsed.Project.Dependencies {
		if strings.TrimSpace(dep) == "" {
			continue
		}
		requirements = append(requirements, dep)
	}
	return requirements, nil
}
