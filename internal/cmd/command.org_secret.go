package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/charmbracelet/huh"
)

func init() {
	Register("org secret list", commandOrgSecretList)
	Register("org secret set", commandOrgSecretSet)
	Register("org secret delete", commandOrgSecretDelete)
}

func commandOrgSecretList(ctx *CommandContext, flags *cmd.OrgSecretListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	teamID, err := ResolveTeam(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}

	// Org-wide list vs per-team list.
	var secrets *managementapi.Secrets
	if teamID == "" {
		secrets, err = cl.API().GetSecrets(ctx)
	} else {
		secrets, err = cl.API().GetTeamsSecrets(ctx, teamID)
	}
	if err != nil {
		return fmt.Errorf("listing secrets: %w", err)
	}

	if ctx.JSON {
		ctx.OutputJSON(secrets)
		return nil
	}
	if len(secrets.Secrets) == 0 {
		ctx.LogLine("No secrets found.")
		return nil
	}
	rows := make([][]string, 0, len(secrets.Secrets))
	for _, s := range secrets.Secrets {
		rows = append(rows, []string{s.Name, s.TeamName, s.CreatedAt.UTC().Format(time.RFC3339)})
	}
	ctx.OutputTable([]string{"NAME", "TEAM", "CREATED"}, rows)
	return nil
}

func commandOrgSecretSet(ctx *CommandContext, flags *cmd.OrgSecretSetFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	teamID, err := ResolveTeam(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}

	// Resolve the value. --value wins when explicitly set (even to ""). Otherwise
	// TTY prompts, non-TTY reads stdin with one trailing newline stripped.
	var value string
	switch {
	case ctx.Command.Flags().Changed("value"):
		value = flags.Value
	case ctx.IsInteractive():
		err = huh.NewInput().
			Title("Secret value").
			EchoMode(huh.EchoModePassword).
			Value(&value).
			Run()
		if err != nil {
			return err
		}
	default:
		buf, err := io.ReadAll(ctx.Stdin)
		if err != nil {
			return fmt.Errorf("reading secret from stdin: %w", err)
		}
		value = strings.TrimRight(string(buf), "\r\n")
		if value == "" {
			return &ErrUsage{Err: fmt.Errorf("no secret value supplied; pipe a value to stdin or pass --value")}
		}
	}

	req := managementapi.UpsertSecretRequest{Name: flags.Name, Value: value}
	var secret *managementapi.Secret
	if teamID == "" {
		secret, err = cl.API().PostSecrets(ctx, req)
	} else {
		secret, err = cl.API().PostTeamsSecrets(ctx, teamID, req)
	}
	if err != nil {
		return fmt.Errorf("upserting secret: %w", err)
	}

	if ctx.JSON {
		ctx.OutputJSON(secret)
		return nil
	}
	ctx.Logf("Set secret %s\n", secret.Name)
	return nil
}

func commandOrgSecretDelete(ctx *CommandContext, flags *cmd.OrgSecretDeleteFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	teamID, err := ResolveTeam(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}

	var secret *managementapi.SecretTombstone
	if teamID == "" {
		secret, err = cl.API().DeleteSecrets(ctx, flags.Name)
	} else {
		secret, err = cl.API().DeleteTeamsSecrets(ctx, teamID, flags.Name)
	}
	if err != nil {
		return fmt.Errorf("deleting secret: %w", err)
	}

	if ctx.JSON {
		ctx.OutputJSON(secret)
		return nil
	}
	ctx.Logf("Deleted secret %s\n", secret.Name)
	return nil
}
