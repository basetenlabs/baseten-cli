package cmd_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

const (
	loopsRunPath         = "/v1/loops/runs/r-1"
	loopsTrainerLogsPath = "/v1/loops/deployments/dep-1/logs"
	loopsTrainerPath     = "/v1/loops/deployments/dep-1"
	loopsSamplerLogsPath = "/v1/models/model-1/deployments/sdep-1/logs"
	loopsSamplerDepPath  = "/v1/models/model-1/deployments/sdep-1"
)

// loopsSamplerFixture is a sampler payload. instanceType "" omits the instance
// type entirely, which is how the backend reports a sampler that never ran.
func loopsSamplerFixture(id, status, instanceType string, gpuCount, nodeCount int, createdAt string) map[string]any {
	s := map[string]any{
		"id":            id,
		"base_model":    "Qwen/Qwen3-8B",
		"base_url":      "https://sampler.example.com",
		"created_at":    createdAt,
		"deployment_id": "sdep-1",
		"model_id":      "model-1",
		"node_count":    nodeCount,
		"status":        map[string]any{"name": status},
		"user":          map[string]any{"email": "owner@example.com"},
	}
	if instanceType != "" {
		s["instance_type"] = loopsInstanceTypeFixture(instanceType, gpuCount)
	}
	return s
}

func loopsInstanceTypeFixture(name string, gpuCount int) map[string]any {
	return map[string]any{
		"id":                   name,
		"name":                 name,
		"gpu_count":            gpuCount,
		"gpu_type":             "H100",
		"gpu_memory_limit_mib": 81920,
		"memory_limit_mib":     262144,
		"millicpu_limit":       26000,
	}
}

func loopsRunFixture(id, status string, sampler map[string]any) map[string]any {
	r := map[string]any{
		"id":            id,
		"name":          "run-" + id,
		"base_model":    "Qwen/Qwen3-8B",
		"base_url":      "https://trainer.example.com",
		"created_at":    "2026-05-14T12:00:00Z",
		"deployment_id": "dep-1",
		"session_id":    "sess-1",
		"status":        map[string]any{"name": status},
		"user":          map[string]any{"email": "owner@example.com"},
	}
	if sampler != nil {
		r["sampler"] = sampler
	}
	return r
}

// loopsDeploymentFixture is a trainer deployment payload for `loops usage`.
func loopsDeploymentFixture(id, status, instanceType string, gpuCount, nodeCount int, createdAt string, sampler map[string]any) map[string]any {
	d := map[string]any{
		"id":            id,
		"base_model":    "Qwen/Qwen3-8B",
		"base_url":      "https://trainer.example.com",
		"created_at":    createdAt,
		"instance_type": loopsInstanceTypeFixture(instanceType, gpuCount),
		"node_count":    nodeCount,
		"active_run_id": "r-" + id,
		"status":        map[string]any{"name": status},
		"user":          map[string]any{"email": "owner@example.com"},
	}
	if sampler != nil {
		d["sampler"] = sampler
	}
	return d
}

func loopsCheckpointFixture(id, runID, target, createdAt string) map[string]any {
	return map[string]any{
		"id":                  id,
		"checkpoint_id":       "checkpoint-" + id,
		"checkpoint_type":     "full",
		"created_at":          createdAt,
		"base_model":          "Qwen/Qwen3-8B",
		"lora_adapter_config": nil,
		"run_id":              runID,
		"size_bytes":          1024,
		"target":              target,
	}
}

func Test_Loops_Run_Create(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/loops/sessions", 200, map[string]any{"session": map[string]any{"id": "sess-1"}})
	m.SetRoute("POST", "/v1/loops/runs", 200,
		map[string]any{"run": loopsRunFixture("r-1", "ACTIVE", nil)})

	h.Require.NoError(h.Execute("loops", "run", "create", "--base-model", "Qwen/Qwen3-8B"))
	call := m.FindCall("POST", "/v1/loops/runs")
	h.Require.NotNil(call)
	body := call.BodyJSON(t)
	h.Require.Equal("sess-1", body["session_id"])
	h.Require.Equal("Qwen/Qwen3-8B", body["base_model"])
	// Unset optional fields are omitted rather than sent as zero values.
	h.Require.NotContains(body, "name")
	h.Require.NotContains(body, "replicas")
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Created Loops run r-1")
}

func Test_Loops_Run_Create_NameAndReplicas(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/loops/sessions", 200, map[string]any{"session": map[string]any{"id": "sess-1"}})
	m.SetRoute("POST", "/v1/loops/runs", 200,
		map[string]any{"run": loopsRunFixture("r-1", "ACTIVE", nil)})

	h.Require.NoError(h.Execute("loops", "run", "create",
		"--base-model", "Qwen/Qwen3-8B", "--name", "experiment-1", "--replicas", "4"))
	body := m.FindCall("POST", "/v1/loops/runs").BodyJSON(t)
	h.Require.Equal("experiment-1", body["name"])
	h.Require.Equal(float64(4), body["replicas"])
}

