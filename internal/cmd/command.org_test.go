package cmd_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

// auditLogsBody returns a representative audit-logs page with two entries: a
// user actor and an API-key actor, covering both actor renderings.
func auditLogsBody() map[string]any {
	return map[string]any{
		"items": []any{
			map[string]any{
				"id":         "log-1",
				"created":    "2026-06-01T12:00:00Z",
				"actor":      map[string]any{"type": "USER", "email": "alice@example.com"},
				"source":     "UI",
				"event_type": "MODEL_DEPLOYED",
				"event_data": map[string]any{"event_type": "MODEL_DEPLOYED", "model_id": "m1", "model_name": "my-model"},
			},
			map[string]any{
				"id":         "log-2",
				"created":    "2026-06-01T11:00:00Z",
				"actor":      map[string]any{"type": "API_KEY", "api_key_prefix": "bsnt_abc"},
				"source":     "API",
				"event_type": "SECRET_UPDATED",
				"event_data": map[string]any{"event_type": "SECRET_UPDATED", "secret_id": "s1", "secret_name": "TOK"},
			},
		},
		"pagination": map[string]any{"has_more": false},
	}
}

// auditEntry builds a minimal audit-log entry for pagination tests.
func auditEntry(id, eventType string) map[string]any {
	return map[string]any{
		"id":         id,
		"created":    "2026-06-01T12:00:00Z",
		"actor":      map[string]any{"type": "USER", "email": "alice@example.com"},
		"source":     "UI",
		"event_type": eventType,
		"event_data": map[string]any{"event_type": eventType},
	}
}

func Test_Org_AuditLogs_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/audit_logs", 200, map[string]any{
		"items": []any{}, "pagination": map[string]any{"has_more": false},
	})

	h.Require.NoError(h.Execute("org", "audit-logs"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No audit-log entries found.")
}

func Test_Org_AuditLogs_Table(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/audit_logs", 200, auditLogsBody())

	h.Require.NoError(h.Execute("org", "audit-logs"))
	out := h.Stdout.String()
	h.Require.Contains(out, "TIME")
	h.Require.Contains(out, "ACTOR")
	h.Require.Contains(out, "EVENT")
	h.Require.Contains(out, "SOURCE")
	h.Require.Contains(out, "alice@example.com")
	h.Require.Contains(out, "MODEL_DEPLOYED")
	h.Require.Contains(out, "UI")
	// API-key actors render as the key prefix plus a masked suffix.
	h.Require.Contains(out, "bsnt_abc****")
	h.Require.Contains(out, "SECRET_UPDATED")

	// Defaults: 20-entry page limit, newest-first.
	call := m.FindCall("GET", "/v1/audit_logs")
	h.Require.NotNil(call)
	h.Require.Equal("20", call.Query().Get("limit"))
	h.Require.Equal("DESC", call.Query().Get("direction"))
}

func Test_Org_AuditLogs_Filters(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/audit_logs", 200, auditLogsBody())

	h.Require.NoError(h.Execute("org", "audit-logs",
		"--event-type-group", "deployed",
		"--event-type-group", "promoted",
		"--source", "ui",
		"--user-id", "u1",
		"--deployment-id", "d1",
		"--environment", "production",
		"--search", "foo",
		"--direction", "asc",
	))

	call := m.FindCall("GET", "/v1/audit_logs")
	h.Require.NotNil(call)
	q := call.Query()
	// Kebab CLI values map to the ALL_CAPS backend enums, repeated once per value.
	h.Require.Equal("DEPLOYED,PROMOTED", strings.Join(q["event_type_groups"], ","))
	h.Require.Equal("UI", strings.Join(q["sources"], ","))
	h.Require.Equal("u1", strings.Join(q["user_ids"], ","))
	h.Require.Equal("d1", strings.Join(q["deployment_ids"], ","))
	h.Require.Equal("production", strings.Join(q["environment_names"], ","))
	h.Require.Equal("foo", q.Get("search"))
	h.Require.Equal("ASC", q.Get("direction"))
}

func Test_Org_AuditLogs_InvalidSource(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on validation error")
	})

	err := h.Execute("org", "audit-logs", "--source", "carrier-pigeon")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "invalid --source")
	h.Require.Contains(err.Error(), "ui, api, mcp, other")
}

func Test_Org_AuditLogs_InvalidEventTypeGroup(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on validation error")
	})

	err := h.Execute("org", "audit-logs", "--event-type-group", "bogus")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "invalid --event-type-group")
}

func Test_Org_AuditLogs_SinceWithStartIsError(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on validation error")
	})

	err := h.Execute("org", "audit-logs", "--since", "1h", "--start", "2026-05-01")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--since cannot be combined with --start or --end")
}

func Test_Org_AuditLogs_SinceWindow(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/audit_logs", 200, auditLogsBody())
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })

	h.Require.NoError(h.Execute("org", "audit-logs", "--since", "2h"))

	call := m.FindCall("GET", "/v1/audit_logs")
	h.Require.NotNil(call)
	h.Require.Equal(strconv.FormatInt(now.UnixMilli(), 10), call.Query().Get("end_epoch_millis"))
	h.Require.Equal(strconv.FormatInt(now.Add(-2*time.Hour).UnixMilli(), 10), call.Query().Get("start_epoch_millis"))
}

func Test_Org_AuditLogs_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/audit_logs", 200, auditLogsBody())

	h.Require.NoError(h.Execute("org", "audit-logs", "--output", "json"))
	out := strings.TrimSpace(h.Stdout.String())
	h.Require.True(strings.HasPrefix(out, "["), "JSON output should be an array")
	h.Require.Contains(out, `"event_type"`)
	h.Require.Contains(out, "MODEL_DEPLOYED")
}

func Test_Org_AuditLogs_PaginatesAllPages(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	// One entry per page; the second page is reached only by following the
	// cursor, so seeing both rows proves the pager advanced.
	m.SetRouteFunc("GET", "/v1/audit_logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":      []any{auditEntry("log-1", "MODEL_DEPLOYED")},
				"pagination": map[string]any{"has_more": true, "cursor": "next-1"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":      []any{auditEntry("log-2", "SECRET_UPDATED")},
			"pagination": map[string]any{"has_more": false},
		})
	})

	h.Require.NoError(h.Execute("org", "audit-logs", "--limit", "0", "--page-size", "1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "MODEL_DEPLOYED")
	h.Require.Contains(out, "SECRET_UPDATED")

	calls := m.Calls()
	h.Require.Equal(2, len(calls))
	h.Require.Equal("next-1", calls[1].Query().Get("cursor"))
}

func Test_Org_AuditLogs_LimitReachedNote(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/audit_logs", 200, map[string]any{
		"items":      []any{auditEntry("log-1", "MODEL_DEPLOYED")},
		"pagination": map[string]any{"has_more": true, "cursor": "next-1"},
	})

	h.Require.NoError(h.Execute("org", "audit-logs", "--limit", "1"))
	h.Require.Contains(h.Stdout.String(), "MODEL_DEPLOYED")
	h.Require.Contains(h.Stderr.String(), "Reached the --limit of 1 entries")
}
