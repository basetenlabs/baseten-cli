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
	defaultLogWindow             = 30 * time.Minute
	maxLogPageSize               = 1000
	deploymentLogPollInterval    = 2 * time.Second
	deploymentLogClockSkewBuffer = 60 * time.Second
	deploymentLogDedupRetention  = 30 * time.Minute
)

// errLogsHitLimit is yielded by paginateLogs after all logs are drained when
// the --limit cap was reached. It is not a command failure: the caller turns it
// into a stderr note and returns success. Other yielded errors (including the
// single-millisecond-burst case) propagate as command failures.
var errLogsHitLimit = errors.New("reached the log line limit")

func init() {
	Register("model deployment logs", commandModelDeploymentLogs)
}

func commandModelDeploymentLogs(ctx *CommandContext, flags *cmd.ModelDeploymentLogsFlags) error {
	api, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, api.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}

	fetchLogs := func(q logQuery) (*managementapi.GetLogsResponse, error) {
		return api.API().GetModelsDeploymentsLogs(ctx, ref.ID, flags.DeploymentID, deploymentLogParams(q))
	}
	fetchStatus := func() (*managementapi.Deployment, error) {
		return api.API().GetModelsDeploymentsDeploymentId(ctx, ref.ID, flags.DeploymentID)
	}
	return runLogsCommand(ctx, &flags.LogFlags, fetchLogs, fetchStatus)
}

// logQuery is the transport-neutral set of log-query parameters shared by the
// deployment and environment logs endpoints. Each command maps it onto its own
// generated query-params type.
type logQuery struct {
	StartEpochMillis *int
	EndEpochMillis   *int
	Limit            *int
	MinLevel         *managementapi.LogLevel
	Includes         *[]string
	Excludes         *[]string
	SearchPattern    *string
	Replica          *string
	RequestId        *string
}

// logFetcher fetches a page of logs for the given query.
type logFetcher func(q logQuery) (*managementapi.GetLogsResponse, error)

// statusFetcher resolves the deployment whose status gates a --tail loop. For a
// deployment command it is the deployment itself; for an environment command it
// is the environment's current deployment.
type statusFetcher func() (*managementapi.Deployment, error)

// deploymentLogParams maps a transport-neutral logQuery onto the deployment
// logs GET query-params type.
func deploymentLogParams(q logQuery) managementapi.GetV1ModelsModelIdDeploymentsDeploymentIdLogsParams {
	direction := managementapi.SortOrder_desc
	return managementapi.GetV1ModelsModelIdDeploymentsDeploymentIdLogsParams{
		StartEpochMillis: q.StartEpochMillis,
		EndEpochMillis:   q.EndEpochMillis,
		Limit:            q.Limit,
		Direction:        &direction,
		MinLevel:         q.MinLevel,
		Includes:         q.Includes,
		Excludes:         q.Excludes,
		SearchPattern:    q.SearchPattern,
		Replica:          q.Replica,
		RequestId:        q.RequestId,
	}
}

