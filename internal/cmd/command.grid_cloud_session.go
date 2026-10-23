package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/basetenlabs/baseten-go/sandbox"
)

const (
	// cloudSessionTmuxSession must match between create (starts it) and
	// connect (attaches to it).
	cloudSessionTmuxSession = "code"

	// cloudSessionCredentialEnv holds the dedicated sandbox token that
	// authenticates the session's harness and management traffic; the
	// backend's secret store replaces this lookup in production.
	cloudSessionCredentialEnv = "BASETEN_API_KEY_CODING_SANDBOX"

	// cloudSessionGitHubPatEnv holds the launcher user's GitHub credential.
	// The web UI's GitHub connector (or Runlayer's) replaces this lookup.
	cloudSessionGitHubPatEnv = "GITHUB_SANDBOX_PAT"

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

// cloudSessionStayAwakeScript registers a keepAlive process via the local
// execution API: registration alone holds off the platform's ~30-40s idle
// pause (proven empirically; in-guest activity is invisible to the counter).
// Driven by hooks: UserPromptSubmit starts it, Stop releases it, so the
// sandbox is awake exactly while the harness works a turn.
const cloudSessionStayAwakeScript = `#!/bin/bash
# Turn-based sandbox keep-alive, driven by Claude Code session hooks.
#   UserPromptSubmit -> stay-awake.sh start : register a keepAlive process;
#                      the sandbox-api holds off idle hibernation while any
#                      keepAlive process runs (verified empirically).
#   Stop             -> stay-awake.sh stop  : kill it, letting the sandbox
#                      hibernate ~30-40s after the turn ends.
# The process itself is a bare sleep: registration, not activity, is what
# holds the sandbox awake.
set -u
API=http://localhost:8080
NAME=stay-awake-turn

case "${1:-}" in
  start)
    # Idempotent: a running registration from a previous turn is replaced.
    curl -s -m 5 -X DELETE "$API/process/$NAME/kill" -o /dev/null
    curl -s -m 5 -X POST "$API/process" \
      -H "Content-Type: application/json" \
      -d '{"command": "sleep 3600", "keepAlive": true, "timeout": 0, "name": "'"$NAME"'"}' \
      -o /dev/null
    ;;
  stop)
    curl -s -m 5 -X DELETE "$API/process/$NAME/kill" -o /dev/null
    ;;
  status)
    curl -s -m 5 "$API/process" | python3 -c '
import json, sys
running = [p for p in json.load(sys.stdin)
           if p.get("name") == "stay-awake-turn" and p["status"] == "running"]
print("awake" if running else "released")'
    ;;
  *)
    echo "usage: $0 start|stop|status" >&2
    exit 2
    ;;
esac
`

// cloudSessionTurnLivenessHooks wires the stay-awake script into the harness.
const cloudSessionTurnLivenessHooks = `{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"/root/hooks-alive/stay-awake.sh start","timeout":15}]}],"Stop":[{"hooks":[{"type":"command","command":"/root/hooks-alive/stay-awake.sh stop","timeout":15}]}]}`

// cloudSessionPrepareTurnLiveness installs the stay-awake script and merges
// the turn-liveness hooks into the harness settings the in-sandbox setup
// already wrote.
func cloudSessionPrepareTurnLiveness(ctx *CommandContext, instance *sandbox.Sandbox) error {
	scriptB64 := base64.StdEncoding.EncodeToString([]byte(cloudSessionStayAwakeScript))
	writeScript := "mkdir -p /root/hooks-alive && printf %s " + sandboxShellQuote(scriptB64) +
		" | base64 -d > /root/hooks-alive/stay-awake.sh && chmod +x /root/hooks-alive/stay-awake.sh"
	if err := cloudSessionExecStream(ctx, instance, writeScript, "", cloudSessionInSandboxTimeout); err != nil {
		return err
	}
	// Idempotent: a re-run of create must not duplicate hook entries.
	mergeHooks := "python3 -c 'import json,os,sys; p=os.path.expanduser(\"~/.claude/settings.json\");" +
		" s=json.load(open(p)) if os.path.exists(p) else {};" +
		" h=s.get(\"hooks\"); s[\"hooks\"]=h if isinstance(h,dict) else {};" +
		" [s[\"hooks\"].setdefault(k,[]).extend([e for e in v if json.dumps(e,sort_keys=True) not in {json.dumps(x,sort_keys=True) for x in s[\"hooks\"][k]}]) for k,v in json.loads(sys.argv[1]).items()];" +
		" json.dump(s,open(p,\"w\"))' " + sandboxShellQuote(cloudSessionTurnLivenessHooks)
	return cloudSessionExecStream(ctx, instance, mergeHooks, "", cloudSessionInSandboxTimeout)
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

// cloudSessionHarnessResumeCommand restarts the harness resuming its last
// conversation. Idle suspend freezes files, not processes, so a reconnect
// starts a new process; resuming the conversation is the closest to "where I
// left off" it can get.
func cloudSessionHarnessResumeCommand(harnessName string) (string, error) {
	switch harnessName {
	case "claude-code":
		return "sh -c 'claude --continue 2>/dev/null || claude'", nil
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

// cloudSessionEnvs builds the sandbox's env vars; the placeholder
// BASETEN_API_KEY is all an agent can leak, the proxy injects the real
// credential on the wire.
func cloudSessionEnvs(flags *cmd.CloudSessionCreateFlags) (*[]managementapi.SandboxEnv, error) {
	sandboxAPIKeyName := "BASETEN_API_KEY"
	sandboxAPIKeyValue := "proxy-injected"
	envs := []managementapi.SandboxEnv{{Name: &sandboxAPIKeyName, Value: &sandboxAPIKeyValue}}
	// Vite rejects the preview host by default (DNS-rebinding check); this whitelists only the preview domain.
	viteAllowedHostsName := "__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS"
	viteAllowedHostsValue := ".preview.b10.co"
	envs = append(envs, managementapi.SandboxEnv{Name: &viteAllowedHostsName, Value: &viteAllowedHostsValue})
	for _, envName := range flags.Env {
		envValue, envIsSet := os.LookupEnv(envName)
		if !envIsSet || envValue == "" {
			return nil, cmd.NewErrUsagef("--env %s: not set in the local environment", envName)
		}
		envs = append(envs, managementapi.SandboxEnv{Name: &envName, Value: &envValue})
	}
	return &envs, nil
}

// cloudSessionURLHost extracts a URL's host for proxy destination matching.
func cloudSessionURLHost(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parsing %q: %w", rawURL, err)
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("parsing %q: no host", rawURL)
	}
	return parsed.Hostname(), nil
}

// cloudSessionProxyNetwork puts the session's credentials in the proxy's
// write-only secret store, replacing the Authorization header on harness,
// management, and Runlayer traffic; the placeholders in the sandbox are all
// an agent can leak.
func cloudSessionProxyNetwork(harnessHost string, apiKeyValue string, runlayerM2MToken string, githubPat string) *managementapi.SandboxNetwork {
	routing := []managementapi.SandboxProxyTarget{{
		Destinations: &[]string{harnessHost, "*.baseten.co"},
		Headers:      &map[string]string{"Authorization": "Bearer {{SECRET:session_key}}"},
		Secrets:      &map[string]string{"session_key": apiKeyValue},
	}}
	if runlayerM2MToken != "" {
		routing = append(routing, managementapi.SandboxProxyTarget{
			Destinations: &[]string{"baseten.runlayer.com"},
			Headers:      &map[string]string{"Authorization": "Bearer {{SECRET:runlayer_m2m}}"},
			Secrets:      &map[string]string{"runlayer_m2m": runlayerM2MToken},
		})
	}
	if githubPat != "" {
		// github.com's git endpoints reject Bearer and take Basic auth only;
		// api.github.com takes Bearer. Both values live in the write-only
		// store, the Basic one precomputed host-side.
		gitBasic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + githubPat))
		routing = append(routing,
			managementapi.SandboxProxyTarget{
				Destinations: &[]string{"github.com"},
				Headers:      &map[string]string{"Authorization": "Basic {{SECRET:github_basic}}"},
				Secrets:      &map[string]string{"github_basic": gitBasic},
			},
			managementapi.SandboxProxyTarget{
				Destinations: &[]string{"api.github.com"},
				Headers:      &map[string]string{"Authorization": "Bearer {{SECRET:github_pat}}"},
				Secrets:      &map[string]string{"github_pat": githubPat},
			})
	}
	return &managementapi.SandboxNetwork{Proxy: &managementapi.SandboxProxyConfig{Routing: &routing}}
}

// cloudSessionRunlayerM2MToken mints a Runlayer agent-account token and, when
// the launcher user's identity is configured, exchanges it for an OBO token
// carrying that user's connectors and policies. The client secret stays on the
// launcher; only the token reaches the proxy.
func cloudSessionRunlayerM2MToken(clientIDEnv string, clientSecretEnv string, servers []string) (string, error) {
	clientID, idIsSet := os.LookupEnv(clientIDEnv)
	clientSecret, secretIsSet := os.LookupEnv(clientSecretEnv)
	if !idIsSet || clientID == "" || !secretIsSet || clientSecret == "" {
		return "", cmd.NewErrUsagef("--runlayer-server needs the Runlayer agent account: export %s and %s", clientIDEnv, clientSecretEnv)
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	// Every target gets named on both calls: an exchange cannot widen past
	// the agent token's scope (RFC 8707).
	resources := make([]string, len(servers))
	for i, server := range servers {
		resources[i] = "https://baseten.runlayer.com/api/v1/proxy/" + server + "/mcp"
	}
	if cloudSessionRunlayerUserEmail != "" {
		form["resource"] = resources
	}
	token, err := cloudSessionRunlayerTokenRequest(form)
	if err != nil {
		return "", err
	}
	if cloudSessionRunlayerUserEmail == "" {
		return token, nil
	}
	obo, err := cloudSessionRunlayerTokenRequest(url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"actor_token":        {token},
		"actor_token_type":   {"urn:ietf:params:oauth:token-type:access_token"},
		"subject_token":      {cloudSessionRunlayerUserEmail},
		"subject_token_type": {"urn:runlayer:token-type:user-email"},
		"resource":           resources,
	})
	if err != nil {
		return "", fmt.Errorf("exchanging for the user's Runlayer token: %w", err)
	}
	return obo, nil
}

func cloudSessionRunlayerTokenRequest(form url.Values) (string, error) {
	response, err := http.PostForm("https://baseten.runlayer.com/api/v1/oauth/token", form)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", response.StatusCode)
	}
	var tokenResponse struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokenResponse); err != nil || tokenResponse.AccessToken == "" {
		return "", fmt.Errorf("the response has no access_token")
	}
	return tokenResponse.AccessToken, nil
}

