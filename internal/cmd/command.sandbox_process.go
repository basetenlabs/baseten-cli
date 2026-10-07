package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/sandboxapi"
	"github.com/basetenlabs/baseten-go/sandbox"
)

func init() {
	Register("sandbox process start", commandSandboxProcessStart)
	Register("sandbox process list", commandSandboxProcessList)
	Register("sandbox process describe", commandSandboxProcessDescribe)
	Register("sandbox process logs", commandSandboxProcessLogs)
	Register("sandbox process wait", commandSandboxProcessWait)
	Register("sandbox process stop", commandSandboxProcessStop)
	Register("sandbox process kill", commandSandboxProcessKill)
}

func commandSandboxProcessStart(ctx *CommandContext, flags *cmd.SandboxProcessStartFlags) error {
	commandLine, err := sandboxCommandLine(ctx.Args)
	if err != nil {
		return err
	}
	options, err := sandboxProcessOptions(commandLine, flags.SandboxProcessOptionFlags)
	if err != nil {
		return err
	}
	options.KeepAlive = flags.KeepAlive
	options.RestartOnFailure = flags.RestartOnFailure
	options.MaxRestarts = flags.MaxRestarts
	for _, value := range flags.WaitForPort {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return cmd.NewErrUsagef("--wait-for-port must be a port number, got %q", value)
		}
		options.WaitForPorts = append(options.WaitForPorts, port)
	}
	instance, err := sandboxInstance(ctx, flags.SandboxRefFlags)
	if err != nil {
		return err
	}
	started, err := instance.Process().Exec(ctx, options)
	if err != nil {
		return fmt.Errorf("starting process in sandbox %s: %w", flags.Name, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(sandboxProcessResponseFromInfo(*started))
		return nil
	}
	ctx.Outputf("Started process %s in sandbox %s.\n", started.PID, flags.Name)
	ctx.Outputf("Follow its output: baseten sandbox process logs --name %s --pid %s --tail\n", flags.Name, started.PID)
	return nil
}

func commandSandboxProcessList(ctx *CommandContext, flags *cmd.SandboxProcessListFlags) error {
	instance, err := sandboxInstance(ctx, flags.SandboxRefFlags)
	if err != nil {
		return err
	}
	processes, err := instance.Process().List(ctx, sandbox.ProcessListOptions{})
	if err != nil {
		return fmt.Errorf("listing processes in sandbox %s: %w", flags.Name, err)
	}
	if ctx.JSON {
		items := make([]sandboxapi.ProcessResponse, 0, len(processes))
		for _, process := range processes {
			items = append(items, sandboxProcessResponseFromInfo(process))
		}
		ctx.OutputJSON(cmd.SandboxProcessList{Items: items})
		return nil
	}
	if len(processes) == 0 {
		ctx.LogLine("No processes found.")
		return nil
	}
	rows := make([][]string, 0, len(processes))
	for _, process := range processes {
		exit := "-"
		if process.Status != sandbox.ProcessStatusRunning {
			exit = strconv.Itoa(process.ExitCode)
		}
		rows = append(rows, []string{process.PID, process.Name, string(process.Status), exit, sandboxFormatTime(&process.StartedAt), process.Command})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"PID", "NAME", "STATUS", "EXIT", "STARTED", "COMMAND"},
		Rows:                rows,
		RightAlignedColumns: []int{3},
	})
	return nil
}

func commandSandboxProcessDescribe(ctx *CommandContext, flags *cmd.SandboxProcessDescribeFlags) error {
	instance, err := sandboxInstance(ctx, flags.SandboxRefFlags)
	if err != nil {
		return err
	}
	identifier := sandboxProcessIdentifier(flags.SandboxProcessRefFlags)
	process, err := instance.Process().Get(ctx, sandbox.ProcessGetOptions{Identifier: identifier})
	if err != nil {
		return fmt.Errorf("describing process %s in sandbox %s: %w", identifier, flags.Name, err)
	}
	sandboxOutputProcess(ctx, *process)
	return nil
}

func commandSandboxProcessLogs(ctx *CommandContext, flags *cmd.SandboxProcessLogsFlags) error {
	instance, err := sandboxInstance(ctx, flags.SandboxRefFlags)
	if err != nil {
		return err
	}
	identifier := sandboxProcessIdentifier(flags.SandboxProcessRefFlags)
	var writer *JSONArrayWriter
	if ctx.JSON {
		writer = ctx.NewJSONArrayWriter()
		defer writer.Close()
	}

	if flags.Tail {
		for line, err := range instance.Process().StreamLogs(ctx, sandbox.ProcessStreamLogsOptions{Identifier: identifier}) {
			if err != nil {
				return fmt.Errorf("following logs of process %s in sandbox %s: %w", identifier, flags.Name, err)
			}
			switch {
			case writer != nil:
				writer.Write(cmd.SandboxProcessLogLine{Stream: string(line.TextStream), Text: line.Text})
			case line.TextStream == sandbox.ProcessStreamStderr:
				ctx.LogLine(line.Text)
			default:
				ctx.OutputLine(line.Text)
			}
		}
		return nil
	}

	logs, err := instance.Process().Logs(ctx, sandbox.ProcessLogsOptions{Identifier: identifier})
	if err != nil {
		return fmt.Errorf("getting logs of process %s in sandbox %s: %w", identifier, flags.Name, err)
	}
	if logs.Logs == "" {
		if writer == nil {
			ctx.LogLine("No output.")
		}
		return nil
	}
	// The output so far is one text, without a mark of which stream each
	// line came from.
	for line := range strings.Lines(logs.Logs) {
		line = strings.TrimSuffix(line, "\n")
		if writer != nil {
			writer.Write(cmd.SandboxProcessLogLine{Text: line})
		} else {
			ctx.OutputLine(line)
		}
	}
	return nil
}

