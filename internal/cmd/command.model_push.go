package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/basetenlabs/baseten-go/client/modelarchive"
	gitignore "github.com/sabhiram/go-gitignore"
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
	if flags.Promote && flags.Environment != "" {
		return &ErrUsage{Err: errors.New("--promote and --environment are mutually exclusive")}
	}
	if flags.Watch || flags.WatchHotReload || flags.WatchKeepalive {
		return errors.New("--watch, --watch-hot-reload, and --watch-keepalive are not yet implemented")
	}

	prepareReq, buildOpts, err := buildModelPushInputs(flags)
	if err != nil {
		return err
	}

	api, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	announceModelPush(ctx, *prepareReq.Name, prepareReq.Deployment.EnvironmentName)

	prepareResp, existingModelID, err := prepareModelPushUpload(ctx, api.API(), prepareReq, flags)
	if err != nil {
		return err
	}
	if flags.DryRun {
		ctx.LogLine("Dry run successful: no upload performed.")
		if ctx.JSON {
			ctx.OutputJSON(struct{}{})
		}
		return nil
	}

	if err := uploadModelPushArchive(ctx, buildOpts, prepareResp); err != nil {
		return err
	}

	modelName := resolvedModelPushName(prepareReq)
	created, err := commitModelPush(ctx, api.API(), existingModelID, modelName, *prepareResp.S3Key, prepareReq.Deployment, flags.DisableArchiveDownload)
	if err != nil {
		return err
	}

	switch {
	case flags.Tail:
		err = tailModelPushDeployment(ctx, api.API(), created, flags.Wait)
	case flags.Wait:
		err = waitModelPushDeployment(ctx, api.API(), created)
	}
	if err != nil {
		return err
	}
	if err := writeModelPushResult(ctx, created, prepareReq.Deployment.EnvironmentName); err != nil {
		return err
	}
	if (flags.Tail || flags.Wait) && created.Deployment.Status != managementapi.DeploymentStatus_ACTIVE {
		return fmt.Errorf("failed deployment status: %s", created.Deployment.Status)
	}
	return nil
}

// buildModelPushInputs assembles the two structs downstream calls consume:
// the prepare request (whose Deployment field is the on-the-wire payload)
// and the archive build options. The model name is set on prepareReq.Name
// here; the prepare step will flip Name to ModelId after looking up an
// existing model.
func buildModelPushInputs(flags *cmd.ModelPushFlags) (*managementapi.PrepareModelUploadRequest, modelarchive.BuildModelArchiveOptions, error) {
	prepareReq := &managementapi.PrepareModelUploadRequest{
		DryRun: &flags.DryRun,
	}
	buildOpts := modelarchive.BuildModelArchiveOptions{
		Dir: flags.Dir,
		IgnoreFileProcessor: func(_ context.Context, opts modelarchive.IgnoreFileProcessorOptions) (modelarchive.IgnoreFileFunc, error) {
			gi := gitignore.CompileIgnoreLines(strings.Split(string(opts.Contents), "\n")...)
			return func(_ context.Context, e modelarchive.IgnoreFileOptions) (bool, error) {
				return gi.MatchesPath(e.RelPath), nil
			}, nil
		},
	}

	if err := readModelConfigYAML(flags.Dir, &prepareReq.Deployment, &buildOpts); err != nil {
		return nil, buildOpts, err
	}

	modelName, err := resolveModelPushName(flags, prepareReq.Deployment.Config)
	if err != nil {
		return nil, buildOpts, err
	}
	prepareReq.Name = &modelName

	if flags.OverrideName != "" {
		prepareReq.Deployment.Config["model_name"] = flags.OverrideName
	}
	if flags.NoCache {
		applyModelPushNoCache(prepareReq.Deployment.Config)
	}
	if err := applyModelPushDeployTimeout(&prepareReq.Deployment, flags.DeployTimeout); err != nil {
		return nil, buildOpts, err
	}
	if err := applyModelPushLabels(&prepareReq.Deployment, flags.Labels); err != nil {
		return nil, buildOpts, err
	}
	applyModelPushEnvironmentFlags(&prepareReq.Deployment, flags)
	prepareReq.Deployment.UserEnv = buildModelPushUserEnv(flags)

	if flags.Team != "" {
		team := flags.Team
		prepareReq.TeamId = &team
	}
	return prepareReq, buildOpts, nil
}