// cloudSessionRunlayerMCPConfig writes the session's Runlayer MCP servers into
// the in-sandbox ~/.claude.json; the Bearer is a placeholder the proxy
// replaces on baseten.runlayer.com traffic.
func cloudSessionRunlayerMCPConfig(ctx *CommandContext, instance *sandbox.Sandbox, serverIDs []string) error {
	mcpServers := map[string]any{}
	for _, serverID := range serverIDs {
		mcpServers[serverID] = map[string]any{
			"type":    "http",
			"url":     "https://baseten.runlayer.com/api/v1/proxy/" + serverID + "/mcp",
			"headers": map[string]string{"Authorization": "Bearer proxy-injected"},
		}
	}
	serversJSON, err := json.Marshal(mcpServers)
	if err != nil {
		return fmt.Errorf("encoding the Runlayer MCP config: %w", err)
	}
	mergeConfig := "python3 -c 'import json,os,sys; p=os.path.expanduser(\"~/.claude.json\");" +
		" c=json.load(open(p)) if os.path.exists(p) else {};" +
		" c.setdefault(\"mcpServers\",{}).update(json.loads(sys.argv[1]));" +
		" json.dump(c,open(p,\"w\"))' " + sandboxShellQuote(string(serversJSON))
	return cloudSessionExecStream(ctx, instance, mergeConfig, "", cloudSessionInSandboxTimeout)
}

