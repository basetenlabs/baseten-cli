package cmd_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	internalcmd "github.com/basetenlabs/baseten-cli/internal/cmd"
)

const volumeSyncCreatedAt = "2026-09-08T18:00:00Z"

func volumeSyncPayload(id, status string) map[string]any {
	return map[string]any{
		"sync_id": id,
		"status":  status,
		"source": map[string]any{
			"type": "HUGGING_FACE", "uri": "hf://org/model",
			"include": []string{}, "exclude": []string{},
		},
		"destination":       map[string]any{"ref": "bdn:weights/model:prod"},
		"volume_version_id": nil,
		"version_ref":       nil,
		"content_digest":    nil,
		"total_size_bytes":  nil,
		"created_at":        volumeSyncCreatedAt,
		"completed_at":      nil,
		"error":             nil,
	}
}

func Test_Volume_Sync_Start_BuildsSourceRequest(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/volumes/syncs", http.StatusOK, volumeSyncPayload("vsync-1", "PENDING"))

	err := h.Execute("volume", "sync", "start",
		"--source", "hf://org/model",
		"--destination", "bdn:weights/model:prod",
		"--include", "*.safetensors",
		"--include", "config.json",
		"--exclude", "*.md",
		"--auth-secret-name", "hf-token",
		"--output", "json")
	h.Require.NoError(err)

	call := m.FindCall("POST", "/v1/volumes/syncs")
	h.Require.NotNil(call)
	body := call.BodyJSON(t)
	h.Require.Equal(map[string]any{"ref": "bdn:weights/model:prod"}, body["destination"])
	h.Require.Equal(map[string]any{
		"type":             "HUGGING_FACE",
		"uri":              "hf://org/model",
		"include":          []any{"*.safetensors", "config.json"},
		"exclude":          []any{"*.md"},
		"auth_secret_name": "hf-token",
	}, body["source"])
	h.Require.Contains(h.Stdout.String(), `"sync_id": "vsync-1"`)
}

func Test_Volume_Sync_Start_InfersAWSAssumeRole(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/volumes/syncs", 200, volumeSyncPayload("vsync-2", "PENDING"))

	err := h.Execute("volume", "sync", "start",
		"--source", "s3://bucket/models",
		"--destination", "bdn:weights/model",
		"--auth-aws-assume-role-arn", "arn:aws:iam::123:role/sync",
		"--auth-aws-assume-role-region", "us-west-2")
	h.Require.NoError(err)

	source := m.FindCall("POST", "/v1/volumes/syncs").BodyJSON(t)["source"].(map[string]any)
	h.Require.Equal("S3", source["type"])
	h.Require.Equal(map[string]any{
		"role_arn": "arn:aws:iam::123:role/sync",
		"region":   "us-west-2",
	}, source["aws_assume_role"])
	h.Require.Equal([]any{}, source["include"])
	h.Require.Equal([]any{}, source["exclude"])
	h.Require.NotContains(source, "auth_secret_name")
}

func Test_Volume_Sync_Start_ValidatesAuthenticationGroups(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "incomplete assume role",
			args: []string{"--auth-aws-assume-role-arn", "arn:aws:iam::123:role/sync"},
			want: "must be provided together",
		},
		{
			name: "conflicting methods",
			args: []string{
				"--auth-secret-name", "aws-secret",
				"--auth-aws-assume-role-arn", "arn:aws:iam::123:role/sync",
				"--auth-aws-assume-role-region", "us-west-2",
			},
			want: "mutually exclusive",
		},
		{
			name: "assume role on hugging face",
			args: []string{
				"--auth-aws-assume-role-arn", "arn:aws:iam::123:role/sync",
				"--auth-aws-assume-role-region", "us-west-2",
			},
			want: "only for an s3:// source",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			args := []string{"volume", "sync", "start", "--source", "hf://org/model",
				"--destination", "bdn:weights/model"}
			args = append(args, tc.args...)
			err := h.Execute(args...)
			h.Require.ErrorContains(err, tc.want)
		})
	}
}

