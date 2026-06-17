package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/deploymentpatch"
	"github.com/basetenlabs/baseten-go/client"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"gopkg.in/yaml.v3"
)

func init() {
	Register("model watch", commandModelWatch)
}

const (
	// modelWatchInitialAttempts and modelWatchRetryDelay bound the first sync:
	// Truss's retry_patch retries any non-success a fixed number of times before
	// giving up, so a freshly created deployment that is not yet ready gets a few
	// chances to come online.
	modelWatchInitialAttempts = 5
	modelWatchRetryDelay      = 5 * time.Second
	// modelPatchConflictAttempts bounds the re-read loop when staging races a
	// landing pending patch (409). The base only moves forward, so this converges.
	modelPatchConflictAttempts = 5
)

// patchOutcome classifies the result of one [modelPatchTick]. patchFatal is the
// zero value and is always paired with a non-nil error.
type patchOutcome int

const (
	patchFatal patchOutcome = iota
	patchApplied
	patchSkipped
	patchNeedsFullDeploy
	patchRecoverable
)

func commandModelWatch(ctx *CommandContext, flags *cmd.ModelWatchFlags) error {
	modelName, err := readModelNameFromConfig(flags.Dir)
	if err != nil {
		return err
	}

	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	api := cl.API()

	teamID, err := ResolveTeam(ctx, api, flags.Team)
	if err != nil {
		return err
	}
	modelID, err := findModelIDByName(ctx, api, modelName, teamID)
	if err != nil {
		return err
	}
	if modelID == "" {
		return cmd.NewErrNotFound(fmt.Errorf(
			"no model named %q; create it first with 'baseten model push --develop'", modelName))
	}

	dep, err := api.GetModelsDeploymentsDevelopment(ctx, modelID)
	if err != nil {
		var re *managementapi.ResponseError
		if errors.As(err, &re) && re.StatusCode == 404 {
			return cmd.NewErrNotFound(fmt.Errorf(
				"model %q has no development deployment; create one with 'baseten model push --develop'", modelName))
		}
		return fmt.Errorf("resolve development deployment: %w", err)
	}

	ic, err := ctx.NewInferenceClient(cmd.InferenceClientFlags{ModelID: modelID})
	if err != nil {
		return err
	}

	ctx.Logf("Watching %q (development deployment %s)...\n", modelName, dep.Id)

	// Best-effort wake so a scaled-to-zero deployment starts coming up before we
	// wait on it; errors are ignored, matching Truss. A freshly pushed model
	// (push --watch) is already coming up, so only the standalone watch wakes.
	_ = ic.API().WakeDeployment(ctx, dep.Id)

	return runModelWatchLoop(ctx, api, ic, modelID, dep.Id, flags.Dir, flags.HotReload, flags.Keepalive)
}

// readModelNameFromConfig reads model_name from the watched directory's
// config.yaml, the same field 'baseten model push' resolves the model by.
func readModelNameFromConfig(dir string) (string, error) {
	path := filepath.Join(dir, modelPushConfigFileName)
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", cmd.NewErrUsagef(
			"%s not found in %q: is this a model directory? Pass --dir to point to one",
			modelPushConfigFileName, dir)
	case err != nil:
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var config struct {
		ModelName string `yaml:"model_name"`
	}
	if err := yaml.Unmarshal(raw, &config); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if config.ModelName == "" {
		return "", cmd.NewErrUsagef("model_name is required in %s", path)
	}
	return config.ModelName, nil
}