// readModelConfigYAML loads config.yaml from dir and populates the fields
// downstream callers will read from: deployment.Config (parsed map),
// deployment.RawConfig (verbatim bytes), and the package-dir options on
// buildOpts. A missing config.yaml is allowed; deployment.Config defaults
// to an empty map so flag mutations have a target.
func readModelConfigYAML(dir string, deployment *managementapi.DeploymentArchivePayload, buildOpts *modelarchive.BuildModelArchiveOptions) error {
	path := filepath.Join(dir, modelPushConfigFileName)
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		deployment.Config = map[string]any{}
		buildOpts.BundledPackagesDir = modelPushDefaultBundledPkgDir
		return nil
	case err != nil:
		return fmt.Errorf("read %s: %w", path, err)
	}

	configMap := map[string]any{}
	if err := yaml.Unmarshal(raw, &configMap); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if configMap == nil {
		configMap = map[string]any{}
	}
	deployment.Config = configMap
	rawStr := string(raw)
	deployment.RawConfig = &rawStr

	if raw, ok := configMap["external_package_dirs"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				buildOpts.ExternalPackageDirs = append(buildOpts.ExternalPackageDirs, s)
			}
		}
	}
	if bundled, ok := configMap["bundled_packages_dir"].(string); ok && bundled != "" {
		buildOpts.BundledPackagesDir = bundled
	} else {
		buildOpts.BundledPackagesDir = modelPushDefaultBundledPkgDir
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

// resolvedModelPushName reads the model name after the prepare step has
// possibly flipped Name -> ModelId. The name is always preserved in
// Deployment.Config["model_name"] regardless of which routing field is set.
func resolvedModelPushName(req *managementapi.PrepareModelUploadRequest) string {
	if req.Name != nil {
		return *req.Name
	}
	if v, ok := req.Deployment.Config["model_name"].(string); ok {
		return v
	}
	return ""
}

func applyModelPushNoCache(configMap map[string]any) {
	build, _ := configMap["build"].(map[string]any)
	if build == nil {
		build = map[string]any{}
		configMap["build"] = build
	}
	build["no_cache"] = true
}

func applyModelPushDeployTimeout(deployment *managementapi.DeploymentArchivePayload, raw string) error {
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
	deployment.DeployTimeoutMinutes = &mins
	return nil
}

func applyModelPushLabels(deployment *managementapi.DeploymentArchivePayload, raw string) error {
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
	deployment.Labels = &asMap
	return nil
}

func applyModelPushEnvironmentFlags(deployment *managementapi.DeploymentArchivePayload, flags *cmd.ModelPushFlags) {
	if flags.DeploymentName != "" {
		name := flags.DeploymentName
		deployment.DeploymentName = &name
	}
	switch {
	case flags.Promote:
		env := "production"
		deployment.EnvironmentName = &env
	case flags.Environment != "":
		env := flags.Environment
		deployment.EnvironmentName = &env
	}
	if deployment.EnvironmentName != nil {
		// Server defaults to true; flag flips it off.
		preserve := !flags.OverrideEnvInstanceType
		deployment.PreserveEnvInstanceType = &preserve
	}
	if flags.Promote {
		// Server defaults to true; flag flips it off.
		scaleDown := !flags.PreservePreviousProductionDeployment
		deployment.ScaleDownOldProduction = &scaleDown
	}
}

func buildModelPushUserEnv(flags *cmd.ModelPushFlags) *map[string]any {
	if !flags.IncludeGitInfo {
		return nil
	}
	info := collectModelPushGitInfo(flags.Dir)
	if info == nil {
		return nil
	}
	env := map[string]any{"git_info": info}
	return &env
}

func collectModelPushGitInfo(dir string) map[string]any {
	sha, ok := runModelPushGit(dir, "rev-parse", "HEAD")
	if !ok {
		return nil
	}
	info := map[string]any{"latest_commit_sha": sha}
	if tag, ok := runModelPushGit(dir, "describe", "--tags", "--abbrev=0"); ok {
		info["latest_tag"] = tag
		if count, ok := runModelPushGit(dir, "rev-list", tag+"..HEAD", "--count"); ok {
			info["commits_since_tag"] = count
		}
	}
	if status, ok := runModelPushGit(dir, "status", "--porcelain"); ok {
		info["has_uncommitted_changes"] = status != ""
	}
	return info
}

func runModelPushGit(dir string, args ...string) (string, bool) {
	c := exec.Command("git", args...)
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// announceModelPush prints the pre-push narrative to stderr.
func announceModelPush(ctx *CommandContext, modelName string, environment *string) {
	if environment != nil {
		ctx.Logf("Pushing model %q to environment %q...\n", modelName, *environment)
	} else {
		ctx.Logf("Pushing model %q...\n", modelName)
	}
}

// prepareModelPushUpload looks up the existing model (if any), finalizes the
// new-vs-existing routing on prepareReq (Name vs ModelId), validates
// route-specific flags, and calls PostPrepareModelUpload. Returns the
// existing model ID (or "" for new) alongside the response so callers can
// pick the right commit path.
func prepareModelPushUpload(
	ctx *CommandContext,
	api *managementapi.Client,
	prepareReq *managementapi.PrepareModelUploadRequest,
	flags *cmd.ModelPushFlags,
) (*managementapi.PrepareModelUploadResponse, string, error) {
	modelName := *prepareReq.Name

	existingModelID, err := findExistingModelByName(ctx, api, modelName)
	if err != nil {
		return nil, "", err
	}
	if existingModelID != "" {
		if flags.DisableArchiveDownload {
			return nil, "", &ErrUsage{Err: errors.New("--disable-archive-download is only valid when creating a new model")}
		}
		if flags.Team != "" {
			ctx.Logf("Ignoring --team: model %q already exists.\n", modelName)
			prepareReq.TeamId = nil
		}
		prepareReq.Name = nil
		prepareReq.ModelId = &existingModelID
	}

	resp, err := api.PostPrepareModelUpload(ctx, *prepareReq)
	if err != nil {
		return nil, "", fmt.Errorf("prepare upload: %w", err)
	}
	return resp, existingModelID, nil
}

func findExistingModelByName(ctx context.Context, api *managementapi.Client, name string) (string, error) {
	resp, err := api.GetModels(ctx)
	if err != nil {
		return "", fmt.Errorf("list models: %w", err)
	}
	var matches []managementapi.Model
	for _, m := range resp.Models {
		if m.Name == name {
			matches = append(matches, m)
		}
	}
	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		return matches[0].Id, nil
	default:
		return "", fmt.Errorf("multiple models named %q across teams; disambiguate by team in the API or rename", name)
	}
}

func uploadModelPushArchive(
	ctx *CommandContext,
	buildOpts modelarchive.BuildModelArchiveOptions,
	prepare *managementapi.PrepareModelUploadResponse,
) error {
	if prepare.Creds == nil || prepare.S3Bucket == nil || prepare.S3Key == nil || prepare.S3Region == nil {
		return errors.New("prepare upload: server returned empty upload credentials")
	}

	archive, err := modelarchive.BuildModelArchive(ctx, buildOpts)
	if err != nil {
		return fmt.Errorf("build archive: %w", err)
	}
	defer archive.Close()
	counted := &readCounter{r: archive}

	awsCfg := aws.Config{
		Region: *prepare.S3Region,
		Credentials: awscreds.NewStaticCredentialsProvider(
			prepare.Creds.AwsAccessKeyId,
			prepare.Creds.AwsSecretAccessKey,
			prepare.Creds.AwsSessionToken,
		),
	}
	tm := transfermanager.New(ctx.newS3APIClient(awsCfg))

	ctx.LogLine("Uploading Truss...")
	start := time.Now()
	if _, err := tm.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket: prepare.S3Bucket,
		Key:    prepare.S3Key,
		Body:   counted,
	}); err != nil {
		return fmt.Errorf("upload archive: %w", err)
	}
	ctx.Logf("Uploaded Truss (%s) in %s\n",
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

func commitModelPush(
	ctx context.Context,
	api *managementapi.Client,
	existingModelID, modelName, s3Key string,
	deployment managementapi.DeploymentArchivePayload,
	disableArchiveDownload bool,
) (*managementapi.CreatedModelDeployment, error) {
	if existingModelID != "" {
		src := managementapi.DeploymentArchiveSource{S3Key: s3Key, Deployment: deployment}
		var union managementapi.CreateModelDeploymentRequest_Source
		if err := union.FromDeploymentArchiveSource(src); err != nil {
			return nil, err
		}
		return api.PostModelsDeployments(ctx, existingModelID, managementapi.CreateModelDeploymentRequest{Source: union})
	}

	src := managementapi.ModelArchiveSource{Name: modelName, S3Key: s3Key, Deployment: deployment}
	if disableArchiveDownload {
		t := true
		src.DisableArchiveDownload = &t
	}
	var union managementapi.CreateModelRequest_Source
	if err := union.FromModelArchiveSource(src); err != nil {
		return nil, err
	}
	return api.PostModels(ctx, managementapi.CreateModelRequest{Source: union})
}

const (
	modelPushPollInterval  = 2 * time.Second
	modelPushWarmupTimeout = 30 * time.Second
)

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
	opts := TailDeploymentLogsOptions{
		API:           api,
		ModelID:       created.Model.Id,
		DeploymentID:  created.Deployment.Id,
		WarmupTimeout: modelPushWarmupTimeout,
	}
	if alsoWait {
		opts.AdditionalTailStopStatuses = []managementapi.DeploymentStatus{managementapi.DeploymentStatus_ACTIVE}
	}
	res := TailDeploymentLogs(ctx, opts)
	for log, err := range res.Logs {
		if err != nil {
			return err
		}
		ctx.LogLine(FormatDeploymentLogLine(*log))
	}
	if dep := res.FinalFetchedDeployment(); dep != nil {
		created.Deployment = *dep
	}
	return nil
}

