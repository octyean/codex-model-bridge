package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"codex-bridge/internal/codexconfig"
	"codex-bridge/internal/config"
	"codex-bridge/internal/logging"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/server"
)

func main() {
	command := "serve"
	args := os.Args[1:]
	if len(args) > 0 && args[0] != "--config" && args[0] != "-config" {
		command = args[0]
		args = args[1:]
	}

	if command == "catalog" && len(args) > 0 && args[0] == "generate" {
		args = args[1:]
		command = "catalog generate"
	}
	if command == "config" && len(args) > 0 && args[0] == "check" {
		args = args[1:]
		command = "config check"
	}
	if command == "codex" && len(args) > 0 && args[0] == "configure" {
		args = args[1:]
		command = "codex configure"
	}

	flags := flag.NewFlagSet(command, flag.ExitOnError)
	configPath := flags.String("config", "config/config.toml", "Path to codex-bridge config")
	codexHome := flags.String("codex-home", "", "Path to Codex home, defaults to CODEX_HOME or ~/.codex")
	providerName := flags.String("provider-name", "codex_bridge", "Codex model provider name to write")
	providerDisplayName := flags.String("provider-display-name", "Codex Bridge", "Codex model provider display name")
	baseURL := flags.String("base-url", "", "Bridge base URL to write into Codex config, defaults to server.listen + /v1")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if command == "catalog generate" {
		if err := cfg.WriteCatalog(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("generated %d models at %s\n", len(cfg.Models), cfg.Codex.ModelCatalogPath)
		return
	}
	if command == "config check" {
		fmt.Printf("config ok: %s\n", *configPath)
		return
	}
	if command == "codex configure" {
		configBaseURL := *baseURL
		if configBaseURL == "" {
			configBaseURL = cfg.BridgeBaseURL()
		}
		result, err := codexconfig.Configure(codexconfig.Settings{
			CodexHome:           *codexHome,
			ProviderName:        *providerName,
			ProviderDisplayName: *providerDisplayName,
			BaseURL:             configBaseURL,
			ModelCatalogPath:    cfg.Codex.ModelCatalogPath,
			DefaultModel:        cfg.Codex.DefaultModel,
			BearerToken:         cfg.Codex.LocalToken,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("configured Codex at %s\n", result.ConfigPath)
		if result.BackupPath != "" {
			fmt.Printf("backup written at %s\n", result.BackupPath)
		}
		return
	}
	if command != "serve" {
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", command)
		os.Exit(1)
	}

	logger := logging.New(os.Stdout)
	if err := cfg.WriteCatalog(); err != nil {
		logger.Error("catalog_write_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("catalog_written", slog.String("path", cfg.Codex.ModelCatalogPath))

	providerClients := map[string]providers.ChatProvider{}
	for name, providerCfg := range cfg.Providers {
		providerClients[name] = providers.NewOpenAIChatClient(providerCfg.BaseURL, providerCfg.APIKey)
	}

	handler := server.New(cfg, providerClients, logger)
	httpServer := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      handler,
		ReadTimeout:  2 * time.Minute,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server_started", slog.String("listen", cfg.Server.Listen))
		errCh <- httpServer.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		logger.Info("server_stopping", slog.String("signal", sig.String()))
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("server_failed", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("server_shutdown_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
