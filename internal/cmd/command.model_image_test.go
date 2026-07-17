package cmd_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

// modelImageFakeExecer implements cmd.Execer for the model image command tests,
// recording calls and simulating docker's --iidfile write so nothing spawns a
// real process.
type modelImageFakeExecer struct {
	available map[string]bool
	calls     []*exec.Cmd
}

func newModelImageFakeExecer(available ...string) *modelImageFakeExecer {
	m := map[string]bool{}
	for _, a := range available {
		m[a] = true
	}
	return &modelImageFakeExecer{available: m}
}

func (f *modelImageFakeExecer) LookPath(name string) (string, error) {
	if f.available[name] {
		return "/fake/" + name, nil
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func (f *modelImageFakeExecer) Exec(c *exec.Cmd) error {
	f.calls = append(f.calls, c)
	if filepath.Base(c.Args[0]) == "docker" {
		if p := modelImageArgValue(c.Args, "--iidfile"); p != "" {
			_ = os.WriteFile(p, []byte("sha256:test\n"), 0o644)
		}
	}
	return nil
}

// call returns the first recorded command whose program is name.
func (f *modelImageFakeExecer) call(name string) *exec.Cmd {
	for _, c := range f.calls {
		if filepath.Base(c.Args[0]) == name {
			return c
		}
	}
	return nil
}

// modelImageArgValue returns the value following flag (or the "flag=value" form).
func modelImageArgValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v
		}
	}
	return ""
}

func modelImageArgCount(args []string, flag string) int {
	n := 0
	for _, a := range args {
		if a == flag {
			n++
		}
	}
	return n
}

