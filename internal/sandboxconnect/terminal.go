// Package sandboxconnect opens an interactive terminal to a sandbox over the
// WebSocket its execution API serves at /terminal/ws. That endpoint is not in
// the API spec: it takes JSON messages of type input, output, resize, and
// error, with the token, window size, and session ID in the URL.
package sandboxconnect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/coder/websocket"
	"golang.org/x/term"
)

// tokenInQuery matches the token query value wherever a dial failure echoes
// the URL, so the sandbox token never reaches an error message.
var tokenInQuery = regexp.MustCompile(`token=[^&\s]+`)

// Message is one frame of the terminal protocol.
type Message struct {
	Type string `json:"type"` // "input", "output", "resize", "error"
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// TerminalControl controls the local terminal a session runs in.
type TerminalControl interface {
	// Size reports the window size.
	Size() (cols, rows int, err error)

	// MakeRaw puts the terminal in raw mode, returning how to restore it.
	MakeRaw() (restore func() error, err error)

	// NotifyResize calls resized whenever the window size changes, until ctx
	// is done.
	NotifyResize(ctx context.Context, resized func())
}

// osTerminal is a [TerminalControl] over a process's terminal. Its
// NotifyResize is per platform.
type osTerminal struct {
	input  *os.File
	output *os.File
}

// NewOSTerminal returns a [TerminalControl] for a terminal read from input
// and written to output, such as the process's stdin and stdout.
func NewOSTerminal(input, output *os.File) TerminalControl {
	return &osTerminal{input: input, output: output}
}

func (t *osTerminal) Size() (cols, rows int, err error) {
	return term.GetSize(int(t.output.Fd()))
}

func (t *osTerminal) MakeRaw() (restore func() error, err error) {
	state, err := term.MakeRaw(int(t.input.Fd()))
	if err != nil {
		return nil, err
	}
	return func() error { return term.Restore(int(t.input.Fd()), state) }, nil
}

// DialOptions are the options for [Dial].
type DialOptions struct {
	// SandboxURL is the sandbox's URL.
	SandboxURL string

	// Token is a sandbox token.
	Token string

	// Input is read and sent to the sandbox's shell.
	Input io.Reader

	// Output receives the shell's output.
	Output io.Writer

	// Errors receives the terminal errors the sandbox reports.
	Errors io.Writer

	// Terminal is the local terminal, or nil for none: an 80 by 24 window,
	// with no raw mode and no resizing.
	Terminal TerminalControl
}

// Terminal is one connected terminal session.
type Terminal struct {
	conn     *websocket.Conn
	writeMu  sync.Mutex
	input    io.Reader
	output   io.Writer
	errors   io.Writer
	terminal TerminalControl
}

// Dial connects a new terminal session. Each session is a new shell: the
// endpoint's default session is shared, so a random session ID keeps a
// connect from rejoining a shell an earlier one left.
func Dial(ctx context.Context, opts DialOptions) (*Terminal, error) {
	parsed, err := url.Parse(opts.SandboxURL)
	if err != nil {
		return nil, fmt.Errorf("invalid sandbox URL: %w", err)
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/terminal/ws"
	cols, rows := 80, 24
	if opts.Terminal != nil {
		if c, r, err := opts.Terminal.Size(); err == nil {
			cols, rows = c, r
		}
	}
	session := make([]byte, 8)
	if _, err := rand.Read(session); err != nil {
		return nil, err
	}
	query := parsed.Query()
	query.Set("token", opts.Token)
	query.Set("cols", strconv.Itoa(cols))
	query.Set("rows", strconv.Itoa(rows))
	query.Set("sessionId", hex.EncodeToString(session))
	parsed.RawQuery = query.Encode()

	conn, _, err := websocket.Dial(ctx, parsed.String(), nil)
	if err != nil {
		// Dial failures echo the URL, which carries the token.
		return nil, fmt.Errorf("connecting terminal: %s", tokenInQuery.ReplaceAllString(err.Error(), "token=REDACTED"))
	}
	return &Terminal{conn: conn, input: opts.Input, output: opts.Output, errors: opts.Errors, terminal: opts.Terminal}, nil
}

// Run relays the session until it ends: the shell exits, the sandbox closes
// it, or ctx is done. With a local terminal, the terminal is in raw mode for
// the session, so keys such as Ctrl+C and Ctrl+D reach the shell. It returns
// an error only for a failure, not for the session ending.
func (t *Terminal) Run(ctx context.Context) error {
	if t.terminal != nil {
		restore, err := t.terminal.MakeRaw()
		if err != nil {
			t.conn.Close(websocket.StatusInternalError, "")
			return fmt.Errorf("setting terminal raw mode: %w", err)
		}
		defer restore()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Ending the session for any reason closes the connection, which ends
	// both loops.
	context.AfterFunc(ctx, func() { t.conn.Close(websocket.StatusNormalClosure, "") })
	if t.terminal != nil {
		t.terminal.NotifyResize(ctx, func() {
			if cols, rows, err := t.terminal.Size(); err == nil {
				_ = t.Resize(cols, rows)
			}
		})
	}
	go t.inputLoop(ctx)
	return t.readLoop(ctx)
}

// Resize tells the sandbox the window's new size.
func (t *Terminal) Resize(cols, rows int) error {
	return t.send(Message{Type: "resize", Cols: cols, Rows: rows})
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

func (t *Terminal) readLoop(ctx context.Context) error {
	for {
		_, payload, err := t.conn.Read(ctx)
		if err != nil {
			// A normal closure or our own cancellation ends the session;
			// anything else is a failure.
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("terminal connection: %w", err)
		}
		var message Message
		if err := json.Unmarshal(payload, &message); err != nil {
			continue
		}
		switch message.Type {
		case "output":
			if _, err := io.WriteString(t.output, message.Data); err != nil {
				return err
			}
		case "error":
			_, _ = io.WriteString(t.errors, message.Data+"\n")
		}
	}
}

func (t *Terminal) inputLoop(ctx context.Context) {
	buf := make([]byte, 512)
	// A read can end inside a multi-byte UTF-8 character; the partial
	// character waits for the next read to complete it.
	var pending []byte
	for ctx.Err() == nil {
		n, err := t.input.Read(buf)
		pending = append(pending, buf[:n]...)
		// The length of the leading complete characters.
		complete := 0
		for complete < len(pending) {
			r, size := utf8.DecodeRune(pending[complete:])
			if r == utf8.RuneError && size <= 1 && !utf8.FullRune(pending[complete:]) {
				break
			}
			complete += size
		}
		if complete > 0 {
			if t.send(Message{Type: "input", Data: string(pending[:complete])}) != nil {
				return
			}
			pending = append(pending[:0], pending[complete:]...)
		}
		if err != nil {
			return
		}
	}
}
