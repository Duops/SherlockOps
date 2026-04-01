package messenger

import (
	"sort"
	"strings"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

// extractTarget picks the most relevant target from labels.
// Priority: pod > instance > host > container > service > deployment > job.
func extractTarget(alert *domain.Alert) (targetType, targetName string) {
	priorities := []struct{ label, display string }{
		{"pod", "pod"},
		{"instance", "instance"},
		{"host", "host"},
		{"container", "container"},
		{"service", "service"},
		{"deployment", "deployment"},
		{"job", "job"},
	}
	for _, p := range priorities {
		if v, ok := alert.Labels[p.label]; ok && v != "" {
			return p.display, v
		}
	}
	return "", ""
}

// severityColor returns hex color for Slack attachment sidebar.
func severityColor(sev domain.Severity, status domain.AlertStatus) string {
	if status == domain.StatusResolved {
		return "#2EB67D" // green
	}
	switch sev {
	case domain.SeverityCritical:
		return "#E01E5A" // red
	case domain.SeverityWarning:
		return "#ECB22E" // yellow
	default:
		return "#36C5F0" // blue (info)
	}
}

// alertSeverityEmoji returns colored square emoji based on severity and status.
func alertSeverityEmoji(sev domain.Severity, status domain.AlertStatus) string {
	if status == domain.StatusResolved {
		return "\U0001F7E9" // green square
	}
	switch sev {
	case domain.SeverityCritical:
		return "\U0001F7E5" // red square
	case domain.SeverityWarning:
		return "\U0001F7E8" // yellow square
	default:
		return "\U0001F7E6" // blue square
	}
}

// formatLabelsContext returns secondary labels as compact text.
// Excludes alertname, severity, and the provided extra keys.
func formatLabelsContext(labels map[string]string, excludeKeys ...string) string {
	exclude := map[string]bool{"alertname": true, "severity": true}
	for _, k := range excludeKeys {
		if k != "" {
			exclude[k] = true
		}
	}

	var parts []string
	for k, v := range labels {
		if exclude[k] {
			continue
		}
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, " | ")
}

// formatToolsTrace builds a compact tools trace line from the list of tool names.
// formatToolsTraceFromResult builds a compact trace from ToolsTrace entries.
// Output: "prometheus ✓  k8s ✓  loki ✗"
func formatToolsTraceFromResult(result *domain.AnalysisResult) string {
	if len(result.ToolsTrace) > 0 {
		var parts []string
		for _, t := range result.ToolsTrace {
			if t.Success {
				parts = append(parts, t.Name+" ✓")
			} else {
				parts = append(parts, t.Name+" ✗")
			}
		}
		return strings.Join(parts, "  ")
	}
	// Fallback for cached results without ToolsTrace.
	if len(result.ToolsUsed) > 0 {
		var parts []string
		for _, t := range result.ToolsUsed {
			parts = append(parts, t+" ✓")
		}
		return strings.Join(parts, "  ")
	}
	return ""
}
