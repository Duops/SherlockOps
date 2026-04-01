package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	// Load with empty path should apply defaults and pass validation
	// (webhooks.path_prefix defaults to "/webhook" so validation passes).
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load with empty path: %v", err)
	}

	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("server.host = %q, want %q", cfg.Server.Host, "0.0.0.0")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("server.port = %d, want %d", cfg.Server.Port, 8080)
	}
	if cfg.Server.MCPPort != 8081 {
		t.Errorf("server.mcp_port = %d, want %d", cfg.Server.MCPPort, 8081)
	}
	if cfg.LLM.Provider != "claude" {
		t.Errorf("llm.provider = %q, want %q", cfg.LLM.Provider, "claude")
	}
	if cfg.LLM.Model != "claude-sonnet-4-6" {
		t.Errorf("llm.model = %q, want %q", cfg.LLM.Model, "claude-sonnet-4-6")
	}
	if cfg.LLM.MaxTokens != 4096 {
		t.Errorf("llm.max_tokens = %d, want %d", cfg.LLM.MaxTokens, 4096)
	}
	if cfg.LLM.MaxIterations != 15 {
		t.Errorf("llm.max_iterations = %d, want %d", cfg.LLM.MaxIterations, 15)
	}
	if cfg.LLM.Language != "en" {
		t.Errorf("llm.language = %q, want %q", cfg.LLM.Language, "en")
	}
	if cfg.Messengers.Slack.DefaultChannel != "#alerts" {
		t.Errorf("slack.default_channel = %q, want %q", cfg.Messengers.Slack.DefaultChannel, "#alerts")
	}
	if cfg.Messengers.Telegram.ParseMode != "HTML" {
		t.Errorf("telegram.parse_mode = %q, want %q", cfg.Messengers.Telegram.ParseMode, "HTML")
	}
	if cfg.Cache.TTL != "24h" {
		t.Errorf("cache.ttl = %q, want %q", cfg.Cache.TTL, "24h")
	}
	if cfg.Cache.Path != "/data/cache.db" {
		t.Errorf("cache.path = %q, want %q", cfg.Cache.Path, "/data/cache.db")
	}
	if cfg.Cache.MinLength != 200 {
		t.Errorf("cache.min_length = %d, want %d", cfg.Cache.MinLength, 200)
	}
	if cfg.Webhooks.PathPrefix != "/webhook" {
		t.Errorf("webhooks.path_prefix = %q, want %q", cfg.Webhooks.PathPrefix, "/webhook")
	}
	if cfg.MCP.Bridge.Port != 8082 {
		t.Errorf("mcp.bridge.port = %d, want %d", cfg.MCP.Bridge.Port, 8082)
	}
}

