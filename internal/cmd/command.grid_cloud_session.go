package cmd

import (
	"fmt"
	"math/rand/v2"
	"os"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/basetenlabs/baseten-go/sandbox"
)

const (
	// cloudSessionTmuxSession must match between create (starts it) and
	// connect (attaches to it).
	cloudSessionTmuxSession = "code"

	cloudSessionDeployTimeout    = 10 * time.Minute
	cloudSessionDeployPollGap    = 2 * time.Second
	cloudSessionInSandboxTimeout = 10 * time.Minute
)

func init() {
	Register("grid cloud-session create", commandGridCloudSessionCreate)
	Register("grid cloud-session connect", commandGridCloudSessionConnect)
	Register("grid cloud-session dispatch", commandGridCloudSessionDispatch)
}

var cloudSessionNameAdjectives = []string{
	"amber", "brisk", "calm", "dapper", "eager", "flint", "gentle", "hazel",
	"ivory", "jolly", "keen", "lucid", "mellow", "nimble", "opal", "plucky",
}

var cloudSessionNameNouns = []string{
	"falcon", "otter", "cedar", "comet", "delta", "ember", "fjord", "gazelle",
	"harbor", "iris", "jasper", "koala", "linden", "mantis", "nectar", "osprey",
}

// cloudSessionGeneratedName is a readable session name, like "amber-falcon-42".
func cloudSessionGeneratedName() string {
	return fmt.Sprintf("%s-%s-%d",
		cloudSessionNameAdjectives[rand.IntN(len(cloudSessionNameAdjectives))],
		cloudSessionNameNouns[rand.IntN(len(cloudSessionNameNouns))],
		rand.IntN(90)+10)
}

// cloudSessionHarnessStartCommand is the command that starts the harness
// interactively.
func cloudSessionHarnessStartCommand(harnessName string) (string, error) {
	switch harnessName {
	case "claude-code":
		return "claude", nil
	case "codex":
		return "codex", nil
	default:
		return "", cmd.NewErrUsagef("--harness %s: not supported by cloud-session yet", harnessName)
	}
}

// cloudSessionHarnessDispatchCommand runs a prompt headless in the harness.
func cloudSessionHarnessDispatchCommand(harnessName string, prompt string) (string, error) {
	switch harnessName {
	case "claude-code":
		return "claude -p " + sandboxShellQuote(prompt), nil
	case "codex":
		return "codex exec " + sandboxShellQuote(prompt), nil
	default:
		return "", cmd.NewErrUsagef("--harness %s: not supported by cloud-session yet", harnessName)
	}
}

// cloudSessionEnvs builds the session's environment variables. The session
// key's local name (--key-env) varies, but it always lands in the sandbox as
// BASETEN_API_KEY, the name the in-sandbox 'grid setup' reads.
func cloudSessionEnvs(flags *cmd.CloudSessionCreateFlags) (*[]managementapi.SandboxEnv, error) {
	keyEnv := flags.KeyEnv
	if keyEnv == "" {
		keyEnv = "BASETEN_API_KEY"
	}
	apiKeyValue, keyIsSet := os.LookupEnv(keyEnv)
	if !keyIsSet || apiKeyValue == "" {
		return nil, cmd.NewErrUsagef("the session's API key is not set: export %s first; a personal API key is never used for a session", keyEnv)
	}
	sandboxAPIKeyName := "BASETEN_API_KEY"
	envs := []managementapi.SandboxEnv{{Name: &sandboxAPIKeyName, Value: &apiKeyValue}}
	for _, envName := range flags.Env {
		envValue, envIsSet := os.LookupEnv(envName)
		if !envIsSet || envValue == "" {
			return nil, cmd.NewErrUsagef("--env %s: not set in the local environment", envName)
		}
		envs = append(envs, managementapi.SandboxEnv{Name: &envName, Value: &envValue})
	}
	return &envs, nil
}

// cloudSessionWaitDeployed polls until the session's sandbox can run commands.
func cloudSessionWaitDeployed(ctx *CommandContext, client *sandbox.Client, teamID *string, name string) error {
	deadline := time.Now().Add(cloudSessionDeployTimeout)
	for {
		record, err := client.API().GetSandbox(ctx, name, managementapi.GetSandboxParams{TeamId: teamID})
		if err != nil {
			return fmt.Errorf("waiting for session %s: %w", name, err)
		}
		switch record.Status {
		case managementapi.SandboxStatus_DEPLOYED:
			return nil
		case managementapi.SandboxStatus_FAILED, managementapi.SandboxStatus_TERMINATED:
			return fmt.Errorf("session %s is %s", name, record.Status)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("session %s did not deploy within %s (status %s)", name, cloudSessionDeployTimeout, record.Status)
		}
		ctx.Logf("Session %s is %s…\n", name, record.Status)
		if err := ctx.Sleep(cloudSessionDeployPollGap); err != nil {
			return err
		}
	}
}

