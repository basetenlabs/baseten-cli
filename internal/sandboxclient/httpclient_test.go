package sandboxclient

import (
	"net/http"
	"testing"
)

func TestDefaultHTTPClientClonesPlainTransport(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })

	http.DefaultTransport = &http.Transport{}
	client := newDefaultHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == http.DefaultTransport {
		t.Fatal("the default transport must be cloned, not used as-is")
	}
	if transport.ResponseHeaderTimeout != responseHeaderTimeout {
		t.Errorf("response header timeout %v", transport.ResponseHeaderTimeout)
	}

	wrapped := struct{ http.RoundTripper }{http.DefaultTransport}
	http.DefaultTransport = wrapped
	client = newDefaultHTTPClient()
	if client != http.DefaultClient {
		t.Error("a wrapped default transport must be preserved by using DefaultClient as-is")
	}
}
