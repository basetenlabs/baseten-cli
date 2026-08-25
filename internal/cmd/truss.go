package cmd

import (
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
)

const (
	// trussAuthRemoteURLEnv and trussAuthAPIKeyEnv are the variables truss
	// reads to define a remote outright, bypassing its trussrc and keyring.
	// Together they are how the CLI hands its own credential to truss.
	trussAuthRemoteURLEnv = "BASETEN_TRUSS_AUTH_REMOTE_URL"
	trussAuthAPIKeyEnv    = "BASETEN_TRUSS_AUTH_API_KEY"

	// trussVersionEnv and trussExecutableEnv default --truss-version and
	// --truss-executable, so a chosen truss survives across invocations.
	trussVersionEnv    = "BASETEN_TRUSS_VERSION"
	trussExecutableEnv = "BASETEN_TRUSS_EXECUTABLE"

	// trussDefaultVersion is the version spec handed to uv when the caller
	// names none.
	trussDefaultVersion = "latest"
)

// trussInvocation describes one run of the truss CLI.
type trussInvocation struct {
	// Flags selects which truss to run.
	Flags cmd.TrussFlags
	// Args are the arguments passed to truss.
	Args []string
	// ForwardAuth hands the CLI's resolved credential to truss over the
	// environment. Set by commands that reach the Baseten API through truss;
	// left false by commands that only run truss locally.
	ForwardAuth bool
	// Env holds extra "KEY=VALUE" entries for the child environment.
	Env []string
	// JSONResult reserves stdout for the command's own JSON result: under JSON
	// output truss's output is routed to stderr and an empty result object is
	// printed once truss succeeds. Truss reports these results as prose only,
	// so there is nothing structured to pass on yet. Left false by
	// `baseten truss`, whose stdout is truss's output verbatim.
	JSONResult bool
}

// trussCommand builds the command for a truss invocation, with the caller's
// stdio wired and the child environment prepared. Callers may retarget the
// stdio before running it, which `baseten model image` does to keep its own
// stdout clean.
//
// Truss is run by `uv tool run` unless an executable is named, since a version
// spec resolved by uv is reproducible and recent enough to honor the forwarded
// credential, whereas whatever is on PATH is neither.
func trussCommand(ctx *CommandContext, inv trussInvocation) (*exec.Cmd, error) {
	// Truss's own auth variables are the channel this CLI forwards over, so a
	// value already in the environment would either be overwritten or, worse,
	// silently steer truss at a different workspace. The CLI's own variables
	// express the same intent and are forwarded from there.
	for _, name := range []string{trussAuthRemoteURLEnv, trussAuthAPIKeyEnv} {
		if os.Getenv(name) != "" {
			return nil, cmd.NewErrUsagef(
				"%s is set, which would point truss at a remote this CLI knows nothing about; "+
					"unset it and use BASETEN_API_KEY, plus BASETEN_REMOTE_URL for a non-default remote, "+
					"which are forwarded to truss for you", name)
		}
	}

	// Either flag suppresses both environment defaults, so a stored
	// BASETEN_TRUSS_VERSION does not collide with an explicit
	// --truss-executable and turn a valid invocation into a usage error.
	version, executable := inv.Flags.TrussVersion, inv.Flags.TrussExecutable
	if version == "" && executable == "" {
		version, executable = os.Getenv(trussVersionEnv), os.Getenv(trussExecutableEnv)
	}
	if version != "" && executable != "" {
		return nil, cmd.NewErrUsagef(
			"--truss-version and --truss-executable are mutually exclusive; a version selects a truss "+
				"for uv to fetch, an executable runs one you already have (values can also come from %s and %s)",
			trussVersionEnv, trussExecutableEnv)
	}

	var c *exec.Cmd
	if executable != "" {
		// LookPath resolves a bare name against PATH and, on Windows, applies
		// PATHEXT so a path without the .exe suffix still resolves.
		path, err := ctx.Execer().LookPath(executable)
		if err != nil {
			return nil, cmd.NewErrUsagef("truss executable %q not found: %v", executable, err)
		}
		c = exec.CommandContext(ctx, path, inv.Args...)
	} else {
		if _, err := ctx.Execer().LookPath("uv"); err != nil {
			return nil, cmd.NewErrUsagef("uv not found on PATH, required to run truss; install from " +
				"https://docs.astral.sh/uv/, or point --truss-executable at a truss you installed")
		}
		if version == "" {
			version = trussDefaultVersion
		}
		c = exec.CommandContext(ctx, "uv", append([]string{"tool", "run", "truss@" + version}, inv.Args...)...)
	}

	// TRUSS_NO_UPDATE_CHECK suppresses truss's update check and its associated
	// disk/network side effects.
	env := append(os.Environ(), "TRUSS_NO_UPDATE_CHECK=1")
	env = append(env, inv.Env...)
	if inv.ForwardAuth {
		transport, remote, err := ctx.AuthTransport()
		if err != nil {
			return nil, err
		}
		// Forwarded over the environment rather than written to a trussrc, and
		// as a bearer token so the same variable carries an API key or an OAuth
		// access token. The child cannot refresh an access token, so delegated
		// invocations are kept short rather than tailing through truss.
		token, err := transport.Credential(ctx)
		if err != nil {
			return nil, err
		}
		env = append(env,
			trussAuthRemoteURLEnv+"="+remote.RemoteURL(),
			trussAuthAPIKeyEnv+"="+token,
		)
	}
	c.Env = env

	c.Stdin = ctx.Stdin
	c.Stdout = ctx.Stdout
	c.Stderr = ctx.Stderr
	return c, nil
}

// trussRun builds and runs a truss invocation, propagating truss's exit code
// as an [ErrSubprocess]. The command line is logged only in verbose mode, since
// forwarded arguments can carry values a user would not want echoed.
func trussRun(ctx *CommandContext, inv trussInvocation) error {
	c, err := trussCommand(ctx, inv)
	if err != nil {
		return err
	}
	if inv.JSONResult && ctx.JSON {
		c.Stdout = ctx.Stderr
	}
	ctx.VerboseLogf("+ %s\n", strings.Join(c.Args, " "))
	if err := ctx.Execer().Exec(c); err != nil {
		return err
	}
	if inv.JSONResult && ctx.JSON {
		ctx.OutputJSON(cmd.TrussDelegatedResult{})
	}
	return nil
}

// trussArg appends "--name value" to args when value is non-empty, the shape
// every delegating command uses to forward an optional flag.
func trussArg(args []string, name, value string) []string {
	if value == "" {
		return args
	}
	return append(args, "--"+name, value)
}

// trussBoolArg appends "--name" to args when set.
func trussBoolArg(args []string, name string, set bool) []string {
	if !set {
		return args
	}
	return append(args, "--"+name)
}

// trussIntArg appends "--name value" when value is non-zero. Every integer flag
// forwarded to truss is a count, timeout, or priority for which zero is not a
// meaningful request, so zero means "not given" and truss applies its default.
func trussIntArg(args []string, name string, value int) []string {
	if value == 0 {
		return args
	}
	return append(args, "--"+name, strconv.Itoa(value))
}
