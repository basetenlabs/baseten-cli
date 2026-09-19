//go:build e2e

package e2etests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const e2eVolumeSyncSource = "hf://hf-internal-testing/tiny-random-bert"

// TestE2EVolumeSync starts a small anonymous Hugging Face sync and exercises
// the complete CLI surface against the resulting durable job. Cancel is sent
// after the job is READY, which verifies the endpoint's idempotent terminal
// behavior without racing or discarding the artifact needed by the other
// assertions.
//
// The organization behind the e2e key needs volume sync enabled and the key
// needs organization-level model management permission. A missing prerequisite
// fails rather than skips, so a misconfigured environment does not read as a
// pass.
func TestE2EVolumeSync(t *testing.T) {
	apiKey := os.Getenv("BASETEN_E2E_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	remoteURL := os.Getenv("BASETEN_E2E_TEST_REMOTE_URL")
	require.NotEmpty(t, remoteURL,
		"BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_REMOTE_URL is missing")

	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())

	suffix := randomSuffix(t)
	baseRef := "bdn:" + e2eVolumeNamespace + "/sync-" + suffix
	destination := baseRef + ":e2e"
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, errOut, err := cliCtx(t, ctx,
			"volume", "rm", "--recursive", "--yes", baseRef); err != nil {
			t.Logf("cleanup delete of volume %s failed: %v\nstderr: %s", baseRef, err, errOut)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	step(t, "start and wait for anonymous Hugging Face volume sync")
	startOut := mustCLICtx(t, ctx,
		"volume", "sync", "start",
		"--source", e2eVolumeSyncSource,
		"--destination", destination,
		"--include", "config.json",
		"--exclude", "*.md",
		"--wait",
		"--output", "json")
	started := parseVolumeSync(t, startOut)
	require.Equal(t, "READY", started.Status)
	require.Equal(t, e2eVolumeSyncSource, started.Source.URI)
	require.Equal(t, []string{"config.json"}, started.Source.Include)
	require.Equal(t, []string{"*.md"}, started.Source.Exclude)
	require.Equal(t, destination, started.Destination.Ref)
	require.NotEmpty(t, started.SyncID)
	require.NotNil(t, started.VolumeVersionID)
	require.NotEmpty(t, *started.VolumeVersionID)
	require.NotNil(t, started.VersionRef)
	require.NotEmpty(t, *started.VersionRef)
	require.NotNil(t, started.ContentDigest)
	require.Regexp(t, `^b3:[0-9a-f]{64}$`, *started.ContentDigest)
	require.NotNil(t, started.TotalSizeBytes)
	require.Positive(t, *started.TotalSizeBytes)
	require.NotNil(t, started.CompletedAt)
	require.Nil(t, started.Error)
	versionRef := *started.VersionRef

	step(t, "describe completed volume sync")
	described := parseVolumeSync(t, mustCLI(t,
		"volume", "sync", "describe",
		"--volume-sync-id", started.SyncID,
		"--output", "json"))
	require.Equal(t, started.SyncID, described.SyncID)
	require.Equal(t, "READY", described.Status)
	require.Equal(t, started.VersionRef, described.VersionRef)

	step(t, "find completed volume sync by exact destination")
	listOut := mustCLI(t,
		"volume", "sync", "list",
		"--destination", destination,
		"--output", "json")
	var listed struct {
		Items []e2eVolumeSync `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(listOut), &listed))
	require.NotEmpty(t, listed.Items)
	found := false
	for _, item := range listed.Items {
		if item.SyncID == started.SyncID {
			require.Equal(t, "READY", item.Status)
			require.Equal(t, destination, item.Destination.Ref)
			found = true
			break
		}
	}
	require.True(t, found, "sync %q not in destination-filtered listing", started.SyncID)

	step(t, "read synced artifact through volume commands")
	statOut := mustCLI(t, "volume", "stat", versionRef, "--output", "json")
	var stat struct {
		VersionRef    string `json:"version_ref"`
		ContentDigest string `json:"digest"`
	}
	require.NoError(t, json.Unmarshal([]byte(statOut), &stat))
	require.Equal(t, versionRef, stat.VersionRef)
	require.Equal(t, *started.ContentDigest, stat.ContentDigest)

	entries := parseVolumeEntryList(t, mustCLI(t,
		"volume", "ls", "--recursive", versionRef, "--output", "json"))
	require.Equal(t, versionRef, entries.VersionRef)
	require.Equal(t, "file", volumeEntryKinds(entries)["/config.json"])

	configOut := mustCLI(t, "volume", "cat", versionRef+"/config.json")
	var sourceConfig map[string]any
	require.NoError(t, json.Unmarshal([]byte(configOut), &sourceConfig))
	require.NotEmpty(t, sourceConfig["model_type"])

	pullDir := t.TempDir()
	pullOut := mustCLI(t, "volume", "pull", versionRef, pullDir, "--output", "json")
	var pulled volumePullResult
	require.NoError(t, json.Unmarshal([]byte(pullOut), &pulled))
	require.Equal(t, versionRef, pulled.VersionRef)
	require.Equal(t, int64(1), pulled.Files)
	pulledConfig, err := os.ReadFile(filepath.Join(pullDir, "config.json"))
	require.NoError(t, err)
	require.JSONEq(t, configOut, string(pulledConfig))

	versionsOut := mustCLI(t, "volume", "versions", baseRef, "--output", "json")
	var versions struct {
		Versions []struct {
			VersionRef string `json:"version_ref"`
		} `json:"versions"`
	}
	require.NoError(t, json.Unmarshal([]byte(versionsOut), &versions))
	versionFound := false
	for _, version := range versions.Versions {
		if version.VersionRef == versionRef {
			versionFound = true
			break
		}
	}
	require.True(t, versionFound, "version %q not in volume history", versionRef)

	step(t, "deploy model with synced immutable BDN volume mounted")
	modelName := "cli-e2e-volume-sync-" + suffix
	modelDir := writeVolumeSyncModel(t, modelName, versionRef)
	modelID := ""
	t.Cleanup(func() {
		dumpModelLogsIfFailure(t, modelName)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if modelID == "" {
			modelID = lookupModelIDByName(t, ctx, modelName)
		}
		if modelID == "" {
			return
		}
		if _, errOut, err := cliCtx(t, ctx,
			"model", "delete", "--model-id", modelID, "--yes"); err != nil {
			t.Logf("cleanup delete of model %s failed: %v\nstderr: %s", modelID, err, errOut)
		}
	})

	pushCtx, pushCancel := context.WithTimeout(t.Context(), pushCLITimeout)
	defer pushCancel()
	pushOut := mustCLICtx(t, pushCtx,
		"model", "push",
		"--dir", modelDir,
		"--environment", "production",
		"--wait",
		"--output", "json")
	var deployment pushedDeployment
	require.NoError(t, json.Unmarshal([]byte(pushOut), &deployment))
	require.Equal(t, modelName, deployment.Model.Name)
	require.Equal(t, "ACTIVE", deployment.Deployment.Status)
	modelID = deployment.Model.ID

	step(t, "invoke model and verify synced file is mounted")
	predictOut := mustCLI(t,
		"model", "predict",
		"--model-id", modelID,
		"--data", `{}`,
		"--output", "json")
	var prediction struct {
		Mounted   bool   `json:"mounted"`
		ModelType string `json:"model_type"`
	}
	require.NoError(t, json.Unmarshal([]byte(predictOut), &prediction))
	require.True(t, prediction.Mounted)
	require.Equal(t, sourceConfig["model_type"], prediction.ModelType)

	step(t, "cancel completed volume sync idempotently")
	canceled := parseVolumeSync(t, mustCLI(t,
		"volume", "sync", "cancel",
		"--volume-sync-id", started.SyncID,
		"--output", "json"))
	require.Equal(t, "READY", canceled.Status)
	require.Equal(t, started.VersionRef, canceled.VersionRef)
}

// e2eVolumeSync mirrors the fields asserted from every volume-sync command.
type e2eVolumeSync struct {
	SyncID string `json:"sync_id"`
	Status string `json:"status"`
	Source struct {
		URI     string   `json:"uri"`
		Include []string `json:"include"`
		Exclude []string `json:"exclude"`
	} `json:"source"`
	Destination struct {
		Ref string `json:"ref"`
	} `json:"destination"`
	VolumeVersionID *string `json:"volume_version_id"`
	VersionRef      *string `json:"version_ref"`
	ContentDigest   *string `json:"content_digest"`
	TotalSizeBytes  *int64  `json:"total_size_bytes"`
	CompletedAt     *string `json:"completed_at"`
	Error           *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func parseVolumeSync(t *testing.T, out string) e2eVolumeSync {
	t.Helper()
	var sync e2eVolumeSync
	require.NoError(t, json.Unmarshal([]byte(out), &sync))
	return sync
}

func writeVolumeSyncModel(t *testing.T, modelName, versionRef string) string {
	t.Helper()
	dir := t.TempDir()
	config := fmt.Sprintf(`model_name: %s
python_version: py313
resources:
  cpu: 50m
  memory: 50Mi
  use_gpu: false
bdn:
  mounts:
    - source: %s
      path: /models/synced
  access:
    - namespace: %s
      grants: [pull]
`, modelName, versionRef, e2eVolumeNamespace)
	model := `import json
from pathlib import Path

class Model:
    def predict(self, request):
        path = Path("/models/synced/config.json")
        if not path.exists():
            return {"mounted": False, "model_type": ""}
        config = json.loads(path.read_text())
        return {"mounted": True, "model_type": config.get("model_type", "")}
`
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "model"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(config), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "model", "model.py"), []byte(model), 0o644))
	return dir
}