// runModelWatchLoop drives the patch loop shared by 'model watch' and
// 'model push --watch': wait for the development deployment to be ready, run
// an initial bounded-retry sync, then patch once per debounced filesystem
// batch until interrupted. A user interrupt propagates as context.Canceled for
// the central handler to report; the keepalive 24-hour cap returns nil; a
// keepalive failure surfaces as an error.
func runModelWatchLoop(
	ctx *CommandContext,
	api *managementapi.Client,
	ic *client.InferenceClient,
	modelID, deploymentID, dir string,
	hotReload, keepalive bool,
) error {
	if err := waitForDeploymentReadyForWatch(ctx, api, modelID, deploymentID); err != nil {
		return err
	}

	// Keepalive runs alongside the watch. It stops the loop (rather than killing
	// the process) by cancelling watchCtx with a cause, so the watcher's own
	// cleanup runs and the exit code is decided here.
	watchCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	if keepalive {
		ctx.startKeepalive(watchCtx, cancel, ic, deploymentID)
	}

	if err := runInitialModelPatch(ctx, api, modelID, deploymentID, dir, hotReload); err != nil {
		return err
	}

	events, err := deploymentpatch.Watch(watchCtx, deploymentpatch.WatchOptions{Dir: dir})
	if err != nil {
		return err
	}
	ctx.LogLine("Watching for changes. Press Ctrl-C to stop.")
	// The range ends only when the channel closes, which Watch does when
	// watchCtx is cancelled (Ctrl-C or keepalive); a watcher failure instead
	// delivers an Err event and returns from inside the loop.
	for ev := range events {
		if ev.Err != nil {
			return ev.Err
		}
		// Steady state: apply once per batch and log the result. Unlike the
		// initial sync there is no active retry; the next filesystem change
		// drives the next attempt.
		outcome, reason, err := modelPatchTick(ctx, api, modelID, deploymentID, dir, hotReload)
		logModelPatchResult(ctx, outcome, reason, err)
	}
	// The channel closed because watchCtx was cancelled. The keepalive 24-hour
	// cap is a normal stop; a user interrupt propagates (context.Canceled) so the
	// central handler reports it; a keepalive failure surfaces as its error.
	if cause := context.Cause(watchCtx); !errors.Is(cause, errKeepaliveExpired) {
		return cause
	}
	return nil
}

// modelWatchPollInterval is how often the readiness wait re-checks deployment
// status.
const modelWatchPollInterval = 2 * time.Second

// waitForDeploymentReadyForWatch blocks until the deployment is ready to accept
// patches (ACTIVE or LOADING_MODEL), failing on a terminal status. Mirrors the
// ready check in Truss wait_for_development_model_ready.
func waitForDeploymentReadyForWatch(
	ctx *CommandContext,
	api *managementapi.Client,
	modelID, deploymentID string,
) error {
	ctx.LogLine("Waiting for deployment to be ready...")
	dep, err := pollDeploymentUntilSettled(ctx, api, modelID, deploymentID,
		func(status managementapi.DeploymentStatus) bool {
			return status == managementapi.DeploymentStatus_BUILDING ||
				status == managementapi.DeploymentStatus_DEPLOYING ||
				status == managementapi.DeploymentStatus_UPDATING ||
				status == managementapi.DeploymentStatus_SCALED_TO_ZERO ||
				status == managementapi.DeploymentStatus_WAKING_UP
		})
	if err != nil {
		return err
	}
	switch dep.Status {
	case managementapi.DeploymentStatus_ACTIVE, managementapi.DeploymentStatus_LOADING_MODEL:
		return nil
	default:
		return cmd.NewErrGeneric(fmt.Errorf(
			"deployment is not ready (status: %s)", dep.Status))
	}
}

// runInitialModelPatch performs the first sync, retrying any non-success a
// bounded number of times before giving up with a non-zero exit. Mirroring
// Truss's retry_patch, even a needs-full-deploy outcome is retried here.
func runInitialModelPatch(
	ctx *CommandContext,
	api *managementapi.Client,
	modelID, deploymentID, dir string,
	hotReload bool,
) error {
	for attempt := 1; attempt <= modelWatchInitialAttempts; attempt++ {
		outcome, reason, err := modelPatchTick(ctx, api, modelID, deploymentID, dir, hotReload)
		if err != nil {
			return err
		}
		switch outcome {
		case patchApplied:
			ctx.LogLine("Synced development deployment.")
			return nil
		case patchSkipped:
			ctx.LogLine("Development deployment already up to date.")
			return nil
		}
		logModelPatchResult(ctx, outcome, reason, nil)
		if attempt < modelWatchInitialAttempts {
			if err := ctx.Sleep(modelWatchRetryDelay); err != nil {
				return err
			}
		}
	}
	return cmd.NewErrGeneric(fmt.Errorf(
		"initial sync did not succeed after %d attempts", modelWatchInitialAttempts))
}

