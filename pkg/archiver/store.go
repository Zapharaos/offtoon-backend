// Package archiver – store.go
// ArchiveStore holds assembled ZIP archives in memory, keyed by the runtime ID
// that produced them. Each archive can be consumed exactly once (the entry is
// deleted after the first read so memory is reclaimed promptly).
package archiver

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// entry holds a ready-to-serve archive together with the filename that should
// be used in the Content-Disposition header.
type entry struct {
	filename string
	data     []byte
}

// Store is a thread-safe in-memory archive store.
type Store struct {
	mu      sync.Mutex
	entries map[uuid.UUID]entry
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{entries: make(map[uuid.UUID]entry)}
}

// Put stores an archive under the given runtime ID.
// Calling Put twice for the same ID overwrites the previous entry.
func (s *Store) Put(id uuid.UUID, filename string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[id] = entry{filename: filename, data: data}
}

// Consume retrieves and removes the archive for the given runtime ID.
// Returns an error if no archive exists for that ID.
func (s *Store) Consume(id uuid.UUID) (filename string, data []byte, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return "", nil, fmt.Errorf("archiver: no archive found for runtime %s", id)
	}
	delete(s.entries, id)
	return e.filename, e.data, nil
}

// Delete removes the archive for the given ID without returning it.
// It is a no-op if the ID is not present.
func (s *Store) Delete(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, id)
}
