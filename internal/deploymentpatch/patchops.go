package deploymentpatch

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/basetenlabs/baseten-go/client/modelarchive"
	"gopkg.in/yaml.v3"
)

// Default source-tree layout, from truss/base/truss_config.py
// (DEFAULT_MODEL_MODULE_DIR / DEFAULT_BUNDLED_PACKAGES_DIR /
// DEFAULT_DATA_DIRECTORY). calc_truss_patch resolves these off the *new* config
// (via TrussSpec) to decide which directory a changed path belongs to.
const (
	defaultModelModuleDir      = "model"
	defaultBundledPackagesDir  = "packages"
	defaultDataDir             = "data"
	defaultExternalDataBackend = "http_public"
)

// ErrNeedsFullDeploy signals that the change between two patch points cannot be
// expressed as a patch and the deployment must be re-pushed. It corresponds to
// calc_truss_patch returning None (the caller should tell the user to run a full
// `baseten model push`). Only two changes trigger it, matching Truss: removing
// config.yaml, and any change strictly under the data directory.
var ErrNeedsFullDeploy = errors.New("deploymentpatch: change cannot be patched, full deploy required")

// BuildPatchOpsOptions configures [BuildPatchOps].
type BuildPatchOpsOptions struct {
	// Dir is the model directory holding the current (Next) source. Required;
	// file contents for model-code and package ops are read from here.
	Dir string
	// Prev is the patch point being patched off of (the server's running or
	// pending point). Required.
	Prev *managementapi.DeploymentPatchPoint
	// Next is the current local source state, as built by [BuildPatchPoint].
	// Required.
	Next *managementapi.DeploymentPatchPoint
	// HotReload requests hot reload. Mirroring Truss, it only takes effect when
	// every resulting op is a model-code change; mixed patches fall back to a
	// cold restart.
	HotReload bool
}

