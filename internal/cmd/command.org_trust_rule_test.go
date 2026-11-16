package cmd_test

import (
	"net/http"
	"testing"
)

const orgGitHubCondition = "claims.repository_owner_id == '12345' && claims.sub == 'repo:acme/models:ref:refs/heads/main'"

func orgTrustRuleFixture() map[string]any {
	return map[string]any{
		"id":                                "tr1",
		"name":                              "models-main-deploy",
		"identity_provider_id":              "idp1",
		"identity_provider_name":            "GitHub Actions",
		"service_account_id":                "sa1",
		"service_account_name":              "ci-deployer",
		"condition":                         orgGitHubCondition,
		"audience":                          "https://api.baseten.co",
		"max_access_token_lifetime_seconds": 3600,
		"created_by":                        "u1",
		"created_at":                        "2026-11-01T00:00:00Z",
		"updated_at":                        "2026-11-01T00:00:00Z",
	}
}

// orgTrustRuleRoutes serves one rule plus the provider and account it references.
func orgTrustRuleRoutes(h *CommandHarness) {
	orgIdentityProviderRoutes(h)
	orgServiceAccountRoutes(h)
	m := h.MockManagementAPI()
	orgSetFilteredListRoute(m, "/v1/access/trust_rules", orgTrustRuleFixture())
	m.SetRoute("GET", "/v1/access/trust_rules/tr1", 200, orgTrustRuleFixture())
	m.SetRoute("PATCH", "/v1/access/trust_rules/tr1", 200, orgTrustRuleFixture())
}

func Test_Org_TrustRule_Create_ResolvesNames(t *testing.T) {
	h := NewCommandHarness(t)
	orgTrustRuleRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/trust_rules", 200, orgTrustRuleFixture())

	h.Require.NoError(h.Execute("org", "trust-rule", "create", "--name", "models-main-deploy",
		"--identity-provider-name", "github-actions", "--service-account-name", "ci-deployer",
		"--condition", orgGitHubCondition, "--audience", "https://api.baseten.co",
		"--max-access-token-lifetime", "15m"))
	h.Require.Equal(map[string]any{
		"name":                              "models-main-deploy",
		"identity_provider_id":              "idp1",
		"service_account_id":                "sa1",
		"condition":                         orgGitHubCondition,
		"audience":                          "https://api.baseten.co",
		"max_access_token_lifetime_seconds": float64(900),
	}, m.FindCall("POST", "/v1/access/trust_rules").BodyJSON(t))
	h.Require.Equal("tr1\n", h.Stdout.String())
}

func Test_Org_TrustRule_Create_ByIDs_JQ(t *testing.T) {
	h := NewCommandHarness(t)
	orgTrustRuleRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/trust_rules", 200, orgTrustRuleFixture())

	h.Require.NoError(h.Execute("org", "trust-rule", "create", "--name", "r", "--identity-provider-id", "idp2",
		"--service-account-id", "sa1", "--condition", "claims.sub == 'system:serviceaccount:ml:deployer'",
		"--audience", "https://api.baseten.co", "--jq", ".id"))
	body := m.FindCall("POST", "/v1/access/trust_rules").BodyJSON(t)
	h.Require.Equal("idp2", body["identity_provider_id"])
	h.Require.Equal("sa1", body["service_account_id"])
	h.Require.Equal("\"tr1\"\n", h.Stdout.String())
}

func Test_Org_TrustRule_Create_RequiresConditionAndAudience(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no condition", []string{"--audience", "https://api.baseten.co"}, "condition"},
		{"no audience", []string{"--condition", orgGitHubCondition}, "audience"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
				t.Fatalf("server should not be hit on usage error")
			})
			args := append([]string{"org", "trust-rule", "create", "--name", "r", "--identity-provider-id", "i", "--service-account-id", "s"}, tc.args...)
			h.Require.ErrorContains(h.Execute(args...), tc.want)
		})
	}
}

