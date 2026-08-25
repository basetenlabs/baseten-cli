//go:build e2e

package e2etests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The training job is deliberately CPU-only: no accelerator means the backend
// resolves the CPU workload-plane row and the least expensive CPU instance
// type, so the whole lifecycle costs about what a CPU model deployment does.
// A GPU is needed to *train* something, and this test trains nothing: it
// checks that the CLI's train surface drives a real job end to end.
//
// The job writes a LoRA adapter config and then idles. Checkpoints are
// discovered by scanning the synced prefix for checkpoint-<N>/adapter_config.json
// and reading peft_type, base_model_name_or_path and r out of it, so that one
// file is a complete checkpoint as far as the platform is concerned, with no
// weights to produce or upload.
//
// The idle matters: the checkpoint sync sidecar loops until the job completes,
// sleeping 30s between passes, so a job that exits right after writing its
// checkpoint can finish before any pass picks the file up. Idling keeps the job
// RUNNING while the assertions run, and the Stop phase ends it.
const trainConfigTmpl = `from truss_train import definitions

CHECKPOINT_DIR = "%[1]s/%[2]s"
ADAPTER_CONFIG = "{\"peft_type\": \"LORA\", \"base_model_name_or_path\": \"%[3]s\", \"r\": %[4]d, \"lora_alpha\": 32}"

training_job = definitions.TrainingJob(
    image=definitions.Image(base_image="%[5]s"),
    # No accelerator, so this resolves to a CPU instance type. The memory
    # request has to cover the checkpoint sync sidecar as well as this
    # container, since both share the instance.
    compute=definitions.Compute(cpu_count=1, memory="%[6]s"),
    runtime=definitions.Runtime(
        start_commands=[
            "mkdir -p " + CHECKPOINT_DIR
            + " && echo '" + ADAPTER_CONFIG + "' > " + CHECKPOINT_DIR + "/adapter_config.json"
            + " && echo %[7]s"
            + " && sleep %[8]d"
        ],
        environment_variables={"%[9]s": "%[10]s"},
        checkpointing_config=definitions.CheckpointingConfig(
            enabled=True, checkpoint_path="%[1]s", volume_size_gib=%[11]d
        ),
    ),
)

training_project = definitions.TrainingProject(name="%[12]s", job=training_job)
`

// deployCheckpointsConfigTmpl is the --config for the delegated
// `train checkpoint deploy`. Everything the deploy needs is spelled out:
// truss prompts for any missing piece (checkpoints, model name, accelerator, HF
// secret) and those prompts fail rather than hang without a terminal.
//
// Paired with --dry-run, this renders a model config and stops: the backend
// returns before creating an oracle version, so nothing is deployed and no GPU
// is claimed.
const deployCheckpointsConfigTmpl = `from truss_train import definitions
from truss.base import truss_config

deploy_config = definitions.DeployCheckpointsConfig(
    checkpoint_details=definitions.CheckpointList(
        base_model_id="%[1]s",
        checkpoints=[
            definitions.LoRACheckpoint(
                training_job_id="%[2]s",
                checkpoint_name="%[3]s",
                lora_details=definitions.LoRADetails(rank=%[4]d),
            )
        ],
    ),
    model_name="%[5]s",
    runtime=definitions.DeployCheckpointsRuntime(
        environment_variables={"HF_TOKEN": definitions.SecretReference(name="%[6]s")}
    ),
    compute=definitions.Compute(
        accelerator=truss_config.AcceleratorSpec(
            accelerator=truss_config.Accelerator.H100, count=1
        )
    ),
)
`

