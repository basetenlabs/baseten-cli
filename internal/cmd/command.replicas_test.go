package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func replicaResponse() map[string]any {
	return map[string]any{
		"items": []any{
			map[string]any{
				"id": "ab1234-abcde", "deployment_id": "deploy1", "deployment_name": "candidate",
				"status": "ACTIVE", "created_at": "2026-10-01T12:00:00Z", "terminated_at": nil,
				"age_seconds": 600.5, "restart_count": 0,
			},
			map[string]any{
				"id": "cd5678-fghij", "deployment_id": "deploy1", "deployment_name": "candidate",
				"status": "TERMINATED", "created_at": "2026-10-01T12:00:00Z", "terminated_at": "2026-10-01T12:05:00Z",
				"age_seconds": 300, "restart_count": nil,
			},
		},
		"total_count": 3,
		"page_info":   map[string]any{"has_next_page": true, "end_cursor": "signed:next+/="},
	}
}

func Test_Replicas_DeploymentTable(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	path := "/v1/models/model1/deployments/deploy1/replicas"
	m.SetRoute("GET", path, 200, replicaResponse())
	h.Require.NoError(h.Execute("replicas", "--model-id", "model1", "--deployment-id", "deploy1"))
	call := m.FindCall("GET", path)
	h.Require.NotNil(call)
	h.Require.Equal("25", call.Query().Get("limit"))
	h.Require.Equal("CREATED_ASC", call.Query().Get("order_by"))
	h.Require.Len(call.Query(), 2)
	for _, value := range []string{"ab1234-abcde", "deploy1", "candidate", "ACTIVE", "TERMINATED", "600.5", "2026-10-01T12:05:00Z", "Total: 3", "Next cursor: signed:next+/="} {
		h.Require.Contains(h.Stdout.String(), value)
	}
}

func Test_Replicas_EnvironmentFiltersJSON(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	path := "/v1/models/model1/environments/production/replicas"
	response := replicaResponse()
	m.SetRoute("GET", path, 200, response)
	h.Require.NoError(h.Execute("replicas", "--model-id", "model1", "--environment", "production",
		"--status", "active", "--status", "terminated", "--order-by", "deployment-name-desc",
		"--replica-id", "ab1234-abcde", "--limit", "2", "--cursor", "signed:prev+/=", "--output", "json"))
	call := m.FindCall("GET", path)
	h.Require.NotNil(call)
	h.Require.Equal("ACTIVE,TERMINATED", call.Query().Get("statuses"))
	h.Require.Equal("DEPLOYMENT_NAME_DESC", call.Query().Get("order_by"))
	h.Require.Equal("2", call.Query().Get("limit"))
	h.Require.Equal("ab1234-abcde", call.Query().Get("replica_id"))
	h.Require.Equal("signed:prev+/=", call.Query().Get("cursor"))
	expected, err := json.Marshal(response)
	h.Require.NoError(err)
	h.Require.JSONEq(string(expected), h.Stdout.String())
}

func Test_Replicas_Empty(t *testing.T) {
	for _, output := range []string{"text", "json", "jsonl"} {
		t.Run(output, func(t *testing.T) {
			h := NewCommandHarness(t)
			h.MockManagementAPI().SetRoute("GET", "/v1/models/model1/environments/production/replicas", 200,
				map[string]any{"items": []any{}, "total_count": 0, "page_info": map[string]any{"has_next_page": false, "end_cursor": nil}})
			h.Require.NoError(h.Execute("replicas", "--model-id", "model1", "--environment", "production", "--output", output))
			if output == "text" {
				h.Require.Contains(h.Stderr.String(), "No replicas found.")
				h.Require.Equal("Total: 0\n", h.Stdout.String())
			} else {
				h.Require.JSONEq(`{"items":[],"total_count":0,"page_info":{"has_next_page":false,"end_cursor":null}}`, h.Stdout.String())
			}
		})
	}
}

func Test_Replicas_InvalidArguments(t *testing.T) {
	cases := [][]string{
		{}, {"--model-id", "m"}, {"--deployment-id", "d"},
		{"--model-id", "m", "--deployment-id", "d", "--environment", "production"},
	}
	for _, extra := range [][]string{
		{"--limit", "0"}, {"--limit", "101"}, {"--status", "invalid"},
		{"--order-by", "requests-desc"}, {"--order-by", "latency-p99-asc"},
		{"--start", "2026-10-01"}, {"--end", "2026-10-02"}, {"--step-seconds", "60"},
	} {
		cases = append(cases, append([]string{"--model-id", "m", "--deployment-id", "d"}, extra...))
	}
	for _, args := range cases {
		t.Run("args="+fmtArgs(args), func(t *testing.T) {
			h := NewCommandHarness(t)
			h.Require.Error(h.Execute(append([]string{"replicas"}, args...)...))
			h.Require.Equal(2, h.ExitCode)
		})
	}
}

func fmtArgs(args []string) string {
	b, _ := json.Marshal(args)
	return string(b)
}

func Test_Replicas_APIError(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m/deployments/d/replicas", 404, map[string]any{"error": "deployment missing"})
	h.Require.Error(h.Execute("replicas", "--model-id", "m", "--deployment-id", "d", "--output", "json"))
	h.Require.Equal(404, decodeJSONErrorEnvelope(h).APIStatusCode)
	h.Require.NotContains(h.Stdout.String(), "Total:")
}

func Test_Replicas_JQ(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m/deployments/d/replicas", 200, replicaResponse())
	h.Require.NoError(h.Execute("replicas", "--model-id", "m", "--deployment-id", "d", "--jq", ".total_count"))
	h.Require.Equal("3\n", h.Stdout.String())
}

func Test_Replicas_Authentication(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRouteFunc("GET", "/v1/models/m/deployments/d/replicas", func(w http.ResponseWriter, r *http.Request) {
		h.Require.Equal("Bearer test-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(replicaResponse())
	})
	h.Require.NoError(h.Execute("replicas", "--model-id", "m", "--deployment-id", "d"))
}

func Test_Replicas_Profile(t *testing.T) {
	h := newAuthHarness(t)
	m := h.MockManagementAPI()
	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile("first", m.URL, "first-key", true, nil))
	h.Require.NoError(store.SetAPIKeyProfile("second", m.URL, "second-key", false, nil))
	m.SetRouteFunc("GET", "/v1/models/m/environments/production/replicas", func(w http.ResponseWriter, r *http.Request) {
		h.Require.Equal("Bearer second-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(replicaResponse())
	})
	h.Require.NoError(h.Execute("replicas", "--model-id", "m", "--environment", "production", "--profile", "second"))
}
