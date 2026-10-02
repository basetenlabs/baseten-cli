package sandboxclient

import (
	"errors"
	"fmt"
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/basetenlabs/baseten-go/client/sandboxapi"
)

// SandboxStatus is the deployment status of a sandbox. Only
// SandboxStatusDeployed sandboxes run commands. Other values may be added,
// so do not treat the constants as exhaustive.
type SandboxStatus = string

// Values for SandboxStatus.
const (
	SandboxStatusDeploying    SandboxStatus = "DEPLOYING"
	SandboxStatusDeployed     SandboxStatus = "DEPLOYED"
	SandboxStatusFailed       SandboxStatus = "FAILED"
	SandboxStatusDeactivating SandboxStatus = "DEACTIVATING"
	SandboxStatusDeactivated  SandboxStatus = "DEACTIVATED"
	SandboxStatusDeleting     SandboxStatus = "DELETING"
	SandboxStatusTerminated   SandboxStatus = "TERMINATED"
	SandboxStatusArchiving    SandboxStatus = "ARCHIVING"
	SandboxStatusArchived     SandboxStatus = "ARCHIVED"
	SandboxStatusUnarchiving  SandboxStatus = "UNARCHIVING"
	SandboxStatusBuilding     SandboxStatus = "BUILDING"
	SandboxStatusUploading    SandboxStatus = "UPLOADING"
)

// SandboxState is whether a deployed sandbox is running or idle in standby.
// Other values may be added, so do not treat the constants as exhaustive.
type SandboxState = string

// Values for SandboxState.
const (
	SandboxStateRunning SandboxState = "RUNNING"
	SandboxStateStandby SandboxState = "STANDBY"
)

// SandboxEnvValue is the value of an environment variable in a sandbox.
type SandboxEnvValue struct {
	// Value of the variable.
	Value string

	// Secret marks the value as a secret.
	Secret bool
}

// SandboxInfo is a sandbox as last reported by the control plane.
type SandboxInfo struct {
	// Name is the unique name of the sandbox, assigned by the server when not
	// given at creation.
	Name string `json:"name"`

	// URL is the base URL of the sandbox's execution API, once it has one.
	URL string `json:"url"`

	// Status is the deployment status.
	Status SandboxStatus `json:"status"`

	// State is the execution state when the status is DEPLOYED.
	State SandboxState `json:"state"`

	// Image is the image reference, including its tag.
	Image string `json:"image"`

	// Memory is the allocation in megabytes, which also sets the CPU
	// allocation.
	Memory int `json:"memory"`

	// Region is where the sandbox runs.
	Region string `json:"region"`

	// Enabled is false when the sandbox is disabled and accepts no
	// connections.
	Enabled bool `json:"enabled"`

	// Envs are the environment variables injected into the sandbox.
	Envs map[string]SandboxEnvValue `json:"envs"`

	// Labels are the key-value pairs for organizing and filtering.
	Labels map[string]string `json:"labels"`

	// ExternalID is the caller-owned identifier for external lookups.
	ExternalID string `json:"external_id"`

	// CreatedAt is when the sandbox was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when the sandbox was last updated.
	UpdatedAt time.Time `json:"updated_at"`

	// CreatedBy is the user or service account that created the sandbox.
	CreatedBy string `json:"created_by"`

	// UpdatedBy is the user or service account that last updated the sandbox.
	UpdatedBy string `json:"updated_by"`

	// LastUsedAt is when the sandbox was last used.
	LastUsedAt time.Time `json:"last_used_at"`

	// ExpiresInSeconds is the time left before automatic deletion, when
	// expiration is configured. Zero means no expiration is configured.
	ExpiresInSeconds int `json:"expires_in_seconds"`
}

// ProcessStatus is the status of a process in a sandbox. Other values may be
// added, so do not treat the constants as exhaustive.
type ProcessStatus = string

// Values for ProcessStatus.
const (
	ProcessStatusRunning   ProcessStatus = "running"
	ProcessStatusCompleted ProcessStatus = "completed"
	ProcessStatusFailed    ProcessStatus = "failed"
	ProcessStatusKilled    ProcessStatus = "killed"
	ProcessStatusStopped   ProcessStatus = "stopped"
)

// ProcessInfo is a process in a sandbox.
type ProcessInfo struct {
	// PID identifies the process, usable wherever a process name is.
	PID string `json:"pid"`

	// Name is the caller-assigned name, or a generated one.
	Name string `json:"name"`

	// Command is the shell command the process runs.
	Command string `json:"command"`

	// Status is the current process status.
	Status ProcessStatus `json:"status"`

	// ExitCode is the process exit code.
	ExitCode int `json:"exit_code"`

	// Stdout is the process standard output.
	Stdout string `json:"stdout"`

	// Stderr is the process standard error.
	Stderr string `json:"stderr"`

	// Logs is standard output and standard error, interleaved.
	Logs string `json:"logs"`

	// WorkingDir is the directory the command runs in.
	WorkingDir string `json:"working_dir"`

	// StartedAt is when the process started.
	StartedAt time.Time `json:"started_at"`

	// CompletedAt is when the process exited. Zero means it has not exited.
	CompletedAt time.Time `json:"completed_at"`
}

