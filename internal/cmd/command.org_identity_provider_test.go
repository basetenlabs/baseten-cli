package cmd_test

import (
	"net/http"
	"testing"
)

func githubActionsProviderFixture() map[string]any {
	return map[string]any{
		"id":         "idp1",
		"name":       "GitHub Actions",
		"type":       "PREDEFINED",
		"predefined": "GITHUB_ACTIONS",
		"issuer_url": "https://token.actions.githubusercontent.com",
		"created_by": "u1",
		"created_at": "2026-11-01T00:00:00Z",
		"updated_at": "2026-11-01T00:00:00Z",
	}
}

func customProviderFixture() map[string]any {
	return map[string]any{
		"id":         "idp2",
		"name":       "prod-eks",
		"type":       "CUSTOM",
		"issuer_url": "https://oidc.eks.example.com/id/1",
		"jwks":       map[string]any{"type": "DISCOVERY"},
		"created_by": "u1",
		"created_at": "2026-11-01T00:00:00Z",
		"updated_at": "2026-11-01T00:00:00Z",
	}
}

// identityProviderRoutes serves a predefined and a custom provider.
func identityProviderRoutes(h *CommandHarness) {
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/access/identity_providers", 200, map[string]any{
		"items":      []any{githubActionsProviderFixture(), customProviderFixture()},
		"pagination": map[string]any{"has_more": false},
	})
	m.SetRoute("GET", "/v1/access/identity_providers/idp1", 200, githubActionsProviderFixture())
	m.SetRoute("GET", "/v1/access/identity_providers/idp2", 200, customProviderFixture())
}

func Test_Org_IdentityProvider_ListPredefined(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/access/predefined_identity_providers", 200, map[string]any{
		"items": []any{map[string]any{"predefined": "GITHUB_ACTIONS", "name": "GitHub Actions", "issuer_url": "https://token.actions.githubusercontent.com"}},
	})

	h.Require.NoError(h.Execute("org", "identity-provider", "list-predefined"))
	out := h.Stdout.String()
	h.Require.Contains(out, "PREDEFINED")
	h.Require.Contains(out, "github-actions")
	h.Require.Contains(out, "https://token.actions.githubusercontent.com")
}

func Test_Org_IdentityProvider_Create_Predefined(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/identity_providers", 200, githubActionsProviderFixture())

	h.Require.NoError(h.Execute("org", "identity-provider", "create", "--predefined", "github-actions"))
	h.Require.Equal(map[string]any{"type": "PREDEFINED", "predefined": "GITHUB_ACTIONS"},
		m.FindCall("POST", "/v1/access/identity_providers").BodyJSON(t))
	h.Require.Equal("idp1\n", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Created identity provider GitHub Actions")
}

func Test_Org_IdentityProvider_Create_Custom(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/identity_providers", 200, customProviderFixture())

	h.Require.NoError(h.Execute("org", "identity-provider", "create", "--name", "prod-eks",
		"--issuer-url", "https://oidc.eks.example.com/id/1", "--description", "EKS"))
	h.Require.Equal(map[string]any{
		"type":        "CUSTOM",
		"name":        "prod-eks",
		"description": "EKS",
		"issuer_url":  "https://oidc.eks.example.com/id/1",
		"jwks":        map[string]any{"type": "DISCOVERY"},
	}, m.FindCall("POST", "/v1/access/identity_providers").BodyJSON(t))
}

func Test_Org_IdentityProvider_Create_FlagValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"neither", nil, "pass --predefined"},
		{"both", []string{"--predefined", "gcp", "--issuer-url", "https://x"}, "not both"},
		{"custom without name", []string{"--issuer-url", "https://x"}, "--name is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
				t.Fatalf("server should not be hit on usage error")
			})
			err := h.Execute(append([]string{"org", "identity-provider", "create"}, tc.args...)...)
			h.Require.ErrorContains(err, tc.want)
		})
	}
}