func Test_Loops_Run_Create_ScopedByTeam(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/teams", 200, map[string]any{
		"teams": []any{map[string]any{"id": "team-abc", "name": "ml"}},
	})
	m.SetRoute("POST", "/v1/teams/team-abc/loops/sessions", 200,
		map[string]any{"session": map[string]any{"id": "sess-1"}})
	m.SetRoute("POST", "/v1/teams/team-abc/loops/runs", 200,
		map[string]any{"run": loopsRunFixture("r-1", "ACTIVE", nil)})

	h.Require.NoError(h.Execute("loops", "run", "create",
		"--base-model", "Qwen/Qwen3-8B", "--team", "ml"))
	h.Require.NotNil(m.FindCall("POST", "/v1/teams/team-abc/loops/sessions"))
	h.Require.NotNil(m.FindCall("POST", "/v1/teams/team-abc/loops/runs"))
	h.Require.Nil(m.FindCall("POST", "/v1/loops/runs"))
}

func Test_Loops_Run_Create_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/loops/sessions", 200, map[string]any{"session": map[string]any{"id": "sess-1"}})
	m.SetRoute("POST", "/v1/loops/runs", 200,
		map[string]any{"run": loopsRunFixture("r-1", "ACTIVE", loopsSamplerFixture("s-1", "ACTIVE", "H100", 8, 1, "2026-05-14T12:00:00Z"))})

	h.Require.NoError(h.Execute("loops", "run", "create",
		"--base-model", "Qwen/Qwen3-8B", "--output", "json"))
	out := h.Stdout.String()
	h.Require.Contains(out, `"id": "r-1"`)
	h.Require.Contains(out, `"sampler"`)
}

func Test_Loops_Run_Create_ReplicasNotPositiveRejected(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("loops", "run", "create", "--base-model", "Qwen/Qwen3-8B", "--replicas", "0")
	h.Require.ErrorContains(err, "--replicas must be a positive number")
	h.Require.Empty(m.Calls())
}

func Test_Loops_Run_Create_MissingBaseModel(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("loops", "run", "create")
	h.Require.ErrorContains(err, "base-model")
}

func Test_Loops_Run_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/loops/runs", 200, map[string]any{"runs": []any{}})

	h.Require.NoError(h.Execute("loops", "run", "list"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No active Loops runs found. Pass --all to include inactive runs.")
}

func Test_Loops_Run_List_Empty_All(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/loops/runs", 200, map[string]any{"runs": []any{}})

	h.Require.NoError(h.Execute("loops", "run", "list", "--all"))
	h.Require.Contains(h.Stderr.String(), "No Loops runs found.")
	h.Require.NotContains(h.Stderr.String(), "--all")
}

func Test_Loops_Run_List_HidesInactiveByDefault(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/loops/runs", 200, map[string]any{"runs": []any{
		loopsRunFixture("r-1", "ACTIVE", nil),
		loopsRunFixture("r-2", "INACTIVE", nil),
	}})

	h.Require.NoError(h.Execute("loops", "run", "list"))
	out := h.Stdout.String()
	h.Require.Contains(out, "BASE MODEL")
	h.Require.NotContains(out, "OWNER")
	h.Require.Contains(out, "r-1")
	h.Require.NotContains(out, "r-2")
}

func Test_Loops_Run_List_All(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/loops/runs", 200, map[string]any{"runs": []any{
		loopsRunFixture("r-1", "ACTIVE", nil),
		loopsRunFixture("r-2", "INACTIVE", nil),
	}})

	h.Require.NoError(h.Execute("loops", "run", "list", "--all"))
	out := h.Stdout.String()
	h.Require.Contains(out, "r-1")
	h.Require.Contains(out, "r-2")
	h.Require.Contains(out, "INACTIVE")
}

func Test_Loops_Run_List_Org(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/loops/runs", 200, map[string]any{"runs": []any{
		loopsRunFixture("r-1", "ACTIVE", nil),
	}})

	h.Require.NoError(h.Execute("loops", "run", "list", "--org"))
	h.Require.Equal("org", m.FindCall("GET", "/v1/loops/runs").Query().Get("scope"))
	out := h.Stdout.String()
	h.Require.Contains(out, "OWNER")
	h.Require.Contains(out, "owner@example.com")
}

func Test_Loops_Run_List_BaseModelFilter(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/loops/runs", 200, map[string]any{"runs": []any{}})

	h.Require.NoError(h.Execute("loops", "run", "list", "--base-model", "Qwen/Qwen3-8B"))
	q := m.FindCall("GET", "/v1/loops/runs").Query()
	h.Require.Equal("Qwen/Qwen3-8B", q.Get("base_model"))
	_, hasScope := q["scope"]
	h.Require.False(hasScope)
}

func Test_Loops_Run_List_Direction(t *testing.T) {
	older := loopsRunFixture("r-old", "ACTIVE", nil)
	older["created_at"] = "2026-05-01T12:00:00Z"
	newer := loopsRunFixture("r-new", "ACTIVE", nil)
	newer["created_at"] = "2026-05-14T12:00:00Z"

	for _, tc := range []struct{ direction, first, second string }{
		{"desc", "r-new", "r-old"},
		{"asc", "r-old", "r-new"},
	} {
		h := NewCommandHarness(t)
		// Served oldest-first so the default desc ordering is a real re-sort.
		h.MockManagementAPI().SetRoute("GET", "/v1/loops/runs", 200,
			map[string]any{"runs": []any{older, newer}})

		h.Require.NoError(h.Execute("loops", "run", "list", "--direction", tc.direction))
		out := h.Stdout.String()
		h.Require.Less(strings.Index(out, tc.first), strings.Index(out, tc.second),
			"--direction %s should list %s before %s", tc.direction, tc.first, tc.second)
	}
}

