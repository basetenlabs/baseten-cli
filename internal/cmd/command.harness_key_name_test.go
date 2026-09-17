package cmd_test

import (
	internalcmd "github.com/basetenlabs/baseten-cli/internal/cmd"
	"testing"
)

func Test_Harness_Auth_DefaultHarnessKeyName(t *testing.T) {
	for _, tc := range []struct{ host, want string }{
		{"Daniels-MacBook-Pro.local", "baseten-harness-daniels-macbook-pro"},
		{"workstation-123", "baseten-harness-workstation-123"},
		{"My__Laptop...", "baseten-harness-my-laptop"},
		{"", "baseten-harness-local-machine"},
		{"...", "baseten-harness-local-machine"},
	} {
		t.Run(tc.host, func(t *testing.T) {
			if got := internalcmd.DefaultHarnessKeyNameForTest(tc.host); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