// waitModelPushDeployment polls the deployment's status until it becomes
// ACTIVE or enters a terminal-failure status. Status transitions are
// logged to stderr. Mutates created.Deployment with the freshest fetch so
// the JSON result reflects final state.
func waitModelPushDeployment(
	ctx *CommandContext,
	api *managementapi.Client,
	created *managementapi.CreatedModelDeployment,
) error {
	warmupDeadline := ctx.Now().Add(modelPushWarmupTimeout)
	warmedUp := false
	var lastStatus managementapi.DeploymentStatus

	for {
		dep, err := api.GetModelsDeploymentsDeploymentId(ctx, created.Model.Id, created.Deployment.Id)
		if err != nil {
			// Brand-new deployments may 404 for a few seconds after creation;
			// retry quietly within the warmup window until the first
			// successful response.
			var re *managementapi.ResponseError
			if !warmedUp && errors.As(err, &re) && re.StatusCode == 404 && ctx.Now().Before(warmupDeadline) {
				if err := ctx.Sleep(modelPushPollInterval); err != nil {
					return err
				}
				continue
			}
			return err
		}
		warmedUp = true
		if dep.Status != lastStatus {
			ctx.Logf("Status: %s\n", dep.Status)
			lastStatus = dep.Status
		}
		switch dep.Status {
		case managementapi.DeploymentStatus_ACTIVE,
			managementapi.DeploymentStatus_BUILD_FAILED,
			managementapi.DeploymentStatus_BUILD_STOPPED,
			managementapi.DeploymentStatus_DEACTIVATING,
			managementapi.DeploymentStatus_DEPLOY_FAILED,
			managementapi.DeploymentStatus_FAILED,
			managementapi.DeploymentStatus_INACTIVE:
			created.Deployment = *dep
			return nil
		}
		if err := ctx.Sleep(modelPushPollInterval); err != nil {
			return err
		}
	}
}

