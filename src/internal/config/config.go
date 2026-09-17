package config

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for sherlockops.
type Config struct {
	Server       ServerConfig                 `yaml:"server"`
	LLM          LLMConfig                    `yaml:"llm"`
	Messengers   MessengersConfig             `yaml:"messengers"`
	Cache        CacheConfig                  `yaml:"cache"`
	Webhooks     WebhooksConfig               `yaml:"webhooks"`
	Tools        ToolsConfig                  `yaml:"tools"`
	MCP          MCPConfig                    `yaml:"mcp"`
	Pipeline     PipelineConfig               `yaml:"pipeline"`
	Runbooks     RunbookConfig                `yaml:"runbooks"`
	Health       HealthConfig                 `yaml:"health"`
	Stats        StatsConfig                  `yaml:"stats"`
	Proxy        ProxyConfig                  `yaml:"proxy"`
	Environments map[string]EnvironmentConfig `yaml:"environments"`
}

// ProxyConfig holds the default outbound proxy for the LLM API and messengers (not tools).
type ProxyConfig struct {
	URL string `yaml:"url"`
}

// ProxyDirect is the proxy_url value that disables an inherited proxy.
const ProxyDirect = "direct"

// ResolveProxy returns the first non-empty proxy, most specific first; ProxyDirect means none.
func ResolveProxy(levels ...string) string {
	for _, v := range levels {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if v == ProxyDirect {
			return ""
		}
		return v
	}
	return ""
}

// LLMProxy is the effective proxy for the LLM API.
func (c *Config) LLMProxy() string { return ResolveProxy(c.LLM.ProxyURL, c.Proxy.URL) }

// MessengerProxy is the effective proxy for one messenger's own proxy_url.
func (c *Config) MessengerProxy(own string) string {
	return ResolveProxy(own, c.Messengers.ProxyURL, c.Proxy.URL)
}

// ToolProxy is the effective proxy for one tool (tool → tools block → default tools block; never proxy.url).
func ToolProxy(own string, blocks ...string) string {
	return ResolveProxy(append([]string{own}, blocks...)...)
}

// HealthConfig holds tool connectivity monitoring settings.
type HealthConfig struct {
	ToolCheckInterval string `yaml:"tool_check_interval"`
}

// ToolCheckIntervalDuration parses the interval; invalid or empty falls back to 1m.
func (h HealthConfig) ToolCheckIntervalDuration() time.Duration {
	d, err := time.ParseDuration(h.ToolCheckInterval)
	if err != nil || d <= 0 {
		return time.Minute
	}
	return d
}

// StatsConfig holds alert volume statistics settings.
type StatsConfig struct {
	Retention      string `yaml:"retention"`
	ReviewInterval string `yaml:"review_interval"` // "1d", "1w", "1mo", Go duration, or "" to disable
}

// ReviewIntervalDuration parses the review interval; empty, "0" or invalid means disabled.
func (s StatsConfig) ReviewIntervalDuration() time.Duration {
	d, err := ParseInterval(s.ReviewInterval)
	if err != nil {
		return 0
	}
	return d
}

// ParseInterval parses Go durations plus day/week/month suffixes: "1d", "2w", "1mo".
func ParseInterval(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "" || v == "0" {
		return 0, nil
	}
	units := map[string]time.Duration{"d": 24 * time.Hour, "w": 7 * 24 * time.Hour, "mo": 30 * 24 * time.Hour}
	for suffix, unit := range units {
		if !strings.HasSuffix(v, suffix) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(v, suffix))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid interval %q", v)
		}
		return time.Duration(n) * unit, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid interval %q", v)
	}
	return d, nil
}

// RetentionDuration parses the retention; invalid or empty falls back to 90 days.
func (s StatsConfig) RetentionDuration() time.Duration {
	d, err := time.ParseDuration(s.Retention)
	if err != nil || d <= 0 {
		return 90 * 24 * time.Hour
	}
	return d
}

// EnvironmentConfig holds per-environment overrides for tools, MCP, and LLM settings.
type EnvironmentConfig struct {
	Tools ToolsConfig             `yaml:"tools"`
	MCP   MCPConfig               `yaml:"mcp"`
	LLM   *LLMEnvironmentOverride `yaml:"llm,omitempty"`
}

