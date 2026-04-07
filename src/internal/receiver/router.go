package receiver

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Duops/SherlockOps/internal/domain"
)

// maxBodySize is the maximum allowed webhook request body size (1 MB).
const maxBodySize = 1 << 20

// NewRouter creates an HTTP handler that registers POST routes for each receiver.
// Routes are: POST {prefix}/{receiver.Source()}
//
// The logger is required so that webhook intake logs share the same handler
// (level, format) as the rest of the service. Passing nil falls back to the
// default global slog logger as a safety net.
func NewRouter(prefix string, receivers []domain.Receiver, handler func([]domain.Alert), logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	mux := http.NewServeMux()

	prefix = strings.TrimRight(prefix, "/")

	for _, r := range receivers {
		rec := r // capture loop variable
		pattern := prefix + "/" + rec.Source()

		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}

			r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				logger.Error("failed to read request body", "source", rec.Source(), "error", err)
				http.Error(w, "request body too large or unreadable", http.StatusBadRequest)
				return
			}
			defer r.Body.Close()

			headers := make(map[string]string)
			for k, v := range r.Header {
				if len(v) > 0 {
					headers[k] = v[0]
				}
			}

			alerts, err := rec.Parse(r.Context(), body, headers)
			if err != nil {
				logger.Error("failed to parse alerts", "source", rec.Source(), "error", err)
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
				return
			}

			// Apply environment from header.
			applyEnvironmentHeader(r, alerts, logger)

			// Apply channel routing from headers.
			applyChannelHeaders(r, alerts)

			// Group alerts by alertname.
			alerts = GroupAlerts(alerts)

			logger.Info("received alerts", "source", rec.Source(), "count", len(alerts))

			func() {
				defer func() {
					if rv := recover(); rv != nil {
						logger.Error("handler panic", "source", rec.Source(), "panic", rv)
						writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
					}
				}()
				handler(alerts)
			}()

			writeJSON(w, http.StatusOK, map[string]int{"accepted": len(alerts)})
		})
	}

	return mux
}

// applyEnvironmentHeader reads X-Environment header and sets it on all alerts.
// The value is validated to contain only alphanumeric characters, hyphens, and
// underscores to prevent injection attacks.
func applyEnvironmentHeader(r *http.Request, alerts []domain.Alert, logger *slog.Logger) {
	env := r.Header.Get("X-Environment")
	if env == "" {
		return
	}
	// Validate: only allow safe characters (alphanumeric, hyphens, underscores, dots).
	for _, c := range env {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			logger.Warn("rejected invalid X-Environment header", "value", env)
			return
		}
	}
	if len(env) > 64 {
		logger.Warn("rejected oversized X-Environment header", "length", len(env))
		return
	}
	for i := range alerts {
		alerts[i].Environment = env
	}
}

// applyChannelHeaders reads X-Channel-Slack, X-Channel-Telegram, X-Channel-Teams
// headers and sets them as ReplyTarget on alerts for channel routing.
func applyChannelHeaders(r *http.Request, alerts []domain.Alert) {
	slackChannel := r.Header.Get("X-Channel-Slack")
	telegramChat := r.Header.Get("X-Channel-Telegram")
	teamsChannel := r.Header.Get("X-Channel-Teams")

	if slackChannel == "" && telegramChat == "" && teamsChannel == "" {
		return
	}

	overrides := make(map[string]string)
	if slackChannel != "" {
		overrides["slack"] = slackChannel
	}
	if telegramChat != "" {
		overrides["telegram"] = telegramChat
	}
	if teamsChannel != "" {
		overrides["teams"] = teamsChannel
	}

	for i := range alerts {
		alerts[i].ChannelOverrides = overrides
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
