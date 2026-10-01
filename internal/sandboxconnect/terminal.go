// Package sandboxconnect opens an interactive terminal to a sandbox over the
// WebSocket its execution API serves. The protocol is the one the sandbox's
// built-in image established: JSON messages of type input, output, resize,
// and error, with the terminal locally in raw mode.
package sandboxconnect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"golang.org/x/term"
)

// Message is one frame of the terminal protocol.
type Message struct {
	Type string `json:"type"` // "input", "output", "resize", "error"
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// Terminal is one connected terminal session. Run blocks until the session
// ends: the server closes the connection, the user presses Ctrl+D, or the
// context is canceled.
type Terminal struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	stdin   *os.File
	stdout  *os.File
	oldMode *term.State
	// done is closed once the session has fully ended and the terminal
	// restored.
	done chan struct{}
}

// WebSocketURL turns a sandbox's execution URL into the terminal WebSocket
// URL, with the sandbox token as the query parameter the terminal endpoint
// reads.
func WebSocketURL(sandboxURL, token string) (string, error) {
	parsed, err := url.Parse(sandboxURL)
	if err != nil {
		return "", fmt.Errorf("invalid sandbox URL: %w", err)
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/terminal/ws"
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// Dial connects the terminal. Each dial is its own session: the server's
// default session is shared, so reusing it would rejoin a shell an earlier
// connect may have left. The window size rides the URL, the same place the
// reference client puts it.
func Dial(ctx context.Context, wsURL string, stdin, stdout *os.File) (*Terminal, error) {
	parsed, err := url.Parse(wsURL)
	if err != nil {
		return nil, err
	}
	cols, rows, sizeErr := term.GetSize(int(stdout.Fd()))
	if sizeErr != nil {
		cols, rows = 80, 24
	}
	session := make([]byte, 8)
	if _, err := rand.Read(session); err != nil {
		return nil, err
	}
	query := parsed.Query()
	query.Set("cols", strconv.Itoa(cols))
	query.Set("rows", strconv.Itoa(rows))
	query.Set("sessionId", hex.EncodeToString(session))
	parsed.RawQuery = query.Encode()

	conn, _, err := websocket.Dial(ctx, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("connecting terminal: %w", err)
	}
	return &Terminal{
		conn: conn, stdin: stdin, stdout: stdout,
		done: make(chan struct{}),
	}, nil
}

// Run puts the terminal in raw mode and ferries bytes until the session
// ends. Ctrl+D reaches the sandbox's shell through raw mode and ends it.
func (t *Terminal) Run(ctx context.Context) error {
	oldMode, err := term.MakeRaw(int(t.stdin.Fd()))
	if err != nil {
		t.conn.Close(websocket.StatusInternalError, "")
		return fmt.Errorf("setting terminal raw mode: %w", err)
	}
	t.oldMode = oldMode
	defer t.restore()

	// Canceling the context ends both loops and the connection.
	context.AfterFunc(ctx, func() {
		t.conn.Close(websocket.StatusNormalClosure, "canceled")
	})
	t.watchResize(ctx)

	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		t.readLoop(ctx)
	}()
	go t.inputLoop(ctx)
	<-outputDone
	t.restore()
	close(t.done)
	return nil
}

func (t *Terminal) send(message Message) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return t.conn.Write(context.Background(), websocket.MessageText, payload)
}

func (t *Terminal) readLoop(ctx context.Context) {
	for {
		_, payload, err := t.conn.Read(ctx)
		if err != nil {
			// The server closed the session: the ordinary exit path.
			return
		}
		var message Message
		if err := json.Unmarshal(payload, &message); err != nil {
			continue
		}
		switch message.Type {
		case "output":
			_, _ = t.stdout.WriteString(message.Data)
		case "error":
			_, _ = os.Stderr.WriteString(message.Data + "\n")
		}
	}
}

func (t *Terminal) inputLoop(ctx context.Context) {
	buffer := make([]byte, 512)
	for {
		read, err := t.stdin.Read(buffer)
		if err != nil {
			return
		}
		if err := t.send(Message{Type: "input", Data: string(buffer[:read])}); err != nil {
			return
		}
	}
}

func (t *Terminal) restore() {
	if t.oldMode == nil {
		return
	}
	_ = term.Restore(int(t.stdin.Fd()), t.oldMode)
	t.oldMode = nil
}
