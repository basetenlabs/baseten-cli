package cmd

import "testing"

func TestCloudSessionHarnessDispatchCommand(t *testing.T) {
	tests := []struct {
		name           string
		harness        string
		prompt         string
		permissionMode string
		want           string
		wantErr        bool
	}{
		{
			name:    "claude-code without a mode",
			harness: "claude-code",
			prompt:  "fix the test",
			want:    `claude -p 'fix the test'`,
		},
		{
			name:           "claude-code with a mode",
			harness:        "claude-code",
			prompt:         "fix the test",
			permissionMode: "acceptEdits",
			want:           `claude -p --permission-mode acceptEdits 'fix the test'`,
		},
		{
			name:    "codex without a mode",
			harness: "codex",
			prompt:  "fix the test",
			want:    `codex exec 'fix the test'`,
		},
		{
			name:           "codex rejects a mode",
			harness:        "codex",
			prompt:         "fix the test",
			permissionMode: "acceptEdits",
			wantErr:        true,
		},
		{
			name:    "unsupported harness",
			harness: "pi",
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := cloudSessionHarnessDispatchCommand(test.harness, test.prompt, test.permissionMode)
			if test.wantErr {
				if err == nil {
					t.Fatalf("command %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("command: %v", err)
			}
			if got != test.want {
				t.Errorf("command %q, want %q", got, test.want)
			}
		})
	}
}
