package sandboxclient

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// recordingDoer stands in for the inner client, answering each request with
// the given statuses and recording the Authorization header seen.
type recordingDoer struct {
	authorizations []string
	statuses       []int
}

func (d *recordingDoer) Do(req *http.Request) (*http.Response, error) {
	d.authorizations = append(d.authorizations, req.Header.Get("Authorization"))
	status := d.statuses[len(d.authorizations)-1]
	header := http.Header{}
	// Set, not a map literal: only Set canonicalizes the key, and Header.Get
	// reads the canonical form, the same form a real server sends.
	header.Set("x-blaxel-error-code", "TOKEN_REVOKED")
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Header:     header,
	}, nil
}

func TestTokenAuthClientRetriesBodylessRequests(t *testing.T) {
	doer := &recordingDoer{statuses: []int{401, 200}}
	tokens := &tokenSource{provider: func(context.Context) (string, error) { return "tok", nil }}
	client := &tokenAuthClient{inner: doer, tokens: tokens}

	request, err := http.NewRequestWithContext(context.Background(), "GET", "https://sbx.invalid/process", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Errorf("the revoked 401 must be retried even without a body, got %d", response.StatusCode)
	}
	if len(doer.authorizations) != 2 {
		t.Errorf("expected a re-send, got %d requests", len(doer.authorizations))
	}
}

func TestTokenAuthClientReturnsRevokedForStreamingBody(t *testing.T) {
	doer := &recordingDoer{statuses: []int{401, 200}}
	tokens := &tokenSource{provider: func(context.Context) (string, error) { return "tok", nil }}
	client := &tokenAuthClient{inner: doer, tokens: tokens}

	// A body without GetBody is a streaming raw call: readable once, so it
	// cannot be re-sent.
	request, err := http.NewRequestWithContext(
		context.Background(), "POST", "https://sbx.invalid/process",
		io.NopCloser(strings.NewReader("{}")))
	if err != nil {
		t.Fatal(err)
	}
	request.GetBody = nil
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 401 {
		t.Errorf("a non-replayable body must return the revoked response, got %d", response.StatusCode)
	}
	if len(doer.authorizations) != 1 {
		t.Errorf("a non-replayable body must not be re-sent, got %d requests", len(doer.authorizations))
	}
}

func TestTokenAuthClientDropsStaleHeaderWhenProviderEmpties(t *testing.T) {
	returned := "tok"
	doer := &recordingDoer{statuses: []int{401, 401}}
	tokens := &tokenSource{provider: func(context.Context) (string, error) {
		token := returned
		returned = ""
		return token, nil
	}}
	client := &tokenAuthClient{inner: doer, tokens: tokens}

	request, err := http.NewRequestWithContext(context.Background(), "GET", "https://sbx.invalid/process", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(request); err != nil {
		t.Fatal(err)
	}
	if got := doer.authorizations[1]; got != "" {
		t.Errorf("the re-send must carry no Authorization once the provider empties, got %q", got)
	}
}
