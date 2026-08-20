package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/deploymentpatch"
	"github.com/basetenlabs/baseten-go/client"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/basetenlabs/baseten-go/client/modelarchive"
	"gopkg.in/yaml.v3"
)

const (
	modelPushConfigFileName       = "config.yaml"
	modelPushDefaultBundledPkgDir = "packages"
	modelPushDeployTimeoutMinMin  = 10
	modelPushDeployTimeoutMaxMin  = 1440
)

func init() {
	Register("model push", commandModelPush)
}

func commandModelPush(ctx *CommandContext, flags *cmd.ModelPushFlags) error {
	// --watch implies --develop: it pushes a development deployment, then loops
	// patching it in place. A development deployment owns the model's single
	// mutable dev slot, so the stable-environment flags do not apply.
	if flags.Develop || flags.Watch {
		switch {
		case flags.Environment != "":
			return cmd.NewErrUsagef("--develop/--watch cannot be combined with --environment")
		case flags.DeploymentName != "":
			return cmd.NewErrUsagef("--develop/--watch cannot be combined with --deployment-name")
		}
	}
	// --watch enters its patch loop as soon as the deployment is created rather
	// than blocking on it becoming active, so --wait has nothing to do there.
	if flags.Watch && flags.Wait {
		return cmd.NewErrUsagef("--watch cannot be combined with --wait")
	}
	if (flags.WatchHotReload || flags.WatchNoKeepalive) && !flags.Watch {
		return cmd.NewErrUsagef("--watch-hot-reload and --watch-no-keepalive require --watch")
	}

	pushOpts, err := buildModelPushOptions(ctx, flags)
	if err != nil {
		return err
	}

	// Fail fast on a directory we cannot turn into a patch point before
	// uploading anything, so a bad --watch target errors before the push.
	if flags.Watch {
		if _, err := deploymentpatch.BuildPatchPoint(ctx, deploymentpatch.BuildPatchPointOptions{Dir: flags.Dir}); err != nil {
			return err
		}
	}

	api, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	teamID, err := ResolveTeam(ctx, api.API(), flags.Team)
	if err != nil {
		return err
	}

	// The push routes by model ID when the model already exists and by name
	// when it does not, so the name is resolved here rather than by the SDK.
	modelName, _ := pushOpts.Config["model_name"].(string)
	existingModelID, err := findModelIDByName(ctx, api.API(), modelName, teamID)
	if err != nil {
		return err
	}
	if existingModelID != "" {
		if flags.DisableArchiveDownload {
			return cmd.NewErrUsagef("--disable-archive-download is only valid when creating a new model")
		}
		pushOpts.ModelID = existingModelID
	} else {
		pushOpts.TeamID = teamID
		pushOpts.DisableArchiveDownload = flags.DisableArchiveDownload
	}

	announceModelPush(ctx, modelName, pushOpts.EnvironmentName)

	result, err := api.PushModel(ctx, pushOpts)
	if err != nil {
		return err
	}
	if flags.DryRun {
		// The archive is built and read on a dry run, so its size is known even
		// though nothing was sent.
		ctx.Logf("Dry run successful: built archive (%s), no upload performed.\n", formatBytes(result.ArchiveBytes))
		if ctx.JSON {
			ctx.OutputJSON(struct{}{})
		}
		return nil
	}
	created := &managementapi.CreatedModelDeployment{Model: *result.Model, Deployment: *result.Deployment}

	remote, err := ctx.authInfo.Remote()
	if err != nil {
		return err
	}
	predictURL := remote.PredictURL(created.Model.Id, created.Deployment.Id, created.Deployment.IsDevelopment)
	logsURL := remote.LogsURL(created.Model.Id, created.Deployment.Id)

	// In JSON mode the human-readable output goes to stderr so stdout carries
	// only the JSON object.
	printf, w := ctx.Outputf, ctx.Stdout
	if ctx.JSON {
		printf, w = ctx.Logf, ctx.Stderr
	}

	// The summary lands before watch/tail/wait so its links are usable while the
	// deployment is still building, rather than only once those have finished.
	printModelPushSummary(printf, w, created, predictURL, logsURL,
		pushOpts.EnvironmentName, sshEnabledInConfig(pushOpts.Config))

	switch {
	case flags.Watch:
		err = watchModelPushDeployment(ctx, api.API(), created, flags)
	case flags.Tail:
		err = tailModelPushDeployment(ctx, api.API(), created, flags.Wait)
	case flags.Wait:
		err = waitModelPushDeployment(ctx, api.API(), created)
		// Only --wait sees the deployment settle, so only it reports how: a bare
		// --tail streams past ACTIVE and ends on interrupt. The summary already
		// announced the push; this says whether the deployment came up.
		if err == nil {
			if created.Deployment.Status == managementapi.DeploymentStatus_ACTIVE {
				printf("\n✅ Model %s is deployed and active\n", created.Model.Name)
			} else {
				printf("\n⚠️  Model %s was pushed but the deployment did not become active (status: %s)\n",
					created.Model.Name, created.Deployment.Status)
			}
		}
	}
	if err != nil {
		return err
	}
	// JSON stays last so the object carries the settled deployment status and a
	// consumer piping stdout still gets exactly one object.
	if ctx.JSON {
		ctx.OutputJSON(cmd.ModelPushResult{
			Model:      created.Model,
			Deployment: created.Deployment,
			PredictURL: predictURL,
			LogsURL:    logsURL,
		})
	}
	// The watch loop runs until interrupted, so its exit is never a deployment
	// failure; only the one-shot tail/wait paths classify the settled status.
	if !flags.Watch && (flags.Tail || flags.Wait) &&
		created.Deployment.Status != managementapi.DeploymentStatus_ACTIVE {
		return fmt.Errorf("failed deployment status: %s", created.Deployment.Status)
	}
	return nil
}

