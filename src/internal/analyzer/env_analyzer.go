package analyzer

import (
	"context"
	"log/slog"

	"github.com/Duops/SherlockOps/internal/tooling"
	"github.com/Duops/SherlockOps/internal/domain"
)

// EnvAnalyzer selects the correct tool registry and system prompt based on the
// alert's Environment field, then delegates to a standard Analyzer.
type EnvAnalyzer struct {
	llm           domain.LLMProvider
	envRegistry   *tooling.EnvRegistry
	runbooks      domain.RunbookMatcher
	systemPrompts          map[string]string // per-env system prompts
	defaultPrompt          string
	language               string
	maxIterations          int
	inputTokenCost         float64
	outputTokenCost        float64
	maxToolOutputChars     int
	contextSoftLimitTokens int
	logger                 *slog.Logger
}

// NewEnvAnalyzer creates an environment-aware analyzer.
func NewEnvAnalyzer(
	llm domain.LLMProvider,
	envRegistry *tooling.EnvRegistry,
	defaultPrompt, language string,
	maxIterations int,
	logger *slog.Logger,
) *EnvAnalyzer {
	if logger == nil {
		logger = slog.Default()
	}
	return &EnvAnalyzer{
		llm:           llm,
		envRegistry:   envRegistry,
		systemPrompts: make(map[string]string),
		defaultPrompt: defaultPrompt,
		language:      language,
		maxIterations: maxIterations,
		logger:        logger,
	}
}

// SetSystemPrompt sets a per-environment system prompt override.
func (a *EnvAnalyzer) SetSystemPrompt(env, prompt string) {
	a.systemPrompts[env] = prompt
}

// SetTokenCost sets per-token pricing from config.
func (a *EnvAnalyzer) SetTokenCost(inputCost, outputCost float64) {
	a.inputTokenCost = inputCost
	a.outputTokenCost = outputCost
}

// SetContextLimits configures the per-tool-result truncation cap and the
// running context soft-limit that stops analysis early before the provider
// hard cap is hit.
func (a *EnvAnalyzer) SetContextLimits(maxToolOutputChars, contextSoftLimitTokens int) {
	a.maxToolOutputChars = maxToolOutputChars
	a.contextSoftLimitTokens = contextSoftLimitTokens
}

// SetRunbookStore attaches a runbook store that will be passed to inner analyzers.
func (a *EnvAnalyzer) SetRunbookStore(store domain.RunbookMatcher) {
	a.runbooks = store
}

// Analyze picks the tool registry and system prompt for the alert's environment,
// creates a temporary Analyzer, and delegates the analysis.
func (a *EnvAnalyzer) Analyze(ctx context.Context, alert *domain.Alert) (*domain.AnalysisResult, error) {
	reg := a.envRegistry.GetRegistry(alert.Environment)

	prompt := a.defaultPrompt
	if p, ok := a.systemPrompts[alert.Environment]; ok && p != "" {
		prompt = p
	}

	a.logger.Debug("env analyzer routing",
		"alert", alert.Name,
		"environment", alert.Environment,
		"prompt_override", prompt != a.defaultPrompt,
	)

	inner := New(a.llm, reg, prompt, a.language, a.maxIterations, a.logger)
	inner.SetNameResolver(reg.DisplayName)
	inner.SetTokenCost(a.inputTokenCost, a.outputTokenCost)
	if a.maxToolOutputChars > 0 {
		inner.SetMaxToolOutputChars(a.maxToolOutputChars)
	}
	if a.contextSoftLimitTokens > 0 {
		inner.SetContextSoftLimitTokens(a.contextSoftLimitTokens)
	}
	if a.runbooks != nil {
		inner.SetRunbookStore(a.runbooks)
	}
	return inner.Analyze(ctx, alert)
}
