package cmd

import (
	"fmt"

	"github.com/basetenlabs/baseten-cli/cmd"
)

func init() { Register("grid classifier list", commandGridClassifierList) }

func commandGridClassifierList(ctx *CommandContext, f *cmd.GridClassifierListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	classifiers, err := cl.API().GetGridClassifiers(ctx)
	if err != nil {
		return fmt.Errorf("listing classifiers: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(classifiers)
		return nil
	}
	if len(classifiers.Classifiers) == 0 {
		ctx.LogLine("No classifiers found.")
		return nil
	}
	var rows [][]string
	for _, classifier := range classifiers.Classifiers {
		rows = append(rows, []string{classifier.Id, classifier.DisplayName})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"ID", "DISPLAY NAME"}, Rows: rows})
	return nil
}
