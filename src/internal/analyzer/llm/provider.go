package llm

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

// NewProvider creates an LLMProvider based on the provider name.
// Supported providers: "claude" (Anthropic), "openai", "openai-compatible".
// Option customizes NewProvider.
type Option func(*options)

type options struct {
	client *http.Client
}

// WithHTTPClient replaces the default HTTP client (e.g. to route through a proxy).
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) {
		if c != nil {
			o.client = c
		}
	}
}

func NewProvider(provider, apiKey, baseURL, model string, maxTokens int, opts ...Option) (domain.LLMProvider, error) {
	o := &options{client: &http.Client{Timeout: 120 * time.Second}}
	for _, opt := range opts {
		opt(o)
	}
	client := o.client

	switch provider {
	case "claude":
		if model == "" {
			model = "claude-sonnet-4-6"
		}
		if maxTokens <= 0 {
			maxTokens = 4096
		}
		return &AnthropicProvider{
			apiKey:    apiKey,
			model:     model,
			maxTokens: maxTokens,
			client:    client,
		}, nil

	case "openai", "openai-compatible":
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		if model == "" {
			model = "gpt-4o"
		}
		if maxTokens <= 0 {
			maxTokens = 4096
		}
		return &OpenAIProvider{
			apiKey:    apiKey,
			baseURL:   baseURL,
			model:     model,
			maxTokens: maxTokens,
			client:    client,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported LLM provider: %q (supported: claude, openai, openai-compatible)", provider)
	}
}
