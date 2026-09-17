// Package httpclient builds outbound HTTP clients with an optional explicit
// proxy for destinations outside the cluster (LLM API, messengers).
package httpclient

import (
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// New returns a client with the given timeout. A non-empty proxyURL forces all
// requests through that proxy; an empty one keeps Go's default behaviour
// (HTTP_PROXY/HTTPS_PROXY/NO_PROXY from the environment).
func New(timeout time.Duration, proxyURL string) (*http.Client, error) {
	client := &http.Client{Timeout: timeout}
	if proxyURL == "" {
		return client, nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid proxy url %q", proxyURL)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(u)
	client.Transport = transport
	return client, nil
}

// ProxyFunc extracts the proxy function from a client built by New, or nil
// when the client uses the default transport.
func ProxyFunc(client *http.Client) func(*http.Request) (*url.URL, error) {
	if client == nil {
		return nil
	}
	if t, ok := client.Transport.(*http.Transport); ok {
		return t.Proxy
	}
	return nil
}
