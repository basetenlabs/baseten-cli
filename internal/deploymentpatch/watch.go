package deploymentpatch

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/basetenlabs/baseten-go/client/modelarchive"
	"github.com/fsnotify/fsnotify"
)

// defaultWatchDebounce is the quiet window changes must settle within before a
// tick is emitted. Editors save with a burst of write/rename/chmod events and
// tools rewrite trees wholesale; coalescing within a short window collapses
// each burst into one patch tick. Mirrors watchfiles' settle behavior in Truss.
const defaultWatchDebounce = 100 * time.Millisecond

// WatchOptions configures [Watch].
type WatchOptions struct {
	// Dir is the model directory to watch. Required.
	Dir string
	// DebounceWindow coalesces a burst of filesystem events into a single tick.
	// Defaults to 100ms when zero.
	DebounceWindow time.Duration
}

// WatchEvent is one item on the channel returned by [Watch]. A zero value
// signals a debounced batch of filesystem changes under the watched directory.
// A non-nil Err means the underlying watcher failed and the channel is about to
// close; the caller should stop watching.
type WatchEvent struct {
	Err error
}

// Watch watches opts.Dir recursively and returns a channel that receives one
// [WatchEvent] per debounced batch of changes to non-ignored paths. Changes to
// ignored paths (per [ResolveTrussIgnore]) never wake the caller.
//
// Mirroring Truss (which watches recursively and filters each event through one
// ignore predicate), the same ignore drives both the watch and the patch
// computation. An entry-ignored directory (.git) is pruned from the watch
// entirely; a contents-only-ignored directory (__pycache__, kept as a null
// entry for hash parity) is still watched, but its events are filtered out.
//
// The filter is best-effort, not authoritative: a spurious wake is harmless
// because the caller re-derives the patch point on each tick and finds zero
// ops.
//
// The channel is closed when ctx is cancelled or the watcher fails. A setup
// failure (bad directory, watcher creation) is returned synchronously instead.
func Watch(ctx context.Context, opts WatchOptions) (<-chan WatchEvent, error) {
	if opts.Dir == "" {
		return nil, fmt.Errorf("deploymentpatch: Dir is required")
	}
	ignore, err := ResolveTrussIgnore(opts.Dir)
	if err != nil {
		return nil, err
	}
	debounce := opts.DebounceWindow
	if debounce == 0 {
		debounce = defaultWatchDebounce
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("deploymentpatch: create watcher: %w", err)
	}
	if err := addWatchesRecursive(ctx, fsw, opts.Dir, opts.Dir, ignore); err != nil {
		_ = fsw.Close()
		return nil, err
	}

	out := make(chan WatchEvent, 1)
	go runWatch(ctx, fsw, opts.Dir, ignore, debounce, out)
	return out, nil
}

// runWatch is the watcher's event loop. It owns fsw and closes it on return.
func runWatch(
	ctx context.Context,
	fsw *fsnotify.Watcher,
	dir string,
	ignore modelarchive.IgnoreFileFunc,
	debounce time.Duration,
	out chan<- WatchEvent,
) {
	defer close(out)
	defer fsw.Close()

	// The first change of a batch opens a fixed debounce window (time.After);
	// every change within it folds into the same window without extending it,
	// so one tick fires per window. This bounds latency to the debounce even
	// under sustained change, where a reset-on-every-event timer would starve.
	// timerC is nil while idle so the select never wakes spuriously.
	var timerC <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-fsw.Errors:
			if !ok {
				return
			}
			select {
			case out <- WatchEvent{Err: fmt.Errorf("deploymentpatch: watch: %w", err)}:
			case <-ctx.Done():
			}
			return
		case ev, ok := <-fsw.Events:
			if !ok {
				return
			}
			if !watchEventRelevant(ctx, ev, dir, ignore) {
				continue
			}
			// A freshly created directory has no watch yet; register it (and any
			// pre-populated children) so changes under it are seen.
			if ev.Has(fsnotify.Create) {
				if info, statErr := os.Stat(ev.Name); statErr == nil && info.IsDir() {
					_ = addWatchesRecursive(ctx, fsw, dir, ev.Name, ignore)
				}
			}
			if timerC == nil {
				timerC = time.After(debounce)
			}
		case <-timerC:
			timerC = nil
			select {
			case out <- WatchEvent{}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// watchEventRelevant reports whether ev should wake the caller. Pure chmod
// events (metadata churn from editors and permission tools) are dropped, as are
// events on ignored paths.
func watchEventRelevant(ctx context.Context, ev fsnotify.Event, dir string, ignore modelarchive.IgnoreFileFunc) bool {
	if ev.Op == fsnotify.Chmod {
		return false
	}
	rel, err := filepath.Rel(dir, ev.Name)
	if err != nil {
		return true
	}
	relSlash := filepath.ToSlash(rel)
	// On removal/rename the path is gone, so its kind is unknowable; treat it as
	// a file for the ignore check. A wrong guess only risks a harmless extra tick.
	entry := entryForPath(ev.Name)
	ignored, err := ignore(ctx, modelarchive.IgnoreFileOptions{RelPath: relSlash, Entry: entry})
	if err != nil {
		return true
	}
	return !ignored
}

// addWatchesRecursive registers a watch for root and every non-ignored
// directory beneath it, pruning ignored subtrees. dir is the model root,
// against which ignore paths are resolved.
func addWatchesRecursive(ctx context.Context, fsw *fsnotify.Watcher, dir, root string, ignore modelarchive.IgnoreFileFunc) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if p != dir {
			rel, relErr := filepath.Rel(dir, p)
			if relErr != nil {
				return relErr
			}
			ignored, ignErr := ignore(ctx, modelarchive.IgnoreFileOptions{RelPath: filepath.ToSlash(rel), Entry: d})
			if ignErr != nil {
				return ignErr
			}
			if ignored {
				return filepath.SkipDir
			}
		}
		if err := fsw.Add(p); err != nil {
			return fmt.Errorf("deploymentpatch: watch %s: %w", p, err)
		}
		return nil
	})
}

// entryForPath returns a best-effort fs.DirEntry for path, falling back to a
// file-typed entry when the path no longer exists (a removed or renamed file).
func entryForPath(path string) fs.DirEntry {
	info, err := os.Lstat(path)
	if err != nil {
		return removedFileEntry{name: filepath.Base(path)}
	}
	return fs.FileInfoToDirEntry(info)
}

// removedFileEntry is a minimal fs.DirEntry for a path that no longer exists,
// reported as a regular file.
type removedFileEntry struct{ name string }

func (e removedFileEntry) Name() string               { return e.name }
func (e removedFileEntry) IsDir() bool                { return false }
func (e removedFileEntry) Type() fs.FileMode          { return 0 }
func (e removedFileEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrNotExist }
