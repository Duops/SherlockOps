package llm

import (
	"net/http"
	"testing"
	"time"
)

func TestNewProvider_WithHTTPClient(t *testing.T) {
	custom := &http.Client{Timeout: 7 * time.Second}
	p, err := NewProvider("claude", "key", "", "", 0, WithHTTPClient(custom))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	ap, ok := p.(*AnthropicProvider)
	if !ok {
		t.Fatalf("expected *AnthropicProvider, got %T", p)
	}
	if ap.client != custom {
		t.Error("custom http client was not applied")
	}

	p, err = NewProvider("openai", "key", "", "", 0, WithHTTPClient(nil))
	if err != nil {
		t.Fatalf("NewProvider openai: %v", err)
	}
	if op := p.(*OpenAIProvider); op.client == nil || op.client.Timeout != 120*time.Second {
		t.Error("nil option must keep the default client")
	}
}
