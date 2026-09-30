//go:build e2e

package e2etests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	publiccmd "github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/stretchr/testify/require"
)

// The alarm bounds the remote job's lifetime if test cleanup cannot reach it.
const loopsExecClientPy = `import os
from pathlib import Path
import signal
import sys
import time

signal.alarm(600)
assert Path("marker.txt").read_text() == "uploaded directory\n"
assert sys.argv[2] == "argument with spaces"
assert os.environ["BASETEN_E2E_LOOPS"] == "environment with spaces"
assert not os.environ.get("BASETEN_API_KEY")
mode = sys.argv[1]
if mode == "success":
    print("loops-e2e-success-final", flush=True)
elif mode == "failure":
    print("loops-e2e-failure-final", flush=True)
    raise RuntimeError("loops-e2e-intentional-failure")
elif mode == "hold":
    print("loops-e2e-ready", flush=True)
    for heartbeat in range(1, 120):
        time.sleep(5)
        print(f"loops-e2e-heartbeat:{heartbeat}", flush=True)
    raise RuntimeError("hold was not stopped by the test")
else:
    raise ValueError(mode)
`

func TestE2ELoopsExec(t *testing.T) {
	apiKey := os.Getenv("BASETEN_E2E_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	remoteURL := os.Getenv("BASETEN_E2E_TEST_REMOTE_URL")
	require.NotEmpty(t, remoteURL, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_REMOTE_URL is missing")
	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "client.py"), []byte(loopsExecClientPy), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("uploaded directory\n"), 0o644))
	projectName := "cli-e2e-loops-" + randomSuffix(t)
	// Register before submission, including recovery when Truss created a job
	// but did not return parseable JSON. Project deletion also removes its cache.
	t.Cleanup(func() { cleanupLoopsExecProject(t, projectName) })
	ctx, cancel := context.WithTimeout(t.Context(), 18*time.Minute)
	defer cancel()

	exec := func(t *testing.T, mode string, stopOn func(string) bool) (loopsCLIResult, publiccmd.LoopsExecResult) {
		t.Helper()
		step(t, "loops exec %s in project %s", mode, projectName)
		commandCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		result := runLoopsCLI(commandCtx, stopOn,
			"loops", "exec", "--dir", dir, "--project-name", projectName,
			"--image", "python:3.12-slim", "--cpu-count", "1", "--memory", "8Gi",
			"--no-api-key", "--env", "BASETEN_E2E_LOOPS=environment with spaces",
			"--tail", "--output", "json", "--", "python", "client.py", mode, "argument with spaces")
		t.Cleanup(func() {
			if t.Failed() {
				t.Logf("loops exec %s stdout:\n%s\nstderr:\n%s", mode, result.stdout, result.stderr)
			}
		})
		var job publiccmd.LoopsExecResult
		require.NoError(t, json.Unmarshal([]byte(result.stdout), &job), "stderr:\n%s", result.stderr)
		require.NotEmpty(t, job.JobID, "stderr:\n%s", result.stderr)
		require.NotNil(t, job.Project.Name)
		require.Equal(t, projectName, *job.Project.Name)
		require.NotContains(t, job.EnvironmentVariables, "BASETEN_API_KEY")
		require.NotContains(t, result.stdout, "loops-e2e-", "remote logs must stay on stderr")
		step(t, "loops exec %s returned job %s (exit %d)", mode, job.JobID, result.exit)
		return result, job
	}

	if !t.Run("Success", func(t *testing.T) {
		result, job := exec(t, "success", nil)
		require.NoError(t, result.err, "stderr:\n%s", result.stderr)
		require.Zero(t, result.exit, "stderr:\n%s", result.stderr)
		require.Contains(t, result.stderr, "loops-e2e-success-final")
		current := describeLoopsExecJob(t, ctx, job.JobID)
		require.Equal(t, projectName, current.TrainingProject.Name)
		require.Equal(t, "TRAINING_JOB_COMPLETED", current.CurrentStatus)
		require.Zero(t, current.InstanceType.GPUCount, "expected CPU, got %s", current.InstanceType.Name)
	}) {
		return
	}
	if !t.Run("Failure", func(t *testing.T) {
		result, job := exec(t, "failure", nil)
		require.NoError(t, result.err, "the framework should report failure through ExitWithCode")
		require.Equal(t, 1, result.exit, "stderr:\n%s", result.stderr)
		require.Contains(t, result.stderr, "loops-e2e-failure-final")
		require.Contains(t, result.stderr, "Traceback (most recent call last)")
		require.Contains(t, result.stderr, "RuntimeError: loops-e2e-intentional-failure")
		current := describeLoopsExecJob(t, ctx, job.JobID)
		require.Equal(t, projectName, current.TrainingProject.Name)
		require.Zero(t, current.InstanceType.GPUCount, "expected CPU, got %s", current.InstanceType.Name)
		require.Equal(t, "TRAINING_JOB_FAILED", current.CurrentStatus)
	}) {
		return
	}
	t.Run("InterruptAndResume", func(t *testing.T) {
		result, job := exec(t, "hold", func(output string) bool {
			return strings.Contains(output, "loops-e2e-ready")
		})
		require.NoError(t, result.err)
		require.Equal(t, 130, result.exit, "stderr:\n%s", result.stderr)
		require.Contains(t, result.stderr, "loops-e2e-ready")
		require.Contains(t, result.stderr, "baseten train job logs --job-id "+job.JobID+" --tail")
		current := describeLoopsExecJob(t, ctx, job.JobID)
		require.Equal(t, projectName, current.TrainingProject.Name)
		require.Zero(t, current.InstanceType.GPUCount, "expected CPU, got %s", current.InstanceType.Name)
		require.Equal(t, "TRAINING_JOB_RUNNING", current.CurrentStatus)

		step(t, "resuming logs for job %s", job.JobID)
		lastHeartbeat := loopsExecHeartbeat(result.stderr)
		resumeCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		resumed := runLoopsCLI(resumeCtx, func(output string) bool {
			return loopsExecHeartbeat(output) > lastHeartbeat
		}, "train", "job", "logs", "--job-id", job.JobID, "--tail")
		require.NoError(t, resumed.err)
		require.Equal(t, 130, resumed.exit, "stderr:\n%s", resumed.stderr)
		require.Greater(t, loopsExecHeartbeat(resumed.stdout), lastHeartbeat, "resumed tail received no new heartbeat")

		step(t, "stopping job %s", job.JobID)
		stopCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		mustCLICtx(t, stopCtx, "train", "job", "stop", "--job-id", job.JobID, "--yes")
		for {
			if describeLoopsExecJob(t, stopCtx, job.JobID).CurrentStatus == "TRAINING_JOB_STOPPED" {
				break
			}
			select {
			case <-stopCtx.Done():
				t.Fatal("job did not reach STOPPED after explicit stop")
			case <-time.After(2 * time.Second):
			}
		}
	})
}