// cloudSessionWaitDeployed polls until the session's sandbox can run commands.
func cloudSessionWaitDeployed(ctx *CommandContext, client *sandbox.Client, teamID *string, name string) error {
	deadline := time.Now().Add(cloudSessionDeployTimeout)
	waiting := startSpinner(ctx, "Waiting for the sandbox to deploy")
	defer waiting.Stop("")
	lastStatus := ""
	for {
		record, err := client.API().GetSandbox(ctx, name, managementapi.GetSandboxParams{TeamId: teamID})
		if err != nil {
			return fmt.Errorf("waiting for session %s: %w", name, err)
		}
		switch record.Status {
		case managementapi.SandboxStatus_DEPLOYED:
			waiting.Stop(fmt.Sprintf("Session %s deployed", name))
			return nil
		case managementapi.SandboxStatus_FAILED, managementapi.SandboxStatus_TERMINATED:
			waiting.Stop("")
			return fmt.Errorf("session %s is %s", name, record.Status)
		}
		if time.Now().After(deadline) {
			waiting.Stop("")
			return fmt.Errorf("session %s did not deploy within %s (status %s)", name, cloudSessionDeployTimeout, record.Status)
		}
		// Status lines only when not animating; they would collide with the rewritten spinner line.
		if !waiting.animated && lastStatus != string(record.Status) {
			ctx.Logf("Session %s is %s\n", name, record.Status)
			lastStatus = string(record.Status)
		}
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

// cloudSessionResolveImage maps --repo to its imported image via the
// session-image map in the CLI config dir, written by the local build/import
// step. The backend's image service replaces that map in production; --image
// bypasses it.
func cloudSessionResolveImage(flags *cmd.CloudSessionCreateFlags) (string, error) {
	if flags.Image != "" && flags.Repo != "" {
		return "", cmd.NewErrUsagef("pass either --repo or --image, not both")
	}
	if flags.Image != "" {
		return flags.Image, nil
	}
	if flags.Repo == "" {
		return "", cmd.NewErrUsagef("a repository (--repo owner/name) or an image (--image) is required")
	}
	configDir, err := auth.DefaultConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolving the image for repo %s: %w", flags.Repo, err)
	}
	imageMapPath := filepath.Join(configDir, "cloud-session-images.json")
	imageMapData, err := os.ReadFile(imageMapPath)
	if err != nil {
		return "", fmt.Errorf("resolving the image for repo %s: reading %s: %w", flags.Repo, imageMapPath, err)
	}
	repoImages := map[string]string{}
	if err := json.Unmarshal(imageMapData, &repoImages); err != nil {
		return "", fmt.Errorf("resolving the image for repo %s: parsing %s: %w", flags.Repo, imageMapPath, err)
	}
	image, found := repoImages[flags.Repo]
	if !found {
		return "", fmt.Errorf("no session image registered for repo %s in %s; build and import one first", flags.Repo, imageMapPath)
	}
	return image, nil
}

// cloudSessionInstructions are the sandbox mechanics every session's harness
// must know; create merges them into the user's own instructions.
const cloudSessionInstructions = `# Baseten cloud coding session

You run inside a Baseten sandbox.

- Your execution API is at http://localhost:8080, no auth from inside. POST /process with {"command": "...", "keepAlive": true, "timeout": 0} runs a process that also disables idle suspend while it runs; stop that process to let the sandbox sleep.
- Idle suspend freezes the sandbox; files and processes resume on the next request. Never busy-wait to stay awake: end the work instead, and state what must re-run on wake.
- The user's interactive harness lives in tmux session "code". On reconnect, attach to it; only when it is gone, start a fresh one (claude --continue resumes the last conversation).
`

// cloudSessionPortConfig copies the local harness configuration into the
// session: skills verbatim, and the user's ~/CLAUDE.md and ~/AGENTS.md merged
// after the sandbox instructions. settings.json stays out: the in-sandbox
// 'grid setup' owns it, and the local one points at the local machine's grid.
func cloudSessionPortConfig(ctx *CommandContext, instance *sandbox.Sandbox) error {
	instructions := cloudSessionInstructions
	for _, instructionsPath := range []string{"CLAUDE.md", "AGENTS.md"} {
		userInstructions, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), instructionsPath))
		if err == nil {
			instructions += "\n" + string(userInstructions)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("reading the local %s: %w", instructionsPath, err)
		}
	}
	if err := instance.FS().Write(ctx, sandbox.FileSystemWriteOptions{
		Path:    "/root/.claude/CLAUDE.md",
		Content: instructions,
	}); err != nil {
		return fmt.Errorf("writing the session instructions: %w", err)
	}
	skillsFiles := map[string]string{}
	skillsRoot := filepath.Join(os.Getenv("HOME"), ".claude", "skills")
	walkErr := filepath.WalkDir(skillsRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		// Text config only: skills may carry images or archives the text
		// write-tree API cannot carry.
		content, readErr := os.ReadFile(path)
		if readErr != nil || len(content) > 256*1024 || !utf8.Valid(content) {
			return nil
		}
		relativePath, relErr := filepath.Rel(skillsRoot, path)
		if relErr != nil {
			return relErr
		}
		skillsFiles[relativePath] = string(content)
		return nil
	})
	if walkErr != nil && !os.IsNotExist(walkErr) {
		return fmt.Errorf("reading the local skills: %w", walkErr)
	}
	if len(skillsFiles) > 0 {
		if err := instance.FS().WriteTree(ctx, sandbox.FileSystemWriteTreeOptions{
			Path:  "/root/.claude/skills",
			Files: skillsFiles,
		}); err != nil {
			return fmt.Errorf("writing the session skills: %w", err)
		}
	}
	ctx.Logf("Ported local harness config: instructions plus %d skill files.\n", len(skillsFiles))
	return nil
}

