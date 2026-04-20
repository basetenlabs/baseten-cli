package cmd

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/charmbracelet/huh"
)

func init() {
	Register("auth login", commandAuthLogin)
	Register("auth logout", commandAuthLogout)
	Register("auth switch", commandAuthSwitch)
	Register("auth status", commandAuthStatus)
}

type whoamiResponse struct {
	UserID        string `json:"user_id"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	WorkspaceName string `json:"workspace_name"`
}

func commandAuthLogin(ctx *CommandContext, flags *cmd.AuthLoginFlags) error {
	host := ResolveHost()
	store, err := NewAuthStore(flags.InsecureStorage)
	if err != nil {
		return err
	}

	if flags.Web && flags.WithAPIKey {
		return &ErrUsage{Err: fmt.Errorf("--web and --with-api-key are mutually exclusive")}
	}

	method := ""
	if flags.Web {
		method = "web"
	} else if flags.WithAPIKey {
		method = "api_key"
	} else if ctx.IsInteractive() {
		var choice string
		err := huh.NewSelect[string]().
			Title("How would you like to authenticate?").
			Options(
				huh.NewOption("Login with Baseten credentials (browser)", "web"),
				huh.NewOption("Paste an API key", "api_key"),
			).
			Value(&choice).
			Run()
		if err != nil {
			return err
		}
		method = choice
	} else {
		return &ErrUsage{Err: fmt.Errorf("must specify --web or --with-api-key when not interactive")}
	}

	switch method {
	case "web":
		return loginWeb(ctx, store, host)
	case "api_key":
		return loginAPIKey(ctx, store, host, flags.Label)
	default:
		return fmt.Errorf("unexpected auth method %q", method)
	}
}

func commandAuthLogout(ctx *CommandContext, flags *cmd.AuthLogoutFlags) error {
	host := ResolveHost()
	store, err := NewAuthStore(false)
	if err != nil {
		return err
	}

	label, entry, ok := store.ActiveUser(host)
	if !ok {
		return fmt.Errorf("no active user for %s", host)
	}

	if entry.AuthType == auth.AuthTypeOAuth {
		if err := revokeOAuthSession(ctx, store, host, label); err != nil {
			ctx.Logf("warning: server-side session revocation failed: %v\n", err)
		}
	}

	if err := store.RemoveUser(host, label); err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(map[string]string{"user": label})
	} else {
		ctx.Outputf("Logged out %s\n", label)
	}
	return nil
}

func revokeOAuthSession(ctx *CommandContext, store *auth.Store, host, label string) error {
	transport := &auth.Transport{
		Store:       store,
		Host:        host,
		OAuthConfig: OAuthConfig(host),
		EnvAPIKey:   os.Getenv("BASETEN_API_KEY"),
		Base:        ctx.httpClient().Transport,
	}
	req, err := http.NewRequestWithContext(
		ctx.Context, http.MethodPost, host+"/v1/users/auth/logout", nil,
	)
	if err != nil {
		return err
	}
	resp, err := transport.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func commandAuthSwitch(ctx *CommandContext, flags *cmd.AuthSwitchFlags) error {
	host := ResolveHost()
	store, err := NewAuthStore(false)
	if err != nil {
		return err
	}

	label := flags.User
	if label == "" {
		if !ctx.IsInteractive() {
			return &ErrUsage{Err: fmt.Errorf("must specify --user when not interactive")}
		}

		af, err := store.Load()
		if err != nil {
			return err
		}
		he, ok := af.Hosts[host]
		if !ok || len(he.Users) == 0 {
			return fmt.Errorf("no credentials stored for %s", host)
		}

		var options []huh.Option[string]
		for name := range he.Users {
			options = append(options, huh.NewOption(name, name))
		}

		err = huh.NewSelect[string]().
			Title("Switch to which account?").
			Options(options...).
			Value(&label).
			Run()
		if err != nil {
			return err
		}
	}

	if err := store.SwitchUser(host, label); err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(map[string]string{"user": label})
	} else {
		ctx.Outputf("Switched to %s\n", label)
	}
	return nil
}

func commandAuthStatus(ctx *CommandContext, flags *cmd.AuthStatusFlags) error {
	host := ResolveHost()
	store, err := NewAuthStore(false)
	if err != nil {
		return err
	}

	label, entry, ok := store.ActiveUser(host)
	if !ok {
		return fmt.Errorf("not logged in to %s; run `baseten auth login` or set BASETEN_API_KEY", host)
	}

	if ctx.JSON {
		ctx.OutputJSON(map[string]string{
			"host":      host,
			"user":      label,
			"auth_type": string(entry.AuthType),
		})
	} else {
		ctx.Outputf("%s\n  Logged in as %s\n  Auth type: %s\n", host, label, entry.AuthType)
	}
	return nil
}

func loginWeb(ctx *CommandContext, store *auth.Store, host string) error {
	cfg := OAuthConfig(host)
	devResp, err := cfg.DeviceAuth(ctx.Context)
	if err != nil {
		return fmt.Errorf("starting device authorization: %w", err)
	}

	ctx.Logf("Enter code %s at %s\n", devResp.UserCode, devResp.VerificationURI)

	token, err := cfg.DeviceAccessToken(ctx.Context, devResp)
	if err != nil {
		return fmt.Errorf("waiting for authorization: %w", err)
	}

	cl, err := ctx.NewManagementClientWithAuth("Bearer " + token.AccessToken)
	if err != nil {
		return err
	}
	user, err := cl.API().GetUsers(ctx.Context, "me")
	if err != nil {
		return fmt.Errorf("validating credentials: %w", err)
	}

	warnWriter := func(msg string) { ctx.Log(msg) }
	cred := auth.OAuthCredential{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
	}
	email := deref(user.Email)
	if err := store.SetOAuthUser(host, email, cred, warnWriter); err != nil {
		return err
	}

	result := whoamiResponse{
		UserID:        user.UserId,
		Email:         email,
		Name:          deref(user.Name),
		WorkspaceName: deref(user.WorkspaceName),
	}
	if ctx.JSON {
		ctx.OutputJSON(result)
	} else {
		ctx.Outputf("Logged in as %s (%s)\n", result.Email, result.WorkspaceName)
	}
	return nil
}

func loginAPIKey(ctx *CommandContext, store *auth.Store, host, label string) error {
	var apiKey string

	if ctx.IsInteractive() {
		err := huh.NewInput().
			Title("Paste your API key").
			EchoMode(huh.EchoModePassword).
			Value(&apiKey).
			Run()
		if err != nil {
			return err
		}
	} else {
		reader := bufio.NewReader(ctx.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return fmt.Errorf("reading API key from stdin: %w", err)
		}
		apiKey = strings.TrimSpace(line)
	}

	if apiKey == "" {
		return &ErrUsage{Err: fmt.Errorf("API key cannot be empty")}
	}

	cl, err := ctx.NewManagementClientWithAuth("Api-Key " + apiKey)
	if err != nil {
		return err
	}
	user, err := cl.API().GetUsers(ctx.Context, "me")
	if err != nil {
		return fmt.Errorf("validating API key: %w", err)
	}

	if label == "" {
		if ctx.IsInteractive() {
			err := huh.NewInput().
				Title("Label for this credential").
				Description("e.g. personal, deploy-bot").
				Value(&label).
				Run()
			if err != nil {
				return err
			}
		} else {
			return &ErrUsage{Err: fmt.Errorf("--label is required when not interactive")}
		}
	}

	if label == "" {
		return &ErrUsage{Err: fmt.Errorf("label cannot be empty")}
	}

	warnWriter := func(msg string) { ctx.Log(msg) }
	if err := store.SetAPIKeyUser(host, label, apiKey, warnWriter); err != nil {
		return err
	}

	result := whoamiResponse{
		UserID:        user.UserId,
		Email:         deref(user.Email),
		Name:          deref(user.Name),
		WorkspaceName: deref(user.WorkspaceName),
	}
	if ctx.JSON {
		ctx.OutputJSON(result)
	} else {
		ctx.Outputf("Logged in as %s (%s)\n", result.Email, result.WorkspaceName)
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