func writeModelPushResult(ctx *CommandContext, created *managementapi.CreatedModelDeployment, environment *string) error {
	predictURL := ctx.Remote.PredictURL(created.Model.Id, created.Deployment.Id, created.Deployment.IsDevelopment)
	logsURL := ctx.Remote.LogsURL(created.Model.Id, created.Deployment.Id)

	// Narrative goes first so a user piping JSON to a file or jq sees the
	// human summary on stderr before the JSON object lands on stdout.
	if ctx.JSON {
		writeModelPushSummary(ctx.Logf, created, predictURL, logsURL, environment)
		ctx.OutputJSON(map[string]any{
			"model":       created.Model,
			"deployment":  created.Deployment,
			"predict_url": predictURL,
			"logs_url":    logsURL,
		})
		return nil
	}
	writeModelPushSummary(ctx.Outputf, created, predictURL, logsURL, environment)
	return nil
}

func writeModelPushSummary(printf func(string, ...any), created *managementapi.CreatedModelDeployment, predictURL, logsURL string, environment *string) {
	logsCmd := fmt.Sprintf("baseten model deployment logs --model-id %s --deployment-id %s",
		created.Model.Id, created.Deployment.Id)
	predictCmd := fmt.Sprintf("baseten model predict --model-id %s", created.Model.Id)
	printf("✨ Model %s was successfully pushed ✨\n", created.Model.Name)
	if environment != nil {
		printf("Your Truss has been deployed into the %q environment. After it successfully deploys, it will become the next %q deployment of your model.\n",
			*environment, *environment)
	}
	printf("🪵  View logs for your deployment at %s or %s\n", inlineCodeStyle.Render(logsURL), inlineCodeStyle.Render(logsCmd))
	printf("🚀  Invoke your model at %s or %s\n", inlineCodeStyle.Render(predictURL), inlineCodeStyle.Render(predictCmd))
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
