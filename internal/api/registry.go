package api

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/pkg/workerpool"
	"github.com/Zapharaos/offtoon-backend/pkg/wsruntime"
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
// customURLs maps a Source to a single extra base URL that is prepended to
// that client's normal URL list for this request only.
//
// Unknown sources are skipped with a warning.
// Sources returning toon.ErrNotFound are silently skipped.
// Real errors are logged and skipped (best-effort).
// Returns an empty slice (not an error) when nothing is found anywhere.
func (r *Registry) SearchSources(ctx context.Context, query string, sources []Source, customURLs map[Source]string) ([]toon.SearchResult, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("api.Registry.SearchSources: no sources requested")
	}

	// Resolve sources to concrete clients up-front, skip unknowns.
	type searchJob struct {
		client   Client
		extraURL string
	}

	jobs := make([]searchJob, 0, len(sources))
	for _, src := range sources {
		c, err := r.Client(string(src))
		if err != nil {
			zap.L().Warn("SearchSources: unknown source, skipping",
				zap.String("source", string(src)))
			continue
		}
		jobs = append(jobs, searchJob{client: c, extraURL: customURLs[src]})
	}

	if len(jobs) == 0 {
		return nil, fmt.Errorf("api.Registry.SearchSources: none of the requested sources are registered")
	}

	var all []toon.SearchResult

	// Worker function: process a single inventory item
	workerFunc := func(ctx context.Context, job searchJob) ([]toon.SearchResult, error) {
		var extraURLs []string
		if job.extraURL != "" {
			extraURLs = []string{job.extraURL}
		}
		results, err := job.client.SearchWithExtraURLs(ctx, query, extraURLs)
		if err != nil {
			if errors.Is(err, toon.ErrNotFound) {
				return nil, nil // not found is not an error
			}
			return nil, err
		}
		return results, nil
	}

	pool := workerpool.NewPool(ctx, workerpool.NewConfigOptimal(len(jobs), -1), workerFunc, nil)

	// Collect individual results as they arrive from each source.
	pool.SetResultHandler(func(results []toon.SearchResult) error {
		all = append(all, results...)
		return nil
	})

	// Non-fatal: log errors from individual sources but keep going.
	pool.SetErrorHandler(func(err error) {
		zap.L().Error("SearchSources: source error", zap.Error(err))
	})

	if err := pool.Process(jobs); err != nil {
		// Only a hard error (context cancelled, result handler failure) reaches here.
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

// FetchSource retrieves full toon details from the client identified by source,
// using slug as the toon identifier. extraURLs are prepended to the client's
// configured URL list for this call only (first one that succeeds wins).
//
// Returns toon.ErrNotFound when the toon could not be found on any URL.
func (r *Registry) FetchSource(ctx context.Context, source Source, slug string, extraURLs []string) (*toon.Toon, error) {
	c, err := r.Client(string(source))
	if err != nil {
		return nil, fmt.Errorf("api.Registry.FetchSource: %w", err)
	}
	return c.FetchWithExtraURLs(ctx, slug, extraURLs)
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

// DownloadSource downloads individual chapters from the given source concurrently,
// using a worker pool. After each completed batch of chapters, onProgress is called
// with the current wsruntime.Progress snapshot so the caller can push it to
// connected WebSocket clients.
//
// source identifies the API client; extraURLs are prepended to its URL list.
// slug is the toon slug; chapterIDs is the list of chapter IDs to download.
// onProgress may be nil if progress notifications are not needed.
//
// Returns the full list of downloaded chapters or toon.ErrNotFound when none
// of the requested chapters were found.
func (r *Registry) DownloadSource(
	ctx context.Context,
	source Source,
	slug string,
	chapterIDs []string,
	extraURLs []string,
	onProgress func(wsruntime.Progress),
) ([]toon.Chapter, error) {
	c, err := r.Client(string(source))
	if err != nil {
		return nil, fmt.Errorf("api.Registry.DownloadSource: %w", err)
	}

	total := len(chapterIDs)
	if total == 0 {
		return nil, fmt.Errorf("api.Registry.DownloadSource: no chapter IDs provided")
	}

	progress := wsruntime.NewProgress(total, workerpool.NewConfigOptimal(total, -1).BatchSize)

	type job struct {
		chapterID string
	}

	workerFunc := func(ctx context.Context, j job) (toon.Chapter, error) {
		chapters, err := c.DownloadWithExtraURLs(ctx, slug, []string{j.chapterID}, extraURLs)
		if err != nil {
			return toon.Chapter{}, err
		}
		if len(chapters) == 0 {
			return toon.Chapter{}, toon.ErrNotFound
		}
		return chapters[0], nil
	}

	var all []toon.Chapter

	batchHandler := func(batch []toon.Chapter) error {
		for _, ch := range batch {
			progress.AddItem(ch)
		}
		all = append(all, batch...)

		if onProgress != nil {
			progress.PrepareForSend()
			onProgress(*progress)
			progress.CompleteBatch()
		}
		return nil
	}

	jobs := make([]job, total)
	for i, id := range chapterIDs {
		jobs[i] = job{chapterID: id}
	}

	cfg := workerpool.NewConfigOptimal(total, -1)
	pool := workerpool.NewPool(ctx, cfg, workerFunc, batchHandler)

	pool.SetErrorHandler(func(err error) {
		if !errors.Is(err, toon.ErrNotFound) {
			zap.L().Error("DownloadSource: chapter download error", zap.Error(err))
		}
	})

	if err := pool.Process(jobs); err != nil {
		return nil, fmt.Errorf("api.Registry.DownloadSource: %w", err)
	}

	if len(all) == 0 {
		return nil, toon.ErrNotFound
	}

	return all, nil
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
