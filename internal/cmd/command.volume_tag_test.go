package cmd_test

import (
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
)

const (
	volumeTagsPath       = "/v1/volumes/weights/llama/tags"
	volumeTagDeletePath  = volumeTagsPath + "/prod"
	volumeTagRefByDigest = "bdn:weights/llama@b3:aabbccddeeff"
)

// volumeTagSetPayload is a tag response with no tag expiration.
var volumeTagSetPayload = map[string]any{
	"namespace": "weights", "volume": "llama", "tag": "prod", "digest": "b3:aabbccddeeff",
	"version_ref": volumeTagRefByDigest, "volume_sequence": 10, "expires_at": nil,
}

// volumeTagDeletePayload is a tag delete as the management API renders it.
var volumeTagDeletePayload = map[string]any{
	"namespace": "weights", "volume": "llama", "tag": "prod", "deleted": true, "volume_sequence": 11,
}

// volumeTagConflictPayload is the 409 the management API answers when the
// volume changed under the request.
var volumeTagConflictPayload = map[string]any{
	"code": "CONFLICT", "message": "volume changed",
	"details": map[string]any{"reason": "CAS_CONFLICT", "domain": "bdn.baseten.co"},
}

func Test_Volume_Tag_Set_ByDigest(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", volumeTagsPath, 200, volumeTagSetPayload)

	h.Require.NoError(h.Execute("volume", "tag", "set", "--volume-ref", volumeTagRefByDigest, "--tag-name", "prod"))
	// expires_at and expected_sequence are absent unless asked for, and no flag
	// exposes the sequence.
	h.Require.Equal(`{"tag":"prod","version":"@b3:aabbccddeeff"}`,
		strings.TrimSpace(m.FindCall("POST", volumeTagsPath).Body))
	out := h.Stdout.String()
	h.Require.Contains(out, "Ref:             bdn:weights/llama@b3:aabbccddeeff\n")
	h.Require.Contains(out, "Tag:             prod\n")
	h.Require.Contains(out, "Digest:          b3:aabbccddeeff\n")
	h.Require.Contains(out, "Expires:         -\n")
	h.Require.Contains(out, "Volume sequence: 10\n")
}

func Test_Volume_Tag_Set_ByTagSelector(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", volumeTagsPath, 200, volumeTagSetPayload)

	h.Require.NoError(h.Execute("volume", "tag", "set", "--volume-ref", "bdn:weights/llama:canary", "--tag-name", "prod"))
	h.Require.Equal(":canary", m.FindCall("POST", volumeTagsPath).BodyJSON(t)["version"])
}

func Test_Volume_Tag_Set_ByHead(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", volumeTagsPath, 200, volumeTagSetPayload)

	h.Require.NoError(h.Execute("volume", "tag", "set", "--volume-ref", "bdn:weights/llama", "--tag-name", "prod"))
	h.Require.Equal("head", m.FindCall("POST", volumeTagsPath).BodyJSON(t)["version"])
}

func Test_Volume_Tag_Set_HeadAsTag(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", volumeTagsPath, 200, map[string]any{
		"namespace": "weights", "volume": "llama", "tag": "head", "digest": "b3:aabbccddeeff",
		"version_ref": volumeTagRefByDigest, "volume_sequence": 10, "expires_at": nil,
	})

	h.Require.NoError(h.Execute("volume", "tag", "set", "--volume-ref", volumeTagRefByDigest, "--tag-name", "head"))
	h.Require.Equal("head", m.FindCall("POST", volumeTagsPath).BodyJSON(t)["tag"])
	h.Require.Contains(h.Stdout.String(), "Tag:             head\n")
}

