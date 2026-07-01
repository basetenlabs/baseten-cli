package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

var commandWhoami = Command{
	Name:        "whoami",
	Summary:     "Show the authenticated user",
	Description: "Show the authenticated user for the active credential, fetched from the management API.",
	Flags:       WhoamiFlags{},
	Output: &CommandOutput[managementapi.UserInfo]{
		TextDescription: "Field-per-line summary of the authenticated user: email, name, workspace, and user ID.",
		Examples: []CommandExample{
			{
				Description: "Show the authenticated user.",
				Command:     "baseten whoami",
			},
		},
		JQExample: CommandExample{
			Description: "Print just the email.",
			Command:     "baseten whoami --jq '.email'",
		},
	},
}

// WhoamiFlags configures `baseten whoami`.
type WhoamiFlags struct {
	CommandFlags
}
