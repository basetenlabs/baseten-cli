package cmd_test

import (
	"testing"
)

func Test_Model_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models", 200, map[string]any{"models": []any{}})

	h.Require.NoError(h.Execute("model", "list"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No models found.")
}

func Test_Model_List_Rows(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models", 200, map[string]any{
		"models": []any{
			map[string]any{
				"id":                 "m-1",
				"name":               "alpha",
				"team_name":          "default",
				"deployments_count":  2,
				"created_at":         "2026-01-02T03:04:05Z",
				"instance_type_name": "A10G",
			},
			map[string]any{
				"id":                 "m-2",
				"name":               "beta",
				"team_name":          "ml",
				"deployments_count":  0,
				"created_at":         "2026-02-03T04:05:06Z",
				"instance_type_name": "A100",
			},
		},
	})

	h.Require.NoError(h.Execute("model", "list"))
	out := h.Stdout.String()
	h.Require.Contains(out, "ID")
	h.Require.Contains(out, "NAME")
	h.Require.Contains(out, "TEAM")
	h.Require.Contains(out, "DEPLOYMENTS")
	h.Require.Contains(out, "CREATED")
	h.Require.Contains(out, "m-1")
	h.Require.Contains(out, "alpha")
	h.Require.Contains(out, "beta")
	h.Require.Contains(out, "2026-01-02T03:04:05Z")
}

