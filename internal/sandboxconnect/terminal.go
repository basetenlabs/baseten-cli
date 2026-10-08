// Package sandboxconnect opens an interactive terminal to a sandbox over the
// WebSocket its execution API serves at /terminal/ws. That endpoint is not in
// the API spec: it takes JSON messages of type input, output, resize, and
// error, with the token, window size, and session ID in the URL.
//
// A session survives transport loss. An idle sandbox hibernates, and the VM
// pause kills the connection without a close frame. Run reports the loss and,
// on the next keystroke, reconnects with the same session ID, which reattaches
// to the shell the session left and replays its buffered output.
package sandboxconnect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
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

	// NewToken returns a sandbox token. Called once per connect, because a
	// reconnection after a long hibernation may outlive the first token.
	NewToken func(context.Context) (string, error)

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
	opts      DialOptions
	sessionID string
	conn      *websocket.Conn
	writeMu   sync.Mutex
	input     io.Reader
	output    io.Writer
	errors    io.Writer
	terminal  TerminalControl

	// reconnectKeystroke delivers the keystroke that asked for a
	// reconnection, or nil when input ended. Capacity one: the first
	// keystroke reconnects, later ones wait in the tty buffer.
	reconnectKeystroke chan []byte
}

// TransportLostError reports the WebSocket dying without a close frame: the
// sandbox hibernating pauses the VM and kills the connection, so the server
// cannot send one.
type TransportLostError struct {
	err error
}

func (e *TransportLostError) Error() string { return e.err.Error() }
func (e *TransportLostError) Unwrap() error { return e.err }

// wakeDialAttempts and wakeDialInterval bound a reconnect: the wake-on-demand
// resume after hibernation usually takes a few seconds.
const (
	wakeDialAttempts = 20
	wakeDialInterval = 1 * time.Second
	// dialTimeoutSeconds bounds one dial: while a hibernated sandbox resumes,
	// the gateway can hold the upgrade open instead of refusing it.
	dialTimeoutSeconds = 5
)

// Dial connects a new terminal session. Each invocation generates a random
// session ID: the endpoint's default session is shared, so a connect must not
// rejoin a shell an earlier one left. Reconnections within one Terminal keep
// the ID, which is what recovers the live shell after a transport failure.
func Dial(ctx context.Context, opts DialOptions) (*Terminal, error) {
	session := make([]byte, 8)
	if _, err := rand.Read(session); err != nil {
		return nil, err
	}
	terminal := &Terminal{
		opts:               opts,
		sessionID:          hex.EncodeToString(session),
		input:              opts.Input,
		output:             opts.Output,
		errors:             opts.Errors,
		terminal:           opts.Terminal,
		reconnectKeystroke: make(chan []byte, 1),
	}
	token, err := opts.NewToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting a sandbox token: %w", err)
	}
	if err := terminal.connect(ctx, token); err != nil {
		return nil, err
	}
	return terminal, nil
}

// connect dials the session's WebSocket with the given token and the window
// size as it is now.
func (t *Terminal) connect(ctx context.Context, token string) error {
	parsed, err := url.Parse(t.opts.SandboxURL)
	if err != nil {
		return fmt.Errorf("invalid sandbox URL: %w", err)
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/terminal/ws"
	cols, rows := 80, 24
	if t.terminal != nil {
		if c, r, err := t.terminal.Size(); err == nil {
			cols, rows = c, r
		}
	}
	query := parsed.Query()
	query.Set("token", token)
	query.Set("cols", strconv.Itoa(cols))
	query.Set("rows", strconv.Itoa(rows))
	query.Set("sessionId", t.sessionID)
	parsed.RawQuery = query.Encode()

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeoutSeconds*time.Second)
	conn, _, err := websocket.Dial(dialCtx, parsed.String(), nil)
	cancel()
	if err != nil {
		// Dial failures echo the URL, which carries the token.
		return fmt.Errorf("connecting terminal: %s", tokenInQuery.ReplaceAllString(err.Error(), "token=REDACTED"))
	}
	t.conn = conn
	return nil
}

