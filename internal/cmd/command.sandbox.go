package cmd

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/sandboxconnect"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/basetenlabs/baseten-go/client/sandboxapi"
	"github.com/basetenlabs/baseten-go/sandbox"
	"golang.org/x/term"
)

func init() {
	Register("sandbox list", commandSandboxList)
	Register("sandbox describe", commandSandboxDescribe)
	Register("sandbox create", commandSandboxCreate)
	Register("sandbox update", commandSandboxUpdate)
	Register("sandbox delete", commandSandboxDelete)
	Register("sandbox exec", commandSandboxExec)
	Register("sandbox connect", commandSandboxConnect)
}

func commandSandboxList(ctx *CommandContext, flags *cmd.SandboxListFlags) error {
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	params := managementapi.ListSandboxesParams{TeamId: teamID}
	if flags.Query != "" {
		params.Q = &flags.Query
	}
	if len(flags.Status) > 0 {
		statuses := make([]string, 0, len(flags.Status))
		for _, status := range flags.Status {
			statuses = append(statuses, enumToAPIValue(status))
		}
		params.Status = &statuses
	}
	items, truncated, err := sandboxCollectPages(flags.Limit, func(cursor *string, pageLimit int) ([]managementapi.Sandbox, managementapi.SandboxApiPagination, error) {
		params.Cursor, params.Limit = cursor, &pageLimit
		page, err := client.API().ListSandboxes(ctx, params)
		if err != nil {
			return nil, managementapi.SandboxApiPagination{}, err
		}
		return page.Items, page.Pagination, nil
	})
	if err != nil {
		return fmt.Errorf("listing sandboxes: %w", err)
	}
	if truncated {
		ctx.Logf("Reached the --limit of %d; more exist. Increase --limit or use --limit 0 for no limit.\n", flags.Limit)
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.SandboxList{Items: items})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No sandboxes found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{deref(item.Name), string(item.Status), deref(item.Region), sandboxFormatTime(item.CreatedAt)})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"NAME", "STATUS", "REGION", "CREATED"},
		Rows:    rows,
	})
	return nil
}

// sandboxListPageSize is the most items asked for per page, the sandbox API's
// largest page.
const sandboxListPageSize = 100

// sandboxCollectPages collects a sandbox API listing page by page through
// fetch, until limit items (0 for no limit) or the last page, and reports
// whether the limit left items out. fetch gets the cursor of the page to get,
// nil for the first, and the most items to return.
func sandboxCollectPages[T any](limit int, fetch func(cursor *string, pageLimit int) ([]T, managementapi.SandboxApiPagination, error)) ([]T, bool, error) {
	if limit < 0 {
		return nil, false, cmd.NewErrUsagef("--limit must be zero (no limit) or a positive number")
	}
	var items []T
	var cursor *string
	seen := map[string]bool{}
	for {
		pageLimit := sandboxListPageSize
		if limit > 0 {
			pageLimit = min(pageLimit, limit-len(items))
		}
		page, pagination, err := fetch(cursor, pageLimit)
		if err != nil {
			return nil, false, err
		}
		items = append(items, page...)
		more := pagination.HasMore && pagination.Cursor != nil
		if limit > 0 && len(items) >= limit {
			return items[:limit], more || len(items) > limit, nil
		} else if !more {
			return items, false, nil
		}
		// A repeated cursor would page forever.
		if seen[*pagination.Cursor] {
			return nil, false, fmt.Errorf("the server returned the cursor %q twice", *pagination.Cursor)
		}
		seen[*pagination.Cursor] = true
		cursor = pagination.Cursor
	}
}

func commandSandboxDescribe(ctx *CommandContext, flags *cmd.SandboxDescribeFlags) error {
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	params := managementapi.GetSandboxParams{TeamId: teamID}
	if flags.ShowSecrets {
		params.ShowSecrets = &flags.ShowSecrets
	}
	record, err := client.API().GetSandbox(ctx, flags.Name, params)
	if err != nil {
		return fmt.Errorf("describing sandbox %s: %w", flags.Name, err)
	}
	sandboxOutputRecord(ctx, record)
	return nil
}

