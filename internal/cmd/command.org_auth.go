package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

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

// getOrganizationInfo fetches GET /v1/organizations/me raw. Swap for the
// generated client method once the endpoint is available in baseten-go.
func getOrganizationInfo(ctx *CommandContext, api *managementapi.Client) (*cmd.OrgInfo, error) {
	url := strings.TrimRight(api.BaseURL, "/") + "/v1/organizations/me"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	for key, vals := range api.Headers {
		for _, val := range vals {
			req.Header.Add(key, val)
		}
	}
	resp, err := api.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching organization info: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading organization info: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &managementapi.ResponseError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	var info cmd.OrgInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("decoding organization info: %w", err)
	}
	return &info, nil
}

func commandOrgDescribe(ctx *CommandContext, flags *cmd.OrgDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	info, err := getOrganizationInfo(ctx, cl.API())
	if err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(info)
		return nil
	}

	teams, err := cl.API().GetTeams(ctx, managementapi.GetV1TeamsParams{})
	if err != nil {
		return fmt.Errorf("listing teams: %w", err)
	}

	ctx.Outputf("Org ID:               %s\n", info.OrgID)
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
	ctx.Outputf("AWS External ID:      %s\n", info.AwsAssumeRole.ExternalID)
	return nil
}
