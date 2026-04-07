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

	// TokensTotal counts total LLM tokens consumed by type (input, output).
	TokensTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llm_tokens_total",
			Help: "Total LLM tokens consumed by type (input, output).",
		},
		[]string{"type"}, // "input", "output"
	)

	// CostTotal tracks estimated total LLM cost in USD.
	CostTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "llm_cost_dollars_total",
			Help: "Estimated total LLM cost in USD.",
		},
	)

	// CacheHits counts total cache hits.
	CacheHits = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "cache_hits_total",
			Help: "Total number of cache hits.",
		},
	)

	// CacheMisses counts total cache misses.
	CacheMisses = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "cache_misses_total",
			Help: "Total number of cache misses.",
		},
	)

	// AnalysisDurationBySource tracks analysis duration by alert source.
	AnalysisDurationBySource = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "analysis_duration_by_source_seconds",
			Help:    "Duration of alert analysis in seconds by source.",
			Buckets: []float64{1, 2, 5, 10, 20, 30, 60, 120},
		},
		[]string{"source"},
	)

	// AnalysisIterations tracks the number of LLM iterations per analysis.
	AnalysisIterations = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "analysis_iterations",
			Help:    "Number of LLM iterations per analysis.",
			Buckets: []float64{1, 2, 3, 5, 8, 10, 15, 20, 30},
		},
	)

	// MessengerDeliveryTotal counts messenger delivery attempts by messenger and status.
	MessengerDeliveryTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "messenger_delivery_total",
			Help: "Total messenger delivery attempts by messenger and status.",
		},
		[]string{"messenger", "status"}, // status: "success", "error"
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
		TokensTotal,
		CostTotal,
		CacheHits,
		CacheMisses,
		AnalysisDurationBySource,
		AnalysisIterations,
		MessengerDeliveryTotal,
	)
}

// Handler returns an HTTP handler that serves Prometheus metrics.
func Handler() http.Handler {
	return promhttp.Handler()
}
