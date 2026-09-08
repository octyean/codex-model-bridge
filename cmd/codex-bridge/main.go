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
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"codex-bridge/internal/codexconfig"
	"codex-bridge/internal/config"
	"codex-bridge/internal/diagnostics"
	"codex-bridge/internal/incidentlog"
	"codex-bridge/internal/logging"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/requestdump"
	"codex-bridge/internal/server"
	bridgesetup "codex-bridge/internal/setup"
	"codex-bridge/internal/toollog"
	"codex-bridge/internal/upstreamprobe"

	"golang.org/x/term"
)

const (
	modelDiscoveryProviderTimeout = 10 * time.Second
	sessionLogPruneInterval       = 6 * time.Hour
	upstreamAPIKeyEnv             = "CODEX_BRIDGE_API_KEY"
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
	upstreamAPIKey := flags.String("upstream-api-key", "", "Upstream API key; prefer CODEX_BRIDGE_API_KEY to avoid command history")
	probeModel := flags.String("model", "", "Model to use for upstream probing")
	setupProfile := flags.String("profile", "", "Adapter profile to use during setup")
	verifyModels := flags.String("models", "", "Comma-separated model slugs or upstream model IDs to verify")
	verifyAll := flags.Bool("all", false, "Verify all configured models, optionally limited by --provider-name")
	replaceUpstream := flags.Bool("replace-upstream", false, "Replace existing upstream config")
	yes := flags.Bool("yes", false, "Run setup without prompts")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}
	resolvedUpstreamAPIKey := resolveUpstreamAPIKey(*upstreamAPIKey)
	if command == "probe" {
		result := upstreamprobe.Run(context.Background(), upstreamprobe.Options{
			BaseURL: *upstreamBaseURL,
			APIKey:  resolvedUpstreamAPIKey,
			Model:   *probeModel,
		})
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		if !result.ResponsesReady() && !result.ChatReady() {
			os.Exit(1)
		}
		return
	}
	if command == "setup" {
		result, err := runSetup(*configPath, *codexHome, *upstreamBaseURL, resolvedUpstreamAPIKey, *probeModel, *setupProfile, *replaceUpstream, *yes)
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
		fmt.Printf("profile: %s\n", result.Profile)
		fmt.Printf("default_model: %s\n", result.DefaultModel)
		fmt.Printf("responses_stream: %t\n", result.ResponsesStream)
		fmt.Printf("responses_tools: %t\n", result.ResponsesTools)
		fmt.Printf("responses_tool_stream: %t\n", result.ResponsesToolStream)
		fmt.Printf("responses_tool_continuation: %t\n", result.ResponsesToolContinuation)
		fmt.Printf("responses_options: %t\n", result.ResponsesOptions)
		fmt.Printf("responses_structured_output: %t\n", result.ResponsesStructuredOutput)
		fmt.Printf("chat_stream: %t\n", result.ChatStream)
		fmt.Printf("chat_tools: %t\n", result.ChatTools)
		fmt.Printf("chat_tool_stream: %t\n", result.ChatToolStream)
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
	if command == "verify" {
		failed, err := runVerify(context.Background(), cfg, *providerName, *verifyModels, *verifyAll)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if failed {
			os.Exit(1)
		}
		return
	}
	if command == "catalog generate" {
		providerClients := buildProviderClients(cfg, nil)
		discoverModels(context.Background(), cfg, providerClients, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err := cfg.WriteCatalog(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("generated %d models at %s\n", len(cfg.Models), cfg.Codex.ModelCatalogPath)
		return
	}
	if command == "config check" {
		if err := config.CheckUnknownFields(*configPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("config ok: %s\n", *configPath)
		if err := writeConfigSummary(os.Stdout, cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, warning := range cfg.CapabilityWarnings(time.Now()) {
			fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
		}
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
		providerClients := buildProviderClients(cfg, nil)
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
	if err := config.CheckUnknownFields(*configPath); err != nil {
		logger.Warn("config_unknown_fields", slog.String("error", err.Error()))
	}
	for _, warning := range cfg.CapabilityWarnings(time.Now()) {
		logger.Warn("model_capability_verification_stale", slog.String("detail", warning))
	}
	if autoConfigure && defaultConfigCreated {
		result, err := configureCodex(cfg, *codexHome, *providerName, *providerDisplayName, cfg.BridgeBaseURL())
		if err != nil {
			logger.Warn("codex_configure_failed", slog.String("error", err.Error()))
		} else {
			logger.Info("codex_configured", slog.String("path", result.ConfigPath))
		}
	}
	providerClients := buildProviderClients(cfg, logger)
	discoverModels(context.Background(), cfg, providerClients, logger)
	if err := cfg.WriteCatalog(); err != nil {
		logger.Error("catalog_write_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("catalog_written", slog.String("path", cfg.Codex.ModelCatalogPath), slog.Int("models", len(cfg.Models)))
	logToolLogStatus(logger)
	logRequestDumpStatus(logger)
	logIncidentLogStatus(logger)
	pruneSessionLogs(cfg, logger)
	pruneCtx, stopSessionLogPruner := context.WithCancel(context.Background())
	defer stopSessionLogPruner()
	go pruneSessionLogsPeriodically(pruneCtx, cfg, logger)

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
	stopSessionLogPruner()

	shutdownTimeout := cfg.ShutdownTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Warn("server_graceful_shutdown_timed_out", slog.Duration("timeout", shutdownTimeout), slog.String("error", err.Error()))
		if closeErr := httpServer.Close(); closeErr != nil {
			logger.Error("server_force_close_failed", slog.String("error", closeErr.Error()))
		}
	}
}

func logRequestDumpStatus(logger *slog.Logger) {
	path, err := requestdump.CheckConfiguredPath()
	if path == "" {
		logger.Info("upstream_request_dump_configured", slog.Bool("enabled", false), slog.String("env", requestdump.EnvPath))
		return
	}
	if err != nil {
		logger.Warn("upstream_request_dump_unavailable", slog.String("path", path), slog.String("error", err.Error()), slog.String("env", requestdump.EnvPath))
		return
	}
	logger.Info("upstream_request_dump_configured", slog.Bool("enabled", true), slog.String("path", path), slog.String("env", requestdump.EnvPath))
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

func logIncidentLogStatus(logger *slog.Logger) {
	path, err := incidentlog.CheckConfiguredPath()
	if path == "" {
		logger.Info("incident_log_configured", slog.Bool("enabled", false), slog.String("env", incidentlog.EnvPath))
		return
	}
	if err != nil {
		logger.Warn("incident_log_unavailable", slog.String("path", path), slog.String("error", err.Error()), slog.String("env", incidentlog.EnvPath))
		return
	}
	logger.Info("incident_log_configured", slog.Bool("enabled", true), slog.String("path", path), slog.String("env", incidentlog.EnvPath))
}

func pruneSessionLogs(cfg *config.Config, logger *slog.Logger) {
	path := toollog.ConfiguredPath()
	if path == "" {
		return
	}
	result, err := diagnostics.PruneSessions(path, cfg.DiagnosticsRetentionDays(), cfg.DiagnosticsMaxTotalBytes())
	if err != nil {
		logger.Warn("session_log_prune_failed", slog.String("path", result.SessionsPath), slog.String("error", err.Error()))
		return
	}
	logger.Info("session_logs_pruned",
		slog.String("path", result.SessionsPath),
		slog.Int("deleted_sessions", result.Deleted),
		slog.Int64("released_bytes", result.ReleasedBytes),
		slog.Int("remaining_sessions", result.Remaining),
		slog.Int64("remaining_bytes", result.RemainingSize),
	)
}

func pruneSessionLogsPeriodically(ctx context.Context, cfg *config.Config, logger *slog.Logger) {
	ticker := time.NewTicker(sessionLogPruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pruneSessionLogs(cfg, logger)
		}
	}
}

func writeConfigSummary(writer io.Writer, cfg *config.Config) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "SLUG\tDISPLAY NAME\tPROVIDER\tUPSTREAM MODEL\tPROFILE\tEXECUTION MODE\tVERIFICATION")
	slugs := make([]string, 0, len(cfg.Models))
	for slug := range cfg.Models {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		model := cfg.Models[slug]
		provider := cfg.Providers[model.Provider]
		displayName := strings.TrimSpace(model.DisplayName)
		if displayName == "" {
			displayName = slug
		}
		verification := "unverified"
		if !cfg.RequiresCapabilityVerification(model, provider) {
			verification = "not_required"
		} else if _, ok := cfg.VerifiedCapability(model, provider); ok {
			verification = "verified"
		} else {
			verification = "verification_required"
		}
		plan := cfg.ExecutionPlan(model, provider)
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			slug,
			displayName,
			model.Provider,
			model.UpstreamModel,
			plan.Profile,
			plan.Mode,
			verification,
		)
	}
	return table.Flush()
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
	defaultText, err := config.DefaultConfigText(homeDir)
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(defaultText), 0o600); err != nil {
		return false, fmt.Errorf("write default config: %w", err)
	}
	fmt.Printf("created config at %s\n", path)
	return true, nil
}

func runSetup(configPath string, codexHome string, baseURL string, apiKey string, model string, profile string, replaceUpstream bool, yes bool) (bridgesetup.Result, error) {
	if !replaceUpstream && configExists(configPath) {
		return bridgesetup.Run(bridgesetup.Options{
			ConfigPath: configPath,
			CodexHome:  codexHome,
			Profile:    profile,
		}, upstreamprobe.Result{})
	}
	if strings.TrimSpace(baseURL) == "" && !yes {
		fmt.Print("Upstream base URL: ")
		if _, err := fmt.Scanln(&baseURL); err != nil {
			return bridgesetup.Result{}, fmt.Errorf("read upstream base URL: %w", err)
		}
	}
	if strings.TrimSpace(apiKey) == "" && !yes {
		var err error
		apiKey, err = readUpstreamAPIKey()
		if err != nil {
			return bridgesetup.Result{}, err
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
	if !probe.ResponsesReady() && !probe.ChatReady() {
		return bridgesetup.Result{}, fmt.Errorf("upstream protocol probe failed: %s", probe.Error)
	}
	return bridgesetup.Run(bridgesetup.Options{
		ConfigPath:      configPath,
		CodexHome:       codexHome,
		BaseURL:         baseURL,
		APIKey:          apiKey,
		DefaultModel:    model,
		Profile:         profile,
		ReplaceUpstream: replaceUpstream,
	}, probe)
}

func resolveUpstreamAPIKey(flagValue string) string {
	if value := strings.TrimSpace(flagValue); value != "" {
		return value
	}
	return strings.TrimSpace(os.Getenv(upstreamAPIKeyEnv))
}

func readUpstreamAPIKey() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("upstream API key is required; set %s or pass --upstream-api-key", upstreamAPIKeyEnv)
	}
	fmt.Fprint(os.Stderr, "Upstream API key: ")
	value, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read upstream API key: %w", err)
	}
	return strings.TrimSpace(string(value)), nil
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

func buildProviderClients(cfg *config.Config, logger *slog.Logger) map[string]providers.ChatProvider {
	providerClients := map[string]providers.ChatProvider{}
	for name, providerCfg := range cfg.Providers {
		client := providers.NewOpenAIChatClient(providerCfg.BaseURL, providerCfg.APIKey)
		if logger != nil {
			providerName := name
			client.SetRetryObserver(func(event providers.RetryEvent) {
				attrs := []any{
					slog.String("provider", providerName),
					slog.String("action", event.Action),
					slog.String("method", event.Method),
					slog.String("url", event.URL),
					slog.Int("retry_count", event.RetryCount),
					slog.Int64("wait_ms", event.Wait.Milliseconds()),
					slog.Int64("total_wait_ms", event.TotalWait.Milliseconds()),
					slog.Int("status_code", event.StatusCode),
					slog.Int64("total_requests", event.TotalRequests),
					slog.Int64("retried_requests", event.RetriedRequests),
					slog.Int64("failed_requests", event.FailedRequests),
					slog.Int64("error_rate_permille", event.ErrorRatePermille),
				}
				if event.Error != "" {
					attrs = append(attrs, slog.String("error", event.Error))
				}
				if event.Action == "failed" {
					logger.Warn("upstream_retry_status", attrs...)
					return
				}
				logger.Info("upstream_retry_status", attrs...)
			})
		}
		providerClients[name] = client
	}
	return providerClients
}

func discoverModels(ctx context.Context, cfg *config.Config, providerClients map[string]providers.ChatProvider, logger *slog.Logger) {
	if !cfg.ModelDiscovery.Enabled || cfg.ModelDiscoveryMode() == "config" {
		return
	}

	names := make([]string, 0, len(providerClients))
	for name := range providerClients {
		names = append(names, name)
	}
	sort.Strings(names)

	discovered := make(map[string][]string, len(names))
	for _, name := range names {
		provider := providerClients[name]
		providerCtx, cancel := context.WithTimeout(ctx, modelDiscoveryProviderTimeout)
		resp, err := provider.ListModels(providerCtx)
		cancel()
		if err != nil {
			logger.Warn("model_discovery_failed", slog.String("provider", name), slog.String("error", err.Error()))
			continue
		}
		ids := make([]string, 0, len(resp.Data))
		for _, item := range resp.Data {
			ids = append(ids, item.ID)
		}
		discovered[name] = ids
	}
	if len(discovered) == 0 {
		return
	}

	cfg.PrepareModelDiscovery()
	for _, name := range names {
		ids, ok := discovered[name]
		if !ok {
			continue
		}
		report := cfg.AddDiscoveredModelsReport(name, ids)
		assignments, _ := json.Marshal(report.Assignments)
		skippedModels, skippedOmitted := summarizeSkippedModels(report.Skipped, 12)
		logger.Info("model_discovery_completed",
			slog.String("provider", name),
			slog.Int("upstream_models", report.Discovered),
			slog.Int("added", report.Added),
			slog.Int("skipped", len(report.Skipped)),
			slog.Any("skipped_models", skippedModels),
			slog.Int("skipped_omitted", skippedOmitted),
			slog.String("assignments", string(assignments)),
		)
	}
	if err := cfg.WriteModelDiscoveryState(); err != nil {
		logger.Warn("model_discovery_state_write_failed",
			slog.String("path", cfg.ModelDiscoveryStatePath()),
			slog.String("error", err.Error()),
		)
	}
}

func summarizeSkippedModels(models []string, limit int) ([]string, int) {
	if len(models) <= limit {
		return models, 0
	}
	return models[:limit], len(models) - limit
}

type verifyProviderOutput struct {
	Provider string                 `json:"provider"`
	Results  []upstreamprobe.Result `json:"results"`
	Cache    map[string]string      `json:"cache"`
}

type verifyOutput struct {
	CachePath string                 `json:"cache_path"`
	Providers []verifyProviderOutput `json:"providers"`
}

func runVerify(ctx context.Context, cfg *config.Config, providerName string, requestedModels string, verifyAll bool) (bool, error) {
	providerName = strings.TrimSpace(providerName)
	requestedModels = strings.TrimSpace(requestedModels)
	if verifyAll && requestedModels != "" {
		return false, fmt.Errorf("--all and --models cannot be used together")
	}
	if !verifyAll && requestedModels == "" {
		return false, fmt.Errorf("pass --models with the third-party models to verify, or use --all explicitly")
	}

	providerNames := make([]string, 0, len(cfg.Providers))
	if providerName != "" {
		if _, ok := cfg.Providers[providerName]; !ok {
			return false, fmt.Errorf("provider %q is not configured", providerName)
		}
		providerNames = append(providerNames, providerName)
	} else if verifyAll {
		for name := range cfg.Providers {
			providerNames = append(providerNames, name)
		}
		sort.Strings(providerNames)
	} else {
		if len(cfg.Providers) != 1 {
			return false, fmt.Errorf("--provider-name is required when multiple providers are configured")
		}
		for name := range cfg.Providers {
			providerNames = append(providerNames, name)
		}
	}

	output := verifyOutput{
		CachePath: cfg.CapabilityCachePath(),
		Providers: make([]verifyProviderOutput, 0, len(providerNames)),
	}
	failed := false
	cacheUpdated := false
	for _, name := range providerNames {
		provider := cfg.Providers[name]
		models, err := verifyModelIDs(cfg, name, requestedModels, verifyAll)
		if err != nil {
			return false, err
		}
		providerOutput := verifyProviderOutput{
			Provider: name,
			Results:  make([]upstreamprobe.Result, 0, len(models)),
			Cache:    make(map[string]string, len(models)),
		}
		for _, model := range models {
			result := upstreamprobe.Run(ctx, upstreamprobe.Options{
				BaseURL: provider.BaseURL,
				APIKey:  provider.APIKey,
				Model:   model,
			})
			if cfg.UpdateVerifiedCapability(name, provider, result) {
				providerOutput.Cache[model] = "updated"
				cacheUpdated = true
			} else {
				providerOutput.Cache[model] = "preserved"
			}
			providerOutput.Results = append(providerOutput.Results, result)
			if result.Outcome != upstreamprobe.ProbeOutcomeSupported {
				failed = true
			}
		}
		output.Providers = append(output.Providers, providerOutput)
	}
	if cacheUpdated {
		if err := cfg.WriteCapabilityCache(); err != nil {
			return false, fmt.Errorf("write capability cache: %w", err)
		}
	}
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return false, err
	}
	fmt.Println(string(data))
	return failed, nil
}

func verifyModelIDs(cfg *config.Config, providerName string, requestedModels string, verifyAll bool) ([]string, error) {
	requested := strings.Split(requestedModels, ",")
	if verifyAll {
		requested = requested[:0]
		slugs := make([]string, 0, len(cfg.Models))
		for slug, model := range cfg.Models {
			if model.Provider == providerName {
				slugs = append(slugs, slug)
			}
		}
		sort.Strings(slugs)
		for _, slug := range slugs {
			requested = append(requested, cfg.Models[slug].UpstreamModel)
		}
	}

	seen := map[string]bool{}
	models := make([]string, 0, len(requested))
	for _, value := range requested {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if configured, ok := cfg.Models[value]; ok {
			if configured.Provider != providerName {
				return nil, fmt.Errorf("model slug %q belongs to provider %q, not %q", value, configured.Provider, providerName)
			}
			value = configured.UpstreamModel
		}
		if !seen[value] {
			seen[value] = true
			models = append(models, value)
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("provider %q has no configured models to verify; pass --models with upstream model IDs", providerName)
	}
	return models, nil
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
