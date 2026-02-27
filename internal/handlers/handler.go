package handlers

import (
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
func NewHandler(toonHandler *toonruntime.Handler, registry *api.Registry) *Handler {
	return &Handler{
		trh:      toonHandler,
		reg:      registry,
		archives: archiver.NewStore(),
	}
}
