package cmd_test

import (
	"archive/tar"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