func Test_Volume_Tag_Set_ExpiresAt(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", volumeTagsPath, 200, map[string]any{
		"namespace": "weights", "volume": "llama", "tag": "prod", "digest": "b3:aabbccddeeff",
		"version_ref": volumeTagRefByDigest, "volume_sequence": 10, "expires_at": tagExpiryTime,
	})

	h.Require.NoError(h.Execute("volume", "tag", "set", "--volume-ref", volumeTagRefByDigest, "--tag-name", "prod",
		"--expires-at", tagExpiryTime.Format(time.RFC3339)))
	h.Require.Equal(`{"expires_at":"2026-09-09T05:04:05Z","tag":"prod","version":"@b3:aabbccddeeff"}`,
		strings.TrimSpace(m.FindCall("POST", volumeTagsPath).Body))
	h.Require.Contains(h.Stdout.String(), "Expires:         2026-09-09T05:04:05Z\n")
}

func Test_Volume_Tag_Set_PastExpiresAtReachesTheAPI(t *testing.T) {
	// The future bound is the management API's, so a past time is sent and
	// its 400 is the error.
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", volumeTagsPath, 400, map[string]any{
		"code": "VALIDATION_ERROR", "message": "expires_at must be after the request time",
	})

	err := h.Execute("volume", "tag", "set", "--volume-ref", volumeTagRefByDigest, "--tag-name", "prod",
		"--expires-at", "2020-01-01T00:00:00Z")
	h.Require.ErrorContains(err, "expires_at must be after the request time")
	h.Require.Equal(int(cmd.ExitValidation), h.ExitCode)
	h.Require.Len(m.Calls(), 1)
	h.Require.Equal("2020-01-01T00:00:00Z", m.Calls()[0].BodyJSON(t)["expires_at"])
}

func Test_Volume_Tag_Set_BadExpiresAt(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("volume", "tag", "set", "--volume-ref", volumeTagRefByDigest, "--tag-name", "prod",
		"--expires-at", "2030-01-01T00:00:00")
	h.Require.ErrorContains(err, "must be an RFC 3339 date-time")
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
	h.Require.Empty(m.Calls())
}

func Test_Volume_Tag_Set_EmptyExpiresAt(t *testing.T) {
	// A plain string flag would read an empty value as omitted and set a
	// permanent tag.
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("volume", "tag", "set", "--volume-ref", volumeTagRefByDigest, "--tag-name", "prod",
		"--expires-at", "")
	h.Require.ErrorContains(err, "must be an RFC 3339 date-time")
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
	h.Require.Empty(m.Calls())
}

func Test_Volume_Tag_Set_CaseKept(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", volumeTagsPath, 200, map[string]any{
		"namespace": "weights", "volume": "llama", "tag": "Prod", "digest": "b3:aabbccddeeff",
		"version_ref": volumeTagRefByDigest, "volume_sequence": 10, "expires_at": nil,
	})

	h.Require.NoError(h.Execute("volume", "tag", "set", "--volume-ref", volumeTagRefByDigest, "--tag-name", "Prod"))
	h.Require.Equal("Prod", m.FindCall("POST", volumeTagsPath).BodyJSON(t)["tag"])
}

func Test_Volume_Tag_Set_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("POST", volumeTagsPath, 200, volumeTagSetPayload)

	h.Require.NoError(h.Execute("volume", "tag", "set", "--volume-ref", volumeTagRefByDigest, "--tag-name", "prod",
		"--output", "json"))
	out := h.Stdout.String()
	h.Require.Contains(out, `"version_ref": "bdn:weights/llama@b3:aabbccddeeff"`)
	h.Require.Contains(out, `"expires_at": null`)
	h.Require.Contains(out, `"volume_sequence": 10`)
}