func Test_Volume_Sync_Start_WaitPollsToReady(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	h.Context = internalcmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	m.SetRoute("POST", "/v1/volumes/syncs", 200, volumeSyncPayload("vsync-3", "PENDING"))
	gets := 0
	m.SetRouteFunc("GET", "/v1/volumes/syncs/vsync-3", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		gets++
		status := "SYNCING"
		payload := volumeSyncPayload("vsync-3", status)
		if gets == 2 {
			payload["status"] = "READY"
			payload["volume_version_id"] = "volver-3"
			payload["version_ref"] = "bdn:weights/model@b3:abc"
			payload["content_digest"] = "b3:abc"
			payload["total_size_bytes"] = 1024
			payload["completed_at"] = "2026-09-08T18:12:00Z"
		}
		_ = json.NewEncoder(w).Encode(payload)
	})

	err := h.Execute("volume", "sync", "start", "--source", "hf://org/model",
		"--destination", "bdn:weights/model:prod", "--wait")
	h.Require.NoError(err)
	h.Require.Equal(2, gets)
	h.Require.Contains(h.Stderr.String(), "Status: PENDING")
	h.Require.Contains(h.Stderr.String(), "Status: SYNCING")
	h.Require.Contains(h.Stderr.String(), "Status: READY")
	h.Require.Contains(h.Stdout.String(), "Version ref: bdn:weights/model@b3:abc")
}

func Test_Volume_Sync_Start_WaitFailsWithFinalJSONOnly(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	h.Context = internalcmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	m.SetRoute("POST", "/v1/volumes/syncs", 200, volumeSyncPayload("vsync-4", "PENDING"))
	payload := volumeSyncPayload("vsync-4", "FAILED")
	payload["completed_at"] = "2026-09-08T18:02:00Z"
	payload["error"] = map[string]any{"code": "SOURCE_AUTH_FAILED", "message": "Source authentication failed"}
	m.SetRoute("GET", "/v1/volumes/syncs/vsync-4", 200, payload)

	err := h.Execute("volume", "sync", "start", "--source", "hf://org/model",
		"--destination", "bdn:weights/model:prod", "--wait", "--output", "json")
	h.Require.ErrorContains(err, "Source authentication failed")
	h.Require.Contains(h.Stdout.String(), `"status": "FAILED"`)
	h.Require.NotContains(h.Stdout.String(), `"exit_code"`)
}

func Test_Volume_Sync_DescribeAndCancel(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/volumes/syncs/vsync-5", 200, volumeSyncPayload("vsync-5", "SYNCING"))
	m.SetRoute("POST", "/v1/volumes/syncs/vsync-5/cancel", 200, volumeSyncPayload("vsync-5", "CANCELED"))

	h.Require.NoError(h.Execute("volume", "sync", "describe", "--volume-sync-id", "vsync-5"))
	h.Require.Contains(h.Stdout.String(), "Status:      SYNCING")
	h.Require.NoError(h.Execute("volume", "sync", "cancel", "--volume-sync-id", "vsync-5"))
	h.Require.Contains(h.Stdout.String(), "Status:      CANCELED")
}

func Test_Volume_Sync_Describe_RejectsOldSyncIDFlag(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "sync", "describe", "--sync-id", "vsync-5")
	h.Require.ErrorContains(err, "unknown flag: --sync-id")
}

func Test_Volume_Sync_ListFollowsPagination(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRouteFunc("GET", "/v1/volumes/syncs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		payload := map[string]any{
			"items":      []any{volumeSyncPayload("vsync-6", "READY")},
			"pagination": map[string]any{"has_more": true, "cursor": "next"},
		}
		if r.URL.Query().Get("cursor") == "next" {
			payload = map[string]any{
				"items":      []any{volumeSyncPayload("vsync-7", "FAILED")},
				"pagination": map[string]any{"has_more": false, "cursor": nil},
			}
		}
		_ = json.NewEncoder(w).Encode(payload)
	})

	err := h.Execute("volume", "sync", "list",
		"--destination", "bdn:weights/model:prod", "--output", "json")
	h.Require.NoError(err)
	h.Require.Contains(h.Stdout.String(), `"sync_id": "vsync-6"`)
	h.Require.Contains(h.Stdout.String(), `"sync_id": "vsync-7"`)
	h.Require.Len(m.Calls(), 2)
	h.Require.Equal("100", m.Calls()[0].Query().Get("limit"))
	h.Require.Equal("bdn:weights/model:prod", m.Calls()[0].Query().Get("ref"))
	h.Require.Equal("next", m.Calls()[1].Query().Get("cursor"))
}
