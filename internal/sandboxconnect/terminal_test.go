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

// waitForString blocks until buf contains substr occurrences of substr,
// failing the test otherwise: waits for a repeated event must not match the
// earlier one.
func waitForString(t *testing.T, buf *lockedBuffer, substr string, occurrences int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(buf.String(), substr) >= occurrences {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("buffer never contained %d of %q: %q", occurrences, substr, buf.String())
}

// terminalTestServer answers the terminal protocol: it echoes each input
// message back as output, and closes the session once the given number of
// inputs arrived. With abruptClosesRemaining above zero, that many first
// closes drop the TCP connection without a close frame, as a hibernating
// sandbox does. A connection with the rejectToken is refused with 401. It
// records the query of every connection and every message received.
type terminalTestServer struct {
	*httptest.Server
	mu                    sync.Mutex
	queries               []url.Values
	messages              []message
	abruptClosesRemaining int
	rejectToken           string
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
		reject := s.rejectToken
		s.mu.Unlock()
		if reject != "" && r.URL.Query().Get("token") == reject {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
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
			var frame message
			if err := json.Unmarshal(payload, &frame); err != nil {
				continue
			}
			s.mu.Lock()
			s.messages = append(s.messages, frame)
			s.mu.Unlock()
			if frame.Type != "input" {
				continue
			}
			echo, _ := json.Marshal(message{Type: "output", Data: frame.Data})
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

// mintCountingTestToken counts mints, failing the failAt-th, so tests can
// assert how many mints a session needs.
func mintCountingTestToken(mints *int, failAt int) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		*mints++
		if *mints == failAt {
			return "", fmt.Errorf("rate limited")
		}
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
	for _, frame := range server.messages {
		if !strings.HasPrefix(text, frame.Data) && !strings.HasSuffix(text, frame.Data) {
			t.Errorf("input split mid-character: %q", frame.Data)
		}
	}
}

func TestTerminal_Resize(t *testing.T) {
	server := newTerminalTestServer(t, 1)
	input, inputWriter := io.Pipe()
	t.Cleanup(func() { inputWriter.Close() })
	_, terminal, done := runTerminal(t, server, input, &lockedBuffer{}, nil)

	if err := terminal.resize(120, 40); err != nil {
		t.Fatal(err)
	}
	// Input after the resize, so the server has both once the session ends.
	if _, err := inputWriter.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	<-done
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.messages) < 1 || server.messages[0] != (message{Type: "resize", Cols: 120, Rows: 40}) {
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

// hungTestServer accepts the connection but never answers the upgrade: a
// gateway holding the handshake while the sandbox resumes.
func hungTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	return server
}

func TestTerminal_DialIsBoundedAgainstAHungUpgrade(t *testing.T) {
	server := hungTestServer(t)
	terminal := &Terminal{opts: DialOptions{SandboxURL: server.URL, NewToken: staticTestToken("tok")}}

	started := time.Now()
	if err := terminal.connect(t.Context(), "tok"); err == nil {
		t.Fatal("connect against a hung upgrade succeeded")
	}
	if elapsed := time.Since(started); elapsed > dialTimeout+2*time.Second {
		t.Errorf("connect took %s, want bounded by %s", elapsed, dialTimeout)
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
	output, _, done := runTerminal(t, server, input, errors, mintCountingTestToken(&mints, 0))

	if _, err := inputWriter.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Connection lost", 1)

	// The keystroke wakes the sandbox; it is not shell input.
	if _, err := inputWriter.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Reconnected", 1)
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

func TestTerminal_SecondWakeInTheSameSession(t *testing.T) {
	// Both connections drop abruptly; each wake reattaches with the same
	// session ID.
	server := newTerminalTestServer(t, 1)
	server.abruptClosesRemaining = 2
	input, inputWriter := io.Pipe()
	t.Cleanup(func() { inputWriter.Close() })
	errors := &lockedBuffer{}
	mints := 0
	output, _, done := runTerminal(t, server, input, errors, mintCountingTestToken(&mints, 0))

	if _, err := inputWriter.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Connection lost", 1)
	if _, err := inputWriter.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Reconnected", 1)
	if _, err := inputWriter.Write([]byte("c")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Connection lost", 2)
	if _, err := inputWriter.Write([]byte("d")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Reconnected", 2)
	if _, err := inputWriter.Write([]byte("e")); err != nil {
		t.Fatal(err)
	}
	if err := runResult(t, done); err != nil {
		t.Fatalf("run: %v", err)
	}
	if output.String() != "ace" {
		t.Errorf("output %q, want %q", output.String(), "ace")
	}
	if mints != 3 {
		t.Errorf("token mints %d, want 3: the initial connect and two wakes", mints)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.queries) != 3 {
		t.Fatalf("connections %d, want 3", len(server.queries))
	}
	for _, query := range server.queries[1:] {
		if query.Get("sessionId") != server.queries[0].Get("sessionId") {
			t.Errorf("wakes reused session: %q then %q",
				server.queries[0].Get("sessionId"), query.Get("sessionId"))
		}
	}
}

func TestTerminal_RemintsARejectedToken(t *testing.T) {
	// The wake mints a token the sandbox API rejects; the re-mint reconnects
	// without a second keystroke.
	server := newTerminalTestServer(t, 1)
	server.abruptClosesRemaining = 1
	server.rejectToken = "stale"
	input, inputWriter := io.Pipe()
	t.Cleanup(func() { inputWriter.Close() })
	errors := &lockedBuffer{}
	mints := 0
	tokens := []string{"tok", "stale", "tok"}
	newToken := func(context.Context) (string, error) {
		token := tokens[min(mints, len(tokens)-1)]
		mints++
		return token, nil
	}
	_, _, done := runTerminal(t, server, input, errors, newToken)

	if _, err := inputWriter.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Connection lost", 1)

	if _, err := inputWriter.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Reconnected", 1)
	if _, err := inputWriter.Write([]byte("c")); err != nil {
		t.Fatal(err)
	}
	if err := runResult(t, done); err != nil {
		t.Fatalf("run: %v", err)
	}
	if mints != 3 {
		t.Errorf("token mints %d, want 3: connect, the rejected wake token, the re-mint", mints)
	}
}

func TestTerminal_EndedInputDuringTheWakeWait(t *testing.T) {
	// Ended input quits the wait for a keystroke instead of hanging it.
	server := newTerminalTestServer(t, 1)
	server.abruptClosesRemaining = 1
	input, inputWriter := io.Pipe()
	errors := &lockedBuffer{}
	_, _, done := runTerminal(t, server, input, errors, nil)

	if _, err := inputWriter.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Connection lost", 1)

	if err := inputWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runResult(t, done); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestTerminal_QuitKeystrokeAfterTransportLoss(t *testing.T) {
	// In raw mode both quit keys arrive as bytes, and after the loss they
	// quit instead of reaching the dead shell.
	for _, quitKey := range []struct {
		name string
		byte byte
	}{
		{"Ctrl+C", 0x03},
		{"Ctrl+D", 0x04},
	} {
		t.Run(quitKey.name, func(t *testing.T) {
			server := newTerminalTestServer(t, 1)
			server.abruptClosesRemaining = 1
			input, inputWriter := io.Pipe()
			t.Cleanup(func() { inputWriter.Close() })
			errors := &lockedBuffer{}
			_, _, done := runTerminal(t, server, input, errors, nil)

			if _, err := inputWriter.Write([]byte("a")); err != nil {
				t.Fatal(err)
			}
			waitForString(t, errors, "Connection lost", 1)

			if _, err := inputWriter.Write([]byte{quitKey.byte}); err != nil {
				t.Fatal(err)
			}
			if err := runResult(t, done); err != nil {
				t.Fatalf("run: %v", err)
			}
			server.mu.Lock()
			defer server.mu.Unlock()
			if len(server.queries) != 1 {
				t.Errorf("connections %d, want 1: the quit key must not reconnect", len(server.queries))
			}
		})
	}
}

func TestTerminal_RetryAfterFailedReconnect(t *testing.T) {
	server := newTerminalTestServer(t, 1)
	server.abruptClosesRemaining = 1
	input, inputWriter := io.Pipe()
	t.Cleanup(func() { inputWriter.Close() })
	errors := &lockedBuffer{}
	mints := 0
	output, _, done := runTerminal(t, server, input, errors, mintCountingTestToken(&mints, 2))

	if _, err := inputWriter.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Connection lost", 1)

	if _, err := inputWriter.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Reconnect failed", 1)

	// The keystroke after the failure reconnects; without restarting the
	// input loop it is never read and the session hangs. Wake keystrokes are
	// triggers, not shell input.
	if _, err := inputWriter.Write([]byte("c")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Reconnected", 1)
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

func TestTerminal_PastedWakeKeystrokeReachesTheShell(t *testing.T) {
	// One read can carry several keystrokes, as a paste does: the first
	// rune triggers the wake, the rest reach the shell once it resumed.
	server := newTerminalTestServer(t, 1)
	server.abruptClosesRemaining = 1
	input, inputWriter := io.Pipe()
	t.Cleanup(func() { inputWriter.Close() })
	errors := &lockedBuffer{}
	mints := 0
	output, _, done := runTerminal(t, server, input, errors, mintCountingTestToken(&mints, 0))

	if _, err := inputWriter.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Connection lost", 1)

	if _, err := inputWriter.Write([]byte("bc")); err != nil {
		t.Fatal(err)
	}
	waitForString(t, errors, "Reconnected", 1)
	if err := runResult(t, done); err != nil {
		t.Fatalf("run: %v", err)
	}
	// "b" is the consumed trigger, "c" the shell input that ends the session.
	if output.String() != "ac" {
		t.Errorf("output %q, want %q", output.String(), "ac")
	}
}