type loopsCLIResult struct {
	stdout, stderr string
	exit           int
	err            error
}

// Cancel on a log marker to exercise Ctrl-C handling after the job starts.
func runLoopsCLI(ctx context.Context, stopOn func(string) bool, args ...string) loopsCLIResult {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var once sync.Once
	stdout := &loopsMarkerWriter{stopOn: stopOn, cancel: cancel, once: &once}
	stderr := &loopsMarkerWriter{stopOn: stopOn, cancel: cancel, once: &once}
	var result loopsCLIResult
	result.err = cmd.Execute(ctx, cmd.ExecuteOptions{
		Args: args, Stdin: strings.NewReader(""), Stdout: stdout, Stderr: stderr,
		ExitWithCode: func(code int) { result.exit = code },
	})
	result.stdout, result.stderr = stdout.String(), stderr.String()
	if key := os.Getenv("BASETEN_API_KEY"); key != "" {
		result.stdout = strings.ReplaceAll(result.stdout, key, "[redacted]")
		result.stderr = strings.ReplaceAll(result.stderr, key, "[redacted]")
	}
	return result
}

type loopsMarkerWriter struct {
	syncBuffer
	stopOn func(string) bool
	cancel context.CancelFunc
	once   *sync.Once
}

func (w *loopsMarkerWriter) Write(p []byte) (int, error) {
	n, err := w.syncBuffer.Write(p)
	if w.stopOn != nil && w.stopOn(w.String()) {
		w.once.Do(w.cancel)
	}
	return n, err
}

