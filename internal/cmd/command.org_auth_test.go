package cmd_test

import (
	"testing"
)

func orgInfoFixture(assumeRole any) map[string]any {
	return map[string]any{
		"org_id":          "abcd1234",
		"name":            "my-org",
		"created_at":      "2026-01-01T00:00:00Z",
		"aws_assume_role": assumeRole,
	}
}

func assumeRoleFixture() map[string]any {
	return map[string]any{
		"baseten_role_arn": "arn:aws:iam::337139236424:role/baseten-customer-access",
		"external_id":      "baseten-2fdd8a01c4c34e6bb92a2b96fca29b70",
	}
}

func teamsFixture() map[string]any {
	return map[string]any{"teams": []any{
		map[string]any{
			"id": "t1", "name": "my-team", "default": true,
			"created_at": "2026-01-01T00:00:00Z",
		},
	}}
}

func Test_Org_Describe_Text(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/organizations/me", 200,
		orgInfoFixture(assumeRoleFixture()))
	h.MockManagementAPI().SetRoute("GET", "/v1/teams", 200, teamsFixture())

	h.Require.NoError(h.Execute("org", "describe"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Org ID:               abcd1234")
	h.Require.Contains(out, "Name:                 my-org")
	h.Require.Contains(out, "t1 (my-team)")
	h.Require.Contains(out, "Issuer:               https://oidc.baseten.co")
	h.Require.Contains(out, "Subject Claim Format: v=1:org=<org_id>")
	h.Require.Contains(out, "Baseten Role ARN:     arn:aws:iam::337139236424:role/baseten-customer-access")
	h.Require.Contains(out, "AWS External ID:      baseten-2fdd8a01c4c34e6bb92a2b96fca29b70")
}

func Test_Org_Describe_Text_AssumeRoleNotEnabled(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/organizations/me", 200,
		orgInfoFixture(nil))
	h.MockManagementAPI().SetRoute("GET", "/v1/teams", 200, teamsFixture())

	h.Require.NoError(h.Execute("org", "describe"))
	out := h.Stdout.String()
	h.Require.Contains(out, "AWS AssumeRole:       not enabled")
	h.Require.NotContains(out, "Baseten Role ARN")
}

func Test_Org_Describe_JSON_IsTheResponseType(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/organizations/me", 200,
		orgInfoFixture(assumeRoleFixture()))

	h.Require.NoError(h.Execute("org", "describe", "--output", "json"))
	out := h.Stdout.String()
	h.Require.Contains(out, `"org_id": "abcd1234"`)
	h.Require.Contains(out, `"external_id": "baseten-2fdd8a01c4c34e6bb92a2b96fca29b70"`)
}
