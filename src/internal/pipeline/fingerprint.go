package pipeline

import "github.com/Duops/SherlockOps/internal/domain"

// Fingerprint produces a deterministic, 16-character hex string that uniquely
// identifies an alert by its name and stable labels.
// Deprecated: use domain.Fingerprint directly. This wrapper exists for backward
// compatibility.
func Fingerprint(alertName string, labels map[string]string) string {
	return domain.Fingerprint(alertName, labels)
}
