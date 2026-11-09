package cmd

import "time"

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
			Name:    "status",
			Summary: "Show authentication status",
			Description: "Show the resolved authentication state: which credential source is active " +
				"(a stored profile, BASETEN_API_KEY, or workload identity from BASETEN_IDENTITY_TOKEN_FILE " +
				"and BASETEN_TRUST_RULE_ID), the remote, and the auth type.\n\n" +
				"Credentials are selected in this order: --profile, BASETEN_API_KEY, workload identity, " +
				"BASETEN_PROFILE, then the current profile. Does not contact Baseten.",
			Flags: AuthStatusFlags{},
			Output: &CommandOutput[AuthStatusResult]{
				TextDescription: "Summary of the active credential source: the profile name, BASETEN_API_KEY, or " +
					"workload identity with its trust rule ID and identity token file; then the remote URL and, " +
					"for a profile, the auth type. Notes when BASETEN_API_KEY shadows workload identity variables.",
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
			Name:    "token",
			Summary: "Print an access token",
			Description: "Print a current Baseten access token for the resolved credential, for scripts that " +
				"call the Baseten API directly with \"Authorization: Bearer <token>\".\n\n" +
				"With workload identity (BASETEN_IDENTITY_TOKEN_FILE and BASETEN_TRUST_RULE_ID), exchanges the " +
				"identity token for a short-lived access token. With an OAuth profile, prints its access token, " +
				"refreshed first if it has expired. API keys are not access tokens, so an API key credential " +
				"is an error; use the key itself.\n\n" +
				"Access tokens expire; request a new one rather than storing it.",
			Flags: AuthTokenFlags{},
			Output: &CommandOutput[AuthTokenResult]{
				TextDescription: "Prints the access token alone on one line to stdout.",
				Examples: []CommandExample{
					{
						Description: "Call the management API with a fresh access token.",
						Command:     `curl -H "Authorization: Bearer $(baseten auth token)" https://api.baseten.co/v1/models`,
					},
				},
				JQExample: CommandExample{
					Description: "Print when the access token expires.",
					Command:     "baseten auth token --jq '.expires_at'",
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

// Credential sources reported by `baseten auth status`.
const (
	AuthSourceProfile          = "profile"
	AuthSourceAPIKeyEnv        = "api_key_env"
	AuthSourceWorkloadIdentity = "workload_identity"
)

// AuthStatusResult is the JSON output of `baseten auth status`.
type AuthStatusResult struct {
	Profile   string `json:"profile"`
	RemoteURL string `json:"remote_url"`
	AuthType  string `json:"auth_type"`
	// Source is the active credential source: "profile", "api_key_env", or
	// "workload_identity".
	Source string `json:"source"`
	// TrustRuleID and IdentityTokenFile are set for workload identity.
	TrustRuleID       string `json:"trust_rule_id,omitempty"`
	IdentityTokenFile string `json:"identity_token_file,omitempty"`
	// WorkloadIdentityShadowed is true when workload identity variables are set
	// but BASETEN_API_KEY takes precedence over them.
	WorkloadIdentityShadowed bool `json:"workload_identity_shadowed,omitempty"`
}

// AuthTokenResult is the JSON output of `baseten auth token`.
type AuthTokenResult struct {
	Token string `json:"token"`
	// ExpiresAt is omitted when the token's expiry is unknown.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
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

// AuthTokenFlags are the flags for baseten auth token.
type AuthTokenFlags struct {
	CommandFlags
}

// AuthListFlags are the flags for baseten auth list.
type AuthListFlags struct {
	CommandFlags
}
