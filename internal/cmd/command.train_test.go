package cmd_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

const (
	trainSearchPath      = "/v1/training_jobs/search"
	trainProjectsPath    = "/v1/training_projects"
	trainJobPath         = "/v1/training_projects/proj-1/jobs/job-1"
	trainCapacityPath    = "/v1/training/capacity"
	trainCacheSummaryPat = "/v1/training_projects/proj-1/cache/summary"
)

func trainInstanceTypeFixture() map[string]any {
	return map[string]any{
		"id":                   "4xH100",
		"name":                 "4xH100",
		"gpu_count":            4,
		"gpu_type":             "H100",
		"gpu_memory_limit_mib": 81920,
		"memory_limit_mib":     262144,
		"millicpu_limit":       26000,
	}
}

func trainJobFixture(id, status string) map[string]any {
	return map[string]any{
		"id":                  id,
		"name":                "job-" + id,
		"created_at":          "2026-05-14T12:00:00Z",
		"updated_at":          "2026-05-14T13:00:00Z",
		"current_status":      status,
		"instance_type":       trainInstanceTypeFixture(),
		"node_count":          2,
		"priority":            5,
		"availability_model":  "DEDICATED",
		"training_project":    map[string]any{"id": "proj-1", "name": "my-project"},
		"training_project_id": "proj-1",
		"user":                map[string]any{"email": "owner@example.com"},
	}
}

// mockTrainJobSearch routes the job-id lookup every job command performs to
// resolve its project.
func mockTrainJobSearch(m *MockManagementAPI, job map[string]any) {
	m.SetRoute("POST", trainSearchPath, 200, map[string]any{"training_jobs": []any{job}})
}

func Test_Train_Job_List(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", trainSearchPath, 200, map[string]any{
		"training_jobs": []any{trainJobFixture("job-1", "TRAINING_JOB_RUNNING")},
	})

	h.Require.NoError(h.Execute("train", "job", "list"))
	out := h.Stdout.String()
	h.Require.Contains(out, "job-1")
	h.Require.Contains(out, "my-project")
	h.Require.Contains(out, "4xH100")
	// Statuses print in the CLI spelling, not the API's.
	h.Require.Contains(out, "running")
	h.Require.NotContains(out, "TRAINING_JOB_RUNNING")
}

func Test_Train_Job_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("POST", trainSearchPath, 200, map[string]any{"training_jobs": []any{}})

	h.Require.NoError(h.Execute("train", "job", "list"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No training jobs found.")
}

func Test_Train_Job_List_StatusAndProjectFilters(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", trainProjectsPath, 200, map[string]any{
		"training_projects": []any{map[string]any{
			"id": "proj-1", "name": "my-project",
			"created_at": "2026-05-14T12:00:00Z", "updated_at": "2026-05-14T12:00:00Z",
			"latest_job": nil,
		}},
	})
	m.SetRoute("POST", trainSearchPath, 200, map[string]any{"training_jobs": []any{}})

	h.Require.NoError(h.Execute("train", "job", "list",
		"--project", "my-project", "--status", "running", "--status", "deploy-failed", "--direction", "asc"))
	body := m.FindCall("POST", trainSearchPath).BodyJSON(t)
	h.Require.Equal("proj-1", body["project_id"])
	// CLI spellings are expanded to the API's.
	h.Require.Equal([]any{"TRAINING_JOB_RUNNING", "TRAINING_JOB_DEPLOY_FAILED"}, body["statuses"])
	h.Require.Equal([]any{map[string]any{"field": "created_at", "order": "asc"}}, body["order_by"])
}

func Test_Train_Job_List_StatusAcceptsAPISpelling(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", trainSearchPath, 200, map[string]any{"training_jobs": []any{}})

	h.Require.NoError(h.Execute("train", "job", "list", "--status", "TRAINING_JOB_RUNNING"))
	body := m.FindCall("POST", trainSearchPath).BodyJSON(t)
	h.Require.Equal([]any{"TRAINING_JOB_RUNNING"}, body["statuses"])
}

func Test_Train_Job_List_UnknownStatusPassesThrough(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", trainSearchPath, 200, map[string]any{
		// A status this build predates still renders, rather than showing blank.
		"training_jobs": []any{trainJobFixture("job-1", "TRAINING_JOB_HIBERNATING")},
	})

	h.Require.NoError(h.Execute("train", "job", "list", "--status", "hibernating"))
	h.Require.Equal([]any{"TRAINING_JOB_HIBERNATING"},
		m.FindCall("POST", trainSearchPath).BodyJSON(t)["statuses"])
	h.Require.Contains(h.Stdout.String(), "hibernating")
}