// runLogsCommand implements the shared logs command flow for both deployment and
// environment logs. It validates the flags, resolves the time window and
// filters, and either tails or fetches a single window via the supplied
// fetchers. fetchStatus is only used by the --tail path.
func runLogsCommand(ctx *CommandContext, flags *cmd.LogFlags, fetchLogs logFetcher, fetchStatus statusFetcher) error {
	hasStart := !flags.Start.IsZero()
	hasEnd := !flags.End.IsZero()
	// Use Changed rather than the zero value so explicit --since 0 fails
	// the positive-duration check below instead of being silently dropped.
	hasSince := ctx.Command.Flags().Changed("since")
	hasLimit := ctx.Command.Flags().Changed("limit")
	hasPageSize := ctx.Command.Flags().Changed("page-size")
	hasFilters := flags.MinLevel != "" || len(flags.Includes) > 0 || len(flags.Excludes) > 0 ||
		flags.SearchPattern != "" || flags.Replica != "" || flags.RequestID != ""
	if flags.Tail && (hasStart || hasEnd || hasSince || hasLimit || hasPageSize || hasFilters) {
		return cmd.NewErrUsagef("--tail cannot be combined with the time-range, --limit, or filter flags")
	}
	if hasSince && (hasStart || hasEnd) {
		return cmd.NewErrUsagef("--since cannot be combined with --start or --end")
	}
	if flags.Limit < 0 {
		return cmd.NewErrUsagef("--limit must be zero (no limit) or a positive number")
	}
	// page-size is capped at the backend's max limit: requesting more would be
	// silently clamped server-side, making a full page look short and ending
	// pagination early.
	if flags.PageSize < 1 || flags.PageSize > maxLogPageSize {
		return cmd.NewErrUsagef("--page-size must be between 1 and %d", maxLogPageSize)
	}

	if flags.Tail {
		res := tailLogs(ctx, tailLogsOptions{FetchLogs: fetchLogs, FetchStatus: fetchStatus})
		if err := emitLogs(ctx, res.Logs); err != nil {
			return err
		}
		if dep := res.FinalFetchedDeployment(); dep != nil {
			ctx.Logf("Tailing stopped: deployment status %s\n", dep.Status)
		}
		return nil
	}

	// Resolve --start/--end/--since into a concrete [startMs, endMs] window.
	// Unlike the server-default behavior, both bounds are resolved client-side
	// so the pagination loop has a fixed floor (startMs) to page back to;
	// otherwise a per-request server default would slide under each page and
	// paging could not tell a sparse window from an exhausted one.
	now := ctx.Now()
	var startMs, endMs int
	if hasSince {
		if flags.Since <= 0 {
			return cmd.NewErrUsagef("--since must be a positive duration")
		}
		if flags.Since > maxLogTimeRange {
			return cmd.NewErrUsagef("--since must be at most 7d")
		}
		endMs = int(now.UnixMilli())
		startMs = int(now.Add(-flags.Since).UnixMilli())
	} else {
		if hasEnd {
			endMs = int(flags.End.UnixMilli())
		} else {
			endMs = int(now.UnixMilli())
		}
		if hasStart {
			startMs = int(flags.Start.UnixMilli())
		} else {
			startMs = endMs - int(defaultLogWindow.Milliseconds())
		}
		if startMs >= endMs {
			return cmd.NewErrUsagef("--start must be earlier than --end")
		}
		if endMs-startMs > int(maxLogTimeRange.Milliseconds()) {
			return cmd.NewErrUsagef("log time range must be at most 7 days; narrow --start/--end or use --since")
		}
	}

	var q logQuery
	q.StartEpochMillis = &startMs
	if flags.MinLevel != "" {
		level := managementapi.LogLevel(strings.ToUpper(flags.MinLevel))
		q.MinLevel = &level
	}
	if len(flags.Includes) > 0 {
		q.Includes = &flags.Includes
	}
	if len(flags.Excludes) > 0 {
		q.Excludes = &flags.Excludes
	}
	if flags.SearchPattern != "" {
		q.SearchPattern = &flags.SearchPattern
	}
	if flags.Replica != "" {
		q.Replica = &flags.Replica
	}
	if flags.RequestID != "" {
		q.RequestId = &flags.RequestID
	}

	// paginateLogs signals why the stream ended via a sentinel error after all
	// logs are drained; hitting the limit or a burst is a note, not a failure.
	err := emitLogs(ctx, paginateLogs(q, startMs, endMs, flags.Limit, flags.PageSize, fetchLogs))
	if errors.Is(err, errLogsHitLimit) {
		ctx.Logf("Reached the --limit of %d log lines; older lines in the window were omitted. Increase --limit or use --limit 0 for no limit.\n", flags.Limit)
	} else if err != nil {
		return err
	}
	return nil
}