func TestLoadFromFile(t *testing.T) {
	content := `
server:
  host: "127.0.0.1"
  port: 9090
llm:
  provider: "openai"
  api_key: "sk-test"
  model: "gpt-4"
  max_tokens: 2048
  max_iterations: 10
  language: "ru"
cache:
  ttl: "1h"
  path: "/tmp/cache.db"
  min_length: 100
webhooks:
  path_prefix: "/hooks"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load from file: %v", err)
	}

	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("server.host = %q, want %q", cfg.Server.Host, "127.0.0.1")
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("server.port = %d, want %d", cfg.Server.Port, 9090)
	}
	if cfg.LLM.Provider != "openai" {
		t.Errorf("llm.provider = %q, want %q", cfg.LLM.Provider, "openai")
	}
	if cfg.LLM.APIKey != "sk-test" {
		t.Errorf("llm.api_key = %q, want %q", cfg.LLM.APIKey, "sk-test")
	}
	if cfg.LLM.Model != "gpt-4" {
		t.Errorf("llm.model = %q, want %q", cfg.LLM.Model, "gpt-4")
	}
	if cfg.LLM.MaxTokens != 2048 {
		t.Errorf("llm.max_tokens = %d, want %d", cfg.LLM.MaxTokens, 2048)
	}
	if cfg.LLM.Language != "ru" {
		t.Errorf("llm.language = %q, want %q", cfg.LLM.Language, "ru")
	}
	if cfg.Cache.TTL != "1h" {
		t.Errorf("cache.ttl = %q, want %q", cfg.Cache.TTL, "1h")
	}
	// Defaults should still apply for unset fields.
	if cfg.Server.MCPPort != 8081 {
		t.Errorf("server.mcp_port = %d, want default %d", cfg.Server.MCPPort, 8081)
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	// Indentation error that yaml.v3 rejects.
	content := "server:\n  host: \"a\"\n bad_indent: true\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestEnvOverrides(t *testing.T) {
	envVars := map[string]string{
		"LLM_API_KEY":          "env-key-123",
		"LLM_PROVIDER":         "openai",
		"LLM_MODEL":            "gpt-4o",
		"LLM_BASE_URL":         "https://custom.api.com",
		"SLACK_BOT_TOKEN":       "xoxb-env",
		"SLACK_APP_TOKEN":       "xapp-env",
		"SLACK_SIGNING_SECRET":  "sec-env",
		"TELEGRAM_BOT_TOKEN":    "tg-env",
	}
	for k, v := range envVars {
		t.Setenv(k, v)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load with env overrides: %v", err)
	}

	if cfg.LLM.APIKey != "env-key-123" {
		t.Errorf("LLM.APIKey = %q, want %q", cfg.LLM.APIKey, "env-key-123")
	}
	if cfg.LLM.Provider != "openai" {
		t.Errorf("LLM.Provider = %q, want %q", cfg.LLM.Provider, "openai")
	}
	if cfg.LLM.Model != "gpt-4o" {
		t.Errorf("LLM.Model = %q, want %q", cfg.LLM.Model, "gpt-4o")
	}
	if cfg.LLM.BaseURL != "https://custom.api.com" {
		t.Errorf("LLM.BaseURL = %q, want %q", cfg.LLM.BaseURL, "https://custom.api.com")
	}
	if cfg.Messengers.Slack.BotToken != "xoxb-env" {
		t.Errorf("Slack.BotToken = %q, want %q", cfg.Messengers.Slack.BotToken, "xoxb-env")
	}
	if cfg.Messengers.Slack.AppToken != "xapp-env" {
		t.Errorf("Slack.AppToken = %q, want %q", cfg.Messengers.Slack.AppToken, "xapp-env")
	}
	if cfg.Messengers.Slack.SigningSecret != "sec-env" {
		t.Errorf("Slack.SigningSecret = %q, want %q", cfg.Messengers.Slack.SigningSecret, "sec-env")
	}
	if cfg.Messengers.Telegram.BotToken != "tg-env" {
		t.Errorf("Telegram.BotToken = %q, want %q", cfg.Messengers.Telegram.BotToken, "tg-env")
	}
}

func TestEnvOverridesFileValues(t *testing.T) {
	content := `
llm:
  api_key: "file-key"
  provider: "claude"
webhooks:
  path_prefix: "/webhook"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("LLM_API_KEY", "env-key")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LLM.APIKey != "env-key" {
		t.Errorf("env should override file: LLM.APIKey = %q, want %q", cfg.LLM.APIKey, "env-key")
	}
}

func TestCacheConfigTTLDuration(t *testing.T) {
	tests := []struct {
		ttl  string
		want time.Duration
	}{
		{"24h", 24 * time.Hour},
		{"30m", 30 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{"invalid", 24 * time.Hour}, // fallback
	}

	for _, tt := range tests {
		c := CacheConfig{TTL: tt.ttl}
		got := c.TTLDuration()
		if got != tt.want {
			t.Errorf("TTLDuration(%q) = %v, want %v", tt.ttl, got, tt.want)
		}
	}
}

func TestValidateInvalidProvider(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.LLM.Provider = "unknown"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for unknown provider")
	}
	if !contains(err.Error(), "llm.provider") {
		t.Errorf("error should mention llm.provider: %v", err)
	}
}

func TestValidateOpenAICompatibleRequiresBaseURL(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.LLM.Provider = "openai-compatible"
	cfg.LLM.BaseURL = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing base_url")
	}
	if !contains(err.Error(), "base_url") {
		t.Errorf("error should mention base_url: %v", err)
	}
}

func TestValidateSlackRequiresTokens(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.Messengers.Slack.Enabled = true
	// Tokens left empty.

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing Slack tokens")
	}
	if !contains(err.Error(), "bot_token") {
		t.Errorf("error should mention bot_token: %v", err)
	}
	// app_token is optional — no validation error expected for it
}

func TestValidateTelegramRequiresToken(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.Messengers.Telegram.Enabled = true

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing Telegram token")
	}
	if !contains(err.Error(), "telegram") {
		t.Errorf("error should mention telegram: %v", err)
	}
}

func TestValidateNoMessengerOrWebhook(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.Webhooks.PathPrefix = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error when no messenger and no webhook")
	}
	if !contains(err.Error(), "at least one messenger") {
		t.Errorf("error should mention messenger requirement: %v", err)
	}
}

