package trading

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"time"
)

// Snapshot serializes checkpoint writers, but keeps encoding and disk I/O off
// the admission lock. At most two WAL files are needed: an immutable previous
// segment covering the frozen state and a current segment accepting new writes.
func (s *Service) Snapshot() error {
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()
	return s.checkpoint(writeSnapshot)
}

// The writer parameter lets crash/slow-disk tests pause actual checkpoint phases
// without adding test callbacks or filesystem abstractions to the public Config.
func (s *Service) checkpoint(write func(string, snapshot) error) error {
	state, err := s.freezeCheckpoint()
	if err != nil {
		return err
	}
	if err = write(s.cfg.SnapshotPath, state); err != nil {
		return err
	}
	if err = s.HealthError(); err != nil {
		return err
	}
	// Only the immutable segment is covered by this snapshot. Never truncate the
	// current WAL: orders may have been acknowledged while the snapshot was written.
	if err = os.Remove(s.cfg.WALPath + ".previous"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(filepath.Dir(s.cfg.WALPath))
}

func (s *Service) freezeCheckpoint() (snapshot, error) {
	s.mu.Lock()
	start := time.Now()
	defer func() { s.observe(StageSnapshotPause, start); s.mu.Unlock() }()
	if s.failure != nil {
		return snapshot{}, s.failure
	}
	if s.cfg.SnapshotPath == "" {
		return snapshot{}, errors.New("snapshot path not configured")
	}
	state := snapshot{Sequence: s.sequence, Markets: maps.Clone(s.markets), Orders: maps.Clone(s.orders), Balances: maps.Clone(s.balances), Positions: make(map[string]map[string]Position, len(s.positions))}
	for user, positions := range s.positions {
		state.Positions[user] = maps.Clone(positions)
	}
	previous := s.cfg.WALPath + ".previous"
	if _, err := os.Stat(previous); err == nil {
		// A prior checkpoint failed or the process crashed before deleting its old
		// segment. Both files were replayed on startup. This retry covers the previous
		// segment without replacing it; the current WAL is retained for one more cycle.
		return state, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return snapshot{}, err
	}
	if err := os.MkdirAll(filepath.Dir(s.cfg.WALPath), 0755); err != nil {
		return snapshot{}, err
	}
	if s.walFile != nil {
		if err := s.walFile.Sync(); err != nil {
			return snapshot{}, s.fail(err)
		}
		if err := s.walFile.Close(); err != nil {
			return snapshot{}, s.fail(err)
		}
		s.walFile = nil
	}
	if err := os.Rename(s.cfg.WALPath, previous); err != nil && !errors.Is(err, os.ErrNotExist) {
		return snapshot{}, s.fail(err)
	}
	// Make the backup name durable before reusing the current WAL's pathname.
	// A power failure may revert an unflushed rename; creating the replacement
	// first could otherwise obscure the last durable segment.
	if err := syncDirectory(filepath.Dir(s.cfg.WALPath)); err != nil {
		return snapshot{}, s.fail(err)
	}
	file, err := os.OpenFile(s.cfg.WALPath, os.O_CREATE|os.O_EXCL|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return snapshot{}, s.fail(err)
	}
	s.walFile = file
	if err = file.Sync(); err != nil {
		return snapshot{}, s.fail(err)
	}
	if err = syncDirectory(filepath.Dir(s.cfg.WALPath)); err != nil {
		return snapshot{}, s.fail(err)
	}
	return state, nil
}

func writeSnapshot(path string, state snapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	// Encoder writes its buffer directly, avoiding Marshal's extra full-size copy.
	if err = json.NewEncoder(file).Encode(state); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
