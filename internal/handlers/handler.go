package handlers

import (
	"context"
	"time"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/toonruntime"
	"github.com/Zapharaos/offtoon-backend/pkg/archiver"
)

type Handler struct {
	trh      *toonruntime.Handler
	reg      *api.Registry
	archives *archiver.Store
}

// NewHandler creates a new handler wrapping both the toon runtime and the API registry.
// ctx is the application-level context; it is used to stop the archive reaper on shutdown.
func NewHandler(ctx context.Context, toonHandler *toonruntime.Handler, registry *api.Registry) *Handler {
	archiver.CleanupOrphans()

	store := archiver.NewStore(time.Hour)
	store.StartReaper(ctx)

	return &Handler{
		trh:      toonHandler,
		reg:      registry,
		archives: store,
	}
}