func TestValidatePrometheusRequiresURL(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.Tools.Prometheus.Enabled = true

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing Prometheus URL")
	}
	if !contains(err.Error(), "prometheus.url") {
		t.Errorf("error should mention prometheus.url: %v", err)
	}
}

func TestValidateLokiRequiresURL(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.Tools.Loki.Enabled = true

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing Loki URL")
	}
	if !contains(err.Error(), "loki.url") {
		t.Errorf("error should mention loki.url: %v", err)
	}
}

func TestValidateInvalidCacheTTL(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.Cache.TTL = "not-a-duration"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for invalid cache TTL")
	}
	if !contains(err.Error(), "cache.ttl") {
		t.Errorf("error should mention cache.ttl: %v", err)
	}
}

func TestValidateInvalidPort(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.Server.Port = 0

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for invalid port")
	}
	if !contains(err.Error(), "server.port") {
		t.Errorf("error should mention server.port: %v", err)
	}
}

func TestValidateMCPClientRequiresURL(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	cfg.MCP.Clients = []MCPClientConfig{
		{Name: "test-client", URL: ""},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for MCP client without URL")
	}
	if !contains(err.Error(), "mcp.clients[0].url") {
		t.Errorf("error should mention mcp.clients[0].url: %v", err)
	}
}

func TestValidateValidConfig(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	// Default webhooks.path_prefix = "/webhook" satisfies "at least one mode".

	if err := cfg.Validate(); err != nil {
		t.Errorf("expected valid config, got: %v", err)
	}
}

func TestLoadFullYAML(t *testing.T) {
	content := `
server:
  host: "localhost"
  port: 3000
  mcp_port: 3001
llm:
  provider: "openai-compatible"
  api_key: "test-key"
  base_url: "https://api.local"
  model: "local-model"
  max_tokens: 1024
  max_iterations: 5
  system_prompt: "You are a helpful assistant."
  language: "de"
messengers:
  slack:
    enabled: true
    bot_token: "xoxb-test"
    app_token: "xapp-test"
    signing_secret: "secret"
    listen_channels: ["C123", "C456"]
    default_channel: "#ops"
  telegram:
    enabled: true
    bot_token: "tg-token"
    listen_chats: [100, 200]
    default_chat: 100
    parse_mode: "Markdown"
cache:
  ttl: "12h"
  path: "/tmp/test.db"
  min_length: 50
webhooks:
  path_prefix: "/api/webhook"
tools:
  prometheus:
    enabled: true
    url: "http://prom:9090"
    username: "admin"
    password: "pass"
  loki:
    enabled: true
    url: "http://loki:3100"
  kubernetes:
    enabled: true
    kubeconfig: "/home/user/.kube/config"
    context: "prod"
mcp:
  clients:
    - name: "remote"
      url: "https://mcp.example.com"
      auth: "bearer"
      token: "tok"
      headers:
        X-Custom: "value"
  bridge:
    enabled: true
    port: 9000
`
	dir := t.TempDir()
	path := filepath.Join(dir, "full.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load full YAML: %v", err)
	}

	// Spot-check a few nested fields.
	if len(cfg.Messengers.Slack.ListenChannels) != 2 {
		t.Errorf("slack.listen_channels len = %d, want 2", len(cfg.Messengers.Slack.ListenChannels))
	}
	if len(cfg.Messengers.Telegram.ListenChats) != 2 {
		t.Errorf("telegram.listen_chats len = %d, want 2", len(cfg.Messengers.Telegram.ListenChats))
	}
	if cfg.Messengers.Telegram.DefaultChat != 100 {
		t.Errorf("telegram.default_chat = %d, want 100", cfg.Messengers.Telegram.DefaultChat)
	}
	if cfg.Tools.Kubernetes.Context != "prod" {
		t.Errorf("kubernetes.context = %q, want %q", cfg.Tools.Kubernetes.Context, "prod")
	}
	if len(cfg.MCP.Clients) != 1 {
		t.Fatalf("mcp.clients len = %d, want 1", len(cfg.MCP.Clients))
	}
	if cfg.MCP.Clients[0].Headers["X-Custom"] != "value" {
		t.Errorf("mcp client header X-Custom = %q, want %q", cfg.MCP.Clients[0].Headers["X-Custom"], "value")
	}
	if cfg.MCP.Bridge.Port != 9000 {
		t.Errorf("mcp.bridge.port = %d, want 9000", cfg.MCP.Bridge.Port)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
