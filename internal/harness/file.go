package harness

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// configFile preserves symlinks and permits atomic replacement of their target.
// A byte comparison catches edits made while the user reviews a preview; it is
// not a cross-process lock or a guarantee against concurrent writers.
type configFile struct {
	Path   string
	Target string
	Data   []byte
	Info   os.FileInfo
}

func resolveConfigPath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	target, err := filepath.EvalSymlinks(path)
	if err == nil {
		path = target
	} else if !os.IsNotExist(err) {
		return "", err
	} else {
		if _, e := os.Lstat(path); e == nil {
			return "", fmt.Errorf("refusing dangling symlink %s", path)
		}
		// Resolve an existing parent symlink, including when the final file is new.
		parent := filepath.Dir(path)
		suffix := filepath.Base(path)
		for {
			resolved, e := filepath.EvalSymlinks(parent)
			if e == nil {
				path = filepath.Join(resolved, suffix)
				break
			}
			if !os.IsNotExist(e) {
				return "", e
			}
			next := filepath.Dir(parent)
			if next == parent {
				return "", e
			}
			suffix = filepath.Join(filepath.Base(parent), suffix)
			parent = next
		}
	}
	return path, nil
}

func readFile(path string) (*configFile, error) {
	target, err := resolveConfigPath(path)
	if err != nil {
		return nil, err
	}
	s := &configFile{Path: path, Target: target}
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

func (s *configFile) check() error {
	now, err := readFile(s.Path)
	if err != nil {
		return err
	}
	same := s.Target == now.Target && bytes.Equal(s.Data, now.Data) && (s.Info == nil) == (now.Info == nil)
	if same && s.Info != nil {
		same = s.Info.Mode() == now.Info.Mode()
	}
	if !same {
		return fmt.Errorf("configuration changed concurrently: %s; retry after reviewing it", s.Path)
	}
	return nil
}

func (s *configFile) writeExisting(data []byte) error {
	mode := os.FileMode(0600)
	if s.Info != nil {
		mode = s.Info.Mode().Perm()
	}
	return s.write(data, mode)
}

// writePrivate installs the replacement with owner-only permissions before rename.
func (s *configFile) writePrivate(data []byte) error {
	return s.write(data, 0600)
}

func (s *configFile) write(data []byte, mode os.FileMode) error {
	if s.Info != nil && bytes.Equal(s.Data, data) && s.Info.Mode().Perm() == mode {
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
	return os.Rename(f.Name(), s.Target)
}
