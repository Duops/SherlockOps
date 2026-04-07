package domain

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"
)

type AlertStatus string

const (
	StatusFiring   AlertStatus = "firing"
	StatusResolved AlertStatus = "resolved"
)

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

// Alert is the normalized representation of an alert from any monitoring source.
type Alert struct {
	ID          string
	Fingerprint string
	RequestID   string // correlation ID for tracing across the pipeline
	Source      string // "alertmanager", "grafana", "zabbix", "datadog", "elk", "loki", "generic"
	Status      AlertStatus
	Severity    Severity
	Name        string
	Labels      map[string]string
	Annotations map[string]string
	RawText     string // assembled text for LLM
	StartsAt    time.Time
	EndsAt      time.Time
	ReplyTarget      *ReplyTarget      // for bot listener mode (single target)
	ChannelOverrides map[string]string // per-messenger channel overrides from X-Channel-* headers
	UserCommand      string            // from @bot mention
	ReceivedAt       time.Time
	Environment      string // from X-Environment header; empty means default
	GroupKey         string   // Alertmanager group key for grouping alerts
	GroupedAlerts    []*Alert // non-nil when this is a grouped alert
}

// ReplyTarget specifies where to send the analysis response.
type ReplyTarget struct {
	Messenger string // "slack", "telegram"
	Channel   string // Slack channel ID or Telegram chat ID
	ThreadID  string // Slack thread_ts or Telegram message_id
}

// MessageRef tracks a posted message for later reply/edit in two-phase delivery.
type MessageRef struct {
	Messenger string // "slack", "telegram", "teams"
	Channel   string // channel/chat ID
	MessageID string // Slack ts, Telegram message_id, Teams activity_id
	Alert     *Alert // the original alert
}

// ephemeralLabels are labels excluded from fingerprinting because they change
// across pod restarts or rescheduling, and should not create distinct fingerprints.
var ephemeralLabels = map[string]struct{}{
	"pod":          {},
	"instance":     {},
	"pod_name":     {},
	"container_id": {},
}

// Fingerprint produces a deterministic, 16-character hex string that uniquely
// identifies an alert by its name and stable labels. Ephemeral labels (pod,
// instance, pod_name, container_id) are excluded so that the same alert firing
// on different pods maps to the same fingerprint.
func Fingerprint(alertName string, labels map[string]string) string {
	// Collect non-ephemeral label keys.
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if _, skip := ephemeralLabels[k]; !skip {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	// Build canonical source string: alertname|key1=val1,key2=val2,...
	var b strings.Builder
	b.WriteString(alertName)
	b.WriteByte('|')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
	}

	h := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("%x", h[:8]) // first 8 bytes = 16 hex chars
}

// BuildRawText assembles a text summary from alert fields for LLM consumption.
func BuildRawText(name string, severity string, labels, annotations map[string]string) string {
	var b strings.Builder
	b.WriteString("Alert: " + name + "\n")
	b.WriteString("Severity: " + severity + "\n")

	if len(labels) > 0 {
		b.WriteString("Labels:\n")
		keys := sortedMapKeys(labels)
		for _, k := range keys {
			b.WriteString("  " + k + ": " + labels[k] + "\n")
		}
	}

	if len(annotations) > 0 {
		b.WriteString("Annotations:\n")
		keys := sortedMapKeys(annotations)
		for _, k := range keys {
			b.WriteString("  " + k + ": " + annotations[k] + "\n")
		}
	}

	return b.String()
}

func sortedMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
