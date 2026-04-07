package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Duops/SherlockOps/internal/analyzer"
	"github.com/Duops/SherlockOps/internal/analyzer/llm"
	"github.com/Duops/SherlockOps/internal/tooling"
	"github.com/Duops/SherlockOps/internal/cache"
	"github.com/Duops/SherlockOps/internal/config"
	"github.com/Duops/SherlockOps/internal/domain"
	"github.com/Duops/SherlockOps/internal/health"
	"github.com/Duops/SherlockOps/internal/messenger"
	"github.com/Duops/SherlockOps/internal/metrics"
	"github.com/Duops/SherlockOps/internal/middleware"
	"github.com/Duops/SherlockOps/internal/pipeline"
	"github.com/Duops/SherlockOps/internal/receiver"
	"github.com/Duops/SherlockOps/internal/runbook"
	"github.com/Duops/SherlockOps/internal/version"
	"github.com/Duops/SherlockOps/internal/webui"
)

func main() {
	configPath := flag.String("config", "/etc/sherlockops/config.yaml", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLogLevel(os.Getenv("LOG_LEVEL"))}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Context with cancellation for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Cache.
	sqliteCache, err := cache.New(cfg.Cache.Path, cfg.Cache.TTLDuration(), cfg.Cache.MinLength)
	if err != nil {
		logger.Error("failed to create cache", "error", err)
		os.Exit(1)
	}
	// 2. Environment-aware tool registry.
	envRegistry := tooling.NewEnvRegistry(logger)

	// Default registry (from top-level tools + mcp).
	defaultRegistry := registerTools(ctx, cfg.Tools, cfg.MCP, logger)
	envRegistry.SetRegistry("default", defaultRegistry)
	logger.Info("registered default tool registry")

	// Health check default tools.
	logger.Info("running tool health checks...")
	tooling.CheckHealth(ctx, defaultRegistry, logger)

	// Per-environment registries.
	for envName, envCfg := range cfg.Environments {
		reg := registerTools(ctx, envCfg.Tools, envCfg.MCP, logger)
		envRegistry.SetRegistry(envName, reg)
		logger.Info("registered environment", "name", envName)
		tooling.CheckHealth(ctx, reg, logger)
	}

	// 3. LLM provider.
	llmProvider, err := llm.NewProvider(
		cfg.LLM.Provider,
		cfg.LLM.APIKey,
		cfg.LLM.BaseURL,
		cfg.LLM.Model,
		cfg.LLM.MaxTokens,
	)
	if err != nil {
		logger.Error("failed to create LLM provider", "error", err)
		os.Exit(1)
	}

	// 4. Environment-aware analyzer with rate limiting.
	envAnalyzer := analyzer.NewEnvAnalyzer(
		llmProvider,
		envRegistry,
		cfg.LLM.SystemPrompt,
		cfg.LLM.Language,
		cfg.LLM.MaxIterations,
		logger,
	)

	// Set token pricing from config.
	if cfg.LLM.InputTokenCost > 0 || cfg.LLM.OutputTokenCost > 0 {
		envAnalyzer.SetTokenCost(cfg.LLM.InputTokenCost, cfg.LLM.OutputTokenCost)
		logger.Info("custom token pricing set", "input_per_m", cfg.LLM.InputTokenCost, "output_per_m", cfg.LLM.OutputTokenCost)
	}

	// Set per-environment system prompts.
	for envName, envCfg := range cfg.Environments {
		if envCfg.LLM != nil && envCfg.LLM.SystemPrompt != "" {
			envAnalyzer.SetSystemPrompt(envName, envCfg.LLM.SystemPrompt)
		}
	}

	// 4a. Runbook store (optional).
	if cfg.Runbooks.Enabled {
		rbStore, err := runbook.NewStore(cfg.Runbooks.Dir, logger)
		if err != nil {
			logger.Error("failed to create runbook store", "error", err)
			os.Exit(1)
		}
		if err := rbStore.Load(); err != nil {
			logger.Error("failed to load runbooks", "error", err)
			os.Exit(1)
		}
		envAnalyzer.SetRunbookStore(rbStore)
		logger.Info("runbook store loaded", "dir", cfg.Runbooks.Dir)
	}

	rateLimitedAnalyzer := analyzer.NewRateLimitedAnalyzer(
		envAnalyzer,
		cfg.Pipeline.MaxConcurrentLLM,
		logger,
	)

	// 5. Messengers.
	var messengers []domain.Messenger

	if cfg.Messengers.Slack.Enabled {
		slackMsg := messenger.NewSlack(
			cfg.Messengers.Slack.BotToken,
			cfg.Messengers.Slack.AppToken,
			cfg.Messengers.Slack.SigningSecret,
			cfg.Messengers.Slack.DefaultChannel,
			cfg.Messengers.Slack.ListenChannels,
			logger,
		)
		messengers = append(messengers, slackMsg)
		logger.Info("messenger enabled", "name", "slack")
	}

	if cfg.Messengers.Telegram.Enabled {
		tgMsg := messenger.NewTelegram(
			cfg.Messengers.Telegram.BotToken,
			cfg.Messengers.Telegram.DefaultChat,
			cfg.Messengers.Telegram.ListenChats,
			cfg.Messengers.Telegram.ParseMode,
			logger,
		)
		messengers = append(messengers, tgMsg)
		logger.Info("messenger enabled", "name", "telegram")
	}

	if cfg.Messengers.Teams.Enabled {
		teamsMsg := messenger.NewTeams(
			cfg.Messengers.Teams.TenantID,
			cfg.Messengers.Teams.ClientID,
			cfg.Messengers.Teams.ClientSecret,
			cfg.Messengers.Teams.WebhookURL,
			cfg.Messengers.Teams.DefaultTeam,
			cfg.Messengers.Teams.DefaultChannel,
			cfg.Messengers.Teams.ListenPort,
			logger,
		)
		messengers = append(messengers, teamsMsg)
		logger.Info("messenger enabled", "name", "teams")
	}

	// 6. Pipeline and worker pool.
	pipe := pipeline.New(sqliteCache, rateLimitedAnalyzer, messengers, logger)
	pipe.SetMode(cfg.Pipeline.Mode)
	pipe.SetPendingStore(sqliteCache)
	workerPool := pipeline.NewWorkerPool(pipe, cfg.Pipeline.Workers, cfg.Pipeline.QueueSize, logger)
	workerPool.Start(ctx)
	logger.Info("pipeline configured", "mode", cfg.Pipeline.Mode)
	if cfg.Pipeline.Mode == config.PipelineModeManual {
		startPendingJanitor(ctx, sqliteCache, 30*24*time.Hour, logger)
	}

	// 7. Receivers.
	receivers := []domain.Receiver{
		receiver.NewAlertmanagerReceiver(),
		receiver.NewGrafanaReceiver(),
		receiver.NewZabbixReceiver(),
		receiver.NewDatadogReceiver(),
		receiver.NewELKReceiver(),
		receiver.NewLokiReceiver(),
		receiver.NewGenericReceiver(),
	}

	webhookHandler := func(alerts []domain.Alert) {
		for i := range alerts {
			metrics.AlertsReceived.WithLabelValues(alerts[i].Source).Inc()
			if err := workerPool.Submit(&alerts[i]); err != nil {
				logger.Error("worker queue full, dropping alert",
					"alert", alerts[i].Name,
					"error", err,
				)
			}
		}
	}

	receiverRouter := receiver.NewRouter(cfg.Webhooks.PathPrefix, receivers, webhookHandler)

	// 8. Health checks.
	healthChecker := health.NewChecker(sqliteCache, messengers, logger)

	// 9. HTTP mux.
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","version":"%s"}`, version.Version)
	})
	mux.HandleFunc("/health/live", healthChecker.Liveness)
	mux.HandleFunc("/health/ready", healthChecker.Readiness)
	mux.Handle("/metrics", metrics.Handler())
	mux.Handle(cfg.Webhooks.PathPrefix+"/", receiverRouter)

	// 9a. Web UI dashboard.
	dashboard := webui.New(sqliteCache, logger)
	dashboard.SetPendingLister(pendingListerAdapter{c: sqliteCache})
	dashboard.RegisterRoutes(mux)

	// 10. Middleware chain: Recovery -> RequestID -> router.
	var handler http.Handler = mux
	handler = middleware.RequestID()(handler)
	handler = middleware.Recovery(logger)(handler)

	server := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// 11. Start messengers in listener mode with panic recovery.
	messengerHandler := func(alert *domain.Alert) {
		defer func() {
			if rv := recover(); rv != nil {
				logger.Error("panic in messenger handler",
					"panic", rv,
					"alert", alert.Name,
				)
			}
		}()

		// Manual-mode @bot mention: if the user replies to an alert message
		// with an analyze command, recover the original alert from the pending
		// store and run normal analysis on it. Falls through unchanged if the
		// alert is not a mention or no pending entry matches.
		lookupCtx, cancelLookup := context.WithTimeout(ctx, 3*time.Second)
		alert = pipeline.ResolvePendingMention(lookupCtx, sqliteCache, alert)
		cancelLookup()

		metrics.AlertsReceived.WithLabelValues(alert.Source).Inc()
		if err := workerPool.Submit(alert); err != nil {
			logger.Error("worker queue full, dropping alert",
				"alert", alert.Name,
				"error", err,
			)
		}
	}
	for _, m := range messengers {
		if err := m.Start(ctx, messengerHandler); err != nil {
			logger.Error("failed to start messenger", "name", m.Name(), "error", err)
			os.Exit(1)
		}
		logger.Info("messenger started", "name", m.Name())
	}

	// 12. Start HTTP server.
	go func() {
		logger.Info("starting sherlockops", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			cancel()
		}
	}()

	// Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		logger.Info("received signal, shutting down", "signal", sig)
	case <-ctx.Done():
	}

	// 13. Graceful shutdown.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown error", "error", err)
	}

	// Stop worker pool (drains remaining queue items).
	workerPool.Stop()

	for _, m := range messengers {
		if err := m.Stop(shutdownCtx); err != nil {
			logger.Error("messenger stop error", "name", m.Name(), "error", err)
		}
	}

	if err := sqliteCache.Close(); err != nil {
		logger.Error("cache close error", "error", err)
	}

	logger.Info("sherlockops stopped")
}

