package sandboxconnect

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	var errorBuffer = &bytes.Buffer{}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	terminal, err := Dial(ctx, wsURL, stdinRead, stdoutWrite, errorBuffer)
	if err != nil {
		t.Fatal(err)
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
	// not hang it. The deadline bounds the blocking read itself.
	if err := stdoutRead.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
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

func TestInputSurvivesSplitMultiByteCharacter(t *testing.T) {
	// Two inputs: the held-back rune completes on the second read, so the
	// paste arrives as two messages.
	server := terminalTestServer(t, 2)
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
	if err := stdoutRead.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var errorBuffer = &bytes.Buffer{}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	terminal, err := Dial(ctx, wsURL, stdinRead, stdoutWrite, errorBuffer)
	if err != nil {
		t.Fatal(err)
	}

	go terminal.inputLoop(ctx)
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		terminal.readLoop(ctx)
	}()

	// A paste can split a multi-byte character at the read boundary; the
	// input loop must hold the fragment back, not corrupt it. The first
	// chunk ends inside the two-byte ö.
	input := "hällö\n"
	if _, err := stdinWrite.Write([]byte(input[:6])); err != nil {
		t.Fatal(err)
	}
	if _, err := stdinWrite.Write([]byte(input[6:])); err != nil {
		t.Fatal(err)
	}

	echoed := make([]byte, len(input))
	if _, err := io.ReadFull(stdoutRead, echoed); err != nil {
		t.Fatalf("reading echoed output: %v", err)
	}
	if string(echoed) != input {
		t.Errorf("echoed %q, want %q", echoed, input)
	}
}
