package cmd

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-go/client"
)

func TestVolumeProgressFlushesBeforePhaseChangeAndStop(t *testing.T) {
	for _, phase := range []client.VolumePhase{client.VolumePhaseUpload, client.VolumePhaseDownload} {
		t.Run(string(phase), func(t *testing.T) {
			var lines []string
			update, stop := startVolumeProgressLogger(func(f string, args ...any) { lines = append(lines, fmt.Sprintf(f, args...)) }, nil)
			update(client.VolumeProgress{Phase: phase, TotalFiles: 2, TotalBytes: 73})
			update(client.VolumeProgress{Phase: phase, Files: 1, Bytes: 30, TotalFiles: 2, TotalBytes: 73})
			update(client.VolumeProgress{Phase: phase, Files: 2, Bytes: 73, TotalFiles: 2, TotalBytes: 73})
			update(client.VolumeProgress{Phase: client.VolumePhasePublish})
			stop()
			stop()
			if len(lines) != 3 || !strings.Contains(lines[1], "2/2 files, 73 B/73 B") || lines[2] != "  publish...\n" {
				t.Fatalf("unexpected output: %q", lines)
			}
		})
	}
}

func TestVolumeProgressTickAndEarlyStop(t *testing.T) {
	ticks := make(chan time.Time)
	lines := make(chan string, 10)
	update, stop := startVolumeProgressLogger(func(f string, args ...any) { lines <- fmt.Sprintf(f, args...) }, ticks)
	defer stop()
	update(client.VolumeProgress{Phase: client.VolumePhaseUpload, TotalFiles: 4})
	<-lines
	update(client.VolumeProgress{Phase: client.VolumePhaseUpload, Files: 3, TotalFiles: 4})
	ticks <- time.Now()
	select {
	case line := <-lines:
		if !strings.Contains(line, "3/4 files") {
			t.Fatal(line)
		}
	case <-time.After(time.Second):
		t.Fatal("pending progress was not printed without another callback")
	}
	// Unchanged state must not produce another line, and shutdown must flush
	// pending state even when a transfer ends without a phase transition.
	ticks <- time.Now()
	update(client.VolumeProgress{Phase: client.VolumePhaseUpload, Files: 4, TotalFiles: 4})
	stop()
	if len(lines) != 1 {
		t.Fatalf("expected one final update, got %d", len(lines))
	}
	if line := <-lines; !strings.Contains(line, "4/4 files") {
		t.Fatal(line)
	}
}
