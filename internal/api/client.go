// Package api defines the interface every external API client must satisfy, plus
// the BaseClient helper that provides the shared URL-fallback loop and HTTP tooling.
package api

import (
	"context"

	"github.com/Zapharaos/offtoon-backend/internal/toon"
)

// -----------------------------------------------------------------------
// Per-operation parameter interfaces
// -----------------------------------------------------------------------

// FetchParams is implemented by each client's own fetch-request struct.
// Using a named interface (rather than `any`) lets callers be explicit about
// what they are passing while still allowing per-client type freedom.
type FetchParams interface {
	// clientName is used by the registry to route to the right client.
	ClientName() string
}

// DownloadParams is implemented by each client's own download-request struct.
type DownloadParams interface {
	ClientName() string
}

// -----------------------------------------------------------------------
// Client interface
// -----------------------------------------------------------------------

// Client is the contract every API source must implement.
//
// URL fallback semantics
//
//	Each implementation receives its URL list via BaseClient.URLs().
//	It MUST iterate over them in order, stopping at the first one that
//	returns usable data.  When none of the URLs yields data it MUST
//	return (nil, toon.ErrNotFound) – never a "real" error – so that the
//	registry can correctly classify the outcome.
//
// Rate-limiting
//
//	Implementations are expected to embed / use the throttle.Throttler
//	provided by BaseClient so that every outgoing HTTP request goes
//	through the shared rate-limiting and retry machinery.
type Client interface {
	// Name returns the unique identifier of this source (e.g. "webtoons", "mangadex").
	Name() string

	// Search returns toons whose title matches query.
	// Returns (nil, toon.ErrNotFound) when nothing was found across all URLs.
	Search(ctx context.Context, query string) ([]toon.SearchResult, error)

	// NewFetchParams builds the source-specific FetchParams for the given slug.
	NewFetchParams(slug string) FetchParams

	// Fetch returns the full Toon details for the given source-specific params.
	// Returns (nil, toon.ErrNotFound) when the toon was not found on any URL.
	Fetch(ctx context.Context, params FetchParams) (*toon.Toon, error)

	// NewDownloadParams builds the source-specific DownloadParams for the given slug and chapter IDs.
	NewDownloadParams(slug string, chapterIDs []string) DownloadParams

	// Download returns the chapters (with pages) described by the source-specific params.
	// Returns (nil, toon.ErrNotFound) when nothing was found on any URL.
	Download(ctx context.Context, params DownloadParams) ([]toon.Chapter, error)
}
