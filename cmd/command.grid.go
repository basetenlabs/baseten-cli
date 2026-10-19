package cmd

import (
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

var commandGrid = Command{
	Name:        "grid",
	Summary:     "Manage Baseten Grid (PRE-RELEASE)",
	Description: "Configure harness integrations and manage routes with Baseten Grid. These commands are pre-release; their arguments, flags, and output may change.",
	Children: []Command{
		{
			Name:    "setup",
			Summary: "Set up harness authentication and configuration (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Configure installed harnesses with your team's routes and a routes API key.\n\n" +
				"Setup offers a picker of installed harnesses when --harness is omitted. Repeat --harness to select several. " +
				"The team's routes with model metadata are added to each harness's model picker, replacing it. Claude Code lists the routes that serve " +
				"the Anthropic Messages API, and Codex lists the routes that serve the OpenAI Responses API. The first listed route is the primary route unless --primary-route is set.\n\n" +
				"Setup overwrites the harness's integration settings without saving their previous values. Running it again refreshes them. " +
				"The routes API key is created on first setup and reused afterward. With --use-session-credential, the session's API key is installed instead and never created or deleted by this CLI. " +
				"Setup records its team and options so 'grid sync' can replay them. Restart the harness after setup. " +
				"For Codex, setup also signs out of OpenAI/ChatGPT and disables ChatGPT login while the Baseten harness is configured, and restarts Codex's background server if one is running, asking first when Codex sessions are attached to it. " +
				"For OpenCode, setup writes the original settings format, which OpenCode 1 and 2 both read, unless the file already has OpenCode 2's native settings, in which case it writes those.",
			Flags: HarnessSetupFlags{},
			Output: &CommandOutput[HarnessPlanList]{
				TextDescription: "The created key and follow-up commands. Use --dry-run or --verbose for the configuration of each harness, and --verbose for setting names.",
				Examples: []CommandExample{
					{
						Description: "Configure installed harnesses interactively.",
						Command:     "baseten grid setup",
					},
					{
						Description: "Install a pre-minted key from the environment instead of creating one.",
						Command:     "baseten grid setup --harness claude-code --use-session-credential --yes",
					},
					{
						Description: "Preview configuration for one harness.",
						Command:     "baseten grid setup --harness claude-code --primary-route <route-name> --dry-run",
					},
					{
						Description: "Apply a configuration without prompting.",
						CommandLines: []string{
							"baseten grid setup --harness codex",
							"--team <team> --primary-route <route-name> --yes",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print the configuration file paths.",
					Command:     "baseten grid setup --harness claude-code --dry-run --jq '.items[].config'",
				},
			},
		},
		{
			Name:    "sync",
			Summary: "Refresh configured harnesses with their recorded setup (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Reapply the setup of every harness configured on this machine, using the team, location, and options recorded by its last 'grid setup'. " +
				"Sync takes no harness selection: to change what is configured, run 'grid setup' or 'grid teardown'. " +
				"Stored routes the team has removed fall back to their defaults with a warning. Only harnesses whose record matches the current profile are synced, " +
				"and a machine with no record needs 'grid setup' first. Harnesses set up with --use-session-credential re-resolve the session's API key; " +
				"when it is unavailable, sync errors instead of creating a replacement.\n\n" +
				"When nothing changes, sync does nothing and exits 0, so it is safe to run from a schedule; pass --yes to skip the prompt when changes are found. " +
				"For Codex, sync restarts the app-server daemon like setup does, asking first when sessions are attached unless --yes is passed; an in-progress turn is interrupted, " +
				"and thread history is preserved. A declined or failed restart is retried on the next sync.",
			Flags: HarnessSyncFlags{},
			Output: &CommandOutput[HarnessPlanList]{
				TextDescription: "The refreshed configuration of each harness, like setup. Use --dry-run to preview, and --verbose for per-harness details.",
				Examples: []CommandExample{
					{
						Description: "Refresh every configured harness, as a scheduled run does.",
						Command:     "baseten grid sync --yes",
					},
					{
						Description: "Preview what a sync would change.",
						Command:     "baseten grid sync --dry-run",
					},
				},
				JQExample: CommandExample{
					Description: "Print the configuration files a sync would change.",
					Command:     "baseten grid sync --dry-run --jq '.items[] | select(.changed) | .config'",
				},
			},
		},
		{
			Name:    "status",
			Summary: "Inspect local harness configuration (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Show the installed versions, configuration path, and configured routes of each harness. " +
				"A harness installed both as a CLI and as a desktop app lists both versions; they share one configuration file. " +
				"Without --harness, shows every harness configured at its default path. Status reads local files only, " +
				"so it works after the harness is uninstalled, but it doesn't check the key or live routes. Rerun setup to refresh routes.",
			Flags: HarnessStatusFlags{},
			Output: &CommandOutput[HarnessStatusList]{
				TextDescription: "Local configuration and configured routes. Use --verbose for setting names.",
				Examples: []CommandExample{
					{
						Description: "Inspect Codex configuration.",
						Command:     "baseten grid status --harness codex",
					},
				},
				JQExample: CommandExample{
					Description: "Print each harness's state.",
					Command:     "baseten grid status --jq '.items[] | {harness, state}'",
				},
			},
		},
		{
			Name:    "harness-defaults",
			Summary: "Show team default routes for harnesses (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Show the default routes for each harness. " +
				"Use --harness to narrow the output; without it, every harness is shown. " +
				"Defaults can be changed on app.baseten.co or by API by an admin; this command only reads them.",
			Flags: HarnessDefaultsFlags{},
			Output: &CommandOutput[HarnessDefaultsList]{
				TextDescription: "Table with columns: HARNESS, PRIMARY, BACKGROUND. " +
					"Each route is followed by its source in parentheses; '-' means no default is set. " +
					"JSON includes the team and each default's full route and source.",
				Examples: []CommandExample{
					{
						Description: "Show every harness's defaults for your only team.",
						Command:     "baseten grid harness-defaults",
					},
					{
						Description: "Show one team's defaults for one harness.",
						Command:     "baseten grid harness-defaults --team Engineering --harness claude-code",
					},
				},
				JQExample: CommandExample{
					Description: "Print the primary route for Claude Code.",
					Command:     "baseten grid harness-defaults --harness claude-code --jq '.items[0].primary.route.name'",
				},
			},
		},
		{
			Name:    "usage",
			Summary: "Show your routes spend in your teams, this month by default (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Show what you've spent on routes in each of your teams: your spend there against your limit there, a " +
				"chart of spend per day, and each model's spend. --team narrows it to one team. " +
				"The window is this UTC month so far, the period monthly spend limits apply to, unless " +
				"--start, --end, or --since narrows or moves it; the limit is only shown for this month so far.\n\n" +
				"This is the same spend that spend limits are checked against, and it can lag by up to 15 minutes. " +
				"Model API costs use your prices and include tool calls. OpenAI, Anthropic, and xAI costs estimate what " +
				"those providers charge and are not Baseten charges. Vertex and OpenAI-compatible usage isn't included.\n\n" +
				"For filters, token counts, or other users' usage, use 'baseten grid route usage'. For " +
				"machine-readable streaming, prefer --output jsonl over --output json.",
			// TODO: Revisit this description, and null-cost handling in
			// internal/cmd/command.harness_usage.go, as server-side usage changes:
			// which providers are included (Vertex and OpenAI-compatible aren't
			// yet), whether costs are always present, and the 15-minute lag.
			Flags: HarnessUsageFlags{},
			Output: &CommandOutput[managementapi.RoutesUsageBucket]{
				JSONArrayStreamed: true,
				TextDescription: "The month and when limits reset go to stderr. Then, for each team, a line with the team, what you spent " +
					"there this month and your limit there, with the team's default when yours differs from it, ending in " +
					"\"limit reached\" or \"team limit reached\" when your requests there are rejected; on a terminal, a " +
					"chart of spend per day per model from the first of the month; and a line per model with its spend and " +
					"share, most expensive first. A cost of \"-\" means some of that usage couldn't be priced. With no " +
					"teams, prints \"No usage this month.\" to stderr. For a window other than this month so far, the " +
					"month line is the window instead, and the team line is what you spent there in it, with no limit.",
				JSONDescription: "One record per UTC day, as returned by the API: its date and the per-team, per-model usage " +
					"in results, including days with no usage. cost_usd values are exact decimal strings.",
				Examples: []CommandExample{
					{
						Description: "Show your spend this month in each of your teams.",
						Command:     "baseten grid usage",
					},
					{
						Description: "Show your spend this month in one of your teams.",
						Command:     "baseten grid usage --team <team>",
					},
					{
						Description: "Show your spend over the last week.",
						Command:     "baseten grid usage --since 7d",
					},
				},
				JQExample: CommandExample{
					Description: "Stream each day's cost per team and model as a JSONL stream.",
					Command:     "baseten grid usage --output jsonl --jq '.results[] | {team_id, model, cost_usd}'",
				},
			},
		},
		{
			Name:    "teardown",
			Summary: "Remove Baseten harness settings (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Remove the Baseten integration settings so the harness's own defaults apply. Previous values are not restored, " +
				"and unrelated settings are kept. Without --harness, removes every integration configured at its default path.\n\n" +
				"Teardown also deletes the routes API key the removed harnesses use, once no other harness on this machine uses it, " +
				"except a key installed with --use-session-credential, whose lifecycle this CLI does not own. " +
				"It forgets the recorded setup so 'grid sync' no longer refreshes the removed harnesses. " +
				"It works after the harness is uninstalled. For Codex, teardown re-enables ChatGPT login and restarts Codex's background server if one is running, asking first when Codex sessions are attached to it. Run 'codex login' to sign back in.",
			Flags: HarnessTeardownFlags{},
			Output: &CommandOutput[HarnessPlanList]{
				TextDescription: "The settings removed from each harness. Use --verbose for configuration paths and setting names.",
				Examples: []CommandExample{
					{
						Description: "Preview removal.",
						Command:     "baseten grid teardown --harness codex --dry-run",
					},
					{
						Description: "Remove integration settings without prompting.",
						Command:     "baseten grid teardown --harness codex --yes",
					},
				},
				JQExample: CommandExample{
					Description: "List the settings teardown would remove.",
					Command:     "baseten grid teardown --dry-run --jq '.items[].settings'",
				},
			},
		},
		commandRoute,
	},
}