// Run relays the session until it ends: the shell exits, the sandbox closes
// it, input ends, or ctx is done. With a local terminal, the terminal is in
// raw mode for the session, so keys such as Ctrl+C and Ctrl+D reach the
// shell. After a transport loss it waits for a keystroke, then reconnects and
// resumes the same shell. It returns an error only for a failure, not for the
// session ending.
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
	if t.terminal != nil {
		t.terminal.NotifyResize(ctx, func() {
			if cols, rows, err := t.terminal.Size(); err == nil {
				_ = t.Resize(cols, rows)
			}
		})
	}
	for {
		err := t.relay(ctx)
		var lost *TransportLostError
		if err == nil || !errors.As(err, &lost) {
			return err
		}
		io.WriteString(t.errors,
			"\r\nConnection lost. The sandbox hibernates after inactivity, which drops the connection.\r\n"+
				"Type any key to reconnect and wake the sandbox, or press Ctrl+C or Ctrl+D to quit.\r\n")
		// The input loop ends when it delivers a keystroke, so a failed
		// reconnect must restart it: nothing else would ever read the retry.
		disconnectedInput := make(chan struct{})
		close(disconnectedInput)
		inputLoopRunning := true // The relay's loop is parked reading input.
		for {
			if !inputLoopRunning {
				go t.inputLoop(ctx, disconnectedInput)
				inputLoopRunning = true
			}
			select {
			case keystroke := <-t.reconnectKeystroke:
				// Raw mode delivers Ctrl+C and Ctrl+D as bytes, not signals.
				if keystroke == nil || (len(keystroke) > 0 && (keystroke[0] == 0x03 || keystroke[0] == 0x04)) {
					return nil
				}
				inputLoopRunning = false
				if err := t.wakeAndConnect(ctx); err != nil {
					fmt.Fprintf(t.errors,
						"Reconnect failed: %v\r\nType any key to retry, or press Ctrl+C or Ctrl+D to quit.\r\n", err)
					continue
				}
				// The keystroke that asked for the reconnection is the
				// trigger, not shell input: forwarding it would run stray
				// characters into the command line.
			case <-ctx.Done():
				return nil
			}
			break
		}
	}
}

// relay runs both loops of one connection until the session ends or the
// transport dies.
func (t *Terminal) relay(ctx context.Context) error {
	conn := t.conn
	// Ending the session for any reason closes the connection, which ends
	// both loops.
	context.AfterFunc(ctx, func() { conn.Close(websocket.StatusNormalClosure, "") })
	disconnect := make(chan struct{})
	go t.inputLoop(ctx, disconnect)
	return t.readLoop(ctx, disconnect)
}

// wakeAndConnect reconnects, retrying while the sandbox is still resuming.
func (t *Terminal) wakeAndConnect(ctx context.Context) error {
	io.WriteString(t.errors, "Waking the sandbox...\r\n")
	// One mint per wake: a token per dial attempt would trip Baseten's API
	// rate limit long before the sandbox finishes resuming.
	token, err := t.opts.NewToken(ctx)
	if err != nil {
		return fmt.Errorf("getting a sandbox token: %w", err)
	}
	var lastErr error
	reMinted := false
	for attempt := 0; attempt < wakeDialAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(wakeDialInterval):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		err := t.connect(ctx, token)
		if err == nil {
			io.WriteString(t.errors, "Reconnected.\r\n")
			return nil
		}
		lastErr = err
		var closeErr *websocket.CloseError
		if errors.As(err, &closeErr) && (closeErr.Code == 401 || closeErr.Code == 403) && !reMinted {
			// A token the sandbox API refused is stale; mint one fresh token
			// and let the remaining attempts use it.
			reMinted = true
			token, err = t.opts.NewToken(ctx)
			if err != nil {
				return fmt.Errorf("getting a sandbox token: %w", err)
			}
		}
		if attempt%5 == 4 {
			fmt.Fprintf(t.errors, "Still waking the sandbox (attempt %d of %d)...\r\n", attempt+1, wakeDialAttempts)
		}
	}
	return lastErr
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

func (t *Terminal) readLoop(ctx context.Context, disconnect chan struct{}) error {
	for {
		_, payload, err := t.conn.Read(ctx)
		if err != nil {
			// A normal closure or our own cancellation ends the session;
			// anything else is a failure.
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure || ctx.Err() != nil {
				return nil
			}
			close(disconnect)
			if websocket.CloseStatus(err) == -1 {
				return &TransportLostError{err: err}
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

// inputLoop reads input and sends it to the shell. After the read loop marked
// the connection lost, the next keystroke becomes the reconnection request:
// it is delivered instead of sent, and the loop ends.
func (t *Terminal) inputLoop(ctx context.Context, disconnect <-chan struct{}) {
	buf := make([]byte, 512)
	// A read can end inside a multi-byte UTF-8 character; the partial
	// character waits for the next read to complete it.
	var pending []byte
	for ctx.Err() == nil {
		n, err := t.input.Read(buf)
		if n > 0 {
			select {
			case <-disconnect:
				t.deliverReconnectKeystroke(buf[:n])
				return
			default:
			}
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
				if err := t.send(Message{Type: "input", Data: string(pending[:complete])}); err != nil {
					// The send failed on a dying connection: after a
					// transport loss these bytes become the reconnection
					// request; after a clean close, nobody reads them.
					t.deliverReconnectKeystroke(pending[:complete])
					return
				}
				pending = append(pending[:0], pending[complete:]...)
			}
		}
		if err != nil {
			// Ended input cannot ask for a reconnection.
			select {
			case <-disconnect:
				t.deliverReconnectKeystroke(nil)
			default:
			}
			return
		}
	}
}

// deliverReconnectKeystroke hands one keystroke (or ended input, for a nil
// slice) to the run loop, dropping it when one is already delivered.
func (t *Terminal) deliverReconnectKeystroke(keystroke []byte) {
	select {
	case t.reconnectKeystroke <- keystroke:
	default:
	}
}
