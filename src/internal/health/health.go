package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
	"github.com/shchepetkov/sherlockops/internal/version"
)

// Checker provides liveness and readiness health check endpoints.
type Checker struct {
	cache      domain.Cache
	messengers []domain.Messenger
	logger     *slog.Logger
}

// NewChecker creates a Checker with the given dependencies.
func NewChecker(cache domain.Cache, messengers []domain.Messenger, logger *slog.Logger) *Checker {
	return &Checker{
		cache:      cache,
		messengers: messengers,
		logger:     logger,
	}
}

type healthResponse struct {
	Status  string            `json:"status"`
	Version string            `json:"version"`
	Checks  map[string]string `json:"checks,omitempty"`
}

// Liveness returns 200 OK if the process is alive. This is a basic liveness probe
// suitable for Kubernetes.
func (c *Checker) Liveness(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:  "ok",
		Version: version.Version,
	})
}

// Readiness verifies that critical dependencies are reachable. It checks cache
// connectivity and reports messenger count. Returns 503 if any critical check fails.
func (c *Checker) Readiness(w http.ResponseWriter, r *http.Request) {
	checks := make(map[string]string)
	healthy := true

	// Check cache connectivity by performing a lightweight read.
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	_, err := c.cache.Get(ctx, "__health_check__")
	if err != nil {
		checks["cache"] = "error: " + err.Error()
		healthy = false
		c.logger.Warn("readiness check: cache unhealthy", "error", err)
	} else {
		checks["cache"] = "ok"
	}

	// Report messenger status.
	if len(c.messengers) == 0 {
		checks["messengers"] = "none configured"
	} else {
		for _, m := range c.messengers {
			checks["messenger_"+m.Name()] = "configured"
		}
	}

	status := "ok"
	code := http.StatusOK
	if !healthy {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	writeJSON(w, code, healthResponse{
		Status:  status,
		Version: version.Version,
		Checks:  checks,
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
