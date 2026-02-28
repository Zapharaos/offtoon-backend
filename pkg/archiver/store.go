// Package archiver – store.go
// Store holds references to assembled ZIP archives as temporary files on disk,
// keyed by the runtime ID that produced them. Each archive can be consumed
// exactly once (the temp file is deleted after it is served).
//
// Unclaimed archives are reaped automatically after TTL (default 1 hour) by a
// background goroutine started with Store.StartReaper. Call CleanupOrphans at
// startup to remove any temp files left behind by a previous crash.
package archiver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// entry holds a ready-to-serve archive file path, download filename, and the
// time it was stored (used for TTL expiry).
type entry struct {
	filename string // download filename (e.g. "my-toon.zip")
	filePath string // path to the temp file on disk
	storedAt time.Time
}

// Store is a thread-safe, disk-backed archive store with automatic TTL cleanup.
type Store struct {
	mu      sync.Mutex
	entries map[uuid.UUID]entry
	ttl     time.Duration
}

// NewStore creates an empty Store with the given TTL for unclaimed archives.
// A TTL of 0 disables automatic expiry (not recommended for production).
func NewStore(ttl time.Duration) *Store {
	return &Store{
		entries: make(map[uuid.UUID]entry),
		ttl:     ttl,
	}
}

// StartReaper starts a background goroutine that deletes unclaimed archives
// whose age exceeds the store TTL. It runs until ctx is cancelled.
func (s *Store) StartReaper(ctx context.Context) {
	if s.ttl == 0 {
		return
	}
	go func() {
		// Check at half the TTL interval so entries are reaped promptly.
		ticker := time.NewTicker(s.ttl / 2)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reap()
			}
		}
	}()
}

// reap removes all entries older than the store TTL.
func (s *Store) reap() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, e := range s.entries {
		if now.Sub(e.storedAt) > s.ttl {
			zap.L().Info("archiver: reaping unclaimed archive",
				zap.String("runtime_id", id.String()),
				zap.String("file", e.filePath),
				zap.Duration("age", now.Sub(e.storedAt)),
			)
			_ = os.Remove(e.filePath)
			delete(s.entries, id)
		}
	}
}

// CleanupOrphans removes any offtoon-archive-*.zip temp files left in the OS
// temp directory by a previous run that crashed before serving them.
// Call this once at startup, before serving requests.
func CleanupOrphans() {
	pattern := filepath.Join(os.TempDir(), "offtoon-archive-*.zip")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		zap.L().Warn("archiver: failed to glob orphaned archives", zap.Error(err))
		return
	}
	for _, path := range matches {
		if err := os.Remove(path); err != nil {
			zap.L().Warn("archiver: failed to remove orphaned archive",
				zap.String("path", path), zap.Error(err))
		} else {
			zap.L().Info("archiver: removed orphaned archive", zap.String("path", path))
		}
	}
}

// Put registers an archive temp file under the given runtime ID.
// If an entry already exists for that ID its old temp file is deleted first.
func (s *Store) Put(id uuid.UUID, filename string, filePath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.entries[id]; ok {
		_ = os.Remove(old.filePath)
	}
	s.entries[id] = entry{filename: filename, filePath: filePath, storedAt: time.Now()}
}

// Consume retrieves and removes the archive for the given runtime ID, returning
// an open *os.File ready to be streamed. The caller must close and delete the
// file after use: defer f.Close(); defer os.Remove(f.Name()).
func (s *Store) Consume(id uuid.UUID) (filename string, f *os.File, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return "", nil, fmt.Errorf("archiver: no archive found for runtime %s", id)
	}
	delete(s.entries, id)

	f, err = os.Open(e.filePath)
	if err != nil {
		_ = os.Remove(e.filePath)
		return "", nil, fmt.Errorf("archiver: open archive file: %w", err)
	}
	return e.filename, f, nil
}

// Delete removes the archive entry and its temp file for the given ID.
// It is a no-op if the ID is not present.
func (s *Store) Delete(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[id]; ok {
		_ = os.Remove(e.filePath)
		delete(s.entries, id)
	}
}
