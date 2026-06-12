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
				"explicit --profile name. The new profile becomes current unless --no-switch is given.",
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
			Name:        "logout",
			Summary:     "Remove a stored profile",
			Description: "Remove a stored profile and its credentials. Defaults to the current profile; pass --profile to choose another. For OAuth credentials, also revokes the session.",
			Flags:       AuthLogoutFlags{},
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
			Name:        "switch",
			Summary:     "Switch the current profile",
			Description: "Set the current profile used when no profile is selected via --profile or BASETEN_PROFILE.",
			Flags:       AuthSwitchFlags{},
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
	},
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
	Profile   string `json:"profile"`
	RemoteURL string `json:"remote_url"`
	AuthType  string `json:"auth_type"`
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
