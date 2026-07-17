package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
	"gopkg.in/yaml.v3"
)

const (
	modelImageConfigFileName = "config.yaml"
	modelImageDockerfileName = "Dockerfile"
	modelImageDefaultTag     = "baseten-model:latest"
)

func init() {
	Register("model image build", commandModelImageBuild)
	Register("model image prepare", commandModelImagePrepare)
}

func commandModelImageBuild(ctx *CommandContext, flags *cmd.ModelImageBuildFlags) error {
	if err := modelImageRequireUV(ctx); err != nil {
		return err
	}
	if _, err := ctx.Execer().LookPath("docker"); err != nil {
		return cmd.NewErrUsagef("docker not found on PATH, required to build the image")
	}

	// Everything after `--` is forwarded verbatim to `docker build`; no
	// positional arguments are allowed before it.
	passthrough, err := modelImageDockerPassthroughArgs(ctx)
	if err != nil {
		return err
	}

	// Resolve the build context dir: an explicit --build-dir is kept, otherwise
	// a temp dir is used and removed afterward.
	buildDir := flags.BuildDir
	if buildDir == "" {
		tmp, err := os.MkdirTemp("", "baseten-image-*")
		if err != nil {
			return fmt.Errorf("create temp build dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		buildDir = tmp
	} else if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return fmt.Errorf("create build dir: %w", err)
	}

	if err := modelImageBuildContext(ctx, flags.TrussVersion, buildDir, flags.Dir); err != nil {
		return err
	}

	tag := flags.Tag
	if tag == "" {
		tag = modelImageDeriveDefaultTag(flags.Dir)
	}

	// Capture the image ID via --iidfile. If the user supplied their own in the
	// passthrough, defer to it; otherwise inject a temp file we own.
	iidPath := modelImageUserIIDFilePath(passthrough)
	inject := iidPath == ""
	if inject {
		f, err := os.CreateTemp("", "baseten-iid-*")
		if err != nil {
			return fmt.Errorf("create iidfile: %w", err)
		}
		iidPath = f.Name()
		_ = f.Close()
		defer func() { _ = os.Remove(iidPath) }()
	}

	dockerArgs := []string{"build", "-t", tag}
	if inject {
		dockerArgs = append(dockerArgs, "--iidfile", iidPath)
	}
	dockerArgs = append(dockerArgs, passthrough...)
	dockerArgs = append(dockerArgs, buildDir)

	build := exec.CommandContext(ctx, "docker", dockerArgs...)
	build.Stdin = ctx.Stdin
	// Keep our stdout clean for the result; build progress goes to stderr.
	build.Stdout = ctx.Stderr
	build.Stderr = ctx.Stderr
	if err := modelImageExec(ctx, build); err != nil {
		return err
	}

	imageID := ""
	if b, err := os.ReadFile(iidPath); err == nil {
		imageID = strings.TrimSpace(string(b))
	}

	result := cmd.ModelImageBuildResult{ImageID: imageID, Tag: tag}
	if ctx.JSON {
		ctx.OutputJSON(result)
	} else if imageID != "" {
		ctx.Outputf("Built image %s (%s)\n", tag, imageID)
	} else {
		ctx.Outputf("Built image %s\n", tag)
	}
	return nil
}

func commandModelImagePrepare(ctx *CommandContext, flags *cmd.ModelImagePrepareFlags) error {
	if err := modelImageRequireUV(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(flags.BuildDir, 0o755); err != nil {
		return fmt.Errorf("create build dir: %w", err)
	}
	if err := modelImageBuildContext(ctx, flags.TrussVersion, flags.BuildDir, flags.Dir); err != nil {
		return err
	}

	buildDir, err := filepath.Abs(flags.BuildDir)
	if err != nil {
		buildDir = flags.BuildDir
	}
	result := cmd.ModelImagePrepareResult{
		BuildDir:   buildDir,
		Dockerfile: filepath.Join(buildDir, modelImageDockerfileName),
	}
	if ctx.JSON {
		ctx.OutputJSON(result)
	} else {
		ctx.Outputf("Wrote Docker build context to %s\n", result.BuildDir)
	}
	return nil
}

// modelImageRequireUV confirms the `uv` binary, required to run the truss CLI,
// is on PATH.
func modelImageRequireUV(ctx *CommandContext) error {
	if _, err := ctx.Execer().LookPath("uv"); err != nil {
		return cmd.NewErrUsagef("uv not found on PATH, required to run truss; install from https://docs.astral.sh/uv/")
	}
	return nil
}

// modelImageBuildContext runs `uv tool run truss@<version> image build_context`,
// which generates the Docker build context for dir into buildDir. Its output is
// routed to stderr so our stdout stays clean for the result.
// TRUSS_NO_UPDATE_CHECK suppresses truss's update check and its associated
// disk/network side effects.
func modelImageBuildContext(ctx *CommandContext, trussVersion, buildDir, dir string) error {
	c := exec.CommandContext(ctx, "uv",
		"tool", "run", "truss@"+trussVersion,
		"image", "build-context", buildDir, dir,
	)
	c.Stdin = ctx.Stdin
	c.Stdout = ctx.Stderr
	c.Stderr = ctx.Stderr
	c.Env = append(os.Environ(), "TRUSS_NO_UPDATE_CHECK=1")
	return modelImageExec(ctx, c)
}

// modelImageExec logs the command line to stderr and runs c via the context's
// Execer, which propagates a non-zero exit as an ErrSubprocess.
func modelImageExec(ctx *CommandContext, c *exec.Cmd) error {
	ctx.Logf("+ %s\n", strings.Join(c.Args, " "))
	return ctx.Execer().Exec(c)
}

// modelImageDockerPassthroughArgs returns the arguments after `--`, which are
// forwarded to `docker build`. Any positional argument before `--` is a usage
// error.
func modelImageDockerPassthroughArgs(ctx *CommandContext) ([]string, error) {
	dash := ctx.Command.ArgsLenAtDash()
	if dash == -1 {
		if len(ctx.Args) > 0 {
			return nil, cmd.NewErrUsagef("unexpected arguments %v; pass extra docker build flags after '--'", ctx.Args)
		}
		return nil, nil
	}
	if dash > 0 {
		return nil, cmd.NewErrUsagef("unexpected arguments %v; pass extra docker build flags after '--'", ctx.Args[:dash])
	}
	return ctx.Args[dash:], nil
}

// modelImageUserIIDFilePath returns the value of a user-supplied --iidfile in
// args, or "" if none is present.
func modelImageUserIIDFilePath(args []string) string {
	for i, a := range args {
		if a == "--iidfile" && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, "--iidfile="); ok {
			return v
		}
	}
	return ""
}

// modelImageDeriveDefaultTag builds a default image tag from the model_name in
// the model directory's config.yaml, falling back to modelImageDefaultTag when
// it is missing or unreadable.
func modelImageDeriveDefaultTag(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, modelImageConfigFileName))
	if err != nil {
		return modelImageDefaultTag
	}
	var cfg struct {
		ModelName string `yaml:"model_name"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return modelImageDefaultTag
	}
	if name := modelImageSanitizeDockerTag(cfg.ModelName); name != "" {
		return name + ":latest"
	}
	return modelImageDefaultTag
}

// modelImageSanitizeDockerTag lowercases name and replaces runs of characters
// that are invalid in a Docker image name with a single hyphen, trimming
// leading and trailing separators. Returns "" if nothing usable remains.
func modelImageSanitizeDockerTag(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-._")
}
