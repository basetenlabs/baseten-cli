package cmd_test

import (
	"net/http"
	"testing"
)

func trustRuleFixture() map[string]any {
	return map[string]any{
		"id":                   "tr1",
		"name":                 "models-main-deploy",
		"identity_provider_id": "idp1",
		"service_account_id":   "sa1",
		"match": map[string]any{
			"subject_glob": "repo:acme/models:ref:refs/heads/main",
			"claims":       map[string]any{"repository_owner_id": "12345"},
			"audience":     "https://api.baseten.co",
		},
		"max_access_token_lifetime_seconds": 3600,
		"created_by":                        "u1",
		"created_at":                        "2026-11-01T00:00:00Z",
		"updated_at":                        "2026-11-01T00:00:00Z",
	}
}

// trustRuleRoutes serves one rule plus the provider and account it references.
func trustRuleRoutes(h *CommandHarness) {
	identityProviderRoutes(h)
	serviceAccountRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/access/trust_rules", 200, map[string]any{
		"items":      []any{trustRuleFixture()},
		"pagination": map[string]any{"has_more": false},
	})
	m.SetRoute("GET", "/v1/access/trust_rules/tr1", 200, trustRuleFixture())
	m.SetRoute("PATCH", "/v1/access/trust_rules/tr1", 200, trustRuleFixture())
}

func Test_Org_TrustRule_Create_ResolvesNames(t *testing.T) {
	h := NewCommandHarness(t)
	trustRuleRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/trust_rules", 200, trustRuleFixture())

	h.Require.NoError(h.Execute("org", "trust-rule", "create", "--name", "models-main-deploy",
		"--identity-provider", "github-actions", "--service-account", "ci-deployer",
		"--subject-glob", "repo:acme/models:ref:refs/heads/main",
		"--claim", "repository_owner_id=12345", "--claim", "workflow=deploy=prod",
		"--condition", "claims.event_name == 'push'", "--max-access-token-lifetime-seconds", "900"))
	h.Require.Equal(map[string]any{
		"name":                 "models-main-deploy",
		"identity_provider_id": "idp1",
		"service_account_id":   "sa1",
		"match": map[string]any{
			"subject_glob": "repo:acme/models:ref:refs/heads/main",
			"claims":       map[string]any{"repository_owner_id": "12345", "workflow": "deploy=prod"},
			"condition":    "claims.event_name == 'push'",
		},
		"max_access_token_lifetime_seconds": float64(900),
	}, m.FindCall("POST", "/v1/access/trust_rules").BodyJSON(t))
	h.Require.Equal("tr1\n", h.Stdout.String())
}

func Test_Org_TrustRule_Create_ByIDs_JQ(t *testing.T) {
	h := NewCommandHarness(t)
	trustRuleRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/trust_rules", 200, trustRuleFixture())

	h.Require.NoError(h.Execute("org", "trust-rule", "create", "--name", "r", "--identity-provider", "idp2",
		"--service-account", "sa1", "--subject-glob", "x", "--jq", ".id"))
	body := m.FindCall("POST", "/v1/access/trust_rules").BodyJSON(t)
	h.Require.Equal("idp2", body["identity_provider_id"])
	h.Require.Equal("sa1", body["service_account_id"])
	h.Require.Equal("\"tr1\"\n", h.Stdout.String())
}

func Test_Org_TrustRule_Create_ClaimValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims []string
		want   string
	}{
		{"no equals", []string{"repository_owner_id"}, "expected key=value"},
		{"empty key", []string{"=1"}, "expected key=value"},
		{"duplicate", []string{"a=1", "a=2"}, "--claim a is given more than once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
				t.Fatalf("server should not be hit on usage error")
			})
			args := []string{"org", "trust-rule", "create", "--name", "r", "--identity-provider", "i", "--service-account", "s"}
			for _, c := range tc.claims {
				args = append(args, "--claim", c)
			}
			h.Require.ErrorContains(h.Execute(args...), tc.want)
		})
	}
}

func Test_Org_TrustRule_Create_UnknownServiceAccount(t *testing.T) {
	h := NewCommandHarness(t)
	trustRuleRoutes(h)

	err := h.Execute("org", "trust-rule", "create", "--name", "r", "--identity-provider", "github-actions", "--service-account", "nope")
	h.Require.ErrorContains(err, `no service account named "nope"`)
}

