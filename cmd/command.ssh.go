package cmd

// commandSSH groups the `baseten ssh` subcommands. setup is the only
// user-facing command; sign and proxy are hidden and invoked by the SSH config
// that setup writes. The commands serve both model deployments and training
// jobs.
var commandSSH = Command{
	Name:    "ssh",
	Summary: "Manage SSH access to Baseten workloads",
	Description: "Configure SSH access to running Baseten workloads.\n\n" +
		"Run 'baseten ssh setup' once, then connect to any running workload with:\n\n" +
		"    ssh model-<model-id>-<deployment-id>.ssh.baseten.co\n" +
		"    ssh training-job-<job-id>-<node>.ssh.baseten.co\n\n" +
		"SSH access requires the workload to be running with SSH enabled.",
	Children: []Command{
		{
			Name:    "setup",
			Summary: "Configure SSH access for Baseten workloads",
			Description: "One-time setup: generate an SSH keypair and add a managed block to " +
				"~/.ssh/config that routes *.ssh.baseten.co connections through this CLI.\n\n" +
				"After running this once, connect to a running workload with:\n\n" +
				"    ssh model-<model-id>-<deployment-id>.ssh.baseten.co\n" +
				"    ssh training-job-<job-id>-<node>.ssh.baseten.co\n\n" +
				"The connection is authenticated with the profile selected at setup time " +
				"(--profile or the current profile). Re-run to refresh the keypair and config " +
				"block. Setup fails if ~/.ssh/config already configures these hosts outside the " +
				"managed block (for example from 'truss ssh setup').",
			Flags: SSHSetupFlags{},
			Output: &CommandOutput[SSHSetupResult]{
				TextDescription: "Prints the keypair path and pinned profile to stderr; no stdout output.",
				JSONDescription: "On success, stdout is a JSON object with the keypair path, whether an " +
					"existing key was reused, and the pinned profile.",
				Examples: []CommandExample{
					{
						Description: "Configure SSH access using the current profile.",
						Command:     "baseten ssh setup",
					},
					{
						Description: "Configure SSH access pinned to a specific profile.",
						Command:     "baseten ssh setup --profile prod",
					},
				},
				JQExample: CommandExample{
					Description: "Print the generated keypair path.",
					Command:     "baseten ssh setup --jq '.key_path'",
				},
			},
		},
		{
			Name:      "sign",
			Hidden:    true,
			ArgsUsage: "<hostname>",
			ExactArgs: 1,
			Summary:   "Sign an SSH certificate for a workload (internal)",
			Description: "Internal command invoked by the SSH config 'Match exec' step. Signs a " +
				"short-lived certificate for the workload named by the hostname, writes it next " +
				"to the keypair, and caches the proxy authorization for the proxy step.",
			Flags: SSHSignFlags{},
			Output: &CommandOutput[JSONUndefined]{
				JSONOutputUnimportant: true,
				TextDescription:       "No output on success; the signed certificate and proxy authorization are written to disk.",
				JSONDescription:       "Emits an empty JSON object on success; the certificate and proxy authorization are written to disk, not stdout.",
				Examples: []CommandExample{
					{
						Description: "Sign a certificate for a model deployment (run by ssh, not directly).",
						Command:     "baseten ssh sign model-<model-id>-<deployment-id>.ssh.baseten.co",
					},
				},
			},
		},
		{
			Name:      "proxy",
			Hidden:    true,
			ArgsUsage: "<hostname>",
			ExactArgs: 1,
			Summary:   "Relay a connection to a workload through the SSH proxy (internal)",
			Description: "Internal command used as the SSH config 'ProxyCommand'. Connects to the " +
				"SSH proxy using the authorization cached by the sign step and relays the " +
				"connection between stdin and stdout.",
			Flags: SSHProxyFlags{},
			Output: &CommandOutput[JSONUndefined]{
				JSONOutputUnimportant: true,
				TextDescription:       "Relays raw bytes between stdin/stdout and the SSH proxy; produces no other output.",
				Examples: []CommandExample{
					{
						Description: "Relay a connection (run by ssh as ProxyCommand, not directly).",
						Command:     "baseten ssh proxy model-<model-id>-<deployment-id>.ssh.baseten.co",
					},
				},
			},
		},
	},
}

// SSHSetupFlags configures `baseten ssh setup`.
type SSHSetupFlags struct {
	CommandFlags
}

// SSHSignFlags configures `baseten ssh sign`. The workload hostname is a
// positional argument.
type SSHSignFlags struct {
	CommandFlags

	DefaultProfile string `flag:"default-profile" desc:"Profile to use when none is selected via --profile or BASETEN_PROFILE. Baked into the SSH config by setup."`
}

// SSHProxyFlags configures `baseten ssh proxy`. The workload hostname is a
// positional argument.
type SSHProxyFlags struct {
	CommandFlags

	DefaultProfile string `flag:"default-profile" desc:"Profile to use when none is selected via --profile or BASETEN_PROFILE. Baked into the SSH config by setup."`
}

// SSHSetupResult is the JSON output of `baseten ssh setup`.
type SSHSetupResult struct {
	KeyPath   string `json:"key_path"`
	KeyReused bool   `json:"key_reused"`
	Profile   string `json:"profile,omitempty"`
}
