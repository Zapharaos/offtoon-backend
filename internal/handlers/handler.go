package handlers

import (
	"github.com/Zapharaos/offtoon-backend/internal/toonruntime"
)

type Handler struct {
	trh *toonruntime.Handler
}

// NewHandler creates a new handler wrapping both the set and search runtime handlers
func NewHandler(toonHandler *toonruntime.Handler) *Handler {
	return &Handler{
		trh: toonHandler,
	}
}
