package cmd_test

import (
	"net/http"
	"testing"
)

func githubActionsProviderFixture() map[string]any {
	return map[string]any{
		"id":   "idp1",
		"name": "GitHub Actions",
		"predefined": map[string]any{
			"kind":       "GITHUB_ACTIONS",
			"issuer_url": "https://token.actions.githubusercontent.com",
		},
		"created_by": "u1",
		"created_at": "2026-11-01T00:00:00Z",
		"updated_at": "2026-11-01T00:00:00Z",
	}
}

func eksProviderFixture() map[string]any {
	return map[string]any{
		"id":   "idp2",
		"name": "prod-eks",
		"predefined": map[string]any{
			"kind":       "AWS_EKS",
			"issuer_url": "https://oidc.eks.us-west-2.amazonaws.com/id/EXAMPLE",
		},
		"created_by": "u1",
		"created_at": "2026-11-01T00:00:00Z",
		"updated_at": "2026-11-01T00:00:00Z",
	}
}

// identityProviderRoutes serves a GitHub Actions and an Amazon EKS provider.
func identityProviderRoutes(h *CommandHarness) {
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/access/identity_providers", 200, map[string]any{
		"items":      []any{githubActionsProviderFixture(), eksProviderFixture()},
		"pagination": map[string]any{"has_more": false},
	})
	m.SetRoute("GET", "/v1/access/identity_providers/idp1", 200, githubActionsProviderFixture())
	m.SetRoute("GET", "/v1/access/identity_providers/idp2", 200, eksProviderFixture())
}

func Test_Org_IdentityProvider_ListPredefined(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/access/predefined_identity_providers", 200, map[string]any{
		"items": []any{
			map[string]any{"kind": "GITHUB_ACTIONS", "name": "GitHub Actions", "issuer_url": "https://token.actions.githubusercontent.com"},
			map[string]any{"kind": "AWS_EKS", "name": "Amazon EKS", "issuer_url_example": "https://oidc.eks.us-west-2.amazonaws.com/id/EXAMPLE"},
		},
	})

	h.Require.NoError(h.Execute("org", "identity-provider", "list-predefined"))
	out := h.Stdout.String()
	h.Require.Contains(out, "KIND")
	h.Require.Contains(out, "github-actions")
	h.Require.Contains(out, "https://token.actions.githubusercontent.com")
	h.Require.Contains(out, "aws-eks")
	h.Require.Contains(out, "yours, such as https://oidc.eks.us-west-2.amazonaws.com/id/EXAMPLE")
}

func Test_Org_IdentityProvider_Create(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/identity_providers", 200, githubActionsProviderFixture())

	h.Require.NoError(h.Execute("org", "identity-provider", "create", "--kind", "github-actions"))
	h.Require.Equal(map[string]any{"predefined": map[string]any{"kind": "GITHUB_ACTIONS"}},
		m.FindCall("POST", "/v1/access/identity_providers").BodyJSON(t))
	h.Require.Equal("idp1\n", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Created identity provider GitHub Actions")
}

func Test_Org_IdentityProvider_Create_WithIssuer(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/identity_providers", 200, eksProviderFixture())

	h.Require.NoError(h.Execute("org", "identity-provider", "create", "--kind", "aws-eks", "--name", "prod-eks",
		"--issuer-url", "https://oidc.eks.us-west-2.amazonaws.com/id/EXAMPLE", "--description", "EKS"))
	h.Require.Equal(map[string]any{
		"name":        "prod-eks",
		"description": "EKS",
		"predefined": map[string]any{
			"kind":       "AWS_EKS",
			"issuer_url": "https://oidc.eks.us-west-2.amazonaws.com/id/EXAMPLE",
		},
	}, m.FindCall("POST", "/v1/access/identity_providers").BodyJSON(t))
}

func Test_Org_IdentityProvider_Create_FlagValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no kind", nil, "kind"},
		{"unknown kind", []string{"--kind", "custom"}, "custom"},
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

	h.Require.NoError(h.Execute("org", "identity-provider", "list", "--kind", "aws-eks"))
	out := h.Stdout.String()
	h.Require.Contains(out, "KIND")
	h.Require.Contains(out, "github-actions")
	h.Require.Contains(out, "https://oidc.eks.us-west-2.amazonaws.com/id/EXAMPLE")
	h.Require.Equal("AWS_EKS", h.MockManagementAPI().FindCall("GET", "/v1/access/identity_providers").Query().Get("predefined_kind"))
}

func Test_Org_IdentityProvider_List_JQ(t *testing.T) {
	h := NewCommandHarness(t)
	identityProviderRoutes(h)

	h.Require.NoError(h.Execute("org", "identity-provider", "list", "--jq", ".items[].name"))
	h.Require.Equal("\"GitHub Actions\"\n\"prod-eks\"\n", h.Stdout.String())
}

func Test_Org_IdentityProvider_Describe_ByKind(t *testing.T) {
	h := NewCommandHarness(t)
	identityProviderRoutes(h)

	h.Require.NoError(h.Execute("org", "identity-provider", "describe", "--name", "github-actions"))
	out := h.Stdout.String()
	h.Require.Contains(out, "ID:               idp1")
	h.Require.Contains(out, "Kind:             github-actions")
	h.Require.Contains(out, "Issuer URL:       https://token.actions.githubusercontent.com")
}

func Test_Org_IdentityProvider_Update(t *testing.T) {
	h := NewCommandHarness(t)
	identityProviderRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("PATCH", "/v1/access/identity_providers/idp2", 200, eksProviderFixture())

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
