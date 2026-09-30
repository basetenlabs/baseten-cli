package ssh

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	markerStart = "# --- baseten-cli-ssh ---"
	markerEnd   = "# --- end baseten-cli-ssh ---"
)

// WriteConfig installs or refreshes the managed block in the SSH config. The
// generated config invokes `baseten ssh sign|proxy` (assuming baseten is on the
// PATH). When profile is non-empty it is passed as --default-profile so
// connections fall back to that profile unless --profile or BASETEN_PROFILE
// overrides it.
//
// If the managed markers are present the block between them is replaced;
// otherwise it is appended. Either way, if any Host/Match line outside the
// managed block already targets *.ssh.baseten.co, the file is left untouched
// and an error is returned so we never clobber config another tool installed.
func WriteConfig(keyPath, profile string) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	existing := ""
	if b, err := os.ReadFile(path); err == nil {
		existing = string(b)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	block := buildBlock(keyPath, profile)

	// Locate any existing managed block so we replace it rather than appending
	// a duplicate on re-run.
	start := strings.Index(existing, markerStart)
	end := strings.Index(existing, markerEnd)
	var newContent string
	if start != -1 && end != -1 && end > start {
		after := end + len(markerEnd)
		// Consume one trailing newline so the block (which ends in "\n") does
		// not accumulate blank lines across re-runs.
		if strings.HasPrefix(existing[after:], "\r\n") {
			after += 2
		} else if strings.HasPrefix(existing[after:], "\n") {
			after++
		}
		if referencesProxyHosts(existing[:start] + existing[after:]) {
			return foreignConfigError(path)
		}
		newContent = existing[:start] + block + existing[after:]
	} else {
		if referencesProxyHosts(existing) {
			return foreignConfigError(path)
		}
		sep := ""
		if existing != "" && !strings.HasSuffix(existing, "\n") {
			sep = "\n"
		}
		newContent = existing + sep + block
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating ssh config directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// referencesProxyHosts reports whether any Host or Match directive in text
// targets the *.ssh.baseten.co proxy hosts, indicating another tool (e.g.
// truss) has already configured SSH for these workloads.
func referencesProxyHosts(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "host", "match":
			if strings.Contains(strings.ToLower(line), "ssh.baseten.co") {
				return true
			}
		}
	}
	return false
}

func foreignConfigError(path string) error {
	return fmt.Errorf(
		"%s already configures *.ssh.baseten.co hosts outside the managed block; refusing to modify it. "+
			"Remove the existing Baseten SSH entries (for example from `truss ssh setup`) and re-run",
		path)
}

// SafeProfileName reports whether name can be embedded as a bare token in the
// shell command lines of the SSH config without quoting. Allowlisting a
// conservative character set (rather than escaping) eliminates any shell
// injection surface and avoids POSIX-vs-cmd.exe quoting differences entirely:
// every allowed character is a safe bare token in any shell. Names that fail
// this are not pinned into the config.
func SafeProfileName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("@._:+-", r):
		default:
			return false
		}
	}
	return true
}

// buildBlock renders the managed config block. Training and inference share the
// same proxy mechanics but connect as different users, so each gets its own
// Match entry; both route through the same sign/proxy subcommands.
func buildBlock(keyPath, profile string) string {
	// BASETEN_SSH_BINARY_OVERRIDE lets e2e tests point the generated config at a
	// freshly built binary instead of relying on `baseten` being on PATH. Unset
	// in normal use, where connections invoke `baseten` resolved from PATH.
	bin := "baseten"
	if override := os.Getenv("BASETEN_SSH_BINARY_OVERRIDE"); override != "" {
		bin = override
	}
	sign := bin + " ssh sign"
	proxy := bin + " ssh proxy"
	if profile != "" {
		// Equals form so a value with a leading '-' is not misread as a flag.
		sign += " --default-profile=" + profile
		proxy += " --default-profile=" + profile
	}
	cert := keyPath + "-cert.pub"

	match := func(pattern, user string) []string {
		return []string{
			fmt.Sprintf("Match host %s exec \"%s %%n\"", pattern, sign),
			fmt.Sprintf("    ProxyCommand %s %%n", proxy),
			"    User " + user,
			"    IdentityFile " + keyPath,
			"    CertificateFile " + cert,
			"    StrictHostKeyChecking no",
			"    UserKnownHostsFile /dev/null",
			"",
		}
	}

	lines := []string{markerStart}
	lines = append(lines, match("training-job-*.ssh.baseten.co", "baseten")...)
	lines = append(lines, match("model-*.ssh.baseten.co", "app")...)
	// Env form <env>.model-<id>.ssh.baseten.co: the environment name is a
	// leading label, so it needs its own pattern (disjoint from model-*, which
	// only matches hostnames that start with model-).
	lines = append(lines, match("*.model-*.ssh.baseten.co", "app")...)
	lines = append(lines, markerEnd, "")
	return strings.Join(lines, "\n")
}
