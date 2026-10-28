package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"sigismundo/internal/catalog"
	"sigismundo/internal/config"
	"sigismundo/internal/httpapi"
	"sigismundo/internal/logger"
	"sigismundo/internal/session"
)

// set through -ldflags
var Version string

func main() {
	logger.Infof("Version: %s", Version)

	// local: no Discord, anyone reaching the API can get a token for any user
	// (open the Activity at /?user=1&name=me).
	devAuth := config.DevAuth
	if len(os.Args) > 1 && os.Args[1] == "local" {
		logger.Warnf("local mode: no Discord, anyone reaching %s can get a token for any user", config.ServerAddress)
		devAuth = true
	} else if config.ClientID == "" || config.ClientSecret == "" {
		logger.Fatalf("CLIENT_ID and CLIENT_SECRET must be set")
	}

	if err := os.MkdirAll(config.DataDir, 0o755); err != nil {
		logger.Fatalf("data dir: %v", err)
	}
	cat, missing, err := catalog.Load(config.CatalogPath)
	if err != nil {
		logger.Warnf("catalog %s: %v (no games available)", config.CatalogPath, err)
		cat = &catalog.Catalog{}
	}
	for _, id := range missing {
		logger.Infof("catalog: %s skipped, exe not found", id)
	}
	logger.Infof("catalog: %d entries", len(cat.Entries))

	sessions := session.NewManager(cat)
	stop := make(chan struct{})
	go sessions.Run(stop)
	go httpapi.NewServer(config.ClientID, devAuth, sessions).Start()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()
	logger.Infof("Shutting down")
	close(stop)
	sessions.Shutdown()
}
