package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// AlertsReceived counts total alerts received, partitioned by source.
	AlertsReceived = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "alerts_received_total",
			Help: "Total number of alerts received by source.",
		},
		[]string{"source"},
	)

	// AlertsAnalyzed counts total alerts that went through analysis, partitioned by status.
	AlertsAnalyzed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "alerts_analyzed_total",
			Help: "Total number of alerts analyzed by status (success, error, cached).",
		},
		[]string{"status"},
	)

	// AnalysisDuration tracks how long alert analyses take.
	AnalysisDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "analysis_duration_seconds",
			Help:    "Duration of alert analysis in seconds.",
			Buckets: prometheus.DefBuckets,
		},
	)

	// LLMCallsTotal counts LLM API calls partitioned by provider and status.
	LLMCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llm_calls_total",
			Help: "Total number of LLM API calls by provider and status.",
		},
		[]string{"provider", "status"},
	)

	// ToolCallsTotal counts tool invocations partitioned by tool name and status.
	ToolCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tool_calls_total",
			Help: "Total number of tool calls by tool name and status.",
		},
		[]string{"tool", "status"},
	)

	// QueueDepth reports the current depth of the worker pool queue.
	QueueDepth = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "worker_queue_depth",
			Help: "Current number of alerts in the worker queue.",
		},
	)

	// ActiveWorkers reports the current number of workers actively processing alerts.
	ActiveWorkers = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "active_workers",
			Help: "Current number of workers actively processing alerts.",
		},
	)
)

func init() {
	prometheus.MustRegister(
		AlertsReceived,
		AlertsAnalyzed,
		AnalysisDuration,
		LLMCallsTotal,
		ToolCallsTotal,
		QueueDepth,
		ActiveWorkers,
	)
}

// Handler returns an HTTP handler that serves Prometheus metrics.
func Handler() http.Handler {
	return promhttp.Handler()
}
