package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

const harnessPreRelease = "PRE-RELEASE: Harness commands are not GA yet and support only macOS and Linux for now. " +
	"Their arguments, flags, and output may change.\n\n"

// HarnessPlan is one configuration file that setup or teardown changes.
// Credentials are never included.
type HarnessPlan struct {
	Harness          string   `json:"harness"`
	Config           string   `json:"config"`
	Settings         []string `json:"settings"`
	ReplacedSettings []string `json:"replaced_settings,omitempty"`
	Managed          bool     `json:"managed"`
	Changed          bool     `json:"changed"`
}

// HarnessPlanList is the JSON output of `baseten grid setup` and `baseten grid teardown`.
type HarnessPlanList struct {
	Items []HarnessPlan `json:"items"`
	// DeletedAPIKeys lists the prefixes of routes API keys teardown deleted.
	DeletedAPIKeys []string `json:"deleted_api_keys,omitempty"`
}

// HarnessDefault is one harness's default routes.
type HarnessDefault struct {
	Harness    string                           `json:"harness"`
	Primary    *managementapi.RouteHarnessModel `json:"primary"`
	Background *managementapi.RouteHarnessModel `json:"background"`
}

// HarnessDefaultsList is the JSON output of `baseten grid harness-defaults`.
type HarnessDefaultsList struct {
	TeamId   string           `json:"team_id"`
	TeamName string           `json:"team_name"`
	Items    []HarnessDefault `json:"items"`
}

// HarnessRoute is a route configured in a harness.
type HarnessRoute struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// HarnessInstall is one copy of a harness found on this machine.
type HarnessInstall struct {
	// Kind is "CLI" or "Desktop".
	Kind    string `json:"kind"`
	Binary  string `json:"binary"`
	Version string `json:"version"`
}

// HarnessStatus is the local integration state of one harness.
type HarnessStatus struct {
	Harness   string `json:"harness"`
	Config    string `json:"config"`
	Installed bool   `json:"installed"`
	// Installs lists every copy of the harness found, the one on PATH first.
	// They all share the configuration file at Config.
	Installs        []HarnessInstall `json:"installs,omitempty"`
	State           string           `json:"state"`
	DefaultRoute    string           `json:"default_route,omitempty"`
	BackgroundRoute string           `json:"background_route,omitempty"`
	Routes          []HarnessRoute   `json:"routes,omitempty"`
	ManagedSettings []string         `json:"managed_settings,omitempty"`
}

// HarnessStatusList is the JSON output of `baseten grid status`.
type HarnessStatusList struct {
	Items []HarnessStatus `json:"items"`
}

// HarnessUsageFlags are the flags for `baseten grid usage`.
type HarnessUsageFlags struct {
	CommandFlags

	Team string `flag:"team" desc:"ID of the team to show. Without it, your only team; required when you belong to several."`

	Start time.Time     `flag:"start" desc:"Start of the range, inclusive, snapped down to its UTC day. ISO 8601, local when no timezone is given. Defaults to the start of the current UTC month."`
	End   time.Time     `flag:"end" desc:"End of the range, exclusive, rounded up to the end of its UTC day. ISO 8601, local when no timezone is given. Defaults to now."`
	Since time.Duration `flag:"since" desc:"Window from a relative time ago until now (e.g. '7d'). Mutually exclusive with --start and --end."`
}

// HarnessFlags selects the harnesses a command applies to.
type HarnessFlags struct {
	Harness   []string `flag:"harness" desc:"Harness to apply to. May be repeated." enum:"claude-code,codex,opencode,pi"`
	ConfigDir string   `flag:"config-dir" desc:"Harness configuration directory. Requires exactly one --harness. Defaults to the harness's own location."`
}

// HarnessStatusFlags are the flags for `baseten grid status`.
type HarnessStatusFlags struct {
	CommandFlags
	HarnessFlags
}

// HarnessSetupFlags are the flags for `baseten grid setup`.
type HarnessSetupFlags struct {
	CommandFlags
	HarnessFlags
	Team                 string `flag:"team" desc:"Team name or ID whose routes to use. Needed unless there is only one team. Run 'baseten org team list' to see teams."`
	KeyName              string `flag:"key-name" desc:"Name of the routes API key, if setup creates one. Defaults to baseten-harness-<hostname>-<random suffix>."`
	PrimaryRoute         string `flag:"primary-route" desc:"Primary route. Defaults to the team's default for the harness, else the first route the harness lists."`
	BackgroundRoute      string `flag:"background-route" desc:"Route for lightweight background tasks (Claude Code and OpenCode). Defaults to the team's default for the harness, else deepseek-ai/DeepSeek-V4.1-Flash."`
	SubagentRoute        string `flag:"subagent-route" desc:"Route for subagents (Claude Code and OpenCode). Defaults to the harness's own setting."`
	FallbackRoute        string `flag:"fallback-route" desc:"Route to fall back to when the default is unavailable (Claude Code). Defaults to the primary route."`
	UseSessionCredential bool   `flag:"use-session-credential" desc:"Install the session's API key (BASETEN_API_KEY or an API-key profile) as the harness credential instead of creating a routes API key. The key is never created, rotated, or deleted by this CLI. Mutually exclusive with --key-name."`
	DryRun               bool   `flag:"dry-run" desc:"Preview the configuration without changing files or creating an API key."`
	Yes                  bool   `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// HarnessDefaultsFlags are the flags for `baseten grid harness-defaults`.
type HarnessDefaultsFlags struct {
	CommandFlags
	Team    string   `flag:"team" desc:"Team name or ID whose defaults to show. Defaults to your only team; required if you belong to more than one. Run 'baseten org team list' to see teams."`
	Harness []string `flag:"harness" desc:"Harness to show. May be repeated. Defaults to every harness." enum:"claude-code,codex,opencode,pi"`
}

// HarnessSyncFlags are the flags for `baseten grid sync`.
type HarnessSyncFlags struct {
	CommandFlags
	DryRun bool `flag:"dry-run" desc:"Preview the refresh without changing files or creating an API key."`
	Yes    bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// HarnessTeardownFlags are the flags for `baseten grid teardown`.
type HarnessTeardownFlags struct {
	CommandFlags
	HarnessFlags
	DryRun bool `flag:"dry-run" desc:"Preview removal without changing files."`
	Yes    bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}
