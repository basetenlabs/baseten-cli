package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

var commandSandboxPreview = Command{
	Name:    "preview",
	Summary: "Manage sandbox previews (PRE-RELEASE)",
	Description: sandboxPreRelease +
		"Previews expose a port of a sandbox on an HTTPS URL. A public preview is reachable by " +
		"anyone with the URL. A private preview, the default, needs a token from 'sandbox preview " +
		"token create'.",
	Children: []Command{
		{
			Name:    "list",
			Summary: "List a sandbox's previews (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Lists a sandbox's previews, up to --limit.",
			Flags: SandboxPreviewListFlags{},
			Output: &CommandOutput[SandboxPreviewList]{
				TextDescription: "Table with columns: NAME, PORT, PUBLIC, STATUS, URL, EXPIRES. " +
					"Prints \"No previews found.\" to stderr when the list is empty, and a note to " +
					"stderr when --limit left some out.",
				Examples: []CommandExample{{
					Description: "List a sandbox's previews.",
					Command:     "baseten sandbox preview list --name my-sandbox",
				}},
				JQExample: CommandExample{
					Description: "Print every preview's URL.",
					Command:     "baseten sandbox preview list --name my-sandbox --jq '.items[].url'",
				},
			},
		},
		{
			Name:    "describe",
			Summary: "Describe a sandbox preview (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Retrieves one preview's record: URL, port, status, and headers. Request header " +
				"values are masked unless --show-secrets is passed.",
			Flags: SandboxPreviewDescribeFlags{},
			Output: &CommandOutput[managementapi.SandboxPreview]{
				TextDescription: "One field per line describing the preview. Empty fields are left out.",
				Examples: []CommandExample{{
					Description: "Describe a preview.",
					Command:     "baseten sandbox preview describe --name my-sandbox --preview-name web",
				}},
				JQExample: CommandExample{
					Description: "Print the preview's URL.",
					CommandLines: []string{
						"baseten sandbox preview describe --name my-sandbox --preview-name web",
						"--jq '.url'",
					},
				},
			},
		},
		{
			Name:    "create",
			Summary: "Create a sandbox preview (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Exposes a port of a sandbox on an HTTPS URL. The preview is private unless --public " +
				"is passed. Port 80 and the sandbox's reserved system ports are rejected, and port " +
				"443 must stay private.",
			Flags: SandboxPreviewCreateFlags{},
			Output: &CommandOutput[managementapi.SandboxPreview]{
				TextDescription: "One field per line describing the preview. Empty fields are left out.",
				Examples: []CommandExample{
					{
						Description: "Expose port 3000 publicly for a day.",
						CommandLines: []string{
							"baseten sandbox preview create --name my-sandbox --port 3000",
							"--public --ttl 24h",
						},
					},
					{
						Description: "Expose port 8000 privately, adding a CORS header to every response.",
						CommandLines: []string{
							"baseten sandbox preview create --name my-sandbox --port 8000",
							"--preview-name api --response-header Access-Control-Allow-Origin='*'",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Create a preview and print its URL.",
					CommandLines: []string{
						"baseten sandbox preview create --name my-sandbox --port 3000",
						"--public --jq '.url'",
					},
				},
			},
		},
		{
			Name:    "update",
			Summary: "Update a sandbox preview (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Changes a preview's access, headers, or expiration. The URL does not change. " +
				"Unset flags leave their fields unchanged. --ttl or --expires-at replaces any " +
				"previous expiration.",
			Flags: SandboxPreviewUpdateFlags{},
			Output: &CommandOutput[managementapi.SandboxPreview]{
				TextDescription: "One field per line describing the preview. Empty fields are left out.",
				Examples: []CommandExample{{
					Description: "Make a preview private.",
					CommandLines: []string{
						"baseten sandbox preview update --name my-sandbox --preview-name web",
						"--private",
					},
				}},
				JQExample: CommandExample{
					Description: "Make a preview public and print whether it is.",
					CommandLines: []string{
						"baseten sandbox preview update --name my-sandbox --preview-name web",
						"--public --jq '.public'",
					},
				},
			},
		},
		{
			Name:    "delete",
			Summary: "Delete a sandbox preview (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Deletes a preview and its tokens. The URL stops serving at once.",
			Flags: SandboxPreviewDeleteFlags{},
			Output: &CommandOutput[managementapi.SandboxPreview]{
				TextDescription: "A confirmation line on stderr.",
				JSONDescription: "The preview's record as of deletion.",
				Examples: []CommandExample{{
					Description: "Delete a preview without the confirmation prompt.",
					CommandLines: []string{
						"baseten sandbox preview delete --name my-sandbox --preview-name web",
						"--yes",
					},
				}},
				JQExample: CommandExample{
					Description: "Delete a preview and print its status.",
					CommandLines: []string{
						"baseten sandbox preview delete --name my-sandbox --preview-name web",
						"--yes --jq '.status'",
					},
				},
			},
		},
		{
			Name:    "token",
			Summary: "Manage tokens for private sandbox previews (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Tokens grant access to a private preview until they expire. Append one to the preview URL " +
				"as ?preview_token=<token>; the first request sets a cookie so a browser can keep " +
				"navigating without it.",
			Children: []Command{
				{
					Name:    "create",
					Summary: "Create a token for a private sandbox preview (PRE-RELEASE)",
					Description: sandboxPreRelease +
						"Creates a token granting access to a private preview until --expires-at.",
					Flags: SandboxPreviewTokenCreateFlags{},
					Output: &CommandOutput[managementapi.SandboxPreviewToken]{
						TextDescription: "One field per line describing the token, then the preview URL with the " +
							"token appended.",
						Examples: []CommandExample{{
							Description: "Create a token valid until a given time.",
							CommandLines: []string{
								"baseten sandbox preview token create --name my-sandbox --preview-name web",
								"--expires-at 2026-11-01T12:00:00Z",
							},
						}},
						JQExample: CommandExample{
							Description: "Create a token and print it.",
							CommandLines: []string{
								"baseten sandbox preview token create --name my-sandbox --preview-name web",
								"--expires-at 2026-11-01T12:00:00Z --jq '.token'",
							},
						},
					},
				},
				{
					Name:    "list",
					Summary: "List a private sandbox preview's tokens (PRE-RELEASE)",
					Description: sandboxPreRelease +
						"Lists a private preview's tokens, including expired ones until they are removed. A " +
						"public preview has no tokens.",
					Flags: SandboxPreviewTokenListFlags{},
					Output: &CommandOutput[SandboxPreviewTokenList]{
						TextDescription: "Table with columns: NAME, EXPIRES, EXPIRED. Prints \"No tokens found.\" " +
							"to stderr when the list is empty.",
						Examples: []CommandExample{{
							Description: "List a preview's tokens.",
							CommandLines: []string{
								"baseten sandbox preview token list --name my-sandbox",
								"--preview-name web",
							},
						}},
						JQExample: CommandExample{
							Description: "Print every unexpired token's name.",
							CommandLines: []string{
								"baseten sandbox preview token list --name my-sandbox",
								"--preview-name web --jq '.items[] | select(.expired | not) | .name'",
							},
						},
					},
				},
				{
					Name:    "delete",
					Summary: "Delete a sandbox preview token (PRE-RELEASE)",
					Description: sandboxPreRelease +
						"Revokes a token. Requests using it are rejected at once.",
					Flags: SandboxPreviewTokenDeleteFlags{},
					Output: &CommandOutput[struct{}]{
						TextDescription: "A confirmation line on stderr.",
						Examples: []CommandExample{{
							Description: "Delete a token without the confirmation prompt.",
							CommandLines: []string{
								"baseten sandbox preview token delete --name my-sandbox --preview-name web",
								"--token-name 7f3k2m9q1x8c4v6b0n5z --yes",
							},
						}},
						JSONOutputUnimportant: true,
					},
				},
			},
		},
	},
}

// SandboxPreviewList is the JSON shape of 'baseten sandbox preview list'.
type SandboxPreviewList struct {
	Items []managementapi.SandboxPreview `json:"items"`
}

// SandboxPreviewTokenList is the JSON shape of 'baseten sandbox preview token
// list'.
type SandboxPreviewTokenList struct {
	Items []managementapi.SandboxPreviewToken `json:"items"`
}

// SandboxPreviewRefFlags selects one preview of the sandbox --name selects.
type SandboxPreviewRefFlags struct {
	PreviewName string `flag:"preview-name" desc:"Name of the preview." required:"true"`
}

// SandboxPreviewHeaderFlags set the headers a preview adds.
type SandboxPreviewHeaderFlags struct {
	ResponseHeader []string `flag:"response-header" desc:"Header added to every response the preview serves, such as a CORS header, as KEY=VALUE. Repeatable."`
	RequestHeader  []string `flag:"request-header" desc:"Header added to every request forwarded to the sandbox, such as an Authorization header the app expects, as KEY=VALUE. Masked when read back. Repeatable."`
}

// SandboxPreviewExpiryFlags set when a preview is deleted automatically.
type SandboxPreviewExpiryFlags struct {
	TTL       time.Duration `flag:"ttl" desc:"Delete the preview this long from now, such as 24h or 7d. Cannot be combined with --expires-at."`
	ExpiresAt time.Time     `flag:"expires-at" desc:"Delete the preview at this time. ISO 8601, local when no timezone is given. Cannot be combined with --ttl."`
}

// SandboxPreviewListFlags configures 'baseten sandbox preview list'.
type SandboxPreviewListFlags struct {
	CommandFlags
	SandboxRefFlags

	Limit int `flag:"limit" desc:"Most previews to list. 0 lists all." default:"1000"`
}

// SandboxPreviewDescribeFlags configures 'baseten sandbox preview describe'.
type SandboxPreviewDescribeFlags struct {
	CommandFlags
	SandboxRefFlags
	SandboxPreviewRefFlags

	ShowSecrets bool `flag:"show-secrets" desc:"Reveal request header values. Requires the workspace administrator role; other callers still see masked values."`
}

// SandboxPreviewCreateFlags configures 'baseten sandbox preview create'. Unset
// flags are left out of the request, so the server applies its defaults.
type SandboxPreviewCreateFlags struct {
	CommandFlags
	SandboxRefFlags
	SandboxPreviewHeaderFlags
	SandboxPreviewExpiryFlags

	Port        int    `flag:"port" desc:"Sandbox port to expose." required:"true"`
	PreviewName string `flag:"preview-name" desc:"Name of the preview, unique within the sandbox. The server generates one when omitted."`
	Public      bool   `flag:"public" desc:"Make the URL reachable without a token."`
	WithToken   bool   `flag:"with-token" desc:"Mint an access token and print the link with it embedded, ready to open or share. The token expires with the preview when it has a ttl, else after 7 days."`
	IfNotExists bool   `flag:"if-not-exists" desc:"Return the existing preview with this name, unchanged, instead of failing."`
}

// SandboxPreviewUpdateFlags configures 'baseten sandbox preview update'. Unset
// flags leave their fields unchanged.
type SandboxPreviewUpdateFlags struct {
	CommandFlags
	SandboxRefFlags
	SandboxPreviewRefFlags
	SandboxPreviewExpiryFlags

	ResponseHeader []string `flag:"response-header" desc:"Header added to every response the preview serves, as KEY=VALUE, replacing all of the preview's response headers. Repeatable."`
	RequestHeader  []string `flag:"request-header" desc:"Header added to every request forwarded to the sandbox, as KEY=VALUE, replacing all of the preview's request headers. Repeatable."`
	Public         bool     `flag:"public" desc:"Make the URL reachable without a token. Cannot be combined with --private."`
	Private        bool     `flag:"private" desc:"Require a token to reach the URL. Cannot be combined with --public."`
}

// SandboxPreviewDeleteFlags configures 'baseten sandbox preview delete'.
type SandboxPreviewDeleteFlags struct {
	CommandFlags
	SandboxRefFlags
	SandboxPreviewRefFlags

	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// SandboxPreviewTokenCreateFlags configures 'baseten sandbox preview token
// create'.
type SandboxPreviewTokenCreateFlags struct {
	CommandFlags
	SandboxRefFlags
	SandboxPreviewRefFlags

	ExpiresAt   time.Time `flag:"expires-at" desc:"Time the token stops working. ISO 8601, local when no timezone is given." required:"true"`
	IfNotExists bool      `flag:"if-not-exists" desc:"Return an existing unexpired token of the preview that stays valid at least until --expires-at, instead of creating one. A token created moments before may not be found yet, so a new one may be created anyway."`
}

// SandboxPreviewTokenListFlags configures 'baseten sandbox preview token
// list'.
type SandboxPreviewTokenListFlags struct {
	CommandFlags
	SandboxRefFlags
	SandboxPreviewRefFlags
}

// SandboxPreviewTokenDeleteFlags configures 'baseten sandbox preview token
// delete'.
type SandboxPreviewTokenDeleteFlags struct {
	CommandFlags
	SandboxRefFlags
	SandboxPreviewRefFlags

	TokenName string `flag:"token-name" desc:"Name of the token." required:"true"`
	Yes       bool   `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}
