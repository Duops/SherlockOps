package messenger

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Duops/SherlockOps/internal/domain"
)

// extractTarget picks the most relevant target from labels.
// Priority: well-known infra labels first, then any non-meta label as fallback.
func extractTarget(alert *domain.Alert) (targetType, targetName string) {
	priorities := []struct{ label, display string }{
		{"pod", "pod"},
		{"instance", "instance"},
		{"host", "host"},
		{"container", "container"},
		{"service", "service"},
		{"deployment", "deployment"},
		{"job", "job"},
		{"queue", "queue"},
		{"topic", "topic"},
		{"database", "database"},
		{"table", "table"},
		{"vhost", "vhost"},
		{"namespace", "namespace"},
		{"node", "node"},
		{"disk", "disk"},
		{"device", "device"},
		{"endpoint", "endpoint"},
		{"url", "url"},
	}
	for _, p := range priorities {
		if v, ok := alert.Labels[p.label]; ok && v != "" {
			return p.display, v
		}
	}
	// Fallback: pick first non-meta label alphabetically.
	meta := map[string]bool{
		"alertname": true, "severity": true, "alertgroup": true,
		"prometheus": true, "grafana_folder": true,
	}
	var keys []string
	for k := range alert.Labels {
		if !meta[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		return keys[0], alert.Labels[keys[0]]
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

// formatToolsTraceFromResult builds a compact trace from ToolsTrace entries.
// Output: "victoriametrics ✓(3)  kubernetes ✓(2)  loki ✗(1) | 12.4k tokens"
func formatToolsTraceFromResult(result *domain.AnalysisResult) string {
	if len(result.ToolsTrace) > 0 {
		var parts []string
		for _, t := range result.ToolsTrace {
			mark := "✗"
			if t.Success {
				mark = "✓"
			}
			if t.CallCount > 0 {
				parts = append(parts, fmt.Sprintf("%s %s(%d)", t.Name, mark, t.CallCount))
			} else {
				parts = append(parts, t.Name+" "+mark)
			}
		}
		trace := strings.Join(parts, "  ")
		if result.TotalTokens > 0 {
			trace += fmt.Sprintf(" | %s tokens", formatTokenCount(result.TotalTokens))
			if cost := estimateCost(result.Model, result.InputTokens, result.OutputTokens, result.InputTokenCost, result.OutputTokenCost); cost != "" {
				trace += " ~" + cost
			}
		}
		return trace
	}
	// Fallback for cached results without ToolsTrace — group by category.
	if len(result.ToolsUsed) > 0 {
		catCount := make(map[string]int)
		for _, t := range result.ToolsUsed {
			cat := t
			for i, c := range t {
				if c == '_' {
					cat = t[:i]
					break
				}
			}
			catCount[cat]++
		}
		catKeys := make([]string, 0, len(catCount))
		for cat := range catCount {
			catKeys = append(catKeys, cat)
		}
		sort.Strings(catKeys)
		var parts []string
		for _, cat := range catKeys {
			parts = append(parts, fmt.Sprintf("%s ✓(%d)", cat, catCount[cat]))
		}
		return strings.Join(parts, "  ")
	}
	return ""
}

// formatTokenCount formats token count as human-readable string: 1234 → "1.2k", 500 → "500".
func formatTokenCount(tokens int) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000.0)
	}
	return fmt.Sprintf("%d", tokens)
}

// modelPricing holds input/output price per 1M tokens for known models.
type modelPricing struct {
	input  float64
	output float64
}

// knownPricing maps model name prefixes to their pricing.
// Prices in USD per 1M tokens (as of 2026).
var knownPricing = map[string]modelPricing{
	// Anthropic Claude
	"claude-opus":    {input: 15.0, output: 75.0},
	"claude-sonnet":  {input: 3.0, output: 15.0},
	"claude-haiku":   {input: 0.80, output: 4.0},
	// OpenAI
	"gpt-4o":         {input: 2.50, output: 10.0},
	"gpt-4o-mini":    {input: 0.15, output: 0.60},
	"gpt-4-turbo":    {input: 10.0, output: 30.0},
	"gpt-4":          {input: 30.0, output: 60.0},
	"gpt-3.5":        {input: 0.50, output: 1.50},
	// DeepSeek
	"deepseek":       {input: 0.27, output: 1.10},
}

// lookupPricing finds pricing by longest matching model name prefix.
// Longest match wins to avoid "gpt-4" matching before "gpt-4o-mini".
func lookupPricing(model string) (modelPricing, bool) {
	model = strings.ToLower(model)
	var bestPrefix string
	var bestPricing modelPricing
	for prefix, p := range knownPricing {
		if strings.HasPrefix(model, prefix) && len(prefix) > len(bestPrefix) {
			bestPrefix = prefix
			bestPricing = p
		}
	}
	if bestPrefix == "" {
		return modelPricing{}, false
	}
	return bestPricing, true
}

// estimateCost returns approximate USD cost string.
// Uses config pricing if provided (>0), otherwise falls back to built-in model pricing.
// Returns empty string if pricing unknown or tokens are zero.
func estimateCost(model string, inputTokens, outputTokens int, cfgInputCost, cfgOutputCost float64) string {
	if inputTokens == 0 && outputTokens == 0 {
		return ""
	}
	var inputPrice, outputPrice float64
	if cfgInputCost > 0 || cfgOutputCost > 0 {
		inputPrice = cfgInputCost
		outputPrice = cfgOutputCost
	} else {
		pricing, ok := lookupPricing(model)
		if !ok {
			return ""
		}
		inputPrice = pricing.input
		outputPrice = pricing.output
	}
	cost := float64(inputTokens)/1_000_000*inputPrice +
		float64(outputTokens)/1_000_000*outputPrice
	if cost < 0.001 {
		return "<$0.001"
	}
	return fmt.Sprintf("$%.3f", cost)
}
