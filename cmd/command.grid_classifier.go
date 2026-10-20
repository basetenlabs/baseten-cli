package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

var commandGridClassifier = Command{
	Name: "classifier", Summary: "Discover managed classifiers (PRE-RELEASE)",
	Children: []Command{{
		Name: "list", Summary: "List classifiers available to your organization",
		Description: gridPrereleaseNotice + "Classifiers are managed by Baseten. Use their IDs when creating or updating classifier-backed routes.",
		Flags:       GridClassifierListFlags{},
		Output: &CommandOutput[managementapi.GridClassifiersResponse]{
			TextDescription: "Table with ID and DISPLAY NAME columns. An empty catalog prints a message to stderr.",
			Examples:        []CommandExample{{Description: "List available classifiers.", Command: "baseten grid classifier list"}},
			JQExample:       CommandExample{Description: "Print classifier IDs.", Command: "baseten grid classifier list --jq '.classifiers[].id'"},
		},
	}},
}

type GridClassifierListFlags struct{ CommandFlags }
