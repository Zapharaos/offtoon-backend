package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Zapharaos/offtoon-backend/internal/app"
	"github.com/Zapharaos/offtoon-backend/internal/router"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

var (
	// Version is the binary version + build number
	Version = ""
	// BuildDate is the date of build
	BuildDate = ""
)

//	@version		v1
//	@title			Offtoon API
//	@description	Offtoon API
//	@termsOfService	http://swagger.io/terms/

//	@contact.name	Zapharaos
//	@contact.url	https://matthieu-freitag.com
//	@contact.email	contact@matthieu-freitag.com

//	@host	localhost:3000

// @securityDefinitions.apikey	Bearer
// @in							header
// @name						Authorization
func main() {
	app.Init(Version, BuildDate)

	ctx, cancel := context.WithCancel(context.Background())
	_ = ctx
	//toonHandler := toonruntime.NewHandler(ctx)
	r := router.New( /*toonHandler*/ )

	// Get server configuration from config
	host := viper.GetString("server.host")
	port := viper.GetInt("server.port")
	serverAddr := fmt.Sprintf("%s:%d", host, port)

	srv := &http.Server{
		Addr:    serverAddr,
		Handler: r.Router,
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		var err error
		err = srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			zap.L().Fatal("server listen", zap.Error(err))
		}
	}()
	zap.L().Info("Server started", zap.String("addr", srv.Addr))

	<-done

	cancel()

	zap.L().Info("Gracefully shutting down server")
	zap.L().Info("If you want to force shutdown, press Ctrl+C again (not recommended)")
	go func() {
		<-done
		zap.L().Fatal("User forced shutdown")
	}()

	if err := srv.Shutdown(context.Background()); err != nil {
		zap.L().Fatal("Server shutdown failed", zap.Error(err))
	}

	//toonHandler.Shutdown()

	zap.L().Info("Server shutdown")
}
