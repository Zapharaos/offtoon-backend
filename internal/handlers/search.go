package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/handlers/render"
)

// searchRequest is the POST body for the search endpoint.
type searchRequest struct {
	// Input is the search query string.
	Input string `json:"input"`

	// Sources is the list of API clients to query.
	// Valid values: "asura".
	// Must contain at least one entry.
	Sources []api.Source `json:"sources"`
}

// validate returns an error describing the first problem found, or nil.
func (req *searchRequest) validate() error {
	if strings.TrimSpace(req.Input) == "" {
		return fmt.Errorf("input must not be empty")
	}
	if len(req.Sources) == 0 {
		return fmt.Errorf("sources must contain at least one entry")
	}

	seen := make(map[api.Source]struct{}, len(req.Sources))
	for _, src := range req.Sources {
		if !api.ValidSource(src) {
			return fmt.Errorf("unknown source %q", src)
		}
		if _, dup := seen[src]; dup {
			return fmt.Errorf("duplicate source %q in sources", src)
		}
		seen[src] = struct{}{}
	}

	return nil
}

// Search handles POST /api/v1/search
//
//	@Summary		Search for toons
//	@Description	Searches for toons matching the input query across the specified API sources.
//	@Tags			toon
//	@Accept			json
//	@Produce		json
//	@Param			body	body		searchRequest			true	"Search request"
//	@Success		200		{array}		toon.SearchResult
//	@Failure		400		{object}	render.ErrorResponse	"Invalid request body or parameters"
//	@Failure		500		{object}	render.ErrorResponse	"Internal server error"
//	@Router			/api/v1/search [post]
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		render.BadRequest(w, r, fmt.Errorf("invalid JSON body: %w", err))
		return
	}

	if err := req.validate(); err != nil {
		render.BadRequest(w, r, err)
		return
	}

	results, err := h.reg.SearchSources(r.Context(), strings.TrimSpace(req.Input), req.Sources)
	if err != nil {
		render.Error(w, r, err, "search failed")
		return
	}

	render.JSON(w, r, results)
}