// LLMEnvironmentOverride allows overriding the system prompt per environment.
type LLMEnvironmentOverride struct {
	SystemPrompt string `yaml:"system_prompt"`
}

// RunbookConfig holds runbook knowledge base settings.
type RunbookConfig struct {
	Enabled bool   `yaml:"enabled"`
	Dir     string `yaml:"dir"`
}

// PipelineConfig holds worker pool and concurrency settings.
type PipelineConfig struct {
	// Mode controls how incoming alerts are processed:
	//   "auto"   — analyze every alert immediately (default)
	//   "manual" — post the raw alert and wait for "@bot analyze" mention
	Mode             string `yaml:"mode"`
	Workers          int    `yaml:"workers"`
	QueueSize        int    `yaml:"queue_size"`
	MaxConcurrentLLM int    `yaml:"max_concurrent_llm"`
}

// PipelineMode constants.
const (
	PipelineModeAuto   = "auto"
	PipelineModeManual = "manual"
)

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
	MCPPort int    `yaml:"mcp_port"`
}

// LLMConfig holds LLM provider settings.
type LLMConfig struct {
	Provider        string  `yaml:"provider"`
	APIKey          string  `yaml:"api_key"`
	BaseURL         string  `yaml:"base_url"`
	Model           string  `yaml:"model"`
	MaxTokens       int     `yaml:"max_tokens"`
	MaxIterations   int     `yaml:"max_iterations"`
	SystemPrompt    string  `yaml:"system_prompt"`
	Language        string  `yaml:"language"`
	ProxyURL        string  `yaml:"proxy_url"`         // overrides proxy.url for the LLM API; "direct" disables
	InputTokenCost  float64 `yaml:"input_token_cost"`  // $/1M input tokens (0 = auto-detect from model)
	OutputTokenCost float64 `yaml:"output_token_cost"` // $/1M output tokens (0 = auto-detect from model)
	// MaxToolOutputChars caps a single tool result's content before it is
	// sent to the LLM. Prevents runaway context growth from big blobs
	// (k8s listings, loki log chunks). 0 = no cap. Default 20000.
	MaxToolOutputChars int `yaml:"max_tool_output_chars"`
	// ContextSoftLimitTokens stops analysis early once input+output tokens
	// exceed this budget, returning a best-effort answer instead of a
	// provider "prompt too long" 400. 0 = no limit. Default 800000.
	ContextSoftLimitTokens int `yaml:"context_soft_limit_tokens"`
}

// MessengersConfig holds all messenger configurations.
type MessengersConfig struct {
	Slack    SlackConfig        `yaml:"slack"`
	Telegram TelegramConfig     `yaml:"telegram"`
	Teams    TeamsConfig        `yaml:"teams"`
	Display  AlertDisplayConfig `yaml:"alert_display"`
	ProxyURL string             `yaml:"proxy_url"` // default for all messengers
}

// AlertDisplayConfig toggles which optional fields are rendered in the alert
// message sent to messengers. All fields default to true; set to false to hide.
type AlertDisplayConfig struct {
	Level       bool `yaml:"level"`
	Env         bool `yaml:"env"`
	Target      bool `yaml:"target"`
	Summary     bool `yaml:"summary"`
	Description bool `yaml:"description"`
	Labels      bool `yaml:"labels"`
}

// TeamsConfig holds Microsoft Teams messenger settings.
type TeamsConfig struct {
	Enabled        bool   `yaml:"enabled"`
	ProxyURL       string `yaml:"proxy_url"`
	WebhookURL     string `yaml:"webhook_url"` // simple mode: incoming webhook
	TenantID       string `yaml:"tenant_id"`   // bot framework mode
	ClientID       string `yaml:"client_id"`
	ClientSecret   string `yaml:"client_secret"`
	DefaultTeam    string `yaml:"default_team"`
	DefaultChannel string `yaml:"default_channel"`
	ListenPort     int    `yaml:"listen_port"` // bot framework listener port (default 3978)
}

// SlackConfig holds Slack messenger settings.
type SlackConfig struct {
	Enabled        bool     `yaml:"enabled"`
	ProxyURL       string   `yaml:"proxy_url"`
	BotToken       string   `yaml:"bot_token"`
	AppToken       string   `yaml:"app_token"`
	SigningSecret  string   `yaml:"signing_secret"`
	ListenChannels []string `yaml:"listen_channels"`
	DefaultChannel string   `yaml:"default_channel"`
}