// buildModelPushOptions assembles the push the SDK will run, everything except
// the model routing (ID versus name), which needs a lookup the caller does.
func buildModelPushOptions(ctx *CommandContext, flags *cmd.ModelPushFlags) (client.PushModelOptions, error) {
	opts := client.PushModelOptions{
		DryRun:          flags.DryRun,
		DeploymentName:  flags.DeploymentName,
		EnvironmentName: flags.Environment,
		// --watch implies --develop: both push a development deployment.
		IsDevelopment:           flags.Develop || flags.Watch,
		OverrideEnvInstanceType: flags.OverrideEnvInstanceType,
		Archive: modelarchive.BuildModelArchiveOptions{
			Dir: flags.Dir,
			IgnoreFileProcessor: func(_ context.Context, opts modelarchive.IgnoreFileProcessorOptions) (modelarchive.IgnoreFileFunc, error) {
				return deploymentpatch.CompileTrussIgnore(opts.Contents), nil
			},
		},
		ModelUploader: func(uploadCtx context.Context, upload client.ModelUpload) error {
			return uploadModelPushArchive(ctx, uploadCtx, upload)
		},
	}

	if err := readModelConfigYAML(flags.Dir, &opts); err != nil {
		return opts, err
	}

	modelName, err := resolveModelPushName(flags, opts.Config)
	if err != nil {
		return opts, err
	}
	opts.Config["model_name"] = modelName

	if flags.NoBuildCache {
		applyModelPushNoBuildCache(opts.Config)
	}
	if err := applyModelPushDeployTimeout(&opts, flags.DeployTimeout); err != nil {
		return opts, err
	}
	if err := applyModelPushLabels(&opts, flags.Labels); err != nil {
		return opts, err
	}
	return opts, nil
}

// readModelConfigYAML loads config.yaml from dir into opts: the parsed Config,
// the verbatim RawConfig, and the archive's package-dir options. A missing
// config.yaml is treated as a usage error since the user is most likely
// pointing at the wrong directory.
func readModelConfigYAML(dir string, opts *client.PushModelOptions) error {
	path := filepath.Join(dir, modelPushConfigFileName)
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return cmd.NewErrUsagef(
			"%s not found in %q: is this a model directory? Pass --dir to point to one",
			modelPushConfigFileName, dir)
	case err != nil:
		return fmt.Errorf("read %s: %w", path, err)
	}

	if err := yaml.Unmarshal(raw, &opts.Config); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if opts.Config == nil {
		opts.Config = map[string]any{}
	}
	// RawConfig is persisted verbatim and only surfaced back for display/download;
	// the server never parses it into the build, so it keeps the original bytes
	// (comments and all), including external_package_dirs.
	opts.RawConfig = string(raw)

	if extDirs, ok := opts.Config["external_package_dirs"].([]any); ok {
		for _, v := range extDirs {
			if s, ok := v.(string); ok {
				opts.Archive.ExternalPackageDirs = append(opts.Archive.ExternalPackageDirs, s)
			}
		}
		// The external package contents are inlined into the archive under
		// bundled_packages_dir (mirroring the Python CLI's gather step), so the
		// field must be dropped from the config the server builds from: its truss
		// validation errors on external_package_dirs whose relative paths don't
		// exist in the extracted archive. ConfigYAMLOverride replaces the archived
		// config.yaml (the file the build actually reads); RawConfig above keeps
		// the original bytes since the server never builds from it.
		delete(opts.Config, "external_package_dirs")
		cleared, err := yaml.Marshal(opts.Config)
		if err != nil {
			return fmt.Errorf("re-marshal %s after clearing external_package_dirs: %w", path, err)
		}
		opts.Archive.ConfigYAMLOverride = cleared
	}
	if bundled, ok := opts.Config["bundled_packages_dir"].(string); ok && bundled != "" {
		opts.Archive.BundledPackagesDir = bundled
	} else {
		opts.Archive.BundledPackagesDir = modelPushDefaultBundledPkgDir
	}
	return nil
}

