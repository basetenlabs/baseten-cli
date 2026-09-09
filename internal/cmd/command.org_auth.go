package cmd

import (
	"fmt"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("org describe", commandOrgDescribe)
}

const (
	oidcIssuer             = "https://oidc.baseten.co"
	oidcAudience           = "oidc.baseten.co"
	oidcWorkloadTypes      = "model_container, model_build"
	oidcSubjectClaimFormat = "v=1:org=<org_id>:team=<team_id>:model=<model_id>:" +
		"deployment=<deployment_id>:environment=<environment>:type=<workload_type>"
)

func commandOrgDescribe(ctx *CommandContext, flags *cmd.OrgDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	info, err := cl.API().GetOrganizationsMe(ctx)
	if err != nil {
		return fmt.Errorf("fetching organization info: %w", err)
	}

	if ctx.JSON {
		ctx.OutputJSON(info)
		return nil
	}

	teams, err := cl.API().GetTeams(ctx, managementapi.GetV1TeamsParams{})
	if err != nil {
		return fmt.Errorf("listing teams: %w", err)
	}

	ctx.Outputf("Org ID:               %s\n", info.OrgId)
	if info.Name != nil && *info.Name != "" {
		ctx.Outputf("Name:                 %s\n", *info.Name)
	}
	for i, t := range teams.Teams {
		label := "Teams:               "
		if i > 0 {
			label = "                     "
		}
		ctx.Outputf("%s %s (%s)\n", label, t.Id, t.Name)
	}
	ctx.Outputf("Issuer:               %s\n", oidcIssuer)
	ctx.Outputf("Audience:             %s\n", oidcAudience)
	ctx.Outputf("Workload Types:       %s\n", oidcWorkloadTypes)
	ctx.Outputf("Subject Claim Format: %s\n", oidcSubjectClaimFormat)
	// The server nulls aws_assume_role while the method is not enabled.
	if info.AwsAssumeRole == nil {
		ctx.Outputf("AWS AssumeRole:       not enabled (contact Baseten support to enable it)\n")
		return nil
	}
	ctx.Outputf("Baseten Role ARN:     %s\n", info.AwsAssumeRole.BasetenRoleArn)
	ctx.Outputf("AWS External ID:      %s\n", info.AwsAssumeRole.ExternalId)
	return nil
}
