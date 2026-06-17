package deploymentpatch

import (
	"net/url"
	"regexp"
	"strings"
)

// These primitives port Truss's requirement-name identification so the watch
// client groups the same packages Truss would when diffing requirements. See
// truss/templates/control/control/helpers/truss_patch/requirement_name_identifier.py.
//
// Per-package matching (prev vs next) only has to be self-consistent here, but
// the name we emit for a non-url REMOVE is fed straight back into the server's
// applier (TrussDirPatchApplier -> identify_requirement_name -> del reqs[name]),
// so the derivation has to agree with Truss for the common cases.

// urlBasedRequirementPattern mirrors URL_PATTERN: a requirement whose specifier
// is a URL or VCS reference rather than a PyPI name. Python's `(\+|:\/\/)` is
// `(\+|://)` here.
var urlBasedRequirementPattern = regexp.MustCompile(`^(https?|git|svn|hg|bzr)(\+|://)`)

// leadingRequirementName captures the project-name token at the start of a PEP
// 508 requirement (e.g. "numpy" from "numpy>=1.0", "requests" from
// "requests[security]==2.0"). Truss uses packaging.Requirement(req).name and
// falls back to the whole line on a parse error; we deliberately do NOT do full
// PEP 508 validation (see identifyRequirementName), we just take the leading
// name token and fall back the same way.
var leadingRequirementName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*`)

// vcsSchemes are the VCS prefixes Truss special-cases when naming a url-based
// requirement (the "git", "svn", "hg", "bzr" list in identify_requirement_name).
var vcsSchemes = []string{"git", "svn", "hg", "bzr"}

func isURLBasedRequirement(req string) bool {
	return urlBasedRequirementPattern.MatchString(strings.TrimSpace(req))
}

// identifyRequirementName ports identify_requirement_name. For url-based VCS
// requirements it builds a stable key from the scheme, host and path (a URL
// can't reliably yield a package name); for other url-based requirements it
// uses the whole line; otherwise it takes the leading name token and falls back
// to the whole line when there isn't one.
func identifyRequirementName(req string) string {
	req = strings.TrimSpace(req)
	if isURLBasedRequirement(req) {
		parsed, err := url.Parse(req)
		if err != nil {
			return req
		}
		for _, vcs := range vcsSchemes {
			if strings.HasPrefix(req, vcs+"+") {
				// Match Python: f"{vcs}+{netloc}{path.split('@')[0]}". The path
				// keeps its leading slash and we drop any @ref suffix.
				pathBeforeRef := strings.SplitN(parsed.Path, "@", 2)[0]
				return vcs + "+" + parsed.Host + pathBeforeRef
			}
		}
		return req
	}
	if name := leadingRequirementName.FindString(req); name != "" {
		return name
	}
	return req
}

// requirementHasEggTag reports whether a url-based requirement carries an `#egg=`
// fragment, mirroring get_egg_tag. Truss refuses to emit a REMOVE for a
// url-based requirement without one (it can't be uninstalled by name), so we
// skip those removals too.
func requirementHasEggTag(req string) bool {
	if !isURLBasedRequirement(req) {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(req))
	if err != nil {
		return false
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		return false
	}
	return len(fragment["egg"]) > 0
}
