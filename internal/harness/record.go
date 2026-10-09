package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// recordFileName names the file of setup records, next to the CLI's auth.json.
const recordFileName = "harness.json"

// Record remembers the options of the last successful setup for one harness,
// so sync can replay them. It stores raw flag values only: an empty route means
// the option was not passed and follows the current default, never the value
// resolved at setup time. Profile and ManagementURL scope the record the same
// way saved routes API keys are scoped.
type Record struct {
	// Config is the settings file path setup wrote, which sync must match.
	Config          string `json:"config"`
	Profile         string `json:"profile"`
	ManagementURL   string `json:"management_url"`
	TeamID          string `json:"team_id"`
	KeyName         string `json:"key_name,omitempty"`
	Route           string `json:"route,omitempty"`
	BackgroundRoute string `json:"background_route,omitempty"`
	SubagentRoute   string `json:"subagent_route,omitempty"`
	FallbackRoute   string `json:"fallback_route,omitempty"`
	// CodexRestartPending records that the codex app-server daemon still needs
	// a restart, because the last one was declined or failed. Retried on the
	// next sync.
	CodexRestartPending bool `json:"codex_restart_pending,omitempty"`
}

// RecordFile is the on-disk harness.json structure. Unrecognized keys are
// ignored on read and dropped on the next write, like auth.json.
type RecordFile struct {
	Version   int               `json:"version"`
	Harnesses map[string]Record `json:"harnesses"`
}

// LoadRecords reads the setup records in dir. A missing file is an empty
// record file.
func LoadRecords(dir string) (RecordFile, error) {
	file := RecordFile{Version: 1, Harnesses: map[string]Record{}}
	data, err := os.ReadFile(filepath.Join(dir, recordFileName))
	if errors.Is(err, os.ErrNotExist) {
		return file, nil
	}
	if err != nil {
		return RecordFile{}, fmt.Errorf("reading harness records: %w", err)
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return RecordFile{}, fmt.Errorf("parsing harness records: %w", err)
	}
	if file.Harnesses == nil {
		file.Harnesses = map[string]Record{}
	}
	return file, nil
}

// SaveRecords writes the setup records to dir, replacing the file.
func SaveRecords(dir string, file RecordFile) error {
	file.Version = 1
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling harness records: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, recordFileName), data, 0o600)
}
