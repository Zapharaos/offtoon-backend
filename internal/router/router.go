package router

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/handlers"
	"github.com/Zapharaos/offtoon-backend/internal/toonruntime"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

type Router struct {
	Router  *chi.Mux
	handler *handlers.Handler
}

func New(ctx context.Context, toonHandler *toonruntime.Handler, registry *api.Registry) *Router {
	r := chi.NewRouter()

	// A good base middleware stack
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	r.Use(zapChiLogger(zap.L(), "router"))

	// Set a timeout value on the request context (ctx), that will signal
	// through ctx.Done() that the request has timed out and further
	// processing should be stopped.
	r.Use(middleware.Timeout(60 * time.Second))

	// Apply CORS middleware only if enabled in config
	if viper.GetBool("cors.enabled") {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins:   viper.GetStringSlice("cors.allowed_origins"),
			AllowedMethods:   viper.GetStringSlice("cors.allowed_methods"),
			AllowedHeaders:   viper.GetStringSlice("cors.allowed_headers"),
			ExposedHeaders:   []string{"Link"},
			AllowCredentials: viper.GetBool("cors.allow_credentials"),
			MaxAge:           viper.GetInt("cors.max_age"),
		}))
		zap.L().Info("CORS enabled with origins", zap.Strings("allowed_origins", viper.GetStringSlice("cors.allowed_origins")))
	} else {
		zap.L().Info("CORS disabled")
	}

	router := &Router{
		Router:  r,
		handler: handlers.NewHandler(ctx, toonHandler, registry),
	}

	// Build the global rate limiter if enabled. It is applied to the /api/v1
	// group only (not the health check), so uptime probes are never throttled.
	var globalLimiter func(http.Handler) http.Handler
	if viper.GetBool("rate_limit.enabled") {
		globalLimit := viper.GetInt("rate_limit.global.max_requests")
		globalWindow, err := time.ParseDuration(viper.GetString("rate_limit.global.window"))
		if err != nil {
			globalWindow = time.Minute
		}
		globalLimiter = httprate.LimitByIP(globalLimit, globalWindow)
		zap.L().Info("Rate limiting enabled",
			zap.Int("global_max_requests", globalLimit),
			zap.Duration("global_window", globalWindow),
		)
	} else {
		zap.L().Info("Rate limiting disabled")
	}

	// helper to build a per-route limiter from config key (falls back to global values)
	endpointLimiter := func(key string) func(http.Handler) http.Handler {
		if !viper.GetBool("rate_limit.enabled") {
			return func(next http.Handler) http.Handler { return next }
		}
		maxReq := viper.GetInt("rate_limit.endpoints." + key + ".max_requests")
		window, err := time.ParseDuration(viper.GetString("rate_limit.endpoints." + key + ".window"))
		if err != nil || maxReq == 0 {
			maxReq = viper.GetInt("rate_limit.global.max_requests")
			window, _ = time.ParseDuration(viper.GetString("rate_limit.global.window"))
		}
		return httprate.LimitByIP(maxReq, window)
	}

	// Health check for uptime monitors (e.g. Uptime Kuma). Kept outside /api/v1
	// and free of any rate limiting so probes are never throttled.
	r.Get("/health", router.handler.Health)
	r.Head("/health", router.handler.Health)

	r.Route("/api/v1", func(r chi.Router) {
		if globalLimiter != nil {
			r.Use(globalLimiter)
		}
		r.With(endpointLimiter("search")).Post("/search", router.handler.Search)
		r.With(endpointLimiter("fetch")).Post("/fetch", router.handler.Fetch)
		r.With(endpointLimiter("download")).Post("/download", router.handler.Download)
		r.Get("/download/{runtimeID}/archive", router.handler.DownloadArchive)
		r.Get("/download/{runtimeID}/ws", router.handler.DownloadWS)
	})

	return router
}

// PrintAllRoutes prints all routes to the console
func (router *Router) PrintAllRoutes() {
	walkFunc := func(method string, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		fmt.Printf("%s %s\n", method, route)
		return nil
	}

	if err := chi.Walk(router.Router, walkFunc); err != nil {
		fmt.Printf("Logging err: %s\n", err.Error())
	}
}
