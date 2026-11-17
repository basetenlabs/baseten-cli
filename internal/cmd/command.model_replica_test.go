package cmd_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func replicaFixture(id, status string) map[string]any {
	return map[string]any{
		"id": id, "deployment_id": "deploy1", "deployment_name": "candidate", "status": status,
		"created_at": "2026-10-01T12:00:00Z", "terminated_at": nil,
		"age_seconds": 600.5, "restart_count": nil,
	}
}

func Test_Model_Replica_List_AllPages(t *testing.T) {
	for _, scope := range []string{"deployment", "environment"} {
		for _, output := range []string{"text", "json", "jsonl", "jq"} {
			t.Run(scope+"/"+output, func(t *testing.T) {
				h := NewCommandHarness(t)
				m := h.MockManagementAPI()
				path := "/v1/models/model1/" + scope + "s/"
				ref := []string{"--deployment-id", "deploy1"}
				if scope == "environment" {
					path += "production"
					ref = []string{"--env-name", "production"}
				} else {
					path += "deploy1"
				}
				path += "/replicas"
				m.SetRouteFunc("GET", path, func(w http.ResponseWriter, r *http.Request) {
					h.Require.Equal("Bearer test-key", r.Header.Get("Authorization"))
					h.Require.Equal([]string{"ACTIVE", "TERMINATED"}, r.URL.Query()["statuses"])
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Query().Get("cursor") == "" {
						_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{replicaFixture("abcde", "ACTIVE")}, "pagination": map[string]any{"has_more": true, "cursor": "next+/="}})
					} else {
						h.Require.Equal("next+/=", r.URL.Query().Get("cursor"))
						_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{replicaFixture("fghij", "TERMINATED")}, "pagination": map[string]any{"has_more": false, "cursor": nil}})
					}
				})
				args := []string{"model", scope, "replica", "list", "--model-id", "model1"}
				args = append(args, ref...)
				args = append(args, "--status", "active", "--status", "terminated")
				if output == "jq" {
					args = append(args, "--jq", ".items[].id")
				} else {
					args = append(args, "--output", output)
				}
				h.Require.NoError(h.Execute(args...))
				h.Require.Len(m.Calls(), 2)
				switch output {
				case "json":
					expected, err := json.Marshal(map[string]any{"items": []any{replicaFixture("abcde", "ACTIVE"), replicaFixture("fghij", "TERMINATED")}})
					h.Require.NoError(err)
					h.Require.JSONEq(string(expected), h.Stdout.String())
				case "jsonl":
					lines := strings.Split(strings.TrimSpace(h.Stdout.String()), "\n")
					h.Require.Len(lines, 2)
					for i, item := range []map[string]any{replicaFixture("abcde", "ACTIVE"), replicaFixture("fghij", "TERMINATED")} {
						expected, err := json.Marshal(item)
						h.Require.NoError(err)
						h.Require.JSONEq(string(expected), lines[i])
					}
				case "jq":
					h.Require.Equal("\"abcde\"\n\"fghij\"\n", h.Stdout.String())
				default:
					h.Require.Equal(2, strings.Count(h.Stdout.String(), "DEPLOYMENT ID"))
					for _, text := range []string{"abcde", "fghij", "ACTIVE", "TERMINATED", "600.5"} {
						h.Require.Contains(h.Stdout.String(), text)
					}
				}
			})
		}
	}
}

func Test_Model_Replica_List_EmptyAndInvalidPagination(t *testing.T) {
	for _, test := range []struct {
		name       string
		pagination map[string]any
		wantErr    bool
	}{
		{"empty", map[string]any{"has_more": false, "cursor": nil}, false},
		{"missing cursor", map[string]any{"has_more": true, "cursor": nil}, true},
		{"repeated cursor", map[string]any{"has_more": true, "cursor": "same"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("GET", "/v1/models/m/deployments/d/replicas", 200, map[string]any{"items": []any{}, "pagination": test.pagination})
			err := h.Execute("model", "deployment", "replica", "list", "--model-id", "m", "--deployment-id", "d", "--output", "json")
			if test.wantErr {
				h.Require.ErrorContains(err, "invalid pagination cursor")
				h.Require.LessOrEqual(len(m.Calls()), 2)
			} else {
				h.Require.NoError(err)
				h.Require.JSONEq(`{"items":[]}`, h.Stdout.String())
			}
		})
	}
}

