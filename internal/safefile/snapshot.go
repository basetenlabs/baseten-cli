package safefile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// Snapshot retains identity, bytes, permissions and the resolved symlink
// target. Writes reject concurrent edits and leave an existing symlink intact.
// As with normal editor saves, a non-cooperating writer can still race the
// final check and rename. The installation lock covers other CLI processes.
type Snapshot struct {
	Path   string
	Target string
	Data   []byte
	Info   os.FileInfo
}

func ReadSnapshot(path string) (*Snapshot, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	s := &Snapshot{Path: path, Target: path}
	target, err := filepath.EvalSymlinks(path)
	if err == nil {
		s.Target = target
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		if _, e := os.Lstat(path); e == nil {
			return nil, fmt.Errorf("refusing dangling symlink %s", path)
		}
		// Resolve an existing parent symlink, including when the final file is new.
		parent := filepath.Dir(path)
		suffix := filepath.Base(path)
		for {
			resolved, e := filepath.EvalSymlinks(parent)
			if e == nil {
				s.Target = filepath.Join(resolved, suffix)
				break
			}
			if !os.IsNotExist(e) {
				return nil, e
			}
			next := filepath.Dir(parent)
			if next == parent {
				return nil, e
			}
			suffix = filepath.Join(filepath.Base(parent), suffix)
			parent = next
		}
	}
	s.Info, err = os.Stat(s.Target)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if !s.Info.Mode().IsRegular() {
		return nil, fmt.Errorf("refusing non-regular configuration file %s", path)
	}
	s.Data, err = os.ReadFile(s.Target)
	return s, err
}
func (s *Snapshot) Check() error {
	now, err := ReadSnapshot(s.Path)
	if err != nil {
		return err
	}
	same := s.Target == now.Target && bytes.Equal(s.Data, now.Data) && (s.Info == nil) == (now.Info == nil)
	if same && s.Info != nil {
		same = os.SameFile(s.Info, now.Info) && s.Info.ModTime() == now.Info.ModTime() && s.Info.Mode() == now.Info.Mode()
	}
	if !same {
		return fmt.Errorf("configuration changed concurrently: %s; retry after reviewing it", s.Path)
	}
	return nil
}
func (s *Snapshot) Write(data []byte) error {
	if err := s.Check(); err != nil {
		return err
	}
	if s.Info != nil && bytes.Equal(s.Data, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.Target), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.Target), ".baseten-harness-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	mode := os.FileMode(0600)
	if s.Info != nil {
		mode = s.Info.Mode().Perm()
	}
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = s.Check(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.Target)
}
