// Package store persists the WellBoard state (initial TZ 5.3 / NFR-3).
//
// state.json is written atomically: marshal → tmp file → fsync → rename →
// fsync of the directory (see save). On the router the state directory is
// 0700 and the file 0600 (NFR-2.3); in dev mode permissions are left to the
// process umask so working trees stay shareable.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/wellboard/wellboard/internal/model"
)

// CurrentVersion is the state schema version written by this build.
const CurrentVersion = 1

// fileMode / dirMode are the production permissions (NFR-2.3).
const (
	fileMode fs.FileMode = 0o600
	dirMode  fs.FileMode = 0o700
)

// ErrVersionTooNew means the on-disk state was written by a newer WellBoard
// and cannot be loaded by this one (downgrades are unsupported).
var ErrVersionTooNew = errors.New("state version is newer than supported")

// Store reads and writes the state file below Root.
type Store struct {
	// Root is the state directory (e.g. /etc/wellboard or a dev dir).
	Root string
	// Prod enforces the 0700/0600 permissions; dev mode leaves umask.
	Prod bool
}

// New returns a Store rooted at dir.
func New(dir string, prod bool) *Store {
	return &Store{Root: dir, Prod: prod}
}

// Path returns the state file location.
func (s *Store) Path() string {
	return filepath.Join(s.Root, "state.json")
}

// DefaultState returns a fresh v1 state with the initial TZ defaults
// (decision Q3: default policy DIRECT; decision Q11: ru; decision Q7:
// runetfreedom geodata).
func DefaultState() *model.State {
	return &model.State{
		Version: CurrentVersion,
		Settings: model.Settings{
			UIPort:               8090,
			Lang:                 "ru",
			Geodata:              "runetfreedom",
			DefaultPolicy:        model.Target{Type: model.TargetDirect},
			DelayTestIntervalSec: 300,
		},
	}
}

// Load reads state.json. A missing file yields DefaultState (first start);
// any other error is returned as-is. On success the loaded version is
// migrated forward if needed.
func (s *Store) Load() (*model.State, error) {
	data, err := os.ReadFile(s.Path())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return DefaultState(), nil
		}
		return nil, fmt.Errorf("read state: %w", err)
	}

	st := &model.State{}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", s.Path(), err)
	}

	if st.Version > CurrentVersion {
		return nil, fmt.Errorf("%w: file has %d, build supports %d",
			ErrVersionTooNew, st.Version, CurrentVersion)
	}
	if err := migrate(st); err != nil {
		return nil, err
	}
	return st, nil
}

// migrate moves an older state schema forward in place. v1 is the initial
// schema, so the chain is empty; future versions append steps here.
func migrate(st *model.State) error {
	// v1 → v2: (reserved) no steps yet.
	return nil
}

// Save atomically writes st to state.json: tmp + fsync + rename + dir fsync.
// In prod mode the state directory (created if missing) is 0700 and the
// file 0600 (NFR-2.3). In dev mode permissions are governed by the umask.
func (s *Store) Save(st *model.State) error {
	st.Version = CurrentVersion

	if err := os.MkdirAll(s.Root, s.dirPerm()); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(s.Root, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	// Best effort: no leftover temp files on failure paths.
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp state: %w", err)
	}
	if err = tmp.Chmod(s.filePerm()); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp state: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp state: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temp state: %w", err)
	}

	if err = os.Rename(tmpName, s.Path()); err != nil {
		return fmt.Errorf("rename state into place: %w", err)
	}

	// Make the rename durable (NFR-3: crash-safe state writes).
	if dir, derr := os.Open(s.Root); derr == nil {
		_ = dir.Sync()
		dir.Close()
	}
	return nil
}

func (s *Store) dirPerm() fs.FileMode {
	if s.Prod {
		return dirMode
	}
	return 0o755 // dev: umask-governed default
}

func (s *Store) filePerm() fs.FileMode {
	if s.Prod {
		return fileMode
	}
	return 0o644 // dev: umask-governed default
}
