package cmd_test

import (
	"archive/tar"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func depFixture(id, name, env, status string) map[string]any {
	d := map[string]any{
		"id":                   id,
		"name":                 name,
		"model_id":             "m-1",
		"status":               status,
		"active_replica_count": 1,
		"is_development":       false,
		"is_production":        env == "production",
		"created_at":           "2026-01-02T03:04:05Z",
		"instance_type_name":   "A10G",
		"autoscaling_settings": map[string]any{},
	}
	if env != "" {
		d["environment"] = env
	} else {
		d["environment"] = nil
	}
	return d
}

func Test_Model_Deployment_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/deployments", 200,
		map[string]any{"deployments": []any{}})

	h.Require.NoError(h.Execute("model", "deployment", "list", "--model-id", "m-1"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No deployments found.")
}

func Test_Model_Deployment_List_Rows(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/deployments", 200,
		map[string]any{"deployments": []any{
			depFixture("d-1", "first", "production", "ACTIVE"),
			depFixture("d-2", "second", "", "INACTIVE"),
		}})

	h.Require.NoError(h.Execute("model", "deployment", "list", "--model-id", "m-1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "ID")
	h.Require.Contains(out, "ENVIRONMENT")
	h.Require.Contains(out, "STATUS")
	h.Require.Contains(out, "d-1")
	h.Require.Contains(out, "production")
	h.Require.Contains(out, "ACTIVE")
	h.Require.Contains(out, "d-2")
	h.Require.Contains(out, "INACTIVE")
}

func Test_Model_Deployment_List_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/deployments", 200,
		map[string]any{"deployments": []any{depFixture("d-1", "first", "production", "ACTIVE")}})

	h.Require.NoError(h.Execute("model", "deployment", "list", "--model-id", "m-1", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"id": "d-1"`)
}

func Test_Model_Deployment_Describe(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/deployments/d-1", 200,
		depFixture("d-1", "first", "production", "ACTIVE"))

	h.Require.NoError(h.Execute("model", "deployment", "describe",
		"--model-id", "m-1", "--deployment-id", "d-1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "ID:")
	h.Require.Contains(out, "d-1")
	h.Require.Contains(out, "production")
	h.Require.Contains(out, "ACTIVE")
}

func Test_Model_Deployment_Describe_MissingDeploymentID(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "describe", "--model-id", "m-1")
	h.Require.Error(err)
}

func Test_Model_Deployment_Describe_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models/m-1/deployments", 200,
		map[string]any{"deployments": []any{depFixture("d-1", "first", "production", "ACTIVE")}})
	m.SetRoute("GET", "/v1/models/m-1/deployments/d-1", 200,
		depFixture("d-1", "first", "production", "ACTIVE"))

	h.Require.NoError(h.Execute("model", "deployment", "describe",
		"--model-id", "m-1", "--deployment-name", "first"))
	h.Require.Contains(h.Stdout.String(), "d-1")
	call := m.FindCall("GET", "/v1/models/m-1/deployments")
	h.Require.NotNil(call)
	h.Require.Equal("first", call.Query().Get("name"))
	h.Require.NotNil(m.FindCall("GET", "/v1/models/m-1/deployments/d-1"))
}

func Test_Model_Deployment_Describe_ByName_NotFound(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/deployments", 200,
		map[string]any{"deployments": []any{}})

	err := h.Execute("model", "deployment", "describe",
		"--model-id", "m-1", "--deployment-name", "ghost")
	h.Require.ErrorContains(err, `no deployment named "ghost"`)
}

func Test_Model_Deployment_Describe_IDAndName_Rejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "describe",
		"--model-id", "m-1", "--deployment-id", "d-1", "--deployment-name", "first")
	h.Require.Error(err)
}

func Test_Model_Deployment_Config_TextRaw(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/deployments/d-1/config", 200,
		map[string]any{"raw_config": "model_name: foo\nresources:\n  cpu: \"1\"\n"})

	h.Require.NoError(h.Execute("model", "deployment", "config",
		"--model-id", "m-1", "--deployment-id", "d-1"))
	h.Require.Equal("model_name: foo\nresources:\n  cpu: \"1\"\n", h.Stdout.String())
}

func Test_Model_Deployment_Config_TextParsedFallback(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/deployments/d-1/config", 200,
		map[string]any{"config": map[string]any{"model_name": "foo"}})

	h.Require.NoError(h.Execute("model", "deployment", "config",
		"--model-id", "m-1", "--deployment-id", "d-1"))
	h.Require.Contains(h.Stdout.String(), "model_name: foo")
}

func Test_Model_Deployment_Config_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/deployments/d-1/config", 200,
		map[string]any{"raw_config": "model_name: foo\n", "config": map[string]any{"model_name": "foo"}})

	h.Require.NoError(h.Execute("model", "deployment", "config",
		"--model-id", "m-1", "--deployment-id", "d-1", "--output", "json"))
	out := h.Stdout.String()
	h.Require.Contains(out, `"raw_config"`)
	h.Require.Contains(out, `"config"`)
}

func Test_Model_Deployment_Activate(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/models/m-1/deployments/d-1/activate", 200,
		map[string]any{"deployment_id": "d-1"})

	h.Require.NoError(h.Execute("model", "deployment", "activate",
		"--model-id", "m-1", "--deployment-id", "d-1"))
	h.Require.NotNil(m.FindCall("POST", "/v1/models/m-1/deployments/d-1/activate"))
	h.Require.Contains(h.Stderr.String(), "Activated deployment d-1")
}

func Test_Model_Deployment_Deactivate_Yes(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/models/m-1/deployments/d-1/deactivate", 200,
		map[string]any{"deployment_id": "d-1"})

	h.Require.NoError(h.Execute("model", "deployment", "deactivate",
		"--model-id", "m-1", "--deployment-id", "d-1", "--yes"))
	h.Require.NotNil(m.FindCall("POST", "/v1/models/m-1/deployments/d-1/deactivate"))
	h.Require.Contains(h.Stderr.String(), "Deactivated deployment d-1")
}

func Test_Model_Deployment_Deactivate_NoTTY_RequiresYes(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("model", "deployment", "deactivate",
		"--model-id", "m-1", "--deployment-id", "d-1")
	h.Require.ErrorContains(err, "stdin is not a terminal")
	h.Require.Nil(m.FindCall("POST", "/v1/models/m-1/deployments/d-1/deactivate"))
}

func Test_Model_Deployment_Delete_Yes(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", "/v1/models/m-1/deployments/d-1", 200,
		map[string]any{"id": "d-1", "model_id": "m-1", "deleted": true})

	h.Require.NoError(h.Execute("model", "deployment", "delete",
		"--model-id", "m-1", "--deployment-id", "d-1", "--yes"))
	h.Require.NotNil(m.FindCall("DELETE", "/v1/models/m-1/deployments/d-1"))
	h.Require.Contains(h.Stderr.String(), "Deleted deployment d-1")
}

func Test_Model_Deployment_Delete_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("DELETE", "/v1/models/m-1/deployments/d-1", 200,
		map[string]any{"id": "d-1", "model_id": "m-1", "deleted": true})

	h.Require.NoError(h.Execute("model", "deployment", "delete",
		"--model-id", "m-1", "--deployment-id", "d-1", "--yes", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"deleted": true`)
}

func Test_Model_Deployment_Delete_NoTTY_RequiresYes(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("model", "deployment", "delete",
		"--model-id", "m-1", "--deployment-id", "d-1")
	h.Require.ErrorContains(err, "stdin is not a terminal")
	h.Require.Nil(m.FindCall("DELETE", "/v1/models/m-1/deployments/d-1"))
}

func Test_Model_Deployment_Promote_Default(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRouteFunc("POST", "/v1/models/m-1/environments/production/promote",
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"id":"d-1","name":"first","model_id":"m-1","status":"ACTIVE","active_replica_count":1,"is_development":false,"is_production":true,"created_at":"2026-01-02T03:04:05Z","environment":"production","autoscaling_settings":{}}`))
		})

	h.Require.NoError(h.Execute("model", "deployment", "promote",
		"--model-id", "m-1", "--deployment-id", "d-1", "--yes"))
	call := m.FindCall("POST", "/v1/models/m-1/environments/production/promote")
	h.Require.NotNil(call)
	h.Require.Contains(call.Body, `"deployment_id":"d-1"`)
	h.Require.Contains(call.Body, `"preserve_env_instance_type":true`)
	h.Require.Contains(h.Stderr.String(), "Promoted deployment d-1 to environment production")
}

func Test_Model_Deployment_Promote_OverrideInstanceType(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/models/m-1/environments/staging/promote", 200,
		map[string]any{"id": "d-1"})

	h.Require.NoError(h.Execute("model", "deployment", "promote",
		"--model-id", "m-1", "--deployment-id", "d-1",
		"--environment", "staging", "--override-env-instance-type", "--yes"))
	call := m.FindCall("POST", "/v1/models/m-1/environments/staging/promote")
	h.Require.NotNil(call)
	h.Require.Contains(call.Body, `"preserve_env_instance_type":false`)
}

func Test_Model_Deployment_Promote_NoTTY_RequiresYes(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("model", "deployment", "promote",
		"--model-id", "m-1", "--deployment-id", "d-1")
	h.Require.ErrorContains(err, "stdin is not a terminal")
	h.Require.Nil(m.FindCall("POST", "/v1/models/m-1/environments/production/promote"))
}

func Test_Model_Deployment_Replica_Terminate_Yes(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", "/v1/models/m-1/deployments/d-1/replicas/r-1", 200,
		map[string]any{"replica_id": "r-1"})

	h.Require.NoError(h.Execute("model", "deployment", "replica", "terminate",
		"--model-id", "m-1", "--deployment-id", "d-1", "--replica-id", "r-1", "--yes"))
	h.Require.NotNil(m.FindCall("DELETE", "/v1/models/m-1/deployments/d-1/replicas/r-1"))
	h.Require.Contains(h.Stderr.String(), "Terminated replica r-1 of deployment d-1")
}

func Test_Model_Deployment_Replica_Terminate_NoTTY_RequiresYes(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("model", "deployment", "replica", "terminate",
		"--model-id", "m-1", "--deployment-id", "d-1", "--replica-id", "r-1")
	h.Require.ErrorContains(err, "stdin is not a terminal")
	h.Require.Nil(m.FindCall("DELETE", "/v1/models/m-1/deployments/d-1/replicas/r-1"))
}

func Test_Model_Deployment_Download_RequiresOutFlag(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "download",
		"--model-id", "m-1", "--deployment-id", "d-1")
	h.Require.ErrorContains(err, "out-file")
	h.Require.ErrorContains(err, "out-dir")
}

func Test_Model_Deployment_Download_OutFile(t *testing.T) {
	body := []byte("tar-bytes-go-here")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/deployments/d-1/download", 200,
		map[string]any{"download_url": srv.URL})

	outFile := filepath.Join(t.TempDir(), "truss.tar")
	h.Require.NoError(h.Execute("model", "deployment", "download",
		"--model-id", "m-1", "--deployment-id", "d-1", "--out-file", outFile))

	got, err := os.ReadFile(outFile)
	h.Require.NoError(err)
	h.Require.Equal(body, got)
	h.Require.Contains(h.Stderr.String(), "Saved to "+outFile)
}

func Test_Model_Deployment_Download_OutFile_ExistsWithoutOverwrite(t *testing.T) {
	h := NewCommandHarness(t)

	outFile := filepath.Join(t.TempDir(), "truss.tar")
	h.Require.NoError(os.WriteFile(outFile, []byte("x"), 0o644))

	err := h.Execute("model", "deployment", "download",
		"--model-id", "m-1", "--deployment-id", "d-1", "--out-file", outFile)
	h.Require.ErrorContains(err, "file already exists")
}

func Test_Model_Deployment_Download_OutDir(t *testing.T) {
	tarBuf := buildTar(t, map[string]string{
		"config.yaml":    "model_name: foo\n",
		"model/model.py": "print('hi')\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(tarBuf)
	}))
	defer srv.Close()

	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/deployments/d-1/download", 200,
		map[string]any{"download_url": srv.URL})

	outDir := filepath.Join(t.TempDir(), "truss")
	h.Require.NoError(h.Execute("model", "deployment", "download",
		"--model-id", "m-1", "--deployment-id", "d-1", "--out-dir", outDir))

	cfg, err := os.ReadFile(filepath.Join(outDir, "config.yaml"))
	h.Require.NoError(err)
	h.Require.Equal("model_name: foo\n", string(cfg))
	model, err := os.ReadFile(filepath.Join(outDir, "model", "model.py"))
	h.Require.NoError(err)
	h.Require.Equal("print('hi')\n", string(model))
}

func Test_Model_Deployment_Describe_ShowsSettings(t *testing.T) {
	h := NewCommandHarness(t)
	dep := depFixture("d-1", "first", "production", "ACTIVE")
	dep["autoscaling_settings"] = map[string]any{
		"min_replica": 1, "max_replica": 5, "concurrency_target": 2,
		"autoscaling_window": 600, "max_scale_down_rate": nil,
	}
	dep["request_backpressure_settings"] = map[string]any{"policy": nil}
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/deployments/d-1", 200, dep)

	h.Require.NoError(h.Execute("model", "deployment", "describe",
		"--model-id", "m-1", "--deployment-id", "d-1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Backpressure: none")
	h.Require.Contains(out, "Autoscaling:")
	h.Require.Contains(out, "Min Replicas:            1")
	h.Require.Contains(out, "Autoscaling Window:      600s")
	h.Require.Contains(out, "Max Scale Down Rate:     -")
}

func Test_Model_Deployment_Rename(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("PATCH", "/v1/models/m-1/deployments/d-1", 200,
		depFixture("d-1", "canary", "", "ACTIVE"))

	h.Require.NoError(h.Execute("model", "deployment", "rename",
		"--model-id", "m-1", "--deployment-id", "d-1", "--new-name", "canary"))
	h.Require.Contains(h.Stderr.String(), "Renamed deployment d-1 to canary")
	body := h.MockManagementAPI().FindCall("PATCH", "/v1/models/m-1/deployments/d-1").BodyJSON(h.T)
	h.Require.Equal("canary", body["name"])
}

// Only the flags that were passed reach the request body. Anything else would
// overwrite settings the caller never mentioned.
func Test_Model_Deployment_UpdateAutoscaling_OmitsUnsetFlags(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("PATCH", "/v1/models/m-1/deployments/d-1/autoscaling_settings", 200,
		map[string]any{"status": "ACCEPTED", "message": "Update accepted"})

	h.Require.NoError(h.Execute("model", "deployment", "update-autoscaling",
		"--model-id", "m-1", "--deployment-id", "d-1", "--min-replica", "2"))
	h.Require.Contains(h.Stderr.String(), "ACCEPTED: Update accepted")

	body := h.MockManagementAPI().FindCall(
		"PATCH", "/v1/models/m-1/deployments/d-1/autoscaling_settings").BodyJSON(h.T)
	h.Require.Len(body, 1)
	h.Require.Equal(float64(2), body["min_replica"])
}

// Zero is a real replica count, so it has to survive as a value rather than
// being treated as "unset" the way a plain int would be.
func Test_Model_Deployment_UpdateAutoscaling_ZeroIsSent(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("PATCH", "/v1/models/m-1/deployments/d-1/autoscaling_settings", 200,
		map[string]any{"status": "ACCEPTED", "message": "Update accepted"})

	h.Require.NoError(h.Execute("model", "deployment", "update-autoscaling",
		"--model-id", "m-1", "--deployment-id", "d-1", "--min-replica", "0"))

	body := h.MockManagementAPI().FindCall(
		"PATCH", "/v1/models/m-1/deployments/d-1/autoscaling_settings").BodyJSON(h.T)
	h.Require.Equal(float64(0), body["min_replica"])
}

func Test_Model_Deployment_UpdateAutoscaling_RejectsNull(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "update-autoscaling",
		"--model-id", "m-1", "--deployment-id", "d-1", "--min-replica", "null")
	h.Require.Error(err)
}

func Test_Model_Deployment_UpdateRequestBackpressure_SetsPolicy(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute(
		"PATCH", "/v1/models/m-1/deployments/d-1/request_backpressure_settings", 200,
		map[string]any{"policy": "REJECT_ON_FULL"})

	h.Require.NoError(h.Execute("model", "deployment", "update-request-backpressure",
		"--model-id", "m-1", "--deployment-id", "d-1", "--policy", "reject-on-full"))
	h.Require.Contains(h.Stderr.String(), "Request backpressure policy: reject-on-full")

	body := h.MockManagementAPI().FindCall(
		"PATCH", "/v1/models/m-1/deployments/d-1/request_backpressure_settings").BodyJSON(h.T)
	h.Require.Equal("REJECT_ON_FULL", body["policy"])
}

// Clearing the policy has to send an explicit null. Omitting the field would
// read as "leave unchanged", so the policy would survive.
func Test_Model_Deployment_UpdateRequestBackpressure_NullClearsPolicy(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute(
		"PATCH", "/v1/models/m-1/deployments/d-1/request_backpressure_settings", 200,
		map[string]any{"policy": nil})

	h.Require.NoError(h.Execute("model", "deployment", "update-request-backpressure",
		"--model-id", "m-1", "--deployment-id", "d-1", "--policy", "null"))
	h.Require.Contains(h.Stderr.String(), "Request backpressure policy: none")

	call := h.MockManagementAPI().FindCall(
		"PATCH", "/v1/models/m-1/deployments/d-1/request_backpressure_settings")
	h.Require.Equal(`{"policy":null}`, strings.TrimSpace(call.Body))
}

func Test_Model_Deployment_UpdateRequestBackpressure_RejectsUnknownPolicy(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "update-request-backpressure",
		"--model-id", "m-1", "--deployment-id", "d-1", "--policy", "REJECT_ON_FULL")
	h.Require.Error(err)
}

func buildTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		t.Helper()
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}