func commandSandboxCreate(ctx *CommandContext, flags *cmd.SandboxCreateFlags) error {
	envs, err := sandboxParseEnvs(flags.SandboxEnvFlags)
	if err != nil {
		return err
	}
	labels, err := sandboxParseKeyValues("label", flags.Label)
	if err != nil {
		return err
	}
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	request := managementapi.CreateSandboxRequest{
		Name:   sandboxOptionalString(flags.Name),
		Region: sandboxOptionalString(flags.Region),
		Image:  sandboxOptionalString(flags.Image),
		Envs:   envs,
	}
	if flags.IfNotExists {
		request.CreateIfNotExists = &flags.IfNotExists
	}
	if flags.Memory != 0 {
		request.Memory = &flags.Memory
	}
	if labels != nil {
		sandboxLabels := managementapi.SandboxMetadataLabels(labels)
		request.Labels = &sandboxLabels
	}
	record, err := client.API().CreateSandbox(ctx, managementapi.CreateSandboxParams{TeamId: teamID}, request)
	if err != nil {
		return fmt.Errorf("creating sandbox: %w", err)
	}
	sandboxOutputRecord(ctx, record)
	return nil
}

func commandSandboxUpdate(ctx *CommandContext, flags *cmd.SandboxUpdateFlags) error {
	envs, err := sandboxParseEnvs(flags.SandboxEnvFlags)
	if err != nil {
		return err
	}
	labels, err := sandboxParseKeyValues("label", flags.Label)
	if err != nil {
		return err
	}
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	request := managementapi.UpdateSandboxRequest{Image: sandboxOptionalString(flags.Image), Envs: envs}
	if labels != nil {
		sandboxLabels := managementapi.SandboxMetadataLabels(labels)
		request.Labels = &sandboxLabels
	}
	record, err := client.API().UpdateSandbox(ctx, flags.Name, managementapi.UpdateSandboxParams{TeamId: teamID}, request)
	if err != nil {
		return fmt.Errorf("updating sandbox %s: %w", flags.Name, err)
	}
	sandboxOutputRecord(ctx, record)
	return nil
}

func commandSandboxDelete(ctx *CommandContext, flags *cmd.SandboxDeleteFlags) error {
	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Delete sandbox %s and everything in it? This cannot be undone.", flags.Name)); err != nil {
			return err
		}
	}
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	record, err := client.API().DeleteSandbox(ctx, flags.Name, managementapi.DeleteSandboxParams{TeamId: teamID})
	if err != nil {
		return fmt.Errorf("deleting sandbox %s: %w", flags.Name, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(record)
		return nil
	}
	ctx.Logf("Deleting sandbox %s.\n", flags.Name)
	return nil
}