// paginateLogs streams logs newest-first, paging backward through the
// [startMs, endMs] window until the window is exhausted or limit lines have
// been emitted (limit 0 means no limit). Each page fetches up to pageSize lines
// ending at the previous page's oldest line; because end is millisecond-granular
// while log timestamps are nanosecond-granular, the seam millisecond is
// re-fetched and deduped so no line is lost or duplicated across the boundary.
// It ends by yielding errLogsHitLimit when the limit was hit, or a descriptive
// error when a single millisecond overflows a page.
func paginateLogs(q logQuery, startMs, endMs, limit, pageSize int, fetchLogs logFetcher) iter.Seq2[*managementapi.Log, error] {
	return func(yield func(*managementapi.Log, error) bool) {
		emitted := 0
		curEnd := endMs
		// prevBoundaryKeys holds the dedup keys of the previous page's lines at
		// prevBoundaryMs (its oldest millisecond), which the next page re-fetches
		// because end is inclusive at millisecond granularity.
		prevBoundaryMs := int64(-1)
		prevBoundaryKeys := map[deploymentLogDedupKey]struct{}{}

		for {
			if limit > 0 && emitted >= limit {
				return
			}
			// Build this page's query: a full page ending at the current window
			// end. We always request the full pageSize and cap emission at limit
			// rather than shrinking the last request to the remaining count. A
			// shrunk page could be entirely consumed by the seam-dedup overlap,
			// stopping one line short of limit and misreporting a burst.
			pq := q
			e := curEnd
			pq.EndEpochMillis = &e
			pq.Limit = &pageSize

			resp, err := fetchLogs(pq)
			if err != nil {
				yield(nil, err)
				return
			}
			n := len(resp.Logs)
			if n == 0 {
				return
			}

			// Logs arrive newest-first, so the last line is the oldest and
			// anchors the next page's end.
			oldestMs, oldestOK := logTimestampMs(resp.Logs[n-1].Timestamp)
			newBoundaryKeys := map[deploymentLogDedupKey]struct{}{}
			newInPage := 0
			for i := range resp.Logs {
				log := resp.Logs[i]
				key := logDedupKey(log)
				ms, ok := logTimestampMs(log.Timestamp)
				if ok && ms == prevBoundaryMs {
					if _, dup := prevBoundaryKeys[key]; dup {
						continue
					}
				}
				if ok && oldestOK && ms == oldestMs {
					newBoundaryKeys[key] = struct{}{}
				}
				if !yield(&log, nil) {
					return
				}
				emitted++
				newInPage++
				if limit > 0 && emitted >= limit {
					yield(nil, errLogsHitLimit)
					return
				}
			}

			// A short page means the window is fully covered.
			if n < pageSize {
				return
			}
			// A full page that yielded nothing new means one millisecond holds
			// more lines than a page can carry; paging by millisecond cannot
			// advance past it, so fail loudly rather than silently drop lines.
			if newInPage == 0 {
				yield(nil, fmt.Errorf("cannot page past a single millisecond holding more than %d log lines; narrow --start/--end/--since or add filters to reduce log density", pageSize))
				return
			}
			if !oldestOK || oldestMs <= int64(startMs) {
				return
			}
			prevBoundaryMs = oldestMs
			prevBoundaryKeys = newBoundaryKeys
			curEnd = int(oldestMs)
		}
	}
}

// logTimestampMs parses an epoch-nanosecond log timestamp into epoch millis.
func logTimestampMs(ts string) (int64, bool) {
	ns, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return 0, false
	}
	return ns / int64(time.Millisecond), true
}