// BuildPatchOps computes the patch ops that take Prev to Next, porting
// truss/truss_handle/patch/calc_patch.py (calc_truss_patch). It returns
// [ErrNeedsFullDeploy] when the change can't be patched.
//
// Unlike Truss, which re-globs and re-hashes the directory inside
// _calc_changed_paths, both sides already carry content_hashes (Next from
// BuildPatchPoint, Prev from the server), so we diff those maps directly.
// Likewise the config / env-var / external-data / requirement ops are derived
// from each point's stored Config text and Requirements list rather than being
// gated on config.yaml or the requirements file appearing in the changed paths:
// those fields only differ when Truss would have entered its config block, so
// the result is equivalent (see the per-section comments below).
func BuildPatchOps(ctx context.Context, opts BuildPatchOpsOptions) ([]managementapi.CreateDeploymentPatchRequest_PatchOps_Item, error) {
	if opts.Dir == "" {
		return nil, errors.New("deploymentpatch: Dir is required")
	}
	if opts.Prev == nil || opts.Next == nil {
		return nil, errors.New("deploymentpatch: Prev and Next are required")
	}

	prevConfig, err := configMapFromText(opts.Prev.Config)
	if err != nil {
		return nil, fmt.Errorf("deploymentpatch: parse previous config: %w", err)
	}
	nextConfig, err := configMapFromText(opts.Next.Config)
	if err != nil {
		return nil, fmt.Errorf("deploymentpatch: parse next config: %w", err)
	}

	// Truss resolves the patchable directories from the NEW config (TrussSpec
	// reads config.yaml off disk = our Next config).
	modelDir := configStringDefault(nextConfig, "model_module_dir", defaultModelModuleDir)
	bundledDir := configStringDefault(nextConfig, "bundled_packages_dir", defaultBundledPackagesDir)
	dataDir := configStringDefault(nextConfig, "data_dir", defaultDataDir)

	// Re-filter the previous point's paths through the CURRENT directory's ignore
	// rules before diffing. calc_truss_patch does this in _calc_changed_paths: it
	// runs _calc_unignored_paths over BOTH the new glob and the stored previous
	// signature's keys with the same (current) ignore patterns. Next is already
	// filtered with this ignore (BuildPatchPoint used the same resolver on
	// opts.Dir), but Prev came from the server filtered with whatever ignore was
	// in effect when it was stored. If a tracked path becomes newly ignored (e.g.
	// the user adds a pattern to .truss_ignore), re-filtering drops it from Prev
	// so it diffs to nothing - matching Truss, which leaves the now-ignored file
	// untouched in the container. Without this we would see it as removed and emit
	// a spurious REMOVE the container applier was never meant to receive.
	ignore, err := ResolveTrussIgnore(opts.Dir)
	if err != nil {
		return nil, err
	}
	prevHashes, err := filterIgnoredHashes(ctx, ignore, opts.Prev.ContentHashes)
	if err != nil {
		return nil, err
	}

	added, removed, updated := diffContentHashes(prevHashes, opts.Next.ContentHashes)

	var modelCodeOps []managementapi.DeploymentPatchOpModelCode
	var packageOps []managementapi.DeploymentPatchOpPackage

	// Removed paths. Mirrors the first loop of calc_truss_patch: only removals
	// under the model/bundled dirs are patchable; removing config.yaml or
	// touching the data dir forces a full deploy; anything else is ignored.
	for _, p := range removed {
		switch {
		case strictlyUnder(p, modelDir):
			modelCodeOps = append(modelCodeOps, managementapi.DeploymentPatchOpModelCode{
				Action: managementapi.DeploymentPatchAction_REMOVE,
				Path:   relativeTo(p, modelDir),
			})
		case p == patchPointConfigFileName:
			return nil, ErrNeedsFullDeploy
		case strictlyUnder(p, bundledDir):
			packageOps = append(packageOps, managementapi.DeploymentPatchOpPackage{
				Action: managementapi.DeploymentPatchAction_REMOVE,
				Path:   relativeTo(p, bundledDir),
			})
		case strictlyUnder(p, dataDir):
			return nil, ErrNeedsFullDeploy
		}
	}

	// Added + updated paths. Mirrors the second loop of calc_truss_patch.
	// config.yaml and the requirements file are intentionally skipped here and
	// handled by the config/requirement derivation below; data-dir changes force
	// a full deploy; everything else outside the model/bundled dirs is ignored.
	for _, ap := range addedAndUpdated(added, updated) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch {
		case strictlyUnder(ap.path, modelDir):
			// Skip directory entries (null content hash); Truss does the same via
			// `if not full_path.is_file(): continue`.
			if opts.Next.ContentHashes[ap.path] == nil {
				continue
			}
			content, contentBytes, err := readFileForPatch(filepath.Join(opts.Dir, filepath.FromSlash(ap.path)))
			if err != nil {
				return nil, err
			}
			modelCodeOps = append(modelCodeOps, managementapi.DeploymentPatchOpModelCode{
				Action:       ap.action,
				Path:         relativeTo(ap.path, modelDir),
				Content:      content,
				ContentBytes: contentBytes,
			})
		case strictlyUnder(ap.path, bundledDir):
			if opts.Next.ContentHashes[ap.path] == nil {
				continue
			}
			content, contentBytes, err := readFileForPatch(filepath.Join(opts.Dir, filepath.FromSlash(ap.path)))
			if err != nil {
				return nil, err
			}
			packageOps = append(packageOps, managementapi.DeploymentPatchOpPackage{
				Action:       ap.action,
				Path:         relativeTo(ap.path, bundledDir),
				Content:      content,
				ContentBytes: contentBytes,
			})
		case strictlyUnder(ap.path, dataDir):
			return nil, ErrNeedsFullDeploy
		}
	}

	// Config op. Truss emits a config replace only when config.yaml itself
	// changed (its `config != prev_config` check inside _calc_general_config_patches
	// after entering on the config.yaml path); the stored Config text is exactly
	// that file, so comparing it is equivalent. We send the parsed YAML map, the
	// same shape `baseten model push` sends (readModelConfigYAML), which the
	// server re-parses with TrussConfig.from_dict.
	configChanged := opts.Prev.Config != opts.Next.Config
	var configOp *managementapi.DeploymentPatchOpConfig
	if configChanged {
		configOp = &managementapi.DeploymentPatchOpConfig{Config: nextConfig}
	}

	envVarOps := diffEnvVars(prevConfig, nextConfig)
	externalDataOps := diffExternalData(prevConfig, nextConfig)
	requirementOps := diffRequirements(prevConfig, nextConfig, opts.Prev.Requirements, opts.Next.Requirements)

	// Hot reload only when every op is a model-code change (Truss:
	// `all(p.type == MODEL_CODE for p in patch_ops)`); mixed patches cold-restart.
	onlyModelCode := configOp == nil && len(packageOps) == 0 &&
		len(envVarOps) == 0 && len(externalDataOps) == 0 && len(requirementOps) == 0
	if opts.HotReload && onlyModelCode {
		for i := range modelCodeOps {
			hotReload := true
			modelCodeOps[i].HotReload = &hotReload
		}
	}

	return assemblePatchOps(modelCodeOps, packageOps, configOp, envVarOps, externalDataOps, requirementOps)
}

