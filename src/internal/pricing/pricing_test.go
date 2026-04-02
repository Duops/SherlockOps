package pricing

import (
	"math"
	"testing"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		model     string
		wantInput float64
		wantOK    bool
	}{
		{"claude-sonnet-4-6", 3.0, true},
		{"claude-opus-4-6", 15.0, true},
		{"claude-haiku-4-5", 0.80, true},
		{"gpt-4o-mini-2024-07-18", 0.15, true},  // must NOT match gpt-4o ($2.50)
		{"gpt-4o", 2.50, true},                    // must NOT match gpt-4 ($30)
		{"gpt-4-turbo-preview", 10.0, true},       // must NOT match gpt-4 ($30)
		{"gpt-4", 30.0, true},
		{"gpt-3.5-turbo", 0.50, true},
		{"deepseek-chat", 0.27, true},
		{"CLAUDE-SONNET-4-6", 3.0, true},          // case insensitive
		{"llama-3-70b", 0, false},                  // unknown
		{"", 0, false},                             // empty
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			p, ok := Lookup(tt.model)
			if ok != tt.wantOK {
				t.Fatalf("Lookup(%q) ok = %v, want %v", tt.model, ok, tt.wantOK)
			}
			if ok && p.Input != tt.wantInput {
				t.Errorf("Lookup(%q).Input = %v, want %v", tt.model, p.Input, tt.wantInput)
			}
		})
	}
}

func TestEstimateCost(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		input      int
		output     int
		cfgIn      float64
		cfgOut     float64
		wantApprox float64
	}{
		{"zero tokens", "claude-sonnet-4-6", 0, 0, 0, 0, 0},
		{"unknown model no config", "llama-3", 1000, 1000, 0, 0, 0},
		{"sonnet auto", "claude-sonnet-4-6", 80000, 2000, 0, 0, 0.24 + 0.03},
		{"config override", "claude-sonnet-4-6", 1_000_000, 0, 5.0, 20.0, 5.0},
		{"config takes precedence", "gpt-4o-mini", 1_000_000, 1_000_000, 1.0, 2.0, 3.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateCost(tt.model, tt.input, tt.output, tt.cfgIn, tt.cfgOut)
			if math.Abs(got-tt.wantApprox) > 0.001 {
				t.Errorf("EstimateCost() = %v, want ~%v", got, tt.wantApprox)
			}
		})
	}
}

func TestFormatCost(t *testing.T) {
	tests := []struct {
		cost float64
		want string
	}{
		{0, ""},
		{0.0001, "<$0.001"},
		{0.042, "$0.042"},
		{1.5, "$1.500"},
	}

	for _, tt := range tests {
		got := FormatCost(tt.cost)
		if got != tt.want {
			t.Errorf("FormatCost(%v) = %q, want %q", tt.cost, got, tt.want)
		}
	}
}