func Test_Loops_Run_List_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/loops/runs", 200, map[string]any{"runs": []any{
		loopsRunFixture("r-1", "ACTIVE", nil),
		loopsRunFixture("r-2", "INACTIVE", nil),
	}})

	h.Require.NoError(h.Execute("loops", "run", "list", "--output", "json"))
	out := h.Stdout.String()
	h.Require.Contains(out, `"runs"`)
	h.Require.Contains(out, `"id": "r-1"`)
	// The default filtering applies to the JSON output too.
	h.Require.NotContains(out, `"id": "r-2"`)
}

func Test_Loops_Run_Describe(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", loopsRunPath, 200, map[string]any{
		"run": loopsRunFixture("r-1", "ACTIVE",
			loopsSamplerFixture("s-1", "ACTIVE", "H100", 8, 1, "2026-05-14T12:00:00Z")),
	})

	h.Require.NoError(h.Execute("loops", "run", "describe", "--run-id", "r-1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "r-1")
	h.Require.Contains(out, "Qwen/Qwen3-8B")
	h.Require.Contains(out, "owner@example.com")
	h.Require.Contains(out, "sess-1")
	h.Require.Contains(out, "dep-1")
	h.Require.Contains(out, "Sampler")
	h.Require.Contains(out, "s-1")
	h.Require.Contains(out, "sdep-1")
	h.Require.Contains(out, "model-1")
}

func Test_Loops_Run_Describe_NoSampler(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", loopsRunPath, 200,
		map[string]any{"run": loopsRunFixture("r-1", "ACTIVE", nil)})

	h.Require.NoError(h.Execute("loops", "run", "describe", "--run-id", "r-1"))
	h.Require.NotContains(h.Stdout.String(), "Sampler")
}

func Test_Loops_Run_Describe_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", loopsRunPath, 200,
		map[string]any{"run": loopsRunFixture("r-1", "ACTIVE", nil)})

	h.Require.NoError(h.Execute("loops", "run", "describe", "--run-id", "r-1", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"id": "r-1"`)
}

func Test_Loops_Run_Describe_MissingRunID(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("loops", "run", "describe")
	h.Require.ErrorContains(err, "run-id")
}

func Test_Loops_Run_Deactivate_Yes(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/loops/runs/r-1/deactivate", 200, map[string]any{
		"id": "r-1", "base_model": "Qwen/Qwen3-8B",
		"user": map[string]any{"email": "owner@example.com"},
	})

	h.Require.NoError(h.Execute("loops", "run", "deactivate", "--run-id", "r-1", "--yes"))
	h.Require.NotNil(m.FindCall("POST", "/v1/loops/runs/r-1/deactivate"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Deactivated Loops run r-1")
}

func Test_Loops_Run_Deactivate_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("POST", "/v1/loops/runs/r-1/deactivate", 200, map[string]any{
		"id": "r-1", "base_model": "Qwen/Qwen3-8B",
		"user": map[string]any{"email": "owner@example.com"},
	})

	h.Require.NoError(h.Execute("loops", "run", "deactivate",
		"--run-id", "r-1", "--yes", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"base_model": "Qwen/Qwen3-8B"`)
}

func Test_Loops_Run_Deactivate_NoTTY_RequiresYes(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("loops", "run", "deactivate", "--run-id", "r-1")
	h.Require.ErrorContains(err, "stdin is not a terminal")
	h.Require.Nil(m.FindCall("POST", "/v1/loops/runs/r-1/deactivate"))
}

func Test_Loops_Run_Logs_Trainer(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", loopsRunPath, 200,
		map[string]any{"run": loopsRunFixture("r-1", "ACTIVE", nil)})
	logAt := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	m.SetRoute("GET", loopsTrainerLogsPath, 200, logsResponse(
		map[string]any{"timestamp": strconv.FormatInt(logAt.UnixNano(), 10), "message": "step 1", "replica": "g0"},
	))

	h.Require.NoError(h.Execute("loops", "run", "logs", "--run-id", "r-1", "--min-level", "info"))
	h.Require.Contains(h.Stdout.String(), "(g0) step 1")
	// The trainer logs endpoint is always queried newest-first so paging works.
	q := m.FindCall("GET", loopsTrainerLogsPath).Query()
	h.Require.Equal("desc", q.Get("direction"))
	h.Require.Equal("INFO", q.Get("min_level"))
	h.Require.Nil(m.FindCall("GET", loopsSamplerLogsPath))
}

func Test_Loops_Run_Logs_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", loopsRunPath, 200,
		map[string]any{"run": loopsRunFixture("r-1", "ACTIVE", nil)})
	m.SetRoute("GET", loopsTrainerLogsPath, 200, logsResponse())

	h.Require.NoError(h.Execute("loops", "run", "logs", "--run-id", "r-1"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No logs found.")
}

func Test_Loops_Run_Logs_EmptyJSON(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", loopsRunPath, 200,
		map[string]any{"run": loopsRunFixture("r-1", "ACTIVE", nil)})
	m.SetRoute("GET", loopsTrainerLogsPath, 200, logsResponse())

	// The empty array is already unambiguous, so the note is suppressed the same
	// way the list commands suppress theirs.
	h.Require.NoError(h.Execute("loops", "run", "logs", "--run-id", "r-1", "--output", "json"))
	h.Require.NotContains(h.Stderr.String(), "No logs found.")
}