func Test_Volume_Tag_Set_ShortensRef(t *testing.T) {
	const digest = "b3:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("POST", volumeTagsPath, 200, map[string]any{
		"namespace": "weights", "volume": "llama", "tag": "prod", "digest": digest,
		"version_ref": "bdn:weights/llama@" + digest, "volume_sequence": 10, "expires_at": nil,
	})

	h.Require.NoError(h.Execute("volume", "tag", "set", "--volume-ref", "bdn:weights/llama@"+digest, "--tag-name", "prod"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Ref:             bdn:weights/llama@0123456789ab\n")
	// The digest a command writes on its own stays whole.
	h.Require.Contains(out, "Digest:          "+digest+"\n")
}

func Test_Volume_Tag_Set_FullRef(t *testing.T) {
	const digest = "b3:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("POST", volumeTagsPath, 200, map[string]any{
		"namespace": "weights", "volume": "llama", "tag": "prod", "digest": digest,
		"version_ref": "bdn:weights/llama@" + digest, "volume_sequence": 10, "expires_at": nil,
	})

	h.Require.NoError(h.Execute("volume", "tag", "set", "--full-ref", "--volume-ref", "bdn:weights/llama@"+digest,
		"--tag-name", "prod"))
	h.Require.Contains(h.Stdout.String(), "Ref:             bdn:weights/llama@"+digest+"\n")
}

func Test_Volume_Tag_Set_Namespace(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("volume", "tag", "set", "--volume-ref", "bdn:weights", "--tag-name", "prod")
	h.Require.ErrorContains(err, "--volume-ref bdn:weights/ names a namespace, and a tag belongs to a volume")
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
	h.Require.Empty(m.Calls())
}

func Test_Volume_Tag_Set_Path(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("volume", "tag", "set", "--volume-ref", "bdn:weights/llama:canary/config", "--tag-name", "prod")
	h.Require.ErrorContains(err, "names a path, and a tag names a whole version")
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
	h.Require.Empty(m.Calls())
}

func Test_Volume_Tag_Set_BadTag(t *testing.T) {
	for _, tag := range []string{
		"a/b", "a/", "/b", "a@b", "a:b", ".hidden", "",
		// Trimmed by the ref parser, so only the comparison against the
		// flag value catches it.
		"prod ",
		strings.Repeat("x", 129),
	} {
		t.Run(tag, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()

			err := h.Execute("volume", "tag", "set", "--volume-ref", volumeTagRefByDigest, "--tag-name", tag)
			h.Require.ErrorContains(err, "--tag-name")
			h.Require.ErrorContains(err, "must begin with a letter, digit, or underscore")
			h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
			h.Require.Empty(m.Calls())
		})
	}
}

func Test_Volume_Tag_Set_Conflict(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", volumeTagsPath, 409, volumeTagConflictPayload)

	err := h.Execute("volume", "tag", "set", "--volume-ref", volumeTagRefByDigest, "--tag-name", "prod",
		"--output", "json")
	h.Require.ErrorContains(err, "volume changed")
	h.Require.Equal(int(cmd.ExitValidation), h.ExitCode)
	// Nothing retries, so the conflict reaches the caller with its code.
	h.Require.Len(m.Calls(), 1)
	jsonErr := decodeJSONErrorEnvelope(h)
	h.Require.Equal(409, jsonErr.APIStatusCode)
	h.Require.Equal("CONFLICT", jsonErr.APIErrorCode)
	h.Require.Equal("CAS_CONFLICT", jsonErr.APIDetails["reason"])
}

func Test_Volume_Tag_Set_MissingTagName(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("volume", "tag", "set", "--volume-ref", volumeTagRefByDigest)
	h.Require.ErrorContains(err, `"tag-name" not set`)
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
	h.Require.Empty(m.Calls())
}

func Test_Volume_Tag_Set_Positional(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("volume", "tag", "set", "--volume-ref", volumeTagRefByDigest, "--tag-name", "prod", "canary")
	h.Require.ErrorContains(err, "unknown command")
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
	h.Require.Empty(m.Calls())
}

func Test_Volume_Tag_Delete_Deleted(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", volumeTagDeletePath, 200, volumeTagDeletePayload)

	// Stdin is a buffer and there is no --yes flag, so a prompt would fail
	// this test.
	h.Require.NoError(h.Execute("volume", "tag", "delete", "--volume-ref", "bdn:weights/llama", "--tag-name", "prod"))
	// The body is empty. No flag sends expected_sequence.
	h.Require.Equal("{}", strings.TrimSpace(m.FindCall("DELETE", volumeTagDeletePath).Body))
	out := h.Stdout.String()
	h.Require.Contains(out, "Tag:             prod\n")
	h.Require.Contains(out, "Deleted:         yes\n")
	h.Require.Contains(out, "Volume sequence: 11\n")
}

