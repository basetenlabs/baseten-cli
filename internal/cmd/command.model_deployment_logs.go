package cmd

import (
	"errors"
	"fmt"
	"iter"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

const (
	maxLogTimeRange              = 7 * 24 * time.Hour
	deploymentLogPollInterval    = 2 * time.Second
	deploymentLogClockSkewBuffer = 60 * time.Second
)

func init() {
	Register("model deployment logs", commandModelDeploymentLogs)
}

func commandModelDeploymentLogs(ctx *CommandContext, flags *cmd.ModelDeploymentLogsFlags) error {
	hasStart := !flags.Start.IsZero()
	hasEnd := !flags.End.IsZero()
	hasSince := flags.Since != 0
	if flags.Tail && (hasStart || hasEnd || hasSince) {
		return &ErrUsage{Err: errors.New("--tail cannot be combined with --start, --end, or --since")}
	}
	if hasSince && (hasStart || hasEnd) {
		return &ErrUsage{Err: errors.New("--since cannot be combined with --start or --end")}
	}

	api, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	if flags.Tail {
		res := TailDeploymentLogs(ctx, TailDeploymentLogsOptions{
			API:          api.API(),
			ModelID:      flags.ModelID,
			DeploymentID: flags.DeploymentID,
		})
		if err := emitDeploymentLogs(ctx, res.Logs); err != nil {
			return err
		}
		if dep := res.FinalFetchedDeployment(); dep != nil {
			ctx.Logf("Tailing stopped: deployment status %s\n", dep.Status)
		}
		return nil
	}

	// Resolve --start/--end/--since into epoch-millis bounds. Nil bounds mean
	// "server default"; unset start/end/since pass nils.
	var startMs, endMs *int
	if hasSince {
		if flags.Since <= 0 {
			return &ErrUsage{Err: errors.New("--since must be greater than zero")}
		}
		if flags.Since > maxLogTimeRange {
			return &ErrUsage{Err: errors.New("--since must be at most 7d")}
		}
		now := ctx.Now()
		s := int(now.Add(-flags.Since).UnixMilli())
		e := int(now.UnixMilli())
		startMs, endMs = &s, &e
	} else if hasStart || hasEnd {
		startT, endT := flags.Start, flags.End
		if !hasEnd {
			endT = ctx.Now().Truncate(time.Second)
		}
		if !hasStart {
			startT = endT.Add(-maxLogTimeRange)
		}
		if !startT.Before(endT) {
			return &ErrUsage{Err: errors.New("--start must be earlier than --end")}
		}
		if endT.Sub(startT) > maxLogTimeRange {
			return &ErrUsage{Err: errors.New("log time range must be at most 7 days; narrow --start/--end or use --since")}
		}
		s := int(startT.UnixMilli())
		e := int(endT.UnixMilli())
		startMs, endMs = &s, &e
	}

	resp, err := api.API().PostModelsDeploymentsLogs(ctx, flags.ModelID, flags.DeploymentID,
		managementapi.GetDeploymentLogsRequest{StartEpochMillis: startMs, EndEpochMillis: endMs})
	if err != nil {
		return err
	}

	return emitDeploymentLogs(ctx, func(yield func(*managementapi.Log, error) bool) {
		for i := range resp.Logs {
			if !yield(&resp.Logs[i], nil) {
				return
			}
		}
	})
}

// emitDeploymentLogs drains an iterator of log records onto stdout in the
// caller-selected output mode. For ctx.JSON (both json and jsonl) it uses a
// JSON array writer so jsonl streams one record per line and json buffers
// into a single closed array. For text it formats each line via
// FormatDeploymentLogLine.
func emitDeploymentLogs(ctx *CommandContext, logs iter.Seq2[*managementapi.Log, error]) error {
	if ctx.JSON {
		w := ctx.NewJSONArrayWriter()
		defer w.Close()
		for log, err := range logs {
			if err != nil {
				return err
			}
			w.Write(log)
		}
		return nil
	}
	for log, err := range logs {
		if err != nil {
			return err
		}
		ctx.OutputLine(FormatDeploymentLogLine(*log))
	}
	return nil
}

// FormatDeploymentLogLine renders a log record as
// "[YYYY-MM-DD HH:MM:SS]: (replica) message" in the local timezone.
// Replica segment is omitted when empty. Unparseable timestamps fall back
// to the raw string.
func FormatDeploymentLogLine(log managementapi.Log) string {
	stamp := log.Timestamp
	if ns, err := strconv.ParseInt(log.Timestamp, 10, 64); err == nil {
		stamp = time.Unix(0, ns).Format("2006-01-02 15:04:05")
	}
	replica := ""
	if log.Replica != nil && *log.Replica != "" {
		replica = "(" + *log.Replica + ") "
	}
	return fmt.Sprintf("[%s]: %s%s", stamp, replica, strings.TrimSpace(log.Message))
}

// TailDeploymentLogsOptions configures TailDeploymentLogs. API, ModelID, and
// DeploymentID are required.
type TailDeploymentLogsOptions struct {
	API          *managementapi.Client
	ModelID      string
	DeploymentID string

	// AdditionalTailStopStatuses extends the default terminal-failure stop
	// set with additional statuses that end tailing cleanly. Unknown
	// statuses are treated as "keep polling" so a new server-side status
	// string does not silently truncate a tail. push --wait passes
	// ACTIVE here to also stop on a successful deploy.
	AdditionalTailStopStatuses []managementapi.DeploymentStatus

	// WarmupTimeout is how long to silently retry 404s from the logs and
	// status APIs at the start of the tail, before any successful poll.
	// After the first successful response, 404s are surfaced as errors.
	// Zero means no warmup retries.
	WarmupTimeout time.Duration
}

// TailDeploymentLogsResult bundles the streaming log iterator with an
// accessor for the final fetched deployment.
type TailDeploymentLogsResult struct {
	// Logs yields log records in arrival order. A non-nil error indicates
	// the stream is ending due to that error and the log pointer is nil.
	// The iterator is single-use.
	Logs iter.Seq2[*managementapi.Log, error]

	// FinalFetchedDeployment returns the deployment as last fetched when
	// the tail loop ended. Valid only after Logs is fully consumed. Nil
	// if the loop ended before any status fetch (no logs ever arrived, or
	// ctx was cancelled during phase 1).
	FinalFetchedDeployment func() *managementapi.Deployment
}

// TailDeploymentLogs polls a deployment's logs and streams new records until
// the status enters the stop set or the context is cancelled. The stop set
// is the default terminal-failure statuses plus any in
// AdditionalTailStopStatuses. Dedup is by (timestamp, message, replica)
// across overlapping clock-skew windows. Clock-and-sleep behavior is taken
// from ctx (overridable via WithNow / WithSleep for tests).
func TailDeploymentLogs(ctx *CommandContext, opts TailDeploymentLogsOptions) *TailDeploymentLogsResult {
	stop := map[managementapi.DeploymentStatus]struct{}{
		managementapi.DeploymentStatus_BUILD_FAILED:  {},
		managementapi.DeploymentStatus_BUILD_STOPPED: {},
		managementapi.DeploymentStatus_DEACTIVATING:  {},
		managementapi.DeploymentStatus_DEPLOY_FAILED: {},
		managementapi.DeploymentStatus_FAILED:        {},
		managementapi.DeploymentStatus_INACTIVE:      {},
	}
	for _, s := range opts.AdditionalTailStopStatuses {
		stop[s] = struct{}{}
	}

	var finalFetched *managementapi.Deployment

	seq := func(yield func(*managementapi.Log, error) bool) {
		seen := map[deploymentLogDedupKey]struct{}{}
		var lastPollMs int64
		warmupDeadline := time.Time{}
		if opts.WarmupTimeout > 0 {
			warmupDeadline = ctx.Now().Add(opts.WarmupTimeout)
		}
		warmedUp := false

		for {
			nowMs := ctx.Now().UnixMilli()
			var startMs *int
			if lastPollMs > 0 {
				v := int(lastPollMs - deploymentLogClockSkewBuffer.Milliseconds())
				startMs = &v
			}
			endMs := int(nowMs + deploymentLogClockSkewBuffer.Milliseconds())
			resp, err := opts.API.PostModelsDeploymentsLogs(ctx, opts.ModelID, opts.DeploymentID,
				managementapi.GetDeploymentLogsRequest{StartEpochMillis: startMs, EndEpochMillis: &endMs})
			if err != nil {
				// Brand-new deployments may 404 on the logs index for a few
				// seconds after creation; retry quietly within the warmup
				// window until the first successful response.
				var re *managementapi.ResponseError
				if !warmedUp && errors.As(err, &re) && re.StatusCode == 404 && ctx.Now().Before(warmupDeadline) {
					if err := ctx.Sleep(deploymentLogPollInterval); err != nil {
						yield(nil, err)
						return
					}
					continue
				}
				yield(nil, err)
				return
			}
			warmedUp = true
			// Poll windows overlap by deploymentLogClockSkewBuffer on each
			// side to tolerate server/client clock skew, so the same record
			// can reappear across polls; dedup by (timestamp, message,
			// replica).
			for i := range resp.Logs {
				log := resp.Logs[i]
				replica := ""
				if log.Replica != nil {
					replica = *log.Replica
				}
				key := deploymentLogDedupKey{Timestamp: log.Timestamp, Message: log.Message, Replica: replica}
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				if !yield(&log, nil) {
					return
				}
			}
			lastPollMs = nowMs

			// Once any log has been seen, refresh status each poll so we can
			// stop when the deployment leaves a runnable state. This is skipped
			// until the first log is seen, similar to Truss.
			// TODO: should the management logs API return current status so
			// we can drop this extra round-trip per poll?
			if len(seen) > 0 {
				dep, err := opts.API.GetModelsDeploymentsDeploymentId(ctx, opts.ModelID, opts.DeploymentID)
				if err != nil {
					yield(nil, fmt.Errorf("fetch deployment status: %w", err))
					return
				}
				finalFetched = dep
				if _, isStop := stop[dep.Status]; isStop {
					return
				}
			}

			if err := ctx.Sleep(deploymentLogPollInterval); err != nil {
				yield(nil, err)
				return
			}
		}
	}

	return &TailDeploymentLogsResult{
		Logs:                   seq,
		FinalFetchedDeployment: func() *managementapi.Deployment { return finalFetched },
	}
}

// deploymentLogDedupKey identifies a log line across overlapping poll windows.
// Replica is flattened from *string so the key stays comparable.
type deploymentLogDedupKey struct {
	Timestamp string
	Message   string
	Replica   string
}
