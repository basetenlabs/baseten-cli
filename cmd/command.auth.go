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
			Description: "Log in to Baseten via browser (OAuth device flow) or API key.\n\n" +
				"By default, opens a browser for interactive login. Use --web to skip prompts " +
				"(suitable for non-TTY environments). Use --with-api-key to provide an API key " +
				"(reads from stdin, or prompts interactively if TTY).",
			Flags: AuthLoginFlags{},
			Output: &CommandOutput[AuthLoginResult]{
				TextDescription: "Prints \"Logged in as <email> (<workspace>)\" to stdout on success.",
				Examples: []CommandExample{
					{
						Description: "Browser-based login (OAuth device flow).",
						Command:     "baseten auth login --web",
					},
					{
						Description: "Provide an API key on stdin.",
						Command:     "echo $API_KEY | baseten auth login --with-api-key --label <label>",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the logged-in user's email.",
					Command:     "baseten auth login --web --jq '.email'",
				},
			},
		},
		{
			Name:        "logout",
			Summary:     "Remove stored credentials",
			Description: "Remove stored credentials for the active user. For OAuth credentials, also revokes the session.",
			Flags:       AuthLogoutFlags{},
			Output: &CommandOutput[AuthLogoutResult]{
				TextDescription: "Prints \"Logged out <user>\" to stdout on success.",
				Examples: []CommandExample{
					{
						Description: "Log out the active user.",
						Command:     "baseten auth logout",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the logged-out user label.",
					Command:     "baseten auth logout --jq '.user'",
				},
			},
		},
		{
			Name:        "switch",
			Summary:     "Switch active account",
			Description: "Switch the active account for the current host.",
			Flags:       AuthSwitchFlags{},
			Output: &CommandOutput[AuthSwitchResult]{
				TextDescription: "Prints \"Switched to <user>\" to stdout on success.",
				Examples: []CommandExample{
					{
						Description: "Switch to a specific account non-interactively.",
						Command:     "baseten auth switch --user <user>",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the newly active user.",
					Command:     "baseten auth switch --user <user> --jq '.user'",
				},
			},
		},
		{
			Name:        "status",
			Summary:     "Show authentication status",
			Description: "Show the current authentication state, including the active user and auth type.",
			Flags:       AuthStatusFlags{},
			Output: &CommandOutput[AuthStatusResult]{
				TextDescription: "Three-line summary: host URL, \"Logged in as <user>\", \"Auth type: <type>\".",
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
	UserID        string `json:"user_id"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	WorkspaceName string `json:"workspace_name"`
}

// AuthLogoutResult is the JSON output of `baseten auth logout`.
type AuthLogoutResult struct {
	User string `json:"user"`
}

// AuthSwitchResult is the JSON output of `baseten auth switch`.
type AuthSwitchResult struct {
	User string `json:"user"`
}

// AuthStatusResult is the JSON output of `baseten auth status`.
type AuthStatusResult struct {
	Host     string `json:"host"`
	User     string `json:"user"`
	AuthType string `json:"auth_type"`
}

// AuthLoginFlags are the flags for baseten auth login.
type AuthLoginFlags struct {
	CommandFlags
	Web             bool   `flag:"web" desc:"Use browser login without interactive prompts"`
	WithAPIKey      bool   `flag:"with-api-key" desc:"Read API key from stdin"`
	Label           string `flag:"label" desc:"Label for the API key credential"`
	InsecureStorage bool   `flag:"insecure-storage" desc:"Store credentials in plain text instead of system keyring"`
}

// AuthLogoutFlags are the flags for baseten auth logout.
type AuthLogoutFlags struct {
	CommandFlags
}

// AuthSwitchFlags are the flags for baseten auth switch.
type AuthSwitchFlags struct {
	CommandFlags
	User string `flag:"user" desc:"User to switch to"`
}

// AuthStatusFlags are the flags for baseten auth status.
type AuthStatusFlags struct {
	CommandFlags
}
