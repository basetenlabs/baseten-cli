package cmd

// commandTruss forwards arbitrary arguments to the truss CLI.
var commandTruss = Command{
	Name:    "truss",
	Summary: "Run truss commands",
	Description: "Run a truss CLI command.\n\n" +
		"Every argument is forwarded to truss verbatim except the --truss-* flags below, " +
		"which this CLI consumes wherever they appear. Run 'baseten truss --help' for " +
		"truss's own help, since --help is forwarded like any other argument.\n\n" +
		"Truss is fetched and run by 'uv tool run', so 'uv' must be on PATH. A truss " +
		"command that imports your own Python code, such as 'chains push', needs " +
		"--truss-executable pointing at a truss installed alongside those dependencies.\n\n" +
		"Baseten credentials are forwarded to truss over environment variables, so it " +
		"uses the same profile as the rest of the CLI without reading your trussrc.",
	ArgsUsage:          "[args...]",
	DisableFlagParsing: true,
	Flags:              TrussPassthroughFlags{},
	Output: &CommandOutput[JSONUndefined]{
		JSONOutputUnimportant: true,
		TextDescription: "Whatever the truss CLI writes to stdout/stderr, passed through verbatim. " +
			"--output and --jq are not honored: every argument other than the --truss-* flags " +
			"is forwarded to truss. The exit code is propagated from truss.",
		Examples: []CommandExample{
			{
				Description: "Show truss help by passing --help to truss.",
				Command:     "baseten truss --help",
			},
			{
				Description: "Push a chain with the truss installed in the current virtualenv, which can import the chainlet's dependencies.",
				Command:     "baseten truss --truss-executable .venv/bin/truss chains push ./my_chain.py",
			},
			{
				Description: "Pin the truss version used for a push.",
				Command:     "baseten truss --truss-version 0.18.26 push --help",
			},
		},
	},
}

// TrussDelegatedResult is the JSON output of the commands that delegate to the
// truss CLI. It is empty: truss reports what it did as human-readable text, so
// there is nothing structured for the CLI to pass on. Fields land here once
// truss can report its results machine-readably.
type TrussDelegatedResult struct{}

// TrussPassthroughFlags configures `baseten truss`. It carries only the shared
// truss flags: everything else on the command line belongs to truss. Flag
// parsing is disabled for this command, so these are extracted from the raw
// arguments rather than by Cobra, and CommandFlags is deliberately absent since
// --output, --jq and friends are forwarded to truss like any other argument.
type TrussPassthroughFlags struct {
	TrussAuthFlags
}

// TrussFlags selects which truss the CLI runs. Embedded by every command that
// shells out to the truss CLI.
type TrussFlags struct {
	TrussVersion    string `flag:"truss-version" desc:"Version of truss to fetch and run with 'uv tool run', e.g. 0.18.26. Defaults to BASETEN_TRUSS_VERSION, or the latest release. Mutually exclusive with --truss-executable." group:"truss" group-pri:"300"`
	TrussExecutable string `flag:"truss-executable" desc:"Run this truss executable instead of fetching one with uv, e.g. a virtualenv's bin/truss. A value with no path separator is looked up on PATH, so '--truss-executable truss' runs the truss you installed. Defaults to BASETEN_TRUSS_EXECUTABLE." group:"truss" group-pri:"300"`
}

// TrussAuthFlags is [TrussFlags] plus control over credential forwarding.
// Embedded by delegating commands that call the Baseten API through truss;
// commands that only run truss locally embed [TrussFlags] instead.
type TrussAuthFlags struct {
	TrussFlags

	TrussNoForwardAuth bool `flag:"truss-no-forward-auth" desc:"Do not forward this CLI's credentials to truss, leaving it to resolve a remote from your trussrc. Old truss versions ignore forwarded credentials and use the trussrc regardless." group:"truss" group-pri:"300"`
}