func resolveModelPushName(flags *cmd.ModelPushFlags, configMap map[string]any) (string, error) {
	if flags.OverrideName != "" {
		return flags.OverrideName, nil
	}
	if v, ok := configMap["model_name"].(string); ok && v != "" {
		return v, nil
	}
	return "", errors.New("model_name is required: set it in config.yaml or pass --override-name")
}

func applyModelPushNoBuildCache(configMap map[string]any) {
	build, _ := configMap["build"].(map[string]any)
	if build == nil {
		build = map[string]any{}
		configMap["build"] = build
	}
	build["no_cache"] = true
}

func applyModelPushDeployTimeout(opts *client.PushModelOptions, raw string) error {
	if raw == "" {
		return nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("--deploy-timeout: %w", err)
	}
	mins := int(math.Ceil(d.Minutes()))
	if mins < modelPushDeployTimeoutMinMin || mins > modelPushDeployTimeoutMaxMin {
		return fmt.Errorf("--deploy-timeout must be between %dm and %dm, got %dm",
			modelPushDeployTimeoutMinMin, modelPushDeployTimeoutMaxMin, mins)
	}
	opts.DeployTimeoutMinutes = mins
	return nil
}

func applyModelPushLabels(opts *client.PushModelOptions, raw string) error {
	if raw == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return fmt.Errorf("--labels: invalid JSON: %w", err)
	}
	asMap, ok := parsed.(map[string]any)
	if !ok {
		return errors.New("--labels: must be a JSON object")
	}
	opts.Labels = asMap
	return nil
}

// announceModelPush prints the pre-push narrative to stderr.
func announceModelPush(ctx *CommandContext, modelName, environment string) {
	if environment != "" {
		ctx.Logf("Pushing model %q to environment %q...\n", modelName, environment)
	} else {
		ctx.Logf("Pushing model %q...\n", modelName)
	}
}

// uploadModelPushArchive uploads the model archive the push built. uploadCtx
// carries the push's cancellation; ctx is the command context, used for output
// and for the injectable S3 client.
func uploadModelPushArchive(ctx *CommandContext, uploadCtx context.Context, upload client.ModelUpload) error {
	awsCfg := aws.Config{
		Region: upload.Region,
		Credentials: awscreds.NewStaticCredentialsProvider(
			upload.AccessKeyID, upload.SecretAccessKey, upload.SessionToken),
	}
	tm := transfermanager.New(ctx.newS3APIClient(awsCfg))

	// Counted here rather than read off the push result, since the size is
	// reported as soon as the upload lands rather than after the commit.
	counted := &readCounter{r: upload.Body}

	ctx.LogLine("Uploading model...")
	start := time.Now()
	if _, err := tm.UploadObject(uploadCtx, &transfermanager.UploadObjectInput{
		Bucket: &upload.Bucket,
		Key:    &upload.Key,
		Body:   counted,
	}); err != nil {
		return err
	}
	ctx.Logf("Uploaded model (%s) in %s\n",
		formatBytes(counted.n), time.Since(start).Round(time.Second))
	return nil
}

// readCounter wraps a reader and counts bytes as they flow through, so the
// archive size can be reported after the upload without an extra buffering
// pass over the stream.
type readCounter struct {
	r io.Reader
	n int64
}