// changedPath pairs an added/updated path with its action.
type changedPath struct {
	path   string
	action managementapi.DeploymentPatchAction
}

// addedAndUpdated merges the added and updated path sets into one ordered slice
// tagged with the right action, sorted by path for deterministic output.
func addedAndUpdated(added, updated []string) []changedPath {
	out := make([]changedPath, 0, len(added)+len(updated))
	for _, p := range added {
		out = append(out, changedPath{path: p, action: managementapi.DeploymentPatchAction_ADD})
	}
	for _, p := range updated {
		out = append(out, changedPath{path: p, action: managementapi.DeploymentPatchAction_UPDATE})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// filterIgnoredHashes returns the content-hash map with any path the ignore
// predicate matches removed. It reproduces _calc_unignored_paths applied to the
// previous signature's keys (see the call site for why). The ignore predicates
// in use (modelarchive.DefaultIgnoreFile and the .truss_ignore matcher) decide
// purely from RelPath, but a synthetic entry is passed anyway so a future
// Entry-reading predicate does not nil-deref; its dir-ness comes from the hash
// value (null = directory, mirroring how BuildPatchPoint records directories).
func filterIgnoredHashes(ctx context.Context, ignore modelarchive.IgnoreFileFunc, hashes map[string]*string) (map[string]*string, error) {
	out := make(map[string]*string, len(hashes))
	for relPath, hash := range hashes {
		ignored, err := ignore(ctx, modelarchive.IgnoreFileOptions{
			RelPath: relPath,
			Entry:   patchPathEntry{name: path.Base(relPath), isDir: hash == nil},
		})
		if err != nil {
			return nil, err
		}
		if ignored {
			continue
		}
		out[relPath] = hash
	}
	return out, nil
}

// patchPathEntry is a minimal fs.DirEntry for a content-hash key, used only to
// satisfy the IgnoreFileFunc signature when re-filtering an already-walked path
// set (we no longer have the real DirEntry from the filesystem walk).
type patchPathEntry struct {
	name  string
	isDir bool
}

func (e patchPathEntry) Name() string { return e.name }
func (e patchPathEntry) IsDir() bool  { return e.isDir }
func (e patchPathEntry) Type() fs.FileMode {
	if e.isDir {
		return fs.ModeDir
	}
	return 0
}

// Info is not recoverable from a content-hash key alone; the ignore predicates
// do not call it, so this never fires in practice.
func (e patchPathEntry) Info() (fs.FileInfo, error) {
	return nil, fs.ErrInvalid
}

// diffContentHashes splits two content-hash maps into added (in next only),
// removed (in prev only), and updated (in both, file digest changed) path sets,
// each sorted. This stands in for Truss's _calc_changed_paths; both maps are
// already ignore-filtered, so the PYCACHE_IGNORE_PATTERNS second pass Truss
// applies is unnecessary (a kept null pycache dir appears identically on both
// sides and diffs to nothing).
func diffContentHashes(prev, next map[string]*string) (added, removed, updated []string) {
	for p := range next {
		if _, ok := prev[p]; !ok {
			added = append(added, p)
		}
	}
	for p, prevHash := range prev {
		nextHash, ok := next[p]
		if !ok {
			removed = append(removed, p)
			continue
		}
		// Only files (non-null hash on both sides) can be "updated".
		if prevHash != nil && nextHash != nil && *prevHash != *nextHash {
			updated = append(updated, p)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(updated)
	return added, removed, updated
}

// diffEnvVars ports _calc_env_var_patches: removed, added, then updated (by
// value change) environment variables. The verbatim config text only differs
// when config.yaml changed, so deriving this unconditionally matches Truss
// entering its config block only on a config.yaml change.
func diffEnvVars(prevConfig, nextConfig map[string]any) []managementapi.DeploymentPatchOpEnvVar {
	prev := configEnvVars(prevConfig)
	next := configEnvVars(nextConfig)
	var ops []managementapi.DeploymentPatchOpEnvVar

	for _, name := range sortedKeys(prev) {
		if _, ok := next[name]; !ok {
			value := prev[name]
			ops = append(ops, managementapi.DeploymentPatchOpEnvVar{
				Action: managementapi.DeploymentPatchAction_REMOVE,
				Name:   name,
				Value:  &value,
			})
		}
	}
	for _, name := range sortedKeys(next) {
		if _, ok := prev[name]; !ok {
			value := next[name]
			ops = append(ops, managementapi.DeploymentPatchOpEnvVar{
				Action: managementapi.DeploymentPatchAction_ADD,
				Name:   name,
				Value:  &value,
			})
		}
	}
	for _, name := range sortedKeys(next) {
		prevValue, ok := prev[name]
		if ok && prevValue != next[name] {
			value := next[name]
			ops = append(ops, managementapi.DeploymentPatchOpEnvVar{
				Action: managementapi.DeploymentPatchAction_UPDATE,
				Name:   name,
				Value:  &value,
			})
		}
	}
	return ops
}

// diffExternalData ports _calc_external_data_patches: items are matched by whole
// normalized-dict equality, so there is no UPDATE action, only REMOVE (in prev,
// not in next) and ADD (in next, not in prev).
func diffExternalData(prevConfig, nextConfig map[string]any) []managementapi.DeploymentPatchOpExternalData {
	prev := configExternalData(prevConfig)
	next := configExternalData(nextConfig)
	var ops []managementapi.DeploymentPatchOpExternalData

	for _, item := range prev {
		if !containsExternalDataItem(next, item) {
			ops = append(ops, managementapi.DeploymentPatchOpExternalData{
				Action: managementapi.DeploymentPatchAction_REMOVE,
				Item:   item,
			})
		}
	}
	for _, item := range next {
		if !containsExternalDataItem(prev, item) {
			ops = append(ops, managementapi.DeploymentPatchOpExternalData{
				Action: managementapi.DeploymentPatchAction_ADD,
				Item:   item,
			})
		}
	}
	return ops
}

// diffRequirements ports _calc_requirements_patches + _calc_python_requirements_patches.
// The "previous" and "next" requirement lists come from the requirements file
// (the resolved Requirements stored on the patch point) when the config sets
// requirements_file, otherwise from the inline `requirements:` list.
func diffRequirements(prevConfig, nextConfig map[string]any, prevFileReqs, nextFileReqs *[]string) []managementapi.DeploymentPatchOpPythonRequirement {
	prevReqs := configInlineRequirements(prevConfig)
	if configStringDefault(prevConfig, "requirements_file", "") != "" {
		prevReqs = derefStrings(prevFileReqs)
	}
	nextReqs := configInlineRequirements(nextConfig)
	if configStringDefault(nextConfig, "requirements_file", "") != "" {
		nextReqs = derefStrings(nextFileReqs)
	}

	prev := requirementsByName(prevReqs)
	next := requirementsByName(nextReqs)
	var ops []managementapi.DeploymentPatchOpPythonRequirement

	for _, name := range sortedKeys(prev) {
		if _, ok := next[name]; ok {
			continue
		}
		removed := prev[name]
		if isURLBasedRequirement(removed) {
			// A url-based requirement can only be uninstalled via its egg tag;
			// without one Truss logs and skips the removal (use `truss push`).
			if !requirementHasEggTag(removed) {
				continue
			}
			ops = append(ops, managementapi.DeploymentPatchOpPythonRequirement{
				Action:      managementapi.DeploymentPatchAction_REMOVE,
				Requirement: removed,
			})
		} else {
			// For non-url removals Truss sends the package name, not the full line.
			ops = append(ops, managementapi.DeploymentPatchOpPythonRequirement{
				Action:      managementapi.DeploymentPatchAction_REMOVE,
				Requirement: name,
			})
		}
	}
	for _, name := range sortedKeys(next) {
		if _, ok := prev[name]; !ok {
			ops = append(ops, managementapi.DeploymentPatchOpPythonRequirement{
				Action:      managementapi.DeploymentPatchAction_ADD,
				Requirement: next[name],
			})
		}
	}
	for _, name := range sortedKeys(next) {
		if prevReq, ok := prev[name]; ok && prevReq != next[name] {
			ops = append(ops, managementapi.DeploymentPatchOpPythonRequirement{
				Action:      managementapi.DeploymentPatchAction_UPDATE,
				Requirement: next[name],
			})
		}
	}
	return ops
}

// assemblePatchOps marshals each typed op into the request union. The relative
// order of op kinds does not affect application (the server applies model-code
// patches directly and aggregates the config), so we emit a deterministic order.
func assemblePatchOps(
	modelCodeOps []managementapi.DeploymentPatchOpModelCode,
	packageOps []managementapi.DeploymentPatchOpPackage,
	configOp *managementapi.DeploymentPatchOpConfig,
	envVarOps []managementapi.DeploymentPatchOpEnvVar,
	externalDataOps []managementapi.DeploymentPatchOpExternalData,
	requirementOps []managementapi.DeploymentPatchOpPythonRequirement,
) ([]managementapi.CreateDeploymentPatchRequest_PatchOps_Item, error) {
	var items []managementapi.CreateDeploymentPatchRequest_PatchOps_Item
	add := func(from func(*managementapi.CreateDeploymentPatchRequest_PatchOps_Item) error) error {
		var item managementapi.CreateDeploymentPatchRequest_PatchOps_Item
		if err := from(&item); err != nil {
			return fmt.Errorf("deploymentpatch: encode patch op: %w", err)
		}
		items = append(items, item)
		return nil
	}

	for _, op := range modelCodeOps {
		if err := add(func(i *managementapi.CreateDeploymentPatchRequest_PatchOps_Item) error {
			return i.FromDeploymentPatchOpModelCode(op)
		}); err != nil {
			return nil, err
		}
	}
	for _, op := range packageOps {
		if err := add(func(i *managementapi.CreateDeploymentPatchRequest_PatchOps_Item) error {
			return i.FromDeploymentPatchOpPackage(op)
		}); err != nil {
			return nil, err
		}
	}
	if configOp != nil {
		if err := add(func(i *managementapi.CreateDeploymentPatchRequest_PatchOps_Item) error {
			return i.FromDeploymentPatchOpConfig(*configOp)
		}); err != nil {
			return nil, err
		}
	}
	for _, op := range envVarOps {
		if err := add(func(i *managementapi.CreateDeploymentPatchRequest_PatchOps_Item) error {
			return i.FromDeploymentPatchOpEnvVar(op)
		}); err != nil {
			return nil, err
		}
	}
	for _, op := range externalDataOps {
		if err := add(func(i *managementapi.CreateDeploymentPatchRequest_PatchOps_Item) error {
			return i.FromDeploymentPatchOpExternalData(op)
		}); err != nil {
			return nil, err
		}
	}
	for _, op := range requirementOps {
		if err := add(func(i *managementapi.CreateDeploymentPatchRequest_PatchOps_Item) error {
			return i.FromDeploymentPatchOpPythonRequirement(op)
		}); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// readFileForPatch reads a model-code/package file into the patch op's content
// fields: UTF-8 text goes in content (Truss reads via read_text, which also
// normalizes universal newlines, so we do too), binary goes in content_bytes as
// base64 (Truss's UnicodeDecodeError fallback).
func readFileForPatch(fullPath string) (content *string, contentBytes *string, err error) {
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, nil, fmt.Errorf("deploymentpatch: read %s: %w", fullPath, err)
	}
	if utf8.Valid(data) {
		text := normalizeNewlines(string(data))
		return &text, nil, nil
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	return nil, &encoded, nil
}

// normalizeNewlines reproduces Python read_text universal-newline translation:
// CRLF and lone CR both become LF.
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// strictlyUnder reports whether path is under parent but not equal to it,
// matching _strictly_under. Content-hash keys are forward-slash relative paths,
// so we use "/" rather than the OS separator Truss uses on already-relative keys.
func strictlyUnder(path, parent string) bool {
	return strings.HasPrefix(path, parent+"/")
}

// relativeTo returns path relative to parent (Truss's _relative_to). strictlyUnder
// is always checked first, so parent is guaranteed to be a prefix.
func relativeTo(path, parent string) string {
	return strings.TrimPrefix(path, parent+"/")
}

func configMapFromText(text string) (map[string]any, error) {
	config := map[string]any{}
	if err := yaml.Unmarshal([]byte(text), &config); err != nil {
		return nil, err
	}
	if config == nil {
		config = map[string]any{}
	}
	return config, nil
}

func configStringDefault(config map[string]any, key, fallback string) string {
	if v, ok := config[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

// configEnvVars extracts environment_variables as a string map. Truss types this
// as dict[str, str]; non-string YAML scalars (e.g. a bare number) are stringified
// defensively rather than dropped.
func configEnvVars(config map[string]any) map[string]string {
	out := map[string]string{}
	raw, _ := config["environment_variables"].(map[string]any)
	for name, value := range raw {
		if s, ok := value.(string); ok {
			out[name] = s
		} else {
			out[name] = fmt.Sprintf("%v", value)
		}
	}
	return out
}

// configExternalData extracts external_data items normalized to match Truss's
// ExternalDataItem.model_dump(exclude_none=True): url and local_data_path are
// required, backend defaults to "http_public" and is always present, name is
// included only when set.
func configExternalData(config map[string]any) []map[string]string {
	raw, _ := config["external_data"].([]any)
	var items []map[string]string
	for _, entry := range raw {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		item := map[string]string{}
		if url, ok := entryMap["url"].(string); ok {
			item["url"] = url
		}
		if localPath, ok := entryMap["local_data_path"].(string); ok {
			item["local_data_path"] = localPath
		}
		item["backend"] = configStringDefault(entryMap, "backend", defaultExternalDataBackend)
		if name, ok := entryMap["name"].(string); ok && name != "" {
			item["name"] = name
		}
		items = append(items, item)
	}
	return items
}

func configInlineRequirements(config map[string]any) []string {
	raw, _ := config["requirements"].([]any)
	var out []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// requirementsByName maps each requirement to its identified name, last write
// wins on a name collision (Truss's create_requirement_map behaves the same).
func requirementsByName(reqs []string) map[string]string {
	out := map[string]string{}
	for _, req := range reqs {
		out[identifyRequirementName(req)] = req
	}
	return out
}

func containsExternalDataItem(items []map[string]string, target map[string]string) bool {
	for _, item := range items {
		if externalDataItemsEqual(item, target) {
			return true
		}
	}
	return false
}

func externalDataItemsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func derefStrings(s *[]string) []string {
	if s == nil {
		return nil
	}
	return *s
}