func Test_Org_IdentityProvider_List(t *testing.T) {
	h := NewCommandHarness(t)
	identityProviderRoutes(h)

	h.Require.NoError(h.Execute("org", "identity-provider", "list", "--type", "custom"))
	out := h.Stdout.String()
	h.Require.Contains(out, "ISSUER URL")
	h.Require.Contains(out, "github-actions")
	h.Require.Contains(out, "custom")
	h.Require.Equal("CUSTOM", h.MockManagementAPI().FindCall("GET", "/v1/access/identity_providers").Query().Get("type"))
}

func Test_Org_IdentityProvider_List_JQ(t *testing.T) {
	h := NewCommandHarness(t)
	identityProviderRoutes(h)

	h.Require.NoError(h.Execute("org", "identity-provider", "list", "--jq", ".items[].name"))
	h.Require.Equal("\"GitHub Actions\"\n\"prod-eks\"\n", h.Stdout.String())
}

func Test_Org_IdentityProvider_Describe_ByPredefinedValue(t *testing.T) {
	h := NewCommandHarness(t)
	identityProviderRoutes(h)

	h.Require.NoError(h.Execute("org", "identity-provider", "describe", "--name", "github-actions"))
	out := h.Stdout.String()
	h.Require.Contains(out, "ID:               idp1")
	h.Require.Contains(out, "Type:             github-actions")
	h.Require.NotContains(out, "Key Source")
}

func Test_Org_IdentityProvider_Describe_Custom(t *testing.T) {
	h := NewCommandHarness(t)
	identityProviderRoutes(h)

	h.Require.NoError(h.Execute("org", "identity-provider", "describe", "--name", "prod-eks"))
	h.Require.Contains(h.Stdout.String(), "Key Source:       discovery")
}

func Test_Org_IdentityProvider_Update(t *testing.T) {
	h := NewCommandHarness(t)
	identityProviderRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("PATCH", "/v1/access/identity_providers/idp2", 200, customProviderFixture())

	h.Require.NoError(h.Execute("org", "identity-provider", "update", "--name", "prod-eks", "--new-name", "eks"))
	h.Require.Equal(map[string]any{"name": "eks"}, m.FindCall("PATCH", "/v1/access/identity_providers/idp2").BodyJSON(t))
}

func Test_Org_IdentityProvider_Delete_ByID(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", "/v1/access/identity_providers/idp2", 200, map[string]any{"id": "idp2", "deleted": true})

	h.Require.NoError(h.Execute("org", "identity-provider", "delete", "--id", "idp2"))
	h.Require.Contains(h.Stderr.String(), "Deleted identity provider idp2")
}

func Test_Org_IdentityProvider_Verify_IssuerURL(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/identity_providers/verify", 200, map[string]any{"ok": true, "key_ids": []any{"k1", "k2"}})

	h.Require.NoError(h.Execute("org", "identity-provider", "verify", "--issuer-url", "https://issuer.example.com"))
	h.Require.Equal(map[string]any{"issuer_url": "https://issuer.example.com", "jwks": map[string]any{"type": "DISCOVERY"}},
		m.FindCall("POST", "/v1/access/identity_providers/verify").BodyJSON(t))
	h.Require.Equal("k1\nk2\n", h.Stdout.String())
}

func Test_Org_IdentityProvider_Verify_SavedProviderFails(t *testing.T) {
	h := NewCommandHarness(t)
	identityProviderRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/identity_providers/verify", 200, map[string]any{"ok": false, "key_ids": []any{}, "error": "404 from issuer"})

	err := h.Execute("org", "identity-provider", "verify", "--name", "prod-eks", "--output", "json")
	h.Require.ErrorContains(err, "404 from issuer")
	h.Require.Equal("https://oidc.eks.example.com/id/1", m.FindCall("POST", "/v1/access/identity_providers/verify").BodyJSON(t)["issuer_url"])
	h.Require.Contains(h.Stdout.String(), `"ok": false`)
	h.Require.NotContains(h.Stdout.String(), `"error": {`)
}
