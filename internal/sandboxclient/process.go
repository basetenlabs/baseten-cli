package sandboxclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"

	"github.com/basetenlabs/baseten-go/client/sandboxapi"
)

// ExecOptions runs one command in a sandbox.
type ExecOptions struct {
	// Command is the shell command to run.
	Command string

	// WorkingDir is the directory to run the command in. Empty uses the
	// sandbox's default.
	WorkingDir string

	// Env holds environment variables for the process, on top of the
	// sandbox's own.
	Env map[string]string

	// Name refers to the process by name, instead of its pid.
	Name string

	// TimeoutSeconds is after which the process is killed. Zero means
	// never.
	TimeoutSeconds int

	// WaitForCompletion returns only once the process has exited. When
	// false, the process keeps running in the background.
	WaitForCompletion bool
}

// SandboxProcess runs and inspects processes in a sandbox.
type SandboxProcess struct {
	api *sandboxapi.Client
	url string
}

// requireURL rejects exec before a request is built against a sandbox whose
// record has no execution URL yet, which a freshly created one lacks.
func (p *SandboxProcess) requireURL() error {
	if p.url == "" {
		return errors.New("sandbox has no URL yet; wait for it to deploy")
	}
	return nil
}

// Exec starts a command and returns its state.
//
// Exec is never retried on a lost response: a command may have side effects
// even then. The one re-send is a revoked token, which the server rejected
// before any work was done, so repeating the request repeats no side effect.
func (p *SandboxProcess) Exec(ctx context.Context, opts *ExecOptions) (*ProcessInfo, error) {
	if err := p.requireURL(); err != nil {
		return nil, err
	}
	response, err := p.api.PostProcess(ctx, execRequest(opts, opts.WaitForCompletion))
	if err != nil {
		return nil, toSandboxAPIError(err, "exec")
	}
	info, err := processInfoFromAPI(response)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// List returns the sandbox's processes.
func (p *SandboxProcess) List(ctx context.Context) ([]ProcessInfo, error) {
	if err := p.requireURL(); err != nil {
		return nil, err
	}
	processes, err := p.api.GetProcess(ctx)
	if err != nil {
		return nil, toSandboxAPIError(err, "exec")
	}
	infos := make([]ProcessInfo, 0, len(*processes))
	for i := range *processes {
		info, err := processInfoFromAPI(&(*processes)[i])
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// Logs returns one process's captured output, by pid or name.
func (p *SandboxProcess) Logs(ctx context.Context, identifier string) (*ProcessLogs, error) {
	if err := p.requireURL(); err != nil {
		return nil, err
	}
	logs, err := p.api.GetProcessLogs(ctx, identifier)
	if err != nil {
		return nil, toSandboxAPIError(err, "exec")
	}
	return &ProcessLogs{Stdout: logs.Stdout, Stderr: logs.Stderr, Logs: logs.Logs}, nil
}

// ExecEventType is the kind of one streamed exec event. The values come from
// the execution API; others may be added, so do not treat the constants as
// exhaustive.
type ExecEventType = string

// Values for ExecEventType.
const (
	// ExecEventStdout carries one line of the command's standard output.
	ExecEventStdout ExecEventType = "stdout"

	// ExecEventStderr carries one line of the command's standard error.
	ExecEventStderr ExecEventType = "stderr"

	// ExecEventResult carries the final record of the exited process.
	ExecEventResult ExecEventType = "result"

	// ExecEventError carries the server's reason the command could not run.
	ExecEventError ExecEventType = "error"

	// ExecEventKeepalive is a periodic ping that keeps the connection open.
	ExecEventKeepalive ExecEventType = "keepalive"
)

// ExecEvent is one event of a running command's stream.
type ExecEvent struct {
	// Type of the event.
	Type ExecEventType

	// Text is the event's line of output for Stdout and Stderr, without its
	// newline, or the server's message for Error.
	Text string

	// Result is the final record of the exited process, set on Result.
	Result *ProcessInfo
}

// execStreamEvent is the wire shape the execution API streams: newline
// delimited {"type": ..., "data": ...}, the data holding an output line, an
// error message, or the final process record as JSON.
type execStreamEvent struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// ExecStream starts a command and yields its output as events arrive, ending
// with the final record of the exited process. An error yields as the pair's
// second value and ends the iteration.
func (p *SandboxProcess) ExecStream(ctx context.Context, opts *ExecOptions) iter.Seq2[ExecEvent, error] {
	return func(yield func(ExecEvent, error) bool) {
		if err := p.requireURL(); err != nil {
			yield(ExecEvent{}, err)
			return
		}
		response, err := p.api.PostProcessRaw(ctx, execRequest(opts, true))
		if err != nil {
			yield(ExecEvent{}, toSandboxAPIError(err, "exec"))
			return
		}
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		// One event per line, and a line can carry arbitrarily much output.
		scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
		sawResult := false
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var event execStreamEvent
			if err := json.Unmarshal(line, &event); err != nil {
				yield(ExecEvent{}, err)
				return
			}
			switch event.Type {
			case ExecEventResult:
				var finalResult sandboxapi.ProcessResponse
				if err := json.Unmarshal([]byte(event.Data), &finalResult); err != nil {
					yield(ExecEvent{}, err)
					return
				}
				info, err := processInfoFromAPI(&finalResult)
				if err != nil {
					yield(ExecEvent{}, err)
					return
				}
				sawResult = true
				// The result is the stream's last event; stop reading there
				// rather than at whatever follows the server's shutdown.
				if !yield(ExecEvent{Type: ExecEventResult, Result: &info}, nil) {
					return
				}
				return
			case ExecEventError:
				yield(ExecEvent{}, fmt.Errorf("sandbox exec: %s", event.Data))
				return
			case ExecEventKeepalive:
				// A ping, not output; nothing to yield.
			default:
				if !yield(ExecEvent{Type: event.Type, Text: event.Data}, nil) {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			yield(ExecEvent{}, err)
			return
		}
		if !sawResult {
			yield(ExecEvent{}, errors.New("exec stream ended without a result event"))
		}
	}
}

func execRequest(opts *ExecOptions, waitForCompletion bool) sandboxapi.ProcessRequest {
	request := sandboxapi.ProcessRequest{
		Command:           opts.Command,
		WaitForCompletion: &waitForCompletion,
	}
	if opts.WorkingDir != "" {
		request.WorkingDir = &opts.WorkingDir
	}
	if len(opts.Env) > 0 {
		env := opts.Env
		request.Env = &env
	}
	if opts.Name != "" {
		request.Name = &opts.Name
	}
	if opts.TimeoutSeconds > 0 {
		request.Timeout = &opts.TimeoutSeconds
	}
	return request
}