func commandSandboxExec(ctx *CommandContext, flags *cmd.SandboxExecFlags) error {
	commandLine, err := sandboxCommandLine(ctx.Args)
	if err != nil {
		return err
	}
	options, err := sandboxProcessOptions(commandLine, flags.SandboxProcessOptionFlags)
	if err != nil {
		return err
	}
	instance, err := sandboxInstance(ctx, flags.SandboxRefFlags)
	if err != nil {
		return err
	}
	process := instance.Process()

	// Stdin can only be written once the process exists, and a streamed exec
	// yields nothing until the process writes, so with stdin the process is
	// started, then its stdin written while its output is followed, then
	// waited for: what 'process start' and 'process wait' do.
	if flags.Stdin {
		options.Stdin = true
		started, err := process.Exec(ctx, options)
		if err != nil {
			return fmt.Errorf("running command in sandbox %s: %w", flags.Name, err)
		}
		pid := started.PID
		// Copies stdin in chunks, closing the process's stdin at end of input.
		stdinDone := make(chan error, 1)
		go func() {
			buf := make([]byte, 64*1024)
			for {
				n, readErr := ctx.Stdin.Read(buf)
				if n > 0 {
					if err := process.WriteStdin(ctx, sandbox.ProcessWriteStdinOptions{Identifier: pid, Data: slices.Clone(buf[:n])}); err != nil {
						stdinDone <- err
						return
					}
				}
				if errors.Is(readErr, io.EOF) {
					stdinDone <- process.CloseStdin(ctx, sandbox.ProcessCloseStdinOptions{Identifier: pid})
					return
				} else if readErr != nil {
					stdinDone <- readErr
					return
				}
			}
		}()
		if !ctx.JSON {
			for line, err := range process.StreamLogs(ctx, sandbox.ProcessStreamLogsOptions{Identifier: pid}) {
				if err != nil {
					return fmt.Errorf("running command in sandbox %s: %w", flags.Name, err)
				}
				if line.TextStream == sandbox.ProcessStreamStderr {
					ctx.LogLine(line.Text)
				} else {
					ctx.OutputLine(line.Text)
				}
			}
		}
		finished, err := process.Wait(ctx, sandbox.ProcessWaitOptions{Identifier: pid, Timeout: -1})
		if err != nil {
			return fmt.Errorf("running command in sandbox %s: %w", flags.Name, err)
		}
		// A process can exit without reading all of its input, which fails the
		// copy without being the command's failure, so only a copy failure
		// while the process still runs counts.
		select {
		case err := <-stdinDone:
			if err != nil && finished.Status == sandbox.ProcessStatusRunning {
				return fmt.Errorf("writing stdin to sandbox %s: %w", flags.Name, err)
			}
		default:
		}
		if ctx.JSON {
			ctx.OutputJSON(sandboxProcessResponseFromInfo(*finished))
			ctx.SuppressJSONError()
		}
		return sandboxProcessExitError(*finished)
	}

	if ctx.JSON {
		// Exec is never retried, so the generated client loses nothing, and
		// its record is the API's own.
		waitForCompletion := true
		request := sandboxapi.ProcessRequest{
			Command:           options.Command,
			WorkingDir:        sandboxOptionalString(options.WorkingDir),
			Name:              sandboxOptionalString(options.Name),
			WaitForCompletion: &waitForCompletion,
		}
		if len(options.Env) > 0 {
			request.Env = &options.Env
		}
		if options.Timeout > 0 {
			// Whole seconds, rounded up so a sub-second timeout is not none.
			seconds := int((options.Timeout + time.Second - 1) / time.Second)
			request.Timeout = &seconds
		}
		finished, err := instance.API().PostProcess(ctx, request)
		if err != nil {
			return fmt.Errorf("running command in sandbox %s: %w", flags.Name, err)
		}
		ctx.OutputJSON(finished)
		if finished.ExitCode != 0 {
			ctx.SuppressJSONError()
			return &ErrSubprocess{Err: fmt.Errorf("command exited with code %d", finished.ExitCode), Code: finished.ExitCode}
		}
		return nil
	}

	var finished *sandbox.ProcessInfo
	for event, err := range process.ExecStream(ctx, sandbox.ProcessExecStreamOptions{
		Command:    options.Command,
		Env:        options.Env,
		WorkingDir: options.WorkingDir,
		Name:       options.Name,
		Timeout:    options.Timeout,
	}) {
		if err != nil {
			return fmt.Errorf("running command in sandbox %s: %w", flags.Name, err)
		}
		switch {
		case event.Exit != nil:
			finished = event.Exit
		case event.TextStream == sandbox.ProcessStreamStderr:
			ctx.Log(event.Text)
		default:
			ctx.Output(event.Text)
		}
	}
	if finished == nil {
		return fmt.Errorf("running command in sandbox %s: the output ended without the process's exit", flags.Name)
	}
	return sandboxProcessExitError(*finished)
}