func commandSandboxProcessWait(ctx *CommandContext, flags *cmd.SandboxProcessWaitFlags) error {
	if flags.Timeout < 0 {
		return cmd.NewErrUsagef("--timeout must be positive")
	}
	instance, err := sandboxInstance(ctx, flags.SandboxRefFlags)
	if err != nil {
		return err
	}
	identifier := sandboxProcessIdentifier(flags.SandboxProcessRefFlags)
	// The SDK reads a negative timeout as no limit.
	timeout := flags.Timeout
	if timeout == 0 {
		timeout = -1
	}
	finished, err := instance.Process().Wait(ctx, sandbox.ProcessWaitOptions{Identifier: identifier, Timeout: timeout})
	if err != nil {
		return fmt.Errorf("waiting for process %s in sandbox %s: %w", identifier, flags.Name, err)
	}
	sandboxOutputProcess(ctx, *finished)
	if ctx.JSON && finished.ExitCode != 0 {
		ctx.SuppressJSONError()
	}
	return sandboxProcessExitError(*finished)
}

func commandSandboxProcessStop(ctx *CommandContext, flags *cmd.SandboxProcessRefCommandFlags) error {
	instance, err := sandboxInstance(ctx, flags.SandboxRefFlags)
	if err != nil {
		return err
	}
	identifier := sandboxProcessIdentifier(flags.SandboxProcessRefFlags)
	if err := instance.Process().Stop(ctx, sandbox.ProcessStopOptions{Identifier: identifier}); err != nil {
		return fmt.Errorf("stopping process %s in sandbox %s: %w", identifier, flags.Name, err)
	}
	ctx.Logf("Requested process %s in sandbox %s to stop.\n", identifier, flags.Name)
	return nil
}

func commandSandboxProcessKill(ctx *CommandContext, flags *cmd.SandboxProcessRefCommandFlags) error {
	instance, err := sandboxInstance(ctx, flags.SandboxRefFlags)
	if err != nil {
		return err
	}
	identifier := sandboxProcessIdentifier(flags.SandboxProcessRefFlags)
	if err := instance.Process().Kill(ctx, sandbox.ProcessKillOptions{Identifier: identifier}); err != nil {
		return fmt.Errorf("killing process %s in sandbox %s: %w", identifier, flags.Name, err)
	}
	ctx.Logf("Requested process %s in sandbox %s to be killed.\n", identifier, flags.Name)
	return nil
}

// sandboxProcessIdentifier is the process --pid or --process-name selects,
// which the API takes either of.
func sandboxProcessIdentifier(ref cmd.SandboxProcessRefFlags) string {
	if ref.PID != "" {
		return ref.PID
	}
	return ref.ProcessName
}

func sandboxOutputProcess(ctx *CommandContext, process sandbox.ProcessInfo) {
	if ctx.JSON {
		ctx.OutputJSON(sandboxProcessResponseFromInfo(process))
		return
	}
	ctx.Outputf("PID:          %s\n", process.PID)
	sandboxOutputField(ctx, "Name:         ", process.Name)
	ctx.Outputf("Command:      %s\n", process.Command)
	ctx.Outputf("Status:       %s\n", process.Status)
	if process.Status != sandbox.ProcessStatusRunning {
		ctx.Outputf("Exit code:    %d\n", process.ExitCode)
	}
	sandboxOutputField(ctx, "Working dir:  ", process.WorkingDir)
	sandboxOutputField(ctx, "Started:      ", sandboxFormatTime(&process.StartedAt))
	sandboxOutputField(ctx, "Completed:    ", sandboxFormatTime(&process.CompletedAt))
	if process.RestartCount > 0 {
		ctx.Outputf("Restarts:     %d\n", process.RestartCount)
	}
}

// sandboxProcessResponseFromInfo converts the SDK's process record back to
// the API's, for JSON output. Times are written as RFC 3339, whatever form
// the sandbox sent them in.
func sandboxProcessResponseFromInfo(process sandbox.ProcessInfo) sandboxapi.ProcessResponse {
	response := sandboxapi.ProcessResponse{
		Pid:              process.PID,
		Name:             process.Name,
		Command:          process.Command,
		Status:           sandboxapi.ProcessResponseStatus(process.Status),
		ExitCode:         process.ExitCode,
		Stdout:           process.Stdout,
		Stderr:           process.Stderr,
		Logs:             process.Logs,
		WorkingDir:       process.WorkingDir,
		KeepAlive:        &process.KeepAlive,
		MaxRestarts:      &process.MaxRestarts,
		RestartCount:     &process.RestartCount,
		RestartOnFailure: &process.RestartOnFailure,
		Stdin:            &process.Stdin,
	}
	if !process.StartedAt.IsZero() {
		response.StartedAt = process.StartedAt.UTC().Format(time.RFC3339)
	}
	if !process.CompletedAt.IsZero() {
		response.CompletedAt = process.CompletedAt.UTC().Format(time.RFC3339)
	}
	return response
}
