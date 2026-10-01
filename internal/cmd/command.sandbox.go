package cmd

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/sandboxconnect"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/basetenlabs/baseten-go/sandbox"
)

func init() {
	Register("sandbox list", commandSandboxList)
	Register("sandbox image-library list", commandSandboxImageLibraryList)
	Register("sandbox image list", commandSandboxImageList)
	Register("sandbox image describe", commandSandboxImageDescribe)
	Register("sandbox image push", commandSandboxImagePush)
	Register("sandbox image delete", commandSandboxImageDelete)
	Register("sandbox connect", commandSandboxConnect)
	Register("sandbox describe", commandSandboxDescribe)
	Register("sandbox create", commandSandboxCreate)
	Register("sandbox update", commandSandboxUpdate)
	Register("sandbox delete", commandSandboxDelete)
	Register("sandbox exec", commandSandboxExec)
	Register("sandbox process exec", commandSandboxExec)
	Register("sandbox process start", commandSandboxProcessStart)
	Register("sandbox process list", commandSandboxProcessList)
	Register("sandbox process logs", commandSandboxProcessLogs)
}

const sandboxPollInterval = 2 * time.Second
const sandboxDeployTimeout = 5 * time.Minute

func commandSandboxList(ctx *CommandContext, flags *cmd.SandboxListFlags) error {
	client, err := ctx.NewSandboxesClient(flags.Team)
	if err != nil {
		return err
	}
	var items []sandbox.SandboxInfo
	for info, err := range client.List(ctx, &sandbox.ListSandboxesRequest{
		Query:    flags.Query,
		Statuses: flags.Status,
	}) {
		if err != nil {
			return fmt.Errorf("listing sandboxes: %w", err)
		}
		items = append(items, *info)
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
	for _, info := range items {
		created := "-"
		if !info.CreatedAt.IsZero() {
			created = info.CreatedAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{
			info.Name,
			info.Status,
			info.State,
			info.Region,
			created,
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"NAME", "STATUS", "STATE", "REGION", "CREATED"},
		Rows:    rows,
	})
	return nil
}

func commandSandboxDescribe(ctx *CommandContext, flags *cmd.SandboxDescribeFlags) error {
	client, err := ctx.NewSandboxesClient(flags.Team)
	if err != nil {
		return err
	}
	info, err := client.GetInfo(ctx, flags.Name, &sandbox.GetInfoOptions{ShowSecrets: flags.ShowSecrets})
	if err != nil {
		return fmt.Errorf("describing sandbox %s: %w", flags.Name, err)
	}
	outputSandboxInfo(ctx, *info)
	return nil
}

func commandSandboxCreate(ctx *CommandContext, flags *cmd.SandboxCreateFlags) error {
	envs, err := parseSandboxEnvs("env", flags.Env)
	if err != nil {
		return err
	}
	labels, err := parseKeyValues("label", flags.Label)
	if err != nil {
		return err
	}
	client, err := ctx.NewSandboxesClient(flags.Team)
	if err != nil {
		return err
	}
	request := &sandbox.CreateSandboxRequest{
		Name:             flags.Name,
		Image:            flags.Image,
		Region:           flags.Region,
		DisplayName:      flags.DisplayName,
		Envs:             envs,
		Labels:           labels,
		CreateIfNotExist: flags.IfNotExists,
	}
	if flags.MemoryMB < 0 {
		return cmd.NewErrUsagef("--memory-mb cannot be negative, got %d", flags.MemoryMB)
	}
	if flags.IfNotExists && request.Name == "" {
		return cmd.NewErrUsagef("--or-get-existing needs --name")
	}
	if flags.MemoryMB > 0 {
		request.Memory = flags.MemoryMB
	}
	created, err := client.Create(ctx, request)
	if err != nil {
		return fmt.Errorf("creating sandbox: %w", err)
	}
	if !flags.Wait {
		outputSandboxInfo(ctx, created.Info())
		return nil
	}

	ctx.Logf("Waiting for sandbox %s to deploy. Press Ctrl+C to stop waiting; the sandbox stays.\n\n", created.Name())
	info, err := waitSandboxDeployed(ctx, client, created.Name())
	if err != nil {
		if ctx.Err() != nil {
			return cmd.NewErrInterrupted(fmt.Errorf(
				"Stopped waiting. Sandbox %s was not deleted and continues deploying.\n\n"+
					"Check status: baseten sandbox describe %s\n"+
					"Delete it:    baseten sandbox delete %s --yes",
				created.Name(), created.Name(), created.Name()))
		}
		return err
	}
	outputSandboxInfo(ctx, *info)
	return nil
}

func commandSandboxUpdate(ctx *CommandContext, flags *cmd.SandboxUpdateFlags) error {
	envs, err := parseSandboxEnvs("env", flags.Env)
	if err != nil {
		return err
	}
	labels, err := parseKeyValues("label", flags.Label)
	if err != nil {
		return err
	}
	client, err := ctx.NewSandboxesClient(flags.Team)
	if err != nil {
		return err
	}
	info, err := client.Update(ctx, flags.Name, &sandbox.UpdateSandboxRequest{
		DisplayName: flags.DisplayName,
		Image:       flags.Image,
		Envs:        envs,
		Labels:      labels,
	})
	if err != nil {
		return fmt.Errorf("updating sandbox %s: %w", flags.Name, err)
	}
	outputSandboxInfo(ctx, *info)
	return nil
}

func commandSandboxDelete(ctx *CommandContext, flags *cmd.SandboxDeleteFlags) error {
	name := flags.Name
	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Delete sandbox %s? This cannot be undone.", name)); err != nil {
			return err
		}
	}
	client, err := ctx.NewSandboxesClient(flags.Team)
	if err != nil {
		return err
	}
	info, err := client.Delete(ctx, name)
	if err != nil {
		return fmt.Errorf("deleting sandbox %s: %w", name, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(*info)
		return nil
	}
	ctx.Logf("Deleting sandbox %s. Deletion continues after this command returns.\n", name)
	outputSandboxInfo(ctx, *info)
	return nil
}

