package receiver

import (
	"github.com/shchepetkov/sherlockops/internal/domain"
)

// generateFingerprint produces a stable fingerprint from alertname and labels.
// Delegates to domain.Fingerprint for consistent behavior across the system.
func generateFingerprint(alertname string, labels map[string]string) string {
	return domain.Fingerprint(alertname, labels)
}
