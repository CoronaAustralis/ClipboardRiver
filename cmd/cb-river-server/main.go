package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/clipboardriver/cb_river_server/internal/app"
	"github.com/clipboardriver/cb_river_server/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("init app: %v", err)
	}
	if username, password, ok := application.InitialAdminCredentials(); ok {
		log.Printf("Initial admin password generated for %s: %s", username, password)
	}

	srv := &http.Server{
		Addr:         cfg.Server.ListenAddr,
		Handler:      application.Router(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("Clipboard River server listening on %s", cfg.Server.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
	if err := application.Close(); err != nil {
		log.Printf("app shutdown: %v", err)
	}
}
