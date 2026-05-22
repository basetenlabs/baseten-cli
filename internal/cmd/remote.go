package cmd

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/basetenlabs/baseten-go/client"
)

// Remote captures the resolved Baseten remote (app URL + override knobs) used
// to derive every URL the CLI talks to. Construct once via NewRemote at the
// start of a command; callers use the methods rather than reading env directly.
type Remote struct {
	remoteURL           string
	scheme              string
	baseHost            string
	managementAPIHost   string
	inferenceBaseURL    string
	inferenceHostSuffix string
}

// NewRemote resolves the remote URL (flag arg, then BASETEN_REMOTE_URL, then
// https://app.baseten.co), looks up any known per-remote URL overrides, and
// applies the optional override env vars on top.
func NewRemote(remoteURL string) (*Remote, error) {
	r := &Remote{remoteURL: remoteURL}
	if r.remoteURL == "" {
		r.remoteURL = os.Getenv("BASETEN_REMOTE_URL")
	}
	if r.remoteURL == "" {
		r.remoteURL = "https://app.baseten.co"
	}
	u, err := url.Parse(r.remoteURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid remote URL: %q", r.remoteURL)
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("remote URL must not include a path, query, or fragment: %q", r.remoteURL)
	}
	r.remoteURL = u.Scheme + "://" + u.Host
	r.scheme = u.Scheme
	r.baseHost = strings.TrimPrefix(u.Host, "app.")

	switch r.remoteURL {
	case "https://app.baseten.co":
		r.managementAPIHost = "api.baseten.co"
	case "https://app.staging.baseten.co":
		r.managementAPIHost = "api.staging.baseten.co"
	case "https://app.dev.baseten.co":
		r.managementAPIHost = "api.mc-dev.baseten.co"
	case "http://localhost:8000":
		r.managementAPIHost = "api.localhost:8000"
		r.inferenceBaseURL = "http://localhost:9090"
		r.inferenceHostSuffix = "api.dev.baseten.co"
	default:
		r.managementAPIHost = "api." + r.baseHost
	}

	if v := os.Getenv("BASETEN_MANAGEMENT_API_URL_OVERRIDE"); v != "" {
		mu, err := url.Parse(v)
		if err != nil || mu.Scheme == "" || mu.Host == "" {
			return nil, fmt.Errorf("invalid BASETEN_MANAGEMENT_API_URL_OVERRIDE: %q", v)
		}
		r.scheme = mu.Scheme
		r.managementAPIHost = mu.Host
	}
	if v := os.Getenv("BASETEN_INFERENCE_BASE_URL_OVERRIDE"); v != "" {
		r.inferenceBaseURL = v
	}
	return r, nil
}

// RemoteURL returns the raw remote URL (suitable as the auth store key).
func (r *Remote) RemoteURL() string { return r.remoteURL }

// ManagementURL returns the base URL for the REST management API.
func (r *Remote) ManagementURL() string {
	return r.scheme + "://" + r.managementAPIHost
}

// InferenceBaseURL returns the base URL (no path) for inference calls. When
// an inference base URL override is set, that is returned verbatim; otherwise
// the URL is built from the remote's scheme and management API host.
func (r *Remote) InferenceBaseURL(modelID, chainID, environment string) (string, error) {
	if r.inferenceBaseURL != "" {
		return r.inferenceBaseURL, nil
	}
	host, err := client.InferenceClientHost(modelID, chainID, environment, r.managementAPIHost)
	if err != nil {
		return "", err
	}
	return r.scheme + "://" + host, nil
}

// InferenceHostHeader returns the Host header value to use for inference
// requests when the remote requires one. The bool reports whether the caller
// should force the header; when false, the default URL-derived host is fine.
func (r *Remote) InferenceHostHeader(modelID, chainID, environment string) (string, bool, error) {
	if r.inferenceHostSuffix == "" {
		return "", false, nil
	}
	host, err := client.InferenceClientHost(modelID, chainID, environment, r.inferenceHostSuffix)
	if err != nil {
		return "", false, err
	}
	return host, true, nil
}

// PredictURL returns the user-facing predict URL printed in push output.
func (r *Remote) PredictURL(modelID, deploymentID string, isDraft bool) string {
	base := r.scheme + "://model-" + modelID + "." + r.managementAPIHost
	if isDraft {
		return base + "/development/predict"
	}
	return base + "/deployment/" + deploymentID + "/predict"
}

// LogsURL returns the user-facing logs URL printed in push output.
func (r *Remote) LogsURL(modelID, deploymentID string) string {
	return r.scheme + "://app." + r.baseHost + "/models/" + modelID + "/logs/" + deploymentID
}
