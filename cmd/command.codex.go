package cmd

var commandCodex = Command{
	Name:        "codex",
	Summary:     "Configure Codex for Baseten Model APIs",
	Description: "Configure the Codex CLI and desktop app to use Baseten Model APIs without Baseten Grid.",
	Children: []Command{
		{
			Name:    "setup",
			Summary: "Generate a Codex model catalog and add Baseten Model APIs to config.toml",
			Description: "Fetch the Model APIs available to BASETEN_API_KEY and write them as a Codex model catalog, " +
				"add the baseten-mapi provider to Codex's config.toml ($CODEX_HOME, default ~/.codex), and write a baseten profile that selects them.\n\n" +
				"Codex shows one provider's models at a time. Run 'codex --profile baseten --no-daemon' to use Baseten Model APIs from the CLI while plain 'codex' keeps its current provider; the profile needs --no-daemon because Codex's background server does not apply a CLI profile's provider. " +
				"The ChatGPT desktop app cannot select a profile: use --default, or 'baseten codex use', to make Baseten Model APIs the provider in config.toml.\n\n" +
				"The catalog is regenerated on every run; models under vllmtest/ and models with no supported features are left out. " +
				"config.toml only gains missing keys: existing values are never overwritten, except model_provider and model_catalog_json with --default, " +
				"and the OpenAI-Marketplace-Id header under [model_providers.baseten-mapi.http_headers], which is set from OPENAI_MARKETPLACE_ID when that is set. " +
				"Rewriting config.toml drops its comments. Setup never sets model or model_reasoning_effort in config.toml; the baseten profile sets model to a Baseten model, keeping the one picked in an earlier profile session while it is still in the catalog.\n\n" +
				"Codex reads BASETEN_API_KEY from the environment at runtime. Setup never writes the key to disk. " +
				"It coexists with 'baseten grid setup', which uses the baseten provider and its own catalog. Running setup again is safe.",
			Flags: CodexSetupFlags{},
			Output: &CommandOutput[CodexSetupResult]{
				TextDescription: "Summary of models found, included, and excluded, catalog changes, and config.toml keys added and preserved.",
				Examples: []CommandExample{
					{Description: "Configure Codex for Baseten Model APIs.", Command: "baseten codex setup"},
					{Description: "Show what setup would change without writing files.", Command: "baseten codex setup --dry-run"},
					{Description: "Also make Baseten Model APIs the default provider, for the ChatGPT desktop app.", Command: "baseten codex setup --default"},
				},
				JQExample: CommandExample{
					Description: "List the config.toml keys setup added.",
					Command:     "baseten codex setup --jq '.config.added'",
				},
			},
		},
		{
			Name:    "use",
			Summary: "Switch Codex's default provider between Baseten Model APIs and OpenAI",
			Description: "Set model_provider in Codex's config.toml, which the ChatGPT desktop app and plain 'codex' use. " +
				"baseten selects Baseten Model APIs (the baseten-mapi provider) and their catalog, as written by 'baseten codex setup'. " +
				"openai removes model_provider when it is baseten-mapi, so Codex falls back to OpenAI, and removes model_catalog_json when it points at the Baseten catalog; it fails if another provider is selected. " +
				"Restart the desktop app afterwards. Threads keep the provider they started with.",
			Flags: CodexUseFlags{},
			Output: &CommandOutput[CodexUseResult]{
				TextDescription: "One line naming the provider Codex now uses.",
				Examples: []CommandExample{
					{Description: "Use Baseten Model APIs in the desktop app.", Command: "baseten codex use --provider baseten"},
					{Description: "Switch back to OpenAI.", Command: "baseten codex use --provider openai"},
				},
				JQExample: CommandExample{
					Description: "List the config.toml keys that changed.",
					Command:     "baseten codex use --provider openai --jq '.config.removed'",
				},
			},
		},
		{
			Name:    "teardown",
			Summary: "Remove Baseten Model APIs from Codex",
			Description: "Undo 'baseten codex setup': remove the [model_providers.baseten-mapi] table from config.toml, " +
				"and model_provider, model_catalog_json, and model there when they select Baseten Model APIs, " +
				"then delete the baseten profile and the Baseten catalog. Other providers, including Grid's, are left alone. Rewriting config.toml drops its comments.",
			Flags: CodexTeardownFlags{},
			Output: &CommandOutput[CodexTeardownResult]{
				TextDescription: "The config.toml keys and files that were removed.",
				Examples: []CommandExample{
					{Description: "Remove Baseten Model APIs from Codex.", Command: "baseten codex teardown"},
					{Description: "Show what teardown would remove.", Command: "baseten codex teardown --dry-run"},
				},
				JQExample: CommandExample{
					Description: "List the files teardown removed.",
					Command:     "baseten codex teardown --jq '.removed_files'",
				},
			},
		},
	},
}

