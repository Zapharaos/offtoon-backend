package api

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/pkg/workerpool"
	"go.uber.org/zap"
)

// -----------------------------------------------------------------------
// Registry
// -----------------------------------------------------------------------

// Registry holds all registered API clients and exposes unified operations
// that fan-out across them (search) or route to a specific one (fetch, download).
type Registry struct {
	mu      sync.RWMutex
	clients map[string]Client
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{clients: make(map[string]Client)}
}

// Register adds a client. Panics if client is nil or its Name() is empty.
// Registering the same name twice overwrites the previous entry with a warning.
func (r *Registry) Register(c Client) {
	if c == nil {
		panic("api.Registry.Register: nil client")
	}
	if c.Name() == "" {
		panic("api.Registry.Register: client name must not be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.clients[c.Name()]; exists {
		zap.L().Warn("API registry: overwriting existing client", zap.String("client", c.Name()))
	}
	r.clients[c.Name()] = c
	zap.L().Info("API registry: client registered", zap.String("client", c.Name()))
}

// Client returns the named client, or an error if it was not registered.
func (r *Registry) Client(name string) (Client, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	c, ok := r.clients[name]
	if !ok {
		return nil, fmt.Errorf("api.Registry: unknown client %q", name)
	}
	return c, nil
}

// Names returns the names of all registered clients in no particular order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.clients))
	for n := range r.clients {
		names = append(names, n)
	}
	return names
}

// -----------------------------------------------------------------------
// Operations
// -----------------------------------------------------------------------

// Search fans the query out to every registered client and merges results.
//
// Individual clients that return toon.ErrNotFound are silently skipped.
// Clients that return real errors are logged and skipped (best-effort).
// The combined slice may be empty when nothing was found anywhere – that is
// NOT treated as an error.
func (r *Registry) Search(ctx context.Context, query string) ([]toon.SearchResult, error) {
	clients := r.snapshot()
	if len(clients) == 0 {
		return nil, errors.New("api.Registry: no clients registered")
	}

	var results []toon.SearchResult
	for _, c := range clients {
		res, err := c.Search(ctx, query)
		if err != nil {
			if errors.Is(err, toon.ErrNotFound) {
				continue // nothing on this source, carry on
			}
			// Real error – log and continue so the rest of the sources still run.
			zap.L().Error("API client search error",
				zap.String("client", c.Name()),
				zap.Error(err),
			)
			continue
		}
		results = append(results, res...)
	}
	return results, nil
}

// SearchSources fans the query out to the requested sources only, using a
// worker pool so each source is queried concurrently.
//
// Unknown sources are skipped with a warning.
// Sources returning toon.ErrNotFound are silently skipped.
// Real errors are logged and skipped (best-effort).
// Returns an empty slice (not an error) when nothing is found anywhere.
func (r *Registry) SearchSources(ctx context.Context, query string, sources []Source) ([]toon.SearchResult, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("api.Registry.SearchSources: no sources requested")
	}

	// Resolve sources to concrete clients up-front, skip unknowns.
	clients := make([]Client, 0, len(sources))
	for _, src := range sources {
		c, err := r.Client(string(src))
		if err != nil {
			zap.L().Warn("SearchSources: unknown source, skipping",
				zap.String("source", string(src)))
			continue
		}
		clients = append(clients, c)
	}

	if len(clients) == 0 {
		return nil, fmt.Errorf("api.Registry.SearchSources: none of the requested sources are registered")
	}

	var all []toon.SearchResult

	workerFunc := func(ctx context.Context, c Client) ([]toon.SearchResult, error) {
		results, err := c.Search(ctx, query)
		if err != nil {
			if errors.Is(err, toon.ErrNotFound) {
				return nil, nil // not found is not an error
			}
			return nil, err
		}
		return results, nil
	}

	pool := workerpool.NewPool(ctx, workerpool.NewConfigOptimal(len(clients), -1), workerFunc, nil)

	pool.SetResultHandler(func(results []toon.SearchResult) error {
		all = append(all, results...)
		return nil
	})

	pool.SetErrorHandler(func(err error) {
		zap.L().Error("SearchSources: source error", zap.Error(err))
	})

	if err := pool.Process(clients); err != nil {
		return nil, fmt.Errorf("api.Registry.SearchSources: %w", err)
	}

	return all, nil
}

// Fetch retrieves full toon details from the client named by params.ClientName().
//
// Returns toon.ErrNotFound (unwrapped with errors.Is) when the toon could not
// be found on any of the client's URLs.
func (r *Registry) Fetch(ctx context.Context, params FetchParams) (*toon.Toon, error) {
	c, err := r.Client(params.ClientName())
	if err != nil {
		return nil, err
	}
	return c.Fetch(ctx, params)
}

// FetchSource retrieves full toon details from the client identified by source
// using slug as the toon identifier.
//
// Returns toon.ErrNotFound when the toon could not be found on any URL.
func (r *Registry) FetchSource(ctx context.Context, source Source, slug string) (*toon.Toon, error) {
	c, err := r.Client(string(source))
	if err != nil {
		return nil, fmt.Errorf("api.Registry.FetchSource: %w", err)
	}
	return c.Fetch(ctx, c.NewFetchParams(slug))
}

// Download retrieves chapters from the client named by params.ClientName().
//
// Returns toon.ErrNotFound when nothing was found on any URL.
func (r *Registry) Download(ctx context.Context, params DownloadParams) ([]toon.Chapter, error) {
	c, err := r.Client(params.ClientName())
	if err != nil {
		return nil, err
	}
	return c.Download(ctx, params)
}

// ResolveChapterPages resolves the page list (image URLs) for a single chapter
// of the given source. It is the per-chapter metadata step that the archiver
// calls lazily inside each chapter worker, so page resolution pipelines with
// image downloading instead of running as a separate up-front phase.
//
// source identifies the API client; slug is the toon slug; chapterID is the
// chapter identifier. Returns toon.ErrNotFound when the chapter could not be
// found on any of the client's URLs (Download handles URL fallback internally).
func (r *Registry) ResolveChapterPages(ctx context.Context, source Source, slug, chapterID string) ([]toon.Page, error) {
	c, err := r.Client(string(source))
	if err != nil {
		return nil, fmt.Errorf("api.Registry.ResolveChapterPages: %w", err)
	}

	chapters, err := c.Download(ctx, c.NewDownloadParams(slug, []string{chapterID}))
	if err != nil {
		return nil, err
	}
	if len(chapters) == 0 {
		return nil, toon.ErrNotFound
	}
	return chapters[0].Pages, nil
}

// -----------------------------------------------------------------------
// Internal helpers
// -----------------------------------------------------------------------

// snapshot returns a stable copy of the client slice under a read lock.
func (r *Registry) snapshot() []Client {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Client, 0, len(r.clients))
	for _, c := range r.clients {
		out = append(out, c)
	}
	return out
}