func Test_Org_TrustRule_List_Filters(t *testing.T) {
	h := NewCommandHarness(t)
	trustRuleRoutes(h)
	gridRouteTeams(h)

	h.Require.NoError(h.Execute("org", "trust-rule", "list", "--identity-provider", "github-actions",
		"--service-account", "ci-deployer", "--team", "Engineering"))
	q := h.MockManagementAPI().FindCall("GET", "/v1/access/trust_rules").Query()
	h.Require.Equal("idp1", q.Get("identity_provider_id"))
	h.Require.Equal("sa1", q.Get("service_account_id"))
	h.Require.Equal("t123456", q.Get("team_id"))
	out := h.Stdout.String()
	h.Require.Contains(out, "IDENTITY PROVIDER")
	h.Require.Contains(out, "models-main-deploy")
}

func Test_Org_TrustRule_Describe_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	trustRuleRoutes(h)

	h.Require.NoError(h.Execute("org", "trust-rule", "describe", "--name", "models-main-deploy"))
	out := h.Stdout.String()
	h.Require.Contains(out, "ID:                 tr1")
	h.Require.Contains(out, "Claims:             repository_owner_id=12345")
	h.Require.Contains(out, "Condition:          -")
	h.Require.Contains(out, "Max Token Lifetime: 3600s")
}

func Test_Org_TrustRule_Update_MergesMatch(t *testing.T) {
	h := NewCommandHarness(t)
	trustRuleRoutes(h)

	h.Require.NoError(h.Execute("org", "trust-rule", "update", "--name", "models-main-deploy",
		"--condition", "claims.event_name == 'push'", "--audience", "null", "--service-account", "ci-deployer"))
	h.Require.Equal(map[string]any{
		"service_account_id": "sa1",
		"match": map[string]any{
			"subject_glob": "repo:acme/models:ref:refs/heads/main",
			"claims":       map[string]any{"repository_owner_id": "12345"},
			"condition":    "claims.event_name == 'push'",
		},
	}, h.MockManagementAPI().FindCall("PATCH", "/v1/access/trust_rules/tr1").BodyJSON(t))
	h.Require.Contains(h.Stderr.String(), "Updated trust rule models-main-deploy")
}

func Test_Org_TrustRule_Update_ClaimsReplaceAll(t *testing.T) {
	h := NewCommandHarness(t)
	trustRuleRoutes(h)

	h.Require.NoError(h.Execute("org", "trust-rule", "update", "--id", "tr1", "--claim", "repository_id=9"))
	match := h.MockManagementAPI().FindCall("PATCH", "/v1/access/trust_rules/tr1").BodyJSON(t)["match"]
	h.Require.Equal(map[string]any{"repository_id": "9"}, match.(map[string]any)["claims"])
}

func Test_Org_TrustRule_Update_ClearClaims(t *testing.T) {
	h := NewCommandHarness(t)
	trustRuleRoutes(h)

	h.Require.NoError(h.Execute("org", "trust-rule", "update", "--id", "tr1", "--clear-claims"))
	match := h.MockManagementAPI().FindCall("PATCH", "/v1/access/trust_rules/tr1").BodyJSON(t)["match"]
	h.Require.Equal(map[string]any{}, match.(map[string]any)["claims"])
	h.Require.Equal("repo:acme/models:ref:refs/heads/main", match.(map[string]any)["subject_glob"])
}

func Test_Org_TrustRule_Update_ClearClaimsWithClaim(t *testing.T) {
	h := NewCommandHarness(t)
	trustRuleRoutes(h)

	h.Require.Error(h.Execute("org", "trust-rule", "update", "--id", "tr1", "--clear-claims", "--claim", "a=b"))
	h.Require.Contains(h.Stderr.String(), "--clear-claims and --claim cannot be combined")
	h.Require.Nil(h.MockManagementAPI().FindCall("PATCH", "/v1/access/trust_rules/tr1"))
}

func Test_Org_TrustRule_Update_NameOnlySkipsMatch(t *testing.T) {
	h := NewCommandHarness(t)
	trustRuleRoutes(h)

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
	trustRuleRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", "/v1/access/trust_rules/tr1", 200, map[string]any{"id": "tr1", "deleted": true})

	h.Require.NoError(h.Execute("org", "trust-rule", "delete", "--name", "models-main-deploy", "--jq", ".id"))
	h.Require.Equal("\"tr1\"\n", h.Stdout.String())
}