// execCommandLine joins the arguments after -- into one shell command: one
// argument verbatim, several re-quoted to keep their boundaries.
func execCommandLine(args []string) (string, error) {
	if len(args) == 0 {
		return "", cmd.NewErrUsagef("needs a command after --")
	}
	if len(args) == 1 {
		return args[0], nil
	}
	quotedArgs := make([]string, 0, len(args))
	for _, arg := range args {
		quotedArgs = append(quotedArgs, shellQuote(arg))
	}
	return strings.Join(quotedArgs, " "), nil
}

// deployedSandbox resolves a sandbox by name and rejects it unless DEPLOYED.
// action names what the caller wanted to do, for the error message.
func deployedSandbox(ctx *CommandContext, client *sandbox.SandboxesClient, name, action string) (*sandbox.Sandbox, error) {
	info, err := client.GetInfo(ctx, name, nil)
	if err != nil {
		return nil, fmt.Errorf("describing sandbox %s: %w", name, err)
	}
	if info.Status != sandbox.SandboxStatusDeployed {
		return nil, cmd.NewErrUsagef(
			"sandbox %s is %s, not DEPLOYED; only deployed sandboxes %s", name, info.Status, action)
	}
	instance, err := client.SandboxFromInfo(*info)
	if err != nil {
		return nil, err
	}
	return instance, nil
}