func Test_Volume_Tag_Delete_Absent(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("DELETE", volumeTagDeletePath, 200, map[string]any{
		"namespace": "weights", "volume": "llama", "tag": "prod", "deleted": false, "volume_sequence": 11,
	})

	h.Require.NoError(h.Execute("volume", "tag", "delete", "--volume-ref", "bdn:weights/llama", "--tag-name", "prod"))
	h.Require.Equal(0, h.ExitCode)
	h.Require.Contains(h.Stdout.String(), "Deleted:         no\n")
}

func Test_Volume_Tag_Delete_Selector(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("volume", "tag", "delete", "--volume-ref", "bdn:weights/llama:prod", "--tag-name", "prod")
	h.Require.ErrorContains(err, "--volume-ref bdn:weights/llama:prod names a version, and a tag is deleted from the volume")
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
	h.Require.Empty(m.Calls())
}

func Test_Volume_Tag_Delete_Path(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("volume", "tag", "delete", "--volume-ref", "bdn:weights/llama/config", "--tag-name", "prod")
	h.Require.ErrorContains(err, "names a path, and a tag belongs to the whole volume")
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
	h.Require.Empty(m.Calls())
}

func Test_Volume_Tag_Delete_Namespace(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("volume", "tag", "delete", "--volume-ref", "bdn:weights", "--tag-name", "prod")
	h.Require.ErrorContains(err, "names a namespace, and a tag belongs to a volume")
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
	h.Require.Empty(m.Calls())
}

func Test_Volume_Tag_Delete_Head(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", volumeTagsPath+"/head", 200, map[string]any{
		"namespace": "weights", "volume": "llama", "tag": "head", "deleted": true, "volume_sequence": 11,
	})

	h.Require.NoError(h.Execute("volume", "tag", "delete", "--volume-ref", "bdn:weights/llama", "--tag-name", "head"))
	h.Require.NotNil(m.FindCall("DELETE", volumeTagsPath+"/head"))
	h.Require.Contains(h.Stdout.String(), "Deleted:         yes\n")
}

func Test_Volume_Tag_Delete_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("DELETE", volumeTagDeletePath, 200, volumeTagDeletePayload)

	h.Require.NoError(h.Execute("volume", "tag", "delete", "--volume-ref", "bdn:weights/llama", "--tag-name", "prod",
		"--output", "json"))
	out := h.Stdout.String()
	h.Require.Contains(out, `"deleted": true`)
	h.Require.Contains(out, `"volume_sequence": 11`)
}

func Test_Volume_Tag_Delete_Conflict(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", volumeTagDeletePath, 409, volumeTagConflictPayload)

	err := h.Execute("volume", "tag", "delete", "--volume-ref", "bdn:weights/llama", "--tag-name", "prod")
	h.Require.ErrorContains(err, "volume changed")
	h.Require.Equal(int(cmd.ExitValidation), h.ExitCode)
	h.Require.Len(m.Calls(), 1)
}

func Test_Volume_Tag_Delete_BadTag(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("volume", "tag", "delete", "--volume-ref", "bdn:weights/llama", "--tag-name", "a/b")
	h.Require.ErrorContains(err, "must begin with a letter, digit, or underscore")
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
	h.Require.Empty(m.Calls())
}

func Test_Volume_Tag_Delete_NotFound(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("DELETE", volumeTagDeletePath, 404, map[string]any{
		"code": "NOT_FOUND", "message": "volume not found",
	})

	err := h.Execute("volume", "tag", "delete", "--volume-ref", "bdn:weights/llama", "--tag-name", "prod")
	h.Require.ErrorContains(err, "volume not found")
	h.Require.Equal(int(cmd.ExitNotFound), h.ExitCode)
}
