package sandboxconnect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// lockedBuffer is a bytes.Buffer safe to write from the session's goroutines
// while a test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForString blocks until buf contains substr, failing the test otherwise.
func waitForString(t *testing.T, buf *lockedBuffer, substr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), substr) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("buffer never contained %q: %q", substr, buf.String())
}

// terminalTestServer answers the terminal protocol: it echoes each input
// message back as output, and closes the session once the given number of
// inputs arrived. With abruptClosesRemaining above zero, that many first
// closes drop the TCP connection without a close frame, as a hibernating
// sandbox does. It records the query of every connection and every message
// received.
type terminalTestServer struct {
	*httptest.Server
	mu                    sync.Mutex
	queries               []url.Values
	messages              []Message
	abruptClosesRemaining int
}

func newTerminalTestServer(t *testing.T, inputsBeforeClose int) *terminalTestServer {
	t.Helper()
	s := &terminalTestServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.queries = append(s.queries, r.URL.Query())
		abrupt := s.abruptClosesRemaining > 0
		if abrupt {
			s.abruptClosesRemaining--
		}
		s.mu.Unlock()
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		inputs := 0
		for {
			_, payload, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var message Message
			if err := json.Unmarshal(payload, &message); err != nil {
				continue
			}
			s.mu.Lock()
			s.messages = append(s.messages, message)
			s.mu.Unlock()
			if message.Type != "input" {
				continue
			}
			echo, _ := json.Marshal(Message{Type: "output", Data: message.Data})
			if err := conn.Write(r.Context(), websocket.MessageText, echo); err != nil {
				return
			}
			if inputs++; inputs >= inputsBeforeClose {
				if abrupt {
					conn.CloseNow()
				}
				return
			}
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// staticTestToken is a NewToken that always returns the same token.
func staticTestToken(token string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return token, nil }
}

// countingTestToken wraps a NewToken, counting the mints: the initial connect
// and one per wake. A token per dial attempt would trip Baseten's rate limit.
func countingTestToken(mints *int) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		*mints++
		return "tok", nil
	}
}

// runTerminal dials server and runs the session in the background, feeding
// it input, returning what it wrote and a channel with Run's result.
func runTerminal(
	t *testing.T, server *terminalTestServer, input io.Reader, errors *lockedBuffer,
	newToken func(context.Context) (string, error),
) (*lockedBuffer, *Terminal, <-chan error) {
	t.Helper()
	if newToken == nil {
		newToken = staticTestToken("tok")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	var output lockedBuffer
	terminal, err := Dial(ctx, DialOptions{
		SandboxURL: server.URL + "/",
		NewToken:   newToken,
		Input:      input,
		Output:     &output,
		Errors:     errors,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- terminal.Run(ctx) }()
	return &output, terminal, done
}

// runResult returns Run's result, failing the test if it does not end.
func runResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return")
		return nil
	}
}

func TestTerminal_RoundTripAndClose(t *testing.T) {
	server := newTerminalTestServer(t, 1)
	input, inputWriter := io.Pipe()
	t.Cleanup(func() { inputWriter.Close() })
	output, _, done := runTerminal(t, server, input, &lockedBuffer{}, nil)

	if _, err := inputWriter.Write([]byte("echo hi\n")); err != nil {
		t.Fatal(err)
	}
	// The server closes the session after one input, which ends Run cleanly.
	if err := runResult(t, done); err != nil {
		t.Fatalf("run: %v", err)
	}
	if output.String() != "echo hi\n" {
		t.Errorf("output %q", output.String())
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.queries[0].Get("token") != "tok" || server.queries[0].Get("cols") != "80" || server.queries[0].Get("rows") != "24" || len(server.queries[0].Get("sessionId")) != 16 {
		t.Errorf("query %v", server.queries[0])
	}
}

func TestTerminal_SplitMultiByteCharacter(t *testing.T) {
	// Two inputs: the held-back character completes on the second read.
	server := newTerminalTestServer(t, 2)
	input, inputWriter := io.Pipe()
	t.Cleanup(func() { inputWriter.Close() })
	output, _, done := runTerminal(t, server, input, &lockedBuffer{}, nil)

	// The first write ends inside the two-byte ö.
	text := "hällö\n"
	if _, err := inputWriter.Write([]byte(text[:6])); err != nil {
		t.Fatal(err)
	}
	if _, err := inputWriter.Write([]byte(text[6:])); err != nil {
		t.Fatal(err)
	}
	runResult(t, done)
	if output.String() != text {
		t.Errorf("output %q, want %q", output.String(), text)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	for _, message := range server.messages {
		if !strings.HasPrefix(text, message.Data) && !strings.HasSuffix(text, message.Data) {
			t.Errorf("input split mid-character: %q", message.Data)
		}
	}
}

func TestTerminal_Resize(t *testing.T) {
	server := newTerminalTestServer(t, 1)
	input, inputWriter := io.Pipe()
	t.Cleanup(func() { inputWriter.Close() })
	_, terminal, done := runTerminal(t, server, input, &lockedBuffer{}, nil)

	if err := terminal.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	// Input after the resize, so the server has both once the session ends.
	if _, err := inputWriter.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	<-done
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.messages) < 1 || server.messages[0] != (Message{Type: "resize", Cols: 120, Rows: 40}) {
		t.Errorf("messages %+v", server.messages)
	}
}

func TestTerminal_DialFailureRedactsToken(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	_, err := Dial(t.Context(), DialOptions{SandboxURL: server.URL, NewToken: staticTestToken("secret-token")})
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error %v", err)
	}
}