func Test_Loops_Run_Logs_Sampler(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", loopsRunPath, 200, map[string]any{
		"run": loopsRunFixture("r-1", "ACTIVE",
			loopsSamplerFixture("s-1", "ACTIVE", "H100", 8, 1, "2026-05-14T12:00:00Z")),
	})
	m.SetRoute("GET", loopsSamplerLogsPath, 200, logsResponse(
		map[string]any{"timestamp": "1", "message": "serving", "replica": nil},
	))

	h.Require.NoError(h.Execute("loops", "run", "logs", "--run-id", "r-1", "--sampler"))
	h.Require.Contains(h.Stdout.String(), "serving")
	h.Require.NotNil(m.FindCall("GET", loopsSamplerLogsPath))
	h.Require.Nil(m.FindCall("GET", loopsTrainerLogsPath))
}

func Test_Loops_Run_Logs_Sampler_MissingSampler(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", loopsRunPath, 200,
		map[string]any{"run": loopsRunFixture("r-1", "ACTIVE", nil)})

	err := h.Execute("loops", "run", "logs", "--run-id", "r-1", "--sampler")
	h.Require.ErrorContains(err, "has no paired sampler")
	h.Require.Nil(m.FindCall("GET", loopsSamplerLogsPath))
}

func Test_Loops_Run_Logs_Tail_StopsOnTerminalTrainerStatus(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", loopsRunPath, 200,
		map[string]any{"run": loopsRunFixture("r-1", "ACTIVE", nil)})
	m.SetRoute("GET", loopsTrainerLogsPath, 200, logsResponse(
		map[string]any{"timestamp": "1", "message": "step 1", "replica": nil},
	))
	m.SetRoute("GET", loopsTrainerPath, 200, map[string]any{
		"deployment": loopsDeploymentFixture("dep-1", "STOPPED", "H100", 8, 1, "2026-05-14T12:00:00Z", nil),
	})
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })

	h.Require.NoError(h.Execute("loops", "run", "logs", "--run-id", "r-1", "--tail"))
	h.Require.Contains(h.Stdout.String(), "step 1")
	h.Require.Contains(h.Stderr.String(), "Tailing stopped: deployment status STOPPED")
}

func Test_Loops_Run_Logs_Tail_StopsOnTerminalTrainerStatusWithoutLogs(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", loopsRunPath, 200,
		map[string]any{"run": loopsRunFixture("r-1", "ACTIVE", nil)})
	// A trainer that stopped before the log window opened returns nothing, so
	// there is no log line to trigger the status check that ends the tail.
	m.SetRoute("GET", loopsTrainerLogsPath, 200, logsResponse())
	m.SetRoute("GET", loopsTrainerPath, 200, map[string]any{
		"deployment": loopsDeploymentFixture("dep-1", "STOPPED", "H100", 8, 1, "2026-05-14T12:00:00Z", nil),
	})
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })

	h.Require.NoError(h.Execute("loops", "run", "logs", "--run-id", "r-1", "--tail"))
	h.Require.Contains(h.Stderr.String(), "Tailing stopped: deployment status STOPPED")
}

func Test_Loops_Run_Logs_Tail_StopsOnUnknownTrainerStatus(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", loopsRunPath, 200,
		map[string]any{"run": loopsRunFixture("r-1", "ACTIVE", nil)})
	m.SetRoute("GET", loopsTrainerLogsPath, 200, logsResponse(
		map[string]any{"timestamp": "1", "message": "step 1", "replica": nil},
	))
	m.SetRoute("GET", loopsTrainerPath, 200, map[string]any{
		"deployment": loopsDeploymentFixture("dep-1", "SOME_NEW_STATE", "H100", 8, 1, "2026-05-14T12:00:00Z", nil),
	})
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })

	h.Require.NoError(h.Execute("loops", "run", "logs", "--run-id", "r-1", "--tail"))
	h.Require.Contains(h.Stderr.String(), "SOME_NEW_STATE")
}

func Test_Loops_Run_Logs_Tail_KeepsGoingWhileTrainerRuns(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", loopsRunPath, 200,
		map[string]any{"run": loopsRunFixture("r-1", "ACTIVE", nil)})
	logsCalls := 0
	m.SetRouteFunc("GET", loopsTrainerLogsPath, func(w http.ResponseWriter, _ *http.Request) {
		logsCalls++
		payload := logsResponse(map[string]any{"timestamp": "1", "message": "step 1", "replica": nil})
		if logsCalls > 1 {
			payload = logsResponse(
				map[string]any{"timestamp": "1", "message": "step 1", "replica": nil},
				map[string]any{"timestamp": "2", "message": "step 2", "replica": nil},
			)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
	statusCalls := 0
	m.SetRouteFunc("GET", loopsTrainerPath, func(w http.ResponseWriter, _ *http.Request) {
		statusCalls++
		status := "RUNNING"
		if statusCalls > 1 {
			status = "STOPPED"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deployment": loopsDeploymentFixture("dep-1", status, "H100", 8, 1, "2026-05-14T12:00:00Z", nil),
		})
	})
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })

	h.Require.NoError(h.Execute("loops", "run", "logs", "--run-id", "r-1", "--tail"))
	out := h.Stdout.String()
	// RUNNING keeps the tail alive for a second poll, and the repeated line is
	// not emitted twice.
	h.Require.Equal(1, strings.Count(out, "step 1"))
	h.Require.Contains(out, "step 2")
}

