package sandboxconnect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// terminalTestServer answers the terminal protocol: it echoes each input
// message back as output, and closes the connection once the given number of
// inputs arrived.
func terminalTestServer(t *testing.T, inputsBeforeClose int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		seen := 0
		for {
			_, payload, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var message Message
			if err := json.Unmarshal(payload, &message); err != nil {
				continue
			}
			if message.Type != "input" {
				continue
			}
			seen++
			echo, _ := json.Marshal(Message{Type: "output", Data: message.Data})
			if err := conn.Write(r.Context(), websocket.MessageText, echo); err != nil {
				return
			}
			if seen >= inputsBeforeClose {
				return // the deferred Close ends the session
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestWebSocketURLCarriesTokenAndPath(t *testing.T) {
	wsURL, err := WebSocketURL("https://sbx-1.invalid/", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if wsURL != "wss://sbx-1.invalid/terminal/ws?token=tok" {
		t.Errorf("url %q", wsURL)
	}
}

func TestTerminalInputRoundTripAndClose(t *testing.T) {
	server := terminalTestServer(t, 1)
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdinRead.Close(); stdinWrite.Close() })
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdoutRead.Close(); stdoutWrite.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	terminal, err := Dial(ctx, wsURL, stdinRead, stdoutWrite)
	if err != nil {
		t.Fatal(err)
	}

	// The dial carries its own session id, so the server saw one.
	if terminal == nil {
		t.Fatal("no terminal")
	}

	go terminal.inputLoop(ctx)
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		terminal.readLoop(ctx)
	}()

	if _, err := stdinWrite.Write([]byte("echo hi\n")); err != nil {
		t.Fatal(err)
	}
	echoed := make([]byte, 8)
	// A bounded read rather than ReadAll: a missing echo must fail the test,
	// not hang it.
	readDeadline := time.After(5 * time.Second)
	for read := 0; read < len(echoed); {
		n, err := stdoutRead.Read(echoed[read:])
		if err != nil {
			t.Fatalf("reading echoed output after %d bytes: %v", read, err)
		}
		read += n
		select {
		case <-readDeadline:
			t.Fatalf("echo incomplete after %d bytes: %q", read, echoed[:read])
		default:
		}
	}
	if string(echoed) != "echo hi\n" {
		t.Errorf("echoed %q", echoed)
	}

	// The server closed after one input; readLoop must end, not hang.
	select {
	case <-outputDone:
	case <-time.After(5 * time.Second):
		t.Fatal("readLoop did not return after the server closed")
	}
}