const (
	// trainCheckpointPath is where the checkpoint volume is mounted, and
	// trainCheckpointName is the directory written under it. The name must match
	// checkpoint-<N> for the backend to recognize the directory as a checkpoint.
	trainCheckpointPath = "/mnt/ckpts"
	trainCheckpointName = "checkpoint-1"

	// trainCheckpointVolumeGiB is the checkpoint volume size. Only an upper
	// limit is enforced, and one adapter config needs none of it.
	trainCheckpointVolumeGiB = 10

	// trainBaseModel is recorded in the adapter config and read back as the
	// checkpoint's base model. Nothing downloads it.
	trainBaseModel = "Qwen/Qwen3-0.6B"
	trainLoRARank  = 16

	// trainBaseImage is small enough to pull in seconds. The job's start
	// commands run under sh, which is all the pod template requires.
	trainBaseImage = "alpine:3.21.3"

	// trainMemory covers this container plus the checkpoint sync sidecar, which
	// requests 4Gi of its own on the same instance.
	trainMemory = "8Gi"

	// trainLogMarker is echoed by the job and asserted by the Logs phase.
	trainLogMarker = "baseten-e2e-train-ready"

	// trainEnvVarName and trainEnvVarValue prove the environment reaches the
	// container: the marker line is echoed through the variable.
	trainEnvVarName  = "BASETEN_E2E_TRAIN"
	trainEnvVarValue = "ok"

	// trainIdleSeconds is how long the job idles after writing its checkpoint,
	// bounding how long the assertions have before it exits on its own. The
	// Stop phase normally ends it well before this elapses.
	trainIdleSeconds = 900
)

// TestE2ETrainLifecycle pushes a CPU-only training job with the delegated
// `train push`, drives the native train surface against it, deploys its
// checkpoint as a dry run through the delegated `train checkpoint deploy`, and
// stops it. Skips when the e2e env vars are absent.
func TestE2ETrainLifecycle(t *testing.T) {
	tr := newTrainLifecycle(t)
	t.Run("Init", tr.Init)
	t.Run("Capacity", tr.Capacity)
	t.Run("Project", tr.Project)
	t.Run("Job", tr.Job)
	t.Run("Logs", tr.Logs)
	t.Run("Checkpoints", tr.Checkpoints)
	t.Run("CheckpointDeployDryRun", tr.CheckpointDeployDryRun)
	t.Run("Stop", tr.Stop)
}

// trainLifecycle holds the state shared across the lifecycle sub-tests.
// Created by [newTrainLifecycle], which performs the push and registers
// teardown.
type trainLifecycle struct {
	projectName string
	projectID   string
	jobName     string
	jobID       string
}

