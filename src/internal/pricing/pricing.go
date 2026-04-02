package pricing

import (
	"fmt"
	"strings"
)

// ModelPricing holds input/output price per 1M tokens.
type ModelPricing struct {
	Input  float64 // USD per 1M input tokens
	Output float64 // USD per 1M output tokens
}

// Known maps model name prefixes to their pricing.
// Prices in USD per 1M tokens (as of 2026).
var Known = map[string]ModelPricing{
	// Anthropic Claude
	"claude-opus":   {Input: 15.0, Output: 75.0},
	"claude-sonnet": {Input: 3.0, Output: 15.0},
	"claude-haiku":  {Input: 0.80, Output: 4.0},
	// OpenAI
	"gpt-4o":      {Input: 2.50, Output: 10.0},
	"gpt-4o-mini": {Input: 0.15, Output: 0.60},
	"gpt-4-turbo": {Input: 10.0, Output: 30.0},
	"gpt-4":       {Input: 30.0, Output: 60.0},
	"gpt-3.5":     {Input: 0.50, Output: 1.50},
	// DeepSeek
	"deepseek": {Input: 0.27, Output: 1.10},
}

// Lookup finds pricing by longest matching model name prefix.
// Longest match wins to avoid "gpt-4" matching before "gpt-4o-mini".
func Lookup(model string) (ModelPricing, bool) {
	model = strings.ToLower(model)
	var bestPrefix string
	var bestPricing ModelPricing
	for prefix, p := range Known {
		if strings.HasPrefix(model, prefix) && len(prefix) > len(bestPrefix) {
			bestPrefix = prefix
			bestPricing = p
		}
	}
	if bestPrefix == "" {
		return ModelPricing{}, false
	}
	return bestPricing, true
}

// EstimateCost returns estimated cost in USD.
// Uses cfgInput/cfgOutput if > 0, otherwise falls back to model name lookup.
// Returns 0 if pricing unknown or tokens are zero.
func EstimateCost(model string, inputTokens, outputTokens int, cfgInput, cfgOutput float64) float64 {
	if inputTokens == 0 && outputTokens == 0 {
		return 0
	}
	var ip, op float64
	if cfgInput > 0 || cfgOutput > 0 {
		ip, op = cfgInput, cfgOutput
	} else {
		p, ok := Lookup(model)
		if !ok {
			return 0
		}
		ip, op = p.Input, p.Output
	}
	return float64(inputTokens)/1_000_000*ip + float64(outputTokens)/1_000_000*op
}

// FormatCost returns cost as human-readable string: "$0.042", "<$0.001", or "".
func FormatCost(cost float64) string {
	if cost <= 0 {
		return ""
	}
	if cost < 0.001 {
		return "<$0.001"
	}
	return fmt.Sprintf("$%.3f", cost)
}
