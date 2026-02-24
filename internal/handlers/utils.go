package handlers

import (
	"fmt"
	"net/http"

	"github.com/Zapharaos/offtoon-backend/internal/handlers/render"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ParseParamUUIDSoft parses an uuid from the request parameters (using key parameter) without auto http status
func ParseParamUUIDSoft(r *http.Request, key string) (uuid.UUID, bool) {
	value := chi.URLParam(r, key)

	result, err := uuid.Parse(value)
	if err != nil {
		zap.L().Debug("Parse uuid", zap.String("key", key), zap.Error(err))
		return uuid.UUID{}, false
	}

	return result, true
}

// ParseParamUUID parses an uuid from the request parameters (using key parameter)
func ParseParamUUID(w http.ResponseWriter, r *http.Request, key string) (uuid.UUID, bool) {
	id, ok := ParseParamUUIDSoft(r, key)
	if !ok {
		render.BadRequest(w, r, fmt.Errorf("invalid %s", key))
	}
	return id, ok
}
