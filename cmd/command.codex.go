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
				"then add the baseten-mapi provider to Codex's config.toml ($CODEX_HOME, default ~/.codex).\n\n" +
				"The catalog is regenerated on every run; models under vllmtest/ and models with no supported features are left out. " +
				"config.toml only gains missing keys: existing values are never overwritten, so a previous model_provider or model_catalog_json stays active. " +
				"The one exception is the OpenAI-Marketplace-Id header under [model_providers.baseten-mapi.http_headers]: when OPENAI_MARKETPLACE_ID is set, its value is written there, replacing any other value. " +
				"Rewriting config.toml drops its comments. Setup does not set model or model_reasoning_effort.\n\n" +
				"Codex reads BASETEN_API_KEY from the environment at runtime. Setup never writes the key to disk. " +
				"It coexists with 'baseten grid setup', which uses the baseten provider and its own catalog. Running setup again is safe.",
			Flags: CodexSetupFlags{},
			Output: &CommandOutput[CodexSetupResult]{
				TextDescription: "Summary of models found, included, and excluded, catalog changes, and config.toml keys added and preserved.",
				Examples: []CommandExample{
					{Description: "Configure Codex for Baseten Model APIs.", Command: "baseten codex setup"},
					{Description: "Show what setup would change without writing files.", Command: "baseten codex setup --dry-run"},
				},
				JQExample: CommandExample{
					Description: "List the config.toml keys setup added.",
					Command:     "baseten codex setup --jq '.config.added'",
				},
			},
		},
	},
}

// CodexSetupFlags configures `baseten codex setup`.
type CodexSetupFlags struct {
	CommandFlags
	DryRun      bool   `flag:"dry-run" desc:"Print what would change without writing files"`
	CatalogPath string `flag:"catalog-path" desc:"Catalog file to write (default: $CODEX_HOME/baseten-mapi-models.json)"`
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
	// Replaced lists keys overwritten from the environment
	// (OPENAI_MARKETPLACE_ID).
	Replaced []string `json:"replaced"`
}