// ProcessLogs is one process's captured output.
type ProcessLogs struct {
	// Stdout is the process standard output.
	Stdout string `json:"stdout"`

	// Stderr is the process standard error.
	Stderr string `json:"stderr"`

	// Logs is standard output and standard error, interleaved.
	Logs string `json:"logs"`
}

// The exec API sends timestamps in either format; both appear in the wild.
var execTimestampLayouts = []string{time.RFC3339, time.RFC1123, time.RFC1123Z}

func sandboxInfoFromAPI(api *managementapi.Sandbox) (SandboxInfo, error) {
	if api == nil || api.Name == nil || *api.Name == "" {
		return SandboxInfo{}, errors.New("sandbox record without a name")
	}
	info := SandboxInfo{
		Name:      *api.Name,
		URL:       stringOrEmpty(api.Url),
		Status:    string(api.Status),
		State:     stateOrEmpty(api.State),
		Image:     stringOrEmpty(api.Image),
		Envs:      envsFromAPI(api.Envs),
		Labels:    labelsFromAPI(api.Labels),
		CreatedBy: stringOrEmpty(api.CreatedBy),
		UpdatedBy: stringOrEmpty(api.UpdatedBy),
	}
	if api.Memory != nil {
		info.Memory = *api.Memory
	}
	if api.Region != nil {
		info.Region = *api.Region
	}
	if api.Enabled != nil {
		info.Enabled = *api.Enabled
	}
	if api.ExternalId != nil {
		info.ExternalID = *api.ExternalId
	}
	if api.CreatedAt != nil {
		info.CreatedAt = *api.CreatedAt
	}
	if api.UpdatedAt != nil {
		info.UpdatedAt = *api.UpdatedAt
	}
	if api.LastUsedAt != nil {
		info.LastUsedAt = *api.LastUsedAt
	}
	if api.ExpiresIn != nil {
		info.ExpiresInSeconds = *api.ExpiresIn
	}
	return info, nil
}

func processInfoFromAPI(api *sandboxapi.ProcessResponse) (ProcessInfo, error) {
	startedAt, err := parseExecTimestamp(api.StartedAt)
	if err != nil {
		return ProcessInfo{}, err
	}
	info := ProcessInfo{
		PID:        api.Pid,
		Name:       api.Name,
		Command:    api.Command,
		Status:     string(api.Status),
		ExitCode:   api.ExitCode,
		Stdout:     api.Stdout,
		Stderr:     api.Stderr,
		Logs:       api.Logs,
		WorkingDir: api.WorkingDir,
		StartedAt:  startedAt,
	}
	if api.CompletedAt != "" {
		completedAt, err := parseExecTimestamp(api.CompletedAt)
		if err != nil {
			return ProcessInfo{}, err
		}
		info.CompletedAt = completedAt
	}
	return info, nil
}

func parseExecTimestamp(value string) (time.Time, error) {
	for _, layout := range execTimestampLayouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized exec API timestamp %q", value)
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stateOrEmpty(state *managementapi.SandboxState) SandboxState {
	if state == nil {
		return ""
	}
	return SandboxState(*state)
}

func envsFromAPI(envs *[]managementapi.SandboxEnv) map[string]SandboxEnvValue {
	if envs == nil {
		return nil
	}
	result := make(map[string]SandboxEnvValue, len(*envs))
	for _, env := range *envs {
		if env.Name == nil {
			continue
		}
		value := SandboxEnvValue{}
		if env.Value != nil {
			value.Value = *env.Value
		}
		if env.Secret != nil {
			value.Secret = *env.Secret
		}
		result[*env.Name] = value
	}
	return result
}

// envsToAPI keeps nil and empty apart: nil omits the field so an update
// leaves values unchanged, an allocated empty map sends [] which replaces
// the previous values with none.
func envsToAPI(envs map[string]SandboxEnvValue) *[]managementapi.SandboxEnv {
	if envs == nil {
		return nil
	}
	result := make([]managementapi.SandboxEnv, 0, len(envs))
	for name, value := range envs {
		result = append(result, managementapi.SandboxEnv{
			Name:   &name,
			Value:  &value.Value,
			Secret: &value.Secret,
		})
	}
	return &result
}

func labelsFromAPI(labels *managementapi.SandboxMetadataLabels) map[string]string {
	if labels == nil {
		return nil
	}
	result := make(map[string]string, len(*labels))
	for name, value := range *labels {
		result[name] = value
	}
	return result
}

// labelsToAPI keeps nil and empty apart, for the same reason as envsToAPI.
func labelsToAPI(labels map[string]string) *managementapi.SandboxMetadataLabels {
	if labels == nil {
		return nil
	}
	result := managementapi.SandboxMetadataLabels(labels)
	return &result
}