func commandSandboxConnect(ctx *CommandContext, flags *cmd.SandboxConnectFlags) error {
	// Checked before anything is sent: the session takes over the terminal,
	// reading keys from stdin and drawing to stdout.
	stdin, stdinIsFile := ctx.Stdin.(*os.File)
	stdout, stdoutIsFile := ctx.Stdout.(*os.File)
	if !ctx.IsInteractive() || !stdinIsFile || !stdoutIsFile || !term.IsTerminal(int(stdout.Fd())) {
		return cmd.NewErrUsagef("connect requires an interactive terminal for both input and output")
	}
	instance, err := sandboxInstance(ctx, flags.SandboxRefFlags)
	if err != nil {
		return err
	}
	// The terminal endpoint reads the sandbox token from its URL, so connect
	// mints its own.
	managementClient, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	minted, err := managementClient.API().PostToken(ctx, managementapi.CreateTokenRequest{
		Scopes: []managementapi.TokenScope{managementapi.TokenScope_sandboxes},
	})
	if err != nil {
		return fmt.Errorf("getting a sandbox token: %w", err)
	}
	ctx.Logf("Connecting to sandbox %s. Exit the shell or press Ctrl+D to disconnect.\n\n", flags.Name)
	terminal, err := sandboxconnect.Dial(ctx, sandboxconnect.DialOptions{
		SandboxURL: instance.URL(),
		Token:      minted.Token,
		Input:      stdin,
		Output:     stdout,
		Errors:     ctx.Stderr,
		Terminal:   sandboxconnect.NewOSTerminal(stdin, stdout),
	})
	if err != nil {
		return err
	}
	if err := terminal.Run(ctx); err != nil {
		return err
	}
	ctx.Logf("\nDisconnected from sandbox %s.\n", flags.Name)
	return nil
}

// sandboxInstance gets a handle to work in a sandbox.
func sandboxInstance(ctx *CommandContext, ref cmd.SandboxRefFlags) (*sandbox.Sandbox, error) {
	client, _, err := ctx.NewSandboxClient(ref.Team)
	if err != nil {
		return nil, err
	}
	instance, err := client.Get(ctx, sandbox.GetOptions{Name: ref.Name})
	if err != nil {
		return nil, fmt.Errorf("getting sandbox %s: %w", ref.Name, err)
	}
	return instance, nil
}

// sandboxCommandLine joins the arguments after -- into one shell command
// line: one argument as is, several quoted so each stays one argument.
func sandboxCommandLine(args []string) (string, error) {
	if len(args) == 0 {
		return "", cmd.NewErrUsagef("a command is required after --")
	} else if len(args) == 1 {
		return args[0], nil
	}
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		// Quoted for a POSIX shell, unless it has nothing the shell would
		// interpret.
		if arg == "" || strings.IndexFunc(arg, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_/.=:@%+,", r))
		}) >= 0 {
			arg = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
		}
		quoted = append(quoted, arg)
	}
	return strings.Join(quoted, " "), nil
}

// sandboxProcessOptions builds the SDK options for a command that 'sandbox
// exec' or 'sandbox process start' runs.
func sandboxProcessOptions(commandLine string, flags cmd.SandboxProcessOptionFlags) (sandbox.ProcessExecOptions, error) {
	env, err := sandboxParseKeyValues("env", flags.Env)
	if err != nil {
		return sandbox.ProcessExecOptions{}, err
	}
	if flags.Timeout < 0 {
		return sandbox.ProcessExecOptions{}, cmd.NewErrUsagef("--timeout must be positive")
	}
	return sandbox.ProcessExecOptions{
		Command:    commandLine,
		Env:        env,
		WorkingDir: flags.WorkingDir,
		Name:       flags.ProcessName,
		Timeout:    flags.Timeout,
	}, nil
}

// sandboxProcessExitError is the error for a process that exited with a
// failure, whose exit code becomes the CLI's.
func sandboxProcessExitError(info sandbox.ProcessInfo) error {
	if info.ExitCode == 0 {
		return nil
	}
	return &ErrSubprocess{Err: fmt.Errorf("command exited with code %d", info.ExitCode), Code: info.ExitCode}
}