// TelegramConfig holds Telegram messenger settings.
type TelegramConfig struct {
	Enabled     bool    `yaml:"enabled"`
	ProxyURL    string  `yaml:"proxy_url"`
	BotToken    string  `yaml:"bot_token"`
	ListenChats []int64 `yaml:"listen_chats"`
	DefaultChat int64   `yaml:"default_chat"`
	ParseMode   string  `yaml:"parse_mode"`
}

// CacheConfig holds cache settings.
type CacheConfig struct {
	TTL       string `yaml:"ttl"`
	Path      string `yaml:"path"`
	MinLength int    `yaml:"min_length"`
}

// TTLDuration parses the TTL string into a time.Duration.
func (c CacheConfig) TTLDuration() time.Duration {
	d, err := time.ParseDuration(c.TTL)
	if err != nil {
		return 24 * time.Hour
	}
	return d
}

// WebhooksConfig holds webhook settings.
type WebhooksConfig struct {
	PathPrefix string `yaml:"path_prefix"`
}

// ToolsConfig holds external tool configurations.
type ToolsConfig struct {
	Prometheus      PrometheusConfig      `yaml:"prometheus"`
	ProxyURL        string                `yaml:"proxy_url"` // default for all tools in this block
	VictoriaMetrics VictoriaMetricsConfig `yaml:"victoriametrics"`
	Loki            LokiConfig            `yaml:"loki"`
	Kubernetes      KubernetesConfig      `yaml:"kubernetes"`
	VSphere         VSphereConfig         `yaml:"vsphere"`
	AWS             AWSConfig             `yaml:"aws"`
	GCP             GCPConfig             `yaml:"gcp"`
	Azure           AzureConfig           `yaml:"azure"`
	Postgres        PostgresConfig        `yaml:"postgres"`
	MongoDB         MongoDBConfig         `yaml:"mongodb"`
	YandexCloud     YandexCloudConfig     `yaml:"yandex_cloud"`
	DigitalOcean    DigitalOceanConfig    `yaml:"digitalocean"`
}

// VictoriaMetricsConfig holds VictoriaMetrics connection settings.
// Uses the same Prometheus-compatible API, but listed separately for clarity.
type VictoriaMetricsConfig struct {
	Enabled  bool   `yaml:"enabled"`
	ProxyURL string `yaml:"proxy_url"`
	URL      string `yaml:"url"`      // e.g., http://victoriametrics:8428
	Username string `yaml:"username"` // basic auth (optional)
	Password string `yaml:"password"`
	Tenant   string `yaml:"tenant"` // for cluster version: "0:0" or "accountID:projectID"
}

// VSphereConfig holds VMware vSphere/vCenter connection settings.
type VSphereConfig struct {
	Enabled  bool   `yaml:"enabled"`
	ProxyURL string `yaml:"proxy_url"`
	URL      string `yaml:"url"` // e.g., https://vcenter.example.com
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Insecure bool   `yaml:"insecure"` // skip TLS verification
}

// AWSConfig holds AWS CloudWatch/EC2 connection settings.
type AWSConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Region    string `yaml:"region"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
}

// GCPConfig holds GCP Monitoring connection settings.
type GCPConfig struct {
	Enabled         bool   `yaml:"enabled"`
	ProjectID       string `yaml:"project_id"`
	CredentialsJSON string `yaml:"credentials_json"` // path to SA JSON or raw JSON
}

// AzureConfig holds Azure Monitor connection settings.
type AzureConfig struct {
	Enabled        bool   `yaml:"enabled"`
	TenantID       string `yaml:"tenant_id"`
	ClientID       string `yaml:"client_id"`
	ClientSecret   string `yaml:"client_secret"`
	SubscriptionID string `yaml:"subscription_id"`
}

// PostgresConfig holds PostgreSQL connection settings.
type PostgresConfig struct {
	Enabled bool   `yaml:"enabled"`
	DSN     string `yaml:"dsn"` // postgres://user:pass@host:5432/dbname?sslmode=disable
}

// MongoDBConfig holds MongoDB connection settings.
type MongoDBConfig struct {
	Enabled bool   `yaml:"enabled"`
	URI     string `yaml:"uri"` // mongodb://user:pass@host:27017/admin
}

// YandexCloudConfig holds Yandex Cloud connection settings.
type YandexCloudConfig struct {
	Enabled   bool   `yaml:"enabled"`
	CloudID   string `yaml:"cloud_id"`
	FolderID  string `yaml:"folder_id"`
	Token     string `yaml:"token"`      // IAM or OAuth token
	TokenType string `yaml:"token_type"` // "iam" or "oauth"
}

// DigitalOceanConfig holds DigitalOcean API connection settings.
type DigitalOceanConfig struct {
	Enabled bool   `yaml:"enabled"`
	Token   string `yaml:"token"` // API token
}

// PrometheusConfig holds Prometheus connection settings.
type PrometheusConfig struct {
	Enabled  bool   `yaml:"enabled"`
	ProxyURL string `yaml:"proxy_url"`
	URL      string `yaml:"url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// LokiConfig holds Loki connection settings.
