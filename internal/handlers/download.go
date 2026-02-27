package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/handlers/render"
	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/internal/toonruntime"
	"github.com/Zapharaos/offtoon-backend/pkg/archiver"
	"github.com/Zapharaos/offtoon-backend/pkg/wsruntime"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
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

	// Format controls the per-chapter output format.
	// Accepted values: "pdf" (default), "cbz", "images".
	Format string `json:"format"`
}

// downloadResponse is returned immediately after the download job is accepted.
type downloadResponse struct {
	// RuntimeID is the WebSocket runtime ID the caller should connect to
	// in order to receive live progress packets.
	RuntimeID string `json:"runtime_id"`
}

// validate returns an error describing the first problem found, or nil.
func (req *downloadRequest) validate() error {
	if !api.ValidSource(req.Source) {
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
//	@Description	Starts an async download job for the requested chapters of a toon. Returns a runtime_id immediately. Connect to the WebSocket endpoint with that ID to receive live progress batches. When the PacketCompleted is received, fetch the archive via GET /api/v1/download/{runtimeID}/archive.
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

	format := archiver.ParseFormat(req.Format)
	slug := strings.TrimSpace(req.Slug)

	rt := h.trh.RunToon(toon.Toon{
		Source: req.Source,
	})

	go func() {
		// Use a detached context — r.Context() is cancelled as soon as the HTTP
		// response is written, which would abort the download immediately.
		ctx := context.Background()

		defer func() {
			if r := recover(); r != nil {
				err := fmt.Errorf("panic: %v", r)
				zap.L().Error("Download: goroutine panic",
					zap.String("runtime_id", rt.ID.String()),
					zap.String("source", string(req.Source)),
					zap.String("slug", slug),
					zap.Any("panic", r),
				)
				rt.SetFetchError(&toon.FetchError{Step: toon.FetchErrorArchive, Message: err.Error()})
				h.trh.PushChange(rt.ID, rt.ID, toonruntime.DataTypeChapter, toonruntime.DataTypeFailed)
			}
		}()

		chapters, err := h.reg.DownloadSource(
			ctx,
			req.Source,
			slug,
			req.ChapterIDs,
			func(progress wsruntime.Progress) {
				progress.Phase = toonruntime.ProgressPhaseChapters
				h.trh.PushBatchProgress(rt.ID, toonruntime.DataTypeChapter, progress)
			},
		)
		if err != nil {
			zap.L().Error("Download: chapter download failed",
				zap.String("runtime_id", rt.ID.String()),
				zap.String("source", string(req.Source)),
				zap.String("slug", slug),
				zap.Error(err),
			)
			rt.SetFetchError(&toon.FetchError{Step: toon.FetchErrorDownload, Message: err.Error()})
			h.trh.PushChange(rt.ID, rt.ID, toonruntime.DataTypeChapter, toonruntime.DataTypeFailed)
			return
		}

		// Build the archive in the background after all chapters are fetched.
		zap.L().Info("Download: starting archive build",
			zap.String("runtime_id", rt.ID.String()),
			zap.String("slug", slug),
			zap.Int("chapters", len(chapters)),
			zap.String("format", string(format)),
		)

		// Send a progress packet every time a page image is downloaded during
		// the archive build. This prevents the WebSocket from going silent for
		// 30+ seconds while images are being fetched, which would trigger the
		// frontend inactivity timeout.
		archiveData, err := archiver.Build(chapters, slug, format, nil, func(pagesDone, pagesTotal int) {
			h.trh.PushBatchProgress(rt.ID, toonruntime.DataTypeChapter, wsruntime.Progress{
				Phase: toonruntime.ProgressPhaseImages,
				Total: pagesTotal,
				Done:  pagesDone,
				Items: []any{},
			})
		})
		if err != nil {
			zap.L().Error("Download: archive build failed",
				zap.String("runtime_id", rt.ID.String()),
				zap.String("source", string(req.Source)),
				zap.String("slug", slug),
				zap.Error(err),
			)
			rt.SetFetchError(&toon.FetchError{Step: toon.FetchErrorArchive, Message: err.Error()})
			h.trh.PushChange(rt.ID, rt.ID, toonruntime.DataTypeChapter, toonruntime.DataTypeFailed)
			return
		}

		filename := fmt.Sprintf("%s.zip", slug)
		h.archives.Put(rt.ID, filename, archiveData)

		archiveURL := fmt.Sprintf("/api/v1/download/%s/archive", rt.ID.String())
		h.trh.PushCompleted(rt.ID, toonruntime.DataTypeChapter, len(chapters), archiveURL)
	}()

	render.Accepted(w, r, downloadResponse{RuntimeID: rt.ID.String()})
}

// DownloadArchive handles GET /api/v1/download/{runtimeID}/archive
//
//	@Summary		Retrieve the assembled archive for a completed download job
//	@Description	Returns the ZIP archive built after a download job completes. The archive URL is provided in the PacketCompleted WebSocket message. This endpoint can only be called once per job — the file is removed from memory after it is served.
//	@Tags			toon
//	@Produce		application/zip
//	@Param			runtimeID	path		string	true	"Runtime ID returned by POST /download"
//	@Success		200			{file}		binary	"ZIP archive"
//	@Failure		400			{object}	render.ErrorResponse	"Invalid runtime ID"
//	@Failure		404			{object}	render.ErrorResponse	"Archive not found or already consumed"
//	@Router			/api/v1/download/{runtimeID}/archive [get]
func (h *Handler) DownloadArchive(w http.ResponseWriter, r *http.Request) {
	rawID := chi.URLParam(r, "runtimeID")
	id, err := uuid.Parse(rawID)
	if err != nil {
		render.BadRequest(w, r, fmt.Errorf("invalid runtime ID %q", rawID))
		return
	}

	filename, data, err := h.archives.Consume(id)
	if err != nil {
		render.NotFound(w, r, fmt.Errorf("archive not found or already consumed"))
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.WriteHeader(http.StatusOK)
	w.Write(data) //nolint:errcheck
}

// DownloadWS handles GET /api/v1/download/{runtimeID}/ws
//
//	@Summary		Connect to a download job via WebSocket
//	@Description	Upgrades the HTTP connection to a WebSocket and streams live progress packets for the given download runtime. \n\nPacket sequence:\n  1. PacketInit — connection confirmed.\n  2. PacketProgress (phase="chapters") — chapter metadata scraped; items[] contains chapter objects with page URLs.\n  3. PacketProgress (phase="images") — page images being downloaded; items=[], total=image count, done increments per image.\n  4. PacketCompleted — archive ready; fetch it via archive_url.\n  OR PacketFatal — something went wrong; step indicates the failing stage.
//	@Tags			toon
//	@Produce		json
//	@Param			runtimeID	path		string					true	"Runtime ID returned by POST /download"
//	@Success		101			{object}	toonruntime.packetSpec	"WebSocket upgrade; subsequent messages are JSON packets (PacketInit / PacketProgress / PacketCompleted / PacketFatal)"
//	@Failure		400			{object}	render.ErrorResponse	"Invalid runtime ID"
//	@Failure		404			{object}	render.ErrorResponse	"Runtime not found"
//	@Router			/api/v1/download/{runtimeID}/ws [get]
func (h *Handler) DownloadWS(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseParamUUID(w, r, "runtimeID")
	if !ok {
		return
	}

	rt := h.trh.GetRuntimeToon(id)
	if rt == nil {
		render.NotFound(w, r, fmt.Errorf("runtime %s not found", id))
		return
	}

	conn, err := h.trh.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrader already wrote the error response.
		return
	}

	toonruntime.NewClient(rt, conn, uuid.New())
}
