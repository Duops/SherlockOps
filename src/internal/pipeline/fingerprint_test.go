package pipeline

import (
	"testing"
)

func TestFingerprintDeterministic(t *testing.T) {
	labels := map[string]string{
		"namespace": "prod",
		"severity":  "critical",
		"service":   "api",
	}

	fp1 := Fingerprint("HighCPU", labels)
	fp2 := Fingerprint("HighCPU", labels)

	if fp1 != fp2 {
		t.Errorf("non-deterministic: %q != %q", fp1, fp2)
	}
	if len(fp1) != 16 {
		t.Errorf("expected 16 hex chars, got %d: %q", len(fp1), fp1)
	}
}

func TestFingerprintExcludesEphemeralLabels(t *testing.T) {
	base := map[string]string{
		"namespace": "prod",
		"severity":  "critical",
	}

	withEphemeral := map[string]string{
		"namespace":    "prod",
		"severity":     "critical",
		"pod":          "api-abc123",
		"instance":     "10.0.0.1:8080",
		"pod_name":     "api-abc123",
		"container_id": "deadbeef",
	}

	fp1 := Fingerprint("HighCPU", base)
	fp2 := Fingerprint("HighCPU", withEphemeral)

	if fp1 != fp2 {
		t.Errorf("ephemeral labels changed fingerprint: %q != %q", fp1, fp2)
	}
}

func TestFingerprintEmptyLabels(t *testing.T) {
	fp := Fingerprint("DeadManSwitch", nil)
	if len(fp) != 16 {
		t.Errorf("expected 16 hex chars, got %d: %q", len(fp), fp)
	}

	fp2 := Fingerprint("DeadManSwitch", map[string]string{})
	if fp != fp2 {
		t.Errorf("nil vs empty labels differ: %q != %q", fp, fp2)
	}
}

func TestFingerprintDifferentNames(t *testing.T) {
	labels := map[string]string{"namespace": "prod"}

	fp1 := Fingerprint("HighCPU", labels)
	fp2 := Fingerprint("HighMemory", labels)

	if fp1 == fp2 {
		t.Errorf("different alert names produced same fingerprint: %q", fp1)
	}
}

func TestFingerprintDifferentLabelValues(t *testing.T) {
	fp1 := Fingerprint("HighCPU", map[string]string{"namespace": "prod"})
	fp2 := Fingerprint("HighCPU", map[string]string{"namespace": "staging"})

	if fp1 == fp2 {
		t.Errorf("different label values produced same fingerprint: %q", fp1)
	}
}