func Test_Model_List_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models", 200, map[string]any{
		"models": []any{
			map[string]any{
				"id": "m-1", "name": "alpha", "team_name": "default",
				"deployments_count": 1, "created_at": "2026-01-02T03:04:05Z",
				"instance_type_name": "A10G",
			},
		},
	})

	h.Require.NoError(h.Execute("model", "list", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"name": "alpha"`)
}

func Test_Model_List_ScopedByTeam(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/teams", 200, map[string]any{
		"teams": []any{
			map[string]any{"id": "team-abc", "name": "ml"},
		},
	})
	m.SetRoute("GET", "/v1/teams/team-abc/models", 200, map[string]any{
		"models": []any{
			map[string]any{
				"id": "m-1", "name": "alpha", "team_name": "ml",
				"deployments_count": 1, "created_at": "2026-01-02T03:04:05Z",
				"instance_type_name": "A10G",
			},
		},
	})

	h.Require.NoError(h.Execute("model", "list", "--team", "ml"))
	h.Require.NotNil(m.FindCall("GET", "/v1/teams/team-abc/models"))
	h.Require.Contains(h.Stdout.String(), "alpha")
}

func Test_Model_Describe_ByID(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1", 200, map[string]any{
		"id": "m-1", "name": "alpha", "team_name": "default",
		"deployments_count": 1, "created_at": "2026-01-02T03:04:05Z",
		"instance_type_name": "A10G",
	})

	h.Require.NoError(h.Execute("model", "describe", "--model-id", "m-1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "ID:")
	h.Require.Contains(out, "m-1")
	h.Require.Contains(out, "alpha")
}

func Test_Model_Describe_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models", 200, map[string]any{
		"models": []any{
			map[string]any{
				"id": "m-1", "name": "alpha", "team_name": "default",
				"deployments_count": 1, "created_at": "2026-01-02T03:04:05Z",
				"instance_type_name": "A10G",
			},
		},
	})
	m.SetRoute("GET", "/v1/models/m-1", 200, map[string]any{
		"id": "m-1", "name": "alpha", "team_name": "default",
		"deployments_count": 1, "created_at": "2026-01-02T03:04:05Z",
		"instance_type_name": "A10G",
	})

	h.Require.NoError(h.Execute("model", "describe", "--model-name", "alpha"))
	h.Require.NotNil(m.FindCall("GET", "/v1/models/m-1"))
}

func Test_Model_Describe_ByNameAmbiguous(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models", 200, map[string]any{
		"models": []any{
			map[string]any{
				"id": "m-1", "name": "alpha", "team_name": "default",
				"deployments_count": 1, "created_at": "2026-01-02T03:04:05Z",
				"instance_type_name": "A10G",
			},
			map[string]any{
				"id": "m-2", "name": "alpha", "team_name": "ml",
				"deployments_count": 1, "created_at": "2026-01-02T03:04:05Z",
				"instance_type_name": "A10G",
			},
		},
	})

	err := h.Execute("model", "describe", "--model-name", "alpha")
	h.Require.ErrorContains(err, "multiple models named")
	h.Require.ErrorContains(err, "--team")
}

func Test_Model_Describe_ByNameWithTeam(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/teams", 200, map[string]any{
		"teams": []any{
			map[string]any{"id": "team-abc", "name": "ml"},
		},
	})
	m.SetRoute("GET", "/v1/teams/team-abc/models", 200, map[string]any{
		"models": []any{
			map[string]any{
				"id": "m-2", "name": "alpha", "team_name": "ml",
				"deployments_count": 1, "created_at": "2026-01-02T03:04:05Z",
				"instance_type_name": "A10G",
			},
		},
	})
	m.SetRoute("GET", "/v1/models/m-2", 200, map[string]any{
		"id": "m-2", "name": "alpha", "team_name": "ml",
		"deployments_count": 1, "created_at": "2026-01-02T03:04:05Z",
		"instance_type_name": "A10G",
	})

	h.Require.NoError(h.Execute("model", "describe", "--model-name", "alpha", "--team", "ml"))
	h.Require.NotNil(m.FindCall("GET", "/v1/models/m-2"))
}

func Test_Model_Describe_ByNameNotFound(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models", 200, map[string]any{"models": []any{}})

	err := h.Execute("model", "describe", "--model-name", "ghost")
	h.Require.ErrorContains(err, `no model named "ghost"`)
}

func Test_Model_Describe_TeamWithID_Rejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "describe", "--model-id", "m-1", "--team", "ml")
	h.Require.ErrorContains(err, "--team is only valid with --model-name")
}

func Test_Model_Describe_MissingRef(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "describe")
	h.Require.Error(err)
}

func Test_Model_Delete_Yes_ByID(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models/m-1", 200, map[string]any{
		"id": "m-1", "name": "alpha", "team_name": "default",
		"deployments_count": 1, "created_at": "2026-01-02T03:04:05Z",
		"instance_type_name": "A10G",
	})
	m.SetRoute("DELETE", "/v1/models/m-1", 200, map[string]any{
		"id": "m-1", "deleted": true,
	})

	h.Require.NoError(h.Execute("model", "delete", "--model-id", "m-1", "--yes"))
	h.Require.NotNil(m.FindCall("DELETE", "/v1/models/m-1"))
	h.Require.Contains(h.Stderr.String(), "Deleted model alpha (m-1)")
}

func Test_Model_Delete_Yes_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models", 200, map[string]any{
		"models": []any{
			map[string]any{
				"id": "m-1", "name": "alpha", "team_name": "default",
				"deployments_count": 1, "created_at": "2026-01-02T03:04:05Z",
				"instance_type_name": "A10G",
			},
		},
	})
	m.SetRoute("GET", "/v1/models/m-1", 200, map[string]any{
		"id": "m-1", "name": "alpha", "team_name": "default",
		"deployments_count": 1, "created_at": "2026-01-02T03:04:05Z",
		"instance_type_name": "A10G",
	})
	m.SetRoute("DELETE", "/v1/models/m-1", 200, map[string]any{
		"id": "m-1", "deleted": true,
	})

	h.Require.NoError(h.Execute("model", "delete", "--model-name", "alpha", "--yes"))
	h.Require.NotNil(m.FindCall("DELETE", "/v1/models/m-1"))
}

func Test_Model_Delete_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models/m-1", 200, map[string]any{
		"id": "m-1", "name": "alpha", "team_name": "default",
		"deployments_count": 1, "created_at": "2026-01-02T03:04:05Z",
		"instance_type_name": "A10G",
	})
	m.SetRoute("DELETE", "/v1/models/m-1", 200, map[string]any{
		"id": "m-1", "deleted": true,
	})

	h.Require.NoError(h.Execute("model", "delete", "--model-id", "m-1", "--yes", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"deleted": true`)
}

func Test_Model_Delete_NoTTY_RequiresYes(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models/m-1", 200, map[string]any{
		"id": "m-1", "name": "alpha", "team_name": "default",
		"deployments_count": 1, "created_at": "2026-01-02T03:04:05Z",
		"instance_type_name": "A10G",
	})

	err := h.Execute("model", "delete", "--model-id", "m-1")
	h.Require.ErrorContains(err, "stdin is not a terminal")
	h.Require.Nil(m.FindCall("DELETE", "/v1/models/m-1"))
}

func Test_Model_Delete_NotFound(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models", 200, map[string]any{"models": []any{}})

	err := h.Execute("model", "delete", "--model-name", "ghost", "--yes")
	h.Require.ErrorContains(err, `no model named "ghost"`)
}