// cloudSessionConfiguredRunlayerServers picks the session's Runlayer servers:
// the flags when given, else the launcher's configured defaults from the CLI
// config dir. The web UI's connector configuration replaces that file in
// production.
func cloudSessionConfiguredRunlayerServers(flagServers []string) ([]string, error) {
	if len(flagServers) > 0 {
		return flagServers, nil
	}
	configDir, err := auth.DefaultConfigDir()
	if err != nil {
		return nil, err
	}
	defaultsData, err := os.ReadFile(filepath.Join(configDir, "cloud-session-runlayer.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading the configured Runlayer servers: %w", err)
	}
	var configured struct {
		Servers   []string `json:"servers"`
		UserEmail string   `json:"user_email"`
	}
	if err := json.Unmarshal(defaultsData, &configured); err != nil {
		return nil, fmt.Errorf("parsing the configured Runlayer servers: %w", err)
	}
	cloudSessionRunlayerUserEmail = configured.UserEmail
	return configured.Servers, nil
}

// cloudSessionRunlayerUserEmail is the launcher user's Runlayer identity; set
// it to exchange the agent token for one carrying the user's own connectors
// and policies (OBO) instead of the shared agent identity (M2M).
var cloudSessionRunlayerUserEmail string

// execLookGitConfig reads one value from the launcher's git config.
func execLookGitConfig(key string) (string, error) {
	value, err := exec.Command("git", "config", key).Output()
	return strings.TrimSpace(string(value)), err
}