// trainJob is the subset of a training job the assertions read.
type trainJob struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	CurrentStatus string `json:"current_status"`
	InstanceType  struct {
		Name     string `json:"name"`
		GPUCount int    `json:"gpu_count"`
	} `json:"instance_type"`
	TrainingProject struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"training_project"`
}

// newTrainLifecycle runs the env-gate, writes the job config, pushes it, and
// registers cleanup. Fatals on setup failure so sub-tests can assume valid
// state.
func newTrainLifecycle(t *testing.T) *trainLifecycle {
	apiKey := os.Getenv("BASETEN_E2E_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	remoteURL := os.Getenv("BASETEN_E2E_TEST_REMOTE_URL")
	require.NotEmpty(t, remoteURL, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_REMOTE_URL is missing")

	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())

	suffix := randomSuffix(t)
	tr := &trainLifecycle{
		projectName: fmt.Sprintf("cli-e2e-train-%s", suffix),
		jobName:     fmt.Sprintf("cli-e2e-job-%s", suffix),
	}

	// The pushed archive is the config's directory, so the config sits alone in
	// its own dir rather than beside the deploy config written later.
	configDir := filepath.Join(t.TempDir(), "job")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	configPath := filepath.Join(configDir, "config.py")
	require.NoError(t, os.WriteFile(configPath, []byte(fmt.Sprintf(trainConfigTmpl,
		trainCheckpointPath, trainCheckpointName, trainBaseModel, trainLoRARank,
		trainBaseImage, trainMemory, "$"+trainEnvVarName+" "+trainLogMarker, trainIdleSeconds,
		trainEnvVarName, trainEnvVarValue, trainCheckpointVolumeGiB, tr.projectName,
	)), 0o644))

	// Registered before the push so a job created by a push that then failed is
	// still stopped and its project removed.
	t.Cleanup(func() {
		if os.Getenv("BASETEN_E2E_KEEP_TRAIN_JOB") != "" {
			t.Logf("BASETEN_E2E_KEEP_TRAIN_JOB set; leaving project %q in place", tr.projectName)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if tr.jobID == "" {
			if job := tr.findJob(t); job != nil {
				tr.jobID, tr.projectID = job.ID, job.TrainingProject.ID
			}
		}
		if tr.jobID != "" {
			t.Logf("stopping training job %s", tr.jobID)
			if _, errOut, err := cliCtx(t, ctx, "train", "job", "stop", "--job-id", tr.jobID, "--yes"); err != nil {
				t.Logf("cleanup stop failed: %v\nstderr: %s", err, errOut)
			}
		}
		if tr.projectID == "" {
			return
		}
		// No native delete command for training projects, so go through the raw
		// API. Leaving the project behind would accumulate one per run.
		t.Logf("deleting training project %s (%s)", tr.projectName, tr.projectID)
		if _, errOut, err := cliCtx(t, ctx, "api", "management",
			"training_projects/"+tr.projectID, "-X", "DELETE"); err != nil {
			t.Logf("cleanup project delete failed: %v\nstderr: %s", err, errOut)
		}
	})

	// The delegated push reports nothing structured, so the job is found by
	// listing the project it created.
	t.Logf("pushing training job %s to project %s", tr.jobName, tr.projectName)
	mustCLI(t, "train", "push", "--config", configPath, "--job-name", tr.jobName)

	job := tr.findJob(t)
	require.NotNil(t, job, "pushed job %q not found in project %q", tr.jobName, tr.projectName)
	tr.jobID, tr.projectID = job.ID, job.TrainingProject.ID
	t.Logf("pushed training job %s in project %s", tr.jobID, tr.projectID)

	// The remaining phases need a container that has started: logs and
	// checkpoint sync both come from the running pod.
	var status string
	require.Eventually(t, func() bool {
		if current := tr.mustDescribeJob(t).CurrentStatus; current != status {
			status = current
			t.Logf("job status: %s", status)
		}
		return status == "TRAINING_JOB_RUNNING"
	}, 10*time.Minute, 5*time.Second, "job never reached running")
	return tr
}

// findJob returns the pushed job from the project's listing, or nil when it is
// not there yet. Errors are not fatal so this is safe to call from cleanup.
func (tr *trainLifecycle) findJob(t *testing.T) *trainJob {
	t.Helper()
	out, _, err := cli(t, "train", "job", "list", "--project", tr.projectName, "--output", "json")
	if err != nil {
		return nil
	}
	var resp struct {
		TrainingJobs []trainJob `json:"training_jobs"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return nil
	}
	for _, job := range resp.TrainingJobs {
		if job.Name == tr.jobName {
			return &job
		}
	}
	return nil
}

// mustDescribeJob fetches the job by ID and fatals if the fetch fails.
func (tr *trainLifecycle) mustDescribeJob(t *testing.T) trainJob {
	t.Helper()
	out := mustCLI(t, "train", "job", "describe", "--job-id", tr.jobID, "--output", "json")
	var resp struct {
		TrainingJob trainJob `json:"training_job"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &resp))
	return resp.TrainingJob
}

// Init covers the scaffolding command, which calls no API and so runs without a
// credential.
func (tr *trainLifecycle) Init(t *testing.T) {
	t.Run("ListExamples", func(t *testing.T) {
		out := mustCLI(t, "train", "init", "--list-examples")
		require.NotEmpty(t, strings.TrimSpace(out), "no examples listed")
	})

	t.Run("EmptyProject", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "scaffold")
		mustCLI(t, "train", "init", "--dir", dir)
		// The template is a config the user edits plus the script it runs.
		for _, name := range []string{"config.py", "run.sh"} {
			_, err := os.Stat(filepath.Join(dir, name))
			require.NoError(t, err, "%s missing from scaffold", name)
		}
	})
}

func (tr *trainLifecycle) Capacity(t *testing.T) {
	out := mustCLI(t, "train", "capacity", "describe", "--output", "json")
	var resp struct {
		GPUCapacities []struct {
			GPUType string `json:"gpu_type"`
		} `json:"gpu_capacities"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &resp))
}

