package cmd_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	e2eAPIKey  = os.Getenv("BASETEN_E2E_TEST_API_KEY")
	e2eDomain  = os.Getenv("BASETEN_E2E_TEST_DOMAIN")
	e2eModelID = os.Getenv("BASETEN_E2E_TEST_MODEL_ID")
)

func skipOrFailE2E(t *testing.T) {
	t.Helper()
	if e2eAPIKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	require.NotEmpty(t, e2eDomain, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_DOMAIN is missing")
	require.NotEmpty(t, e2eModelID, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_MODEL_ID is missing")
}

func newE2EHarness(t *testing.T) *CommandHarness {
	t.Helper()
	h := &CommandHarness{T: t, Require: require.New(t), Context: t.Context()}
	t.Setenv("BASETEN_API_KEY", e2eAPIKey)
	t.Setenv("BASETEN_BASE_URL", fmt.Sprintf("https://api.%s", e2eDomain))
	return h
}

func Test_E2E_API_Management_ListModels(t *testing.T) {
	skipOrFailE2E(t)
	h := newE2EHarness(t)
	err := h.Execute("api", "management", "models")
	h.Require.NoError(err)

	var resp map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &resp))
	models := resp["models"].([]any)
	found := false
	for _, m := range models {
		if m.(map[string]any)["id"] == e2eModelID {
			found = true
			break
		}
	}
	h.Require.True(found, "model %s not found in list", e2eModelID)
}

func Test_E2E_API_Management_GetModel(t *testing.T) {
	skipOrFailE2E(t)
	h := newE2EHarness(t)
	err := h.Execute("api", "management", fmt.Sprintf("models/%s", e2eModelID))
	h.Require.NoError(err)

	var resp map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &resp))
	h.Require.Equal(e2eModelID, resp["id"])
}

func Test_E2E_API_Management_GetModelNotFound(t *testing.T) {
	skipOrFailE2E(t)
	h := newE2EHarness(t)
	_ = h.Execute("api", "management", "models/nonexistent-model-id")
	h.Require.True(h.Exited())
	h.Require.Contains(h.Stderr.String(), "status 404")
}

func Test_E2E_API_Management_ListDeployments(t *testing.T) {
	skipOrFailE2E(t)
	h := newE2EHarness(t)
	err := h.Execute("api", "management", fmt.Sprintf("models/%s/deployments", e2eModelID))
	h.Require.NoError(err)

	var resp map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &resp))
	deployments := resp["deployments"].([]any)
	h.Require.NotEmpty(deployments)
}

func Test_E2E_API_Management_JQFilter(t *testing.T) {
	skipOrFailE2E(t)
	h := newE2EHarness(t)
	err := h.Execute("api", "management", "--jq", ".id", fmt.Sprintf("models/%s", e2eModelID))
	h.Require.NoError(err)
	h.Require.Equal(fmt.Sprintf("%q\n", e2eModelID), h.Stdout.String())
}

func Test_E2E_API_Inference_Predict(t *testing.T) {
	skipOrFailE2E(t)
	h := newE2EHarness(t)
	t.Setenv("BASETEN_BASE_URL", fmt.Sprintf("https://model-%s.api.%s", e2eModelID, e2eDomain))
	err := h.Execute("api", "inference",
		"-F", `prompt="hello"`,
		"production/predict",
	)
	h.Require.NoError(err)

	var resp map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &resp))
	h.Require.NotEmpty(resp)
}