// registerTools creates a Registry and populates it from the given ToolsConfig and MCPConfig.
func registerTools(ctx context.Context, toolsCfg config.ToolsConfig, mcpCfg config.MCPConfig, logger *slog.Logger) *tooling.Registry {
	registry := tooling.NewRegistry(logger)

	if toolsCfg.Prometheus.Enabled {
		prom := tooling.NewPrometheusExecutor(
			toolsCfg.Prometheus.URL,
			toolsCfg.Prometheus.Username,
			toolsCfg.Prometheus.Password,
			logger,
		)
		registry.RegisterNamed(prom, "prometheus")
		logger.Info("registered tool executor", "name", "prometheus")
	}

	if toolsCfg.VictoriaMetrics.Enabled {
		url := toolsCfg.VictoriaMetrics.URL
		if toolsCfg.VictoriaMetrics.Tenant != "" {
			url = url + "/select/" + toolsCfg.VictoriaMetrics.Tenant + "/prometheus"
		}
		vm := tooling.NewPrometheusExecutor(url, toolsCfg.VictoriaMetrics.Username, toolsCfg.VictoriaMetrics.Password, logger)
		registry.RegisterNamed(vm, "victoriametrics")
		logger.Info("registered tool executor", "name", "victoriametrics")
	}

	if toolsCfg.Loki.Enabled {
		loki := tooling.NewLokiExecutor(
			toolsCfg.Loki.URL,
			toolsCfg.Loki.Username,
			toolsCfg.Loki.Password,
			logger,
		)
		registry.RegisterNamed(loki, "loki")
		logger.Info("registered tool executor", "name", "loki")
	}

	if toolsCfg.Kubernetes.Enabled {
		k8s, err := tooling.NewKubernetesExecutor(
			toolsCfg.Kubernetes.Kubeconfig,
			toolsCfg.Kubernetes.Context,
			logger,
		)
		if err != nil {
			logger.Error("failed to create kubernetes executor", "error", err)
			os.Exit(1)
		}
		registry.RegisterNamed(k8s, "kubernetes")
		logger.Info("registered tool executor", "name", "kubernetes")
	}

	if toolsCfg.VSphere.Enabled {
		vs := tooling.NewVSphereExecutor(toolsCfg.VSphere.URL, toolsCfg.VSphere.Username, toolsCfg.VSphere.Password, toolsCfg.VSphere.Insecure, logger)
		registry.RegisterNamed(vs, "vsphere")
		logger.Info("registered tool executor", "name", "vsphere")
	}

	if toolsCfg.AWS.Enabled {
		aws := tooling.NewAWSCloudWatchExecutor(toolsCfg.AWS.Region, toolsCfg.AWS.AccessKey, toolsCfg.AWS.SecretKey, logger)
		registry.RegisterNamed(aws, "aws")
		logger.Info("registered tool executor", "name", "aws")
	}

	if toolsCfg.GCP.Enabled {
		gcp := tooling.NewGCPMonitoringExecutor(toolsCfg.GCP.ProjectID, toolsCfg.GCP.CredentialsJSON, logger)
		registry.RegisterNamed(gcp, "gcp")
		logger.Info("registered tool executor", "name", "gcp")
	}

	if toolsCfg.Azure.Enabled {
		az := tooling.NewAzureMonitorExecutor(toolsCfg.Azure.TenantID, toolsCfg.Azure.ClientID, toolsCfg.Azure.ClientSecret, toolsCfg.Azure.SubscriptionID, logger)
		registry.RegisterNamed(az, "azure")
		logger.Info("registered tool executor", "name", "azure")
	}

	if toolsCfg.Postgres.Enabled {
		pg, err := tooling.NewPostgresExecutor(toolsCfg.Postgres.DSN, logger)
		if err != nil {
			logger.Error("failed to create postgres executor", "error", err)
			os.Exit(1)
		}
		registry.RegisterNamed(pg, "postgres")
		logger.Info("registered tool executor", "name", "postgres")
	}

	if toolsCfg.MongoDB.Enabled {
		mongo, err := tooling.NewMongoDBExecutor(toolsCfg.MongoDB.URI, logger)
		if err != nil {
			logger.Error("failed to create mongodb executor", "error", err)
			os.Exit(1)
		}
		registry.RegisterNamed(mongo, "mongodb")
		logger.Info("registered tool executor", "name", "mongodb")
	}

	if toolsCfg.YandexCloud.Enabled {
		yc := tooling.NewYandexCloudExecutor(toolsCfg.YandexCloud.CloudID, toolsCfg.YandexCloud.FolderID, toolsCfg.YandexCloud.Token, toolsCfg.YandexCloud.TokenType, logger)
		registry.RegisterNamed(yc, "yandex_cloud")
		logger.Info("registered tool executor", "name", "yandex_cloud")
	}

	if toolsCfg.DigitalOcean.Enabled {
		do := tooling.NewDigitalOceanExecutor(toolsCfg.DigitalOcean.Token, logger)
		registry.RegisterNamed(do, "digitalocean")
		logger.Info("registered tool executor", "name", "digitalocean")
	}

	for _, mcpClient := range mcpCfg.Clients {
		client := tooling.NewMCPClient(
			mcpClient.Name,
			mcpClient.URL,
			mcpClient.Auth,
			mcpClient.Token,
			mcpClient.Headers,
			logger,
		)
		if err := client.Connect(ctx); err != nil {
			logger.Error("failed to connect MCP client", "name", mcpClient.Name, "error", err)
			os.Exit(1)
		}
		registry.Register(client)
		logger.Info("registered MCP client", "name", mcpClient.Name)
	}

	return registry
}