func Test_Loops_Run_Logs_TailWithFilterRejected(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("loops", "run", "logs", "--run-id", "r-1", "--tail", "--min-level", "info")
	h.Require.ErrorContains(err, "--tail cannot be combined")
	// The flags are rejected before the run is resolved, so a flag mistake is not
	// masked by a lookup failure.
	h.Require.Empty(m.Calls())
}

func Test_Loops_Run_Logs_SinceOver7DaysRejectedBeforeLookup(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("loops", "run", "logs", "--run-id", "r-1", "--since", "8d")
	h.Require.ErrorContains(err, "at most 7d")
	h.Require.Empty(m.Calls())
}

func Test_Loops_Usage_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/loops/deployments", 200, map[string]any{"deployments": []any{}})
	m.SetRoute("GET", "/v1/loops/samplers", 200, map[string]any{"samplers": []any{}})

	h.Require.NoError(h.Execute("loops", "usage"))
	h.Require.Contains(h.Stdout.String(), "Trainer GPUs: 0 in use, 0 scaled to zero.")
	h.Require.Contains(h.Stderr.String(), "No Loops trainers or samplers found.")
}

func Test_Loops_Usage_PairedSamplerNotDoubleCounted(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sampler := loopsSamplerFixture("s-1", "ACTIVE", "H100", 2, 1, "2026-05-14T12:00:00Z")
	m.SetRoute("GET", "/v1/loops/deployments", 200, map[string]any{"deployments": []any{
		loopsDeploymentFixture("dep-1", "RUNNING", "H100", 8, 1, "2026-05-14T12:00:00Z", sampler),
	}})
	// The same sampler also comes back from the samplers listing.
	m.SetRoute("GET", "/v1/loops/samplers", 200, map[string]any{"samplers": []any{sampler}})

	h.Require.NoError(h.Execute("loops", "usage", "--output", "json"))
	var result struct {
		Summary struct {
			TrainerInUse int `json:"trainer_in_use"`
			SamplerInUse int `json:"sampler_in_use"`
		} `json:"summary"`
		Rows []map[string]any `json:"rows"`
	}
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Len(result.Rows, 1)
	h.Require.Equal(8, result.Summary.TrainerInUse)
	h.Require.Equal(2, result.Summary.SamplerInUse)
	h.Require.Equal("r-dep-1", result.Rows[0]["run_id"])
}

func Test_Loops_Usage_StandaloneSampler(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/loops/deployments", 200, map[string]any{"deployments": []any{}})
	m.SetRoute("GET", "/v1/loops/samplers", 200, map[string]any{"samplers": []any{
		loopsSamplerFixture("s-1", "ACTIVE", "H100", 2, 1, "2026-05-14T12:00:00Z"),
	}})

	h.Require.NoError(h.Execute("loops", "usage"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Sampler GPUs: 2 in use, 0 scaled to zero.")
	h.Require.Contains(out, "H100 (2 GPUs)")
}

func Test_Loops_Usage_MultiNodeGPUCell(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/loops/deployments", 200, map[string]any{"deployments": []any{
		loopsDeploymentFixture("dep-1", "RUNNING", "H100", 8, 2, "2026-05-14T12:00:00Z", nil),
	}})
	m.SetRoute("GET", "/v1/loops/samplers", 200, map[string]any{"samplers": []any{}})

	h.Require.NoError(h.Execute("loops", "usage"))
	out := h.Stdout.String()
	h.Require.Contains(out, "H100 (16 GPUs across 2 nodes)")
	h.Require.Contains(out, "Trainer GPUs: 16 in use")
}

func Test_Loops_Usage_HidesIdleRowsButCountsThem(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/loops/deployments", 200, map[string]any{"deployments": []any{
		loopsDeploymentFixture("dep-live", "RUNNING", "H100", 8, 1, "2026-05-14T12:00:00Z", nil),
		loopsDeploymentFixture("dep-idle", "SCALED_TO_ZERO", "H100", 8, 1, "2026-05-13T12:00:00Z", nil),
		loopsDeploymentFixture("dep-dead", "FAILED", "H100", 8, 1, "2026-05-12T12:00:00Z", nil),
	}})
	m.SetRoute("GET", "/v1/loops/samplers", 200, map[string]any{"samplers": []any{}})

	h.Require.NoError(h.Execute("loops", "usage"))
	out := h.Stdout.String()
	// The summary counts live and idle GPUs; the terminal row counts in neither.
	h.Require.Contains(out, "Trainer GPUs: 8 in use, 8 scaled to zero.")
	h.Require.Contains(out, "r-dep-live")
	h.Require.NotContains(out, "r-dep-idle")
	h.Require.NotContains(out, "r-dep-dead")
	h.Require.Contains(h.Stderr.String(), "2 allocation(s) holding no live GPUs hidden")
}

