package cmd

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
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
	baseURL := ctx.Remote.RemoteURL()
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
		return loginWeb(ctx, store, baseURL)
	case "api_key":
		return loginAPIKey(ctx, store, baseURL, flags.Label)
	default:
		return fmt.Errorf("unexpected auth method %q", method)
	}
}

func commandAuthLogout(ctx *CommandContext, flags *cmd.AuthLogoutFlags) error {
	baseURL := ctx.Remote.RemoteURL()
	store, err := NewAuthStore(false)
	if err != nil {
		return err
	}

	label, entry, ok := store.ActiveUser(baseURL)
	if !ok {
		return fmt.Errorf("no active user for %s", baseURL)
	}

	if entry.AuthType == auth.AuthTypeOAuth {
		if err := revokeOAuthSession(ctx, store, baseURL, label); err != nil {
			ctx.Logf("warning: server-side session revocation failed: %v\n", err)
		}
	}

	if err := store.RemoveUser(baseURL, label); err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(map[string]string{"user": label})
	} else {
		ctx.Outputf("Logged out %s\n", label)
	}
	return nil
}

func revokeOAuthSession(ctx *CommandContext, store *auth.Store, baseURL, label string) error {
	mgmtURL := ctx.Remote.ManagementURL()
	transport := &auth.Transport{
		Store:       store,
		Host:        baseURL,
		OAuthConfig: OAuthConfig(mgmtURL),
		EnvAPIKey:   os.Getenv("BASETEN_API_KEY"),
		Base:        ctx.httpClient().Transport,
	}
	req, err := http.NewRequestWithContext(
		ctx.Context, http.MethodPost, mgmtURL+"/v1/users/auth/logout", nil,
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
	baseURL := ctx.Remote.RemoteURL()
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
		he, ok := af.Hosts[baseURL]
		if !ok || len(he.Users) == 0 {
			return fmt.Errorf("no credentials stored for %s", baseURL)
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

	if err := store.SwitchUser(baseURL, label); err != nil {
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
	baseURL := ctx.Remote.RemoteURL()
	store, err := NewAuthStore(false)
	if err != nil {
		return err
	}

	label, entry, ok := store.ActiveUser(baseURL)
	if !ok {
		return fmt.Errorf("not logged in to %s; run `baseten auth login` or set BASETEN_API_KEY", baseURL)
	}

	if ctx.JSON {
		ctx.OutputJSON(map[string]string{
			"host":      baseURL,
			"user":      label,
			"auth_type": string(entry.AuthType),
		})
	} else {
		ctx.Outputf("%s\n  Logged in as %s\n  Auth type: %s\n", baseURL, label, entry.AuthType)
	}
	return nil
}

func loginWeb(ctx *CommandContext, store *auth.Store, baseURL string) error {
	cfg := OAuthConfig(ctx.Remote.ManagementURL())
	oauthCtx := auth.OAuthContext(ctx.Context, ctx.httpClient().Transport)
	devResp, err := cfg.DeviceAuth(oauthCtx)
	if err != nil {
		return fmt.Errorf("starting device authorization: %w", err)
	}

	ctx.Logf("Enter code %s at %s\n", devResp.UserCode, devResp.VerificationURI)

	token, err := cfg.DeviceAccessToken(oauthCtx, devResp)
	if err != nil {
		return fmt.Errorf("waiting for authorization: %w", err)
	}

	if claims, err := decodeJWTClaims(token.AccessToken); err == nil {
		ctx.VerboseLogf("access token claims: %s\n", claims)
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
		Expiry:       token.Expiry,
	}
	email := deref(user.Email)
	if err := store.SetOAuthUser(baseURL, email, cred, warnWriter); err != nil {
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

func loginAPIKey(ctx *CommandContext, store *auth.Store, baseURL, label string) error {
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
	if err := store.SetAPIKeyUser(baseURL, label, apiKey, warnWriter); err != nil {
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

func decodeJWTClaims(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("not a JWT (got %d segments)", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("json unmarshal: %w", err)
	}
	pretty, err := json.MarshalIndent(claims, "", "  ")
	if err != nil {
		return "", err
	}
	return string(pretty), nil
}
