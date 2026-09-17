package httpclient

import (
	"net/http"
	"testing"
	"time"
)

func TestNew_WithoutProxyUsesDefaultTransport(t *testing.T) {
	c, err := New(5*time.Second, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Transport != nil {
		t.Errorf("expected nil transport, got %T", c.Transport)
	}
	if c.Timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", c.Timeout)
	}
	if ProxyFunc(c) != nil {
		t.Error("expected nil proxy func for default transport")
	}
}

func TestNew_WithProxy(t *testing.T) {
	c, err := New(time.Second, "http://user:pass@proxy.local:8888")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fn := ProxyFunc(c)
	if fn == nil {
		t.Fatal("expected proxy func")
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.anthropic.com/v1/messages", nil)
	u, err := fn(req)
	if err != nil || u == nil || u.Host != "proxy.local:8888" || u.User.Username() != "user" {
		t.Errorf("proxy for request = %v, %v; want proxy.local:8888 with user", u, err)
	}
}

func TestNew_InvalidProxy(t *testing.T) {
	for _, bad := range []string{"proxy.local:8888", "://x", "http://"} {
		if _, err := New(time.Second, bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}