func commandGridCloudSessionCreate(ctx *CommandContext, flags *cmd.CloudSessionCreateFlags) error {
	imageRef, err := cloudSessionResolveImage(flags)
	if err != nil {
		return err
	}
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
	apiKeyValue, credentialIsSet := os.LookupEnv(cloudSessionCredentialEnv)
	if !credentialIsSet || apiKeyValue == "" {
		return cmd.NewErrUsagef("the session credential is not set: export %s (a dedicated sandbox token, never a personal key)", cloudSessionCredentialEnv)
	}
	name := flags.Name
	if name == "" {
		name = cloudSessionGeneratedName()
	}
	createIfNotExists := true
	createRequest := managementapi.CreateSandboxRequest{
		Name:              &name,
		Image:             &imageRef,
		Memory:            &flags.Memory,
		Envs:              envs,
		CreateIfNotExists: &createIfNotExists,
	}
	runlayerServers, err := cloudSessionConfiguredRunlayerServers(flags.RunlayerServer)
	if err != nil {
		return err
	}
	var runlayerM2MToken string
	if len(runlayerServers) > 0 {
		runlayerM2MToken, err = cloudSessionRunlayerM2MToken(flags.RunlayerClientIDEnv, flags.RunlayerSecretEnv, runlayerServers)
		if err != nil {
			return err
		}
	}
	harnessHost, err := cloudSessionURLHost(flags.HarnessURL)
	if err != nil {
		return err
	}
	githubPat, githubPatIsSet := os.LookupEnv(cloudSessionGitHubPatEnv)
	if githubPatIsSet && githubPat == "" {
		githubPat, githubPatIsSet = "", false
	}
	createRequest.Network = cloudSessionProxyNetwork(harnessHost, apiKeyValue, runlayerM2MToken, githubPat)
	creating := startSpinner(ctx, fmt.Sprintf("Creating session %s from image %s", name, imageRef))
	record, err := client.API().CreateSandbox(ctx, managementapi.CreateSandboxParams{TeamId: teamID}, createRequest)
	if err != nil {
		creating.Stop("")
		return fmt.Errorf("creating session %s: %w", name, err)
	}
	name = deref(record.Name)
	creating.Stop(fmt.Sprintf("Session %s created", name))
	if err := cloudSessionWaitDeployed(ctx, client, teamID, name); err != nil {
		return err
	}
	instance, err := client.Get(ctx, sandbox.GetOptions{Name: name})
	if err != nil {
		return err
	}
	// The setup installs the placeholder credential, which the proxy replaces on the wire.
	setupCommand := "baseten grid setup --harness " + flags.Harness + " --use-session-credential --yes"
	if flags.Team != "" {
		setupCommand += " --team " + sandboxShellQuote(flags.Team)
	}
	ctx.Logf("Setting up %s inside session %s…\n", flags.Harness, name)
	if err := cloudSessionExecStream(ctx, instance, setupCommand, "", cloudSessionInSandboxTimeout); err != nil {
		return err
	}
	// First-run screens (theme, onboarding, the workdir trust dialog) would
	// greet every fresh session; dismiss them here until the image bake
	// carries it.
	prepareOnboarding := "python3 -c 'import json,os; p=os.path.expanduser(\"~/.claude.json\");" +
		" c=json.load(open(p)) if os.path.exists(p) else {};" +
		" c[\"theme\"]=\"dark\"; c[\"hasCompletedOnboarding\"]=True;" +
		" c.setdefault(\"projects\",{}).setdefault(\"/workspace\",{})[\"hasTrustDialogAccepted\"]=True;" +
		" json.dump(c,open(p,\"w\"))'"
	if err := cloudSessionExecStream(ctx, instance, prepareOnboarding, "", cloudSessionInSandboxTimeout); err != nil {
		return err
	}
	if err := cloudSessionPrepareTurnLiveness(ctx, instance); err != nil {
		return err
	}
	if githubPatIsSet {
		// The proxy authenticates git, so the sandbox needs only an identity
		// to write commits under, carried from the launcher's own git config.
		gitUserName, nameErr := execLookGitConfig("user.name")
		gitUserEmail, emailErr := execLookGitConfig("user.email")
		if nameErr == nil && emailErr == nil {
			writeGitIdentity := "git config --global user.name " + sandboxShellQuote(gitUserName) +
				" && git config --global user.email " + sandboxShellQuote(gitUserEmail)
			if err := cloudSessionExecStream(ctx, instance, writeGitIdentity, "", cloudSessionInSandboxTimeout); err != nil {
				return err
			}
		}
	}
	if flags.PortConfig {
		ctx.Logf("Copying local harness config into the session…\n")
		if err := cloudSessionPortConfig(ctx, instance); err != nil {
			return err
		}
	}
	if err := cloudSessionPreparePreviewGuidance(ctx, instance); err != nil {
		return err
	}
	if len(runlayerServers) > 0 {
		ctx.Logf("Exposing %d Runlayer MCP servers in session %s…\n", len(runlayerServers), name)
		if err := cloudSessionRunlayerMCPConfig(ctx, instance, runlayerServers); err != nil {
			return err
		}
	}
	ctx.Logf("Starting the harness in tmux…\n")
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
	// JSON output must stay machine-readable, so only a terminal auto-attaches.
	if !ctx.JSON && stdinStdoutAreTerminals(ctx) {
		ctx.Logf("Reconnect later with: baseten grid cloud-session connect --name %s\n", name)
		return cloudSessionAttach(ctx, cmd.SandboxRefFlags{Name: name, Team: flags.Team}, harnessStart)
	}
	ctx.Logf("Attach with: baseten grid cloud-session connect --name %s\n", name)
	return nil
}