func Test_Loops_Usage_All(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/loops/deployments", 200, map[string]any{"deployments": []any{
		loopsDeploymentFixture("dep-live", "RUNNING", "H100", 8, 1, "2026-05-14T12:00:00Z", nil),
		loopsDeploymentFixture("dep-idle", "SCALED_TO_ZERO", "H100", 8, 1, "2026-05-13T12:00:00Z", nil),
	}})
	m.SetRoute("GET", "/v1/loops/samplers", 200, map[string]any{"samplers": []any{}})

	h.Require.NoError(h.Execute("loops", "usage", "--all"))
	out := h.Stdout.String()
	h.Require.Contains(out, "r-dep-live")
	h.Require.Contains(out, "r-dep-idle")
	// Rows are newest-first.
	h.Require.Less(strings.Index(out, "r-dep-live"), strings.Index(out, "r-dep-idle"))
	h.Require.NotContains(h.Stderr.String(), "hidden")
}

func Test_Loops_Usage_ScaledToZeroSampler(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sampler := loopsSamplerFixture("s-1", "SCALED_TO_ZERO", "H100", 2, 1, "2026-05-14T12:00:00Z")
	m.SetRoute("GET", "/v1/loops/deployments", 200, map[string]any{"deployments": []any{
		loopsDeploymentFixture("dep-1", "SCALED_TO_ZERO", "H100", 8, 1, "2026-05-14T12:00:00Z", sampler),
	}})
	m.SetRoute("GET", "/v1/loops/samplers", 200, map[string]any{"samplers": []any{sampler}})

	h.Require.NoError(h.Execute("loops", "usage"))
	h.Require.Contains(h.Stdout.String(),
		"Trainer GPUs: 0 in use, 8 scaled to zero. Sampler GPUs: 0 in use, 2 scaled to zero.")
	// Neither half holds live GPUs, so the row is hidden by default.
	h.Require.Contains(h.Stderr.String(), "1 allocation(s) holding no live GPUs hidden")
}

func Test_Loops_Usage_IdleTrainerFallsBackToLatestRun(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	idle := loopsDeploymentFixture("dep-1", "SCALED_TO_ZERO", "H100", 8, 1, "2026-05-14T12:00:00Z", nil)
	idle["active_run_id"] = nil
	idle["latest_run_id"] = "r-latest"
	m.SetRoute("GET", "/v1/loops/deployments", 200, map[string]any{"deployments": []any{idle}})
	m.SetRoute("GET", "/v1/loops/samplers", 200, map[string]any{"samplers": []any{}})

	h.Require.NoError(h.Execute("loops", "usage", "--all"))
	h.Require.Contains(h.Stdout.String(), "r-latest")
}

func Test_Loops_Usage_Org(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/loops/deployments", 200, map[string]any{"deployments": []any{
		loopsDeploymentFixture("dep-1", "RUNNING", "H100", 8, 1, "2026-05-14T12:00:00Z", nil),
	}})
	m.SetRoute("GET", "/v1/loops/samplers", 200, map[string]any{"samplers": []any{}})

	h.Require.NoError(h.Execute("loops", "usage", "--org"))
	h.Require.Equal("org", m.FindCall("GET", "/v1/loops/deployments").Query().Get("scope"))
	h.Require.Equal("org", m.FindCall("GET", "/v1/loops/samplers").Query().Get("scope"))
	out := h.Stdout.String()
	h.Require.Contains(out, "OWNER")
	h.Require.Contains(out, "owner@example.com")
}

func Test_Loops_Usage_UserImpliesOrg(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	other := loopsDeploymentFixture("dep-2", "RUNNING", "H100", 8, 1, "2026-05-13T12:00:00Z", nil)
	other["user"] = map[string]any{"email": "someone@example.com"}
	m.SetRoute("GET", "/v1/loops/deployments", 200, map[string]any{"deployments": []any{
		loopsDeploymentFixture("dep-1", "RUNNING", "H100", 8, 1, "2026-05-14T12:00:00Z", nil),
		other,
	}})
	m.SetRoute("GET", "/v1/loops/samplers", 200, map[string]any{"samplers": []any{}})

	h.Require.NoError(h.Execute("loops", "usage", "--user", "someone@example.com"))
	h.Require.Equal("org", m.FindCall("GET", "/v1/loops/deployments").Query().Get("scope"))
	out := h.Stdout.String()
	h.Require.Contains(out, "OWNER")
	h.Require.Contains(out, "r-dep-2")
	h.Require.NotContains(out, "r-dep-1")
	// Only the matching owner's GPUs are summarized.
	h.Require.Contains(out, "Trainer GPUs: 8 in use")
}

func Test_Loops_Usage_FilteredTrainersSamplerNotStandalone(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	sampler := loopsSamplerFixture("s-1", "ACTIVE", "H100", 2, 1, "2026-05-14T12:00:00Z")
	other := loopsDeploymentFixture("dep-2", "RUNNING", "H100", 8, 1, "2026-05-13T12:00:00Z", sampler)
	other["user"] = map[string]any{"email": "someone@example.com"}
	m.SetRoute("GET", "/v1/loops/deployments", 200, map[string]any{"deployments": []any{other}})
	m.SetRoute("GET", "/v1/loops/samplers", 200, map[string]any{"samplers": []any{sampler}})

	h.Require.NoError(h.Execute("loops", "usage", "--user", "nobody@example.com"))
	// The owner filter drops the trainer row; its sampler must not resurface as a
	// standalone row.
	h.Require.Contains(h.Stdout.String(), "Sampler GPUs: 0 in use")
	h.Require.Contains(h.Stderr.String(), "No Loops trainers or samplers found.")
}