// cloudSessionExecStream runs a command in the session and streams its output
// to the CLI's stderr, so stdout carries create's own record.
func cloudSessionExecStream(ctx *CommandContext, instance *sandbox.Sandbox, commandLine string, workingDir string, timeout time.Duration) error {
	process := instance.Process()
	var finished *sandbox.ProcessInfo
	for event, err := range process.ExecStream(ctx, sandbox.ProcessExecStreamOptions{
		Command:    commandLine,
		WorkingDir: workingDir,
		Timeout:    timeout,
	}) {
		if err != nil {
			return fmt.Errorf("running %q in the session: %w", commandLine, err)
		}
		switch {
		case event.Exit != nil:
			finished = event.Exit
		default:
			ctx.Log(event.Text)
		}
	}
	if finished == nil {
		return fmt.Errorf("running %q in the session: the output ended without the process's exit", commandLine)
	}
	return sandboxProcessExitError(*finished)
}

func commandGridCloudSessionCreate(ctx *CommandContext, flags *cmd.CloudSessionCreateFlags) error {
	envs, err := cloudSessionEnvs(flags)
	if err != nil {
		return err
	}
	harnessStart, err := cloudSessionHarnessStartCommand(flags.Harness)
	if err != nil {
		return err
	}
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	name := flags.Name
	if name == "" {
		name = cloudSessionGeneratedName()
	}
	createIfNotExists := true
	record, err := client.API().CreateSandbox(ctx, managementapi.CreateSandboxParams{TeamId: teamID}, managementapi.CreateSandboxRequest{
		Name:              &name,
		Image:             &flags.Image,
		Memory:            &flags.Memory,
		Envs:              envs,
		CreateIfNotExists: &createIfNotExists,
	})
	if err != nil {
		return fmt.Errorf("creating session %s: %w", name, err)
	}
	name = deref(record.Name)
	if err := cloudSessionWaitDeployed(ctx, client, teamID, name); err != nil {
		return err
	}
	instance, err := client.Get(ctx, sandbox.GetOptions{Name: name})
	if err != nil {
		return err
	}
	setupCommand := "baseten grid setup --harness " + flags.Harness + " --use-session-credential --yes"
	if flags.Team != "" {
		setupCommand += " --team " + sandboxShellQuote(flags.Team)
	}
	ctx.Logf("Setting up %s inside session %s…\n", flags.Harness, name)
	if err := cloudSessionExecStream(ctx, instance, setupCommand, "", cloudSessionInSandboxTimeout); err != nil {
		return err
	}
	tmuxCommand := "tmux has-session -t " + cloudSessionTmuxSession + " 2>/dev/null || " +
		"tmux new-session -d -s " + cloudSessionTmuxSession + " " + sandboxShellQuote(harnessStart)
	if err := cloudSessionExecStream(ctx, instance, tmuxCommand, "/workspace", cloudSessionInSandboxTimeout); err != nil {
		return err
	}
	deployedRecord, err := client.API().GetSandbox(ctx, name, managementapi.GetSandboxParams{TeamId: teamID})
	if err != nil {
		return fmt.Errorf("describing session %s: %w", name, err)
	}
	sandboxOutputRecord(ctx, deployedRecord)
	ctx.Logf("Attach with: baseten grid cloud-session connect --name %s\n", name)
	return nil
}

func commandGridCloudSessionConnect(ctx *CommandContext, flags *cmd.CloudSessionConnectFlags) error {
	harnessStart, err := cloudSessionHarnessStartCommand(flags.Harness)
	if err != nil {
		return err
	}
	// tmux new -A: attach when the session exists, create it with the harness when not.
	attachCommand := "tmux new-session -A -s " + cloudSessionTmuxSession + " " + harnessStart + "\n"
	if err := sandboxConnectTerminal(ctx, flags.SandboxRefFlags, attachCommand); err != nil {
		return err
	}
	ctx.Logf("\nDetached from session %s.\n", flags.Name)
	return nil
}

func commandGridCloudSessionDispatch(ctx *CommandContext, flags *cmd.CloudSessionDispatchFlags) error {
	commandLine, err := cloudSessionHarnessDispatchCommand(flags.Harness, flags.Prompt)
	if err != nil {
		return err
	}
	options, err := sandboxProcessOptions(commandLine, flags.SandboxProcessOptionFlags)
	if err != nil {
		return err
	}
	if options.WorkingDir == "" {
		options.WorkingDir = "/workspace"
	}
	instance, err := sandboxInstance(ctx, flags.SandboxRefFlags)
	if err != nil {
		return err
	}
	process := instance.Process()
	var finished *sandbox.ProcessInfo
	for event, err := range process.ExecStream(ctx, sandbox.ProcessExecStreamOptions{
		Command:    options.Command,
		Env:        options.Env,
		WorkingDir: options.WorkingDir,
		Name:       options.Name,
		Timeout:    options.Timeout,
	}) {
		if err != nil {
			return fmt.Errorf("running the dispatch in session %s: %w", flags.Name, err)
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
		return fmt.Errorf("running the dispatch in session %s: the output ended without the process's exit", flags.Name)
	}
	return sandboxProcessExitError(*finished)
}