// modelPatchTick performs one patch attempt: read the server's patch state,
// diff the local source against it, and stage + sync any change. A non-nil
// error is a fatal failure; otherwise the outcome classifies the result.
func modelPatchTick(
	ctx *CommandContext,
	api *managementapi.Client,
	modelID, deploymentID, dir string,
	hotReload bool,
) (patchOutcome, string, error) {
	next, err := deploymentpatch.BuildPatchPoint(ctx, deploymentpatch.BuildPatchPointOptions{Dir: dir})
	if err != nil {
		return patchFatal, "", err
	}

	// The staged patch must build on the server's latest point. A 409 means the
	// base moved under us (a pending patch landed); re-read state and recompute.
	for attempt := 0; attempt < modelPatchConflictAttempts; attempt++ {
		state, err := api.GetModelsDeploymentsPatchesState(ctx, modelID, deploymentID)
		if err != nil {
			return patchFatal, "", fmt.Errorf("read patch state: %w", err)
		}
		prevWithHash := state.RunningPatchPoint
		if state.PendingPatchPoint != nil {
			prevWithHash = *state.PendingPatchPoint
		}
		prev := withHashToPatchPoint(prevWithHash)

		ops, err := deploymentpatch.BuildPatchOps(ctx, deploymentpatch.BuildPatchOpsOptions{
			Dir:       dir,
			Prev:      &prev,
			Next:      next,
			HotReload: hotReload,
		})
		if errors.Is(err, deploymentpatch.ErrNeedsFullDeploy) {
			return patchNeedsFullDeploy, "the change cannot be patched", nil
		}
		if err != nil {
			return patchFatal, "", err
		}

		switch {
		case len(ops) > 0:
			_, err := api.PostModelsDeploymentsPatches(ctx, modelID, deploymentID, managementapi.CreateDeploymentPatchRequest{
				PrevPatchHash:  prevWithHash.Hash,
				NextPatchPoint: *next,
				PatchOps:       ops,
			})
			if err != nil {
				var re *managementapi.ResponseError
				if errors.As(err, &re) && re.StatusCode == 409 {
					continue // stale base; re-read and recompute
				}
				return patchFatal, "", fmt.Errorf("stage patch: %w", err)
			}
		case state.PendingPatchPoint == nil:
			// Local matches the running point and nothing is pending: no-op.
			return patchSkipped, "", nil
		}
		// Either we just staged a patch, or an unapplied pending patch exists;
		// sync applies it to the running container.
		return syncModelPatch(ctx, api, modelID, deploymentID)
	}
	return patchRecoverable, "patch base kept changing under us", nil
}

// syncModelPatch applies the deployment's pending patch to the running
// container. A 503 is recoverable (the deployment is not ready yet); a
// needs_full_deploy reason in the response means the patch cannot be applied.
func syncModelPatch(
	ctx *CommandContext,
	api *managementapi.Client,
	modelID, deploymentID string,
) (patchOutcome, string, error) {
	resp, err := api.PostModelsDeploymentsPatchesSync(ctx, modelID, deploymentID, managementapi.SyncDeploymentPatchesRequest{})
	if err != nil {
		var re *managementapi.ResponseError
		if errors.As(err, &re) && re.StatusCode == 503 {
			return patchRecoverable, "deployment is not ready to sync yet", nil
		}
		return patchFatal, "", fmt.Errorf("sync patch: %w", err)
	}
	if resp.NeedsFullDeployReason != nil {
		return patchNeedsFullDeploy, *resp.NeedsFullDeployReason, nil
	}
	return patchApplied, "", nil
}

// logModelPatchResult writes a one-line status for a tick to stderr.
func logModelPatchResult(ctx *CommandContext, outcome patchOutcome, reason string, err error) {
	switch {
	case err != nil:
		ctx.Logf("Patch failed: %v\n", err)
	case outcome == patchApplied:
		ctx.LogLine("Applied patch to development deployment.")
	case outcome == patchSkipped:
		ctx.LogLine("No changes to patch.")
	case outcome == patchNeedsFullDeploy:
		ctx.Logf("Cannot patch (%s); run 'baseten model push --develop' to redeploy.\n", reason)
	case outcome == patchRecoverable:
		ctx.Logf("Patch not applied: %s\n", reason)
	}
}

