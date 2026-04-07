package domain

import (
	"testing"
)

func TestFingerprint(t *testing.T) {
	t.Run("deterministic output", func(t *testing.T) {
		tests := []struct {
			name      string
			alertName string
			labels    map[string]string
		}{
			{
				name:      "with labels",
				alertName: "HighCPU",
				labels:    map[string]string{"namespace": "prod", "severity": "critical", "service": "api"},
			},
			{
				name:      "empty labels",
				alertName: "DeadManSwitch",
				labels:    map[string]string{},
			},
			{
				name:      "nil labels",
				alertName: "DeadManSwitch",
				labels:    nil,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				fp1 := Fingerprint(tc.alertName, tc.labels)
				fp2 := Fingerprint(tc.alertName, tc.labels)

				if fp1 != fp2 {
					t.Errorf("non-deterministic: %q != %q", fp1, fp2)
				}
				if len(fp1) != 16 {
					t.Errorf("expected 16 hex chars, got %d: %q", len(fp1), fp1)
				}
			})
		}
	})

	t.Run("excludes ephemeral labels", func(t *testing.T) {
		tests := []struct {
			name           string
			baseLabels     map[string]string
			extendedLabels map[string]string
		}{
			{
				name:       "pod excluded",
				baseLabels: map[string]string{"namespace": "prod"},
				extendedLabels: map[string]string{
					"namespace": "prod",
					"pod":       "api-abc123",
				},
			},
			{
				name:       "instance excluded",
				baseLabels: map[string]string{"namespace": "prod"},
				extendedLabels: map[string]string{
					"namespace": "prod",
					"instance":  "10.0.0.1:8080",
				},
			},
			{
				name:       "pod_name excluded",
				baseLabels: map[string]string{"namespace": "prod"},
				extendedLabels: map[string]string{
					"namespace": "prod",
					"pod_name":  "api-abc123",
				},
			},
			{
				name:       "container_id excluded",
				baseLabels: map[string]string{"namespace": "prod"},
				extendedLabels: map[string]string{
					"namespace":    "prod",
					"container_id": "deadbeef",
				},
			},
			{
				name:       "all ephemeral labels excluded",
				baseLabels: map[string]string{"namespace": "prod", "severity": "critical"},
				extendedLabels: map[string]string{
					"namespace":    "prod",
					"severity":     "critical",
					"pod":          "api-abc123",
					"instance":     "10.0.0.1:8080",
					"pod_name":     "api-abc123",
					"container_id": "deadbeef",
				},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				fp1 := Fingerprint("HighCPU", tc.baseLabels)
				fp2 := Fingerprint("HighCPU", tc.extendedLabels)

				if fp1 != fp2 {
					t.Errorf("ephemeral labels changed fingerprint: %q != %q", fp1, fp2)
				}
			})
		}
	})

	t.Run("different alertnames produce different fingerprints", func(t *testing.T) {
		tests := []struct {
			name   string
			nameA  string
			nameB  string
			labels map[string]string
		}{
			{
				name:   "HighCPU vs HighMemory",
				nameA:  "HighCPU",
				nameB:  "HighMemory",
				labels: map[string]string{"namespace": "prod"},
			},
			{
				name:   "different names with empty labels",
				nameA:  "AlertA",
				nameB:  "AlertB",
				labels: map[string]string{},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				fp1 := Fingerprint(tc.nameA, tc.labels)
				fp2 := Fingerprint(tc.nameB, tc.labels)

				if fp1 == fp2 {
					t.Errorf("different alert names produced same fingerprint: %q", fp1)
				}
			})
		}
	})

	t.Run("empty labels", func(t *testing.T) {
		tests := []struct {
			name   string
			labels map[string]string
		}{
			{name: "nil map", labels: nil},
			{name: "empty map", labels: map[string]string{}},
		}

		fps := make([]string, len(tests))
		for i, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				fp := Fingerprint("TestAlert", tc.labels)
				if len(fp) != 16 {
					t.Errorf("expected 16 hex chars, got %d: %q", len(fp), fp)
				}
				fps[i] = fp
			})
		}

		if fps[0] != fps[1] {
			t.Errorf("nil vs empty labels differ: %q != %q", fps[0], fps[1])
		}
	})

	t.Run("label ordering does not matter", func(t *testing.T) {
		tests := []struct {
			name    string
			labelsA map[string]string
			labelsB map[string]string
		}{
			{
				name:    "two labels reordered",
				labelsA: map[string]string{"a": "1", "b": "2"},
				labelsB: map[string]string{"b": "2", "a": "1"},
			},
			{
				name:    "three labels reordered",
				labelsA: map[string]string{"z": "3", "a": "1", "m": "2"},
				labelsB: map[string]string{"a": "1", "m": "2", "z": "3"},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				fp1 := Fingerprint("TestAlert", tc.labelsA)
				fp2 := Fingerprint("TestAlert", tc.labelsB)

				if fp1 != fp2 {
					t.Errorf("label ordering affected fingerprint: %q != %q", fp1, fp2)
				}
			})
		}
	})
}

