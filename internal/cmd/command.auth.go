package cmd

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/charmbracelet/huh"
	"github.com/cli/browser"
)

func init() {
	Register("auth login", commandAuthLogin)
	Register("auth logout", commandAuthLogout)
	Register("auth switch", commandAuthSwitch)
	Register("auth status", commandAuthStatus)
}

func commandAuthLogin(ctx *CommandContext, flags *cmd.AuthLoginFlags) error {
	if flags.Web && flags.WithAPIKey {
		return cmd.NewErrUsagef("--web and --with-api-key are mutually exclusive")
	}

	remote, err := NewRemote(flags.RemoteURL)
	if err != nil {
		return err
	}
	store, err := NewAuthStore(flags.InsecureStorage)
	if err != nil {
		return err
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
		return cmd.NewErrUsagef("must specify --web or --with-api-key when not interactive")
	}

	switch method {
	case "web":
		return loginWeb(ctx, store, remote, flags)
	case "api_key":
		return loginAPIKey(ctx, store, remote, flags)
	default:
		return fmt.Errorf("unexpected auth method %q", method)
	}
}

func commandAuthLogout(ctx *CommandContext, flags *cmd.AuthLogoutFlags) error {
	store, err := NewAuthStore(false)
	if err != nil {
		return err
	}

	profileName := flags.Profile
	if profileName == "" {
		current, _, ok := store.CurrentProfile()
		if !ok {
			return fmt.Errorf("no current profile; pass --profile to choose one to remove")
		}
		profileName = current
	}

	profile, ok := store.GetProfile(profileName)
	if !ok {
		return fmt.Errorf("profile %q not found", profileName)
	}

	if profile.AuthType == auth.AuthTypeOAuth {
		if err := revokeOAuthSession(ctx, profileName); err != nil {
			ctx.Logf("warning: server-side session revocation failed: %v\n", err)
		}
	}

	if err := store.RemoveProfile(profileName); err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.AuthLogoutResult{Profile: profileName})
	} else {
		ctx.Outputf("Logged out %s\n", profileName)
	}
	return nil
}

func revokeOAuthSession(ctx *CommandContext, profileName string) error {
	session, err := auth.ResolveSession(profileName)
	if err != nil {
		return err
	}
	remote, err := NewRemote(session.RemoteURL())
	if err != nil {
		return err
	}
	mgmtURL := remote.ManagementURL()
	transport := &auth.Transport{
		Session:     session,
		OAuthConfig: OAuthConfig(mgmtURL),
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
	store, err := NewAuthStore(false)
	if err != nil {
		return err
	}

	profileName := flags.Profile
	if profileName == "" {
		if !ctx.IsInteractive() {
			return cmd.NewErrUsagef("must specify --profile when not interactive")
		}

		names, err := store.ProfileNames()
		if err != nil {
			return err
		}
		if len(names) == 0 {
			return fmt.Errorf("no profiles stored; run `baseten auth login`")
		}

		options := make([]huh.Option[string], 0, len(names))
		for _, name := range names {
			options = append(options, huh.NewOption(name, name))
		}
		err = huh.NewSelect[string]().
			Title("Switch to which profile?").
			Options(options...).
			Value(&profileName).
			Run()
		if err != nil {
			return err
		}
	}

	if err := store.SwitchProfile(profileName); err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.AuthSwitchResult{Profile: profileName})
	} else {
		ctx.Outputf("Switched to %s\n", profileName)
	}
	return nil
}

func commandAuthStatus(ctx *CommandContext, flags *cmd.AuthStatusFlags) error {
	store, err := NewAuthStore(false)
	if err != nil {
		return err
	}

	session, err := auth.ResolveSession(flags.Profile)
	if err != nil {
		return err
	}

	if session.IsEphemeral() {
		remoteURL := session.RemoteURL()
		if remoteURL == "" {
			remoteURL = "https://app.baseten.co"
		}
		if ctx.JSON {
			ctx.OutputJSON(cmd.AuthStatusResult{RemoteURL: remoteURL, AuthType: string(auth.AuthTypeAPIKey)})
		} else {
			ctx.Outputf("Using API key from BASETEN_API_KEY\n  Remote: %s\n", remoteURL)
		}
		return nil
	}

	profileName := session.ProfileName()
	if profileName == "" {
		return fmt.Errorf("not logged in; run `baseten auth login` or set BASETEN_API_KEY")
	}
	profile, ok := store.GetProfile(profileName)
	if !ok {
		return fmt.Errorf("profile %q not found", profileName)
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.AuthStatusResult{
			Profile:   profileName,
			RemoteURL: profile.RemoteURL,
			AuthType:  string(profile.AuthType),
		})
	} else {
		ctx.Outputf("%s\n  Remote: %s\n  Auth type: %s\n", profileName, profile.RemoteURL, profile.AuthType)
	}
	return nil
}

