package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("model deployment replica list", commandModelDeploymentReplicaList)
	Register("model deployment replica describe", commandModelDeploymentReplicaDescribe)
	Register("model environment replica list", commandModelEnvironmentReplicaList)
	Register("model environment replica describe", commandModelEnvironmentReplicaDescribe)
	Register("model environment replica terminate", commandModelEnvironmentReplicaTerminate)
}

func replicaStatuses(values []string) *[]managementapi.DeploymentReplicaStatus {
	if len(values) == 0 {
		return nil
	}
	statuses := make([]managementapi.DeploymentReplicaStatus, len(values))
	for i, value := range values {
		statuses[i] = managementapi.DeploymentReplicaStatus(strings.ToUpper(value))
	}
	return &statuses
}

func commandModelDeploymentReplicaList(ctx *CommandContext, flags *cmd.ModelDeploymentReplicaListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveDeploymentRef(ctx, cl.API(), flags.ModelDeploymentIDFlags)
	if err != nil {
		return err
	}
	params := managementapi.GetV1ModelsModelIdDeploymentsDeploymentIdReplicasParams{Statuses: replicaStatuses(flags.Status)}
	return listReplicas(ctx, func(cursor *string) (*managementapi.DeploymentReplicas, error) {
		params.Cursor = cursor
		return cl.API().GetModelsDeploymentsReplicas(ctx, ref.ModelID, ref.DeploymentID, params)
	})
}

func commandModelEnvironmentReplicaList(ctx *CommandContext, flags *cmd.ModelEnvironmentReplicaListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}
	params := managementapi.GetV1ModelsModelIdEnvironmentsEnvNameReplicasParams{Statuses: replicaStatuses(flags.Status)}
	return listReplicas(ctx, func(cursor *string) (*managementapi.DeploymentReplicas, error) {
		params.Cursor = cursor
		return cl.API().GetModelsEnvironmentsReplicas(ctx, ref.ID, flags.EnvName, params)
	})
}

func listReplicas(ctx *CommandContext, fetch func(*string) (*managementapi.DeploymentReplicas, error)) error {
	var cursor *string
	seen := make(map[string]bool)
	var replicas []managementapi.DeploymentReplica
	printed := false
	stream := ctx.JSON && (ctx.JSONLines || ctx.JQQuery == nil)
	var writer *JSONArrayWriter
	for {
		page, err := fetch(cursor)
		if err != nil {
			if writer != nil {
				writer.Close()
				if !ctx.JSONLines {
					ctx.OutputLine("}")
				}
			}
			return fmt.Errorf("list replicas: %w", err)
		}
		if stream {
			if writer == nil {
				if !ctx.JSONLines {
					ctx.Output("{\"items\":")
				}
				writer = ctx.NewJSONArrayWriter()
			}
			for _, replica := range page.Items {
				writer.Write(replica)
			}
		} else if ctx.JSON {
			replicas = append(replicas, page.Items...)
		} else if len(page.Items) > 0 {
			rows := make([][]string, 0, len(page.Items))
			for _, replica := range page.Items {
				rows = append(rows, replicaRow(replica))
			}
			ctx.OutputTable(TableOutput{Headers: replicaHeaders(), Rows: rows})
			printed = true
		}
		if !page.Pagination.HasMore {
			break
		}
		next := page.Pagination.Cursor
		if next == nil || *next == "" || seen[*next] {
			if writer != nil {
				writer.Close()
				if !ctx.JSONLines {
					ctx.OutputLine("}")
				}
			}
			return fmt.Errorf("list replicas: invalid pagination cursor")
		}
		seen[*next] = true
		cursor = next
	}
	if writer != nil {
		writer.Close()
		if !ctx.JSONLines {
			ctx.OutputLine("}")
		}
		return nil
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.ModelReplicaListResult{Items: normalizeJSONSlice(replicas)})
		return nil
	}
	if !printed {
		ctx.LogLine("No replicas found.")
	}
	return nil
}

func replicaHeaders() []string {
	return []string{"ID", "DEPLOYMENT ID", "DEPLOYMENT", "STATUS", "CREATED", "TERMINATED", "AGE (SECONDS)", "RESTARTS"}
}

func replicaRow(replica managementapi.DeploymentReplica) []string {
	terminated, restarts := "-", "-"
	if replica.TerminatedAt != nil {
		terminated = replica.TerminatedAt.UTC().Format(time.RFC3339)
	}
	if replica.RestartCount != nil {
		restarts = strconv.Itoa(*replica.RestartCount)
	}
	return []string{replica.Id, replica.DeploymentId, replica.DeploymentName, string(replica.Status),
		replica.CreatedAt.UTC().Format(time.RFC3339), terminated,
		strconv.FormatFloat(float64(replica.AgeSeconds), 'f', -1, 32), restarts}
}

func outputReplica(ctx *CommandContext, replica *managementapi.DeploymentReplica) {
	if ctx.JSON {
		ctx.OutputJSON(replica)
		return
	}
	row := replicaRow(*replica)
	for i, header := range replicaHeaders() {
		ctx.Outputf("%s: %s\n", header, row[i])
	}
}

func commandModelDeploymentReplicaDescribe(ctx *CommandContext, flags *cmd.ModelDeploymentReplicaDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveDeploymentRef(ctx, cl.API(), flags.ModelDeploymentIDFlags)
	if err != nil {
		return err
	}
	replica, err := cl.API().GetModelsDeploymentsReplicasReplicaId(ctx, ref.ModelID, ref.DeploymentID, flags.ReplicaID)
	if err != nil {
		return fmt.Errorf("describe replica %s: %w", flags.ReplicaID, err)
	}
	outputReplica(ctx, replica)
	return nil
}

func commandModelEnvironmentReplicaDescribe(ctx *CommandContext, flags *cmd.ModelEnvironmentReplicaDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}
	replica, err := cl.API().GetModelsEnvironmentsReplicasReplicaId(ctx, ref.ID, flags.EnvName, flags.ReplicaID)
	if err != nil {
		return fmt.Errorf("describe replica %s: %w", flags.ReplicaID, err)
	}
	outputReplica(ctx, replica)
	return nil
}

func commandModelEnvironmentReplicaTerminate(ctx *CommandContext, flags *cmd.ModelEnvironmentReplicaTerminateFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}
	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Terminate replica %s of environment %s?", flags.ReplicaID, flags.EnvName)); err != nil {
			return err
		}
	}
	resp, err := cl.API().DeleteModelsEnvironmentsReplicas(ctx, ref.ID, flags.EnvName, flags.ReplicaID)
	if err != nil {
		return fmt.Errorf("terminate replica %s: %w", flags.ReplicaID, err)
	}
	if resp.Success == nil || !*resp.Success {
		return fmt.Errorf("replica %s was not terminated", flags.ReplicaID)
	}
	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Terminated replica %s of environment %s\n", flags.ReplicaID, flags.EnvName)
	return nil
}