func Test_Org_TrustRule_Create_UnknownServiceAccountName(t *testing.T) {
	h := NewCommandHarness(t)
	orgTrustRuleRoutes(h)

	err := h.Execute("org", "trust-rule", "create", "--name", "r", "--identity-provider-id", "idp1",
		"--service-account-name", "nope", "--condition", orgGitHubCondition, "--audience", "https://api.baseten.co")
	h.Require.ErrorContains(err, `no service account named "nope"`)
}

func Test_Org_TrustRule_List_Filters(t *testing.T) {
	h := NewCommandHarness(t)
	orgTrustRuleRoutes(h)
	gridRouteTeams(h)

	h.Require.NoError(h.Execute("org", "trust-rule", "list", "--identity-provider-name", "github-actions",
		"--service-account-name", "ci-deployer", "--team", "Engineering"))
	q := h.MockManagementAPI().FindCall("GET", "/v1/access/trust_rules").Query()
	h.Require.Equal("idp1", q.Get("identity_provider_id"))
	h.Require.Equal("sa1", q.Get("service_account_id"))
	h.Require.Equal("t123456", q.Get("team_id"))
	out := h.Stdout.String()
	h.Require.Contains(out, "IDENTITY PROVIDER")
	h.Require.Contains(out, "models-main-deploy")
	h.Require.Contains(out, "GitHub Actions")
	h.Require.Contains(out, "ci-deployer")
}

func Test_Org_TrustRule_Describe_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	orgTrustRuleRoutes(h)

	h.Require.NoError(h.Execute("org", "trust-rule", "describe", "--name", "models-main-deploy"))
	out := h.Stdout.String()
	h.Require.Contains(out, "ID:                 tr1")
	h.Require.Contains(out, "Identity Provider:  GitHub Actions (idp1)")
	h.Require.Contains(out, "Condition:          "+orgGitHubCondition)
	h.Require.Contains(out, "Audience:           https://api.baseten.co")
	h.Require.Contains(out, "Max Token Lifetime: 1h0m0s")
}

func Test_Org_TrustRule_Update_SendsOnlyGivenFields(t *testing.T) {
	h := NewCommandHarness(t)
	orgTrustRuleRoutes(h)

	h.Require.NoError(h.Execute("org", "trust-rule", "update", "--name", "models-main-deploy",
		"--condition", orgGitHubCondition+" && claims.event_name == 'push'", "--service-account-name", "ci-deployer",
		"--max-access-token-lifetime", "10m"))
	h.Require.Equal(map[string]any{
		"service_account_id":                "sa1",
		"condition":                         orgGitHubCondition + " && claims.event_name == 'push'",
		"max_access_token_lifetime_seconds": float64(600),
	}, h.MockManagementAPI().FindCall("PATCH", "/v1/access/trust_rules/tr1").BodyJSON(t))
	h.Require.Contains(h.Stderr.String(), "Updated trust rule models-main-deploy")
}

func Test_Org_TrustRule_Update_NameOnly(t *testing.T) {
	h := NewCommandHarness(t)
	orgTrustRuleRoutes(h)

	h.Require.NoError(h.Execute("org", "trust-rule", "update", "--id", "tr1", "--new-name", "deploy", "--description", ""))
	h.Require.Equal(map[string]any{"name": "deploy", "description": ""},
		h.MockManagementAPI().FindCall("PATCH", "/v1/access/trust_rules/tr1").BodyJSON(t))
	h.Require.Nil(h.MockManagementAPI().FindCall("GET", "/v1/access/trust_rules/tr1"))
}

func Test_Org_TrustRule_Update_NothingToUpdate(t *testing.T) {
	h := NewCommandHarness(t)
	h.Require.ErrorContains(h.Execute("org", "trust-rule", "update", "--id", "tr1"), "nothing to update")
}

func Test_Org_TrustRule_Delete_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	orgTrustRuleRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", "/v1/access/trust_rules/tr1", 200, map[string]any{"id": "tr1", "deleted": true})

	h.Require.NoError(h.Execute("org", "trust-rule", "delete", "--name", "models-main-deploy", "--jq", ".id"))
	h.Require.Equal("\"tr1\"\n", h.Stdout.String())
}