// modelImageWriteDir creates a temp model directory, optionally with a
// config.yaml holding the given contents.
func modelImageWriteDir(t *testing.T, configYAML string) string {
	t.Helper()
	dir := t.TempDir()
	if configYAML != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(configYAML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func Test_Model_Image_Build_UVNotOnPath(t *testing.T) {
	h := NewCommandHarness(t)
	h.Context = cmd.WithExecer(h.Context, newModelImageFakeExecer())
	_ = h.Execute("model", "image", "build")
	h.Require.True(h.Exited())
	h.Require.Contains(h.Stderr.String(), "uv not found")
}

func Test_Model_Image_Build_DockerNotOnPath(t *testing.T) {
	h := NewCommandHarness(t)
	h.Context = cmd.WithExecer(h.Context, newModelImageFakeExecer("uv"))
	_ = h.Execute("model", "image", "build")
	h.Require.True(h.Exited())
	h.Require.Contains(h.Stderr.String(), "docker not found")
}

func Test_Model_Image_Build_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	fake := newModelImageFakeExecer("uv", "docker")
	h.Context = cmd.WithExecer(h.Context, fake)
	modelDir := modelImageWriteDir(t, "model_name: test-model\n")

	h.Require.NoError(h.Execute("model", "image", "build", "--dir", modelDir, "--output", "json"))

	var result map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Equal("sha256:test", result["image_id"])
	h.Require.Equal("test-model:latest", result["tag"])

	uv := fake.call("uv")
	docker := fake.call("docker")
	h.Require.NotNil(uv)
	h.Require.NotNil(docker)
	// truss writes the context into the same dir docker builds from.
	buildDir := uv.Args[len(uv.Args)-2] // ...build-context <buildDir> <dir>
	h.Require.Equal(modelDir, uv.Args[len(uv.Args)-1])
	h.Require.Contains(uv.Env, "TRUSS_NO_UPDATE_CHECK=1")
	h.Require.Equal("build", docker.Args[1])
	h.Require.Equal("test-model:latest", modelImageArgValue(docker.Args, "-t"))
	h.Require.NotEmpty(modelImageArgValue(docker.Args, "--iidfile"))
	h.Require.Equal(buildDir, docker.Args[len(docker.Args)-1])
}

func Test_Model_Image_Build_DefaultTagFromConfig(t *testing.T) {
	h := NewCommandHarness(t)
	fake := newModelImageFakeExecer("uv", "docker")
	h.Context = cmd.WithExecer(h.Context, fake)
	modelDir := modelImageWriteDir(t, "model_name: My Model\n")

	h.Require.NoError(h.Execute("model", "image", "build", "--dir", modelDir, "--output", "json"))

	var result map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Equal("my-model:latest", result["tag"])
}

func Test_Model_Image_Build_DefaultTagFallback(t *testing.T) {
	h := NewCommandHarness(t)
	fake := newModelImageFakeExecer("uv", "docker")
	h.Context = cmd.WithExecer(h.Context, fake)
	modelDir := modelImageWriteDir(t, "") // no config.yaml

	h.Require.NoError(h.Execute("model", "image", "build", "--dir", modelDir, "--output", "json"))

	var result map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Equal("baseten-model:latest", result["tag"])
}

func Test_Model_Image_Build_TagOverride(t *testing.T) {
	h := NewCommandHarness(t)
	fake := newModelImageFakeExecer("uv", "docker")
	h.Context = cmd.WithExecer(h.Context, fake)
	modelDir := modelImageWriteDir(t, "model_name: test-model\n")

	h.Require.NoError(h.Execute("model", "image", "build", "--dir", modelDir, "--tag", "custom:v1", "--output", "json"))

	var result map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Equal("custom:v1", result["tag"])
	h.Require.Equal("custom:v1", modelImageArgValue(fake.call("docker").Args, "-t"))
}

func Test_Model_Image_Build_TrussVersion(t *testing.T) {
	h := NewCommandHarness(t)
	fake := newModelImageFakeExecer("uv", "docker")
	h.Context = cmd.WithExecer(h.Context, fake)
	modelDir := modelImageWriteDir(t, "model_name: test-model\n")

	h.Require.NoError(h.Execute("model", "image", "build", "--dir", modelDir, "--truss-version", "0.9.0"))

	h.Require.Equal("truss@0.9.0", fake.call("uv").Args[3])
}

func Test_Model_Image_Build_Passthrough(t *testing.T) {
	h := NewCommandHarness(t)
	fake := newModelImageFakeExecer("uv", "docker")
	h.Context = cmd.WithExecer(h.Context, fake)
	modelDir := modelImageWriteDir(t, "model_name: test-model\n")

	h.Require.NoError(h.Execute("model", "image", "build", "--dir", modelDir, "--", "--build-arg", "X=Y", "--no-cache"))

	docker := fake.call("docker")
	h.Require.Contains(strings.Join(docker.Args, " "), "--build-arg X=Y --no-cache")
	// Passthrough sits before the build dir; our --iidfile is still injected once.
	h.Require.Equal(1, modelImageArgCount(docker.Args, "--iidfile"))
	h.Require.NotEmpty(modelImageArgValue(docker.Args, "--iidfile"))
}

func Test_Model_Image_Build_UserIIDFile(t *testing.T) {
	h := NewCommandHarness(t)
	fake := newModelImageFakeExecer("uv", "docker")
	h.Context = cmd.WithExecer(h.Context, fake)
	modelDir := modelImageWriteDir(t, "model_name: test-model\n")
	userIID := filepath.Join(t.TempDir(), "iid.txt")

	h.Require.NoError(h.Execute("model", "image", "build", "--dir", modelDir, "--output", "json", "--", "--iidfile", userIID))

	docker := fake.call("docker")
	// We defer to the user's --iidfile rather than injecting a second one.
	h.Require.Equal(1, modelImageArgCount(docker.Args, "--iidfile"))
	h.Require.Equal(userIID, modelImageArgValue(docker.Args, "--iidfile"))

	var result map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Equal("sha256:test", result["image_id"])
}

func Test_Model_Image_Build_PositionalRejected(t *testing.T) {
	h := NewCommandHarness(t)
	h.Context = cmd.WithExecer(h.Context, newModelImageFakeExecer("uv", "docker"))
	_ = h.Execute("model", "image", "build", "foo")
	h.Require.True(h.Exited())
	h.Require.Contains(h.Stderr.String(), "unexpected arguments")
}

func Test_Model_Image_Build_Text(t *testing.T) {
	h := NewCommandHarness(t)
	fake := newModelImageFakeExecer("uv", "docker")
	h.Context = cmd.WithExecer(h.Context, fake)
	modelDir := modelImageWriteDir(t, "model_name: test-model\n")

	h.Require.NoError(h.Execute("model", "image", "build", "--dir", modelDir))

	h.Require.Contains(h.Stdout.String(), "Built image test-model:latest (sha256:test)")
	h.Require.Contains(h.Stderr.String(), "+ uv tool run truss@latest")
	h.Require.Contains(h.Stderr.String(), "+ docker build")
}

func Test_Model_Image_Prepare_UVNotOnPath(t *testing.T) {
	h := NewCommandHarness(t)
	h.Context = cmd.WithExecer(h.Context, newModelImageFakeExecer())
	_ = h.Execute("model", "image", "prepare", "--build-dir", t.TempDir())
	h.Require.True(h.Exited())
	h.Require.Contains(h.Stderr.String(), "uv not found")
}

func Test_Model_Image_Prepare_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	fake := newModelImageFakeExecer("uv")
	h.Context = cmd.WithExecer(h.Context, fake)
	modelDir := modelImageWriteDir(t, "model_name: test-model\n")
	buildDir := filepath.Join(t.TempDir(), "ctx")

	h.Require.NoError(h.Execute("model", "image", "prepare", "--dir", modelDir, "--build-dir", buildDir, "--output", "json"))

	var result map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	absBuild, _ := filepath.Abs(buildDir)
	h.Require.Equal(absBuild, result["build_dir"])
	h.Require.Equal(filepath.Join(absBuild, "Dockerfile"), result["dockerfile"])

	uv := fake.call("uv")
	h.Require.NotNil(uv)
	h.Require.Equal([]string{"uv", "tool", "run", "truss@latest", "image", "build-context", buildDir, modelDir}, uv.Args)
	h.Require.Contains(uv.Env, "TRUSS_NO_UPDATE_CHECK=1")
}