// emitLogs drains an iterator of log records onto stdout in the caller-selected
// output mode. For ctx.JSON (both json and jsonl) it uses a JSON array writer so
// jsonl streams one record per line and json buffers into a single closed
// array. For text it formats each line via FormatDeploymentLogLine.
func emitLogs(ctx *CommandContext, logs iter.Seq2[*managementapi.Log, error]) error {
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

	// StopOnActive stops the tail when the deployment reaches ACTIVE. By
	// default ACTIVE is treated as a runnable state and tailing continues
	// (matching `truss model logs --tail`). push --tail --wait sets this
	// so a successful deploy ends the tail.
	StopOnActive bool

	// WarmupTimeout is how long to silently retry 404s from the logs API
	// at the start of the tail, before any successful poll. After the
	// first successful logs response, 404s are surfaced as errors. Zero
	// means no warmup retries. (Status-API 404s are never retried here.)
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

// TailDeploymentLogs tails a specific deployment's logs. It is a thin adapter
// over tailLogs that wires the deployment logs and describe endpoints.
func TailDeploymentLogs(ctx *CommandContext, opts TailDeploymentLogsOptions) *TailDeploymentLogsResult {
	return tailLogs(ctx, tailLogsOptions{
		FetchLogs: func(q logQuery) (*managementapi.GetLogsResponse, error) {
			return opts.API.GetModelsDeploymentsLogs(ctx, opts.ModelID, opts.DeploymentID, deploymentLogParams(q))
		},
		FetchStatus: func() (*managementapi.Deployment, error) {
			return opts.API.GetModelsDeploymentsDeploymentId(ctx, opts.ModelID, opts.DeploymentID)
		},
		StopOnActive:  opts.StopOnActive,
		WarmupTimeout: opts.WarmupTimeout,
	})
}

// tailLogsOptions configures tailLogs, the transport-neutral tail loop shared by
// the deployment and environment logs commands.
type tailLogsOptions struct {
	// FetchLogs fetches a poll window of logs. Required.
	FetchLogs logFetcher
	// FetchStatus resolves the deployment whose status gates the tail.
	// Required.
	FetchStatus statusFetcher

	// StopOnActive stops the tail when the deployment reaches ACTIVE. By
	// default ACTIVE is treated as a runnable state and tailing continues.
	StopOnActive bool

	// WarmupTimeout is how long to silently retry 404s from the logs API at
	// the start of the tail, before any successful poll. Zero means no
	// warmup retries.
	WarmupTimeout time.Duration
}

// tailLogs polls logs and streams new records until the gating deployment's
// status leaves the runnable set or the context is cancelled. The runnable set
// is {BUILDING, DEPLOYING, LOADING_MODEL, UPDATING, WAKING_UP} plus ACTIVE when
// StopOnActive is false (default). Any other status, including unknown ones,
// stops the tail. Dedup is by (timestamp, message, replica) across overlapping
// clock-skew windows. Clock-and-sleep behavior is taken from ctx (overridable
// via WithNow / WithSleep for tests).
func tailLogs(ctx *CommandContext, opts tailLogsOptions) *TailDeploymentLogsResult {
	var finalFetched *managementapi.Deployment

	seq := func(yield func(*managementapi.Log, error) bool) {
		// seen maps each delivered log key to the wall-clock time it was
		// first observed. Entries older than deploymentLogDedupRetention
		// are evicted each poll so a long-running tail does not grow the
		// map without bound; the retention window is far larger than the
		// clock-skew overlap, so dedup correctness is preserved.
		seen := map[deploymentLogDedupKey]time.Time{}
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
			resp, err := opts.FetchLogs(logQuery{StartEpochMillis: startMs, EndEpochMillis: &endMs})
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
			// Evict dedup keys older than the retention window so a
			// long-running tail does not grow the map without bound.
			cutoff := ctx.Now().Add(-deploymentLogDedupRetention)
			for k, t := range seen {
				if t.Before(cutoff) {
					delete(seen, k)
				}
			}
			// Poll windows overlap by deploymentLogClockSkewBuffer on each
			// side to tolerate server/client clock skew, so the same record
			// can reappear across polls; dedup by (timestamp, message,
			// replica).
			for i := range resp.Logs {
				log := resp.Logs[i]
				key := logDedupKey(log)
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = ctx.Now()
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
				dep, err := opts.FetchStatus()
				if err != nil {
					yield(nil, fmt.Errorf("fetch deployment status: %w", err))
					return
				}
				finalFetched = dep
				switch dep.Status {
				case managementapi.DeploymentStatus_BUILDING,
					managementapi.DeploymentStatus_DEPLOYING,
					managementapi.DeploymentStatus_LOADING_MODEL,
					managementapi.DeploymentStatus_UPDATING,
					managementapi.DeploymentStatus_WAKING_UP:
					// keep polling
				case managementapi.DeploymentStatus_ACTIVE:
					if opts.StopOnActive {
						return
					}
				default:
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

// logDedupKey builds a comparable dedup key for a log line, flattening the
// optional replica to the empty string.
func logDedupKey(log managementapi.Log) deploymentLogDedupKey {
	replica := ""
	if log.Replica != nil {
		replica = *log.Replica
	}
	return deploymentLogDedupKey{Timestamp: log.Timestamp, Message: log.Message, Replica: replica}
}