func Test_Model_Replica_Describe(t *testing.T) {
	for _, scope := range []string{"deployment", "environment"} {
		t.Run(scope, func(t *testing.T) {
			h := NewCommandHarness(t)
			path := "/v1/models/m/" + scope + "s/"
			ref := []string{"--deployment-id", "d"}
			if scope == "environment" {
				path += "production"
				ref = []string{"--env-name", "production"}
			} else {
				path += "d"
			}
			path += "/replicas/abcde"
			h.MockManagementAPI().SetRoute("GET", path, 200, replicaFixture("abcde", "TERMINATED"))
			args := []string{"model", scope, "replica", "describe", "--model-id", "m"}
			args = append(args, ref...)
			args = append(args, "--replica-id", "abcde", "--output", "json")
			h.Require.NoError(h.Execute(args...))
			expected, err := json.Marshal(replicaFixture("abcde", "TERMINATED"))
			h.Require.NoError(err)
			h.Require.JSONEq(string(expected), h.Stdout.String())
			h.MockManagementAPI().SetRoute("GET", path, 404, map[string]any{"error": "not found"})
			h.Require.Error(h.Execute(args...))
			h.Require.Equal(404, decodeJSONErrorEnvelope(h).APIStatusCode)
		})
	}
}

func Test_Model_Replica_InvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"model", "deployment", "replica", "list", "--model-id", "m"},
		{"model", "deployment", "replica", "describe", "--model-id", "m", "--deployment-id", "d"},
		{"model", "environment", "replica", "list", "--model-id", "m"},
		{"model", "environment", "replica", "describe", "--model-id", "m", "--env-name", "production"},
		{"model", "environment", "replica", "list", "--model-id", "m", "--env-name", "production", "--status", "bad"},
	} {
		h := NewCommandHarness(t)
		h.Require.Error(h.Execute(args...))
		h.Require.Equal(2, h.ExitCode)
	}
}

func Test_Model_Environment_Replica_Terminate(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	path := "/v1/models/m/environments/production/replicas/abcde"
	m.SetRoute("DELETE", path, 200, map[string]any{"success": true})
	args := []string{"model", "environment", "replica", "terminate", "--model-id", "m", "--env-name", "production", "--replica-id", "abcde"}
	h.Require.Error(h.Execute(args...))
	h.Require.Empty(m.Calls())
	args = append(args, "--yes", "--output", "json")
	h.Require.NoError(h.Execute(args...))
	h.Require.JSONEq(`{"success":true}`, h.Stdout.String())
	m.SetRoute("DELETE", path, 200, map[string]any{"success": false})
	h.Require.ErrorContains(h.Execute(args...), "was not terminated")
}

func Test_Model_Replica_List_LaterPageError(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRouteFunc("GET", "/v1/models/m/deployments/d/replicas", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") != "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"unavailable"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":      []any{replicaFixture("abcde", "ACTIVE")},
			"pagination": map[string]any{"has_more": true, "cursor": "next"},
		})
	})
	err := h.Execute("model", "deployment", "replica", "list", "--model-id", "m", "--deployment-id", "d", "--output", "json")
	h.Require.Error(err)
	decoder := json.NewDecoder(&h.Stdout)
	var partial, apiError map[string]any
	h.Require.NoError(decoder.Decode(&partial))
	h.Require.Len(partial["items"], 1)
	h.Require.NoError(decoder.Decode(&apiError))
	h.Require.Contains(apiError, "error")
	err = h.Execute("model", "deployment", "replica", "list", "--model-id", "m", "--deployment-id", "d")
	h.Require.Error(err)
	h.Require.Contains(h.Stdout.String(), "abcde")
}

func Test_Model_Replica_List_Profile(t *testing.T) {
	h := newAuthHarness(t)
	m := h.MockManagementAPI()
	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "first", m.URL, "first-key", true, nil))
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "second", m.URL, "second-key", false, nil))
	m.SetRouteFunc("GET", "/v1/models/m/environments/production/replicas", func(w http.ResponseWriter, r *http.Request) {
		h.Require.Equal("Bearer second-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": false, "cursor": nil}})
	})
	h.Require.NoError(h.Execute("model", "environment", "replica", "list", "--model-id", "m", "--env-name", "production", "--profile", "second"))
}
