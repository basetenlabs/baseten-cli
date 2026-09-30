package cmd_test

import (
	"testing"
)

func Test_Whoami_Text(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/users/me", 200,
		userFixture("u1", "ada@acme.io", "Ada Lovelace"))

	h.Require.NoError(h.Execute("whoami"))
	out := h.Stdout.String()
	h.Require.Contains(out, "u1")
	h.Require.Contains(out, "ada@acme.io")
	h.Require.Contains(out, "Ada Lovelace")
	h.Require.Contains(out, "Acme")
}

func Test_Whoami_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/users/me", 200,
		userFixture("u1", "ada@acme.io", "Ada Lovelace"))

	h.Require.NoError(h.Execute("whoami", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"email": "ada@acme.io"`)
}
