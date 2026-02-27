package toon

import "errors"

// ErrNotFound is returned (wrapped) when a client finds no result across all its URLs.
// Callers must use errors.Is(err, ErrNotFound) to distinguish "no result" from real failures.
var ErrNotFound = errors.New("not found")

// FetchErrorStep identifies which stage of the pipeline failed.
type FetchErrorStep int

const (
	FetchErrorSearch   FetchErrorStep = iota + 1 // failure during Search
	FetchErrorFetch                              // failure during Fetch
	FetchErrorDownload                           // failure during Download
	FetchErrorArchive                            // failure during archive building (image download / packaging)
	FetchErrorUnknown                            // catch-all
)

// FetchError is attached to the RuntimeToon so clients know exactly what went wrong.
type FetchError struct {
	Step    FetchErrorStep `json:"step"`
	Message string         `json:"message"`
}