func loopsExecHeartbeat(output string) int {
	latest := 0
	for _, line := range strings.Split(output, "\n") {
		if _, value, ok := strings.Cut(line, "loops-e2e-heartbeat:"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n > latest {
				latest = n
			}
		}
	}
	return latest
}

func describeLoopsExecJob(t *testing.T, ctx context.Context, jobID string) trainJob {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out := mustCLICtx(t, ctx, "train", "job", "describe", "--job-id", jobID, "--output", "json")
	var response struct {
		TrainingJob trainJob `json:"training_job"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &response))
	return response.TrainingJob
}

func cleanupLoopsExecProject(t *testing.T, projectName string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	call := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		out, stderr, err := cliCtx(t, ctx, args...)
		if key := os.Getenv("BASETEN_API_KEY"); key != "" {
			stderr = strings.ReplaceAll(stderr, key, "[redacted]")
		}
		if err != nil {
			return out, fmt.Errorf("%w; stderr: %s", err, stderr)
		}
		return out, nil
	}
	lookupProject := func() (string, error) {
		out, err := call("train", "project", "list", "--output", "json")
		var response struct {
			TrainingProjects []struct{ ID, Name string } `json:"training_projects"`
		}
		if err == nil {
			err = json.Unmarshal([]byte(out), &response)
		}
		if err != nil {
			return "", err
		}
		for _, project := range response.TrainingProjects {
			if project.Name == projectName {
				return project.ID, nil
			}
		}
		return "", nil
	}
	// A timed-out submission can create a project without returning any job
	// JSON. Allow its listing to settle, matching only this test's random name.
	var projectID string
	lookupDeadline := time.Now().Add(time.Minute)
	for projectID == "" {
		var err error
		projectID, err = lookupProject()
		if projectID != "" {
			break
		}
		if time.Now().After(lookupDeadline) {
			if err != nil {
				t.Errorf("cleanup could not locate project %s: %v", projectName, err)
			}
			return
		}
		time.Sleep(2 * time.Second)
	}
	step(t, "cleaning up loops exec project %s (%s)", projectName, projectID)
	out, err := call("train", "job", "list", "--project", projectID, "--output", "json")
	var jobs struct {
		TrainingJobs []trainJob `json:"training_jobs"`
	}
	if err == nil {
		err = json.Unmarshal([]byte(out), &jobs)
	}
	if err != nil {
		t.Errorf("cleanup could not list jobs; project deletion will stop them: %v", err)
	} else {
		for _, job := range jobs.TrainingJobs {
			if job.TrainingProject.ID != projectID {
				t.Errorf("cleanup refused job %s from a different project", job.ID)
				continue
			}
			switch job.CurrentStatus {
			case "TRAINING_JOB_COMPLETED", "TRAINING_JOB_FAILED", "TRAINING_JOB_DEPLOY_FAILED", "TRAINING_JOB_STOPPED", "TRAINING_JOB_EXPIRED":
				continue
			}
			if _, err := call("train", "job", "stop", "--job-id", job.ID, "--yes"); err != nil {
				t.Errorf("cleanup stop of job %s failed; still deleting project: %v", job.ID, err)
			}
		}
	}
	// The backend stops any remaining project jobs and removes their cache.
	for {
		_, deleteErr := call("api", "management", "training_projects/"+projectID, "-X", "DELETE")
		remainingID, lookupErr := lookupProject()
		if lookupErr == nil && remainingID == "" {
			step(t, "confirmed loops exec project %s (%s) removed", projectName, projectID)
			return
		}
		select {
		case <-ctx.Done():
			t.Errorf("cleanup could not confirm deletion of project %s (%s): delete=%v; lookup=%v", projectName, projectID, deleteErr, lookupErr)
			return
		case <-time.After(2 * time.Second):
		}
	}
}
