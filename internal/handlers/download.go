package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/handlers/render"
	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/internal/toonruntime"
	"github.com/Zapharaos/offtoon-backend/pkg/wsruntime"
)

// downloadRequest is the POST body for the download endpoint.
type downloadRequest struct {
	// Source is the API client to use (e.g. "asura", "nato").
	Source api.Source `json:"source"`

	// Slug is the source-specific toon slug.
	Slug string `json:"slug"`

	// ChapterIDs is the list of chapter IDs to download.
	// Must contain at least one entry.
	ChapterIDs []string `json:"chapter_ids"`
}

// downloadResponse is returned immediately after the download job is accepted.
type downloadResponse struct {
	// RuntimeID is the WebSocket runtime ID the caller should connect to
	// in order to receive live progress packets.
	RuntimeID string `json:"runtime_id"`
}

// validate returns an error describing the first problem found, or nil.
func (req *downloadRequest) validate() error {
	if !req.Source.Valid() {
		return fmt.Errorf("unknown source %q", req.Source)
	}
	if strings.TrimSpace(req.Slug) == "" {
		return fmt.Errorf("slug must not be empty")
	}
	if len(req.ChapterIDs) == 0 {
		return fmt.Errorf("chapter_ids must contain at least one entry")
	}
	seen := make(map[string]struct{}, len(req.ChapterIDs))
	for _, id := range req.ChapterIDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("chapter_ids must not contain empty entries")
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("duplicate chapter_id %q in chapter_ids", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// Download handles POST /api/v1/download
//
//	@Summary		Download chapters of a toon
//	@Description	Starts an async download job for the requested chapters of a toon. Returns a runtime_id immediately. Connect to the WebSocket endpoint with that ID to receive live progress batches.
//	@Tags			toon
//	@Accept			json
//	@Produce		json
//	@Param			body	body		downloadRequest		true	"Download request"
//	@Success		202		{object}	downloadResponse	"Download job accepted; connect via WebSocket to track progress"
//	@Failure		400		{object}	render.ErrorResponse	"Invalid request body or parameters"
//	@Failure		500		{object}	render.ErrorResponse	"Internal server error"
//	@Router			/api/v1/download [post]
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	var req downloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		render.BadRequest(w, r, fmt.Errorf("invalid JSON body: %w", err))
		return
	}

	if err := req.validate(); err != nil {
		render.BadRequest(w, r, err)
		return
	}

	rt := h.trh.RunToon(toon.Toon{
		Source: string(req.Source),
	})

	go func() {
		ctx := r.Context()

		chapters, err := h.reg.DownloadSource(
			ctx,
			req.Source,
			strings.TrimSpace(req.Slug),
			req.ChapterIDs,
			func(progress wsruntime.Progress) {
				h.trh.PushBatchProgress(rt.ID, toonruntime.DataTypeChapter, progress)
			},
		)
		if err != nil {
			h.trh.PushChange(rt.ID, rt.ID, toonruntime.DataTypeChapter, toonruntime.DataTypeFailed)
			return
		}

		h.trh.PushCompleted(rt.ID, toonruntime.DataTypeChapter, len(chapters))
	}()

	render.Accepted(w, r, downloadResponse{RuntimeID: rt.ID.String()})
}
