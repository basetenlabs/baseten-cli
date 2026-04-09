package cmd_test

import "testing"

func Test_Truss_NotOnPath(t *testing.T) {
	h := NewCommandHarness(t)
	h.T.Setenv("PATH", "")
	_ = h.Execute("truss", "--help")
	h.Require.True(h.Exited())
	h.Require.Contains(h.Stderr.String(), "truss not found")
}