// withHashToPatchPoint strips the server-assigned hash, yielding the request
// shape [deploymentpatch.BuildPatchOps] diffs against.
func withHashToPatchPoint(p managementapi.DeploymentPatchPointWithHash) managementapi.DeploymentPatchPoint {
	return managementapi.DeploymentPatchPoint{
		Config:        p.Config,
		ContentHashes: p.ContentHashes,
		Requirements:  p.Requirements,
	}
}

const (
	keepaliveInterval    = 30 * time.Second
	keepaliveMaxFailures = 20
	keepaliveMaxDuration = 24 * time.Hour
	keepaliveWarnBefore  = 30 * time.Minute
)

// errKeepaliveExpired is the cause the keepalive goroutine cancels the watch
// with on the 24-hour cap; runModelWatchLoop matches it to treat that stop as
// normal (a ping failure is cancelled with a plain error and surfaces as one).
var errKeepaliveExpired = errors.New("keepalive reached the 24 hour limit")

// startKeepalive launches a background goroutine that pings the deployment's
// sync endpoint to keep it from scaling to zero while watching. It mirrors
// Truss's start_keepalive: a 30s ping interval, a tolerance of consecutive
// failures, and a hard 24-hour cap. Rather than killing the process (as Truss
// does), it stops the watch by cancelling watchCtx with a cause.
//
// The warm-up endpoint lives on the model's inference host and is not part of
// the typed inference surface, so we reuse the inference client's authed
// transport and base URL and issue a raw GET against the sync path.
func (c *CommandContext) startKeepalive(
	watchCtx context.Context,
	cancel context.CancelCauseFunc,
	ic *client.InferenceClient,
	deploymentID string,
) {
	url := ic.API().BaseURL + "/deployment/" + deploymentID + "/sync/v1/models/model"
	c.LogLine("💤 Keeping development deployment warm (--keepalive)")
	go runKeepaliveLoop(c, watchCtx, cancel, ic.API().HTTPClient, url)
}

// runKeepaliveLoop pings url every keepaliveInterval until watchCtx is
// cancelled. Repeated failures cancel with errKeepaliveFailed and the 24-hour
// cap with errKeepaliveExpired, stopping the watch loop.
func runKeepaliveLoop(
	c *CommandContext,
	watchCtx context.Context,
	cancel context.CancelCauseFunc,
	doer interface {
		Do(*http.Request) (*http.Response, error)
	},
	url string,
) {
	start := c.Now()
	consecutiveFailures := 0
	warned := false
	for {
		if watchCtx.Err() != nil {
			return
		}
		elapsed := c.Now().Sub(start)
		if elapsed > keepaliveMaxDuration {
			c.LogLine("⚠️  Keepalive has run for 24 hours; stopping watch.")
			cancel(errKeepaliveExpired)
			return
		}
		if !warned && elapsed > keepaliveMaxDuration-keepaliveWarnBefore {
			c.LogLine("⚠️  Keepalive will stop in 30 minutes (24 hour limit).")
			warned = true
		}

		req, err := http.NewRequestWithContext(watchCtx, http.MethodGet, url, nil)
		if err != nil {
			c.Logf("Keepalive request error: %v\n", err)
			return
		}
		resp, err := doer.Do(req)
		switch {
		case err != nil:
			consecutiveFailures++
		case resp.StatusCode == http.StatusOK:
			consecutiveFailures = 0
		case resp.StatusCode >= 400 && resp.StatusCode < 500:
			// Ignore 4xx: the deployment is reachable, just not serving yet.
		default:
			consecutiveFailures++
		}
		if resp != nil {
			resp.Body.Close()
		}
		if consecutiveFailures >= keepaliveMaxFailures {
			c.Logf("⚠️  Keepalive ping failed %d times in a row; stopping watch.\n", consecutiveFailures)
			cancel(fmt.Errorf("keepalive could not reach the deployment after %d attempts", consecutiveFailures))
			return
		}
		select {
		case <-watchCtx.Done():
			return
		case <-time.After(keepaliveInterval):
		}
	}
}