func Test_Loops_Usage_SamplerWithoutInstanceType(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/loops/deployments", 200, map[string]any{"deployments": []any{}})
	m.SetRoute("GET", "/v1/loops/samplers", 200, map[string]any{"samplers": []any{
		loopsSamplerFixture("s-1", "BUILDING", "", 0, 1, "2026-05-14T12:00:00Z"),
	}})

	h.Require.NoError(h.Execute("loops", "usage"))
	out := h.Stdout.String()
	// A sampler still coming up reports no instance type, so it contributes no
	// GPUs but is still a live row.
	h.Require.Contains(out, "Sampler GPUs: 0 in use, 0 scaled to zero.")
	h.Require.Contains(out, "BUILDING")
}

func Test_Loops_Checkpoint_List_ByRun(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/loops/checkpoints", 200, map[string]any{"checkpoints": []any{
		loopsCheckpointFixture("cp-1", "r-1", "sampler", "2026-05-14T12:00:00Z"),
	}})

	h.Require.NoError(h.Execute("loops", "checkpoint", "list", "--run-id", "r-1"))
	q := m.FindCall("GET", "/v1/loops/checkpoints").Query()
	h.Require.Equal("r-1", q.Get("run_id"))
	_, hasBaseModel := q["base_model"]
	h.Require.False(hasBaseModel)
	out := h.Stdout.String()
	h.Require.Contains(out, "cp-1")
	h.Require.Contains(out, "checkpoint-cp-1")
	h.Require.Contains(out, "sampler")
}

func Test_Loops_Checkpoint_List_ByBaseModel(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/loops/checkpoints", 200, map[string]any{"checkpoints": []any{}})

	h.Require.NoError(h.Execute("loops", "checkpoint", "list", "--base-model", "Qwen/Qwen3-8B"))
	q := m.FindCall("GET", "/v1/loops/checkpoints").Query()
	h.Require.Equal("Qwen/Qwen3-8B", q.Get("base_model"))
	_, hasRunID := q["run_id"]
	h.Require.False(hasRunID)
	h.Require.Contains(h.Stderr.String(), "No Loops checkpoints found.")
}

func Test_Loops_Checkpoint_List_RunAndBaseModelRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("loops", "checkpoint", "list",
		"--run-id", "r-1", "--base-model", "Qwen/Qwen3-8B")
	h.Require.Error(err)
}

func Test_Loops_Checkpoint_List_ScopeRequired(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("loops", "checkpoint", "list")
	h.Require.Error(err)
}

func Test_Loops_Checkpoint_List_Direction(t *testing.T) {
	// The server returns newest-first, so only asc re-sorts.
	for _, tc := range []struct{ direction, first, second string }{
		{"desc", "cp-new", "cp-old"},
		{"asc", "cp-old", "cp-new"},
	} {
		h := NewCommandHarness(t)
		h.MockManagementAPI().SetRoute("GET", "/v1/loops/checkpoints", 200, map[string]any{
			"checkpoints": []any{
				loopsCheckpointFixture("cp-new", "r-1", "sampler", "2026-05-14T12:00:00Z"),
				loopsCheckpointFixture("cp-old", "r-1", "sampler", "2026-05-01T12:00:00Z"),
			},
		})

		h.Require.NoError(h.Execute("loops", "checkpoint", "list",
			"--run-id", "r-1", "--direction", tc.direction))
		out := h.Stdout.String()
		h.Require.Less(strings.Index(out, tc.first), strings.Index(out, tc.second),
			"--direction %s should list %s before %s", tc.direction, tc.first, tc.second)
	}
}

func Test_Loops_Checkpoint_List_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/loops/checkpoints", 200, map[string]any{
		"checkpoints": []any{loopsCheckpointFixture("cp-1", "r-1", "trainer", "2026-05-14T12:00:00Z")},
	})

	h.Require.NoError(h.Execute("loops", "checkpoint", "list", "--run-id", "r-1", "--output", "json"))
	out := h.Stdout.String()
	h.Require.Contains(out, `"checkpoints"`)
	h.Require.Contains(out, `"id": "cp-1"`)
}

// loopsCheckpointFile is a presigned file payload for `loops checkpoint files`.
func loopsCheckpointFile(name string) map[string]any {
	return map[string]any{
		"last_modified":      "2026-05-14T12:00:00Z",
		"node_rank":          0,
		"relative_file_name": name,
		"size_bytes":         1024,
		"url":                "https://files.example.com/" + name,
	}
}

