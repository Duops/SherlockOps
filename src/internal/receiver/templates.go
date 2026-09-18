package receiver

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"text/template"

	"github.com/Duops/SherlockOps/internal/domain"
)

// LabelTemplates derives the environment and the Slack channel from alert
// labels when the corresponding webhook headers are absent.
type LabelTemplates struct {
	env   *template.Template
	slack *template.Template
}

type templateData struct {
	Name        string
	Status      string
	Severity    string
	Labels      map[string]string
	Annotations map[string]string
}

var templateFuncs = template.FuncMap{
	"default": func(def string, val string) string {
		if val == "" {
			return def
		}
		return val
	},
	"lower":      strings.ToLower,
	"upper":      strings.ToUpper,
	"replace":    strings.ReplaceAll,
	"hasPrefix":  strings.HasPrefix,
	"trimPrefix": strings.TrimPrefix,
}

// NewLabelTemplates parses the templates; both empty yields nil.
func NewLabelTemplates(envTmpl, slackTmpl string) (*LabelTemplates, error) {
	envTmpl, slackTmpl = strings.TrimSpace(envTmpl), strings.TrimSpace(slackTmpl)
	if envTmpl == "" && slackTmpl == "" {
		return nil, nil
	}
	lt := &LabelTemplates{}
	var err error
	if envTmpl != "" {
		if lt.env, err = template.New("environment").Funcs(templateFuncs).Option("missingkey=zero").Parse(envTmpl); err != nil {
			return nil, fmt.Errorf("environment_template: %w", err)
		}
	}
	if slackTmpl != "" {
		if lt.slack, err = template.New("slack_channel").Funcs(templateFuncs).Option("missingkey=zero").Parse(slackTmpl); err != nil {
			return nil, fmt.Errorf("channel_template: %w", err)
		}
	}
	return lt, nil
}

// Apply fills Environment and the Slack channel override for alerts that did not get them from headers.
func (lt *LabelTemplates) Apply(alerts []domain.Alert, logger *slog.Logger) {
	if lt == nil {
		return
	}
	for i := range alerts {
		a := &alerts[i]
		data := templateData{
			Name: a.Name, Status: string(a.Status), Severity: string(a.Severity),
			Labels: a.Labels, Annotations: a.Annotations,
		}
		if data.Labels == nil {
			data.Labels = map[string]string{}
		}
		if data.Annotations == nil {
			data.Annotations = map[string]string{}
		}

		if lt.env != nil && a.Environment == "" {
			env, err := render(lt.env, data)
			switch {
			case err != nil:
				logger.Warn("environment_template failed", "alert", a.Name, "error", err)
			case env != "" && !validEnvironment(env):
				logger.Warn("environment_template produced invalid value", "alert", a.Name, "value", env)
			default:
				a.Environment = env
			}
		}

		if lt.slack != nil && a.ChannelOverrides["slack"] == "" {
			channel, err := render(lt.slack, data)
			if err != nil {
				logger.Warn("channel_template failed", "alert", a.Name, "error", err)
				continue
			}
			if channel == "" {
				continue
			}
			overrides := make(map[string]string, len(a.ChannelOverrides)+1)
			for k, v := range a.ChannelOverrides {
				overrides[k] = v
			}
			overrides["slack"] = channel
			a.ChannelOverrides = overrides
		}
	}
}

func render(t *template.Template, data templateData) (string, error) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

func validEnvironment(env string) bool {
	if len(env) > 64 {
		return false
	}
	for _, c := range env {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