func commandSandboxExec(ctx *CommandContext, flags *cmd.SandboxExecFlags) error {
	commandLine, err := execCommandLine(ctx.Args)
	if err != nil {
		return err
	}
	envs, err := parseKeyValues("env", flags.Env)
	if err != nil {
		return err
	}
	name := flags.Name
	client, err := ctx.NewSandboxesClient(flags.Team)
	if err != nil {
		return err
	}
	instance, err := deployedSandbox(ctx, client, name, "run commands")
	if err != nil {
		return err
	}
	execOptions := &sandbox.ExecOptions{Command: commandLine, Env: envs}

	if ctx.JSON {
		// JSON mode prints the final record, so it waits; the streamed
		// path below waits by construction.
		execOptions.WaitForCompletion = true
		finalInfo, err := instance.Process().Exec(ctx, execOptions)
		if err != nil {
			return fmt.Errorf("executing in sandbox %s: %w", name, err)
		}
		ctx.OutputJSON(*finalInfo)
		if finalInfo.ExitCode != 0 {
			ctx.ExitWithCode(finalInfo.ExitCode)
		}
		return nil
	}

	var finalResult *sandbox.ProcessInfo
	for event, err := range instance.Process().ExecStream(ctx, execOptions) {
		if err != nil {
			return fmt.Errorf("executing in sandbox %s: %w", name, err)
		}
		switch event.Type {
		case sandbox.ExecEventStdout:
			ctx.Output(event.Text + "\n")
		case sandbox.ExecEventStderr:
			ctx.Logf("%s\n", event.Text)
		case sandbox.ExecEventResult:
			finalResult = event.Result
		}
	}
	if finalResult == nil {
		return fmt.Errorf("executing in sandbox %s: the stream ended without a result", name)
	}
	if finalResult.ExitCode != 0 {
		ctx.ExitWithCode(finalResult.ExitCode)
	}
	return nil
}

func commandSandboxProcessStart(ctx *CommandContext, flags *cmd.SandboxExecFlags) error {
	commandLine, err := execCommandLine(ctx.Args)
	if err != nil {
		return err
	}
	envs, err := parseKeyValues("env", flags.Env)
	if err != nil {
		return err
	}
	name := flags.Name
	client, err := ctx.NewSandboxesClient(flags.Team)
	if err != nil {
		return err
	}
	instance, err := deployedSandbox(ctx, client, name, "run commands")
	if err != nil {
		return err
	}
	started, err := instance.Process().Exec(ctx, &sandbox.ExecOptions{
		Command:           commandLine,
		Env:               envs,
		WaitForCompletion: false,
	})
	if err != nil {
		return fmt.Errorf("starting a process in sandbox %s: %w", name, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(*started)
		return nil
	}
	ctx.Outputf("Started process %s (%s) in sandbox %s.\n", started.PID, started.Name, name)
	ctx.Outputf("Logs: baseten sandbox process logs --name %s --pid %s\n", name, started.PID)
	return nil
}

func commandSandboxProcessList(ctx *CommandContext, flags *cmd.SandboxNameFlags) error {
	client, err := ctx.NewSandboxesClient(flags.Team)
	if err != nil {
		return err
	}
	instance, err := deployedSandbox(ctx, client, flags.Name, "run commands")
	if err != nil {
		return err
	}
	infos, err := instance.Process().List(ctx)
	if err != nil {
		return fmt.Errorf("listing processes in sandbox %s: %w", flags.Name, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.SandboxProcessList{Items: infos})
		return nil
	}
	if len(infos) == 0 {
		ctx.LogLine("No processes found.")
		return nil
	}
	rows := make([][]string, 0, len(infos))
	for _, info := range infos {
		started := "-"
		if !info.StartedAt.IsZero() {
			started = info.StartedAt.UTC().Format(time.RFC3339)
		}
		exit := "-"
		if info.Status != "running" {
			exit = strconv.Itoa(info.ExitCode)
		}
		rows = append(rows, []string{info.PID, info.Name, info.Status, exit, started})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"PID", "NAME", "STATUS", "EXIT", "STARTED"},
		Rows:                rows,
		RightAlignedColumns: []int{3},
	})
	return nil
}