// serveCheckpointFilePages serves one page per entry in pages, keyed off the
// page_token offset, handing out a next_page_token for every page but the last.
func serveCheckpointFilePages(m *MockManagementAPI, pages [][]map[string]any, tokens *[]string) {
	m.SetRouteFunc("GET", "/v1/loops/checkpoints/cp-1/files", func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("page_token")
		*tokens = append(*tokens, token)
		index := 0
		if token != "" {
			index, _ = strconv.Atoi(token)
		}
		payload := map[string]any{"presigned_urls": []map[string]any{}, "total_count": 0}
		if index < len(pages) {
			payload["presigned_urls"] = pages[index]
			payload["total_count"] = len(pages[index])
		}
		if index+1 < len(pages) {
			payload["next_page_token"] = index + 1
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
}

func Test_Loops_Checkpoint_Files_SinglePage(t *testing.T) {
	h := NewCommandHarness(t)
	var tokens []string
	serveCheckpointFilePages(h.MockManagementAPI(),
		[][]map[string]any{{loopsCheckpointFile("model.safetensors")}}, &tokens)

	h.Require.NoError(h.Execute("loops", "checkpoint", "files", "--checkpoint-id", "cp-1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "NAME")
	h.Require.Contains(out, "model.safetensors")
	h.Require.Contains(out, "https://files.example.com/model.safetensors")
	h.Require.Equal([]string{""}, tokens)
}

func Test_Loops_Checkpoint_Files_PagesThroughEveryPage(t *testing.T) {
	h := NewCommandHarness(t)
	var tokens []string
	serveCheckpointFilePages(h.MockManagementAPI(), [][]map[string]any{
		{loopsCheckpointFile("a.bin")},
		{loopsCheckpointFile("b.bin")},
		{loopsCheckpointFile("c.bin")},
	}, &tokens)

	h.Require.NoError(h.Execute("loops", "checkpoint", "files", "--checkpoint-id", "cp-1"))
	out := h.Stdout.String()
	for _, name := range []string{"a.bin", "b.bin", "c.bin"} {
		h.Require.Contains(out, name)
	}
	// The first request sends no token; each later one sends the previous page's.
	h.Require.Equal([]string{"", "1", "2"}, tokens)
}

func Test_Loops_Checkpoint_Files_EmptyPageEndsWalk(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	// A server that hands back a token forever must not spin: an empty page ends
	// the walk.
	calls := 0
	m.SetRouteFunc("GET", "/v1/loops/checkpoints/cp-1/files", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"presigned_urls":  []map[string]any{},
			"next_page_token": 1,
			"total_count":     0,
		})
	})

	h.Require.NoError(h.Execute("loops", "checkpoint", "files", "--checkpoint-id", "cp-1"))
	h.Require.Equal(1, calls)
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No files found for this Loops checkpoint.")
}

func Test_Loops_Checkpoint_Files_JSONStreamsAcrossPages(t *testing.T) {
	h := NewCommandHarness(t)
	var tokens []string
	serveCheckpointFilePages(h.MockManagementAPI(), [][]map[string]any{
		{loopsCheckpointFile("a.bin")},
		{loopsCheckpointFile("b.bin")},
	}, &tokens)

	h.Require.NoError(h.Execute("loops", "checkpoint", "files",
		"--checkpoint-id", "cp-1", "--output", "json"))
	out := strings.TrimSpace(h.Stdout.String())
	// One flat JSON array of files across both pages, with no response envelope.
	h.Require.True(strings.HasPrefix(out, "["))
	h.Require.True(strings.HasSuffix(out, "]"))
	h.Require.NotContains(out, "presigned_urls")
	h.Require.NotContains(out, "total_count")
	var files []map[string]any
	h.Require.NoError(json.Unmarshal([]byte(out), &files))
	h.Require.Len(files, 2)
	h.Require.Equal("a.bin", files[0]["relative_file_name"])
	h.Require.Equal("b.bin", files[1]["relative_file_name"])
}

func Test_Loops_Checkpoint_Files_JSONL(t *testing.T) {
	h := NewCommandHarness(t)
	var tokens []string
	serveCheckpointFilePages(h.MockManagementAPI(), [][]map[string]any{
		{loopsCheckpointFile("a.bin")},
		{loopsCheckpointFile("b.bin")},
	}, &tokens)

	h.Require.NoError(h.Execute("loops", "checkpoint", "files",
		"--checkpoint-id", "cp-1", "--output", "jsonl"))
	lines := strings.Split(strings.TrimRight(h.Stdout.String(), "\n"), "\n")
	h.Require.Len(lines, 2)
	for _, line := range lines {
		var v map[string]any
		h.Require.NoError(json.Unmarshal([]byte(line), &v))
	}
}

func Test_Loops_Checkpoint_Files_MissingCheckpointID(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("loops", "checkpoint", "files")
	h.Require.ErrorContains(err, "checkpoint-id")
}

func Test_Loops_Checkpoint_Deploy_ForwardsFlags(t *testing.T) {
	h, fake := newTrussHarness(t)

	h.Require.NoError(h.Execute("loops", "checkpoint", "deploy",
		"--run-id", "run-1", "--checkpoint", "step-50", "--checkpoint", "step-100", "--dry-run"))

	c := fake.only(t)
	h.Require.Equal([]string{
		"uv", "tool", "run", "truss@latest", "loops", "checkpoints", "deploy",
		"--run-id", "run-1",
		"--checkpoints", "step-50,step-100",
		"--dry-run",
	}, c.Args)
	h.Require.Contains(c.Env, "BASETEN_TRUSS_AUTH_API_KEY=test-key")
}

func Test_Loops_Checkpoint_Deploy_CheckpointIDs(t *testing.T) {
	h, fake := newTrussHarness(t)

	h.Require.NoError(h.Execute("loops", "checkpoint", "deploy",
		"--checkpoint-id", "cp-1", "--checkpoint-id", "cp-2"))

	h.Require.Equal([]string{
		"uv", "tool", "run", "truss@latest", "loops", "checkpoints", "deploy",
		"--checkpoint-ids", "cp-1,cp-2",
	}, fake.only(t).Args)
}