func (c *readCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

const (
	modelPushPollInterval  = 2 * time.Second
	modelPushWarmupTimeout = 30 * time.Second
)

// watchModelPushDeployment runs the development-deployment patch loop after a
// push. With --tail the log tail runs concurrently with the watch: it never
// stops on ACTIVE, so logs keep flowing as patches land. Both observe ctx, so
// Ctrl-C stops both, and a tail failure is logged without interrupting the
// watch.
func watchModelPushDeployment(
	ctx *CommandContext,
	api *managementapi.Client,
	created *managementapi.CreatedModelDeployment,
	flags *cmd.ModelPushFlags,
) error {
	ic, err := ctx.NewInferenceClient(cmd.InferenceClientFlags{ModelID: created.Model.Id})
	if err != nil {
		return err
	}
	if flags.Tail {
		// Copy created for the background tail: it runs concurrently with the
		// watch loop and writes the deployment's final fetched state, which
		// would race the push result read after the loop. The trade-off is the
		// result reflects the push, not any status the tail observed later.
		tailTarget := *created
		go func() {
			if err := tailModelPushDeployment(ctx, api, &tailTarget, false); err != nil {
				ctx.Logf("Log tail stopped: %v\n", err)
			}
		}()
	}
	return runModelWatchLoop(ctx, api, ic, created.Model.Id, created.Deployment.Id,
		flags.Dir, flags.WatchHotReload, !flags.WatchNoKeepalive)
}

// tailModelPushDeployment streams build/runtime logs to stderr as text
// (regardless of --output) until the deployment reaches a terminal status.
// When alsoWait is true, ACTIVE is added to the stop set so a successful
// deploy ends the tail. Mutates created.Deployment with the freshest fetch
// so the JSON result reflects final state.
func tailModelPushDeployment(
	ctx *CommandContext,
	api *managementapi.Client,
	created *managementapi.CreatedModelDeployment,
	alsoWait bool,
) error {
	res := TailDeploymentLogs(ctx, TailDeploymentLogsOptions{
		API:           api,
		ModelID:       created.Model.Id,
		DeploymentID:  created.Deployment.Id,
		WarmupTimeout: modelPushWarmupTimeout,
		StopOnActive:  alsoWait,
	})
	for log, err := range res.Logs {
		if err != nil {
			return err
		}
		ctx.LogLine(FormatDeploymentLogLine(*log))
	}
	if status := res.FinalFetchedStatus(); status != nil && status.Deployment != nil {
		created.Deployment = *status.Deployment
	}
	return nil
}

// waitModelPushDeployment polls the deployment's status until it leaves
// the in-progress set {BUILDING, DEPLOYING, LOADING_MODEL, UPDATING}.
// ACTIVE is treated as success; any other status (including UNHEALTHY,
// SCALED_TO_ZERO, INACTIVE, FAILED, and unknown values) is terminal and
// surfaces as a failure via the caller's status check. Status transitions
// are logged to stderr. Mutates created.Deployment with the freshest
// fetch so the JSON result reflects final state.
func waitModelPushDeployment(
	ctx *CommandContext,
	api *managementapi.Client,
	created *managementapi.CreatedModelDeployment,
) error {
	dep, err := pollDeploymentUntilSettled(ctx, api, created.Model.Id, created.Deployment.Id,
		func(status managementapi.DeploymentStatus) bool {
			return status == managementapi.DeploymentStatus_BUILDING ||
				status == managementapi.DeploymentStatus_DEPLOYING ||
				status == managementapi.DeploymentStatus_LOADING_MODEL ||
				status == managementapi.DeploymentStatus_UPDATING
		})
	if err != nil {
		return err
	}
	created.Deployment = *dep
	return nil
}

// pollDeploymentUntilSettled polls the deployment's status, logging each
// transition to stderr, until pending reports the status is no longer
// in-progress, then returns the settled deployment for the caller to classify.
// A brand-new deployment may 404 for a few seconds after creation; those reads
// are retried within a warmup window.
func pollDeploymentUntilSettled(
	ctx *CommandContext,
	api *managementapi.Client,
	modelID, deploymentID string,
	pending func(managementapi.DeploymentStatus) bool,
) (*managementapi.Deployment, error) {
	warmupDeadline := ctx.Now().Add(modelPushWarmupTimeout)
	warmedUp := false
	var lastStatus managementapi.DeploymentStatus

	for {
		dep, err := api.GetModelsDeploymentsDeploymentId(ctx, modelID, deploymentID)
		if err != nil {
			// Brand-new deployments may 404 for a few seconds after creation;
			// retry quietly within the warmup window until the first
			// successful response.
			var re *managementapi.ResponseError
			if !warmedUp && errors.As(err, &re) && re.StatusCode == 404 && ctx.Now().Before(warmupDeadline) {
				if err := ctx.Sleep(modelPushPollInterval); err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}
		warmedUp = true
		if dep.Status != lastStatus {
			ctx.Logf("Status: %s\n", dep.Status)
			lastStatus = dep.Status
		}
		if !pending(dep.Status) {
			return dep, nil
		}
		if err := ctx.Sleep(modelPushPollInterval); err != nil {
			return nil, err
		}
	}
}

// sshEnabledInConfig reports whether the pushed Truss config turns on remote
// SSH (runtime.remote_ssh.enabled: true), so the push summary only advertises
// SSH when the workload actually accepts it. config is the parsed config.yaml
// map, whose nested maps yaml.v3 decodes as map[string]any.
func sshEnabledInConfig(config map[string]any) bool {
	runtime, ok := config["runtime"].(map[string]any)
	if !ok {
		return false
	}
	remoteSSH, ok := runtime["remote_ssh"].(map[string]any)
	if !ok {
		return false
	}
	enabled, _ := remoteSSH["enabled"].(bool)
	return enabled
}

// printModelPushSummary prints the post-push narrative: a facts card followed by
// grouped, code-styled hints for logs, invocation, and (when the config enables
// it) SSH. environment is the --environment target, if any; env-scoped hints are
// tagged "(once deployed)" since the new deployment only becomes the
// environment's live one after it finishes deploying.
func printModelPushSummary(
	printf func(string, ...any),
	w io.Writer,
	created *managementapi.CreatedModelDeployment,
	predictURL string,
	logsURL string,
	environment string,
	sshEnabled bool,
) {
	modelID, deploymentID := created.Model.Id, created.Deployment.Id

	// Show the environment when the push targeted one, otherwise when the
	// response associates the deployment with one.
	envName := environment
	if envName == "" && created.Deployment.Environment != nil {
		envName = *created.Deployment.Environment
	}

	printf("✨ Model %s was successfully pushed ✨\n", created.Model.Name)

	// Facts card: fixed labels left-padded to the widest shown label.
	rows := [][2]string{
		{"Model:", fmt.Sprintf("%s (%s)", created.Model.Name, modelID)},
		{"Deployment:", deploymentID},
	}
	if envName != "" {
		rows = append(rows, [2]string{"Environment:", envName})
	}
	labelWidth := 0
	for _, r := range rows {
		if len(r[0]) > labelWidth {
			labelWidth = len(r[0])
		}
	}
	printf("\n")
	for _, r := range rows {
		printf("  %-*s %s\n", labelWidth, r[0], r[1])
	}

	if environment != "" {
		printf("\nYour model has been deployed into the %q environment. After it successfully deploys, "+
			"it will become the next %q deployment of your model.\n", environment, environment)
	}

	// Each group is an emoji header followed by aligned rows: the code-styled
	// values line up in a column two spaces past the group's longest label.
	// Env-scoped rows are tagged "(once deployed)" since the new deployment only
	// becomes the environment's live one after it finishes deploying.
	type hint struct {
		label string
		value string
		note  string
		// rendered marks value as already styled (e.g. a hyperlink), so it is
		// printed as-is rather than wrapped in inlineCodeStyle.
		rendered bool
	}
	group := func(emoji, header string, hints []hint) {
		printf("\n%s %s\n", emoji, header)
		width := 0
		for _, h := range hints {
			if len(h.label) > width {
				width = len(h.label)
			}
		}
		width += 2
		for _, h := range hints {
			value := h.value
			if !h.rendered {
				value = inlineCodeStyle.Render(value)
			}
			printf("   %-*s%s", width, h.label, value)
			if h.note != "" {
				printf("  %s", h.note)
			}
			printf("\n")
		}
	}

	logs := []hint{
		{label: "deployment:", value: fmt.Sprintf(
			"baseten model deployment logs --model-id %s --deployment-id %s", modelID, deploymentID)},
	}
	if envName != "" {
		logs = append(logs, hint{label: "environment:", note: "(once deployed)", value: fmt.Sprintf(
			"baseten model environment logs --model-id %s --environment %s", modelID, envName)})
	}
	logs = append(logs, hint{label: "app:", value: hyperlink(w, logsURL), rendered: true})
	group("🪵", "View logs:", logs)

	group("🚀", "Invoke your model:", []hint{
		{label: "URL:", value: hyperlink(w, predictURL), rendered: true},
		{label: "CLI:", value: fmt.Sprintf("baseten model predict --model-id %s", modelID)},
	})

	// SSH last, and only when the pushed config enabled it.
	if sshEnabled {
		ssh := []hint{
			{label: "deployment:", value: fmt.Sprintf("ssh model-%s-%s.ssh.baseten.co", modelID, deploymentID)},
		}
		if envName != "" {
			ssh = append(ssh, hint{label: "environment:", note: "(once deployed)", value: fmt.Sprintf(
				"ssh %s.model-%s.ssh.baseten.co", envName, modelID)})
		}
		group("🔑", "SSH in:", ssh)
	}
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