func commandGridCloudSessionConnect(ctx *CommandContext, flags *cmd.CloudSessionConnectFlags) error {
	harnessResume, err := cloudSessionHarnessResumeCommand(flags.Harness)
	if err != nil {
		return err
	}
	if err := cloudSessionAttach(ctx, flags.SandboxRefFlags, harnessResume); err != nil {
		return err
	}
	ctx.Logf("\nDetached from session %s.\n", flags.Name)
	return nil
}

// cloudSessionAttach opens the session's terminal and lands it in the
// session's tmux session, keeping the sandbox awake while attached. The
// window command only runs when the tmux session does not exist yet.
// The sentinel kill on detach is best effort: a hibernated sandbox wakes for the
// kill and sleeps again right after.
func cloudSessionAttach(ctx *CommandContext, ref cmd.SandboxRefFlags, tmuxWindowCommand string) error {
	// tmux -u: the pty's TERM=linux plus empty locale would make tmux
	// ASCII-substitute glyphs. new -A: attach when the session exists, create
	// it with the harness when not.
	attachCommand := "TERM=xterm-256color tmux -u new-session -A -s " + cloudSessionTmuxSession + " " + tmuxWindowCommand + "\n"
	return sandboxConnectTerminal(ctx, ref, attachCommand)
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

// cloudSessionPreviewGuidance is the preview flow for the harness: it cannot
// mint previews (the session credential is not allowed to), so the operator does.
const cloudSessionPreviewGuidance = `- To serve something for the user (vite, docs, a notebook): bind 0.0.0.0 and hand the user one command to run outside, with the port filled in: "baseten sandbox preview create --name=$BASETEN_SANDBOX_NAME --port=<port> --with-token" (add --ttl for a shorter-lived link). It prints a private HTTPS link with an embedded access token, ready to open or curl. Your own listening ports: GET /network/process/{pid}/ports on the local API.
- Do not hand out the sandbox URL or a :port link: the URL is auth-walled (401) and direct ports return 502. Only a preview link is reachable, and you cannot create one yourself, so the user must run the command above.
- A preview forwards the external hostname in the Host header (X-Forwarded-Host carries it too). Vite already works: the session sets __VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS=.preview.b10.co. Other host-checking servers (Django ALLOWED_HOSTS and CSRF_TRUSTED_ORIGINS, webpack) must allow the host or trust X-Forwarded-Host, or sit behind a small proxy that rewrites Host and Origin to localhost. Serve a frontend and its API behind one port so the browser stays same-origin.`

// cloudSessionPreparePreviewGuidance replaces the preview bullets in the
// session's CLAUDE.md, dropping older ones baked into images. Idempotent.
func cloudSessionPreparePreviewGuidance(ctx *CommandContext, instance *sandbox.Sandbox) error {
	rewrite := "python3 -c 'import os,sys; p=os.path.expanduser(\"~/.claude/CLAUDE.md\");" +
		" lines=open(p).read().splitlines() if os.path.exists(p) else [];" +
		" lines=[l for l in lines if (\"baseten sandbox preview create\" not in l and \"sandbox preview token create\" not in l and \"cloud-session preview --name=\" not in l and \"auth-walled\" not in l and \"X-Forwarded-Host\" not in l)];" +
		" open(p,\"w\").write(\"\\n\".join(lines+[\"\"]+[sys.argv[1]])+\"\\n\")' " +
		sandboxShellQuote(cloudSessionPreviewGuidance)
	return cloudSessionExecStream(ctx, instance, rewrite, "", cloudSessionInSandboxTimeout)
}
