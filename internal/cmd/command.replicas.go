package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("replicas", commandReplicas)
}

func commandReplicas(ctx *CommandContext, flags *cmd.ReplicasFlags) error {
	if flags.Limit < 1 || flags.Limit > 100 {
		return cmd.NewErrUsagef("--limit must be between 1 and 100")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	api := cl.API()
	path := "/v1/models/" + url.PathEscape(flags.ModelID)
	if flags.DeploymentID != "" {
		path += "/deployments/" + url.PathEscape(flags.DeploymentID)
	} else {
		path += "/environments/" + url.PathEscape(flags.Environment)
	}
	query := url.Values{
		"limit":    {strconv.Itoa(flags.Limit)},
		"order_by": {strings.ToUpper(strings.ReplaceAll(flags.OrderBy, "-", "_"))},
	}
	if flags.ReplicaID != "" {
		query.Set("replica_id", flags.ReplicaID)
	}
	if len(flags.Status) > 0 {
		query.Set("statuses", strings.ToUpper(strings.Join(flags.Status, ",")))
	}
	if flags.Cursor != "" {
		query.Set("cursor", flags.Cursor)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(api.BaseURL, "/")+path+"/replicas?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("build replica request: %w", err)
	}
	req.Header = api.Headers.Clone()
	resp, err := api.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("list replicas: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read replicas: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &managementapi.ResponseError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	var result cmd.ReplicasResult
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decode replicas: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(json.RawMessage(body))
		return nil
	}
	rows := make([][]string, 0, len(result.Items))
	for _, replica := range result.Items {
		terminated := "-"
		if replica.TerminatedAt != nil {
			terminated = replica.TerminatedAt.UTC().Format(time.RFC3339)
		}
		restarts := "-"
		if replica.RestartCount != nil {
			restarts = strconv.Itoa(*replica.RestartCount)
		}
		rows = append(rows, []string{
			replica.ID, replica.DeploymentID, replica.DeploymentName, replica.Status,
			replica.CreatedAt.UTC().Format(time.RFC3339), terminated,
			strconv.FormatFloat(replica.AgeSeconds, 'f', -1, 64), restarts,
		})
	}
	if len(rows) == 0 {
		ctx.LogLine("No replicas found.")
	} else {
		ctx.OutputTable(TableOutput{
			Headers: []string{"ID", "DEPLOYMENT ID", "DEPLOYMENT", "STATUS", "CREATED", "TERMINATED", "AGE (SECONDS)", "RESTARTS"},
			Rows:    rows,
		})
	}
	ctx.Outputf("Total: %d\n", result.TotalCount)
	if result.PageInfo.HasNextPage && result.PageInfo.EndCursor != nil {
		ctx.Outputf("Next cursor: %s\n", *result.PageInfo.EndCursor)
	}
	return nil
}