func commandSandboxProcessLogs(ctx *CommandContext, flags *cmd.SandboxProcessLogsFlags) error {
	client, err := ctx.NewSandboxesClient(flags.Team)
	if err != nil {
		return err
	}
	instance, err := deployedSandbox(ctx, client, flags.Name, "run commands")
	if err != nil {
		return err
	}
	logs, err := instance.Process().Logs(ctx, flags.PID)
	if err != nil {
		return fmt.Errorf("getting logs of process %s in sandbox %s: %w", flags.PID, flags.Name, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(*logs)
		return nil
	}
	if logs.Logs == "" {
		ctx.LogLine("No logs.")
		return nil
	}
	ctx.Output(logs.Logs)
	if !strings.HasSuffix(logs.Logs, "\n") {
		ctx.Output("\n")
	}
	return nil
}

// waitSandboxDeployed polls until the sandbox is DEPLOYED, fails fast on
// FAILED, and errors on timeout.
func waitSandboxDeployed(ctx *CommandContext, client *sandbox.SandboxesClient, name string) (*sandbox.SandboxInfo, error) {
	deadline := time.Now().Add(sandboxDeployTimeout)
	for {
		info, err := client.GetInfo(ctx, name, nil)
		if err != nil {
			return nil, fmt.Errorf("checking sandbox %s: %w", name, err)
		}
		switch {
		case info.Status == sandbox.SandboxStatusDeployed:
			return info, nil
		case info.Status == sandbox.SandboxStatusFailed:
			return nil, fmt.Errorf("sandbox %s failed to deploy", name)
		case time.Now().After(deadline):
			return nil, fmt.Errorf(
				"sandbox %s was still %s after %s; check later with 'baseten sandbox describe %s'",
				name, info.Status, sandboxDeployTimeout, name)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(sandboxPollInterval):
		}
	}
}

func outputSandboxInfo(ctx *CommandContext, info sandbox.SandboxInfo) {
	if ctx.JSON {
		ctx.OutputJSON(info)
		return
	}
	ctx.Outputf("Name:        %s\n", info.Name)
	ctx.Outputf("Status:      %s\n", info.Status)
	if info.State != "" {
		ctx.Outputf("State:       %s\n", info.State)
	}
	if info.URL != "" {
		ctx.Outputf("URL:         %s\n", info.URL)
	}
	if info.DisplayName != "" {
		ctx.Outputf("Display:     %s\n", info.DisplayName)
	}
	if info.Image != "" {
		ctx.Outputf("Image:       %s\n", info.Image)
	}
	if info.Memory > 0 {
		ctx.Outputf("Memory:      %d MB\n", info.Memory)
	}
	if info.Region != "" {
		ctx.Outputf("Region:      %s\n", info.Region)
	}
	ctx.Outputf("Enabled:     %t\n", info.Enabled)
	if !info.CreatedAt.IsZero() {
		ctx.Outputf("Created:     %s\n", info.CreatedAt.UTC().Format(time.RFC3339))
	}
	if len(info.Labels) > 0 {
		ctx.Outputf("Labels:      %s\n", formatSandboxLabels(info.Labels))
	}
	if info.ExpiresInSeconds > 0 {
		ctx.Outputf("Expires in:  %ds\n", info.ExpiresInSeconds)
	}
}

func formatSandboxLabels(labels map[string]string) string {
	pairs := make([]string, 0, len(labels))
	for key, value := range labels {
		pairs = append(pairs, key+"="+value)
	}
	// Sorted, so the same labels always print the same way.
	slices.Sort(pairs)
	return strings.Join(pairs, ", ")
}

// parseKeyValues turns KEY=VALUE flag repeats into a map, rejecting malformed
// entries rather than silently dropping them.
func parseKeyValues(flagName string, values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	parsed := make(map[string]string, len(values))
	for _, value := range values {
		key, rest, found := strings.Cut(value, "=")
		if !found || key == "" {
			return nil, cmd.NewErrUsagef("--%s entries must be KEY=VALUE, got %q", flagName, value)
		}
		parsed[key] = rest
	}
	return parsed, nil
}

// parseSandboxEnvs wraps parsed KEY=VALUE entries as sandbox envs. Nil stays
// nil: the API reads nil as "leave unchanged" and an allocated empty map as
// "replace with none".
func parseSandboxEnvs(flagName string, values []string) (map[string]sandbox.SandboxEnvValue, error) {
	parsed, err := parseKeyValues(flagName, values)
	if err != nil {
		return nil, err
	}
	if parsed == nil {
		return nil, nil
	}
	envs := make(map[string]sandbox.SandboxEnvValue, len(parsed))
	for key, value := range parsed {
		envs[key] = sandbox.SandboxEnvValue{Value: value}
	}
	return envs, nil
}

// shellQuote renders one argument for the command string the exec API runs
// through a shell, keeping argument boundaries intact.
func shellQuote(arg string) string {
	if arg == "" {
		return "''"
	}
	for _, r := range arg {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '/' || r == '.' || r == '=' || r == ':' || r == '@' ||
			r == '%' || r == '+' || r == ',' {
			continue
		}
		return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return arg
}

func commandSandboxImageList(ctx *CommandContext, flags *cmd.SandboxTeamFlags) error {
	client, err := ctx.NewSandboxesClient(flags.Team)
	if err != nil {
		return err
	}
	var items []sandbox.ImageInfo
	for info, err := range client.Images().List(ctx, &sandbox.ImageListOptions{}) {
		if err != nil {
			return fmt.Errorf("listing sandbox images: %w", err)
		}
		items = append(items, *info)
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.SandboxImageList{Items: items})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No sandbox images found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, info := range items {
		rows = append(rows, []string{
			info.Name,
			info.Status,
			strconv.FormatInt(info.TagCount, 10),
			formatBytes(info.SizeBytes),
			info.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"NAME", "STATUS", "TAGS", "SIZE", "CREATED"},
		Rows:                rows,
		RightAlignedColumns: []int{2, 3},
	})
	return nil
}

func commandSandboxImageDescribe(ctx *CommandContext, flags *cmd.SandboxImageNameFlags) error {
	client, err := ctx.NewSandboxesClient(flags.Team)
	if err != nil {
		return err
	}
	info, err := client.Images().GetInfo(ctx, flags.Name)
	if err != nil {
		return fmt.Errorf("describing sandbox image %s: %w", flags.Name, err)
	}
	outputSandboxImageInfo(ctx, *info)
	return nil
}

func commandSandboxImagePush(ctx *CommandContext, flags *cmd.SandboxImagePushFlags) error {
	client, err := ctx.NewSandboxesClient(flags.Team)
	if err != nil {
		return err
	}
	name := flags.Name
	if flags.Wait {
		ctx.Logf("Waiting for image %s to build. Press Ctrl+C to stop waiting; the build continues.\n\n", name)
	}
	info, err := client.Images().Push(ctx, &sandbox.ImagePushOptions{
		Name:          name,
		Directory:     flags.Dir,
		RegistryImage: flags.Image,
		WaitForBuilt:  flags.Wait,
	})
	if err != nil {
		if ctx.Err() != nil {
			return cmd.NewErrInterrupted(fmt.Errorf(
				"Stopped waiting. Whatever the server accepted before this continues; a build that never started does not.\n\n"+
					"Check status: baseten sandbox image describe %s",
				name))
		}
		return fmt.Errorf("pushing sandbox image %s: %w", name, err)
	}
	outputSandboxImageInfo(ctx, *info)
	return nil
}

func commandSandboxImageDelete(ctx *CommandContext, flags *cmd.SandboxDeleteFlags) error {
	name := flags.Name
	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Delete sandbox image %s? This cannot be undone.", name)); err != nil {
			return err
		}
	}
	client, err := ctx.NewSandboxesClient(flags.Team)
	if err != nil {
		return err
	}
	info, err := client.Images().Delete(ctx, name)
	if err != nil {
		return fmt.Errorf("deleting sandbox image %s: %w", name, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(*info)
		return nil
	}
	ctx.Logf("Deleting sandbox image %s.\n", name)
	outputSandboxImageInfo(ctx, *info)
	return nil
}

func commandSandboxConnect(ctx *CommandContext, flags *cmd.SandboxNameFlags) error {
	if !ctx.IsInteractive() {
		return cmd.NewErrUsagef("connect requires an interactive terminal")
	}
	name := flags.Name
	client, err := ctx.NewSandboxesClient(flags.Team)
	if err != nil {
		return err
	}
	instance, err := deployedSandbox(ctx, client, name, "accept terminals")
	if err != nil {
		return err
	}

	// The terminal reads the sandbox token from the WebSocket query, so mint
	// one through the session's management credential.
	managementClient, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	minted, err := managementClient.API().PostToken(ctx, managementapi.CreateTokenRequest{
		Scopes: []managementapi.TokenScope{managementapi.TokenScope_sandboxes},
	})
	if err != nil {
		return fmt.Errorf("minting a sandbox token: %w", err)
	}
	wsURL, err := sandboxconnect.WebSocketURL(instance.URL(), minted.Token)
	if err != nil {
		return err
	}
	stdin, stdinIsFile := ctx.Stdin.(*os.File)
	stdout, stdoutIsFile := ctx.Stdout.(*os.File)
	if !stdinIsFile || !stdoutIsFile {
		return cmd.NewErrUsagef("connect requires terminal stdin and stdout")
	}

	ctx.Logf("Connecting to sandbox %s. Press Ctrl+D to disconnect.\n\n", name)
	terminal, err := sandboxconnect.Dial(ctx, wsURL, stdin, stdout)
	if err != nil {
		return err
	}
	if err := terminal.Run(ctx); err != nil {
		return err
	}
	ctx.Logf("\nDisconnected from sandbox %s.\n", name)
	return nil
}

func outputSandboxImageInfo(ctx *CommandContext, info sandbox.ImageInfo) {
	if ctx.JSON {
		ctx.OutputJSON(info)
		return
	}
	ctx.Outputf("Name:        %s\n", info.Name)
	ctx.Outputf("Status:      %s\n", info.Status)
	if info.DisplayName != "" {
		ctx.Outputf("Display:     %s\n", info.DisplayName)
	}
	if info.TagCount > 0 {
		ctx.Outputf("Tags:        %d\n", info.TagCount)
	}
	if info.SizeBytes > 0 {
		ctx.Outputf("Size:        %s\n", formatBytes(info.SizeBytes))
	}
	if !info.CreatedAt.IsZero() {
		ctx.Outputf("Created:     %s\n", info.CreatedAt.UTC().Format(time.RFC3339))
	}
	if !info.LastDeployedAt.IsZero() {
		ctx.Outputf("Last used:   %s\n", info.LastDeployedAt.UTC().Format(time.RFC3339))
	}
}

// commandSandboxImageLibraryList lists the platform's starter images from
// the starter-image library, hidden and coming-soon entries left out by the SDK.
func commandSandboxImageLibraryList(ctx *CommandContext, _ *cmd.SandboxLibraryListFlags) error {
	client, err := ctx.NewSandboxesClient("")
	if err != nil {
		return err
	}
	images, err := client.LibraryImages(ctx)
	if err != nil {
		return fmt.Errorf("listing starter images: %w", err)
	}

	items := make([]cmd.SandboxLibraryImage, 0, len(images))
	for _, image := range images {
		memory := image.MemoryMB
		items = append(items, cmd.SandboxLibraryImage{
			Name:        image.Name,
			DisplayName: image.DisplayName,
			Image:       image.Image,
			Description: image.Description,
			MemoryMB:    &memory,
			Categories:  image.Categories,
		})
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.SandboxLibraryImageList{Items: items})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No starter images found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, image := range items {
		display := image.DisplayName
		if display == "" {
			display = image.Name
		}
		memory := "-"
		if image.MemoryMB != nil && *image.MemoryMB > 0 {
			memory = strconv.Itoa(*image.MemoryMB)
		}
		rows = append(rows, []string{
			display,
			image.Image,
			memory,
			strings.Join(image.Categories, ","),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"NAME", "IMAGE", "DEFAULT MB", "CATEGORIES"},
		Rows:                rows,
		RightAlignedColumns: []int{2},
	})
	return nil
}
