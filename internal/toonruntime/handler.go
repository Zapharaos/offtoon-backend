package toonruntime

import (
	"context"
	"net/http"
	"sync"

	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/pkg/supervisor"
	"github.com/Zapharaos/offtoon-backend/pkg/wsruntime"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

type Handler struct {
	toons       map[uuid.UUID]*RuntimeToon // Direct lookup by ID
	Upgrader    *websocket.Upgrader
	ErrorLogger *supervisor.AsyncErrorLogger

	wg    *sync.WaitGroup
	mutex sync.RWMutex
}

// NewHandler creates a new handler
func NewHandler(ctx context.Context) *Handler {
	return &Handler{
		toons: make(map[uuid.UUID]*RuntimeToon),
		mutex: sync.RWMutex{},
		wg:    &sync.WaitGroup{},
		Upgrader: &websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		ErrorLogger: supervisor.NewAsyncErrorLogger(ctx, 1000),
	}
}

// RunToon runs a runtime toon
func (h *Handler) RunToon(t toon.Toon) *RuntimeToon {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	// Create a new runtime
	rt := NewRuntimeToon(&t, RuntimeOptionsFromConfig(), h.wg, h.ErrorLogger)

	// Set the runtime toon end callback
	rt.onEnd = h.onRuntimeToonEnd

	// Start the runtime
	rt.Start()

	// Add the runtime toon to the handler
	h.toons[rt.ID] = rt

	zap.L().Info("Started new runtime toon",
		zap.String("runtime_id", rt.ID.String()),
	)

	return rt
}

// GetRuntimeToon gets a runtime toon from the handler
func (h *Handler) GetRuntimeToon(id uuid.UUID) *RuntimeToon {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	return h.toons[id]
}

// RemoveRuntimeToon removes a runtime toon from the handler
func (h *Handler) RemoveRuntimeToon(id uuid.UUID) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	if _, ok := h.toons[id]; ok {
		delete(h.toons, id)
		zap.L().Info("Removed runtime toon",
			zap.String("runtime_id", id.String()),
		)
	}
}

// PushChange pushes a change to the runtime toon, if it exists
func (h *Handler) PushChange(rsId, changedId uuid.UUID, dType DataType, reason DataChangeReason) {
	if rs := h.GetRuntimeToon(rsId); rs != nil {
		rs.PushChange(dataChange{
			Id:     changedId,
			Type:   dType,
			Reason: reason,
		})
	}
}

// PushBatchProgress pushes batch progress updates to the runtime toon
// This is specifically for incremental processing updates heavy fetching
func (h *Handler) PushBatchProgress(rsId uuid.UUID, dType DataType, progress wsruntime.Progress) {
	if rs := h.GetRuntimeToon(rsId); rs != nil {
		rs.PushChange(dataChange{
			Id:       uuid.Nil, // No specific entity ID for batch progress
			Type:     dType,
			Reason:   DataTypeProgress,
			Progress: progress,
		})
	}
}

// Shutdown shuts down the handler
func (h *Handler) Shutdown() {
	h.mutex.Lock()

	for _, s := range h.toons {
		select {
		case s.done <- struct{}{}:
		default:
		}
	}

	h.mutex.Unlock()

	h.wg.Wait()
}

// onRuntimeToonEnd is called when a runtime toon ends
func (h *Handler) onRuntimeToonEnd(id uuid.UUID) {
	h.RemoveRuntimeToon(id)
}

// logWarning logs a warning-level error to the async error logger
// Used for non-critical errors that don't stop execution
func (h *Handler) logWarning(id uuid.UUID, scope string, err error) {
	if h.ErrorLogger == nil || err == nil {
		return
	}

	h.ErrorLogger.LogError(supervisor.Error{
		Id:       id,
		Scope:    scope,
		Message:  err.Error(),
		Severity: "warning",
	})
}

// logError logs an error-level error to the async error logger
// Used for errors that may impact functionality but allow continued operation
func (h *Handler) logError(id uuid.UUID, scope string, err error) {
	if h.ErrorLogger == nil || err == nil {
		return
	}

	h.ErrorLogger.LogError(supervisor.Error{
		Id:       id,
		Scope:    scope,
		Message:  err.Error(),
		Severity: "error",
	})
}

// logCriticalError logs a critical-level error to the async error logger
// Used for fatal errors that stop execution or cause operation failure
func (h *Handler) logCriticalError(id uuid.UUID, scope string, err error) {
	if h.ErrorLogger == nil || err == nil {
		return
	}

	h.ErrorLogger.LogError(supervisor.Error{
		Id:       id,
		Scope:    scope,
		Message:  err.Error(),
		Severity: "critical",
	})
}