func TestTerminal_ReconnectAfterTransportLoss(t *testing.T) {
	// The first connection dies without a close frame, as a hibernating
	// sandbox kills it. The next keystroke reconnects, reattaching to the
	// same session, and the second connection closes normally.
	server := newTerminalTestServer(t, 1)
	server.abruptClosesRemaining = 1
	input, inputWriter := io.Pipe()
	t.Cleanup(func() { inputWriter.Close() })
	errors := &lockedBuffer{}
	mints := 0
	output, _, done := runTerminal(t, server, input, errors, countingTestToken(&mints))

	if _, err := inputWriter.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Connection lost")

	// The keystroke wakes the sandbox; it is not shell input.
	if _, err := inputWriter.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Reconnected")
	if _, err := inputWriter.Write([]byte("c")); err != nil {
		t.Fatal(err)
	}
	if err := runResult(t, done); err != nil {
		t.Fatalf("run: %v", err)
	}
	if output.String() != "ac" {
		t.Errorf("output %q, want %q", output.String(), "ac")
	}
	if mints != 2 {
		t.Errorf("token mints %d, want 2: the initial connect and the one wake", mints)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.queries) != 2 {
		t.Fatalf("connections %d, want 2", len(server.queries))
	}
	if server.queries[0].Get("sessionId") != server.queries[1].Get("sessionId") {
		t.Errorf("reconnect reused session: %q then %q",
			server.queries[0].Get("sessionId"), server.queries[1].Get("sessionId"))
	}
}

func TestTerminal_QuitKeystrokeAfterTransportLoss(t *testing.T) {
	server := newTerminalTestServer(t, 1)
	server.abruptClosesRemaining = 1
	input, inputWriter := io.Pipe()
	t.Cleanup(func() { inputWriter.Close() })
	errors := &lockedBuffer{}
	_, _, done := runTerminal(t, server, input, errors, nil)

	if _, err := inputWriter.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Connection lost")

	// In raw mode Ctrl+D arrives as a byte, and after the loss it quits
	// instead of reaching the dead shell.
	if _, err := inputWriter.Write([]byte{0x04}); err != nil {
		t.Fatal(err)
	}
	if err := runResult(t, done); err != nil {
		t.Fatalf("run: %v", err)
	}
}

// failingWakeTestToken mints, fails on the first wake, then mints again: the
// retry keystroke after a failed reconnect must still be read.
func failingWakeTestToken(mints *int) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		*mints++
		if *mints == 2 {
			return "", fmt.Errorf("rate limited")
		}
		return "tok", nil
	}
}

func TestTerminal_RetryAfterFailedReconnect(t *testing.T) {
	server := newTerminalTestServer(t, 1)
	server.abruptClosesRemaining = 1
	input, inputWriter := io.Pipe()
	t.Cleanup(func() { inputWriter.Close() })
	errors := &lockedBuffer{}
	mints := 0
	output, _, done := runTerminal(t, server, input, errors, failingWakeTestToken(&mints))

	if _, err := inputWriter.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Connection lost")

	if _, err := inputWriter.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Reconnect failed")

	// The keystroke after the failure reconnects; without restarting the
	// input loop it is never read and the session hangs. Wake keystrokes are
	// triggers, not shell input.
	if _, err := inputWriter.Write([]byte("c")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Reconnected")
	if _, err := inputWriter.Write([]byte("d")); err != nil {
		t.Fatal(err)
	}
	if err := runResult(t, done); err != nil {
		t.Fatalf("run: %v", err)
	}
	if output.String() != "ad" {
		t.Errorf("output %q, want %q", output.String(), "ad")
	}
	if mints != 3 {
		t.Errorf("token mints %d, want 3", mints)
	}
}