func sandboxOutputRecord(ctx *CommandContext, record *managementapi.Sandbox) {
	if ctx.JSON {
		ctx.OutputJSON(record)
		return
	}
	ctx.Outputf("Name:         %s\n", deref(record.Name))
	ctx.Outputf("Status:       %s\n", record.Status)
	sandboxOutputField(ctx, "URL:          ", deref(record.Url))
	sandboxOutputField(ctx, "Image:        ", deref(record.Image))
	if record.Memory != nil {
		ctx.Outputf("Memory:       %d MB\n", *record.Memory)
	}
	sandboxOutputField(ctx, "Region:       ", deref(record.Region))
	sandboxOutputField(ctx, "External ID:  ", deref(record.ExternalId))
	if record.Labels != nil && len(*record.Labels) > 0 {
		labels := *record.Labels
		pairs := make([]string, 0, len(labels))
		// Sorted, so the same labels always print the same way.
		for _, key := range slices.Sorted(maps.Keys(labels)) {
			pairs = append(pairs, key+"="+labels[key])
		}
		ctx.Outputf("Labels:       %s\n", strings.Join(pairs, ", "))
	}
	if record.Envs != nil && len(*record.Envs) > 0 {
		pairs := make([]string, 0, len(*record.Envs))
		for _, env := range *record.Envs {
			pairs = append(pairs, deref(env.Name)+"="+deref(env.Value))
		}
		slices.Sort(pairs)
		ctx.Outputf("Envs:         %s\n", strings.Join(pairs, ", "))
	}
	if record.Ports != nil && len(*record.Ports) > 0 {
		ports := make([]string, 0, len(*record.Ports))
		for _, port := range *record.Ports {
			ports = append(ports, strconv.Itoa(port.Target))
		}
		ctx.Outputf("Ports:        %s\n", strings.Join(ports, ", "))
	}
	sandboxOutputField(ctx, "Created:      ", sandboxFormatTime(record.CreatedAt))
	sandboxOutputField(ctx, "Last used:    ", sandboxFormatTime(record.LastUsedAt))
	if record.ExpiresIn != nil {
		ctx.Outputf("Expires in:   %s\n", time.Duration(*record.ExpiresIn)*time.Second)
	}
}

// sandboxOutputField prints one labeled field, unless its value is empty or
// a placeholder.
func sandboxOutputField(ctx *CommandContext, label, value string) {
	if value != "" && value != "-" {
		ctx.Outputf("%s%s\n", label, value)
	}
}

func sandboxFormatTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

// sandboxParseKeyValues turns KEY=VALUE flag repeats into a map, nil when
// there are none, rejecting malformed entries rather than dropping them.
func sandboxParseKeyValues(flagName string, values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	parsed := make(map[string]string, len(values))
	for _, value := range values {
		key, rest, found := strings.Cut(value, "=")
		if !found || key == "" {
			return nil, cmd.NewErrUsagef("--%s must be KEY=VALUE, got %q", flagName, value)
		}
		parsed[key] = rest
	}
	return parsed, nil
}

// sandboxParseEnvs builds a sandbox's environment variables, nil when no flag
// sets any. --env leaves secret out, so the server's default, secret, applies.
func sandboxParseEnvs(flags cmd.SandboxEnvFlags) (*[]managementapi.SandboxEnv, error) {
	secret, err := sandboxParseKeyValues("env", flags.Env)
	if err != nil {
		return nil, err
	}
	plain, err := sandboxParseKeyValues("plain-env", flags.PlainEnv)
	if err != nil {
		return nil, err
	}
	if secret == nil && plain == nil {
		return nil, nil
	}
	notSecret := false
	var envs []managementapi.SandboxEnv
	// Sorted, so the same flags always send the same request.
	for _, name := range slices.Sorted(maps.Keys(secret)) {
		envs = append(envs, managementapi.SandboxEnv{Name: &name, Value: new(secret[name])})
	}
	for _, name := range slices.Sorted(maps.Keys(plain)) {
		if _, ok := secret[name]; ok {
			return nil, cmd.NewErrUsagef("environment variable %s is set by both --env and --plain-env", name)
		}
		envs = append(envs, managementapi.SandboxEnv{Name: &name, Value: new(plain[name]), Secret: &notSecret})
	}
	return &envs, nil
}

func sandboxOptionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