// Project covers the project listing. `train project cache describe` is not
// covered: it 404s unless the project has a cache-enabled job, and enabling the
// cache would provision a cache volume and pin the project to one workload
// plane, which is more than this test should cost.
func (tr *trainLifecycle) Project(t *testing.T) {
	out := mustCLI(t, "train", "project", "list", "--output", "json")
	var resp struct {
		TrainingProjects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"training_projects"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &resp))
	for _, project := range resp.TrainingProjects {
		if project.ID == tr.projectID {
			require.Equal(t, tr.projectName, project.Name)
			return
		}
	}
	t.Fatalf("project %s (%s) missing from listing", tr.projectName, tr.projectID)
}

func (tr *trainLifecycle) Job(t *testing.T) {
	t.Run("Describe", func(t *testing.T) {
		job := tr.mustDescribeJob(t)
		require.Equal(t, tr.jobID, job.ID)
		require.Equal(t, tr.jobName, job.Name)
		// The point of the whole test: no accelerator was requested, so the job
		// landed on a CPU instance.
		require.Zero(t, job.InstanceType.GPUCount, "expected a CPU instance, got %s", job.InstanceType.Name)
	})

	t.Run("ListByStatus", func(t *testing.T) {
		out := mustCLI(t, "train", "job", "list", "--project", tr.projectName,
			"--status", "running", "--output", "json")
		var resp struct {
			TrainingJobs []trainJob `json:"training_jobs"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Len(t, resp.TrainingJobs, 1)
		require.Equal(t, tr.jobID, resp.TrainingJobs[0].ID)
	})

	t.Run("ListByUnmatchedStatus", func(t *testing.T) {
		out := mustCLI(t, "train", "job", "list", "--project", tr.projectName,
			"--status", "completed", "--output", "json")
		var resp struct {
			TrainingJobs []trainJob `json:"training_jobs"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Empty(t, resp.TrainingJobs)
	})

	// `train job update` is not covered: the backend only allows a priority
	// change while a job is PENDING, waiting on capacity, which a CPU job never
	// is.

	t.Run("Metrics", func(t *testing.T) {
		// A job this short may not have reported a sample yet, so this asserts
		// the query works rather than what it contains.
		out := mustCLI(t, "train", "job", "metrics", "--job-id", tr.jobID, "--since", "30m", "--output", "json")
		require.NoError(t, json.Unmarshal([]byte(out), &struct{}{}))
	})

	t.Run("SessionDescribe", func(t *testing.T) {
		// No interactive session was requested, so there is nothing to list.
		out := mustCLI(t, "train", "job", "session", "describe", "--job-id", tr.jobID, "--output", "json")
		var resp struct {
			AuthCodes []struct {
				SessionID string `json:"session_id"`
			} `json:"auth_codes"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Empty(t, resp.AuthCodes)
	})
}

func (tr *trainLifecycle) Logs(t *testing.T) {
	collect := func(extraArgs ...string) ([]logLine, error) {
		args := append([]string{"train", "job", "logs", "--job-id", tr.jobID,
			"--since", "1h", "--output", "jsonl"}, extraArgs...)
		out, _, err := cli(t, args...)
		if err != nil {
			return nil, err
		}
		var lines []logLine
		for _, raw := range strings.Split(strings.TrimSpace(out), "\n") {
			if raw == "" {
				continue
			}
			var ll logLine
			require.NoError(t, json.Unmarshal([]byte(raw), &ll))
			lines = append(lines, ll)
		}
		return lines, nil
	}

	// Log propagation lags the container starting, so poll until the marker the
	// job echoed is queryable.
	var lines []logLine
	require.Eventually(t, func() bool {
		got, err := collect()
		if err != nil {
			return false
		}
		lines = got
		for _, ll := range lines {
			if strings.Contains(ll.Message, trainLogMarker) {
				return true
			}
		}
		return false
	}, 3*time.Minute, 5*time.Second, "marker line never appeared in job logs")

	// The job echoes the marker through an environment variable it was
	// configured with, so the line proves the environment arrived too.
	var marker string
	for _, ll := range lines {
		if strings.Contains(ll.Message, trainLogMarker) {
			marker = ll.Message
		}
	}
	require.Contains(t, marker, trainEnvVarValue+" "+trainLogMarker)

	t.Run("Limit", func(t *testing.T) {
		limited, err := collect("--limit", "1")
		require.NoError(t, err)
		require.Len(t, limited, 1)
	})
}

func (tr *trainLifecycle) Checkpoints(t *testing.T) {
	type checkpoint struct {
		CheckpointID   string  `json:"checkpoint_id"`
		CheckpointType string  `json:"checkpoint_type"`
		BaseModel      *string `json:"base_model"`
		SizeBytes      int     `json:"size_bytes"`
	}

	// The sync sidecar walks the checkpoint directory every 30 seconds, and the
	// listing reads S3 directly, so the checkpoint shows up within a pass or two
	// of the job writing it.
	var found checkpoint
	require.Eventually(t, func() bool {
		out, _, err := cli(t, "train", "checkpoint", "list", "--job-id", tr.jobID, "--output", "json")
		if err != nil {
			return false
		}
		var resp struct {
			Checkpoints []checkpoint `json:"checkpoints"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			return false
		}
		for _, cp := range resp.Checkpoints {
			if cp.CheckpointID == trainCheckpointName {
				found = cp
				return true
			}
		}
		return false
	}, 4*time.Minute, 10*time.Second, "checkpoint %s never appeared", trainCheckpointName)

	// Type and base model are read out of the adapter config the job wrote.
	require.Equal(t, "lora", strings.ToLower(found.CheckpointType))
	require.NotNil(t, found.BaseModel)
	require.Equal(t, trainBaseModel, *found.BaseModel)

	t.Run("Files", func(t *testing.T) {
		out := mustCLI(t, "train", "checkpoint", "files", "--job-id", tr.jobID, "--output", "json")
		var files []struct {
			RelativeFileName string `json:"relative_file_name"`
			SizeBytes        int    `json:"size_bytes"`
			URL              string `json:"url"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &files))
		require.NotEmpty(t, files)
		var names []string
		for _, f := range files {
			names = append(names, f.RelativeFileName)
			require.NotEmpty(t, f.URL, "%s has no presigned URL", f.RelativeFileName)
		}
		require.Contains(t, strings.Join(names, "\n"), trainCheckpointName+"/adapter_config.json")
	})
}

// CheckpointDeployDryRun exercises the delegated deploy: truss evaluates the
// Python config, the CLI's forwarded credential authenticates the GraphQL
// mutation behind it, and --dry-run means the backend renders the model config
// without creating a deployment.
func (tr *trainLifecycle) CheckpointDeployDryRun(t *testing.T) {
	hfSecret := os.Getenv("BASETEN_E2E_TEST_HF_SECRET_NAME")
	if hfSecret == "" {
		hfSecret = "hf_access_token"
	}
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "deploy_checkpoints.py")
	require.NoError(t, os.WriteFile(configPath, []byte(fmt.Sprintf(deployCheckpointsConfigTmpl,
		trainBaseModel, tr.jobID, trainCheckpointName, trainLoRARank,
		tr.projectName, hfSecret,
	)), 0o644))

	outDir := filepath.Join(configDir, "generated")
	mustCLI(t, "train", "checkpoint", "deploy", "--job-id", tr.jobID,
		"--config", configPath, "--dry-run", "--config-out-dir", outDir)

	// The generated config is what a user edits and re-pushes, so the run is
	// only useful if it actually landed on disk.
	entries, err := os.ReadDir(outDir)
	require.NoError(t, err, "no config written to %s", outDir)
	require.NotEmpty(t, entries)
}

func (tr *trainLifecycle) Stop(t *testing.T) {
	mustCLI(t, "train", "job", "stop", "--job-id", tr.jobID, "--yes")

	// Stopping is asynchronous, so the status settles a moment later.
	var status string
	require.Eventually(t, func() bool {
		if current := tr.mustDescribeJob(t).CurrentStatus; current != status {
			status = current
			t.Logf("job status: %s", status)
		}
		return status == "TRAINING_JOB_STOPPED"
	}, 3*time.Minute, 5*time.Second, "job never reported stopped")
}
