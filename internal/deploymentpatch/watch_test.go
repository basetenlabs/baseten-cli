package deploymentpatch

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// awaitTick blocks until the watcher emits one non-error tick. It deliberately
// has no timeout: the test makes no assumption about how quickly the event is
// delivered (that would be a processor-speed assumption), so it simply waits
// for the real event and relies on `go test`'s global timeout as the only hang
// guard. A broken watcher that never emits fails by timing out the suite.
func awaitTick(t *testing.T, ch <-chan WatchEvent) {
	t.Helper()
	ev, ok := <-ch
	if !ok {
		t.Fatal("watch channel closed before a tick arrived")
	}
	if ev.Err != nil {
		t.Fatalf("unexpected watch error: %v", ev.Err)
	}
}

// TestWatchEventRelevant exercises the per-event filter as a pure function: no
// watcher, no events, no clock.
func TestWatchEventRelevant(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".truss_ignore"), []byte("*.log\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "model.py"), []byte("print('hi')"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "debug.log"), []byte("noise"), 0o644))

	ignore, err := ResolveTrussIgnore(dir)
	require.NoError(t, err)

	cases := []struct {
		name string
		ev   fsnotify.Event
		want bool
	}{
		{"tracked file write", fsnotify.Event{Name: filepath.Join(dir, "model.py"), Op: fsnotify.Write}, true},
		{"tracked file create", fsnotify.Event{Name: filepath.Join(dir, "model.py"), Op: fsnotify.Create}, true},
		{"chmod only is dropped", fsnotify.Event{Name: filepath.Join(dir, "model.py"), Op: fsnotify.Chmod}, false},
		{"ignored file is dropped", fsnotify.Event{Name: filepath.Join(dir, "debug.log"), Op: fsnotify.Write}, false},
		{"removed ignored file is dropped", fsnotify.Event{Name: filepath.Join(dir, "gone.log"), Op: fsnotify.Remove}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, watchEventRelevant(t.Context(), tc.ev, dir, ignore))
		})
	}
}

// TestAddWatchesRecursivePrunesIgnored asserts which directories get a watch.
// Mirroring Truss (one ignore predicate, applied while walking), a directory
// whose entry is itself ignored (.git) is pruned entirely. A directory only
// ignored by contents (__pycache__ keeps its null dir entry for hash parity)
// is still watched, but the ancestor-ignored subtree beneath it is not - those
// events are dropped by watchEventRelevant instead.
func TestAddWatchesRecursivePrunesIgnored(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "pkg"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "pkg", "sub"), 0o755))
	// .git is a bare-name ignore: the entry itself is ignored, so it is pruned.
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	// __pycache__ is a contents-only ignore: the dir is kept but its subtree is
	// ignored by the ancestor rule.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "__pycache__", "nested"), 0o755))

	ignore, err := ResolveTrussIgnore(dir)
	require.NoError(t, err)

	fsw, err := fsnotify.NewWatcher()
	require.NoError(t, err)
	defer fsw.Close()

	require.NoError(t, addWatchesRecursive(t.Context(), fsw, dir, dir, ignore))

	assert.ElementsMatch(t, []string{
		dir,
		filepath.Join(dir, "pkg"),
		filepath.Join(dir, "pkg", "sub"),
		filepath.Join(dir, "__pycache__"),
	}, fsw.WatchList())
}

func TestWatchRequiresDir(t *testing.T) {
	_, err := Watch(t.Context(), WatchOptions{})
	require.Error(t, err)
}

// TestWatchDetectsChange is the end-to-end check: a real write under a real temp
// directory wakes the watcher. It blocks for the tick (no timeout) per the
// no-clock policy above.
func TestWatchDetectsChange(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.txt")
	require.NoError(t, os.WriteFile(existing, []byte("v1"), 0o644))

	ch, err := Watch(t.Context(), WatchOptions{Dir: dir})
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hello"), 0o644))
	awaitTick(t, ch)

	require.NoError(t, os.WriteFile(existing, []byte("v2"), 0o644))
	awaitTick(t, ch)
}

// TestWatchPicksUpNewDirectory proves a directory created after the watch
// started is itself watched: the second tick can only arrive if a watch was
// added to the new directory. The receives are serialized, so the two changes
// land in separate debounce windows without any timing assumption.
func TestWatchPicksUpNewDirectory(t *testing.T) {
	dir := t.TempDir()

	ch, err := Watch(t.Context(), WatchOptions{Dir: dir})
	require.NoError(t, err)

	sub := filepath.Join(dir, "pkg")
	require.NoError(t, os.Mkdir(sub, 0o755))
	awaitTick(t, ch)

	require.NoError(t, os.WriteFile(filepath.Join(sub, "util.py"), []byte("x = 1"), 0o644))
	awaitTick(t, ch)
}

func TestWatchClosesOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())

	ch, err := Watch(ctx, WatchOptions{Dir: dir})
	require.NoError(t, err)

	cancel()
	// Draining to completion blocks until the channel closes; if cancellation
	// failed to close it the suite times out rather than asserting on a clock.
	for range ch { //nolint:revive // intentionally draining until close
	}
}