func loginWeb(ctx *CommandContext, store *auth.Store, remote *Remote, flags *cmd.AuthLoginFlags) error {
	mgmtURL := remote.ManagementURL()
	cfg := OAuthConfig(mgmtURL)
	oauthCtx := auth.OAuthContext(ctx.Context, ctx.httpClient().Transport)
	devResp, err := cfg.DeviceAuth(oauthCtx)
	if err != nil {
		return fmt.Errorf("starting device authorization: %w", err)
	}

	verificationURI := devResp.VerificationURIComplete
	if verificationURI == "" {
		verificationURI = devResp.VerificationURI
	}
	browser.Stdout = io.Discard
	browser.Stderr = io.Discard
	_ = browser.OpenURL(verificationURI)
	ctx.Logf("Browser opened to authenticate...\n\nIf it didn't open, visit:\n  %s\n\n", verificationURI)
	ctx.Logf("Verification code: %s\n\n", devResp.UserCode)
	ctx.Logf("Waiting...\n")

	token, err := cfg.DeviceAccessToken(oauthCtx, devResp)
	if err != nil {
		return fmt.Errorf("waiting for authorization: %w", err)
	}

	if claims, err := decodeJWTClaims(token.AccessToken); err == nil {
		ctx.VerboseLogf("access token claims: %s\n", claims)
	}

	cl, err := ctx.NewManagementClientWithAuth(mgmtURL, "Bearer "+token.AccessToken)
	if err != nil {
		return err
	}
	user, err := cl.API().GetUsers(ctx.Context, "me")
	if err != nil {
		return fmt.Errorf("validating credentials: %w", err)
	}

	email := deref(user.Email)
	profileName := flags.Profile
	if profileName == "" {
		if email == "" {
			return fmt.Errorf("could not determine a profile name from the account; pass --profile")
		}
		profileName = defaultProfileName(email, remote)
	}

	warnWriter := func(msg string) { ctx.Log(msg) }
	cred := auth.OAuthCredential{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		Expiry:       token.Expiry,
	}
	if err := store.SetOAuthProfile(profileName, remote.RemoteURL(), cred, !flags.NoSwitch, warnWriter); err != nil {
		return err
	}

	return writeLoginResult(ctx, profileName, user)
}

func loginAPIKey(ctx *CommandContext, store *auth.Store, remote *Remote, flags *cmd.AuthLoginFlags) error {
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
		return fmt.Errorf("API key cannot be empty")
	}

	cl, err := ctx.NewManagementClientWithAuth(remote.ManagementURL(), "Api-Key "+apiKey)
	if err != nil {
		return err
	}
	user, err := cl.API().GetUsers(ctx.Context, "me")
	if err != nil {
		return fmt.Errorf("validating API key: %w", err)
	}

	profileName := flags.Profile
	if profileName == "" {
		if !ctx.IsInteractive() {
			return cmd.NewErrUsagef("--profile is required when logging in with an API key non-interactively")
		}
		err := huh.NewInput().
			Title("Profile name for this credential").
			Description("e.g. personal, deploy-bot").
			Value(&profileName).
			Run()
		if err != nil {
			return err
		}
	}
	if profileName == "" {
		return cmd.NewErrUsagef("profile name cannot be empty")
	}

	warnWriter := func(msg string) { ctx.Log(msg) }
	if err := store.SetAPIKeyProfile(profileName, remote.RemoteURL(), apiKey, !flags.NoSwitch, warnWriter); err != nil {
		return err
	}

	return writeLoginResult(ctx, profileName, user)
}

func writeLoginResult(ctx *CommandContext, profileName string, user *managementapi.UserInfo) error {
	result := cmd.AuthLoginResult{
		Profile:       profileName,
		UserID:        user.UserId,
		Email:         deref(user.Email),
		Name:          deref(user.Name),
		WorkspaceName: deref(user.WorkspaceName),
	}
	if ctx.JSON {
		ctx.OutputJSON(result)
	} else {
		ctx.Outputf("Logged in as %s (%s) as profile %s\n", result.Email, result.WorkspaceName, profileName)
	}
	return nil
}

// defaultProfileName derives the profile name for an OAuth login: the email,
// with the remote host appended for non-default remotes to disambiguate.
func defaultProfileName(email string, remote *Remote) string {
	if remote.IsDefault() {
		return email
	}
	return email + ":" + remote.HostLabel()
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
