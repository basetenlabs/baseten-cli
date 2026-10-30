package cmd

import "time"

var commandReplicas = Command{
	Name:    "replicas",
	Summary: "List replica status for a deployment or environment",
	Description: "List one page of replica lifecycle information, including terminated replicas. " +
		"Choose exactly one deployment or environment. Environment listings include deployments " +
		"active during the last hour. Metrics are not included.\n\n" +
		"Use --profile to select a remote and --output json for the complete REST response. " +
		"Continue with --cursor and unchanged filters and ordering; cursors expire after one hour.",
	Flags: ReplicasFlags{},
	Output: &CommandOutput[ReplicasResult]{
		TextDescription: "Table of replica IDs, deployment IDs and names, statuses, creation and termination times, " +
			"ages in seconds, and restart counts. The filtered total and next cursor follow the table.",
		JSONDescription: "The complete replica listing REST response, including items, total_count, and page_info.",
		Examples: []CommandExample{
			{Description: "List replicas for a deployment.", Command: "baseten replicas --model-id MODEL --deployment-id DEPLOYMENT"},
			{Description: "List active replicas in production.", CommandLines: []string{"baseten replicas --model-id MODEL --environment production", "--status active"}},
		},
		JQExample: CommandExample{
			Description:  "Print replica IDs in production.",
			CommandLines: []string{"baseten replicas --model-id MODEL --environment production", "--jq '.items[].id'"},
		},
	},
}

type ReplicasFlags struct {
	CommandFlags
	ModelID      string   `flag:"model-id" desc:"Model ID" required:"true"`
	DeploymentID string   `flag:"deployment-id" desc:"Deployment ID" oneof:"replica-scope"`
	Environment  string   `flag:"environment" desc:"Environment name" oneof:"replica-scope"`
	ReplicaID    string   `flag:"replica-id" desc:"Exact replica ID to return"`
	Status       []string `flag:"status" desc:"Include this status. Repeatable; defaults to all statuses." enum:"starting,active,unhealthy,terminating,terminated,unknown"`
	OrderBy      string   `flag:"order-by" desc:"Replica ordering" default:"created-asc" enum:"status-asc,status-desc,deployment-name-asc,deployment-name-desc,created-asc,created-desc,terminated-asc,terminated-desc,restarts-asc,restarts-desc,age-asc,age-desc,name-asc,name-desc"`
	Limit        int      `flag:"limit" desc:"Maximum replicas per page (1 to 100)" default:"25"`
	Cursor       string   `flag:"cursor" desc:"Next cursor from a previous page"`
}

type Replica struct {
	ID             string     `json:"id"`
	DeploymentID   string     `json:"deployment_id"`
	DeploymentName string     `json:"deployment_name"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	TerminatedAt   *time.Time `json:"terminated_at"`
	AgeSeconds     float64    `json:"age_seconds"`
	RestartCount   *int       `json:"restart_count"`
}

type ReplicasResult struct {
	Items      []Replica       `json:"items"`
	TotalCount int             `json:"total_count"`
	PageInfo   ReplicaPageInfo `json:"page_info"`
}

type ReplicaPageInfo struct {
	HasNextPage bool    `json:"has_next_page"`
	EndCursor   *string `json:"end_cursor"`
}
