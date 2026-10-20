package cmd_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func Test_Grid_Classifier_List(t *testing.T) {
	for _, empty := range []bool{false, true} {
		for _, output := range []string{"text", "json", "jsonl"} {
			t.Run(fmt.Sprintf("%t/%s", empty, output), func(t *testing.T) {
				h := NewCommandHarness(t)
				m := h.MockManagementAPI()
				items := []any{}
				if !empty {
					items = append(items, map[string]any{"id": "general-v1", "display_name": "General"})
				}
				body := map[string]any{"classifiers": items}
				m.SetRouteFunc("GET", "/v1/grid/classifiers", func(w http.ResponseWriter, r *http.Request) {
					h.Require.Equal("Bearer test-key", r.Header.Get("Authorization"))
					h.Require.Empty(r.URL.RawQuery)
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(body)
				})
				h.Require.NoError(h.Execute("grid", "classifier", "list", "--output", output))
				h.Require.Len(m.Calls(), 1)
				if output == "text" {
					if empty {
						h.Require.Contains(h.Stderr.String(), "No classifiers found")
					} else {
						h.Require.Contains(h.Stdout.String(), "general-v1")
						h.Require.Contains(h.Stdout.String(), "General")
					}
				} else {
					var got any
					h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
					h.Require.Equal(body, got)
				}
			})
		}
	}
}

func Test_Grid_Classifier_List_Errors(t *testing.T) {
	for _, status := range []int{401, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("GET", "/v1/grid/classifiers", status, map[string]string{"error": "unavailable"})
			h.Require.Error(h.Execute("grid", "classifier", "list", "--output", "json"))
			h.Require.Equal(status, decodeJSONErrorEnvelope(h).APIStatusCode)
		})
	}
	t.Run("malformed", func(t *testing.T) {
		h := NewCommandHarness(t)
		m := h.MockManagementAPI()
		m.SetRouteFunc("GET", "/v1/grid/classifiers", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"classifiers":`))
		})
		h.Require.ErrorContains(h.Execute("grid", "classifier", "list"), "unexpected end of JSON input")
	})
}

func Test_Grid_UnavailableDescribeCommandsAreNotExposed(t *testing.T) {
	for _, resource := range []string{"classifier", "connection"} {
		t.Run(resource, func(t *testing.T) {
			h := NewCommandHarness(t)
			h.Require.ErrorContains(h.Execute("grid", resource, "describe"), "unknown command")
		})
	}
}