func TestBuildRawText(t *testing.T) {
	t.Run("basic output format", func(t *testing.T) {
		tests := []struct {
			name        string
			alertName   string
			severity    string
			labels      map[string]string
			annotations map[string]string
			expected    string
		}{
			{
				name:      "full alert",
				alertName: "HighCPU",
				severity:  "critical",
				labels:    map[string]string{"namespace": "prod", "service": "api"},
				annotations: map[string]string{
					"summary":     "CPU usage is high",
					"description": "CPU above 90%",
				},
				expected: "Alert: HighCPU\n" +
					"Severity: critical\n" +
					"Labels:\n" +
					"  namespace: prod\n" +
					"  service: api\n" +
					"Annotations:\n" +
					"  description: CPU above 90%\n" +
					"  summary: CPU usage is high\n",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				got := BuildRawText(tc.alertName, tc.severity, tc.labels, tc.annotations)
				if got != tc.expected {
					t.Errorf("unexpected output:\ngot:\n%s\nwant:\n%s", got, tc.expected)
				}
			})
		}
	})

	t.Run("empty labels and annotations", func(t *testing.T) {
		tests := []struct {
			name        string
			labels      map[string]string
			annotations map[string]string
			expected    string
		}{
			{
				name:        "both nil",
				labels:      nil,
				annotations: nil,
				expected:    "Alert: TestAlert\nSeverity: warning\n",
			},
			{
				name:        "both empty",
				labels:      map[string]string{},
				annotations: map[string]string{},
				expected:    "Alert: TestAlert\nSeverity: warning\n",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				got := BuildRawText("TestAlert", "warning", tc.labels, tc.annotations)
				if got != tc.expected {
					t.Errorf("unexpected output:\ngot:\n%s\nwant:\n%s", got, tc.expected)
				}
			})
		}
	})

	t.Run("labels and annotations are sorted alphabetically", func(t *testing.T) {
		tests := []struct {
			name        string
			labels      map[string]string
			annotations map[string]string
			expected    string
		}{
			{
				name:   "labels sorted",
				labels: map[string]string{"z_label": "z", "a_label": "a", "m_label": "m"},
				annotations: map[string]string{
					"z_ann": "z",
					"a_ann": "a",
				},
				expected: "Alert: SortTest\n" +
					"Severity: info\n" +
					"Labels:\n" +
					"  a_label: a\n" +
					"  m_label: m\n" +
					"  z_label: z\n" +
					"Annotations:\n" +
					"  a_ann: a\n" +
					"  z_ann: z\n",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				got := BuildRawText("SortTest", "info", tc.labels, tc.annotations)
				if got != tc.expected {
					t.Errorf("unexpected output:\ngot:\n%s\nwant:\n%s", got, tc.expected)
				}
			})
		}
	})

	t.Run("no annotations", func(t *testing.T) {
		tests := []struct {
			name     string
			labels   map[string]string
			expected string
		}{
			{
				name:   "labels only",
				labels: map[string]string{"env": "prod"},
				expected: "Alert: NoAnnotations\n" +
					"Severity: warning\n" +
					"Labels:\n" +
					"  env: prod\n",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				got := BuildRawText("NoAnnotations", "warning", tc.labels, nil)
				if got != tc.expected {
					t.Errorf("unexpected output:\ngot:\n%s\nwant:\n%s", got, tc.expected)
				}
			})
		}
	})
}