// parseLogLevel converts a LOG_LEVEL env value ("debug"/"info"/"warn"/"error",
// case-insensitive) into a slog.Level. Empty or unknown values default to Info.
func parseLogLevel(v string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// pendingListerAdapter adapts cache.SQLiteCache.ListPending to the
// webui.PendingLister interface (different value types).
type pendingListerAdapter struct {
	c *cache.SQLiteCache
}

func (a pendingListerAdapter) ListPending(ctx context.Context, limit int) ([]webui.PendingItem, error) {
	entries, err := a.c.ListPending(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]webui.PendingItem, 0, len(entries))
	for _, e := range entries {
		out = append(out, webui.PendingItem{Alert: e.Alert, CreatedAt: e.CreatedAt})
	}
	return out, nil
}

// startPendingJanitor periodically deletes pending_alerts entries older than
// maxAge so the table stays bounded over time.
func startPendingJanitor(ctx context.Context, c *cache.SQLiteCache, maxAge time.Duration, logger *slog.Logger) {
	if maxAge <= 0 {
		maxAge = 30 * 24 * time.Hour
	}
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				n, err := c.CleanupPending(cleanupCtx, now.Add(-maxAge))
				cancel()
				if err != nil {
					logger.Warn("pending janitor failed", "error", err)
				} else if n > 0 {
					logger.Info("pending janitor removed entries", "count", n, "older_than", maxAge.String())
				}
			}
		}
	}()
}