// CodexTeardownFlags configures `baseten codex teardown`.
type CodexTeardownFlags struct {
	CommandFlags
	DryRun bool `flag:"dry-run" desc:"Print what would be removed without changing files"`
}

// CodexTeardownResult is the JSON output of `baseten codex teardown`.
type CodexTeardownResult struct {
	DryRun       bool              `json:"dry_run"`
	Config       CodexConfigChange `json:"config"`
	RemovedFiles []string          `json:"removed_files"`
}

// CodexUseFlags configures `baseten codex use`.
type CodexUseFlags struct {
	CommandFlags
	Provider string `flag:"provider" desc:"Provider for Codex to use" enum:"baseten,openai" required:"true"`
}

// CodexUseResult is the JSON output of `baseten codex use`.
type CodexUseResult struct {
	Provider string            `json:"provider"`
	Config   CodexConfigChange `json:"config"`
}

// CodexSetupFlags configures `baseten codex setup`.
type CodexSetupFlags struct {
	CommandFlags
	DryRun      bool   `flag:"dry-run" desc:"Print what would change without writing files"`
	CatalogPath string `flag:"catalog-path" desc:"Catalog file to write (default: $CODEX_HOME/baseten-mapi-models.json)"`
	Default     bool   `flag:"default" desc:"Also make Baseten Model APIs the provider in config.toml, replacing model_provider and model_catalog_json"`
}

// CodexSetupResult is the JSON output of `baseten codex setup`.
type CodexSetupResult struct {
	DryRun bool `json:"dry_run"`
	// Found counts the models the inference endpoint returned.
	Found    int                  `json:"found"`
	Included []string             `json:"included"`
	Excluded []CodexExcludedModel `json:"excluded"`
	Catalog  CodexCatalogChange   `json:"catalog"`
	Config   CodexConfigChange    `json:"config"`
	// Profile describes the baseten profile file.
	Profile CodexConfigChange `json:"profile"`
	// Active is set when config.toml selects Baseten Model APIs.
	Active bool `json:"active"`
	// MarketplaceIDMissing is set when OPENAI_MARKETPLACE_ID is unset and
	// config.toml has no OpenAI-Marketplace-Id header.
	MarketplaceIDMissing bool `json:"marketplace_id_missing"`
}

// CodexExcludedModel is a model left out of the catalog.
type CodexExcludedModel struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// CodexCatalogChange describes the catalog file write.
type CodexCatalogChange struct {
	Path     string   `json:"path"`
	Added    []string `json:"added"`
	Removed  []string `json:"removed"`
	Modified []string `json:"modified"`
}

// CodexConfigChange describes the config.toml patch.
type CodexConfigChange struct {
	Path      string   `json:"path"`
	Added     []string `json:"added"`
	Preserved []string `json:"preserved"`
	// Replaced lists keys whose values were overwritten.
	Replaced []string `json:"replaced"`
	Removed  []string `json:"removed"`
}
