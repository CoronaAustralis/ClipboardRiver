package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/clipboardriver/cb_river_server/internal/app"
	"github.com/clipboardriver/cb_river_server/internal/config"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "", "JSON config file path (defaults to ./config.json)")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()
	if flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	if *showVersion {
		fmt.Printf("Clipboard River Server %s\n", version)
		return
	}

	cfg, err := config.LoadFile(*configPath)
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
