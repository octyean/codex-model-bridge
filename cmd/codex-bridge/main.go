package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"codex-bridge/internal/codexconfig"
	"codex-bridge/internal/config"
	"codex-bridge/internal/logging"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/server"
	bridgesetup "codex-bridge/internal/setup"
	"codex-bridge/internal/toollog"
	"codex-bridge/internal/upstreamprobe"
)

func main() {
	command := "serve"
	args := os.Args[1:]
	defaultConfigPath := "config/config.toml"
	autoConfigure := false
	defaultConfigCreated := false
	if runtime.GOOS == "windows" && len(args) == 0 {
		defaultConfigPath = windowsDefaultConfigPath()
		autoConfigure = true
	}
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
	if command == "auth" && len(args) > 0 && args[0] == "token" {
		args = args[1:]
		command = "auth token"
	}

	flags := flag.NewFlagSet(command, flag.ExitOnError)
	configPath := flags.String("config", defaultConfigPath, "Path to codex-bridge config")
	codexHome := flags.String("codex-home", "", "Path to Codex home, defaults to CODEX_HOME or ~/.codex")
	providerName := flags.String("provider-name", "", "Codex model provider name to write")
	providerDisplayName := flags.String("provider-display-name", "Codex Bridge", "Codex model provider display name")
	baseURL := flags.String("base-url", "", "Bridge base URL to write into Codex config, defaults to server.listen + /v1")
	upstreamBaseURL := flags.String("upstream-base-url", "", "Upstream OpenAI-compatible base URL")
	upstreamAPIKey := flags.String("upstream-api-key", "", "Upstream API key")
	probeModel := flags.String("model", "", "Model to use for upstream probing")
	replaceUpstream := flags.Bool("replace-upstream", false, "Replace existing upstream config")
	yes := flags.Bool("yes", false, "Run setup without prompts")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}
	if command == "probe" {
		result := upstreamprobe.Run(context.Background(), upstreamprobe.Options{
			BaseURL: *upstreamBaseURL,
			APIKey:  *upstreamAPIKey,
			Model:   *probeModel,
		})
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		if !result.ResponsesStreamOK && !result.ChatStreamOK {
			os.Exit(1)
		}
		return
	}
	if command == "setup" {
		result, err := runSetup(*configPath, *codexHome, *upstreamBaseURL, *upstreamAPIKey, *probeModel, *replaceUpstream, *yes)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("config: %s\n", result.ConfigPath)
		if result.ExistingPreserved {
			fmt.Println("existing config preserved")
			return
		}
		fmt.Printf("protocol: %s\n", result.Protocol)
		fmt.Printf("default_model: %s\n", result.DefaultModel)
		fmt.Printf("responses_stream: %t\n", result.ResponsesStream)
		fmt.Printf("chat_stream: %t\n", result.ChatStream)
		return
	}
	if autoConfigure {
		created, err := ensureDefaultConfig(*configPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defaultConfigCreated = created
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if command == "catalog generate" {
		providerClients := buildProviderClients(cfg)
		discoverModels(context.Background(), cfg, providerClients, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	if command == "auth token" {
		fmt.Println(cfg.Codex.LocalToken)
		return
	}
	if command == "codex configure" {
		configBaseURL := *baseURL
		if configBaseURL == "" {
			configBaseURL = cfg.BridgeBaseURL()
		}
		providerClients := buildProviderClients(cfg)
		discoverModels(context.Background(), cfg, providerClients, slog.New(slog.NewTextHandler(os.Stderr, nil)))
		result, err := configureCodex(cfg, *codexHome, *providerName, *providerDisplayName, configBaseURL)
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
	if autoConfigure && defaultConfigCreated {
		result, err := configureCodex(cfg, *codexHome, *providerName, *providerDisplayName, cfg.BridgeBaseURL())
		if err != nil {
			logger.Warn("codex_configure_failed", slog.String("error", err.Error()))
		} else {
			logger.Info("codex_configured", slog.String("path", result.ConfigPath))
		}
	}
	providerClients := buildProviderClients(cfg)
	discoverModels(context.Background(), cfg, providerClients, logger)
	if err := cfg.WriteCatalog(); err != nil {
		logger.Error("catalog_write_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("catalog_written", slog.String("path", cfg.Codex.ModelCatalogPath), slog.Int("models", len(cfg.Models)))
	logToolLogStatus(logger)

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

func logToolLogStatus(logger *slog.Logger) {
	path, err := toollog.CheckConfiguredPath()
	if path == "" {
		logger.Info("tool_log_configured", slog.Bool("enabled", false), slog.String("env", toollog.EnvToolLogPath))
		return
	}
	if err != nil {
		logger.Warn("tool_log_unavailable", slog.String("path", path), slog.String("error", err.Error()), slog.String("env", toollog.EnvToolLogPath))
		return
	}
	logger.Info("tool_log_configured", slog.Bool("enabled", true), slog.String("path", path), slog.String("env", toollog.EnvToolLogPath))
}

func windowsDefaultConfigPath() string {
	executable, err := os.Executable()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(filepath.Dir(executable), "config.toml")
}

func ensureDefaultConfig(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("check config: %w", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false, fmt.Errorf("resolve user home: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, fmt.Errorf("create config directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(config.DefaultConfigText(homeDir)), 0o600); err != nil {
		return false, fmt.Errorf("write default config: %w", err)
	}
	fmt.Printf("created config at %s\n", path)
	return true, nil
}

func runSetup(configPath string, codexHome string, baseURL string, apiKey string, model string, replaceUpstream bool, yes bool) (bridgesetup.Result, error) {
	if !replaceUpstream && configExists(configPath) {
		return bridgesetup.Run(bridgesetup.Options{
			ConfigPath: configPath,
			CodexHome:  codexHome,
		}, upstreamprobe.Result{})
	}
	if strings.TrimSpace(baseURL) == "" && !yes {
		fmt.Print("Upstream base URL: ")
		if _, err := fmt.Scanln(&baseURL); err != nil {
			return bridgesetup.Result{}, fmt.Errorf("read upstream base URL: %w", err)
		}
	}
	if strings.TrimSpace(apiKey) == "" && !yes {
		fmt.Print("Upstream API key: ")
		if _, err := fmt.Scanln(&apiKey); err != nil {
			return bridgesetup.Result{}, fmt.Errorf("read upstream API key: %w", err)
		}
	}
	if strings.TrimSpace(baseURL) == "" {
		return bridgesetup.Result{}, fmt.Errorf("upstream base URL is required")
	}
	if strings.TrimSpace(apiKey) == "" {
		return bridgesetup.Result{}, fmt.Errorf("upstream API key is required")
	}
	probe := upstreamprobe.Run(context.Background(), upstreamprobe.Options{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
	})
	if !probe.ResponsesStreamOK && !probe.ChatStreamOK {
		return bridgesetup.Result{}, fmt.Errorf("upstream stream probe failed: %s", probe.Error)
	}
	return bridgesetup.Run(bridgesetup.Options{
		ConfigPath:      configPath,
		CodexHome:       codexHome,
		BaseURL:         baseURL,
		APIKey:          apiKey,
		DefaultModel:    model,
		ReplaceUpstream: replaceUpstream,
	}, probe)
}

func configExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func configureCodex(cfg *config.Config, codexHome string, providerName string, providerDisplayName string, baseURL string) (codexconfig.Result, error) {
	if strings.TrimSpace(cfg.Codex.DefaultModel) == "" {
		return codexconfig.Result{}, fmt.Errorf("no default model is available; configure a [models.*] entry or make upstream /models discovery succeed")
	}
	configPath := cfg.Path
	if abs, err := filepath.Abs(configPath); err == nil {
		configPath = abs
	}
	command, args, timeout := authHelper(configPath)
	return codexconfig.Configure(codexconfig.Settings{
		CodexHome:           codexHome,
		ProviderName:        providerName,
		ProviderDisplayName: providerDisplayName,
		BaseURL:             baseURL,
		ModelCatalogPath:    cfg.Codex.ModelCatalogPath,
		DefaultModel:        cfg.Codex.DefaultModel,
		AuthCommand:         command,
		AuthArgs:            args,
		AuthConfigPath:      configPath,
		AuthTimeoutMS:       timeout,
	})
}

func buildProviderClients(cfg *config.Config) map[string]providers.ChatProvider {
	providerClients := map[string]providers.ChatProvider{}
	for name, providerCfg := range cfg.Providers {
		providerClients[name] = providers.NewOpenAIChatClient(providerCfg.BaseURL, providerCfg.APIKey)
	}
	return providerClients
}

func discoverModels(ctx context.Context, cfg *config.Config, providerClients map[string]providers.ChatProvider, logger *slog.Logger) {
	if !cfg.ModelDiscovery.Enabled || cfg.ModelDiscoveryMode() == "config" {
		cfg.AddDiscoveredModels("", nil)
		return
	}
	for name, provider := range providerClients {
		resp, err := provider.ListModels(ctx)
		if err != nil {
			logger.Warn("model_discovery_failed", slog.String("provider", name), slog.String("error", err.Error()))
			continue
		}
		ids := make([]string, 0, len(resp.Data))
		for _, item := range resp.Data {
			ids = append(ids, item.ID)
		}
		added := cfg.AddDiscoveredModels(name, ids)
		logger.Info("model_discovery_completed", slog.String("provider", name), slog.Int("upstream_models", len(ids)), slog.Int("added", added))
	}
}

func authHelper(configPath string) (string, []string, int) {
	path, err := os.Executable()
	if err != nil {
		return "codex-bridge", []string{"auth", "token", "--config", configPath}, 5000
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	return authHelperFromPath(path, cwd, configPath)
}

func authHelperFromPath(path string, cwd string, configPath string) (string, []string, int) {
	if isGoRunExecutable(path) && cwd != "" {
		return "go", []string{"run", filepath.Join(cwd, "cmd", "codex-bridge"), "auth", "token", "--config", configPath}, 30000
	}
	return path, []string{"auth", "token", "--config", configPath}, 5000
}

func isGoRunExecutable(path string) bool {
	sep := string(filepath.Separator)
	return strings.Contains(path, sep+".cache"+sep+"go-build"+sep) || strings.Contains(path, sep+"go-build")
}
