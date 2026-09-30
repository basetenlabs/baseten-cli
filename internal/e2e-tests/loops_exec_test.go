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

func TestE2ELoopsExec(t *testing.T) {
	apiKey := os.Getenv("BASETEN_E2E_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	remoteURL := os.Getenv("BASETEN_E2E_TEST_REMOTE_URL")
	require.NotEmpty(t, remoteURL)
	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())

	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%t", fail), func(t *testing.T) {
			project := "cli-e2e-loops-" + randomSuffix(t)
			t.Logf("test project: %s", project)
			// Discover by unique project name even if submission loses its response.
			t.Cleanup(func() { cleanupLoopsExecProject(t, project) })
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("uploaded fixture"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "client.py"), []byte(`import os
import pathlib
import sys

assert pathlib.Path("marker.txt").read_text() == "uploaded fixture"
assert os.environ["LOOPS_EXEC_TEST"] == "value with spaces"
assert sys.argv[1] == "argument with spaces"
print("loops-exec-upload-and-args-ok", flush=True)
if sys.argv[2] == "true":
    raise RuntimeError("loops-exec-intentional-failure")
print("loops-exec-complete", flush=True)
`), 0o644))
			ctx, cancel := context.WithTimeout(t.Context(), pushCLITimeout)
			defer cancel()
			out, logs, err := cliCtx(t, ctx, "loops", "exec", "--dir", dir,
				"--project-name", project, "--image", "python:3.12-slim",
				"--cpu-count", "1", "--memory", "8Gi", "--no-api-key",
				"--env", "LOOPS_EXEC_TEST=value with spaces", "--tail", "--output", "json",
				"--", "python", "client.py", "argument with spaces", fmt.Sprint(fail))
			if fail {
				require.Error(t, err, "failed remote job must fail the CLI")
				require.Contains(t, logs, "loops-exec-intentional-failure")
			} else {
				require.NoError(t, err, "stderr: %s", logs)
				require.Contains(t, logs, "loops-exec-complete")
			}
			require.Contains(t, logs, "loops-exec-upload-and-args-ok")
			var result struct {
				JobID string `json:"job_id"`
			}
			require.NoError(t, json.Unmarshal([]byte(out), &result), "stdout must contain only one JSON result")
			require.NotEmpty(t, result.JobID)
			tr := &trainLifecycle{jobID: result.JobID}
			job := tr.mustDescribeJob(t)
			require.Equal(t, project, job.TrainingProject.Name)
			require.Zero(t, job.InstanceType.GPUCount)
			if fail {
				require.Equal(t, "TRAINING_JOB_FAILED", job.CurrentStatus)
			} else {
				require.Equal(t, "TRAINING_JOB_COMPLETED", job.CurrentStatus)
			}
		})
	}
}

func cleanupLoopsExecProject(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, logs, err := cliCtx(t, ctx, "train", "project", "list", "--output", "json")
	if err != nil {
		t.Errorf("list projects for cleanup of %s: %v; stderr: %s", name, err, logs)
		return
	}
	var projects struct {
		TrainingProjects []struct{ ID, Name string } `json:"training_projects"`
	}
	if err := json.Unmarshal([]byte(out), &projects); err != nil {
		t.Errorf("decode cleanup project list: %v", err)
		return
	}
	for _, project := range projects.TrainingProjects {
		if project.Name == name {
			// Project deletion also stops its jobs, including timed-out submissions.
			_, logs, err := cliCtx(t, ctx, "api", "management", "training_projects/"+project.ID, "-X", "DELETE")
			if err != nil {
				t.Errorf("delete test project %s: %v; stderr: %s", name, err, logs)
			}
			return
		}
	}
}
