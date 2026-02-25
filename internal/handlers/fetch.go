package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/handlers/render"
	"github.com/Zapharaos/offtoon-backend/internal/toon"
)

// fetchRequest is the POST body for the fetch endpoint.
type fetchRequest struct {
	// Source is the API client to use (e.g. "asura", "nato").
	Source api.Source `json:"source"`

	// Slug is the source-specific toon slug (e.g. "0a59965f-some-toon-slug").
	Slug string `json:"slug"`
}

// validate returns an error describing the first problem found, or nil.
func (req *fetchRequest) validate() error {
	if !req.Source.Valid() {
		return fmt.Errorf("unknown source %q", req.Source)
	}
	if strings.TrimSpace(req.Slug) == "" {
		return fmt.Errorf("slug must not be empty")
	}
	return nil
}

// Fetch handles POST /api/v1/fetch
//
//	@Summary		Fetch a toon
//	@Description	Fetches the full details of a toon from a specific API source using the provided slug.
//	@Tags			toon
//	@Accept			json
//	@Produce		json
//	@Param			body	body		fetchRequest	true	"Fetch request"
//	@Success		200		{object}	toon.Toon
//	@Failure		400		{object}	render.ErrorResponse	"Invalid request body or parameters"
//	@Failure		404		{object}	render.ErrorResponse	"Toon not found"
//	@Failure		500		{object}	render.ErrorResponse	"Internal server error"
//	@Router			/api/v1/fetch [post]
func (h *Handler) Fetch(w http.ResponseWriter, r *http.Request) {
	var req fetchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		render.BadRequest(w, r, fmt.Errorf("invalid JSON body: %w", err))
		return
	}

	if err := req.validate(); err != nil {
		render.BadRequest(w, r, err)
		return
	}

	result, err := h.reg.FetchSource(r.Context(), req.Source, strings.TrimSpace(req.Slug))
	if err != nil {
		if errors.Is(err, toon.ErrNotFound) {
			render.NotFound(w, r, fmt.Errorf("toon %q not found on source %q", req.Slug, req.Source))
			return
		}
		render.Error(w, r, err, "fetch failed")
		return
	}

	render.JSON(w, r, result)
}
