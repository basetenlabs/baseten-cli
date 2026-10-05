package sandboxconnect

import (
	"bytes"
	"context"
	"encoding/json"
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

// terminalTestServer answers the terminal protocol: it echoes each input
// message back as output, and closes the session once the given number of
// inputs arrived. It records the query of the first connection and every
// message received.
type terminalTestServer struct {
	*httptest.Server
	mu       sync.Mutex
	query    url.Values
	messages []Message
}

func newTerminalTestServer(t *testing.T, inputsBeforeClose int) *terminalTestServer {
	t.Helper()
	s := &terminalTestServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.query = r.URL.Query()
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
				return
			}
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// runTerminal dials server and runs the session in the background, feeding
// it input, returning what it wrote and a channel with Run's result.
func runTerminal(t *testing.T, server *terminalTestServer, input io.Reader) (*bytes.Buffer, *Terminal, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	var output bytes.Buffer
	terminal, err := Dial(ctx, DialOptions{SandboxURL: server.URL + "/", Token: "tok", Input: input, Output: &output, Errors: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- terminal.Run(ctx) }()
	return &output, terminal, done
}

func TestTerminal_RoundTripAndClose(t *testing.T) {
	server := newTerminalTestServer(t, 1)
	input, inputWriter := io.Pipe()
	t.Cleanup(func() { inputWriter.Close() })
	output, _, done := runTerminal(t, server, input)

	if _, err := inputWriter.Write([]byte("echo hi\n")); err != nil {
		t.Fatal(err)
	}
	// The server closes the session after one input, which ends Run cleanly.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after the server closed the session")
	}
	if output.String() != "echo hi\n" {
		t.Errorf("output %q", output.String())
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.query.Get("token") != "tok" || server.query.Get("cols") != "80" || server.query.Get("rows") != "24" || len(server.query.Get("sessionId")) != 16 {
		t.Errorf("query %v", server.query)
	}
}

func TestTerminal_SplitMultiByteCharacter(t *testing.T) {
	// Two inputs: the held-back character completes on the second read.
	server := newTerminalTestServer(t, 2)
	input, inputWriter := io.Pipe()
	t.Cleanup(func() { inputWriter.Close() })
	output, _, done := runTerminal(t, server, input)

	// The first write ends inside the two-byte ö.
	text := "hällö\n"
	if _, err := inputWriter.Write([]byte(text[:6])); err != nil {
		t.Fatal(err)
	}
	if _, err := inputWriter.Write([]byte(text[6:])); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return")
	}
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
	_, terminal, done := runTerminal(t, server, input)

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
	_, err := Dial(t.Context(), DialOptions{SandboxURL: server.URL, Token: "secret-token"})
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error %v", err)
	}
}
