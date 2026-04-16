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
		},
		{
			Name:        "logout",
			Summary:     "Remove stored credentials",
			Description: "Remove stored credentials for the active user. For OAuth credentials, also revokes the session.",
			Flags:       AuthLogoutFlags{},
		},
		{
			Name:        "switch",
			Summary:     "Switch active account",
			Description: "Switch the active account for the current host.",
			Flags:       AuthSwitchFlags{},
		},
		{
			Name:        "status",
			Summary:     "Show authentication status",
			Description: "Show the current authentication state, including the active user and credential validity.",
			Flags:       AuthStatusFlags{},
		},
	},
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