type LokiConfig struct {
	Enabled  bool   `yaml:"enabled"`
	ProxyURL string `yaml:"proxy_url"`
	URL      string `yaml:"url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// KubernetesConfig holds Kubernetes connection settings.
type KubernetesConfig struct {
	Enabled    bool   `yaml:"enabled"`
	ProxyURL   string `yaml:"proxy_url"`
	Kubeconfig string `yaml:"kubeconfig"`
	Context    string `yaml:"context"`
}

// MCPConfig holds MCP client and bridge settings.
type MCPConfig struct {
	Clients []MCPClientConfig `yaml:"clients"`
	Bridge  MCPBridgeConfig   `yaml:"bridge"`
}

// MCPClientConfig holds a single MCP client connection.
type MCPClientConfig struct {
	Name     string            `yaml:"name"`
	ProxyURL string            `yaml:"proxy_url"`
	URL      string            `yaml:"url"`
	Auth     string            `yaml:"auth"`
	Token    string            `yaml:"token"`
	Headers  map[string]string `yaml:"headers"`
}

// MCPBridgeConfig holds MCP bridge settings.
type MCPBridgeConfig struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

// Load reads a YAML config file, applies defaults, then applies env overrides.
func Load(path string) (*Config, error) {
	cfg := &Config{}
	applyDefaults(cfg)

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config file: %w", err)
		}
		data, err = expandEnv(data)
		if err != nil {
			return nil, fmt.Errorf("config file: %w", err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config file: %w", err)
		}
	}

	applyEnvOverrides(cfg)
	inheritDefaultMCPClients(cfg)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

func applyDefaults(cfg *Config) {
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.Port = 8080
	cfg.Server.MCPPort = 8081

	cfg.LLM.Provider = "claude"
	cfg.LLM.Model = "claude-sonnet-4-6"
	cfg.LLM.MaxTokens = 4096
	cfg.LLM.MaxIterations = 30
	cfg.LLM.Language = "en"

	cfg.Messengers.Slack.DefaultChannel = "#alerts"
	cfg.Messengers.Telegram.ParseMode = "HTML"
	cfg.Messengers.Display = AlertDisplayConfig{
		Level:       true,
		Env:         true,
		Target:      true,
		Summary:     true,
		Description: true,
		Labels:      true,
	}

	cfg.Cache.TTL = "24h"
	cfg.Cache.Path = "/data/cache.db"
	cfg.Cache.MinLength = 200

	cfg.Webhooks.PathPrefix = "/webhook"

	cfg.MCP.Bridge.Port = 8082

	cfg.Pipeline.Mode = PipelineModeAuto
	cfg.Pipeline.Workers = 5
	cfg.Pipeline.QueueSize = 1000
	cfg.Pipeline.MaxConcurrentLLM = 3

	cfg.Runbooks.Dir = "/data/runbooks"

	cfg.Health.ToolCheckInterval = "1m"
	cfg.Stats.Retention = "2160h"
	cfg.Stats.ReviewInterval = "1w"
}

// inheritDefaultMCPClients copies top-level mcp.clients into environments without their own.
func inheritDefaultMCPClients(cfg *Config) {
	if len(cfg.MCP.Clients) == 0 {
		return
	}
	for name, env := range cfg.Environments {
		if len(env.MCP.Clients) > 0 {
			continue
		}
		env.MCP.Clients = append([]MCPClientConfig(nil), cfg.MCP.Clients...)
		cfg.Environments[name] = env
	}
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// expandEnv replaces ${VAR} and ${VAR:-default}; an unset VAR without default is an error.
func expandEnv(data []byte) ([]byte, error) {
	var missing []string
	out := envRef.ReplaceAllFunc(data, func(m []byte) []byte {
		parts := envRef.FindSubmatch(m)
		name := string(parts[1])
		if v, ok := os.LookupEnv(name); ok {
			return []byte(v)
		}
		if len(parts) > 2 && parts[2] != nil {
			return parts[2]
		}
		if bytes.Contains(m, []byte(":-")) {
			return nil
		}
		missing = append(missing, name)
		return m
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("environment variables not set: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func applyEnvOverrides(cfg *Config) {
	// Simple env var names — no prefix needed.
	// Docker-compose passes them directly from .env file.
	if v := os.Getenv("LLM_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}
	if v := os.Getenv("LLM_PROVIDER"); v != "" {
		cfg.LLM.Provider = v
	}
	if v := os.Getenv("LLM_MODEL"); v != "" {
		cfg.LLM.Model = v
	}
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		cfg.LLM.BaseURL = v
	}
	if v := os.Getenv("SLACK_BOT_TOKEN"); v != "" {
		cfg.Messengers.Slack.BotToken = v
	}
	if v := os.Getenv("SLACK_APP_TOKEN"); v != "" {
		cfg.Messengers.Slack.AppToken = v
	}
	if v := os.Getenv("SLACK_SIGNING_SECRET"); v != "" {
		cfg.Messengers.Slack.SigningSecret = v
	}
	if v := os.Getenv("TELEGRAM_BOT_TOKEN"); v != "" {
		cfg.Messengers.Telegram.BotToken = v
	}
	if v := os.Getenv("TEAMS_WEBHOOK_URL"); v != "" {
		cfg.Messengers.Teams.WebhookURL = v
	}
	if v := os.Getenv("TEAMS_CLIENT_ID"); v != "" {
		cfg.Messengers.Teams.ClientID = v
	}
	if v := os.Getenv("TEAMS_CLIENT_SECRET"); v != "" {
		cfg.Messengers.Teams.ClientSecret = v
	}
	if v := os.Getenv("PROXY_URL"); v != "" {
		cfg.Proxy.URL = v
	}
}

// Validate checks that the configuration is valid.
func (c *Config) Validate() error {
	var errs []string

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		errs = append(errs, "server.port must be between 1 and 65535")
	}
	if c.Server.MCPPort < 1 || c.Server.MCPPort > 65535 {
		errs = append(errs, "server.mcp_port must be between 1 and 65535")
	}

	validProviders := map[string]bool{"claude": true, "openai": true, "openai-compatible": true}
	if !validProviders[c.LLM.Provider] {
		errs = append(errs, fmt.Sprintf("llm.provider must be one of: claude, openai, openai-compatible; got %q", c.LLM.Provider))
	}
	if c.LLM.Provider == "openai-compatible" && c.LLM.BaseURL == "" {
		errs = append(errs, "llm.base_url is required when provider is openai-compatible")
	}
	if c.LLM.MaxTokens < 1 {
		errs = append(errs, "llm.max_tokens must be positive")
	}
	if c.LLM.MaxIterations < 1 {
		errs = append(errs, "llm.max_iterations must be positive")
	}

	switch c.Pipeline.Mode {
	case "", PipelineModeAuto, PipelineModeManual:
		// ok
	default:
		errs = append(errs, fmt.Sprintf("pipeline.mode must be one of: auto, manual; got %q", c.Pipeline.Mode))
	}

	if _, err := time.ParseDuration(c.Cache.TTL); err != nil {
		errs = append(errs, fmt.Sprintf("cache.ttl is not a valid duration: %v", err))
	}
	if c.Cache.MinLength < 0 {
		errs = append(errs, "cache.min_length must be non-negative")
	}
	if _, err := time.ParseDuration(c.Health.ToolCheckInterval); err != nil {
		errs = append(errs, fmt.Sprintf("health.tool_check_interval is not a valid duration: %v", err))
	}
	if _, err := time.ParseDuration(c.Stats.Retention); err != nil {
		errs = append(errs, fmt.Sprintf("stats.retention is not a valid duration: %v", err))
	}
	if _, err := ParseInterval(c.Stats.ReviewInterval); err != nil {
		errs = append(errs, fmt.Sprintf("stats.review_interval: %v (use e.g. 1d, 1w, 1mo or 72h)", err))
	}
	for field, v := range c.proxyFields() {
		if v == "" || v == ProxyDirect {
			continue
		}
		if u, err := url.Parse(v); err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, fmt.Sprintf("%s must be an absolute URL like http://host:port or %q, got %q", field, ProxyDirect, v))
		}
	}

	slackConfigured := c.Messengers.Slack.Enabled
	telegramConfigured := c.Messengers.Telegram.Enabled
	teamsConfigured := c.Messengers.Teams.Enabled
	webhooksConfigured := c.Webhooks.PathPrefix != ""

	if !slackConfigured && !telegramConfigured && !teamsConfigured && !webhooksConfigured {
		errs = append(errs, "at least one messenger must be enabled or webhooks must be configured")
	}

	if c.Messengers.Slack.Enabled {
		if c.Messengers.Slack.BotToken == "" {
			errs = append(errs, "messengers.slack.bot_token is required when Slack is enabled")
		}
		// app_token is optional — without it, Slack runs in webhook-only mode (no listener)
	}

	if c.Messengers.Telegram.Enabled {
		if c.Messengers.Telegram.BotToken == "" {
			errs = append(errs, "messengers.telegram.bot_token is required when Telegram is enabled")
		}
	}

	if c.Messengers.Teams.Enabled {
		hasWebhook := c.Messengers.Teams.WebhookURL != ""
		hasBotFramework := c.Messengers.Teams.TenantID != "" && c.Messengers.Teams.ClientID != "" && c.Messengers.Teams.ClientSecret != ""
		if !hasWebhook && !hasBotFramework {
			errs = append(errs, "messengers.teams requires either webhook_url or tenant_id+client_id+client_secret")
		}
	}

	if c.MCP.Bridge.Enabled && (c.MCP.Bridge.Port < 1 || c.MCP.Bridge.Port > 65535) {
		errs = append(errs, "mcp.bridge.port must be between 1 and 65535")
	}

	for i, client := range c.MCP.Clients {
		if client.Name != "" && client.URL == "" {
			errs = append(errs, fmt.Sprintf("mcp.clients[%d].url is required when name is set", i))
		}
	}

	if c.Tools.Prometheus.Enabled && c.Tools.Prometheus.URL == "" {
		errs = append(errs, "tools.prometheus.url is required when Prometheus is enabled")
	}
	if c.Tools.Loki.Enabled && c.Tools.Loki.URL == "" {
		errs = append(errs, "tools.loki.url is required when Loki is enabled")
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}

	return nil
}

// proxyFields lists every proxy_url in the config with its path, for validation.
func (c *Config) proxyFields() map[string]string {
	out := map[string]string{
		"proxy.url":                     c.Proxy.URL,
		"llm.proxy_url":                 c.LLM.ProxyURL,
		"messengers.proxy_url":          c.Messengers.ProxyURL,
		"messengers.slack.proxy_url":    c.Messengers.Slack.ProxyURL,
		"messengers.telegram.proxy_url": c.Messengers.Telegram.ProxyURL,
		"messengers.teams.proxy_url":    c.Messengers.Teams.ProxyURL,
	}
	addTools := func(prefix string, t ToolsConfig, m MCPConfig) {
		out[prefix+"proxy_url"] = t.ProxyURL
		out[prefix+"prometheus.proxy_url"] = t.Prometheus.ProxyURL
		out[prefix+"victoriametrics.proxy_url"] = t.VictoriaMetrics.ProxyURL
		out[prefix+"loki.proxy_url"] = t.Loki.ProxyURL
		out[prefix+"kubernetes.proxy_url"] = t.Kubernetes.ProxyURL
		out[prefix+"vsphere.proxy_url"] = t.VSphere.ProxyURL
		for i, cl := range m.Clients {
			out[fmt.Sprintf("%smcp.clients[%d].proxy_url", strings.TrimSuffix(prefix, "tools."), i)] = cl.ProxyURL
		}
	}
	addTools("tools.", c.Tools, c.MCP)
	for name, env := range c.Environments {
		addTools("environments."+name+".tools.", env.Tools, env.MCP)
	}
	return out
}
