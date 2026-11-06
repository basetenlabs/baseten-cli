package cmd

var commandAuth = Command{
	Name:    "auth",
	Summary: "Manage authentication",
	Description: "Log in, log out, and manage Baseten credentials.\n\n" +
		"Credentials are stored in the system keyring when available, " +
		"with a plaintext fallback in the config directory.",
	Children: []Command{
		{
			Name:    "login",
			Summary: "Authenticate with Baseten",
			Description: "Log in to Baseten via browser (OAuth device flow) or API key, storing a " +
				"named profile.\n\n" +
				"By default, opens a browser for interactive login. Use --web to skip prompts " +
				"(suitable for non-TTY environments). Use --with-api-key to provide an API key " +
				"(reads from stdin, or prompts interactively if TTY).\n\n" +
				"Browser logins name the profile after your email; API key logins require an " +
				"explicit --profile name. The new profile becomes current unless --no-switch is given or " +
				"explicit profile selection is required (see `baseten auth config update`).",
			Flags: AuthLoginFlags{},
			Output: &CommandOutput[AuthLoginResult]{
				TextDescription: "Prints \"Logged in as <email> (<workspace>) as profile <profile>\" to stdout on success.",
				Examples: []CommandExample{
					{
						Description: "Browser-based login (OAuth device flow).",
						Command:     "baseten auth login --web",
					},
					{
						Description: "Provide an API key on stdin under a named profile.",
						Command:     "echo $API_KEY | baseten auth login --with-api-key --profile <profile>",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the new profile name.",
					Command:     "baseten auth login --web --jq '.profile'",
				},
			},
		},
		{
			Name:    "logout",
			Summary: "Remove a stored profile",
			Description: "Remove a stored profile and its credentials. Defaults to the current profile; pass --profile to choose another. " +
				"When explicit profile selection is required, prompts for a profile on a terminal. For OAuth credentials, also revokes the session.",
			Flags: AuthLogoutFlags{},
			Output: &CommandOutput[AuthLogoutResult]{
				TextDescription: "Prints \"Logged out <profile>\" to stdout on success.",
				Examples: []CommandExample{
					{
						Description: "Log out the current profile.",
						Command:     "baseten auth logout",
					},
					{
						Description: "Log out a specific profile.",
						Command:     "baseten auth logout --profile <profile>",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the logged-out profile name.",
					Command:     "baseten auth logout --jq '.profile'",
				},
			},
		},
		{
			Name:    "switch",
			Summary: "Switch the current profile",
			Description: "Set the current profile used when no profile is selected via --profile or BASETEN_PROFILE. " +
				"Not available when explicit profile selection is required.",
			Flags: AuthSwitchFlags{},
			Output: &CommandOutput[AuthSwitchResult]{
				TextDescription: "Prints \"Switched to <profile>\" to stdout on success.",
				Examples: []CommandExample{
					{
						Description: "Switch to a specific profile non-interactively.",
						Command:     "baseten auth switch --profile <profile>",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the new current profile.",
					Command:     "baseten auth switch --profile <profile> --jq '.profile'",
				},
			},
		},
		{
			Name:        "status",
			Summary:     "Show authentication status",
			Description: "Show the resolved authentication state, including the profile, remote, and auth type.",
			Flags:       AuthStatusFlags{},
			Output: &CommandOutput[AuthStatusResult]{
				TextDescription: "Summary of the resolved profile: profile name, remote URL, and auth type.",
				Examples: []CommandExample{
					{
						Description: "Show the current auth status.",
						Command:     "baseten auth status",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the auth type.",
					Command:     "baseten auth status --jq '.auth_type'",
				},
			},
		},
		{
			Name:        "list",
			Summary:     "List stored profiles",
			Description: "List stored profiles, marking the one currently in use.",
			Flags:       AuthListFlags{},
			Output: &CommandOutput[AuthListResult]{
				TextDescription: "Table with columns: current marker (*), PROFILE, REMOTE (only when a profile uses " +
					"a remote other than https://app.baseten.co), AUTH TYPE. When no profiles exist, prints " +
					"\"No profiles found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List all stored profiles.",
						Command:     "baseten auth list",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the profile names.",
					Command:     "baseten auth list --jq '.profiles[].profile'",
				},
			},
		},
		{
			Name:    "config",
			Summary: "Manage settings that apply across profiles",
			Children: []Command{
				{
					Name:        "describe",
					Summary:     "Show auth settings",
					Description: "Show settings that apply across all profiles.",
					Flags:       AuthConfigDescribeFlags{},
					Output: &CommandOutput[AuthConfigResult]{
						TextDescription: "Each setting and its value.",
						Examples: []CommandExample{
							{
								Description: "Show auth settings.",
								Command:     "baseten auth config describe",
							},
						},
						JQExample: CommandExample{
							Description: "Print whether explicit profile selection is required.",
							Command:     "baseten auth config describe --jq '.require_explicit_profile'",
						},
					},
				},
				{
					Name:    "update",
					Summary: "Update auth settings",
					Description: "Update settings that apply across all profiles.\n\n" +
						"With --require-explicit-profile, there is no current profile: every command must select " +
						"a profile via --profile, BASETEN_PROFILE, or BASETEN_API_KEY. Otherwise it prompts for " +
						"one on a terminal and fails when not on a terminal. Enabling clears the current profile.",
					Flags: AuthConfigUpdateFlags{},
					Output: &CommandOutput[AuthConfigResult]{
						TextDescription: "Each setting and its updated value.",
						Examples: []CommandExample{
							{
								Description: "Require an explicit profile on every command.",
								Command:     "baseten auth config update --require-explicit-profile",
							},
							{
								Description: "Go back to using the current profile.",
								Command:     "baseten auth config update --require-explicit-profile=false",
							},
						},
						JQExample: CommandExample{
							Description: "Print whether explicit profile selection is required.",
							Command:     "baseten auth config update --require-explicit-profile --jq '.require_explicit_profile'",
						},
					},
				},
			},
		},
	},
}

// AuthConfigResult is the JSON output of `baseten auth config describe` and
// `baseten auth config update`.
type AuthConfigResult struct {
	RequireExplicitProfile bool `json:"require_explicit_profile"`
}

// AuthLoginResult is the JSON output of `baseten auth login`.
type AuthLoginResult struct {
	Profile       string `json:"profile"`
	UserID        string `json:"user_id"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	WorkspaceName string `json:"workspace_name"`
}

// AuthLogoutResult is the JSON output of `baseten auth logout`.
type AuthLogoutResult struct {
	Profile string `json:"profile"`
}

// AuthSwitchResult is the JSON output of `baseten auth switch`.
type AuthSwitchResult struct {
	Profile string `json:"profile"`
}

// AuthStatusResult is the JSON output of `baseten auth status`.
type AuthStatusResult struct {
	Profile                string `json:"profile"`
	RemoteURL              string `json:"remote_url"`
	AuthType               string `json:"auth_type"`
	RequireExplicitProfile bool   `json:"require_explicit_profile"`
}

// AuthListResult is the JSON output of `baseten auth list`.
type AuthListResult struct {
	Profiles []AuthListProfile `json:"profiles"`
}

// AuthListProfile is a single stored profile in `baseten auth list` output.
type AuthListProfile struct {
	Profile   string `json:"profile"`
	RemoteURL string `json:"remote_url"`
	AuthType  string `json:"auth_type"`
	Current   bool   `json:"current"`
}

// AuthLoginFlags are the flags for baseten auth login.
type AuthLoginFlags struct {
	CommandFlags
	Web             bool   `flag:"web" desc:"Use browser login without interactive prompts"`
	WithAPIKey      bool   `flag:"with-api-key" desc:"Read API key from stdin"`
	RemoteURL       string `flag:"remote-url" desc:"Baseten remote URL for this profile (default https://app.baseten.co)"`
	NoSwitch        bool   `flag:"no-switch" desc:"Store the profile without making it the current profile"`
	InsecureStorage bool   `flag:"insecure-storage" desc:"Store credentials in plain text instead of system keyring"`
}

// AuthLogoutFlags are the flags for baseten auth logout.
type AuthLogoutFlags struct {
	CommandFlags
}

// AuthSwitchFlags are the flags for baseten auth switch.
type AuthSwitchFlags struct {
	CommandFlags
}

// AuthStatusFlags are the flags for baseten auth status.
type AuthStatusFlags struct {
	CommandFlags
}

// AuthListFlags are the flags for baseten auth list.
type AuthListFlags struct {
	CommandFlags
}

// AuthConfigDescribeFlags are the flags for baseten auth config describe.
type AuthConfigDescribeFlags struct {
	CommandFlags
}

// AuthConfigUpdateFlags are the flags for baseten auth config update.
type AuthConfigUpdateFlags struct {
	CommandFlags
	RequireExplicitProfile bool `flag:"require-explicit-profile" desc:"Require every command to select a profile instead of using the current profile"`
}
