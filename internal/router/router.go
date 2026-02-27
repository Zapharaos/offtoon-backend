package router

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Zapharaos/offtoon-backend/internal/api"
	"github.com/Zapharaos/offtoon-backend/internal/handlers"
	"github.com/Zapharaos/offtoon-backend/internal/toonruntime"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

type Router struct {
	Router  *chi.Mux
	handler *handlers.Handler
}

func New(toonHandler *toonruntime.Handler, registry *api.Registry) *Router {
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
		handler: handlers.NewHandler(toonHandler, registry),
	}

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/search", router.handler.Search)
		r.Post("/fetch", router.handler.Fetch)
		r.Post("/download", router.handler.Download)
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
