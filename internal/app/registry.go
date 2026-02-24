package app

import (
	"fmt"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/api/clients/asura"
	"github.com/Zapharaos/offtoon-backend/internal/api/clients/nato"
	"go.uber.org/zap"
)

// SetupRegistry builds every API client from viper config and registers
// them into a new Registry.  Must be called after initializeConfig().
func SetupRegistry() (*api.Registry, error) {
	reg := api.NewRegistry()

	// ── Asura ────────────────────────────────────────────────────────────
	asuraClient, err := asura.New()
	if err != nil {
		return nil, fmt.Errorf("registry setup: %w", err)
	}
	reg.Register(asuraClient)

	// ── Nato ─────────────────────────────────────────────────────────────
	natoClient, err := nato.New()
	if err != nil {
		return nil, fmt.Errorf("registry setup: %w", err)
	}
	reg.Register(natoClient)

	zap.L().Info("API registry ready", zap.Strings("clients", reg.Names()))
	return reg, nil
}