func Test_Train_Job_List_JSONKeepsAPIStatus(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", trainSearchPath, 200, map[string]any{
		"training_jobs": []any{trainJobFixture("job-1", "TRAINING_JOB_RUNNING")},
	})

	h.Require.NoError(h.Execute("train", "job", "list", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"current_status": "TRAINING_JOB_RUNNING"`)
}

func Test_Train_Job_Describe(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_RUNNING"))
	m.SetRoute("GET", trainJobPath, 200, map[string]any{
		"training_job": trainJobFixture("job-1", "TRAINING_JOB_RUNNING"),
		"training_project": map[string]any{
			"id": "proj-1", "name": "my-project",
			"created_at": "2026-05-14T12:00:00Z", "updated_at": "2026-05-14T12:00:00Z",
			"latest_job": nil,
		},
	})

	h.Require.NoError(h.Execute("train", "job", "describe", "--job-id", "job-1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "ID:             job-1")
	h.Require.Contains(out, "Project:        my-project (proj-1)")
	h.Require.Contains(out, "Status:         running")
	h.Require.Contains(out, "Nodes:          2")
}

func Test_Train_Job_Describe_UnknownJob(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", trainSearchPath, 200, map[string]any{"training_jobs": []any{}})

	err := h.Execute("train", "job", "describe", "--job-id", "nope")
	h.Require.ErrorContains(err, "no training job found with ID nope")
	// The job endpoint is never reached without a resolved project.
	h.Require.Nil(m.FindCall("GET", trainJobPath))
}

func Test_Train_Job_Stop_RequiresConfirmation(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_RUNNING"))

	err := h.Execute("train", "job", "stop", "--job-id", "job-1")
	h.Require.Error(err)
	h.Require.Nil(m.FindCall("POST", trainJobPath+"/stop"))
}

func Test_Train_Job_Stop(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_RUNNING"))
	m.SetRoute("POST", trainJobPath+"/stop", 200, map[string]any{
		"training_job": trainJobFixture("job-1", "TRAINING_JOB_STOPPED"),
	})

	h.Require.NoError(h.Execute("train", "job", "stop", "--job-id", "job-1", "--yes"))
	h.Require.NotNil(m.FindCall("POST", trainJobPath+"/stop"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Stopped training job job-1")
}

func Test_Train_Job_Recreate(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"))
	m.SetRoute("POST", trainJobPath+"/recreate", 200, map[string]any{
		"training_job": trainJobFixture("job-2", "TRAINING_JOB_CREATED"),
	})

	h.Require.NoError(h.Execute("train", "job", "recreate", "--job-id", "job-1"))
	h.Require.Contains(h.Stderr.String(), "Created training job job-2 from job-1")
}

func Test_Train_Job_Update(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_PENDING"))
	m.SetRoute("PATCH", trainJobPath, 200, map[string]any{
		"training_job": trainJobFixture("job-1", "TRAINING_JOB_PENDING"),
	})

	h.Require.NoError(h.Execute("train", "job", "update", "--job-id", "job-1", "--priority", "10"))
	body := m.FindCall("PATCH", trainJobPath).BodyJSON(t)
	h.Require.Equal(float64(10), body["priority"])
	h.Require.NotContains(body, "availability_model")
	h.Require.Contains(h.Stderr.String(), "priority to 10")
}

func Test_Train_Job_Update_AvailabilityModel(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_PENDING"))
	m.SetRoute("PATCH", trainJobPath, 200, map[string]any{
		"training_job": trainJobFixture("job-1", "TRAINING_JOB_PENDING"),
	})

	h.Require.NoError(h.Execute("train", "job", "update", "--job-id", "job-1",
		"--availability-model", "spot"))
	body := m.FindCall("PATCH", trainJobPath).BodyJSON(t)
	h.Require.Equal("spot", body["availability_model"])
	// Priority is omitted entirely rather than sent as 0, which would be a real change.
	h.Require.NotContains(body, "priority")
	h.Require.Contains(h.Stderr.String(), "availability model to spot")
}

func Test_Train_Job_Update_PriorityAndAvailabilityModel(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_PENDING"))
	m.SetRoute("PATCH", trainJobPath, 200, map[string]any{
		"training_job": trainJobFixture("job-1", "TRAINING_JOB_PENDING"),
	})

	h.Require.NoError(h.Execute("train", "job", "update", "--job-id", "job-1",
		"--priority", "7", "--availability-model", "dedicated"))
	body := m.FindCall("PATCH", trainJobPath).BodyJSON(t)
	h.Require.Equal(float64(7), body["priority"])
	h.Require.Equal("dedicated", body["availability_model"])
	h.Require.Contains(h.Stderr.String(), "priority to 7")
	h.Require.Contains(h.Stderr.String(), "availability model to dedicated")
}

func Test_Train_Job_Update_ZeroPriorityIsSent(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_PENDING"))
	m.SetRoute("PATCH", trainJobPath, 200, map[string]any{
		"training_job": trainJobFixture("job-1", "TRAINING_JOB_PENDING"),
	})

	// 0 is a valid priority, so an explicit --priority 0 must reach the API
	// instead of being mistaken for an unset flag.
	h.Require.NoError(h.Execute("train", "job", "update", "--job-id", "job-1", "--priority", "0"))
	body := m.FindCall("PATCH", trainJobPath).BodyJSON(t)
	h.Require.Equal(float64(0), body["priority"])
}

func Test_Train_Job_Update_NoFieldsIsUsageError(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_PENDING"))

	err := h.Execute("train", "job", "update", "--job-id", "job-1")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "at least one of --priority or --availability-model")
	h.Require.Nil(m.FindCall("PATCH", trainJobPath))
}

func Test_Train_Job_Update_NoFieldsReportsUsageErrorBeforeClientSetup(t *testing.T) {
	h := NewCommandHarness(t)
	// A malformed remote makes building the management client fail outright. A
	// malformed invocation should still report what is wrong with the invocation
	// rather than the config error hit on the way there.
	t.Setenv("BASETEN_REMOTE_URL", "://bogus")

	err := h.Execute("train", "job", "update", "--job-id", "job-1")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "at least one of --priority or --availability-model")
	h.Require.NotContains(err.Error(), "invalid remote URL")
}

func Test_Train_Job_Update_RejectsUnknownAvailabilityModel(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI()

	err := h.Execute("train", "job", "update", "--job-id", "job-1",
		"--availability-model", "bogus")
	h.Require.Error(err)
}

func Test_Train_Job_Logs(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_RUNNING"))
	m.SetRoute("GET", trainJobPath+"/logs", 200, map[string]any{
		"logs": []any{map[string]any{
			"timestamp": "1747224000000000000",
			"message":   "epoch 1 complete",
		}},
	})

	h.Require.NoError(h.Execute("train", "job", "logs", "--job-id", "job-1"))
	h.Require.Contains(h.Stdout.String(), "epoch 1 complete")
}

func Test_Train_Job_Metrics(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_RUNNING"))
	m.SetRoute("GET", trainJobPath+"/metrics", 200, map[string]any{
		"training_job":           trainJobFixture("job-1", "TRAINING_JOB_RUNNING"),
		"cpu_usage":              []any{},
		"cpu_memory_usage_bytes": []any{},
		"gpu_utilization":        map[string]any{},
		"gpu_memory_usage_bytes": map[string]any{},
		"ephemeral_storage":      map[string]any{"usage_bytes": []any{}, "utilization": []any{}},
		"cache":                  nil,
		"per_node_metrics": []any{map[string]any{
			"node_id": "node-0",
			"metrics": map[string]any{
				// Samples arrive unordered; the latest one is reported.
				"cpu_usage": []any{
					map[string]any{"timestamp": "2026-05-14T12:00:00Z", "value": 1.5},
					map[string]any{"timestamp": "2026-05-14T12:05:00Z", "value": 3.25},
					map[string]any{"timestamp": "2026-05-14T12:02:00Z", "value": 2.0},
				},
				"cpu_memory_usage_bytes": []any{},
				"gpu_utilization": map[string]any{
					"0": []any{map[string]any{"timestamp": "2026-05-14T12:05:00Z", "value": 0.42}},
				},
				"gpu_memory_usage_bytes": map[string]any{},
				"ephemeral_storage":      map[string]any{"usage_bytes": []any{}, "utilization": []any{}},
			},
		}},
	})

	h.Require.NoError(h.Execute("train", "job", "metrics", "--job-id", "job-1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "node-0")
	h.Require.Contains(out, "3.25 cores")
	h.Require.Contains(out, "42.0%")
	// Series with no samples are omitted rather than shown as zero.
	h.Require.NotContains(out, "cpu memory")
}

func Test_Train_Job_Metrics_Window(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_RUNNING"))
	m.SetRoute("GET", trainJobPath+"/metrics", 200, map[string]any{
		"training_job":           trainJobFixture("job-1", "TRAINING_JOB_RUNNING"),
		"cpu_usage":              []any{},
		"cpu_memory_usage_bytes": []any{},
		"gpu_utilization":        map[string]any{},
		"gpu_memory_usage_bytes": map[string]any{},
		"ephemeral_storage":      map[string]any{"usage_bytes": []any{}, "utilization": []any{}},
		"cache":                  nil,
		"per_node_metrics":       []any{},
	})

	h.Require.NoError(h.Execute("train", "job", "metrics", "--job-id", "job-1", "--since", "1h"))
	q := m.FindCall("GET", trainJobPath+"/metrics").Query()
	h.Require.NotEmpty(q.Get("start_epoch_millis"))
	h.Require.NotEmpty(q.Get("end_epoch_millis"))
	h.Require.Contains(h.Stderr.String(), "No metrics reported")
}

func Test_Train_Job_Metrics_SinceWithStartRejected(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("train", "job", "metrics", "--job-id", "job-1",
		"--since", "1h", "--start", "2026-05-14T12:00:00Z")
	h.Require.ErrorContains(err, "--since cannot be combined with --start or --end")
	h.Require.Empty(m.Calls())
}

func Test_Train_Job_Session_Describe(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_RUNNING"))
	m.SetRoute("GET", trainJobPath+"/auth_codes", 200, map[string]any{
		"auth_codes": []any{map[string]any{
			"auth_code":  "code-1",
			"auth_url":   "https://sessions.example.com/code-1",
			"replica_id": "replica-0",
			"session_id": "sess-1",
			"expires_at": "2026-05-14T14:00:00Z",
		}},
	})

	h.Require.NoError(h.Execute("train", "job", "session", "describe", "--job-id", "job-1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "replica-0")
	h.Require.Contains(out, "sess-1")
}

func Test_Train_Job_Session_Describe_None(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_RUNNING"))
	m.SetRoute("GET", trainJobPath+"/auth_codes", 200, map[string]any{"auth_codes": []any{}})

	h.Require.NoError(h.Execute("train", "job", "session", "describe", "--job-id", "job-1"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No interactive sessions found.")
}

// trainArtifactTar builds an uncompressed tar holding one file, standing in for
// a job's uploaded code archive. Truss packs these with tarfile "w:" and names
// them ".tgz" by convention, so the bytes are plain tar despite the name.
func trainArtifactTar(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// trainArtifactTarGz is trainArtifactTar compressed, which the backend does not
// currently produce but extraction tolerates.
func trainArtifactTarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(trainArtifactTar(t, name, content)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// mockTrainArtifact serves archive at /artifact.tgz and points the job's
// download endpoint at it.
func mockTrainArtifact(m *MockManagementAPI, archive []byte) {
	m.SetRouteFunc("GET", "/artifact.tgz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	m.SetRoute("GET", trainJobPath+"/download", 200, map[string]any{
		"artifact_presigned_urls": []any{m.URL + "/artifact.tgz"},
	})
}

func Test_Train_Job_Download_Extracts(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"))
	mockTrainArtifact(m, trainArtifactTar(t, "train.py", "print('hi')"))
	dir := filepath.Join(t.TempDir(), "out")

	h.Require.NoError(h.Execute("train", "job", "download", "--job-id", "job-1", "--out-dir", dir))
	content, err := os.ReadFile(filepath.Join(dir, "train.py"))
	h.Require.NoError(err)
	h.Require.Equal("print('hi')", string(content))
	h.Require.Contains(h.Stderr.String(), "Extracted to "+dir)
}

func Test_Train_Job_Download_ExtractsGzipped(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"))
	// Compression is detected from the bytes, so a compressed artifact extracts
	// too even though nothing produces one today.
	mockTrainArtifact(m, trainArtifactTarGz(t, "train.py", "print('hi')"))
	dir := filepath.Join(t.TempDir(), "out")

	h.Require.NoError(h.Execute("train", "job", "download", "--job-id", "job-1", "--out-dir", dir))
	content, err := os.ReadFile(filepath.Join(dir, "train.py"))
	h.Require.NoError(err)
	h.Require.Equal("print('hi')", string(content))
}

func Test_Train_Job_Download_OutFile(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"))
	archive := trainArtifactTar(t, "train.py", "print('hi')")
	mockTrainArtifact(m, archive)
	out := filepath.Join(t.TempDir(), "job.tar")

	h.Require.NoError(h.Execute("train", "job", "download",
		"--job-id", "job-1", "--out-file", out, "--output", "json"))
	written, err := os.ReadFile(out)
	h.Require.NoError(err)
	h.Require.Equal(archive, written)
	h.Require.Contains(h.Stdout.String(), `"out_file"`)
}

func Test_Train_Job_Download_RequiresOutFlag(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	// Matching `model deployment download`, the destination is never implied.
	err := h.Execute("train", "job", "download", "--job-id", "job-1")
	h.Require.ErrorContains(err, "out-dir")
	h.Require.Empty(m.Calls())
}

func Test_Train_Job_Download_NoOverwrite(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	out := filepath.Join(t.TempDir(), "job.tar")
	h.Require.NoError(os.WriteFile(out, []byte("existing"), 0o644))

	err := h.Execute("train", "job", "download", "--job-id", "job-1", "--out-file", out)
	h.Require.ErrorContains(err, "pass --overwrite")
	h.Require.Empty(m.Calls())
}

func Test_Train_Job_Download_NoArtifacts(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"))
	m.SetRoute("GET", trainJobPath+"/download", 200, map[string]any{"artifact_presigned_urls": []any{}})

	err := h.Execute("train", "job", "download",
		"--job-id", "job-1", "--out-dir", filepath.Join(t.TempDir(), "out"))
	h.Require.ErrorContains(err, "has no artifacts to download")
}

func Test_Train_Checkpoint_List(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"))
	m.SetRoute("GET", trainJobPath+"/checkpoints", 200, map[string]any{
		"training_job": trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"),
		"checkpoints": []any{
			map[string]any{
				"checkpoint_id": "ckpt-old", "checkpoint_type": "full",
				"created_at": "2026-05-14T12:00:00Z", "size_bytes": 2048,
				"base_model": "Qwen/Qwen3-8B", "sync_status": "COMPLETE",
				"lora_adapter_config": nil,
			},
			map[string]any{
				"checkpoint_id": "ckpt-new", "checkpoint_type": "full",
				"created_at": "2026-05-14T13:00:00Z", "size_bytes": 4096,
				"base_model": "Qwen/Qwen3-8B", "sync_status": "SYNCING",
				"lora_adapter_config": nil,
			},
		},
	})

	h.Require.NoError(h.Execute("train", "checkpoint", "list", "--job-id", "job-1"))
	out := h.Stdout.String()
	// Newest first by default.
	h.Require.Less(strings.Index(out, "ckpt-new"), strings.Index(out, "ckpt-old"))
	h.Require.Contains(out, "4.0 KiB")
}

func Test_Train_Checkpoint_List_Ascending(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"))
	m.SetRoute("GET", trainJobPath+"/checkpoints", 200, map[string]any{
		"training_job": trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"),
		"checkpoints": []any{
			map[string]any{
				"checkpoint_id": "ckpt-new", "checkpoint_type": "full",
				"created_at": "2026-05-14T13:00:00Z", "size_bytes": 4096,
				"base_model": "Qwen/Qwen3-8B", "sync_status": "COMPLETE",
				"lora_adapter_config": nil,
			},
			map[string]any{
				"checkpoint_id": "ckpt-old", "checkpoint_type": "full",
				"created_at": "2026-05-14T12:00:00Z", "size_bytes": 2048,
				"base_model": "Qwen/Qwen3-8B", "sync_status": "COMPLETE",
				"lora_adapter_config": nil,
			},
		},
	})

	h.Require.NoError(h.Execute("train", "checkpoint", "list", "--job-id", "job-1", "--direction", "asc"))
	out := h.Stdout.String()
	h.Require.Less(strings.Index(out, "ckpt-old"), strings.Index(out, "ckpt-new"))
}

func Test_Train_Checkpoint_Files_Paged(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"))
	page := 0
	m.SetRouteFunc("GET", trainJobPath+"/checkpoint_files", func(w http.ResponseWriter, r *http.Request) {
		page++
		w.Header().Set("Content-Type", "application/json")
		if page == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total_count":     2,
				"next_page_token": 2,
				"presigned_urls": []any{map[string]any{
					"relative_file_name": "shard-1.safetensors", "size_bytes": 1024,
					"url": "https://files.example.com/1", "node_rank": 0,
					"last_modified": "2026-05-14T12:00:00Z",
				}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count":     2,
			"next_page_token": nil,
			"presigned_urls": []any{map[string]any{
				"relative_file_name": "shard-2.safetensors", "size_bytes": 2048,
				"url": "https://files.example.com/2", "node_rank": 1,
				"last_modified": "2026-05-14T12:00:00Z",
			}},
		})
	})

	h.Require.NoError(h.Execute("train", "checkpoint", "files", "--job-id", "job-1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "shard-1.safetensors")
	h.Require.Contains(out, "shard-2.safetensors")
	h.Require.Equal(2, page)
}

func Test_Train_Checkpoint_Files_JSONStreamsArray(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"))
	m.SetRoute("GET", trainJobPath+"/checkpoint_files", 200, map[string]any{
		"total_count":     1,
		"next_page_token": nil,
		"presigned_urls": []any{map[string]any{
			"relative_file_name": "shard-1.safetensors", "size_bytes": 1024,
			"url": "https://files.example.com/1", "node_rank": 0,
			"last_modified": "2026-05-14T12:00:00Z",
		}},
	})

	h.Require.NoError(h.Execute("train", "checkpoint", "files", "--job-id", "job-1", "--output", "json"))
	out := h.Stdout.String()
	// A bare array of file records, with no envelope around it.
	h.Require.Contains(out, `"relative_file_name": "shard-1.safetensors"`)
	h.Require.NotContains(out, "checkpoint_artifacts")
	h.Require.Equal("[", out[:1])
}

func Test_Train_Checkpoint_Files_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"))
	m.SetRoute("GET", trainJobPath+"/checkpoint_files", 200, map[string]any{
		"total_count": 0, "next_page_token": nil, "presigned_urls": []any{},
	})

	h.Require.NoError(h.Execute("train", "checkpoint", "files", "--job-id", "job-1"))
	h.Require.Contains(h.Stderr.String(), "No checkpoint files found.")
}

func Test_Train_Project_List(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", trainProjectsPath, 200, map[string]any{
		"training_projects": []any{
			map[string]any{
				"id": "proj-old", "name": "older", "team_name": "ml",
				"created_at": "2026-05-13T12:00:00Z", "updated_at": "2026-05-13T12:00:00Z",
				"latest_job": nil,
			},
			map[string]any{
				"id": "proj-new", "name": "newer",
				"created_at": "2026-05-14T12:00:00Z", "updated_at": "2026-05-14T12:00:00Z",
				"latest_job": trainJobFixture("job-1", "TRAINING_JOB_RUNNING"),
			},
		},
	})

	h.Require.NoError(h.Execute("train", "project", "list"))
	out := h.Stdout.String()
	h.Require.Less(strings.Index(out, "proj-new"), strings.Index(out, "proj-old"))
	h.Require.Contains(out, "running")
	h.Require.Contains(out, "ml")
}

func Test_Train_Project_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", trainProjectsPath, 200,
		map[string]any{"training_projects": []any{}})

	h.Require.NoError(h.Execute("train", "project", "list"))
	h.Require.Contains(h.Stderr.String(), "No training projects found.")
}

func Test_Train_Project_Cache_Describe(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", trainProjectsPath, 200, map[string]any{
		"training_projects": []any{map[string]any{
			"id": "proj-1", "name": "my-project",
			"created_at": "2026-05-14T12:00:00Z", "updated_at": "2026-05-14T12:00:00Z",
			"latest_job": nil,
		}},
	})
	m.SetRoute("GET", trainCacheSummaryPat, 200, map[string]any{
		"project_id": "proj-1",
		"timestamp":  "2026-05-14T12:00:00Z",
		"file_summaries": []any{
			map[string]any{
				"path": "a-small.bin", "file_type": "file", "size_bytes": 1024,
				"permissions": "rw-r--r--", "modified": "2026-05-14T12:00:00Z",
			},
			map[string]any{
				"path": "z-large.bin", "file_type": "file", "size_bytes": 8192,
				"permissions": "rw-r--r--", "modified": "2026-05-14T12:00:00Z",
			},
		},
	})

	h.Require.NoError(h.Execute("train", "project", "cache", "describe", "--project", "my-project"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Total: 9.0 KiB across 2 files")
	// Largest first by default.
	h.Require.Less(strings.Index(out, "z-large.bin"), strings.Index(out, "a-small.bin"))
}

func Test_Train_Project_Cache_Describe_SortByPath(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", trainProjectsPath, 200, map[string]any{
		"training_projects": []any{map[string]any{
			"id": "proj-1", "name": "my-project",
			"created_at": "2026-05-14T12:00:00Z", "updated_at": "2026-05-14T12:00:00Z",
			"latest_job": nil,
		}},
	})
	m.SetRoute("GET", trainCacheSummaryPat, 200, map[string]any{
		"project_id": "proj-1",
		"timestamp":  "2026-05-14T12:00:00Z",
		"file_summaries": []any{
			map[string]any{
				"path": "z-large.bin", "file_type": "file", "size_bytes": 8192,
				"permissions": "rw-r--r--", "modified": "2026-05-14T12:00:00Z",
			},
			map[string]any{
				"path": "a-small.bin", "file_type": "file", "size_bytes": 1024,
				"permissions": "rw-r--r--", "modified": "2026-05-14T12:00:00Z",
			},
		},
	})

	h.Require.NoError(h.Execute("train", "project", "cache", "describe",
		"--project", "my-project", "--sort", "path"))
	out := h.Stdout.String()
	h.Require.Less(strings.Index(out, "a-small.bin"), strings.Index(out, "z-large.bin"))
}

func Test_Train_Project_Cache_Describe_UnknownProject(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", trainProjectsPath, 200, map[string]any{"training_projects": []any{}})

	err := h.Execute("train", "project", "cache", "describe", "--project", "nope")
	h.Require.ErrorContains(err, `no training project found named "nope"`)
	h.Require.Nil(m.FindCall("GET", trainCacheSummaryPat))
}

func Test_Train_Project_Cache_Describe_ByID(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	// An ID match wins over a same-spelled name on another project.
	m.SetRoute("GET", trainProjectsPath, 200, map[string]any{
		"training_projects": []any{
			map[string]any{
				"id": "other", "name": "proj-1",
				"created_at": "2026-05-14T12:00:00Z", "updated_at": "2026-05-14T12:00:00Z",
				"latest_job": nil,
			},
			map[string]any{
				"id": "proj-1", "name": "my-project",
				"created_at": "2026-05-14T12:00:00Z", "updated_at": "2026-05-14T12:00:00Z",
				"latest_job": nil,
			},
		},
	})
	m.SetRoute("GET", trainCacheSummaryPat, 200, map[string]any{
		"project_id": "proj-1", "timestamp": "2026-05-14T12:00:00Z", "file_summaries": []any{},
	})

	h.Require.NoError(h.Execute("train", "project", "cache", "describe", "--project", "proj-1"))
	h.Require.NotNil(m.FindCall("GET", trainCacheSummaryPat))
}

func Test_Train_Capacity_Describe(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", trainCapacityPath, 200, map[string]any{
		"gpu_capacities": []any{map[string]any{
			"gpu_type": "H100", "limit": 64, "baseline": 8, "usage_count": 12,
		}},
		"team_gpu_capacities": []any{map[string]any{
			"team_id": "team-1", "team_name": "research",
			"gpu_type": "H100", "limit": 32, "baseline": 4, "usage_count": 6,
		}},
	})

	h.Require.NoError(h.Execute("train", "capacity", "describe"))
	out := h.Stdout.String()
	h.Require.Contains(out, "H100")
	h.Require.Contains(out, "research")
	h.Require.Contains(out, "TEAM")
}

func Test_Train_Capacity_Describe_NoTeams(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", trainCapacityPath, 200, map[string]any{
		"gpu_capacities": []any{map[string]any{
			"gpu_type": "H100", "limit": 64, "baseline": 8, "usage_count": 12,
		}},
	})

	h.Require.NoError(h.Execute("train", "capacity", "describe"))
	h.Require.NotContains(h.Stdout.String(), "TEAM")
}

func Test_Train_Capacity_Describe_TeamsWithoutOrgCapacity(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	// A team limit is enforced whether or not the org has its own capacity, so
	// an org with none must still show the team table rather than reporting
	// nothing configured.
	m.SetRoute("GET", trainCapacityPath, 200, map[string]any{
		"gpu_capacities": []any{},
		"team_gpu_capacities": []any{map[string]any{
			"team_id": "team-1", "team_name": "research",
			"gpu_type": "H100", "limit": 32, "baseline": 4, "usage_count": 6,
		}},
	})

	h.Require.NoError(h.Execute("train", "capacity", "describe"))
	out := h.Stdout.String()
	h.Require.Contains(out, "TEAM")
	h.Require.Contains(out, "research")
	h.Require.NotContains(h.Stderr.String(), "No training GPU capacity configured.")
	// The separator between the two tables is not emitted when only one renders.
	h.Require.False(strings.HasPrefix(out, "\n"))
}

func Test_Train_Capacity_Describe_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", trainCapacityPath, 200, map[string]any{"gpu_capacities": []any{}})

	h.Require.NoError(h.Execute("train", "capacity", "describe"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No training GPU capacity configured.")
}

func Test_Train_Capacity_Update(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/teams", 200, map[string]any{
		"teams": []any{map[string]any{"id": "team-1", "name": "research"}},
	})
	m.SetRoute("PATCH", trainCapacityPath, 200, map[string]any{
		"team_gpu_capacity": map[string]any{
			"team_id": "team-1", "team_name": "research",
			"gpu_type": "H100", "limit": 32, "baseline": 4, "usage_count": 6,
		},
	})

	h.Require.NoError(h.Execute("train", "capacity", "update",
		"--team", "research", "--gpu-type", "H100", "--max-gpus", "32"))
	body := m.FindCall("PATCH", trainCapacityPath).BodyJSON(t)
	h.Require.Equal("team-1", body["team_id"])
	h.Require.Equal("H100", body["gpu_type"])
	h.Require.Equal(float64(32), body["max_gpus"])
	h.Require.Contains(h.Stderr.String(), "Set H100 limit to 32 GPUs for team research")
}

func Test_Train_Capacity_Update_NegativeRejected(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("train", "capacity", "update",
		"--team", "research", "--gpu-type", "H100", "--max-gpus", "-1")
	h.Require.ErrorContains(err, "--max-gpus must be zero or a positive number")
	h.Require.Empty(m.Calls())
}

func Test_Train_Job_Logs_Tail_StopsOnTerminalStatus(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_RUNNING"))
	m.SetRoute("GET", trainJobPath+"/logs", 200, logsResponse(
		map[string]any{"timestamp": "1", "message": "step 1", "replica": nil},
	))
	m.SetRoute("GET", trainJobPath, 200, map[string]any{
		"training_job": trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"),
		"training_project": map[string]any{
			"id": "proj-1", "name": "my-project",
			"created_at": "2026-05-14T12:00:00Z", "updated_at": "2026-05-14T12:00:00Z",
			"latest_job": nil,
		},
	})
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })

	h.Require.NoError(h.Execute("train", "job", "logs", "--job-id", "job-1", "--tail"))
	h.Require.Contains(h.Stdout.String(), "step 1")
	h.Require.Contains(h.Stderr.String(), "completed")
}

func Test_Train_Job_Logs_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"))
	m.SetRoute("GET", trainJobPath+"/logs", 200, logsResponse())

	h.Require.NoError(h.Execute("train", "job", "logs", "--job-id", "job-1"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No logs found.")
}

func Test_Train_Job_Logs_Tail_StopsOnTerminalStatusWithoutLogs(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_RUNNING"))
	// A job that finished before the log window opened returns nothing, so there
	// is no log line to trigger the status check that ends the tail.
	m.SetRoute("GET", trainJobPath+"/logs", 200, logsResponse())
	m.SetRoute("GET", trainJobPath, 200, map[string]any{
		"training_job": trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"),
		"training_project": map[string]any{
			"id": "proj-1", "name": "my-project",
			"created_at": "2026-05-14T12:00:00Z", "updated_at": "2026-05-14T12:00:00Z",
			"latest_job": nil,
		},
	})
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })

	h.Require.NoError(h.Execute("train", "job", "logs", "--job-id", "job-1", "--tail"))
	h.Require.Contains(h.Stderr.String(), "completed")
}

func Test_Train_Job_Logs_Tail_KeepsGoingWhileQueued(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_QUEUED"))
	// A queued job has produced no logs yet, so the first-poll status check must
	// not mistake "nothing to show" for "nothing left to show".
	logsCalls := 0
	m.SetRouteFunc("GET", trainJobPath+"/logs", func(w http.ResponseWriter, _ *http.Request) {
		logsCalls++
		payload := logsResponse()
		if logsCalls > 1 {
			payload = logsResponse(map[string]any{"timestamp": "1", "message": "step 1", "replica": nil})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
	statusCalls := 0
	m.SetRouteFunc("GET", trainJobPath, func(w http.ResponseWriter, _ *http.Request) {
		statusCalls++
		status := "TRAINING_JOB_QUEUED"
		if statusCalls > 1 {
			status = "TRAINING_JOB_COMPLETED"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"training_job": trainJobFixture("job-1", status),
			"training_project": map[string]any{
				"id": "proj-1", "name": "my-project",
				"created_at": "2026-05-14T12:00:00Z", "updated_at": "2026-05-14T12:00:00Z",
				"latest_job": nil,
			},
		})
	})
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })

	h.Require.NoError(h.Execute("train", "job", "logs", "--job-id", "job-1", "--tail"))
	h.Require.Contains(h.Stdout.String(), "step 1")
}

func Test_Train_Job_Logs_Tail_StopsOnUnknownStatus(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_RUNNING"))
	m.SetRoute("GET", trainJobPath+"/logs", 200, logsResponse(
		map[string]any{"timestamp": "1", "message": "step 1", "replica": nil},
	))
	// A status this build predates is treated as terminal, so the tail ends
	// rather than polling a job that will never produce more logs.
	m.SetRoute("GET", trainJobPath, 200, map[string]any{
		"training_job": trainJobFixture("job-1", "TRAINING_JOB_HIBERNATING"),
		"training_project": map[string]any{
			"id": "proj-1", "name": "my-project",
			"created_at": "2026-05-14T12:00:00Z", "updated_at": "2026-05-14T12:00:00Z",
			"latest_job": nil,
		},
	})
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })

	h.Require.NoError(h.Execute("train", "job", "logs", "--job-id", "job-1", "--tail"))
	h.Require.Contains(h.Stderr.String(), "hibernating")
}

func Test_Train_Job_Download_MultipleArtifacts(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	mockTrainJobSearch(m, trainJobFixture("job-1", "TRAINING_JOB_COMPLETED"))
	archive := trainArtifactTar(t, "train.py", "print('hi')")
	for _, path := range []string{"/artifact-1.tgz", "/artifact-2.tgz"} {
		m.SetRouteFunc("GET", path, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(archive)
		})
	}
	m.SetRoute("GET", trainJobPath+"/download", 200, map[string]any{
		"artifact_presigned_urls": []any{m.URL + "/artifact-1.tgz", m.URL + "/artifact-2.tgz"},
	})
	out := filepath.Join(t.TempDir(), "job.tar")

	h.Require.NoError(h.Execute("train", "job", "download", "--job-id", "job-1", "--out-file", out))
	// Only the first artifact is fetched, and the extras are called out rather
	// than dropped silently.
	h.Require.Contains(h.Stderr.String(), "has 2 artifacts; downloading the first")
	h.Require.NotNil(m.FindCall("GET", "/artifact-1.tgz"))
	h.Require.Nil(m.FindCall("GET", "/artifact-2.tgz"))
}

func Test_Train_Push_ForwardsFlags(t *testing.T) {
	h, fake := newTrussHarness(t)

	h.Require.NoError(h.Execute("train", "push",
		"--config", "./train.py",
		"--job-name", "sweep-3",
		"--team", "research",
		"--accelerator", "H200:8",
		"--node-count", "2",
		"--entrypoint", "./run.sh",
		"--priority", "10",
		"--spot",
		"--interactive", "on-failure",
		"--interactive-timeout", "90m",
	))

	h.Require.Equal([]string{
		"uv", "tool", "run", "truss@latest", "train", "push", "./train.py",
		"--job-name", "sweep-3",
		"--team", "research",
		"--accelerator", "H200:8",
		"--node-count", "2",
		"--entrypoint", "./run.sh",
		"--priority", "10",
		"--spot",
		// Hyphenated here, underscored downstream, and a duration in whole minutes.
		"--interactive", "on_failure",
		"--interactive-timeout-minutes", "90",
	}, fake.only(t).Args)
}

func Test_Train_Push_RejectsSubMinuteInteractiveTimeout(t *testing.T) {
	h, fake := newTrussHarness(t)

	// A sub-minute value truncates to zero minutes, which would be forwarded as
	// nothing at all and leave truss applying its own default.
	err := h.Execute("train", "push", "--config", "./train.py", "--interactive-timeout", "45s")
	h.Require.ErrorContains(err, "--interactive-timeout must be at least 1m")
	h.Require.Empty(fake.calls)
}

func Test_Train_Push_OmitsUnsetFlags(t *testing.T) {
	h, fake := newTrussHarness(t)

	h.Require.NoError(h.Execute("train", "push", "--config", "./train.py"))

	h.Require.Equal([]string{"uv", "tool", "run", "truss@latest", "train", "push", "./train.py"}, fake.only(t).Args)
}

func Test_Train_CheckpointDeploy_ForwardsFlags(t *testing.T) {
	h, fake := newTrussHarness(t)

	h.Require.NoError(h.Execute("train", "checkpoint", "deploy",
		"--job-id", "job-1", "--config-out-dir", "./generated", "--dry-run"))

	h.Require.Equal([]string{
		"uv", "tool", "run", "truss@latest", "train", "deploy_checkpoints",
		"--job-id", "job-1",
		"--truss-config-output-dir", "./generated",
		"--dry-run",
	}, fake.only(t).Args)
}

func Test_Train_CheckpointDeploy_RequiresJobOrConfig(t *testing.T) {
	h, fake := newTrussHarness(t)
	_ = h.Execute("train", "checkpoint", "deploy")
	h.Require.True(h.Exited())
	// Rejected before truss runs, so it never falls back to the latest job.
	h.Require.Empty(fake.calls)
	h.Require.Contains(h.Stderr.String(), "--job-id is required unless --config")
}

func Test_Train_CheckpointDeploy_ConfigWithoutJob(t *testing.T) {
	h, fake := newTrussHarness(t)

	h.Require.NoError(h.Execute("train", "checkpoint", "deploy", "--config", "./deploy.py"))

	h.Require.Equal([]string{
		"uv", "tool", "run", "truss@latest", "train", "deploy_checkpoints", "--config", "./deploy.py",
	}, fake.only(t).Args)
}

func Test_Train_Init_ForwardsFlagsWithoutAuth(t *testing.T) {
	h, fake := newTrussHarness(t)

	h.Require.NoError(h.Execute("train", "init", "--dir", "./out", "--example", "sft-lora", "--example", "grpo"))

	c := fake.only(t)
	h.Require.Equal([]string{
		"uv", "tool", "run", "truss@latest", "train", "init",
		"--target-directory", "./out",
		"--examples", "sft-lora,grpo",
	}, c.Args)
	// Scaffolding calls no API, so no credential is forwarded.
	h.Require.NotContains(strings.Join(c.Env, " "), "BASETEN_TRUSS_AUTH_")
}

func Test_Train_Init_ListExamples(t *testing.T) {
	h, fake := newTrussHarness(t)

	h.Require.NoError(h.Execute("train", "init", "--list-examples"))

	h.Require.Equal([]string{
		"uv", "tool", "run", "truss@latest", "train", "init", "--list-examples",
	}, fake.only(t).Args)
}

func Test_Train_WorkstationCreate_ForwardsFlags(t *testing.T) {
	h, fake := newTrussHarness(t)

	h.Require.NoError(h.Execute("train", "workstation", "create",
		"--node-count", "4",
		"--project", "my-workstation",
		"--team", "research",
		"--image", "myrepo/base:1",
		"--enable-checkpointing",
		"--checkpoint-path", "/checkpoints",
		"--checkpoint-volume-size", "512",
		"--checkpoint-from-job", "job-1",
	))

	c := fake.only(t)
	h.Require.Equal([]string{
		"uv", "tool", "run", "truss@latest", "train", "workstation",
		// Defaulted rather than omitted, so the workstation shape is explicit.
		"--accelerator", "H100",
		"--node-count", "4",
		"--image", "myrepo/base:1",
		// The project is named, not identified, which is what truss's flag means.
		"--project-id", "my-workstation",
		"--team", "research",
		"--orchestrator", "slurm",
		"--enable-checkpointing",
		"--checkpoint-path", "/checkpoints",
		"--checkpoint-volume-size", "512",
		"--checkpoint-from-job", "job-1",
	}, c.Args)
	h.Require.Contains(c.Env, "BASETEN_TRUSS_AUTH_API_KEY=test-key")
}
